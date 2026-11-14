package store

import (
	"context"
	"database/sql"
	"errors"
)

// SetDeviceDetails records what a device says about itself; nil leaves a
// field as it is.
func (s *Store) SetDeviceDetails(ctx context.Context, id string, model, osVersion, caps, form *string) error {
	q, args := `UPDATE devices SET id = id`, []any{}
	for _, f := range []struct {
		col string
		v   *string
	}{{"model", model}, {"os_version", osVersion}, {"caps", caps}, {"form", form}} {
		if f.v != nil {
			q += `, ` + f.col + ` = ?`
			args = append(args, *f.v)
		}
	}
	if len(args) == 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, q+` WHERE id = ? AND revoked_at = 0`, append(args, id)...)
	return err
}

// RenameDevice renames an active device. With a userID, only that person's
// device matches (ErrNotFound otherwise); empty means any device.
func (s *Store) RenameDevice(ctx context.Context, id, userID, name string) (*Device, error) {
	q := `UPDATE devices SET name = ? WHERE id = ? AND revoked_at = 0`
	args := []any{name, id}
	if userID != "" {
		q += ` AND user_id = ?`
		args = append(args, userID)
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.DeviceByID(ctx, id)
}

// CountDevices is how many active devices a person has linked.
func (s *Store) CountDevices(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM devices WHERE user_id = ? AND revoked_at = 0`, userID).Scan(&n)
	return n, err
}

// DeviceCounts is how many active devices each person has linked (people
// with none are left out).
func (s *Store) DeviceCounts(ctx context.Context) (map[string]int, error) {
	return s.intsByUser(ctx, `SELECT user_id, COUNT(*) FROM devices WHERE revoked_at = 0 GROUP BY user_id`)
}

// DeviceLimits is every per-person device limit an administrator set.
func (s *Store) DeviceLimits(ctx context.Context) (map[string]int, error) {
	return s.intsByUser(ctx, `SELECT user_id, max_devices FROM device_limits`)
}

// DeviceLimit is the limit set for one person; ok is false when they use
// the server-wide default.
func (s *Store) DeviceLimit(ctx context.Context, userID string) (n int, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT max_devices FROM device_limits WHERE user_id = ?`, userID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return n, err == nil, err
}

// SetDeviceLimit sets one person's device limit; nil goes back to the
// server-wide default.
func (s *Store) SetDeviceLimit(ctx context.Context, userID string, max *int) error {
	if max == nil {
		_, err := s.db.ExecContext(ctx, `DELETE FROM device_limits WHERE user_id = ?`, userID)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO device_limits (user_id, max_devices) VALUES (?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET max_devices = excluded.max_devices`, userID, *max)
	return err
}

func (s *Store) intsByUser(ctx context.Context, q string) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
