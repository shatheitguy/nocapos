package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// NoCap Shell's socket: only NoCapOS's own nocap-shell, as a known account.
func TestShellSocketNeedsTheShellProgram(t *testing.T) {
	s := &Server{}
	h := s.ShellHandler()
	for name, peer := range map[string]*shellPeer{
		"no peer":       nil,
		"other program": {Account: "sam", Exe: "/usr/bin/python3"},
		"copied shell":  {Account: "sam", Exe: "/home/sam/nocap-shell"},
	} {
		r := httptest.NewRequest(http.MethodPost, "/v1/shell/session", nil)
		if peer != nil {
			r = r.WithContext(WithShellPeer(r.Context(), *peer))
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: %d, want 403", name, w.Code)
		}
	}
	for _, p := range ShellPrograms {
		if !shellProgramAllowed(p) {
			t.Errorf("%s isn't allowed", p)
		}
	}
}

func TestShellOriginOnLoopback(t *testing.T) {
	var seenOrigin string
	h := ShellCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenOrigin = r.Header.Get("Origin")
		w.WriteHeader(http.StatusOK)
	}))

	// Live connections from the shell's pages: accepted (the ticket decides).
	r := httptest.NewRequest(http.MethodGet, "/ws?ticket=x", nil)
	r.Header.Set("Origin", ShellOrigin)
	r.Header.Set("Upgrade", "websocket")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seenOrigin != "" {
		t.Errorf("websocket from the shell kept its origin %q", seenOrigin)
	}

	// Another site's page: nothing changes, no CORS headers.
	r = httptest.NewRequest(http.MethodGet, "/ws?ticket=x", nil)
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if seenOrigin != "https://evil.example" || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("other origin: %q, CORS %q", seenOrigin, w.Header().Get("Access-Control-Allow-Origin"))
	}

	// A preflight from the shell: answered, without credentials.
	r = httptest.NewRequest(http.MethodOptions, "/api/v1/system/info", nil)
	r.Header.Set("Origin", ShellOrigin)
	r.Header.Set("Access-Control-Request-Method", "GET")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != ShellOrigin || w.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("preflight: %d %v", w.Code, w.Header())
	}
}
