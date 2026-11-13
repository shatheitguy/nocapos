package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Agent token scopes (from before per-capability scopes; still accepted
// when making a token).
const (
	AgentScopeRead = "read" // read-only tools only
	AgentScopeAct  = "act"  // may also ask to do things (each needs approval)
)

// Agent kinds.
const (
	AgentKindLocal     = "local"     // this machine only (cap mcp, or HTTP on loopback)
	AgentKindRemote    = "remote"    // other devices, when the network may use NoCap MCP
	AgentKindAssistant = "assistant" // the built-in AI Assistant (no token)
)

// AgentToken lets one AI agent use NoCap MCP as the person who made it.
// The token itself is never stored, only its hash.
type AgentToken struct {
	ID          string
	UserID      string
	Username    string // filled by the queries
	Name        string
	Scope       string
	Tools       []string // nil = every tool its scopes allow
	AlwaysAllow []string // action tools the owner said never to ask about again
	CreatedAt   time.Time
	LastUsedAt  time.Time // zero = never

	Kind      string
	Scopes    string    // JSON profile; "" = derived from Scope
	IPAllow   []string  // empty = any address
	ExpiresAt time.Time // zero = never
	Paused    bool
	// Pairing is true while the agent waits for its pairing code to be used.
	Pairing       bool
	PairExpiresAt time.Time
}

// Expired reports whether the token has run out at now.
func (t *AgentToken) Expired(now time.Time) bool {
	return !t.ExpiresAt.IsZero() && !now.Before(t.ExpiresAt)
}

const agentTokenCols = `t.id, t.user_id, COALESCE(u.username, ''), t.name, t.scope, t.tools, t.always_allow, t.created_at, t.last_used_at,
	t.kind, t.scopes, t.ip_allow, t.expires_at, t.paused, t.pair_hash != '', t.pair_expires_at`

func scanAgentToken(row scanner) (*AgentToken, error) {
	var (
		t                        AgentToken
		tools, always, ips       string
		created, used, exp, pexp int64
	)
	if err := row.Scan(&t.ID, &t.UserID, &t.Username, &t.Name, &t.Scope, &tools, &always, &created, &used,
		&t.Kind, &t.Scopes, &ips, &exp, &t.Paused, &t.Pairing, &pexp); err != nil {
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
	if ips != "" {
		_ = json.Unmarshal([]byte(ips), &t.IPAllow)
	}
	t.CreatedAt = time.Unix(created, 0).UTC()
	t.LastUsedAt = unixOrZero(used)
	t.ExpiresAt = unixOrZero(exp)
	t.PairExpiresAt = unixOrZero(pexp)
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

func unixOrNone(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// CreateAgentToken stores a new token. tokenHash must be unique. With a
// pairHash the agent waits for its pairing code instead (tokenHash is then
// a placeholder no token hashes to).
func (s *Store) CreateAgentToken(ctx context.Context, t *AgentToken, tokenHash string, pairHash ...string) error {
	ph := ""
	if len(pairHash) > 0 {
		ph = pairHash[0]
	}
	if t.Kind == "" {
		t.Kind = AgentKindLocal
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_tokens (id, user_id, name, scope, token_hash, tools, always_allow, created_at,
		   kind, scopes, ip_allow, expires_at, paused, pair_hash, pair_expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.UserID, t.Name, t.Scope, tokenHash, encodeList(t.Tools, true), encodeList(t.AlwaysAllow, true), t.CreatedAt.Unix(),
		t.Kind, t.Scopes, encodeList(t.IPAllow, true), unixOrNone(t.ExpiresAt), t.Paused, ph, unixOrNone(t.PairExpiresAt))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err == nil {
		t.Pairing = ph != ""
	}
	return err
}

// AgentTokenByHash finds an active, paired, token-holding agent by its token's hash.
func (s *Store) AgentTokenByHash(ctx context.Context, tokenHash string) (*AgentToken, error) {
	return scanAgentToken(s.db.QueryRowContext(ctx,
		`SELECT `+agentTokenCols+` FROM agent_tokens t LEFT JOIN users u ON u.id = t.user_id
		 WHERE t.token_hash = ? AND t.revoked_at = 0 AND t.pair_hash = '' AND t.kind != 'assistant'`, tokenHash))
}

// AgentPairingByHash finds an agent still waiting for its pairing code.
func (s *Store) AgentPairingByHash(ctx context.Context, pairHash string) (*AgentToken, error) {
	if pairHash == "" {
		return nil, ErrNotFound
	}
	return scanAgentToken(s.db.QueryRowContext(ctx,
		`SELECT `+agentTokenCols+` FROM agent_tokens t LEFT JOIN users u ON u.id = t.user_id
		 WHERE t.pair_hash = ? AND t.revoked_at = 0`, pairHash))
}

// ClaimAgentPairing turns a pairing code (by its hash) into a token: the
// code works once, before it expires.
func (s *Store) ClaimAgentPairing(ctx context.Context, id, pairHash, tokenHash string, now time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE agent_tokens SET token_hash = ?, pair_hash = '', pair_expires_at = 0
		 WHERE id = ? AND pair_hash = ? AND pair_expires_at > ? AND revoked_at = 0`, tokenHash, id, pairHash, now.Unix())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound // expired, or used a moment ago
	}
	return nil
}

// SetAgentSecret replaces an agent's token (by hash) or, with a pairHash,
// sets it waiting for a new pairing code. The old token stops working.
func (s *Store) SetAgentSecret(ctx context.Context, id, userID, tokenHash, pairHash string, pairExpires time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE agent_tokens SET token_hash = ?, pair_hash = ?, pair_expires_at = ?
		 WHERE id = ? AND user_id = ? AND revoked_at = 0 AND kind != 'assistant'`,
		tokenHash, pairHash, unixOrNone(pairExpires), id, userID)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AgentTokenByID finds one of a person's active agents.
func (s *Store) AgentTokenByID(ctx context.Context, id, userID string) (*AgentToken, error) {
	return scanAgentToken(s.db.QueryRowContext(ctx,
		`SELECT `+agentTokenCols+` FROM agent_tokens t LEFT JOIN users u ON u.id = t.user_id
		 WHERE t.id = ? AND t.user_id = ? AND t.revoked_at = 0`, id, userID))
}

// AgentAssistant is a person's built-in AI Assistant profile (ErrNotFound if none yet).
func (s *Store) AgentAssistant(ctx context.Context, userID string) (*AgentToken, error) {
	return scanAgentToken(s.db.QueryRowContext(ctx,
		`SELECT `+agentTokenCols+` FROM agent_tokens t LEFT JOIN users u ON u.id = t.user_id
		 WHERE t.user_id = ? AND t.kind = 'assistant' AND t.revoked_at = 0 ORDER BY t.created_at LIMIT 1`, userID))
}

// ListAgentTokens lists a person's active agents (not the AI Assistant), newest first.
func (s *Store) ListAgentTokens(ctx context.Context, userID string) ([]AgentToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+agentTokenCols+` FROM agent_tokens t LEFT JOIN users u ON u.id = t.user_id
		 WHERE t.user_id = ? AND t.revoked_at = 0 AND t.kind != 'assistant' ORDER BY t.created_at DESC, t.id`, userID)
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
		`UPDATE agent_tokens SET revoked_at = ?, pair_hash = '' WHERE id = ? AND user_id = ? AND revoked_at = 0 AND kind != 'assistant'`,
		at.Unix(), id, userID)
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

// UpdateAgentProfile saves an agent's name, scopes, IP allowlist, expiry and pause switch.
func (s *Store) UpdateAgentProfile(ctx context.Context, t *AgentToken) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE agent_tokens SET name = ?, scopes = ?, ip_allow = ?, expires_at = ?, paused = ?
		 WHERE id = ? AND user_id = ? AND revoked_at = 0`,
		t.Name, t.Scopes, encodeList(t.IPAllow, true), unixOrNone(t.ExpiresAt), t.Paused, t.ID, t.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PauseAgents pauses every agent of one person ("" = everyone's): Stop all agents.
func (s *Store) PauseAgents(ctx context.Context, userID string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE agent_tokens SET paused = 1 WHERE (? = '' OR user_id = ?) AND revoked_at = 0 AND paused = 0`, userID, userID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
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
	Outcome   string    `json:"outcome"` // ok | error | denied | timeout | stopped
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
	return s.ListAgentActivityFor(ctx, userID, "", 0, limit)
}

// ListAgentActivityFor is one agent's activity (tokenID "" = every agent),
// newest first, older than entry before (0 = from the newest).
func (s *Store) ListAgentActivityFor(ctx context.Context, userID, tokenID string, before int64, limit int) ([]AgentActivity, error) {
	if before <= 0 {
		before = 1<<63 - 1
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, token_id, user_id, agent, tool, summary, outcome, detail, created_at FROM agent_activity
		 WHERE user_id = ? AND (? = '' OR token_id = ?) AND id < ? ORDER BY id DESC LIMIT ?`,
		userID, tokenID, tokenID, before, limit)
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
