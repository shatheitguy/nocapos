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
	ErrSetupDone          = errors.New("setup's already done — just sign in")
	ErrSetupToken         = errors.New("that setup token's not it — copy it again from the NoCapOS console")
	// ErrFilesOnly: the account is for file sharing only (SMB / WebDAV),
	// not for signing in to the desktop.
	ErrFilesOnly = errors.New("this account is for file sharing only; connect to the shared folders from your computer or phone instead")
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
	st   *store.Store
	iss  *Issuer
	opt  Options
	box  Sealer
	sys  SystemAccounts // nil = NoCapOS's own accounts
	now  func() time.Time
	lock *Lockout // per-account failed attempts (password and 2FA code)
	mfa  mfaStore // sign-ins waiting for their 2FA code
}

func NewService(st *store.Store, iss *Issuer, opt Options) *Service {
	return &Service{st: st, iss: iss, opt: opt, now: time.Now, lock: NewLockout()}
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

// Login checks a password. Accounts with 2FA get an *MFAChallenge error
// instead of a session (finish with LoginTOTP); ErrFilesOnly means the
// password was right but the account has no desktop. Failures count toward
// a per-account lockout (ErrLocked).
func (s *Service) Login(ctx context.Context, username, password string, meta ClientMeta) (*Session, error) {
	if s.lock.Locked(username) {
		_, _ = VerifyPassword(password, dummyHash()) // keep timing uniform
		return nil, ErrLocked
	}
	var (
		u   *store.User
		err error
	)
	if s.sys != nil {
		u, err = s.loginSystem(ctx, username, password)
	} else {
		u, err = s.loginApp(ctx, username, password)
	}
	if errors.Is(err, ErrInvalidCredentials) {
		s.lock.Fail(username)
	}
	if err != nil {
		return nil, err
	}
	if err := s.desktopAllowed(ctx, u); err != nil {
		return nil, err
	}
	if !u.TOTPEnabled {
		s.lock.Succeed(username) // with 2FA, only the code step clears failures
	}
	return s.challengeOrSession(ctx, u, meta)
}

func (s *Service) loginApp(ctx context.Context, username, password string) (*store.User, error) {
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
	return u, nil
}

// ConfirmPassword re-checks a signed-in person's password before a sensitive
// change (2FA settings, a new password). Wrong passwords count toward the
// account's lockout.
func (s *Service) ConfirmPassword(ctx context.Context, userID, password string) error {
	u, err := s.st.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if s.lock.Locked(u.Username) {
		return ErrLocked
	}
	var ok bool
	if s.sys != nil {
		ok, err = s.sys.Verify(ctx, u.Username, password)
	} else {
		ok, err = VerifyPassword(password, u.PasswordHash)
	}
	if err != nil {
		return err
	}
	if !ok {
		s.lock.Fail(u.Username)
		return ErrInvalidCredentials
	}
	return nil
}

// DesktopAllowed reports (nil) whether a person may use the desktop right
// now: not disabled and not a file-sharing-only account.
func (s *Service) DesktopAllowed(ctx context.Context, u *store.User) error {
	if u.Disabled {
		return ErrInvalidToken
	}
	return s.desktopAllowed(ctx, u)
}

// desktopAllowed refuses web sessions to file-sharing-only accounts.
// Administrators always have the desktop.
func (s *Service) desktopAllowed(ctx context.Context, u *store.User) error {
	if u.Role == store.RoleAdmin {
		return nil
	}
	access, err := s.st.UserAccess(ctx, u.Username)
	if err != nil {
		return err
	}
	if access == store.AccessFiles {
		return ErrFilesOnly
	}
	return nil
}

// CheckPassword verifies someone's password without starting a session,
// for the file sharing services (WebDAV). It returns the user name as
// stored and whether they're an administrator; disabled accounts fail.
func (s *Service) CheckPassword(ctx context.Context, username, password string) (string, bool, bool) {
	if s.sys != nil {
		role, disabled, err := s.sys.RoleOf(ctx, username)
		if err != nil || disabled {
			_, _ = VerifyPassword(password, dummyHash())
			return "", false, false
		}
		// Disabled in NoCapOS counts too, even if the host account isn't locked.
		if u, err := s.st.UserByUsername(ctx, username); err == nil && u.Disabled {
			_, _ = VerifyPassword(password, dummyHash())
			return "", false, false
		}
		if ok, err := s.sys.Verify(ctx, username, password); err != nil || !ok {
			return "", false, false
		}
		return username, role == store.RoleAdmin, true
	}
	u, err := s.st.UserByUsername(ctx, username)
	if err != nil {
		_, _ = VerifyPassword(password, dummyHash())
		return "", false, false
	}
	ok, err := VerifyPassword(password, u.PasswordHash)
	if err != nil || !ok || u.Disabled {
		return "", false, false
	}
	return u.Username, u.Role == store.RoleAdmin, true
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
	if err := s.desktopAllowed(ctx, u); err != nil {
		_ = s.st.RevokeFamily(ctx, rt.FamilyID, now)
		return nil, ErrInvalidToken
	}
	sess, err := s.issue(ctx, u, rt.FamilyID, rt.FamilyExpiresAt, meta, hash)
	if errors.Is(err, store.ErrConflict) {
		return nil, ErrInvalidToken
	}
	return sess, err
}

// Logout ends the session the refresh token belongs to and returns whose
// it was and its ID (empty when there was none).
func (s *Service) Logout(ctx context.Context, raw string) (userID, sessionID string, err error) {
	if raw == "" {
		return "", "", nil
	}
	rt, err := s.st.RefreshTokenByHash(ctx, hashToken(raw))
	if errors.Is(err, store.ErrNotFound) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	return rt.UserID, rt.FamilyID, s.st.RevokeFamily(ctx, rt.FamilyID, s.now())
}

// Authenticate validates an access token and returns the current user. It
// re-reads the user and session so disabling an account or logging out takes
// effect immediately instead of when the token expires.
func (s *Service) Authenticate(ctx context.Context, token string) (*store.User, error) {
	u, _, err := s.AuthenticateSession(ctx, token)
	return u, err
}

// AuthenticateSession is Authenticate that also returns the session (refresh
// token family) the access token belongs to.
func (s *Service) AuthenticateSession(ctx context.Context, token string) (*store.User, string, error) {
	u, claims, err := s.authenticate(ctx, token)
	if err != nil {
		return nil, "", err
	}
	return u, claims.SessionID, nil
}

// SessionActive reports whether a login session is still live.
func (s *Service) SessionActive(ctx context.Context, sessionID string) (bool, error) {
	return s.st.FamilyActive(ctx, sessionID, s.now())
}

func (s *Service) authenticate(ctx context.Context, token string) (*store.User, *Claims, error) {
	claims, err := s.iss.Verify(token)
	if err != nil {
		return nil, nil, err
	}
	active, err := s.st.FamilyActive(ctx, claims.SessionID, s.now())
	if err != nil {
		return nil, nil, err
	}
	if !active {
		return nil, nil, ErrInvalidToken
	}
	u, err := s.st.UserByID(ctx, claims.Subject)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, ErrInvalidToken
	}
	if err != nil {
		return nil, nil, err
	}
	if u.Disabled {
		return nil, nil, ErrInvalidToken
	}
	if err := s.desktopAllowed(ctx, u); errors.Is(err, ErrFilesOnly) {
		return nil, nil, ErrInvalidToken
	} else if err != nil {
		return nil, nil, err
	}
	return u, claims, nil
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
