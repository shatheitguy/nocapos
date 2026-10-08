package ai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/store"
)

var (
	ErrNoProvider    = errors.New("no AI provider configured — an administrator can add one in the AI Assistant")
	ErrBusy          = errors.New("this conversation is already generating a reply")
	ErrNotLocal      = errors.New("only local Ollama providers keep models in memory")
	ErrInvalidInput  = errors.New("invalid input")
	ErrNothingToRedo = errors.New("there is no answer to regenerate")
)

const (
	maxUserMessage = 100_000 // characters
	maxMemoryItems = 200
	maxMemoryLen   = 1000
	modelCacheTTL  = 5 * time.Minute
)

type ProviderView struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	BaseURL      string `json:"base_url"`
	HasKey       bool   `json:"has_key"`
	DefaultModel string `json:"default_model"`
	KeepAlive    string `json:"keep_alive,omitempty"`
	ContextSize  int    `json:"context_size,omitempty"`
}

type ProviderInput struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"api_key"`   // empty on update = keep the stored key
	ClearKey     bool   `json:"clear_key"` // remove the stored key
	DefaultModel string `json:"default_model"`
	KeepAlive    string `json:"keep_alive"`
	ContextSize  int    `json:"context_size"`
}

type cachedModels struct {
	at     time.Time
	models []ModelInfo
}

type Service struct {
	st  *store.Store
	box *SecretBox
	log *slog.Logger

	mu     sync.Mutex
	busy   map[string]bool
	models map[string]cachedModels
}

func NewService(st *store.Store, box *SecretBox, log *slog.Logger) *Service {
	return &Service{st: st, box: box, log: log, busy: map[string]bool{}, models: map[string]cachedModels{}}
}

// ---------- providers ----------

func view(p *store.AIProvider) ProviderView {
	return ProviderView{
		ID: p.ID, Name: p.Name, Kind: p.Kind, BaseURL: p.BaseURL, HasKey: len(p.APIKeyEnc) > 0,
		DefaultModel: p.DefaultModel, KeepAlive: p.KeepAlive, ContextSize: p.ContextSize,
	}
}

func (s *Service) Providers(ctx context.Context) ([]ProviderView, error) {
	ps, err := s.st.ListAIProviders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ProviderView, 0, len(ps))
	for i := range ps {
		out = append(out, view(&ps[i]))
	}
	return out, nil
}

// SaveProvider creates (id == "") or updates a provider.
func (s *Service) SaveProvider(ctx context.Context, id string, in ProviderInput) (*ProviderView, error) {
	in.Name, in.BaseURL, in.DefaultModel = strings.TrimSpace(in.Name), strings.TrimSpace(in.BaseURL), strings.TrimSpace(in.DefaultModel)
	switch in.Kind {
	case KindOllama, KindAnthropic, KindOpenAI:
	default:
		return nil, fmt.Errorf("%w: kind must be ollama, anthropic or openai", ErrInvalidInput)
	}
	if in.Name == "" || len(in.Name) > 80 {
		return nil, fmt.Errorf("%w: name is required (max 80 characters)", ErrInvalidInput)
	}
	if in.Kind == KindOpenAI && in.BaseURL == "" {
		return nil, fmt.Errorf("%w: an OpenAI-compatible provider needs a base URL", ErrInvalidInput)
	}
	if in.BaseURL != "" && !strings.HasPrefix(in.BaseURL, "http://") && !strings.HasPrefix(in.BaseURL, "https://") {
		return nil, fmt.Errorf("%w: base URL must start with http:// or https://", ErrInvalidInput)
	}
	if in.Kind == KindOllama {
		if in.KeepAlive == "" {
			in.KeepAlive = DefaultKeepAlive
		}
		if !ValidKeepAlive(in.KeepAlive) {
			return nil, fmt.Errorf("%w: keep_alive must be a duration like 4h, or -1 for always", ErrInvalidInput)
		}
	} else {
		in.KeepAlive = ""
	}
	if in.ContextSize < 0 || in.ContextSize > 2_000_000 {
		return nil, fmt.Errorf("%w: context_size out of range", ErrInvalidInput)
	}

	now := time.Now()
	var p *store.AIProvider
	if id == "" {
		p = &store.AIProvider{ID: store.NewID(), CreatedAt: now}
	} else {
		existing, err := s.st.AIProviderByID(ctx, id)
		if err != nil {
			return nil, err
		}
		p = existing
	}
	p.Name, p.Kind, p.BaseURL, p.DefaultModel, p.KeepAlive, p.ContextSize, p.UpdatedAt =
		in.Name, in.Kind, in.BaseURL, in.DefaultModel, in.KeepAlive, in.ContextSize, now
	if in.ClearKey {
		p.APIKeyEnc = nil
	}
	if k := strings.TrimSpace(in.APIKey); k != "" {
		p.APIKeyEnc = s.box.Seal(k)
	}
	if p.Kind == KindAnthropic && len(p.APIKeyEnc) == 0 {
		return nil, fmt.Errorf("%w: Anthropic needs an API key", ErrInvalidInput)
	}
	var err error
	if id == "" {
		err = s.st.InsertAIProvider(ctx, p)
	} else {
		err = s.st.UpdateAIProvider(ctx, p)
	}
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	delete(s.models, p.ID)
	s.mu.Unlock()
	v := view(p)
	return &v, nil
}

func (s *Service) DeleteProvider(ctx context.Context, id string) error {
	if _, err := s.st.AIProviderByID(ctx, id); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.models, id)
	s.mu.Unlock()
	return s.st.DeleteAIProvider(ctx, id)
}

// resolveProvider returns the provider for id, or the first configured one when
// id is empty.
func (s *Service) resolveProvider(ctx context.Context, id string) (*store.AIProvider, error) {
	if id != "" {
		return s.st.AIProviderByID(ctx, id)
	}
	ps, err := s.st.ListAIProviders(ctx)
	if err != nil {
		return nil, err
	}
	if len(ps) == 0 {
		return nil, ErrNoProvider
	}
	return &ps[0], nil
}

// client builds a provider client, decrypting the API key.
func (s *Service) client(p *store.AIProvider) (Provider, error) {
	key, err := s.box.Open(p.APIKeyEnc)
	if err != nil {
		return nil, err
	}
	switch p.Kind {
	case KindOllama:
		return newOllama(p.BaseURL), nil
	case KindAnthropic:
		return newAnthropic(p.BaseURL, key), nil
	case KindOpenAI:
		return newOpenAICompat(p.BaseURL, key), nil
	default:
		return nil, fmt.Errorf("unknown provider kind %q", p.Kind)
	}
}

func (s *Service) pickModel(p *store.AIProvider, requested string) string {
	if m := strings.TrimSpace(requested); m != "" {
		return m
	}
	if p.DefaultModel != "" {
		return p.DefaultModel
	}
	switch p.Kind {
	case KindAnthropic:
		return DefaultAnthropicModel
	default:
		return ""
	}
}

// Models lists a provider's models, cached briefly so the UI can poll.
func (s *Service) Models(ctx context.Context, providerID string) ([]ModelInfo, error) {
	p, err := s.resolveProvider(ctx, providerID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if c, ok := s.models[p.ID]; ok && time.Since(c.at) < modelCacheTTL {
		s.mu.Unlock()
		return c.models, nil
	}
	s.mu.Unlock()

	cl, err := s.client(p)
	if err != nil {
		return nil, err
	}
	models, err := cl.Models(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.models[p.ID] = cachedModels{at: time.Now(), models: models}
	s.mu.Unlock()
	return models, nil
}

// RunningModels reports which models a local provider currently holds in RAM,
// with how long until they unload — the "loaded for 2-4 h" view.
func (s *Service) RunningModels(ctx context.Context, providerID string) ([]ModelInfo, error) {
	p, err := s.resolveProvider(ctx, providerID)
	if err != nil {
		return nil, err
	}
	cl, err := s.client(p)
	if err != nil {
		return nil, err
	}
	mm, ok := cl.(MemoryManager)
	if !ok {
		return nil, ErrNotLocal
	}
	return mm.Running(ctx)
}

// LoadModel preloads a model into RAM and keeps it for the provider's keep-alive.
func (s *Service) LoadModel(ctx context.Context, providerID, model string) error {
	p, err := s.resolveProvider(ctx, providerID)
	if err != nil {
		return err
	}
	cl, err := s.client(p)
	if err != nil {
		return err
	}
	mm, ok := cl.(MemoryManager)
	if !ok {
		return ErrNotLocal
	}
	if model = strings.TrimSpace(model); model == "" {
		model = p.DefaultModel
	}
	if model == "" {
		return fmt.Errorf("%w: no model given", ErrInvalidInput)
	}
	return mm.Load(ctx, model, p.KeepAlive, p.ContextSize)
}

func (s *Service) UnloadModel(ctx context.Context, providerID, model string) error {
	p, err := s.resolveProvider(ctx, providerID)
	if err != nil {
		return err
	}
	cl, err := s.client(p)
	if err != nil {
		return err
	}
	mm, ok := cl.(MemoryManager)
	if !ok {
		return ErrNotLocal
	}
	return mm.Unload(ctx, strings.TrimSpace(model))
}
