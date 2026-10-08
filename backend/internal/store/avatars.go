package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Avatar returns a user's profile photo and its media type.
func (s *Store) Avatar(ctx context.Context, userID string) ([]byte, string, error) {
	var data []byte
	var mime string
	err := s.db.QueryRowContext(ctx, `SELECT data, mime FROM user_avatars WHERE user_id = ?`, userID).Scan(&data, &mime)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	return data, mime, err
}

func (s *Store) SetAvatar(ctx context.Context, userID, mime string, data []byte) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO user_avatars (user_id, mime, data, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET mime = excluded.mime, data = excluded.data, updated_at = excluded.updated_at`,
		userID, mime, data, time.Now().Unix())
	return err
}

func (s *Store) DeleteAvatar(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM user_avatars WHERE user_id = ?`, userID)
	return err
}
