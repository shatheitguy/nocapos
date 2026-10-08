package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type AIMemory struct {
	ID        string    `json:"id"`
	UserID    string    `json:"-"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Store) ListAIMemories(ctx context.Context, userID string) ([]AIMemory, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, content, created_at, updated_at FROM ai_memories WHERE user_id = ? ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIMemory{}
	for rows.Next() {
		var m AIMemory
		var created, updated int64
		if err := rows.Scan(&m.ID, &m.UserID, &m.Content, &created, &updated); err != nil {
			return nil, err
		}
		m.CreatedAt, m.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) CountAIMemories(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ai_memories WHERE user_id = ?`, userID).Scan(&n)
	return n, err
}

func (s *Store) InsertAIMemory(ctx context.Context, m *AIMemory) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ai_memories (id, user_id, content, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		m.ID, m.UserID, m.Content, m.CreatedAt.Unix(), m.UpdatedAt.Unix())
	return err
}

func (s *Store) UpdateAIMemory(ctx context.Context, userID, id, content string, now time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE ai_memories SET content = ?, updated_at = ? WHERE id = ? AND user_id = ?`, content, now.Unix(), id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteAIMemory(ctx context.Context, userID, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM ai_memories WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AIMemoryEnabled defaults to true for users who never changed the setting.
func (s *Store) AIMemoryEnabled(ctx context.Context, userID string) (bool, error) {
	var v int
	err := s.db.QueryRowContext(ctx, `SELECT memory_enabled FROM ai_user_settings WHERE user_id = ?`, userID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return v != 0, err
}

func (s *Store) SetAIMemoryEnabled(ctx context.Context, userID string, enabled bool) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ai_user_settings (user_id, memory_enabled) VALUES (?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET memory_enabled = excluded.memory_enabled`, userID, boolInt(enabled))
	return err
}
