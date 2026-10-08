package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type RefreshToken struct {
	TokenHash       string
	FamilyID        string
	UserID          string
	CreatedAt       time.Time
	ExpiresAt       time.Time
	FamilyExpiresAt time.Time
	RevokedAt       *time.Time
	UserAgent       string
	IP              string
}

func (s *Store) InsertRefreshToken(ctx context.Context, t *RefreshToken) error {
	return insertRefreshToken(ctx, s.db, t)
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertRefreshToken(ctx context.Context, db execer, t *RefreshToken) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO refresh_tokens
		 (token_hash, family_id, user_id, created_at, expires_at, family_expires_at, user_agent, ip)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.TokenHash, t.FamilyID, t.UserID, t.CreatedAt.Unix(), t.ExpiresAt.Unix(),
		t.FamilyExpiresAt.Unix(), t.UserAgent, t.IP)
	return err
}

func (s *Store) RefreshTokenByHash(ctx context.Context, hash string) (*RefreshToken, error) {
	var (
		t                        RefreshToken
		created, expires, famExp int64
		revoked                  sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT token_hash, family_id, user_id, created_at, expires_at, family_expires_at, revoked_at, user_agent, ip
		 FROM refresh_tokens WHERE token_hash = ?`, hash).
		Scan(&t.TokenHash, &t.FamilyID, &t.UserID, &created, &expires, &famExp, &revoked, &t.UserAgent, &t.IP)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.CreatedAt = time.Unix(created, 0)
	t.ExpiresAt = time.Unix(expires, 0)
	t.FamilyExpiresAt = time.Unix(famExp, 0)
	if revoked.Valid {
		r := time.Unix(revoked.Int64, 0)
		t.RevokedAt = &r
	}
	return &t, nil
}

// RotateRefreshToken atomically revokes oldHash and stores next. It returns
// ErrConflict if oldHash was already revoked (a concurrent refresh won).
func (s *Store) RotateRefreshToken(ctx context.Context, oldHash string, next *RefreshToken, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE refresh_tokens SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL`,
		now.Unix(), oldHash)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConflict
	}
	if err := insertRefreshToken(ctx, tx, next); err != nil {
		return err
	}
	return tx.Commit()
}

// RevokeAllUserSessions revokes every active refresh token for a user (used
// after a password reset).
func (s *Store) RevokeAllUserSessions(ctx context.Context, userID string, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE refresh_tokens SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`,
		now.Unix(), userID)
	return err
}

func (s *Store) RevokeFamily(ctx context.Context, familyID string, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE refresh_tokens SET revoked_at = ? WHERE family_id = ? AND revoked_at IS NULL`,
		now.Unix(), familyID)
	return err
}

// FamilyActive reports whether a login session still has a live refresh
// token. Access tokens carry the family ID so logout takes effect immediately.
func (s *Store) FamilyActive(ctx context.Context, familyID string, now time.Time) (bool, error) {
	var ok int
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM refresh_tokens
		 WHERE family_id = ? AND revoked_at IS NULL AND expires_at > ?)`,
		familyID, now.Unix()).Scan(&ok)
	return ok == 1, err
}

func (s *Store) PurgeExpiredTokens(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM refresh_tokens WHERE expires_at < ? OR family_expires_at < ?`,
		now.Unix(), now.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
