package store

import (
	"context"
	"database/sql"
	"errors"
)

// ClipboardSettings is one person's NoCap Link clipboard choices.
type ClipboardSettings struct {
	// History keeps a short history; off keeps only the last item.
	History bool `json:"history"`
	// Desktop: NoCap Desktop's own clipboard (on this machine's screen) joins in.
	Desktop bool `json:"desktop"`
}

// DefaultClipboardSettings: the last item only, the desktop included.
var DefaultClipboardSettings = ClipboardSettings{Desktop: true}

// ClipboardSettings returns a person's clipboard choices (the defaults when
// they never changed them).
func (s *Store) ClipboardSettings(ctx context.Context, userID string) (ClipboardSettings, error) {
	var history, desktop bool
	err := s.db.QueryRowContext(ctx, `SELECT history, desktop FROM clipboard_settings WHERE user_id = ?`, userID).Scan(&history, &desktop)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultClipboardSettings, nil
	}
	if err != nil {
		return DefaultClipboardSettings, err
	}
	return ClipboardSettings{History: history, Desktop: desktop}, nil
}

// SetClipboardSettings saves a person's clipboard choices.
func (s *Store) SetClipboardSettings(ctx context.Context, userID string, c ClipboardSettings) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO clipboard_settings (user_id, history, desktop) VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET history = excluded.history, desktop = excluded.desktop`, userID, c.History, c.Desktop)
	return err
}
