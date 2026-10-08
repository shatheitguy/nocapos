package api

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strings"
	"time"

	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/store"
)

type ctxKey int

const userKey ctxKey = 0

func userFrom(ctx context.Context) *store.User {
	u, _ := ctx.Value(userKey).(*store.User)
	return u
}

// authed requires a valid Bearer access token.
func (s *Server) authed(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || tok == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="alfa"`)
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}
		u, err := s.Auth.Authenticate(r.Context(), tok)
		if errors.Is(err, auth.ErrInvalidToken) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="alfa", error="invalid_token"`)
			writeError(w, http.StatusUnauthorized, "invalid_token", "invalid or expired token")
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	})
}

func (s *Server) admin(h http.HandlerFunc) http.Handler {
	return s.authed(func(w http.ResponseWriter, r *http.Request) {
		if userFrom(r.Context()).Role != store.RoleAdmin {
			writeError(w, http.StatusForbidden, "forbidden", "administrator role required")
			return
		}
		h(w, r)
	})
}

// requireCSRFHeader protects cookie-authenticated endpoints. Browsers cannot
// attach a custom header cross-origin without a CORS preflight, which alfad
// never approves. SameSite=Strict on the cookie is the first line of defense.
func requireCSRFHeader(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-Alfa-Request") != "1" {
		writeError(w, http.StatusForbidden, "csrf", "missing X-Alfa-Request header")
		return false
	}
	return true
}

const (
	apiCSP = "default-src 'none'; frame-ancestors 'none'"
	// The UI loads only its own scripts/styles; no inline code, no third parties.
	// frame-src allows the in-OS Browser and Remote Desktop apps to embed external
	// pages; frame-ancestors still forbids NoCapOS itself from being embedded.
	// blob: images are how the Remote Desktop client paints decoded frames.
	uiCSP = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; " +
		"connect-src 'self'; frame-src https: http:; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
)

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		switch {
		case strings.HasPrefix(r.URL.Path, "/apps/"):
			// Streamed app content (e.g. Brave). It is framed same-origin by the
			// desktop, so no X-Frame-Options DENY here; the proxy manages the
			// upstream's own security headers.
		case isAPIPath(r.URL.Path):
			h.Set("X-Frame-Options", "DENY")
			h.Set("Content-Security-Policy", apiCSP)
			h.Set("Cache-Control", "no-store")
		default:
			h.Set("X-Frame-Options", "DENY")
			h.Set("Content-Security-Policy", uiCSP)
			h.Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

func isAPIPath(p string) bool {
	return strings.HasPrefix(p, "/api/") || p == "/ws" || p == "/healthz"
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.Log.Error("panic", "path", r.URL.Path, "panic", v, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "internal", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// requestLogger logs the path only: the query string may carry a WebSocket ticket.
func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/healthz" {
			return
		}
		s.Log.Info("http",
			"method", r.Method, "path", r.URL.Path, "status", rec.status,
			"dur_ms", time.Since(start).Milliseconds(), "ip", clientIP(r, s.Config.TrustedProxies))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack is required for the WebSocket upgrade to pass through this wrapper.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijack not supported")
	}
	r.status = http.StatusSwitchingProtocols
	return hj.Hijack()
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// clientIP returns the peer address, or — when the peer is a trusted proxy —
// the right-most X-Forwarded-For entry, which is the address Traefik itself
// observed. Left-most entries are client-controlled and never trusted.
func clientIP(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	peer = peer.Unmap()
	if !inPrefixes(peer, trusted) {
		return peer.String()
	}
	xff := r.Header.Values("X-Forwarded-For")
	if len(xff) == 0 {
		return peer.String()
	}
	parts := strings.Split(xff[len(xff)-1], ",")
	if a, err := netip.ParseAddr(strings.TrimSpace(parts[len(parts)-1])); err == nil {
		return a.Unmap().String()
	}
	return peer.String()
}

func inPrefixes(a netip.Addr, ps []netip.Prefix) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
