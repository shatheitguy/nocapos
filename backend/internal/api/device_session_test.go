package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/hostaccess"
	"alfaos/alfad/internal/store"
)

// linkedDevice pairs a phone for as and returns its device token.
func linkedDevice(t *testing.T, w *authWorld, as *store.User) claimResponse {
	t.Helper()
	pc := newCode(t, w, as)
	rec := claim(t, w.s, "10.9.9.9", map[string]string{"code": pc.Code, "device_name": "Pixel", "platform": "android"})
	var cr claimResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &cr)
	if rec.Code != http.StatusCreated || cr.Token == "" {
		t.Fatalf("claim: %d %s", rec.Code, rec.Body)
	}
	return cr
}

func webTicket(t *testing.T, w *authWorld, token string) (deviceSessionResponse, int) {
	t.Helper()
	rec := asDevice(w.s, w.s.deviceSession, "POST", "/api/v1/device/session", token, "")
	var out deviceSessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out, rec.Code
}

func redeemLink(w *authWorld, ticket string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/v1/auth/link", strings.NewReader(`{"ticket":"`+ticket+`"}`))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "10.9.9.9:40000"
	rec := httptest.NewRecorder()
	w.s.authLink(rec, r)
	return rec
}

func TestDeviceWebSession(t *testing.T) {
	w := newAuthWorld(t)
	w.s.Config.DataDir = t.TempDir()
	alex := w.users["alex"]
	cr := linkedDevice(t, w, alex)

	ts, code := webTicket(t, w, cr.Token)
	if code != http.StatusOK || ts.Ticket == "" || time.Until(ts.ExpiresAt) > linkTicketTTL+time.Second {
		t.Fatalf("ticket: %d %+v", code, ts)
	}
	u, err := url.Parse(ts.URL)
	if err != nil || u.Host != "nocap.local:8443" || u.Path != "/" || u.Query().Get("link") != ts.Ticket {
		t.Fatalf("url %q", ts.URL)
	}

	// The ticket signs alex in, like a password would.
	rec := redeemLink(w, ts.Ticket)
	var sess sessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)
	if rec.Code != http.StatusOK || sess.AccessToken == "" || sess.User == nil || sess.User.ID != alex.ID {
		t.Fatalf("link: %d %s", rec.Code, rec.Body)
	}
	if c := rec.Result().Cookies(); len(c) == 0 || c[0].Name != refreshCookie || c[0].Value == "" {
		t.Fatalf("no refresh cookie: %v", c)
	}
	if got, err := w.s.Auth.Authenticate(context.Background(), sess.AccessToken); err != nil || got.ID != alex.ID {
		t.Fatalf("session not usable: %v", err)
	}

	// Single use, and junk is refused.
	if rec := redeemLink(w, ts.Ticket); rec.Code != http.StatusUnauthorized {
		t.Fatalf("reused ticket: %d", rec.Code)
	}
	if rec := redeemLink(w, "nope"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("junk ticket: %d", rec.Code)
	}
	if rec := redeemLink(w, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("empty ticket: %d", rec.Code)
	}

	// No device token, no ticket.
	if _, code := webTicket(t, w, ""); code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", code)
	}
}

func TestDeviceWebSessionRefusals(t *testing.T) {
	w := newAuthWorld(t)
	w.s.Config.DataDir = t.TempDir()
	ctx := context.Background()
	alex := w.users["alex"]
	cr := linkedDevice(t, w, alex)

	// Owner disabled after the ticket was issued.
	ts, _ := webTicket(t, w, cr.Token)
	_ = w.st.SetDisabled(ctx, alex.ID, true)
	if rec := redeemLink(w, ts.Ticket); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled owner: %d %s", rec.Code, rec.Body)
	}
	// ...and a disabled owner's device gets no ticket at all.
	if _, code := webTicket(t, w, cr.Token); code != http.StatusUnauthorized {
		t.Fatalf("disabled owner ticket: %d", code)
	}
	_ = w.st.SetDisabled(ctx, alex.ID, false)

	// File-sharing-only accounts get no desktop.
	ts, _ = webTicket(t, w, cr.Token)
	if err := w.st.SetUserAccess(ctx, alex.Username, store.AccessFiles); err != nil {
		t.Fatal(err)
	}
	if rec := redeemLink(w, ts.Ticket); rec.Code != http.StatusUnauthorized {
		t.Fatalf("files-only owner: %d %s", rec.Code, rec.Body)
	}
	_ = w.st.SetUserAccess(ctx, alex.Username, store.AccessDesktop)

	// Device unlinked after the ticket was issued.
	ts, _ = webTicket(t, w, cr.Token)
	if _, err := w.st.RevokeDevice(ctx, cr.DeviceID, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if rec := redeemLink(w, ts.Ticket); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "linked") {
		t.Fatalf("revoked device: %d %s", rec.Code, rec.Body)
	}

	// Tickets expire.
	cr = linkedDevice(t, w, alex)
	w.s.pairing.links = auth.NewTicketStore(time.Millisecond)
	ts, _ = webTicket(t, w, cr.Token)
	time.Sleep(5 * time.Millisecond)
	if rec := redeemLink(w, ts.Ticket); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired ticket: %d", rec.Code)
	}
}

func TestDeviceWebSessionLocalOnly(t *testing.T) {
	s, dir := localOnlyServer(t)
	if err := hostaccess.SetRemote(dir, false); err != nil {
		t.Fatal(err)
	}
	s.localGate.Refresh()
	h := s.localOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	for _, p := range []string{"/api/v1/device/session", "/api/v1/auth/link", "/?link=abc"} {
		r := httptest.NewRequest(http.MethodPost, p, nil)
		r.RemoteAddr = "192.168.1.50:5000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s from the LAN while local-only: %d", p, rec.Code)
		}
	}
}
