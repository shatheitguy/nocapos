package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"alfaos/alfad/internal/store"
)

type plainSealer struct{}

func (plainSealer) Seal(s string) []byte          { return []byte(s) }
func (plainSealer) Open(b []byte) (string, error) { return string(b), nil }

const testPassword = "correct-horse-battery"

func newTestService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := NewService(st, NewIssuer([]byte("0123456789abcdef0123456789abcdef"), time.Minute), Options{RefreshTTL: time.Hour, SessionMaxAge: time.Hour})
	svc.SetSealer(plainSealer{})
	return svc, st
}

func addUser(t *testing.T, st *store.Store, name, role string) *store.User {
	t.Helper()
	hash, err := HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	u := &store.User{ID: store.NewID(), Username: name, PasswordHash: hash, Role: role, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

// codeAt is the authenticator code for a moment.
func codeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	c, ok := totpCode(secret, uint64(at.Unix())/uint64(totpPeriod.Seconds()))
	if !ok {
		t.Fatal("bad secret")
	}
	return c
}

// enroll turns 2FA on for u with the code from one step ago (so the current
// step is still free for signing in) and returns the secret.
func enroll(t *testing.T, svc *Service, u *store.User) string {
	t.Helper()
	ctx := context.Background()
	secret, _, err := svc.BeginTOTP(ctx, u.ID, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.EnableTOTP(ctx, u.ID, codeAt(t, secret, svc.now().Add(-totpPeriod))); err != nil {
		t.Fatal(err)
	}
	return secret
}

func TestLoginNeedsCodeWhen2FAIsOn(t *testing.T) {
	ctx := context.Background()
	svc, st := newTestService(t)
	u := addUser(t, st, "alex", store.RoleUser)
	secret := enroll(t, svc, u)

	sess, err := svc.Login(ctx, "alex", testPassword, ClientMeta{})
	var ch *MFAChallenge
	if sess != nil || !errors.As(err, &ch) || ch.Token == "" {
		t.Fatalf("password alone: sess=%v err=%v", sess, err)
	}
	if ch.ExpiresAt.Sub(time.Now()) > 5*time.Minute {
		t.Errorf("challenge lives too long: %v", ch.ExpiresAt)
	}
	if _, err := svc.LoginTOTP(ctx, ch.Token, "000000", ClientMeta{}); !errors.Is(err, ErrTOTPBadCode) && codeAt(t, secret, time.Now()) != "000000" {
		t.Fatalf("wrong code: %v", err)
	}
	code := codeAt(t, secret, time.Now())
	sess, err = svc.LoginTOTP(ctx, ch.Token, code, ClientMeta{})
	if err != nil || sess == nil || sess.AccessToken == "" {
		t.Fatalf("password + code: %v", err)
	}
	// The challenge is single-use…
	if _, err := svc.LoginTOTP(ctx, ch.Token, code, ClientMeta{}); !errors.Is(err, ErrMFAExpired) {
		t.Errorf("challenge reused: %v", err)
	}
	// …and so is the code, even with a fresh challenge.
	_, err = svc.Login(ctx, "alex", testPassword, ClientMeta{})
	if !errors.As(err, &ch) {
		t.Fatal(err)
	}
	if _, err := svc.LoginTOTP(ctx, ch.Token, code, ClientMeta{}); !errors.Is(err, ErrTOTPBadCode) {
		t.Errorf("replayed code accepted: %v", err)
	}
	// A made-up challenge goes nowhere.
	if _, err := svc.LoginTOTP(ctx, "not-a-token", code, ClientMeta{}); !errors.Is(err, ErrMFAExpired) {
		t.Errorf("bogus challenge: %v", err)
	}
}

func TestMFAChallengeExpires(t *testing.T) {
	ctx := context.Background()
	svc, st := newTestService(t)
	u := addUser(t, st, "alex", store.RoleUser)
	secret := enroll(t, svc, u)
	_, err := svc.Login(ctx, "alex", testPassword, ClientMeta{})
	var ch *MFAChallenge
	if !errors.As(err, &ch) {
		t.Fatal(err)
	}
	later := time.Now().Add(6 * time.Minute)
	svc.now = func() time.Time { return later }
	if _, err := svc.LoginTOTP(ctx, ch.Token, codeAt(t, secret, later), ClientMeta{}); !errors.Is(err, ErrMFAExpired) {
		t.Errorf("expired challenge: %v", err)
	}
}

func TestAccountLockout(t *testing.T) {
	ctx := context.Background()
	svc, st := newTestService(t)
	addUser(t, st, "admin", store.RoleAdmin)
	now := time.Now()
	svc.lock.now = func() time.Time { return now }
	for i := 0; i < lockoutThreshold; i++ {
		if _, err := svc.Login(ctx, "admin", "wrong-password", ClientMeta{IP: fmt.Sprintf("10.0.0.%d", i)}); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	// Locked, whatever address the right password now comes from.
	if _, err := svc.Login(ctx, "ADMIN", testPassword, ClientMeta{IP: "10.9.9.9"}); !errors.Is(err, ErrLocked) {
		t.Fatalf("locked account signed in: %v", err)
	}
	now = now.Add(lockoutDuration + time.Second)
	if _, err := svc.Login(ctx, "admin", testPassword, ClientMeta{}); err != nil {
		t.Fatalf("after the wait: %v", err)
	}
}

func TestWrongCodesLockTheAccount(t *testing.T) {
	ctx := context.Background()
	svc, st := newTestService(t)
	u := addUser(t, st, "alex", store.RoleUser)
	secret := enroll(t, svc, u)
	wrong := "000000"
	if codeAt(t, secret, time.Now()) == wrong {
		wrong = "111111"
	}
	// A new challenge after every few codes doesn't reset the count: only a
	// full sign-in does.
	for i := 0; i < lockoutThreshold; i++ {
		_, err := svc.Login(ctx, "alex", testPassword, ClientMeta{})
		var ch *MFAChallenge
		if !errors.As(err, &ch) {
			t.Fatalf("attempt %d: %v", i, err)
		}
		_, _ = svc.LoginTOTP(ctx, ch.Token, wrong, ClientMeta{})
	}
	if _, err := svc.Login(ctx, "alex", testPassword, ClientMeta{}); !errors.Is(err, ErrLocked) {
		t.Errorf("guessing codes didn't lock the account: %v", err)
	}
}

func TestTOTPSetupAndDisableNeedProof(t *testing.T) {
	ctx := context.Background()
	svc, st := newTestService(t)
	u := addUser(t, st, "alex", store.RoleUser)
	if _, _, err := svc.BeginTOTP(ctx, u.ID, "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("setup without the password: %v", err)
	}
	secret := enroll(t, svc, u)
	if _, _, err := svc.BeginTOTP(ctx, u.ID, testPassword); !errors.Is(err, ErrTOTPEnabled) {
		t.Errorf("setup replaced a working authenticator: %v", err)
	}
	if err := svc.DisableTOTP(ctx, u.ID, testPassword, "12345"); !errors.Is(err, ErrTOTPBadCode) {
		t.Errorf("disable without a code: %v", err)
	}
	if err := svc.DisableTOTP(ctx, u.ID, "wrong-password", codeAt(t, secret, time.Now())); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("disable without the password: %v", err)
	}
	if err := svc.DisableTOTP(ctx, u.ID, testPassword, codeAt(t, secret, time.Now())); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if on, _ := svc.TOTPStatus(ctx, u.ID); on {
		t.Error("still on")
	}
}

func TestResetLocal(t *testing.T) {
	ctx := context.Background()
	svc, st := newTestService(t)
	u := addUser(t, st, "admin", store.RoleAdmin)
	enroll(t, svc, u)
	if err := svc.ResetLocal(ctx, "admin", "a-brand-new-password"); err != nil {
		t.Fatal(err)
	}
	if on, _ := svc.TOTPStatus(ctx, u.ID); on {
		t.Error("2FA still on after a local reset")
	}
	if _, err := svc.Login(ctx, "admin", "a-brand-new-password", ClientMeta{}); err != nil {
		t.Errorf("sign in with the new password: %v", err)
	}
}

type fakeSystem struct {
	role     string
	disabled bool
}

func (f fakeSystem) Verify(_ context.Context, _, pw string) (bool, error) {
	return pw == testPassword, nil
}
func (f fakeSystem) RoleOf(context.Context, string) (string, bool, error) {
	return f.role, f.disabled, nil
}
func (f fakeSystem) SetPassword(context.Context, string, string) error { return nil }

func TestDisabledStaysDisabledWithHostAccounts(t *testing.T) {
	ctx := context.Background()
	svc, st := newTestService(t)
	svc.SetSystemAccounts(fakeSystem{role: store.RoleUser})
	u := addUser(t, st, "sam", store.RoleUser)
	if err := st.SetDisabled(ctx, u.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(ctx, "sam", testPassword, ClientMeta{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("disabled person signed in: %v", err)
	}
	if got, _ := st.UserByID(ctx, u.ID); !got.Disabled {
		t.Error("sign-in re-enabled a disabled account")
	}
	if _, _, ok := svc.CheckPassword(ctx, "sam", testPassword); ok {
		t.Error("disabled person passed the file sharing password check")
	}
}

func TestSessionsEndAfterReboot(t *testing.T) {
	ctx := context.Background()
	svc, st := newTestService(t)
	addUser(t, st, "alex", store.RoleUser)
	if _, err := EndSessionsAfterReboot(ctx, st, "boot:one"); err != nil {
		t.Fatal(err)
	}
	sess, err := svc.Login(ctx, "alex", testPassword, ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	// Same boot (NoCapOS restarted): sessions stay.
	if n, err := EndSessionsAfterReboot(ctx, st, "boot:one"); err != nil || n != 0 {
		t.Fatalf("same boot: n=%d err=%v", n, err)
	}
	if _, err := svc.Authenticate(ctx, sess.AccessToken); err != nil {
		t.Fatalf("session lost without a reboot: %v", err)
	}
	// New boot: everyone signs in again.
	if n, err := EndSessionsAfterReboot(ctx, st, "boot:two"); err != nil || n == 0 {
		t.Fatalf("new boot: n=%d err=%v", n, err)
	}
	if _, err := svc.Authenticate(ctx, sess.AccessToken); err == nil {
		t.Error("session survived a reboot")
	}
	if _, err := svc.Refresh(ctx, sess.RefreshToken, ClientMeta{}); err == nil {
		t.Error("refresh survived a reboot")
	}
}

func TestLimiterStaysBounded(t *testing.T) {
	l := NewLimiter(time.Hour, 2)
	for i := 0; i < maxBuckets+100; i++ {
		l.Allow(fmt.Sprintf("k%d", i)) // each bucket is part-used, so none can be swept
	}
	if len(l.buckets) > maxBuckets {
		t.Errorf("limiter grew to %d buckets", len(l.buckets))
	}
}

func TestLockoutKeepsLockedAccountsUnderFlood(t *testing.T) {
	l := NewLockout()
	for i := 0; i < lockoutThreshold; i++ {
		l.Fail("admin")
	}
	for i := 0; i < maxLockoutKeys+100; i++ {
		l.Fail(fmt.Sprintf("u%d", i))
	}
	if len(l.m) > maxLockoutKeys {
		t.Errorf("lockout table grew to %d", len(l.m))
	}
	if !l.Locked("admin") {
		t.Error("a flood of other names lifted the lockout")
	}
}
