package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/config"
)

// NoCap Desktop on the machine's own screen is NoCap OS itself, running
// natively: the NoCap Shell host (nocap-shell, shell/) shows the same web OS
// build from local files in its own windows and surfaces. It gets its web
// session for the person signed in on the screen over a local socket that
// the kernel identifies (SO_PEERCRED: the Linux account, and the program,
// which must be NoCapOS's own nocap-shell; see shell_socket_linux.go). The
// pages then talk to alfad on the loopback-only listener (DesktopAddr) with
// that session's bearer token, like a browser tab would.

// ShellSocketPath is where alfad listens for NoCap Shell.
const ShellSocketPath = "/run/nocapos/shell.sock"

// ShellOrigin is the origin of NoCap Shell's pages (the host's own URI
// scheme). Only the loopback listener answers it (shellCORS).
const ShellOrigin = "nocap-shell://shell"

// ShellPrograms are the programs allowed to ask for a session.
var ShellPrograms = []string{"/usr/libexec/nocapos/nocap-shell"}

type shellPeer struct {
	Account string
	Exe     string
}

type shellPeerKey struct{}

// WithShellPeer marks a connection as coming from a Linux account and program.
func WithShellPeer(ctx context.Context, p shellPeer) context.Context {
	return context.WithValue(ctx, shellPeerKey{}, p)
}

func shellPeerFrom(ctx context.Context) (shellPeer, bool) {
	p, ok := ctx.Value(shellPeerKey{}).(shellPeer)
	return p, ok && p.Account != ""
}

func shellProgramAllowed(exe string) bool {
	for _, p := range ShellPrograms {
		if exe == p {
			return true
		}
	}
	return false
}

type shellSessionRequest struct {
	Refresh string `json:"refresh"`
}

type shellSessionResponse struct {
	User     string `json:"user"`
	RealName string `json:"realName"`
	Admin    bool   `json:"admin"`
	// APIOrigin is where the pages reach alfad (loopback only).
	APIOrigin string `json:"apiOrigin"`
	// The web session: the pages use AccessToken; the host keeps
	// RefreshToken and trades it for a new one before it expires.
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresAt    string `json:"expires_at"`
	RefreshToken string `json:"refresh_token"`
	Account      any    `json:"account"`
}

// ShellHandler serves NoCap Shell's local socket.
func (s *Server) ShellHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/shell/session", s.shellSession)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := shellPeerFrom(r.Context())
		if !ok {
			writeError(w, http.StatusForbidden, "forbidden", "unknown account")
			return
		}
		if !shellProgramAllowed(p.Exe) {
			writeError(w, http.StatusForbidden, "forbidden", "only NoCap Shell may ask for a desktop session")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// shellAPIOrigin is the loopback listener's address as an origin.
func (s *Server) shellAPIOrigin() string {
	addr := strings.TrimSpace(s.Config.DesktopAddr)
	if addr == "" || strings.EqualFold(addr, "off") {
		addr = config.DefaultDesktopAddr
	}
	return "http://" + addr
}

func (s *Server) shellSession(w http.ResponseWriter, r *http.Request) {
	p, _ := shellPeerFrom(r.Context())
	var req shellSessionRequest
	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}
	meta := auth.ClientMeta{IP: "127.0.0.1", UserAgent: "NoCap Desktop"}
	var sess *auth.Session
	var err error
	if req.Refresh != "" {
		sess, err = s.Auth.Refresh(r.Context(), req.Refresh, meta)
		if err == nil && (sess.User == nil || sess.User.Username != p.Account) {
			sess, err = nil, auth.ErrInvalidToken
		}
	}
	if sess == nil {
		sess, err = s.Auth.ScreenSession(r.Context(), p.Account, meta)
	}
	if err != nil {
		if errors.Is(err, auth.ErrInvalidToken) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "the desktop session ended")
			return
		}
		writeError(w, http.StatusForbidden, "forbidden", "no desktop session for this account: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, shellSessionResponse{
		User: sess.User.Username, RealName: sess.User.Username, Admin: sess.User.Role == "admin",
		APIOrigin:   s.shellAPIOrigin(),
		AccessToken: sess.AccessToken, TokenType: "Bearer",
		ExpiresAt:    sess.AccessExpires.UTC().Format("2006-01-02T15:04:05Z07:00"),
		RefreshToken: sess.RefreshToken,
		Account:      sess.User,
	})
}

// ShellCORS serves NoCap Shell's pages (origin nocap-shell://shell) on the
// loopback listener. Their HTTP requests come through the shell host, which
// forwards them without an origin; their live connections (WebSockets) come
// straight from the page, and need a ticket from an authenticated request
// like any other, so the shell's origin is accepted for them here. No
// cookies are involved, and no other origin gets anything new.
func ShellCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != ShellOrigin {
			next.ServeHTTP(w, r)
			return
		}
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			r.Header.Del("Origin")
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", ShellOrigin)
		h.Add("Vary", "Origin")
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Range, X-Alfa-Request")
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
