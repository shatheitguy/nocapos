package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"alfaos/alfad/internal/config"
	"alfaos/alfad/internal/hostaccess"
)

func localOnlyServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	return &Server{Deps: Deps{Config: &config.Config{DataDir: dir, CookieSecure: true}}, localGate: hostaccess.NewGate(dir)}, dir
}

func TestLocalOnlyBlocksOtherDevicesWhenOff(t *testing.T) {
	s, dir := localOnlyServer(t)
	h := s.localOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	do := func(remote, path string, hdr map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.RemoteAddr = remote
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// On (the default): everyone gets through.
	if w := do("192.168.1.50:5000", "/", nil); w.Code != http.StatusTeapot {
		t.Fatalf("remote allowed by default: got %d", w.Code)
	}

	if err := hostaccess.SetRemote(dir, false); err != nil {
		t.Fatal(err)
	}
	s.localGate.Refresh()

	for _, remote := range []string{"127.0.0.1:40000", "[::1]:40000", "[::ffff:127.0.0.1]:1"} {
		if w := do(remote, "/api/v1/auth/status", nil); w.Code != http.StatusTeapot {
			t.Errorf("loopback %s blocked: %d", remote, w.Code)
		}
	}
	// Headers never count: a LAN device claiming to be loopback stays out.
	spoof := map[string]string{"X-Forwarded-For": "127.0.0.1", "X-Real-IP": "127.0.0.1", "Host": "localhost"}
	for _, remote := range []string{"192.168.1.50:5000", "10.0.0.7:1", "[fe80::1]:1", "[2001:db8::2]:443"} {
		w := do(remote, "/api/v1/auth/status", spoof)
		if w.Code != http.StatusForbidden {
			t.Errorf("LAN %s allowed: %d", remote, w.Code)
			continue
		}
		var body map[string]errorBody
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["error"].Code != "local_only" ||
			!strings.Contains(body["error"].Message, "sudo cap web on") {
			t.Errorf("API answer = %s", w.Body.String())
		}
	}
	// WebSockets, app proxies and the UI are covered too.
	for _, p := range []string{"/ws", "/ws/terminal", "/apps/brave/", "/dav/x", "/"} {
		if w := do("192.168.1.50:5000", p, nil); w.Code != http.StatusForbidden {
			t.Errorf("%s allowed: %d", p, w.Code)
		}
	}
	if w := do("192.168.1.50:5000", "/", nil); !strings.Contains(w.Header().Get("Content-Type"), "text/html") ||
		!strings.Contains(w.Body.String(), "sudo cap web on") {
		t.Errorf("UI answer should be a friendly page: %q %s", w.Header().Get("Content-Type"), w.Body.String())
	}

	// Back on.
	_ = hostaccess.SetRemote(dir, true)
	s.localGate.Refresh()
	if w := do("192.168.1.50:5000", "/", nil); w.Code != http.StatusTeapot {
		t.Fatalf("remote should be allowed again: %d", w.Code)
	}
}

func TestSecureCookieOffOnlyOnLocalDesktopListener(t *testing.T) {
	s, _ := localOnlyServer(t)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if !s.secureCookie(r) {
		t.Fatal("cookies must stay Secure on the main listener")
	}
	var got bool
	MarkLocalDesktop(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = s.secureCookie(r) })).
		ServeHTTP(httptest.NewRecorder(), r)
	if got {
		t.Fatal("the loopback desktop listener has no TLS, so no Secure flag")
	}
}
