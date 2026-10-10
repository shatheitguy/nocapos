package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// FileFavorite is one folder pinned in a user's Files sidebar.
type FileFavorite struct {
	Root string `json:"root"`
	Path string `json:"path"`
}

// FileFavorites returns a user's favorites; set is false if they were never saved.
func (s *Store) FileFavorites(ctx context.Context, userID string) (favs []FileFavorite, set bool, err error) {
	var raw string
	err = s.db.QueryRowContext(ctx, `SELECT favorites FROM file_favorites WHERE user_id = ?`, userID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := json.Unmarshal([]byte(raw), &favs); err != nil {
		return nil, false, err
	}
	return favs, true, nil
}

// SetFileFavorites replaces a user's favorites.
func (s *Store) SetFileFavorites(ctx context.Context, userID string, favs []FileFavorite) error {
	if favs == nil {
		favs = []FileFavorite{}
	}
	raw, err := json.Marshal(favs)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO file_favorites (user_id, favorites, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET favorites = excluded.favorites, updated_at = excluded.updated_at`,
		userID, string(raw), time.Now().Unix())
	return err
}
