package api

import (
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

func randomKey(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

// streamTransport talks to the loopback noVNC bridges.
var streamTransport = &http.Transport{}

// proxyStream reverse-proxies a loopback noVNC bridge under prefix (streamed
// Linux apps). Callers have checked who may reach it.
func (s *Server) proxyStream(w http.ResponseWriter, r *http.Request, prefix string, port int, scheme, label string) {
	target := &url.URL{Scheme: scheme, Host: "127.0.0.1:" + strconv.Itoa(port)}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = streamTransport
	proxy.FlushInterval = -1 // stream immediately (VNC framebuffer)
	base := proxy.Director
	proxy.Director = func(req *http.Request) {
		base(req)
		req.URL.Path = strings.TrimPrefix(req.URL.Path, prefix)
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
		req.Host = target.Host
		// The bridge is loopback-only; never forward NoCapOS credentials to it.
		req.Header.Del("Authorization")
		req.Header.Del("Cookie")
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		// Our content, our frame: drop the bridge's anti-embedding headers.
		resp.Header.Del("X-Frame-Options")
		resp.Header.Del("Content-Security-Policy")
		resp.Header.Del("Content-Security-Policy-Report-Only")
		// Keep redirects inside the proxied path.
		if loc := resp.Header.Get("Location"); strings.HasPrefix(loc, "/") && !strings.HasPrefix(loc, prefix) {
			resp.Header.Set("Location", prefix+loc)
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		writeError(w, http.StatusBadGateway, "proxy_error", label+" stream error: "+err.Error())
	}
	proxy.ServeHTTP(w, r)
}

// --- cookie signing (in-memory key, valid for this process' lifetime) ---

func (s *Server) signStream(userID, session string, exp time.Time) string {
	payload := userID + "~" + session + "~" + strconv.FormatInt(exp.Unix(), 10)
	mac := hmac.New(sha256.New, s.streamKey)
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig
}

// streamCookie checks a signed, path-scoped stream cookie: who, from which
// login session, not expired.
func (s *Server) streamCookie(r *http.Request, name string) (userID, session string, ok bool) {
	c, err := r.Cookie(name)
	if err != nil {
		return "", "", false
	}
	payload, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return "", "", false
	}
	mac := hmac.New(sha256.New, s.streamKey)
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
