package api

import (
	"context"
	"errors"
	"html"
	"net/http"
	"strings"

	"alfaos/alfad/internal/config"
	"alfaos/alfad/internal/hostaccess"
	"alfaos/alfad/internal/rdp"
	"alfaos/alfad/internal/syspkg"
)

// ---- "Let other devices open NoCapOS" ----

// localOnly keeps every device but this machine out while the switch is off.
// It sits in front of everything (API, UI, WebSockets, app proxies, WebDAV)
// and looks only at the real TCP peer: headers can't talk their way in.
func (s *Server) localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.localGate.LocalOnly() && !hostaccess.LoopbackPeer(r.RemoteAddr) {
			writeLocalOnly(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// LocalOnly wraps another listener's handler (the http→https redirect) with
// the same switch.
func (s *Server) LocalOnly(next http.Handler) http.Handler { return s.localOnly(next) }

const localOnlyPage = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>NoCapOS is local-only</title><style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#0b0b10;color:#f2f2f7;font:16px/1.5 system-ui,sans-serif}
main{max-width:30rem;padding:2rem;text-align:center}h1{font-size:1.4rem;margin:0 0 .5rem}code{background:#ffffff1a;padding:.1rem .35rem;border-radius:.3rem}p{color:#c7c7cc}</style></head>
<body><main><h1>NoCapOS is local-only rn 🔒</h1><p>%s</p></main></body></html>`

func writeLocalOnly(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	if isAPIPath(r.URL.Path) || strings.HasPrefix(r.URL.Path, "/ws") || isUserContentPath(r.URL.Path) ||
		strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeError(w, http.StatusForbidden, "local_only", hostaccess.LocalOnlyMessage)
		return
	}
	msg := html.EscapeString("Turn it back on from the server's own screen or run ")
	page := strings.Replace(localOnlyPage, "%s", "NoCapOS only opens on the server itself right now. "+msg+"<code>sudo cap web on</code>.", 1)
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(page))
}

// ---- the local desktop listener ----

type localDesktopKey struct{}

// MarkLocalDesktop wraps the handler served on the loopback-only plain-HTTP
// listener NoCap Desktop's browser opens. Cookies set there can't carry the
// Secure flag (there is no TLS), which is fine: that listener only accepts
// connections from this machine, so nothing travels over a network.
func MarkLocalDesktop(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), localDesktopKey{}, true)))
	})
}

func onLocalDesktop(r *http.Request) bool {
	v, _ := r.Context().Value(localDesktopKey{}).(bool)
	return v
}

// secureCookie: cookies are Secure except on the loopback desktop listener.
func (s *Server) secureCookie(r *http.Request) bool {
	return s.Config.CookieSecure && !onLocalDesktop(r)
}

// ---- Settings → General → This Screen & Access ----

// hostSwitchesNative: both switches only make sense when NoCapOS runs on
// the server itself as root (in Docker the peer is always the proxy).
func hostSwitchesNative() bool { return syspkg.Native() && !config.InContainer() }

type hostAccessState struct {
	Native      bool                    `json:"native"`
	ClientLocal bool                    `json:"client_local"` // this browser runs on the server itself
	Remote      remoteAccessState       `json:"remote"`
	Desktop     hostaccess.DesktopState `json:"desktop"`
}

type remoteAccessState struct {
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
	Reason    string `json:"reason,omitempty"`
}

func (s *Server) hostAccessState(r *http.Request) hostAccessState {
	native := hostSwitchesNative()
	st := hostAccessState{
		Native:      native,
		ClientLocal: hostaccess.LoopbackPeer(r.RemoteAddr),
		Remote:      remoteAccessState{Supported: native, Enabled: hostaccess.RemoteEnabled(s.Config.DataDir)},
		Desktop:     s.desktop.State(r.Context()),
	}
	if !native {
		st.Remote.Reason = "This switch needs NoCapOS installed straight on Linux (not in Docker)."
	}
	return st
}

func (s *Server) hostAccessGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.hostAccessState(r))
}

func (s *Server) remoteAccessSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool `json:"enabled"`
		// Confirm: turning it off from another device locks that browser out.
		Confirm bool `json:"confirm"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "say enabled: true or false")
		return
	}
	if !hostSwitchesNative() {
		writeError(w, http.StatusNotImplemented, "unsupported", "This switch needs NoCapOS installed straight on Linux (not in Docker).")
		return
	}
	if !*req.Enabled && !hostaccess.LoopbackPeer(r.RemoteAddr) && !req.Confirm {
		writeError(w, http.StatusConflict, "confirm", "This browser isn't on the server, so turning this off locks it out. Confirm to go ahead.")
		return
	}
	err := hostaccess.SetRemote(s.Config.DataDir, *req.Enabled)
	s.localGate.Refresh()
	what := "on"
	if !*req.Enabled {
		what = "off"
	}
	s.audit(r, userFrom(r.Context()).ID, "system.remote_access", what, err == nil, errText(err))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "remote_access_failed", "Couldn't change it: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.hostAccessState(r))
}

// desktopSet switches NoCap Desktop: {"mode": "full"|"kiosk"|"off"}, or the
// older {"enabled": true|false}.
func (s *Server) desktopSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode    string `json:"mode"`
		Enabled *bool  `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	var err error
	what := req.Mode
	switch {
	case req.Mode == hostaccess.ModeFull || req.Mode == hostaccess.ModeKiosk || req.Mode == hostaccess.ModeOff:
		err = s.desktop.SetMode(r.Context(), req.Mode)
	case req.Mode == "" && req.Enabled != nil:
		what = "on"
		if !*req.Enabled {
			what = "off"
		}
		err = s.desktop.Set(r.Context(), *req.Enabled)
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "say mode: full, kiosk or off")
		return
	}
	s.audit(r, userFrom(r.Context()).ID, "system.desktop", what, err == nil, errText(err))
	if err != nil {
		switch {
		case errors.Is(err, hostaccess.ErrDesktopUnavailable):
			writeError(w, http.StatusNotImplemented, "unsupported", s.desktop.State(r.Context()).Reason)
		case errors.Is(err, hostaccess.ErrBadMode):
			writeError(w, http.StatusBadRequest, "bad_mode", err.Error())
		default:
			writeError(w, http.StatusBadRequest, "desktop_failed", "NoCap Desktop didn't switch: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, s.hostAccessState(r))
}

// desktopAutologinSet turns the sign-in screen's automatic sign-in on (for
// the admin's own Linux account) or off.
func (s *Server) desktopAutologinSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "say enabled: true or false")
		return
	}
	u := userFrom(r.Context())
	err := s.desktop.SetAutologin(u.Username, *req.Enabled)
	what := "off"
	if *req.Enabled {
		what = "on for " + u.Username
	}
	s.audit(r, u.ID, "system.desktop_autologin", what, err == nil, errText(err))
	if err != nil {
		switch {
		case errors.Is(err, hostaccess.ErrDesktopUnavailable):
			writeError(w, http.StatusNotImplemented, "unsupported", "Automatic sign-in needs NoCap Desktop's full desktop.")
		case errors.Is(err, hostaccess.ErrNotLinuxUser):
			writeError(w, http.StatusBadRequest, "not_linux_user", err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "autologin_failed", "Couldn't change automatic sign-in: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, s.hostAccessState(r))
}

// desktopView hands out a single-use ticket for the server's own screen:
// whatever it shows right now, the sign-in screen included. The browser
// opens the Remote Desktop WebSocket with it; guacd connects to the screen
// server on 127.0.0.1. The credentials stay on the server, inside the
// ticket. When the screen's session changes the connection ends and the
// browser asks again (sign_in says whether it's the sign-in screen).
func (s *Server) desktopView(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Width  int `json:"width"`
		Height int `json:"height"`
		DPI    int `json:"dpi"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	t, err := s.desktop.RemoteView(r.Context())
	uid := userFrom(r.Context()).ID
	s.audit(r, uid, "system.desktop_view", t.Protocol, err == nil, errText(err))
	if err != nil {
		if errors.Is(err, hostaccess.ErrDesktopUnavailable) {
			writeError(w, http.StatusNotImplemented, "unsupported", s.desktop.State(r.Context()).Reason)
			return
		}
		if errors.Is(err, hostaccess.ErrScreenStarting) {
			writeError(w, http.StatusServiceUnavailable, "screen_switching", err.Error())
			return
		}
		writeError(w, http.StatusConflict, "desktop_view_failed", err.Error())
		return
	}
	p := rdp.Params{
		Protocol: t.Protocol, Hostname: "127.0.0.1", Port: t.Port,
		Username: t.Username, Password: t.Password,
		Width: clampRange(req.Width, 640, 7680, 1280), Height: clampRange(req.Height, 480, 4320, 800),
		DPI: clampRange(req.DPI, 48, 384, 96),
	}
	if t.Protocol == "rdp" {
		p.Security, p.IgnoreCert = "tls", true // NoCapOS's own certificate, on loopback
	}
	id := s.rdpTickets.issue(rdpTicket{userID: uid, session: sessionFrom(r.Context()), params: p})
	writeJSON(w, http.StatusOK, map[string]any{"ticket": id, "expires_in": int(rdpTicketTTL.Seconds()), "protocol": t.Protocol, "sign_in": t.SignIn})
}
