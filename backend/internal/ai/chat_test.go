package ai

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"alfaos/alfad/internal/store"
)

func testService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Now()
	for _, id := range []string{"u1", "u2"} {
		if err := st.CreateUser(ctx, &store.User{ID: id, Username: id, PasswordHash: "x", Role: "user", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	box, err := NewSecretBox(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return NewService(st, box, slog.New(slog.NewTextHandler(io.Discard, nil))), st
}

func TestDeleteMessagesFrom(t *testing.T) {
	ctx := context.Background()
	s, st := testService(t)
	c, err := s.CreateConversation(ctx, "u1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i, role := range []string{"user", "assistant", "user", "assistant"} {
		m := &store.AIMessage{ConversationID: c.ID, Role: role, Content: string(rune('a' + i)), CreatedAt: time.Now()}
		if err := st.InsertAIMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}

	// Someone else's conversation is not found, and nothing is removed.
	if err := s.DeleteMessagesFrom(ctx, "u2", c.ID, ids[0]); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("other user: %v, want not found", err)
	}
	if err := s.DeleteMessagesFrom(ctx, "u1", c.ID, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad id: %v, want invalid input", err)
	}

	// While a reply is generating, history can't change under it.
	s.lockConversation(c.ID)
	if err := s.DeleteMessagesFrom(ctx, "u1", c.ID, ids[2]); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy: %v, want ErrBusy", err)
	}
	s.unlockConversation(c.ID)

	if err := s.DeleteMessagesFrom(ctx, "u1", c.ID, ids[2]); err != nil {
		t.Fatal(err)
	}
	d, err := s.Conversation(ctx, "u1", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Messages) != 2 || d.Messages[0].ID != ids[0] || d.Messages[1].ID != ids[1] {
		t.Fatalf("after truncating: %+v, want the first two messages", d.Messages)
	}
}

func TestSetConversationModel(t *testing.T) {
	ctx := context.Background()
	s, st := testService(t)
	now := time.Now()
	if err := st.InsertAIProvider(ctx, &store.AIProvider{ID: "p2", Name: "Local", Kind: KindOllama, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateConversation(ctx, "u1", "", "old")
	if err != nil {
		t.Fatal(err)
	}
	str := func(v string) *string { return &v }

	if err := s.SetConversationModel(ctx, "u2", c.ID, nil, str("x")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("other user: %v, want not found", err)
	}
	if err := s.SetConversationModel(ctx, "u1", c.ID, str("nope"), nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown provider: %v, want invalid input", err)
	}
	if err := s.SetConversationModel(ctx, "u1", c.ID, str("p2"), str("  llama3  ")); err != nil {
		t.Fatal(err)
	}
	got, err := st.AIConversationByID(ctx, "u1", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProviderID != "p2" || got.Model != "llama3" {
		t.Fatalf("got provider %q model %q, want p2 / llama3", got.ProviderID, got.Model)
	}
	// Only the model changes; an empty one falls back to the provider default.
	if err := s.SetConversationModel(ctx, "u1", c.ID, nil, str("")); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.AIConversationByID(ctx, "u1", c.ID); got.ProviderID != "p2" || got.Model != "" {
		t.Fatalf("got provider %q model %q, want p2 / empty", got.ProviderID, got.Model)
	}
}
