package auth

import (
	"context"
	"errors"
	"sync"
	"time"

	"alfaos/alfad/internal/store"
)

// Two-step sign-in. When an account has 2FA on, a right password doesn't
// start a session: it returns a short-lived, single-use challenge token, and
// the session only starts when that token comes back with a valid code.
// Only the token's SHA-256 is kept, in memory.

const (
	mfaTTL      = 5 * time.Minute
	mfaMaxTries = 5
	maxMFA      = 10000
)

// ErrMFAExpired: the challenge is unknown, used up or too old.
var ErrMFAExpired = errors.New("that sign-in timed out — enter your password again")

// MFAChallenge is returned (as an error) by Login when the account needs a
// code from its authenticator app to finish signing in.
type MFAChallenge struct {
	Token     string
	ExpiresAt time.Time
}

func (c *MFAChallenge) Error() string { return "a code from your authenticator app is required" }

type mfaPending struct {
	userID   string
	username string
	exp      time.Time
	tries    int
}

type mfaStore struct {
	mu sync.Mutex
	m  map[string]*mfaPending // sha256(token) → pending sign-in
}

func (s *mfaStore) issue(u *store.User, now time.Time) (*MFAChallenge, error) {
	raw, hash, err := newRefreshToken()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = make(map[string]*mfaPending)
	}
	for k, p := range s.m {
		if !now.Before(p.exp) {
			delete(s.m, k)
		}
	}
	if len(s.m) >= maxMFA {
		var oldest string
		var at time.Time
		for k, p := range s.m {
			if oldest == "" || p.exp.Before(at) {
				oldest, at = k, p.exp
			}
		}
		delete(s.m, oldest)
	}
	exp := now.Add(mfaTTL)
	s.m[hash] = &mfaPending{userID: u.ID, username: u.Username, exp: exp}
	return &MFAChallenge{Token: raw, ExpiresAt: exp}, nil
}

// get returns a live challenge without using it up.
func (s *mfaStore) get(raw string, now time.Time) (mfaPending, bool) {
	if raw == "" {
		return mfaPending{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.m[hashToken(raw)]
	if !ok || !now.Before(p.exp) {
		return mfaPending{}, false
	}
	return *p, true
}

// failed counts a wrong code; the challenge ends after mfaMaxTries.
func (s *mfaStore) failed(raw string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := hashToken(raw)
	if p, ok := s.m[k]; ok {
		p.tries++
		if p.tries >= mfaMaxTries {
			delete(s.m, k)
		}
	}
}

// consume uses the challenge up; false if someone else already did.
func (s *mfaStore) consume(raw string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := hashToken(raw)
	if _, ok := s.m[k]; !ok {
		return false
	}
	delete(s.m, k)
	return true
}

// challengeOrSession finishes a correct password: accounts with 2FA get a
// challenge, everyone else a session.
func (s *Service) challengeOrSession(ctx context.Context, u *store.User, meta ClientMeta) (*Session, error) {
	if u.TOTPEnabled && len(u.TOTPSecret) > 0 {
		c, err := s.mfa.issue(u, s.now())
		if err != nil {
			return nil, err
		}
		return nil, c
	}
	return s.startSession(ctx, u, meta)
}

// LoginTOTP is the second sign-in step: the challenge from Login plus a code
// from the authenticator app. Codes work once, and wrong codes count toward
// the account's lockout like wrong passwords.
func (s *Service) LoginTOTP(ctx context.Context, token, code string, meta ClientMeta) (*Session, error) {
	now := s.now()
	p, ok := s.mfa.get(token, now)
	if !ok {
		return nil, ErrMFAExpired
	}
	if s.lock.Locked(p.username) {
		s.mfa.consume(token)
		return nil, ErrLocked
	}
	u, err := s.st.UserByID(ctx, p.userID)
	if errors.Is(err, store.ErrNotFound) {
		s.mfa.consume(token)
		return nil, ErrMFAExpired
	}
	if err != nil {
		return nil, err
	}
	if u.Disabled || !u.TOTPEnabled || len(u.TOTPSecret) == 0 || s.box == nil {
		s.mfa.consume(token)
		return nil, ErrMFAExpired
	}
	secret, err := s.box.Open(u.TOTPSecret)
	if err != nil {
		return nil, err
	}
	step, ok := MatchTOTP(secret, code, now)
	if ok {
		// Each code once: a step at or before the last accepted one is refused.
		if ok, err = s.st.UseTOTPStep(ctx, u.ID, step); err != nil {
			return nil, err
		}
	}
	if !ok {
		s.mfa.failed(token)
		s.lock.Fail(u.Username)
		return nil, ErrTOTPBadCode
	}
	if !s.mfa.consume(token) {
		return nil, ErrMFAExpired
	}
	s.lock.Succeed(u.Username)
	if s.sys != nil {
		// The host account may have been locked or demoted meanwhile.
		if err := s.checkSystemUser(ctx, u, now); err != nil {
			return nil, ErrInvalidCredentials
		}
	}
	if err := s.desktopAllowed(ctx, u); err != nil {
		return nil, err
	}
	return s.startSession(ctx, u, meta)
}
