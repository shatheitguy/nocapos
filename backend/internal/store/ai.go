package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type AIProvider struct {
	ID           string
	Name         string
	Kind         string // ollama | anthropic | openai
	BaseURL      string
	APIKeyEnc    []byte // AES-GCM sealed; never leaves the server
	DefaultModel string
	KeepAlive    string // Ollama only: how long models stay in RAM after use
	ContextSize  int    // tokens; 0 = provider/model default
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type AIConversation struct {
	ID         string    `json:"id"`
	UserID     string    `json:"-"`
	Title      string    `json:"title"`
	ProviderID string    `json:"provider_id"`
	Model      string    `json:"model"`
	Pinned     bool      `json:"pinned"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Preview    string    `json:"preview,omitempty"` // start of the last message, for the list
}

type AIMessage struct {
	ID             int64     `json:"id"`
	ConversationID string    `json:"-"`
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	Model          string    `json:"model,omitempty"`
	Error          string    `json:"error,omitempty"`
	TokensIn       int       `json:"tokens_in,omitempty"` // prompt tokens reported by the model
	TokensOut      int       `json:"tokens_out,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// ---------- providers ----------

const aiProviderCols = `id, name, kind, base_url, api_key_enc, default_model, keep_alive, context_size, created_at, updated_at`

func scanAIProvider(row scanner) (*AIProvider, error) {
	var p AIProvider
	var created, updated int64
	if err := row.Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.APIKeyEnc, &p.DefaultModel, &p.KeepAlive, &p.ContextSize, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.CreatedAt, p.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	return &p, nil
}

func (s *Store) ListAIProviders(ctx context.Context) ([]AIProvider, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+aiProviderCols+` FROM ai_providers ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AIProvider
	for rows.Next() {
		p, err := scanAIProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (s *Store) AIProviderByID(ctx context.Context, id string) (*AIProvider, error) {
	return scanAIProvider(s.db.QueryRowContext(ctx, `SELECT `+aiProviderCols+` FROM ai_providers WHERE id = ?`, id))
}

func (s *Store) InsertAIProvider(ctx context.Context, p *AIProvider) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO ai_providers (`+aiProviderCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Kind, p.BaseURL, p.APIKeyEnc, p.DefaultModel, p.KeepAlive, p.ContextSize, p.CreatedAt.Unix(), p.UpdatedAt.Unix())
	return err
}

func (s *Store) UpdateAIProvider(ctx context.Context, p *AIProvider) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE ai_providers SET name = ?, kind = ?, base_url = ?, api_key_enc = ?, default_model = ?, keep_alive = ?, context_size = ?, updated_at = ? WHERE id = ?`,
		p.Name, p.Kind, p.BaseURL, p.APIKeyEnc, p.DefaultModel, p.KeepAlive, p.ContextSize, p.UpdatedAt.Unix(), p.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteAIProvider(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ai_providers WHERE id = ?`, id)
	return err
}

// ---------- conversations ----------

const aiConvCols = `c.id, c.user_id, c.title, c.provider_id, c.model, c.pinned, c.created_at, c.updated_at`

func scanAIConversation(row scanner, extra ...any) (*AIConversation, error) {
	var c AIConversation
	var pinned int
	var created, updated int64
	dest := append([]any{&c.ID, &c.UserID, &c.Title, &c.ProviderID, &c.Model, &pinned, &created, &updated}, extra...)
	if err := row.Scan(dest...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	c.Pinned = pinned != 0
	c.CreatedAt, c.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	return &c, nil
}

// ListAIConversations returns a user's conversations, newest first, optionally
// filtered by a case-insensitive match on the title or any message.
func (s *Store) ListAIConversations(ctx context.Context, userID, query string, limit int) ([]AIConversation, error) {
	args := []any{userID}
	where := `c.user_id = ?`
	if q := strings.TrimSpace(query); q != "" {
		like := "%" + escapeLike(q) + "%"
		where += ` AND (c.title LIKE ? ESCAPE '\' OR EXISTS (SELECT 1 FROM ai_messages m WHERE m.conversation_id = c.id AND m.content LIKE ? ESCAPE '\'))`
		args = append(args, like, like)
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+aiConvCols+`,
		        COALESCE((SELECT substr(m.content, 1, 160) FROM ai_messages m WHERE m.conversation_id = c.id ORDER BY m.id DESC LIMIT 1), '')
		 FROM ai_conversations c WHERE `+where+`
		 ORDER BY c.updated_at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIConversation{}
	for rows.Next() {
		var preview string
		c, err := scanAIConversation(rows, &preview)
		if err != nil {
			return nil, err
		}
		c.Preview = preview
		out = append(out, *c)
	}
	return out, rows.Err()
}

// AIConversationByID enforces ownership: other users' conversations are ErrNotFound.
func (s *Store) AIConversationByID(ctx context.Context, userID, id string) (*AIConversation, error) {
	return scanAIConversation(s.db.QueryRowContext(ctx,
		`SELECT `+aiConvCols+` FROM ai_conversations c WHERE c.id = ? AND c.user_id = ?`, id, userID))
}

func (s *Store) InsertAIConversation(ctx context.Context, c *AIConversation) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ai_conversations (id, user_id, title, provider_id, model, pinned, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.UserID, c.Title, c.ProviderID, c.Model, boolInt(c.Pinned), c.CreatedAt.Unix(), c.UpdatedAt.Unix())
	return err
}

func (s *Store) UpdateAIConversation(ctx context.Context, c *AIConversation) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE ai_conversations SET title = ?, provider_id = ?, model = ?, pinned = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
		c.Title, c.ProviderID, c.Model, boolInt(c.Pinned), c.UpdatedAt.Unix(), c.ID, c.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteAIConversation(ctx context.Context, userID, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM ai_conversations WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- messages ----------

func (s *Store) ListAIMessages(ctx context.Context, conversationID string) ([]AIMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, conversation_id, role, content, model, error, tokens_in, tokens_out, created_at FROM ai_messages WHERE conversation_id = ? ORDER BY id`,
		conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIMessage{}
	for rows.Next() {
		var m AIMessage
		var created int64
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.Model, &m.Error, &m.TokensIn, &m.TokensOut, &created); err != nil {
			return nil, err
		}
		m.CreatedAt = time.Unix(created, 0).UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) InsertAIMessage(ctx context.Context, m *AIMessage) error {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO ai_messages (conversation_id, role, content, model, error, tokens_in, tokens_out, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ConversationID, m.Role, m.Content, m.Model, m.Error, m.TokensIn, m.TokensOut, m.CreatedAt.Unix())
	if err != nil {
		return err
	}
	m.ID, err = res.LastInsertId()
	return err
}

// DeleteAIMessagesFrom removes a message and everything after it (used to
// regenerate the last answer).
func (s *Store) DeleteAIMessagesFrom(ctx context.Context, conversationID string, fromID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ai_messages WHERE conversation_id = ? AND id >= ?`, conversationID, fromID)
	return err
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
