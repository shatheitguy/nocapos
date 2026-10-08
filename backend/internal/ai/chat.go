package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"alfaos/alfad/internal/store"
)

// systemPrompt is the base instruction, with the user's saved memory appended.
func (s *Service) systemPrompt(ctx context.Context, userID, username string) string {
	base := fmt.Sprintf("You are NoCap, the assistant built into NoCapOS, a self-hosted server operating system. "+
		"You are helping %s. Be concise and practical. Use plain text unless asked for code or a list.", username)
	on, err := s.st.AIMemoryEnabled(ctx, userID)
	if err != nil || !on {
		return base
	}
	mems, err := s.st.ListAIMemories(ctx, userID)
	if err != nil || len(mems) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\nThings you remember about ")
	b.WriteString(username)
	b.WriteString(":")
	for _, m := range mems {
		b.WriteString("\n- ")
		b.WriteString(m.Content)
	}
	return b.String()
}

// ---------- conversations ----------

func (s *Service) ListConversations(ctx context.Context, userID, query string) ([]store.AIConversation, error) {
	return s.st.ListAIConversations(ctx, userID, query, 200)
}

type ConversationDetail struct {
	store.AIConversation
	Messages []store.AIMessage `json:"messages"`
}

func (s *Service) Conversation(ctx context.Context, userID, id string) (*ConversationDetail, error) {
	c, err := s.st.AIConversationByID(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	msgs, err := s.st.ListAIMessages(ctx, id)
	if err != nil {
		return nil, err
	}
	return &ConversationDetail{AIConversation: *c, Messages: msgs}, nil
}

func (s *Service) CreateConversation(ctx context.Context, userID, providerID, model string) (*store.AIConversation, error) {
	now := time.Now()
	c := &store.AIConversation{
		ID: store.NewID(), UserID: userID, ProviderID: providerID, Model: strings.TrimSpace(model),
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.st.InsertAIConversation(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Service) RenameConversation(ctx context.Context, userID, id, title string) error {
	c, err := s.st.AIConversationByID(ctx, userID, id)
	if err != nil {
		return err
	}
	c.Title = truncate(strings.TrimSpace(title), 200)
	c.UpdatedAt = time.Now()
	return s.st.UpdateAIConversation(ctx, c)
}

func (s *Service) SetPinned(ctx context.Context, userID, id string, pinned bool) error {
	c, err := s.st.AIConversationByID(ctx, userID, id)
	if err != nil {
		return err
	}
	c.Pinned = pinned
	c.UpdatedAt = time.Now()
	return s.st.UpdateAIConversation(ctx, c)
}

func (s *Service) DeleteConversation(ctx context.Context, userID, id string) error {
	return s.st.DeleteAIConversation(ctx, userID, id)
}

// ---------- sending ----------

type SendOptions struct {
	UserID         string
	Username       string
	ConversationID string
	Text           string
}

// lockConversation prevents two replies generating in the same conversation.
func (s *Service) lockConversation(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy[id] {
		return false
	}
	s.busy[id] = true
	return true
}

func (s *Service) unlockConversation(id string) {
	s.mu.Lock()
	delete(s.busy, id)
	s.mu.Unlock()
}

// Send runs one user turn against the conversation's provider, streaming the
// reply through onDelta, and saves both messages. It returns the stored
// assistant message.
func (s *Service) Send(ctx context.Context, opt SendOptions, onDelta func(string)) (*store.AIMessage, error) {
	text := strings.TrimSpace(opt.Text)
	if text == "" {
		return nil, fmt.Errorf("%w: empty message", ErrInvalidInput)
	}
	if len(text) > maxUserMessage {
		return nil, fmt.Errorf("%w: message is too long", ErrInvalidInput)
	}
	conv, err := s.st.AIConversationByID(ctx, opt.UserID, opt.ConversationID)
	if err != nil {
		return nil, err
	}
	if !s.lockConversation(conv.ID) {
		return nil, ErrBusy
	}
	defer s.unlockConversation(conv.ID)

	provider, err := s.resolveProvider(ctx, conv.ProviderID)
	if err != nil {
		return nil, err
	}
	cl, err := s.client(provider)
	if err != nil {
		return nil, err
	}
	model := s.pickModel(provider, conv.Model)

	history, err := s.st.ListAIMessages(ctx, conv.ID)
	if err != nil {
		return nil, err
	}

	// Persist the user message first so a failed generation still shows it.
	now := time.Now()
	userMsg := &store.AIMessage{ConversationID: conv.ID, Role: "user", Content: text, CreatedAt: now}
	if err := s.st.InsertAIMessage(ctx, userMsg); err != nil {
		return nil, err
	}
	if conv.Title == "" {
		conv.Title = firstLine(text, 60)
	}
	conv.ProviderID = provider.ID
	conv.Model = model
	conv.UpdatedAt = now
	_ = s.st.UpdateAIConversation(ctx, conv)

	system := s.systemPrompt(ctx, opt.UserID, opt.Username)
	msgs := buildMessages(history, text, system, provider.ContextSize)

	var sb strings.Builder
	result, genErr := cl.Chat(ctx, ChatRequest{
		Model:       model,
		System:      system,
		Messages:    msgs,
		ContextSize: provider.ContextSize,
		KeepAlive:   provider.KeepAlive,
	}, func(delta string) {
		sb.WriteString(delta)
		onDelta(delta)
	})

	reply := sb.String()
	asst := &store.AIMessage{
		ConversationID: conv.ID, Role: "assistant", Content: reply, Model: model,
		TokensIn: result.Usage.InputTokens, TokensOut: result.Usage.OutputTokens, CreatedAt: time.Now(),
	}
	if genErr != nil && reply == "" {
		asst.Error = genErr.Error()
	} else if isRefusal(result.StopReason) {
		asst.Error = "The model declined to answer this request."
	}
	if err := s.st.InsertAIMessage(ctx, asst); err != nil {
		return nil, err
	}
	conv.UpdatedAt = time.Now()
	_ = s.st.UpdateAIConversation(ctx, conv)

	if genErr != nil && reply == "" {
		return asst, genErr
	}
	return asst, nil
}

// buildMessages keeps the newest history that fits the model's context window,
// reserving room for the system prompt and the reply.
func buildMessages(history []store.AIMessage, newText, system string, contextSize int) []Message {
	budget := contextSize
	if budget <= 0 {
		budget = 8192
	}
	// Leave room for the system prompt and the model's answer.
	budget = budget - EstimateTokens(system) - 1024
	if budget < 512 {
		budget = 512
	}

	type turn struct {
		msgs   []Message
		tokens int
	}
	var turns []turn
	for _, m := range history {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		if m.Content == "" {
			continue
		}
		turns = append(turns, turn{
			msgs:   []Message{{Role: m.Role, Content: m.Content}},
			tokens: EstimateTokens(m.Content),
		})
	}

	// Walk backwards, keeping what fits.
	kept := []Message{{Role: "user", Content: newText}}
	used := EstimateTokens(newText)
	for i := len(turns) - 1; i >= 0; i-- {
		if used+turns[i].tokens > budget {
			break
		}
		used += turns[i].tokens
		kept = append(turns[i].msgs, kept...)
	}
	return kept
}

func isRefusal(stopReason string) bool {
	switch stopReason {
	case "refusal", "content_filter":
		return true
	}
	return false
}

func firstLine(s string, n int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return truncate(strings.TrimSpace(s), n)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// ---------- memory ----------

func (s *Service) Memories(ctx context.Context, userID string) ([]store.AIMemory, error) {
	return s.st.ListAIMemories(ctx, userID)
}

func (s *Service) MemoryEnabled(ctx context.Context, userID string) (bool, error) {
	return s.st.AIMemoryEnabled(ctx, userID)
}

func (s *Service) SetMemoryEnabled(ctx context.Context, userID string, enabled bool) error {
	return s.st.SetAIMemoryEnabled(ctx, userID, enabled)
}

func (s *Service) AddMemory(ctx context.Context, userID, content string) (*store.AIMemory, error) {
	content = strings.TrimSpace(content)
	if content == "" || len([]rune(content)) > maxMemoryLen {
		return nil, fmt.Errorf("%w: memory must be 1-%d characters", ErrInvalidInput, maxMemoryLen)
	}
	n, err := s.st.CountAIMemories(ctx, userID)
	if err != nil {
		return nil, err
	}
	if n >= maxMemoryItems {
		return nil, fmt.Errorf("%w: at most %d memories", ErrInvalidInput, maxMemoryItems)
	}
	now := time.Now()
	m := &store.AIMemory{ID: store.NewID(), UserID: userID, Content: content, CreatedAt: now, UpdatedAt: now}
	if err := s.st.InsertAIMemory(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *Service) UpdateMemory(ctx context.Context, userID, id, content string) error {
	content = strings.TrimSpace(content)
	if content == "" || len([]rune(content)) > maxMemoryLen {
		return fmt.Errorf("%w: memory must be 1-%d characters", ErrInvalidInput, maxMemoryLen)
	}
	return s.st.UpdateAIMemory(ctx, userID, id, content, time.Now())
}

func (s *Service) DeleteMemory(ctx context.Context, userID, id string) error {
	return s.st.DeleteAIMemory(ctx, userID, id)
}
