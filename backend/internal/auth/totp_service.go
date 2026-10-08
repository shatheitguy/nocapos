package auth

import (
	"context"
	"errors"

	"alfaos/alfad/internal/store"
)

var (
	ErrTOTPNotEnrolled = errors.New("two-factor authentication is not set up")
	ErrTOTPBadCode     = errors.New("that code is not valid — check your authenticator app")
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
// the secret and the otpauth URI for the QR code. A second call replaces an
// in-progress (not yet enabled) enrolment.
func (s *Service) BeginTOTP(ctx context.Context, userID string) (secret, uri string, err error) {
	if s.box == nil {
		return "", "", ErrNoSealer
	}
	u, err := s.st.UserByID(ctx, userID)
	if err != nil {
		return "", "", err
	}
	secret = NewTOTPSecret()
	if err := s.st.SetTOTP(ctx, userID, s.box.Seal(secret), false); err != nil {
		return "", "", err
	}
	return secret, TOTPURI(totpIssuer, u.Username, secret), nil
}

// EnableTOTP verifies a code against the pending secret and turns 2FA on.
func (s *Service) EnableTOTP(ctx context.Context, userID, code string) error {
	secret, err := s.totpSecret(ctx, userID)
	if err != nil {
		return err
	}
	if !VerifyTOTP(secret, code) {
		return ErrTOTPBadCode
	}
	return s.st.SetTOTP(ctx, userID, s.box.Seal(secret), true)
}

// DisableTOTP turns 2FA off (the password was already checked by the handler).
func (s *Service) DisableTOTP(ctx context.Context, userID string) error {
	return s.st.SetTOTP(ctx, userID, nil, false)
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
	u, err := s.st.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	var ok bool
	if s.sys != nil {
		ok, err = s.sys.Verify(ctx, u.Username, current)
	} else {
		ok, err = VerifyPassword(current, u.PasswordHash)
	}
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidCredentials
	}
	return s.setPassword(ctx, u, next)
}

// ResetPassword is the "forgot password" flow: with a valid TOTP code, set a
// new password without the old one. Requires the user to have 2FA enabled.
func (s *Service) ResetPassword(ctx context.Context, username, code, next string) error {
	u, err := s.st.UserByUsername(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		// Run a verify anyway to keep timing uniform, then fail.
		VerifyTOTP(NewTOTPSecret(), code)
		return ErrInvalidCredentials
	}
	if err != nil {
		return err
	}
	if !u.TOTPEnabled || len(u.TOTPSecret) == 0 {
		return ErrTOTPNotEnrolled
	}
	secret, err := s.box.Open(u.TOTPSecret)
	if err != nil {
		return err
	}
	if !VerifyTOTP(secret, code) {
		return ErrTOTPBadCode
	}
	if err := s.setPassword(ctx, u, next); err != nil {
		return err
	}
	// Revoke every active session so a thief with the old password is logged out.
	return s.st.RevokeAllUserSessions(ctx, u.ID, s.now())
}

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
