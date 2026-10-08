// Package auth implements local accounts, JWT access tokens and rotating
// refresh tokens with reuse detection.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"alfaos/alfad/internal/store"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidToken       = errors.New("invalid or expired token")
	ErrTokenReuse         = errors.New("refresh token reuse detected")
	ErrSetupDone          = errors.New("setup already completed")
	ErrSetupToken         = errors.New("invalid setup token")
)

// A revoked refresh token presented within this window is treated as a benign
// race (two tabs refreshing at once) rather than a replay attack.
const reuseGrace = 10 * time.Second

type Options struct {
	RefreshTTL    time.Duration
	SessionMaxAge time.Duration
	SetupToken    string
}

type ClientMeta struct {
	IP        string
	UserAgent string
}

type Session struct {
	AccessToken    string
	AccessExpires  time.Time
	RefreshToken   string
	RefreshExpires time.Time
	User           *store.User
}

// Sealer encrypts small secrets at rest (the TOTP secret). ai.SecretBox satisfies it.
type Sealer interface {
	Seal(plain string) []byte
	Open(sealed []byte) (string, error)
}

type Service struct {
	st  *store.Store
	iss *Issuer
	opt Options
	box Sealer
	sys SystemAccounts // nil = NoCapOS's own accounts
	now func() time.Time
	// reset rate limiting is handled at the handler layer
}

func NewService(st *store.Store, iss *Issuer, opt Options) *Service {
	return &Service{st: st, iss: iss, opt: opt, now: time.Now}
}

// SetSealer provides the box used to encrypt TOTP secrets at rest.
func (s *Service) SetSealer(box Sealer) { s.box = box }

func (s *Service) SetupRequired(ctx context.Context) (bool, error) {
	if s.sys != nil {
		return false, nil // sign in with an existing host account instead
	}
	n, err := s.st.CountUsers(ctx)
	return n == 0, err
}

// Setup creates the first administrator. It requires the one-time setup token
// printed to the daemon log so a device on the LAN cannot be claimed by whoever
// reaches the web UI first.
func (s *Service) Setup(ctx context.Context, setupToken, username, password string, meta ClientMeta) (*Session, error) {
	required, err := s.SetupRequired(ctx)
	if err != nil {
		return nil, err
	}
	if !required {
		return nil, ErrSetupDone
	}
	if s.opt.SetupToken == "" || subtle.ConstantTimeCompare([]byte(setupToken), []byte(s.opt.SetupToken)) != 1 {
		return nil, ErrSetupToken
	}
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if err := ValidatePassword(password); err != nil {
		return nil, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	now := s.now()
	u := &store.User{ID: store.NewID(), Username: username, PasswordHash: hash, CreatedAt: now, UpdatedAt: now}
	if err := s.st.CreateFirstAdmin(ctx, u); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, ErrSetupDone
		}
		return nil, err
	}
	return s.startSession(ctx, u, meta)
}

func (s *Service) Login(ctx context.Context, username, password string, meta ClientMeta) (*Session, error) {
	if s.sys != nil {
		return s.loginSystem(ctx, username, password, meta)
	}
	u, err := s.st.UserByUsername(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		_, _ = VerifyPassword(password, dummyHash())
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	ok, err := VerifyPassword(password, u.PasswordHash)
	if err != nil {
		return nil, err
	}
	if !ok || u.Disabled {
		return nil, ErrInvalidCredentials
	}
	return s.startSession(ctx, u, meta)
}

func (s *Service) Refresh(ctx context.Context, raw string, meta ClientMeta) (*Session, error) {
	if raw == "" {
		return nil, ErrInvalidToken
	}
	hash := hashToken(raw)
	rt, err := s.st.RefreshTokenByHash(ctx, hash)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	if rt.RevokedAt != nil {
		if now.Sub(*rt.RevokedAt) > reuseGrace {
			// A rotated token came back: assume it was stolen and end the session.
			if err := s.st.RevokeFamily(ctx, rt.FamilyID, now); err != nil {
				return nil, err
			}
			return nil, ErrTokenReuse
		}
		return nil, ErrInvalidToken
	}
	if !now.Before(rt.ExpiresAt) {
		return nil, ErrInvalidToken
	}
	u, err := s.st.UserByID(ctx, rt.UserID)
	if err != nil || u.Disabled {
		_ = s.st.RevokeFamily(ctx, rt.FamilyID, now)
		return nil, ErrInvalidToken
	}
	if s.sys != nil {
		if err := s.checkSystemUser(ctx, u, now); err != nil {
			return nil, err
		}
	}
	sess, err := s.issue(ctx, u, rt.FamilyID, rt.FamilyExpiresAt, meta, hash)
	if errors.Is(err, store.ErrConflict) {
		return nil, ErrInvalidToken
	}
	return sess, err
}

func (s *Service) Logout(ctx context.Context, raw string) error {
	if raw == "" {
		return nil
	}
	rt, err := s.st.RefreshTokenByHash(ctx, hashToken(raw))
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.st.RevokeFamily(ctx, rt.FamilyID, s.now())
}

// Authenticate validates an access token and returns the current user. It
// re-reads the user and session so disabling an account or logging out takes
// effect immediately instead of when the token expires.
func (s *Service) Authenticate(ctx context.Context, token string) (*store.User, error) {
	claims, err := s.iss.Verify(token)
	if err != nil {
		return nil, err
	}
	active, err := s.st.FamilyActive(ctx, claims.SessionID, s.now())
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, ErrInvalidToken
	}
	u, err := s.st.UserByID(ctx, claims.Subject)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	if u.Disabled {
		return nil, ErrInvalidToken
	}
	return u, nil
}

func (s *Service) startSession(ctx context.Context, u *store.User, meta ClientMeta) (*Session, error) {
	return s.issue(ctx, u, store.NewID(), s.now().Add(s.opt.SessionMaxAge), meta, "")
}

// issue mints an access token plus a refresh token in family. When rotateFrom
// is set, that token is revoked in the same transaction.
func (s *Service) issue(ctx context.Context, u *store.User, family string, familyExp time.Time, meta ClientMeta, rotateFrom string) (*Session, error) {
	now := s.now()
	raw, hash, err := newRefreshToken()
	if err != nil {
		return nil, err
	}
	exp := now.Add(s.opt.RefreshTTL)
	if exp.After(familyExp) {
		exp = familyExp
	}
	rt := &store.RefreshToken{
		TokenHash:       hash,
		FamilyID:        family,
		UserID:          u.ID,
		CreatedAt:       now,
		ExpiresAt:       exp,
		FamilyExpiresAt: familyExp,
		UserAgent:       truncate(meta.UserAgent, 256),
		IP:              meta.IP,
	}
	if rotateFrom == "" {
		err = s.st.InsertRefreshToken(ctx, rt)
	} else {
		err = s.st.RotateRefreshToken(ctx, rotateFrom, rt, now)
	}
	if err != nil {
		return nil, err
	}
	access, accessExp, err := s.iss.Issue(u, family, now)
	if err != nil {
		return nil, err
	}
	return &Session{
		AccessToken:    access,
		AccessExpires:  accessExp,
		RefreshToken:   raw,
		RefreshExpires: exp,
		User:           u,
	}, nil
}

// Refresh tokens are 256-bit random values; only their SHA-256 is stored, so a
// database leak does not yield usable tokens.
func newRefreshToken() (raw, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, hashToken(raw), nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
