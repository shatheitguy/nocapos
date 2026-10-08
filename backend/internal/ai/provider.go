// Package ai runs the AI Assistant: provider clients (local Ollama, Anthropic,
// OpenAI-compatible APIs), chat orchestration with saved history, context
// window management and long-term memory.
package ai

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	KindOllama    = "ollama"
	KindAnthropic = "anthropic"
	KindOpenAI    = "openai"
)

type Message struct {
	Role    string // user | assistant
	Content string
}

type ChatRequest struct {
	Model       string
	System      string
	Messages    []Message
	ContextSize int    // Ollama num_ctx
	KeepAlive   string // Ollama keep_alive
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type ChatResult struct {
	Usage      Usage
	StopReason string // "refusal" when the model declined
}

// ModelInfo describes a model, and for local runtimes whether it is in RAM.
type ModelInfo struct {
	ID            string     `json:"id"`
	Name          string     `json:"name,omitempty"`
	Size          int64      `json:"size,omitempty"`           // on disk
	ContextWindow int        `json:"context_window,omitempty"` // model maximum, tokens
	Loaded        bool       `json:"loaded,omitempty"`
	RAM           int64      `json:"ram,omitempty"`  // total memory in use when loaded
	VRAM          int64      `json:"vram,omitempty"` // portion on the GPU
	ContextInUse  int        `json:"context_in_use,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"` // when an idle model unloads
}

type Provider interface {
	Chat(ctx context.Context, req ChatRequest, onDelta func(string)) (ChatResult, error)
	Models(ctx context.Context) ([]ModelInfo, error)
}

// MemoryManager is implemented by local runtimes that keep models in RAM.
type MemoryManager interface {
	Running(ctx context.Context) ([]ModelInfo, error)
	Load(ctx context.Context, model, keepAlive string, contextSize int) error
	Unload(ctx context.Context, model string) error
}

// KeepAlive presets offered in the UI. "-1" keeps a model loaded until Ollama restarts.
var KeepAliveOptions = []string{"30m", "2h", "4h", "-1"}

const (
	DefaultKeepAlive        = "4h"
	DefaultOllamaContext    = 8192
	DefaultOpenAIContext    = 128000
	DefaultAnthropicContext = 200000
	DefaultAnthropicModel   = "claude-opus-5-5"
)

// ---------- secret box for API keys ----------

// SecretBox seals API keys with AES-256-GCM so a copied database alone does
// not reveal them. The key lives in <data>/secrets/ai.key.
type SecretBox struct{ aead cipher.AEAD }

func NewSecretBox(key []byte) (*SecretBox, error) {
	if len(key) < 32 {
		return nil, errors.New("secret box key must be at least 32 bytes")
	}
	block, err := aes.NewCipher(key[:32])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &SecretBox{aead: aead}, nil
}

func (b *SecretBox) Seal(plain string) []byte {
	if plain == "" {
		return nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	_, _ = rand.Read(nonce)
	return b.aead.Seal(nonce, nonce, []byte(plain), []byte("alfa-ai-key"))
}

func (b *SecretBox) Open(sealed []byte) (string, error) {
	if len(sealed) == 0 {
		return "", nil
	}
	n := b.aead.NonceSize()
	if len(sealed) < n {
		return "", errors.New("sealed key too short")
	}
	plain, err := b.aead.Open(nil, sealed[:n], sealed[n:], []byte("alfa-ai-key"))
	if err != nil {
		return "", fmt.Errorf("decrypt API key: %w", err)
	}
	return string(plain), nil
}

// ---------- helpers ----------

// EstimateTokens is a rough count (≈3.5 characters per token) used when a
// provider does not report usage and for trimming history to fit.
func EstimateTokens(s string) int {
	return (len([]rune(s))*2 + 6) / 7
}

func trimBase(u string) string { return strings.TrimRight(strings.TrimSpace(u), "/") }

// apiError extracts a readable message from common error JSON shapes.
func apiError(status int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	for _, key := range []string{`"message":"`, `"error":"`} {
		if i := strings.Index(msg, key); i >= 0 {
			rest := msg[i+len(key):]
			if j := strings.Index(rest, `"`); j > 0 {
				msg = rest[:j]
				break
			}
		}
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	if msg == "" {
		msg = fmt.Sprintf("HTTP %d", status)
	}
	return fmt.Errorf("provider error (HTTP %d): %s", status, msg)
}
