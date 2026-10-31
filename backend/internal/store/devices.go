package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Device is a phone (or other device) linked to a person. Its token is
// never stored, only TokenHash.
type Device struct {
	ID         string
	UserID     string
	Username   string // filled by the listing queries
	Name       string
	Platform   string
	AppVersion string
	Battery    int // -1 = unknown
	CreatedAt  time.Time
	LastSeenAt time.Time // zero = never
	RevokedAt  time.Time // zero = active
}

const deviceCols = `d.id, d.user_id, COALESCE(u.username, ''), d.name, d.platform, d.app_version, d.battery, d.created_at, d.last_seen_at, d.revoked_at`

func scanDevice(row scanner) (*Device, error) {
	var (
		d                      Device
		created, seen, revoked int64
	)
	if err := row.Scan(&d.ID, &d.UserID, &d.Username, &d.Name, &d.Platform, &d.AppVersion, &d.Battery, &created, &seen, &revoked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	d.CreatedAt = time.Unix(created, 0).UTC()
	d.LastSeenAt = unixOrZero(seen)
	d.RevokedAt = unixOrZero(revoked)
	return &d, nil
}

// CreateDevice links a device to its owner. tokenHash must be unique.
func (s *Store) CreateDevice(ctx context.Context, d *Device, tokenHash string) error {
	var seen int64
	if !d.LastSeenAt.IsZero() {
		seen = d.LastSeenAt.Unix()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO devices (id, user_id, name, platform, token_hash, app_version, battery, created_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.UserID, d.Name, d.Platform, tokenHash, d.AppVersion, d.Battery, d.CreatedAt.Unix(), seen)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// DeviceByTokenHash finds an active (not revoked) device by its token hash.
func (s *Store) DeviceByTokenHash(ctx context.Context, tokenHash string) (*Device, error) {
	return scanDevice(s.db.QueryRowContext(ctx,
		`SELECT `+deviceCols+` FROM devices d LEFT JOIN users u ON u.id = d.user_id
		 WHERE d.token_hash = ? AND d.revoked_at = 0`, tokenHash))
}

// ListDevices lists active devices, newest first: one person's, or everyone's
// when userID is empty.
func (s *Store) ListDevices(ctx context.Context, userID string) ([]Device, error) {
	q := `SELECT ` + deviceCols + ` FROM devices d LEFT JOIN users u ON u.id = d.user_id WHERE d.revoked_at = 0`
	var args []any
	if userID != "" {
		q += ` AND d.user_id = ?`
		args = append(args, userID)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY d.created_at DESC, d.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// RevokeDevice unlinks an active device at once. With a userID, only that
// person's device matches (ErrNotFound otherwise); empty means any device.
func (s *Store) RevokeDevice(ctx context.Context, id, userID string, at time.Time) (*Device, error) {
	q := `SELECT ` + deviceCols + ` FROM devices d LEFT JOIN users u ON u.id = d.user_id WHERE d.id = ? AND d.revoked_at = 0`
	args := []any{id}
	if userID != "" {
		q += ` AND d.user_id = ?`
		args = append(args, userID)
	}
	d, err := scanDevice(s.db.QueryRowContext(ctx, q, args...))
	if err != nil {
		return nil, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE devices SET revoked_at = ? WHERE id = ? AND revoked_at = 0`, at.Unix(), id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	d.RevokedAt = at.UTC()
	return d, nil
}

// TouchDevice records that a device was seen, and optionally its battery
// level and app version.
func (s *Store) TouchDevice(ctx context.Context, id string, at time.Time, battery *int, appVersion *string) error {
	q := `UPDATE devices SET last_seen_at = ?`
	args := []any{at.Unix()}
	if battery != nil {
		q += `, battery = ?`
		args = append(args, *battery)
	}
	if appVersion != nil {
		q += `, app_version = ?`
		args = append(args, *appVersion)
	}
	args = append(args, id)
	_, err := s.db.ExecContext(ctx, q+` WHERE id = ? AND revoked_at = 0`, args...)
	return err
}
