package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	Disabled     bool      `json:"disabled"`
	TOTPSecret   []byte    `json:"-"` // AES-GCM sealed; nil when not enrolled
	TOTPEnabled  bool      `json:"totp_enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

const userCols = `id, username, password_hash, role, disabled, totp_secret, totp_enabled, created_at, updated_at`

type scanner interface{ Scan(dest ...any) error }

func scanUser(row scanner) (*User, error) {
	var (
		u                     User
		disabled, totpEnabled int
		created, updated      int64
	)
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &disabled, &u.TOTPSecret, &totpEnabled, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.Disabled = disabled != 0
	u.TOTPEnabled = totpEnabled != 0
	u.CreatedAt = time.Unix(created, 0).UTC()
	u.UpdatedAt = time.Unix(updated, 0).UTC()
	return &u, nil
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateFirstAdmin inserts u only if the users table is empty. The check and
// insert are a single statement, so concurrent setup requests cannot both win.
func (s *Store) CreateFirstAdmin(ctx context.Context, u *User) error {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, username, password_hash, role, disabled, created_at, updated_at)
		 SELECT ?, ?, ?, ?, 0, ?, ?
		 WHERE NOT EXISTS (SELECT 1 FROM users)`,
		u.ID, u.Username, u.PasswordHash, RoleAdmin, u.CreatedAt.Unix(), u.UpdatedAt.Unix())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConflict
	}
	u.Role = RoleAdmin
	return nil
}

func (s *Store) CreateUser(ctx context.Context, u *User) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, username, password_hash, role, disabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Username, u.PasswordHash, u.Role, boolInt(u.Disabled), u.CreatedAt.Unix(), u.UpdatedAt.Unix())
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// SetTOTP stores (or clears) a user's sealed TOTP secret and enabled flag.
func (s *Store) SetTOTP(ctx context.Context, userID string, secret []byte, enabled bool) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET totp_secret = ?, totp_enabled = ?, updated_at = ? WHERE id = ?`,
		secret, boolInt(enabled), time.Now().Unix(), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetPassword updates a user's password hash.
func (s *Store) SetPassword(ctx context.Context, userID, hash string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, hash, time.Now().Unix(), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UserByUsername(ctx context.Context, username string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userCols+` FROM users WHERE username = ?`, username))
}

func (s *Store) UserByID(ctx context.Context, id string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ListUsers returns every account, oldest first.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// CountAdmins counts enabled administrators.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = ? AND disabled = 0`, RoleAdmin).Scan(&n)
	return n, err
}

// SetRole changes a user's role.
func (s *Store) SetRole(ctx context.Context, userID, role string) error {
	return s.updateUser(ctx, `UPDATE users SET role = ?, updated_at = ? WHERE id = ?`, role, time.Now().Unix(), userID)
}

// SetDisabled enables or disables an account.
func (s *Store) SetDisabled(ctx context.Context, userID string, disabled bool) error {
	return s.updateUser(ctx, `UPDATE users SET disabled = ?, updated_at = ? WHERE id = ?`, boolInt(disabled), time.Now().Unix(), userID)
}

// DeleteUser removes an account; sessions, chats and memory cascade.
func (s *Store) DeleteUser(ctx context.Context, userID string) error {
	return s.updateUser(ctx, `DELETE FROM users WHERE id = ?`, userID)
}

func (s *Store) updateUser(ctx context.Context, q string, args ...any) error {
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
