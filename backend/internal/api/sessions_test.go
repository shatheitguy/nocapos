package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"alfaos/alfad/internal/notify"
	"alfaos/alfad/internal/store"
	"alfaos/alfad/internal/useragent"
)

const (
	uaChromeWin = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	uaFirefox   = "Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0"
)

type signIn struct {
	token string
	sid   string
}

// signInAs signs u in with a password from a browser at ip.
func signInAs(t *testing.T, w *authWorld, u *store.User, ua, ip string) signIn {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"username": u.Username, "password": pw})
	r := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("User-Agent", ua)
	r.RemoteAddr = ip + ":50000"
	rec := httptest.NewRecorder()
	w.s.authLogin(rec, r)
	var sess sessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)
	if rec.Code != http.StatusOK || sess.AccessToken == "" {
		t.Fatalf("login %s: %d %s", u.Username, rec.Code, rec.Body)
	}
	_, sid, err := w.s.Auth.AuthenticateSession(context.Background(), sess.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	return signIn{token: sess.AccessToken, sid: sid}
}

// asSession calls h as u, signed in with session sid.
func asSession(w *authWorld, h http.HandlerFunc, u *store.User, sid, method, target string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	ctx := context.WithValue(context.WithValue(r.Context(), userKey, u), sessionKey, sid)
	r = r.WithContext(ctx)
	if strings.Contains(target, "/sessions/") {
		parts := strings.Split(strings.TrimSuffix(target, "/end"), "/")
		r.SetPathValue("id", parts[len(parts)-1])
		r.SetPathValue("name", parts[len(parts)-2])
	}
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

func sessionsOf(t *testing.T, rec *httptest.ResponseRecorder) sessionsResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("sessions: %d %s", rec.Code, rec.Body)
	}
	var out sessionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func withNotify(w *authWorld) {
	w.s.notify = notify.New(w.st, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestSignInAlerts(t *testing.T) {
	w := newAuthWorld(t)
	withNotify(w)
	ctx := context.Background()
	alex := w.users["alex"]

	feed, stop := w.s.sessions.subscribe(alex.ID)
	defer stop()
	nextSignin := func() *signinAlert {
		for {
			select {
			case e := <-feed:
				if e.Type == "signin" {
					return e.Alert
				}
			case <-time.After(200 * time.Millisecond):
				return nil
			}
		}
	}

	// First sign-in: nothing else is signed in, so no one to tell, and
	// nothing on record to call it a new device against.
	first := signInAs(t, w, alex, uaChromeWin, "192.168.1.10")
	if a := nextSignin(); a != nil {
		t.Fatalf("alert with no other sessions: %+v", a)
	}
	if n, _, _ := w.s.notify.List(ctx, 0); len(n) != 0 {
		t.Fatalf("new-device notice on the first sign-in: %+v", n)
	}

	// Second sign-in, another browser: the first session hears about it.
	second := signInAs(t, w, alex, uaFirefox, "203.0.113.7")
	a := nextSignin()
	if a == nil {
		t.Fatal("no sign-in alert")
	}
	if a.SessionID != second.sid || a.Source != sourceSession || a.Kind != store.SessionWeb ||
		a.Browser != "Firefox 133" || a.OS != "Linux" || a.IP != "203.0.113.7" || a.Network != "internet" ||
		!strings.Contains(a.Body, "Not you?") || a.GenZ == nil || a.ID == 0 {
		t.Fatalf("alert: %+v", a)
	}
	if got := w.s.link().alerts(alex.ID, ""); len(got) != 1 || got[0].SessionID != second.sid {
		t.Fatalf("remembered alerts: %+v", got)
	}
	// ...and administrators hear about the new device.
	items, _, _ := w.s.notify.List(ctx, 0)
	if len(items) != 1 || items[0].Kind != notify.KindSecurity || !strings.Contains(items[0].Title, "alex") {
		t.Fatalf("new-device notice: %+v", items)
	}

	// Same browser again: still an alert, but not a new device.
	signInAs(t, w, alex, uaFirefox, "203.0.113.8")
	if nextSignin() == nil {
		t.Fatal("no alert for the third sign-in")
	}
	if items, _, _ := w.s.notify.List(ctx, 0); len(items) != 1 {
		t.Fatalf("known device reported as new: %d notices", len(items))
	}

	// The list: three sessions, the caller's marked, with what they are.
	list := sessionsOf(t, asSession(w, w.s.authSessions, alex, first.sid, "GET", "/api/v1/auth/sessions"))
	if len(list.Sessions) != 3 || !list.Sessions[0].Current || list.Sessions[0].ID != first.sid {
		t.Fatalf("sessions: %+v", list.Sessions)
	}
	if s0 := list.Sessions[0]; s0.Browser != "Chrome 131" || s0.OS != "Windows" || s0.IP != "192.168.1.10" || s0.Network != "lan" || s0.Kind != store.SessionWeb {
		t.Fatalf("first session: %+v", s0)
	}
	if len(list.Alerts) != 2 {
		t.Fatalf("alerts in the list: %d", len(list.Alerts))
	}

	// "Not you? Sign it out": the second session ends at once.
	if rec := asSession(w, w.s.authSessionEnd, alex, first.sid, "DELETE", "/api/v1/auth/sessions/"+second.sid); rec.Code != http.StatusNoContent {
		t.Fatalf("end: %d %s", rec.Code, rec.Body)
	}
	if _, err := w.s.Auth.Authenticate(ctx, second.token); err == nil {
		t.Fatal("ended session still works")
	}
	if rec := asSession(w, w.s.authSessionEnd, alex, first.sid, "DELETE", "/api/v1/auth/sessions/"+second.sid); rec.Code != http.StatusNotFound {
		t.Fatalf("ending it twice: %d", rec.Code)
	}
}

func TestSessionsOwnOnly(t *testing.T) {
	w := newAuthWorld(t)
	ctx := context.Background()
	alex, boss := w.users["alex"], w.users["boss"]
	a1 := signInAs(t, w, alex, uaChromeWin, "192.168.1.10")
	b1 := signInAs(t, w, boss, uaFirefox, "192.168.1.20")

	// Alex sees only alex's sessions, and can't end boss's.
	list := sessionsOf(t, asSession(w, w.s.authSessions, alex, a1.sid, "GET", "/api/v1/auth/sessions"))
	if len(list.Sessions) != 1 || list.Sessions[0].ID != a1.sid || list.Sessions[0].Username != "" {
		t.Fatalf("alex's list: %+v", list.Sessions)
	}
	if rec := asSession(w, w.s.authSessionEnd, alex, a1.sid, "DELETE", "/api/v1/auth/sessions/"+b1.sid); rec.Code != http.StatusNotFound {
		t.Fatalf("alex ended boss's session: %d", rec.Code)
	}
	if _, err := w.s.Auth.Authenticate(ctx, b1.token); err != nil {
		t.Fatal("boss's session was ended")
	}

	// Administrators see everyone's (with whose it is) and can end any.
	all := sessionsOf(t, asSession(w, w.s.adminSessions, boss, b1.sid, "GET", "/api/v1/sessions"))
	if len(all.Sessions) != 2 {
		t.Fatalf("admin list: %+v", all.Sessions)
	}
	for _, s := range all.Sessions {
		if s.Username == "" {
			t.Fatalf("admin list without user names: %+v", s)
		}
	}
	one := sessionsOf(t, asSession(w, w.s.adminSessions, boss, b1.sid, "GET", "/api/v1/sessions?user=alex"))
	if len(one.Sessions) != 1 || one.Sessions[0].Username != "alex" {
		t.Fatalf("admin list for alex: %+v", one.Sessions)
	}
	if rec := asSession(w, w.s.authSessionEnd, boss, b1.sid, "DELETE", "/api/v1/auth/sessions/"+a1.sid); rec.Code != http.StatusNoContent {
		t.Fatalf("admin end: %d", rec.Code)
	}
	if _, err := w.s.Auth.Authenticate(ctx, a1.token); err == nil {
		t.Fatal("alex's session survived")
	}
}

func TestEndOtherSessions(t *testing.T) {
	w := newAuthWorld(t)
	w.s.Config.DataDir = t.TempDir()
	ctx := context.Background()
	alex, boss := w.users["alex"], w.users["boss"]
	keep := signInAs(t, w, alex, uaChromeWin, "192.168.1.10")
	other := signInAs(t, w, alex, uaFirefox, "192.168.1.11")
	cr := linkedDevice(t, w, alex)
	bossSess := signInAs(t, w, boss, uaFirefox, "192.168.1.20")

	rec := asSession(w, w.s.authSessionsEndOthers, alex, keep.sid, "POST", "/api/v1/auth/sessions/end-others")
	var out map[string]int
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusOK || out["ended"] != 2 {
		t.Fatalf("end others: %d %s", rec.Code, rec.Body)
	}
	if _, err := w.s.Auth.Authenticate(ctx, keep.token); err != nil {
		t.Fatal("the current session was ended")
	}
	if _, err := w.s.Auth.Authenticate(ctx, other.token); err == nil {
		t.Fatal("the other session survived")
	}
	if rec := asDevice(w.s, w.s.deviceMe, "GET", "/api/v1/device/me", cr.Token, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the linked phone survived: %d", rec.Code)
	}
	if _, err := w.s.Auth.Authenticate(ctx, bossSess.token); err != nil {
		t.Fatal("someone else's session was ended")
	}

	// An administrator signs alex out everywhere.
	again := signInAs(t, w, alex, uaFirefox, "192.168.1.12")
	rec = asSession(w, w.s.adminUserSessionsEnd, boss, bossSess.sid, "POST", "/api/v1/users/alex/sessions/end")
	if rec.Code != http.StatusOK {
		t.Fatalf("admin end all: %d %s", rec.Code, rec.Body)
	}
	for _, tok := range []string{keep.token, again.token} {
		if _, err := w.s.Auth.Authenticate(ctx, tok); err == nil {
			t.Fatal("alex is still signed in")
		}
	}
	// Doing it to yourself keeps the session you're using.
	rec = asSession(w, w.s.adminUserSessionsEnd, boss, bossSess.sid, "POST", "/api/v1/users/boss/sessions/end")
	if rec.Code != http.StatusOK {
		t.Fatalf("admin end own: %d", rec.Code)
	}
	if _, err := w.s.Auth.Authenticate(ctx, bossSess.token); err != nil {
		t.Fatal("the admin's own session was ended")
	}
}

func TestDeviceSessionsAndAlerts(t *testing.T) {
	w := newAuthWorld(t)
	w.s.Config.DataDir = t.TempDir()
	alex, boss := w.users["alex"], w.users["boss"]
	web := signInAs(t, w, alex, uaChromeWin, "192.168.1.10")

	// Linking a phone is a sign-in too: the web session hears about it.
	feed, stop := w.s.sessions.subscribe(alex.ID)
	cr := linkedDevice(t, w, alex)
	var got *signinAlert
	for got == nil {
		select {
		case e := <-feed:
			if e.Type == "signin" {
				got = e.Alert
			}
		case <-time.After(time.Second):
			t.Fatal("no alert for the linked phone")
		}
	}
	stop()
	if got.Source != sourceDevice || got.SessionID != cr.DeviceID || got.Kind != store.SessionPhone || got.DeviceName != "Pixel" {
		t.Fatalf("pairing alert: %+v", got)
	}

	// The phone follows the long-poll: a new web sign-in reaches it, and
	// its own pairing doesn't.
	h := w.s.link()
	h.mu.Lock()
	cursor := h.seq - 1
	h.mu.Unlock()
	second := signInAs(t, w, alex, uaFirefox, "198.51.100.4")
	h.mu.Lock()
	ev := h.events(alex.ID, cr.DeviceID, false, false, cursor)
	h.mu.Unlock()
	if len(ev.Signins) != 1 || ev.Signins[0].SessionID != second.sid {
		t.Fatalf("phone events: %+v", ev.Signins)
	}

	// The phone's list: both web sessions and itself (marked current, with
	// the address it last called from).
	rec := asDevice(w.s, w.s.deviceSessions, "GET", "/api/v1/device/sessions", cr.Token, "")
	list := sessionsOf(t, rec)
	if len(list.Sessions) != 3 || !list.Sessions[0].Current || list.Sessions[0].Source != sourceDevice || list.Sessions[0].IP != "192.0.2.1" {
		t.Fatalf("phone list: %+v", list.Sessions)
	}
	for _, a := range list.Alerts {
		if a.Source == sourceDevice {
			t.Fatalf("the phone is told about itself: %+v", a)
		}
	}

	// "Not you? Sign it out" from the phone: its owner's sessions only.
	bossSess := signInAs(t, w, boss, uaFirefox, "192.168.1.20")
	r := httptest.NewRequest("DELETE", "/api/v1/device/sessions/"+bossSess.sid, nil)
	r.Header.Set("Authorization", "Device "+cr.Token)
	r.SetPathValue("id", bossSess.sid)
	out := httptest.NewRecorder()
	w.s.device(w.s.deviceSessionEnd).ServeHTTP(out, r)
	if out.Code != http.StatusNotFound {
		t.Fatalf("phone ended someone else's session: %d", out.Code)
	}
	r = httptest.NewRequest("DELETE", "/api/v1/device/sessions/"+second.sid, nil)
	r.Header.Set("Authorization", "Device "+cr.Token)
	r.SetPathValue("id", second.sid)
	out = httptest.NewRecorder()
	w.s.device(w.s.deviceSessionEnd).ServeHTTP(out, r)
	if out.Code != http.StatusNoContent {
		t.Fatalf("phone end: %d %s", out.Code, out.Body)
	}
	if _, err := w.s.Auth.Authenticate(context.Background(), second.token); err == nil {
		t.Fatal("the session survived")
	}
	if _, err := w.s.Auth.Authenticate(context.Background(), web.token); err != nil {
		t.Fatal("the wrong session was ended")
	}
}

func TestSessionDetailsAndAudit(t *testing.T) {
	w := newAuthWorld(t)
	ctx := context.Background()
	alex := w.users["alex"]
	a := signInAs(t, w, alex, "curl/8.5.0", "127.0.0.1")
	b := signInAs(t, w, alex, uaChromeWin, "127.0.0.1")
	list, err := w.s.listSessions(ctx, alex.ID, a.sid, "")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, s := range list {
		kinds[s.ID] = s.Kind
	}
	if kinds[a.sid] != store.SessionCLI || kinds[b.sid] != store.SessionDesktop {
		t.Fatalf("kinds: %v", kinds)
	}
	if rec := asSession(w, w.s.authSessionEnd, alex, a.sid, "DELETE", "/api/v1/auth/sessions/"+b.sid); rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code)
	}
	ended, err := w.st.CountAudit(ctx, alex.ID, "sessions.end")
	if err != nil {
		t.Fatal(err)
	}
	alerted, _ := w.st.CountAudit(ctx, alex.ID, "sessions.signin_alert")
	if ended != 1 || alerted != 1 {
		t.Fatalf("audit entries: ended %d, alerted %d", ended, alerted)
	}
}

func TestNetOfAndKinds(t *testing.T) {
	for ip, want := range map[string]string{
		"127.0.0.1": "local", "::1": "local", "192.168.1.5": "lan", "10.0.0.1": "lan", "172.16.4.4": "lan",
		"fd00::1": "lan", "fe80::1": "lan", "100.64.1.1": "lan", "8.8.8.8": "internet", "2001:db8::1": "internet",
		"::ffff:192.168.1.5": "lan", "": "", "junk": "",
	} {
		if got := netOf(ip); got != want {
			t.Errorf("netOf(%q) = %q, want %q", ip, got, want)
		}
	}
	chrome := useragent.Parse(uaChromeWin)
	if k := sessionKind(chrome, "192.168.1.5", ""); k != store.SessionWeb {
		t.Errorf("browser: %s", k)
	}
	if k := sessionKind(chrome, "127.0.0.1", ""); k != store.SessionDesktop {
		t.Errorf("this screen: %s", k)
	}
	if k := sessionKind(chrome, "192.168.1.5", "dev1"); k != store.SessionPhone {
		t.Errorf("linked phone: %s", k)
	}
	if k := sessionKind(useragent.Parse("curl/8"), "192.168.1.5", ""); k != store.SessionCLI {
		t.Errorf("curl: %s", k)
	}
	if sameDeviceKey(store.SessionWeb, useragent.Parse(uaChromeWin), "") != sameDeviceKey(store.SessionWeb, useragent.Parse(strings.Replace(uaChromeWin, "131", "132", 1)), "") {
		t.Error("a browser update made a new device")
	}
}
