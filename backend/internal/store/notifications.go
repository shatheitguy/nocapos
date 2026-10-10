package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Notification is one row of the Notification Center.
type Notification struct {
	ID        int64
	Key       string
	Kind      string
	Level     string
	Title     string
	Body      string
	Action    string // JSON, '' = none
	CreatedAt time.Time
	ReadAt    time.Time // zero = unread
}

// keepHidden is how many deleted notifications are remembered (just their
// keys matter) so the same event isn't notified again.
const keepHidden = 2000

// AddNotification stores n unless a notification with its key exists (even a
// deleted one), then keeps only the newest keep. It reports whether n was added
// (with n.ID set).
func (s *Store) AddNotification(ctx context.Context, n *Notification, keep int) (bool, error) {
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now()
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO notifications (key, kind, level, title, body, action_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(key) DO NOTHING`,
		n.Key, n.Kind, n.Level, n.Title, n.Body, n.Action, n.CreatedAt.Unix())
	if err != nil {
		return false, err
	}
	if c, _ := res.RowsAffected(); c == 0 {
		return false, nil
	}
	n.ID, _ = res.LastInsertId()
	if keep > 0 {
		_, err = s.db.ExecContext(ctx, `DELETE FROM notifications WHERE
			(hidden = 0 AND id NOT IN (SELECT id FROM notifications WHERE hidden = 0 ORDER BY id DESC LIMIT ?)) OR
			(hidden = 1 AND id NOT IN (SELECT id FROM notifications WHERE hidden = 1 ORDER BY id DESC LIMIT ?))`, keep, keepHidden)
	}
	return true, err
}

// Notifications returns the newest limit notifications and how many are unread.
func (s *Store) Notifications(ctx context.Context, limit int) ([]Notification, int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, key, kind, level, title, body, action_json, created_at, read_at
		FROM notifications WHERE hidden = 0 ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var n Notification
		var created int64
		var read sql.NullInt64
		if err := rows.Scan(&n.ID, &n.Key, &n.Kind, &n.Level, &n.Title, &n.Body, &n.Action, &created, &read); err != nil {
			return nil, 0, err
		}
		n.CreatedAt = unixOrZero(created)
		if read.Valid {
			n.ReadAt = unixOrZero(read.Int64)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	unread, err := s.UnreadNotifications(ctx)
	return out, unread, err
}

func (s *Store) UnreadNotifications(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE hidden = 0 AND read_at IS NULL`).Scan(&n)
	return n, err
}

// MarkNotificationsRead marks the given ids read, or every one when all is set.
func (s *Store) MarkNotificationsRead(ctx context.Context, ids []int64, all bool) error {
	now := time.Now().Unix()
	if all {
		_, err := s.db.ExecContext(ctx, `UPDATE notifications SET read_at = ? WHERE read_at IS NULL`, now)
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	args := []any{now}
	for _, id := range ids {
		args = append(args, id)
	}
	q := `UPDATE notifications SET read_at = ? WHERE read_at IS NULL AND id IN (?` + strings.Repeat(",?", len(ids)-1) + `)`
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

// DeleteNotification hides one notification (its key stays remembered).
func (s *Store) DeleteNotification(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE notifications SET hidden = 1, read_at = COALESCE(read_at, ?) WHERE id = ? AND hidden = 0`, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearNotifications hides every notification.
func (s *Store) ClearNotifications(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE notifications SET hidden = 1, read_at = COALESCE(read_at, ?) WHERE hidden = 0`, time.Now().Unix())
	return err
}
