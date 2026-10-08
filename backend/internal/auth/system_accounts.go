package auth

import (
	"context"
	"errors"
	"time"

	"alfaos/alfad/internal/store"
)

// ErrNoAccount means the host has no such (login-capable) account.
var ErrNoAccount = errors.New("no such account")

// SystemAccounts lets NoCapOS sign people in with the host's own accounts
// (real Linux users) instead of its private user table. The private table
// still holds a row per person for sessions, 2FA and preferences.
type SystemAccounts interface {
	// Verify checks a password the way the system login does.
	Verify(ctx context.Context, username, password string) (bool, error)
	// RoleOf reports the account's NoCapOS role (admin for root and the
	// sudo/wheel group) and whether it is locked. ErrNoAccount if it's gone.
	RoleOf(ctx context.Context, username string) (role string, disabled bool, err error)
	// SetPassword changes the account's system password.
	SetPassword(ctx context.Context, username, password string) error
}

// SetSystemAccounts switches sign-in to the host's accounts.
func (s *Service) SetSystemAccounts(sa SystemAccounts) { s.sys = sa }

// SystemAccountsEnabled reports whether sign-in uses the host's accounts.
func (s *Service) SystemAccountsEnabled() bool { return s.sys != nil }

// loginSystem verifies against the host and makes sure a matching NoCapOS
// record exists with the account's current role.
func (s *Service) loginSystem(ctx context.Context, username, password string, meta ClientMeta) (*Session, error) {
	role, disabled, err := s.sys.RoleOf(ctx, username)
	if err != nil || disabled {
		_, _ = VerifyPassword(password, dummyHash()) // keep timing uniform
		if err != nil && !errors.Is(err, ErrNoAccount) {
			return nil, err
		}
		return nil, ErrInvalidCredentials
	}
	ok, err := s.sys.Verify(ctx, username, password)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrInvalidCredentials
	}
	u, err := s.syncSystemUser(ctx, username, role)
	if err != nil {
		return nil, err
	}
	return s.startSession(ctx, u, meta)
}

// syncSystemUser returns the NoCapOS record for a host account, creating it
// on first sign-in and keeping its role in step with the host.
func (s *Service) syncSystemUser(ctx context.Context, username, role string) (*store.User, error) {
	u, err := s.st.UserByUsername(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		// The password lives in the host's shadow file; store an unusable hash.
		hash, herr := HashPassword(NewTOTPSecret() + NewTOTPSecret())
		if herr != nil {
			return nil, herr
		}
		now := s.now()
		u = &store.User{ID: store.NewID(), Username: username, PasswordHash: hash, Role: role, CreatedAt: now, UpdatedAt: now}
		if err := s.st.CreateUser(ctx, u); err != nil {
			return nil, err
		}
		return u, nil
	}
	if err != nil {
		return nil, err
	}
	if u.Role != role {
		if err := s.st.SetRole(ctx, u.ID, role); err != nil {
			return nil, err
		}
		u.Role = role
	}
	if u.Disabled {
		if err := s.st.SetDisabled(ctx, u.ID, false); err != nil {
			return nil, err
		}
		u.Disabled = false
	}
	return u, nil
}

// checkSystemUser re-validates a host account on session refresh: deleted or
// locked accounts lose their sessions, and role changes take effect.
func (s *Service) checkSystemUser(ctx context.Context, u *store.User, now time.Time) error {
	role, disabled, err := s.sys.RoleOf(ctx, u.Username)
	if err != nil || disabled {
		_ = s.st.RevokeAllUserSessions(ctx, u.ID, now)
		return ErrInvalidToken
	}
	if role != u.Role {
		if err := s.st.SetRole(ctx, u.ID, role); err != nil {
			return err
		}
		u.Role = role
	}
	return nil
}
