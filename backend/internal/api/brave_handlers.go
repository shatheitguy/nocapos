package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	braveCookie = "nocap_brave"
	bravePrefix = "/apps/brave"
	// The cookie is renewed each time the window opens Brave; it names the
	// person and login session, which are re-checked on every request.
	braveTTL = time.Hour
)

func randomKey(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

// braveTransport talks to the loopback noVNC bridge.
var braveTransport = &http.Transport{}

// --- lifecycle endpoints (admin, JWT) ---

func (s *Server) braveStart(w http.ResponseWriter, r *http.Request) {
	s.brave.Ensure()
	// Grant this browser a short-lived, path-scoped cookie so the iframe, its
	// assets and the VNC WebSocket can reach the proxy without a bearer header.
	http.SetCookie(w, &http.Cookie{
		Name:     braveCookie,
		Value:    s.signBrave(userFrom(r.Context()).ID, sessionFrom(r.Context()), time.Now().Add(braveTTL)),
		Path:     bravePrefix,
		MaxAge:   int(braveTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.Config.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, s.brave.Status())
}

func (s *Server) braveStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.brave.Status())
}

// --- the reverse proxy (cookie-gated) ---

func (s *Server) braveProxy(w http.ResponseWriter, r *http.Request) {
	userID, sid, ok := s.braveCookie(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "open Brave from NoCapOS first")
		return
	}
	// Still an administrator, still signed in from that session?
	if _, err := s.currentUser(r.Context(), userID, sid, true); err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "open Brave from NoCapOS first")
		return
	}
	// The VNC WebSocket lives on; signing out or losing admin ends it (the
	// proxy closes an upgraded connection when its request context ends).
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer s.live.add(userID, sid, cancel)()
	r = r.WithContext(ctx)
	port, scheme := s.brave.Target()
	if port == 0 {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "Brave is still starting")
		return
	}
	target := &url.URL{Scheme: scheme, Host: "127.0.0.1:" + strconv.Itoa(port)}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = braveTransport
	proxy.FlushInterval = -1 // stream immediately (VNC framebuffer)
	base := proxy.Director
	proxy.Director = func(req *http.Request) {
		base(req)
		req.URL.Path = strings.TrimPrefix(req.URL.Path, bravePrefix)
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
		req.Host = target.Host
		// The bridge is loopback-only; never forward NoCapOS credentials to it.
		req.Header.Del("Authorization")
		req.Header.Del("Cookie")
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		// Our content, our frame: drop the container's anti-embedding headers.
		resp.Header.Del("X-Frame-Options")
		resp.Header.Del("Content-Security-Policy")
		resp.Header.Del("Content-Security-Policy-Report-Only")
		// Keep redirects inside the proxied path.
		if loc := resp.Header.Get("Location"); strings.HasPrefix(loc, "/") && !strings.HasPrefix(loc, bravePrefix) {
			resp.Header.Set("Location", bravePrefix+loc)
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		writeError(w, http.StatusBadGateway, "proxy_error", "Brave stream error: "+err.Error())
	}
	proxy.ServeHTTP(w, r)
}

// braveRedirect sends /apps/brave → /apps/brave/ so relative assets resolve.
func (s *Server) braveRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, bravePrefix+"/", http.StatusFound)
}

// --- cookie signing (in-memory key, valid for this process' lifetime) ---

func (s *Server) signBrave(userID, session string, exp time.Time) string {
	payload := userID + "~" + session + "~" + strconv.FormatInt(exp.Unix(), 10)
	mac := hmac.New(sha256.New, s.braveKey)
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig
}

func (s *Server) braveCookie(r *http.Request) (userID, session string, ok bool) {
	c, err := r.Cookie(braveCookie)
	if err != nil {
		return "", "", false
	}
	payload, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return "", "", false
	}
	mac := hmac.New(sha256.New, s.braveKey)
	mac.Write([]byte(payload))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(want)) {
		return "", "", false
	}
	parts := strings.Split(payload, "~")
	if len(parts) != 3 || parts[0] == "" {
		return "", "", false
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || time.Now().Unix() >= exp {
		return "", "", false
	}
	return parts[0], parts[1], true
}
