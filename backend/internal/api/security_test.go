package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"alfaos/alfad/internal/accounts"
	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/config"
	"alfaos/alfad/internal/store"
	"alfaos/alfad/internal/ws"
)

type plainBox struct{}

func (plainBox) Seal(s string) []byte          { return []byte(s) }
func (plainBox) Open(b []byte) (string, error) { return string(b), nil }

const pw = "correct-horse-battery"

type authWorld struct {
	s     *Server
	st    *store.Store
	users map[string]*store.User
}

func newAuthWorld(t *testing.T) *authWorld {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := auth.NewService(st, auth.NewIssuer([]byte("0123456789abcdef0123456789abcdef"), time.Minute), auth.Options{RefreshTTL: time.Hour, SessionMaxAge: time.Hour})
	svc.SetSealer(plainBox{})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{
		Deps:         Deps{Config: &config.Config{}, Store: st, Auth: svc, Log: log, Accounts: accounts.NewApp(st)},
		tickets:      auth.NewTicketStore(30 * time.Second),
		loginLimiter: auth.NewLimiter(time.Millisecond, 1000),
	}
	s.hub = ws.NewHub(ctx, s.resolveTopic, log)
	w := &authWorld{s: s, st: st, users: map[string]*store.User{}}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"boss", "admin2", "alex"} {
		role := store.RoleAdmin
		if name == "alex" {
			role = store.RoleUser
		}
		u := &store.User{ID: store.NewID(), Username: name, PasswordHash: hash, Role: role, CreatedAt: time.Now(), UpdatedAt: time.Now()}
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		w.users[name] = u
	}
	return w
}

func (w *authWorld) do(t *testing.T, h http.HandlerFunc, as *store.User, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	r := httptest.NewRequest(method, target, &buf)
	r.Header.Set("Content-Type", "application/json")
	if as != nil {
		r = r.WithContext(context.WithValue(r.Context(), userKey, as))
	}
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

// totpNow computes the current authenticator code (RFC 6238, as the app would).
func totpNow(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(at.Unix()/30))
	m := hmac.New(sha1.New, key)
	m.Write(b[:])
	sum := m.Sum(nil)
	o := sum[len(sum)-1] & 0x0f
	v := (uint32(sum[o]&0x7f)<<24 | uint32(sum[o+1])<<16 | uint32(sum[o+2])<<8 | uint32(sum[o+3])) % 1000000
	return fmt.Sprintf("%06d", v)
}

func TestLoginEndpointsWith2FA(t *testing.T) {
	w := newAuthWorld(t)
	alex := w.users["alex"]

	// Setting up 2FA needs the password.
	if rec := w.do(t, w.s.totpSetup, alex, "POST", "/api/v1/auth/totp/setup", map[string]string{}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("setup without password: %d %s", rec.Code, rec.Body)
	}
	rec := w.do(t, w.s.totpSetup, alex, "POST", "/api/v1/auth/totp/setup", map[string]string{"password": pw})
	if rec.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body)
	}
	var setup struct{ Secret string }
	_ = json.Unmarshal(rec.Body.Bytes(), &setup)
	if rec := w.do(t, w.s.totpEnable, alex, "POST", "/api/v1/auth/totp/enable", map[string]string{"code": totpNow(t, setup.Secret, time.Now().Add(-30*time.Second))}); rec.Code != http.StatusNoContent {
		t.Fatalf("enable: %d %s", rec.Code, rec.Body)
	}
	if rec := w.do(t, w.s.totpSetup, alex, "POST", "/api/v1/auth/totp/setup", map[string]string{"password": pw}); rec.Code != http.StatusConflict {
		t.Fatalf("setup while on: %d %s", rec.Code, rec.Body)
	}

	// The password alone gives no session, only a challenge.
	rec = w.do(t, w.s.authLogin, nil, "POST", "/api/v1/auth/login", map[string]string{"username": "alex", "password": pw})
	var ch struct {
		MFARequired bool   `json:"mfa_required"`
		MFAToken    string `json:"mfa_token"`
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &ch)
	if rec.Code != http.StatusOK || !ch.MFARequired || ch.MFAToken == "" || ch.AccessToken != "" || rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("password step: %d %s", rec.Code, rec.Body)
	}
	code := totpNow(t, setup.Secret, time.Now())
	rec = w.do(t, w.s.authLoginTOTP, nil, "POST", "/api/v1/auth/login/totp", map[string]string{"mfa_token": ch.MFAToken, "code": code})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "access_token") {
		t.Fatalf("code step: %d %s", rec.Code, rec.Body)
	}
	// The same code again, on a fresh challenge, is refused.
	rec = w.do(t, w.s.authLogin, nil, "POST", "/api/v1/auth/login", map[string]string{"username": "alex", "password": pw})
	_ = json.Unmarshal(rec.Body.Bytes(), &ch)
	if rec := w.do(t, w.s.authLoginTOTP, nil, "POST", "/api/v1/auth/login/totp", map[string]string{"mfa_token": ch.MFAToken, "code": code}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("replayed code: %d %s", rec.Code, rec.Body)
	}
	// No password reset by code.
	if rec := w.do(t, w.s.authReset, nil, "POST", "/api/v1/auth/reset", map[string]string{"username": "alex", "code": code, "password": "new-password-123"}); rec.Code != http.StatusGone {
		t.Fatalf("reset: %d", rec.Code)
	}
	// Turning 2FA off needs password and a code, and starts no session.
	if rec := w.do(t, w.s.totpDisable, alex, "POST", "/api/v1/auth/totp/disable", map[string]string{"password": pw}); rec.Code != http.StatusBadRequest {
		t.Fatalf("disable without code: %d %s", rec.Code, rec.Body)
	}
	if rec := w.do(t, w.s.totpDisable, alex, "POST", "/api/v1/auth/totp/disable", map[string]string{"password": pw, "code": totpNow(t, setup.Secret, time.Now().Add(30*time.Second))}); rec.Code != http.StatusNoContent || rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
}

// A demoted administrator's open WebSocket is closed at once, and
// subscribing re-checks the role.
func TestDemotionClosesLiveConnections(t *testing.T) {
	w := newAuthWorld(t)
	boss, admin2 := w.users["boss"], w.users["admin2"]
	srv := httptest.NewServer(http.HandlerFunc(w.s.wsConnect))
	defer srv.Close()

	tok, _ := w.s.tickets.Issue(admin2.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws?ticket="+tok, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	deadline := time.Now().Add(5 * time.Second)
	for w.s.live.count(admin2.ID) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if w.s.live.count(admin2.ID) != 1 {
		t.Fatal("connection not tracked")
	}

	req := httptest.NewRequest("PATCH", "/api/v1/users/admin2", strings.NewReader(`{"role":"user"}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("name", "admin2")
	req = req.WithContext(context.WithValue(req.Context(), userKey, boss))
	rec := httptest.NewRecorder()
	w.s.usersUpdate(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("demote: %d %s", rec.Code, rec.Body)
	}
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("demoted administrator's WebSocket still open")
	}
	if w.s.live.count(admin2.ID) != 0 {
		t.Error("connection still tracked")
	}
	// The old record (a stale admin) can't subscribe to admin feeds.
	if _, err := w.s.resolveTopic("docker.events", admin2); err == nil {
		t.Error("stale admin role accepted on subscribe")
	}
}

func TestFileLinksRecheckFilesOnly(t *testing.T) {
	w := newAuthWorld(t)
	alex := w.users["alex"]
	ctx := context.Background()
	if err := w.st.SetUserAccess(ctx, "alex", store.AccessFiles); err != nil {
		t.Fatal(err)
	}
	// (Without spaces only administrators hold file links; the access check
	// itself is what must refuse here.)
	if err := w.s.Auth.DesktopAllowed(ctx, alex); err == nil {
		t.Error("file-sharing-only account still allowed the desktop")
	}
	if w.s.ticketHolderCan(ctx, alex, "drive", "/x") {
		t.Error("file-sharing-only account still opens file links")
	}
}

func TestUserContentHeaders(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for _, tc := range []struct{ path, want, not string }{
		{"/dav/Media/evil.html", "sandbox", "script-src"},
		{"/", "script-src nocap.local:8443/assets/", "script-src 'self'"},
	} {
		r := httptest.NewRequest("GET", tc.path, nil)
		r.Host = "nocap.local:8443"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		csp := rec.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, tc.want) || strings.Contains(csp, tc.not) {
			t.Errorf("%s: CSP %q", tc.path, csp)
		}
	}
	// A Host header that could smuggle directives keeps the plain policy.
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "evil; script-src *"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if csp := rec.Header().Get("Content-Security-Policy"); strings.Contains(csp, "evil") || !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("odd host: CSP %q", csp)
	}
}
