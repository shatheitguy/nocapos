package auth

import (
	"context"
	"errors"

	"alfaos/alfad/internal/store"
)

var (
	ErrTOTPNotEnrolled = errors.New("two-factor authentication is not set up")
	ErrTOTPBadCode     = errors.New("that code is not valid — check your authenticator app")
	ErrTOTPEnabled     = errors.New("two-factor authentication is already on; turn it off first to set it up again")
	ErrNoSealer        = errors.New("secret storage is not configured")
)

const totpIssuer = "NoCapOS"

// TOTPStatus reports whether a user has 2FA enabled.
func (s *Service) TOTPStatus(ctx context.Context, userID string) (bool, error) {
	u, err := s.st.UserByID(ctx, userID)
	if err != nil {
		return false, err
	}
	return u.TOTPEnabled, nil
}

// BeginTOTP generates a fresh secret and stores it (not yet enabled). Returns
// the secret and the otpauth URI for the QR code. It needs the current
// password, and refuses while 2FA is on: replacing a working authenticator
// goes through DisableTOTP (password and code) first. A second call replaces
// an in-progress (not yet enabled) enrolment.
func (s *Service) BeginTOTP(ctx context.Context, userID, password string) (secret, uri string, err error) {
	if s.box == nil {
		return "", "", ErrNoSealer
	}
	if err := s.ConfirmPassword(ctx, userID, password); err != nil {
		return "", "", err
	}
	u, err := s.st.UserByID(ctx, userID)
	if err != nil {
		return "", "", err
	}
	if u.TOTPEnabled {
		return "", "", ErrTOTPEnabled
	}
	secret = NewTOTPSecret()
	if err := s.st.SetTOTP(ctx, userID, s.box.Seal(secret), false); err != nil {
		return "", "", err
	}
	return secret, TOTPURI(totpIssuer, u.Username, secret), nil
}

// EnableTOTP verifies a code against the pending secret and turns 2FA on.
// The code is used up, so it can't also sign someone in.
func (s *Service) EnableTOTP(ctx context.Context, userID, code string) error {
	u, err := s.st.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.TOTPEnabled {
		return ErrTOTPEnabled
	}
	secret, err := s.totpSecret(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.useCode(ctx, u, secret, code); err != nil {
		return err
	}
	return s.st.SetTOTP(ctx, userID, s.box.Seal(secret), true)
}

// DisableTOTP turns 2FA off. It needs both the current password and a
// current code from the authenticator.
func (s *Service) DisableTOTP(ctx context.Context, userID, password, code string) error {
	if err := s.ConfirmPassword(ctx, userID, password); err != nil {
		return err
	}
	u, err := s.st.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if !u.TOTPEnabled {
		return s.st.SetTOTP(ctx, userID, nil, false) // drop a half-finished enrolment
	}
	secret, err := s.totpSecret(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.useCode(ctx, u, secret, code); err != nil {
		return err
	}
	return s.st.SetTOTP(ctx, userID, nil, false)
}

// useCode checks a code, refuses one that was already used, and counts
// wrong codes toward the account's lockout.
func (s *Service) useCode(ctx context.Context, u *store.User, secret, code string) error {
	if s.lock.Locked(u.Username) {
		return ErrLocked
	}
	step, ok := MatchTOTP(secret, code, s.now())
	if ok {
		var err error
		if ok, err = s.st.UseTOTPStep(ctx, u.ID, step); err != nil {
			return err
		}
	}
	if !ok {
		s.lock.Fail(u.Username)
		return ErrTOTPBadCode
	}
	return nil
}

func (s *Service) totpSecret(ctx context.Context, userID string) (string, error) {
	if s.box == nil {
		return "", ErrNoSealer
	}
	u, err := s.st.UserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	if len(u.TOTPSecret) == 0 {
		return "", ErrTOTPNotEnrolled
	}
	return s.box.Open(u.TOTPSecret)
}

// ChangePassword updates the password after verifying the current one.
func (s *Service) ChangePassword(ctx context.Context, userID, current, next string) error {
	if err := s.ConfirmPassword(ctx, userID, current); err != nil {
		return err
	}
	u, err := s.st.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	return s.setPassword(ctx, u, next)
}

// There is deliberately no "forgot password" reset here: a code from the
// authenticator alone must never be enough to take over an account. An
// administrator resets passwords in Users & Groups; a sole administrator
// uses `nocapos reset-password <user>` as root on the machine itself
// (see ResetLocal).

func (s *Service) setPassword(ctx context.Context, u *store.User, next string) error {
	if err := ValidatePassword(next); err != nil {
		return err
	}
	if s.sys != nil {
		return s.sys.SetPassword(ctx, u.Username, next) // the real Linux password
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	return s.st.SetPassword(ctx, u.ID, hash)
}

// ResetLocal is the local recovery for a locked-out (sole) administrator,
// run as root on the machine: it sets a new password, turns 2FA off and
// ends every session. Never reachable over the network.
func (s *Service) ResetLocal(ctx context.Context, username, next string) error {
	u, err := s.st.UserByUsername(ctx, username)
	if errors.Is(err, store.ErrNotFound) && s.sys != nil {
		// A Linux user who never signed in to NoCapOS: just the password.
		if _, _, rerr := s.sys.RoleOf(ctx, username); rerr != nil {
			return rerr
		}
		if err := ValidatePassword(next); err != nil {
			return err
		}
		return s.sys.SetPassword(ctx, username, next)
	}
	if err != nil {
		return err
	}
	if err := s.setPassword(ctx, u, next); err != nil {
		return err
	}
	if err := s.st.SetTOTP(ctx, u.ID, nil, false); err != nil {
		return err
	}
	return s.st.RevokeAllUserSessions(ctx, u.ID, s.now())
}
