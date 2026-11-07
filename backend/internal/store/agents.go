package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Agent token scopes.
const (
	AgentScopeRead = "read" // read-only tools only
	AgentScopeAct  = "act"  // may also ask to do things (each needs approval)
)

// AgentToken lets one AI agent use NoCap MCP as the person who made it.
// The token itself is never stored, only its hash.
type AgentToken struct {
	ID          string
	UserID      string
	Username    string // filled by the queries
	Name        string
	Scope       string
	Tools       []string // nil = every tool its scope allows
	AlwaysAllow []string // action tools the owner said never to ask about again
	CreatedAt   time.Time
	LastUsedAt  time.Time // zero = never
}

const agentTokenCols = `t.id, t.user_id, COALESCE(u.username, ''), t.name, t.scope, t.tools, t.always_allow, t.created_at, t.last_used_at`

func scanAgentToken(row scanner) (*AgentToken, error) {
	var (
		t             AgentToken
		tools, always string
		created, used int64
	)
	if err := row.Scan(&t.ID, &t.UserID, &t.Username, &t.Name, &t.Scope, &tools, &always, &created, &used); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if tools != "" {
		_ = json.Unmarshal([]byte(tools), &t.Tools)
		if t.Tools == nil {
			t.Tools = []string{}
		}
	}
	if always != "" {
		_ = json.Unmarshal([]byte(always), &t.AlwaysAllow)
	}
	t.CreatedAt = time.Unix(created, 0).UTC()
	t.LastUsedAt = unixOrZero(used)
	return &t, nil
}

func encodeList(l []string, nilAsEmpty bool) string {
	if l == nil && nilAsEmpty {
		return ""
	}
	if l == nil {
		l = []string{}
	}
	b, _ := json.Marshal(l)
	return string(b)
}

// CreateAgentToken stores a new token. tokenHash must be unique.
func (s *Store) CreateAgentToken(ctx context.Context, t *AgentToken, tokenHash string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_tokens (id, user_id, name, scope, token_hash, tools, always_allow, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.UserID, t.Name, t.Scope, tokenHash, encodeList(t.Tools, true), encodeList(t.AlwaysAllow, true), t.CreatedAt.Unix())
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// AgentTokenByHash finds an active token by its hash.
func (s *Store) AgentTokenByHash(ctx context.Context, tokenHash string) (*AgentToken, error) {
	return scanAgentToken(s.db.QueryRowContext(ctx,
		`SELECT `+agentTokenCols+` FROM agent_tokens t LEFT JOIN users u ON u.id = t.user_id
		 WHERE t.token_hash = ? AND t.revoked_at = 0`, tokenHash))
}

// AgentTokenByID finds one of a person's active tokens.
func (s *Store) AgentTokenByID(ctx context.Context, id, userID string) (*AgentToken, error) {
	return scanAgentToken(s.db.QueryRowContext(ctx,
		`SELECT `+agentTokenCols+` FROM agent_tokens t LEFT JOIN users u ON u.id = t.user_id
		 WHERE t.id = ? AND t.user_id = ? AND t.revoked_at = 0`, id, userID))
}

// ListAgentTokens lists a person's active tokens, newest first.
func (s *Store) ListAgentTokens(ctx context.Context, userID string) ([]AgentToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+agentTokenCols+` FROM agent_tokens t LEFT JOIN users u ON u.id = t.user_id
		 WHERE t.user_id = ? AND t.revoked_at = 0 ORDER BY t.created_at DESC, t.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentToken{}
	for rows.Next() {
		t, err := scanAgentToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// RevokeAgentToken switches one of a person's tokens off for good.
func (s *Store) RevokeAgentToken(ctx context.Context, id, userID string, at time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE agent_tokens SET revoked_at = ? WHERE id = ? AND user_id = ? AND revoked_at = 0`, at.Unix(), id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetAgentTokenTools saves which tools a token may use (nil = all) and
// which actions run without asking.
func (s *Store) SetAgentTokenTools(ctx context.Context, id, userID string, tools, always []string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE agent_tokens SET tools = ?, always_allow = ? WHERE id = ? AND user_id = ? AND revoked_at = 0`,
		encodeList(tools, true), encodeList(always, true), id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchAgentToken records that a token was used.
func (s *Store) TouchAgentToken(ctx context.Context, id string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agent_tokens SET last_used_at = ? WHERE id = ?`, at.Unix(), id)
	return err
}

// AgentActivity is one tool call an agent made.
type AgentActivity struct {
	ID        int64     `json:"id"`
	TokenID   string    `json:"token_id"`
	UserID    string    `json:"-"`
	Agent     string    `json:"agent"`
	Tool      string    `json:"tool"`
	Summary   string    `json:"summary"`
	Outcome   string    `json:"outcome"` // ok | error | denied | timeout | refused
	Detail    string    `json:"detail,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// agentActivityKeep is how many entries are kept in all.
const agentActivityKeep = 5000

// AddAgentActivity records a tool call (and trims the oldest entries).
func (s *Store) AddAgentActivity(ctx context.Context, a AgentActivity) error {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_activity (token_id, user_id, agent, tool, summary, outcome, detail, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.TokenID, a.UserID, a.Agent, a.Tool, a.Summary, a.Outcome, a.Detail, a.CreatedAt.Unix())
	if err != nil {
		return err
	}
	if id, err := res.LastInsertId(); err == nil && id > agentActivityKeep {
		_, err = s.db.ExecContext(ctx, `DELETE FROM agent_activity WHERE id <= ?`, id-agentActivityKeep)
		return err
	}
	return nil
}

// ListAgentActivity is a person's most recent agent activity, newest first.
func (s *Store) ListAgentActivity(ctx context.Context, userID string, limit int) ([]AgentActivity, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, token_id, user_id, agent, tool, summary, outcome, detail, created_at FROM agent_activity
		 WHERE user_id = ? ORDER BY id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentActivity{}
	for rows.Next() {
		var (
			a  AgentActivity
			at int64
		)
		if err := rows.Scan(&a.ID, &a.TokenID, &a.UserID, &a.Agent, &a.Tool, &a.Summary, &a.Outcome, &a.Detail, &at); err != nil {
			return nil, err
		}
		a.CreatedAt = time.Unix(at, 0).UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}
