package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"alfaos/alfad/internal/nativeapps"
	"alfaos/alfad/internal/store"
	"alfaos/alfad/internal/webapps"
)

// Linux apps: desktop apps from Flathub, installed system-wide (admin,
// audited) and opened by anyone signed in, either on the server's own
// screen or streamed into a window through the same-origin proxy below.

const (
	nativeCookie = "nocap_native"
	nativePrefix = "/apps/native/"
	nativeTTL    = time.Hour
)

func (s *Server) nativeRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/native-apps/status", s.authed(s.nativeStatus))
	mux.Handle("GET /api/v1/native-apps/installed", s.authed(s.nativeInstalled))
	mux.Handle("GET /api/v1/native-apps/icons/{id}", s.authed(s.nativeIcon))
	mux.Handle("GET /api/v1/native-apps/popular", s.admin(s.nativePopular))
	mux.Handle("GET /api/v1/native-apps/search", s.admin(s.nativeSearch))
	mux.Handle("GET /api/v1/native-apps/details/{id}", s.admin(s.nativeDetails))
	mux.Handle("GET /api/v1/native-apps/jobs", s.admin(s.nativeJobs))
	mux.Handle("GET /api/v1/native-apps/jobs/{job}", s.admin(s.nativeJob))
	mux.Handle("POST /api/v1/native-apps/{id}/install", s.admin(s.nativeInstall))
	mux.Handle("POST /api/v1/native-apps/{id}/uninstall", s.admin(s.nativeUninstall))
	mux.Handle("POST /api/v1/native-apps/{id}/launch", s.authed(s.nativeLaunch))
	mux.Handle("GET /api/v1/native-apps/streams/{sid}", s.authed(s.nativeStreamStatus))
	mux.Handle("DELETE /api/v1/native-apps/streams/{sid}", s.authed(s.nativeStreamStop))
	// The stream itself (noVNC page, assets, VNC WebSocket): gated by a
	// signed, path-scoped cookie and the session's owner.
	mux.HandleFunc(nativePrefix, s.nativeStreamProxy)
}

// nativeErr answers a nativeapps error with the right status.
func nativeErr(w http.ResponseWriter, err error) {
	var unsup nativeapps.ErrUnsupported
	switch {
	case errors.As(err, &unsup):
		writeError(w, http.StatusNotImplemented, "unsupported", unsup.Reason)
	case errors.Is(err, nativeapps.ErrBadID):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, nativeapps.ErrNotInstalled):
		writeError(w, http.StatusNotFound, "not_installed", err.Error())
	case errors.Is(err, nativeapps.ErrBusy):
		writeError(w, http.StatusConflict, "busy", err.Error())
	case errors.Is(err, webapps.ErrNoSlots):
		writeError(w, http.StatusServiceUnavailable, "busy", err.Error())
	default:
		writeError(w, http.StatusBadGateway, "native_error", err.Error())
	}
}

func (s *Server) nativeStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Native.Status())
}

// nativeInstalled is what Launchpad shows; everyone signed in may open them.
func (s *Server) nativeInstalled(w http.ResponseWriter, r *http.Request) {
	st := s.Native.Status()
	if !st.Supported {
		writeJSON(w, http.StatusOK, map[string]any{"status": st, "apps": []nativeapps.App{}})
		return
	}
	apps, err := s.Native.Installed(r.Context())
	if err != nil {
		nativeErr(w, err)
		return
	}
	out := map[string]any{"status": st, "apps": apps}
	if userFrom(r.Context()).Role == store.RoleAdmin {
		out["jobs"] = s.Native.Jobs.Recent()
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) nativePopular(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	apps, err := s.Native.Popular(ctx)
	if err != nil {
		nativeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"apps": apps, "jobs": s.Native.Jobs.Recent()})
}

func (s *Server) nativeSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{"apps": []nativeapps.App{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	apps, err := s.Native.Search(ctx, q)
	if err != nil {
		nativeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"apps": apps})
}

func (s *Server) nativeDetails(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	d, err := s.Native.Details(ctx, r.PathValue("id"))
	if err != nil {
		nativeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) nativeIcon(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	ic, err := s.Native.Icon(ctx, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "no_icon", "no icon for that app")
		return
	}
	h := w.Header()
	h.Set("Content-Type", ic.Type)
	h.Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(ic.Data)
}

func (s *Server) nativeJobs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"jobs": s.Native.Jobs.Recent()})
}

func (s *Server) nativeJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.Native.Jobs.Get(r.PathValue("job"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such job")
		return
	}
	writeJSON(w, http.StatusOK, j)
}

type nativeActReq struct {
	Name string `json:"name,omitempty"` // shown in progress and toasts
}

func (s *Server) nativeChange(w http.ResponseWriter, r *http.Request, action string) {
	id := r.PathValue("id")
	var req nativeActReq
	if r.ContentLength > 0 && !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Name) > 120 {
		req.Name = req.Name[:120]
	}
	u := userFrom(r.Context())
	ip := clientIP(r, s.Config.TrustedProxies)
	onDone := func(j nativeapps.Job) {
		// The request is long gone; audit with a fresh context.
		detail := j.Error
		if j.OK {
			detail = "done"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.auditCtx(ctx, u.ID, "nativeapp."+action+".done", id, ip, j.OK, detail)
	}
	start := s.Native.Install
	if action == "uninstall" {
		start = s.Native.Uninstall
	}
	j, err := start(id, req.Name, onDone)
	if err != nil {
		if nativeapps.ValidID(id) {
			s.audit(r, u.ID, "nativeapp."+action, id, false, err.Error())
		}
		nativeErr(w, err)
		return
	}
	s.audit(r, u.ID, "nativeapp."+action, id, true, "")
	writeJSON(w, http.StatusAccepted, j)
}

func (s *Server) nativeInstall(w http.ResponseWriter, r *http.Request) {
	s.nativeChange(w, r, "install")
}

func (s *Server) nativeUninstall(w http.ResponseWriter, r *http.Request) {
	s.nativeChange(w, r, "uninstall")
}

func (s *Server) nativeLaunch(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	who := nativeapps.Person{ID: u.ID}
	// Streamed apps run as the person's own Linux account when sign-in uses
	// this machine's accounts; never as another account of the same name.
	if s.Auth != nil && s.Auth.SystemAccountsEnabled() {
		who.Username = u.Username
	}
	got, err := s.Native.Launch(r.Context(), who, r.PathValue("id"))
	if err != nil {
		nativeErr(w, err)
		return
	}
	if got.Where == "stream" {
		s.grantNativeCookie(w, r)
	}
	writeJSON(w, http.StatusOK, got)
}

// grantNativeCookie lets this browser's iframe reach its streams (and only
// its own: the proxy checks the session's owner too).
func (s *Server) grantNativeCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     nativeCookie,
		Value:    s.signBrave(userFrom(r.Context()).ID, sessionFrom(r.Context()), time.Now().Add(nativeTTL)),
		Path:     nativePrefix,
		MaxAge:   int(nativeTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.Config.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) nativeStreamStatus(w http.ResponseWriter, r *http.Request) {
	if s.Streams == nil {
		writeError(w, http.StatusNotFound, "not_found", "no such stream")
		return
	}
	st, ok := s.Streams.Status(userFrom(r.Context()).ID, r.PathValue("sid"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such stream")
		return
	}
	if st.Ready {
		s.grantNativeCookie(w, r) // renewed while the window is open
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) nativeStreamStop(w http.ResponseWriter, r *http.Request) {
	if s.Streams == nil || !s.Streams.Stop(userFrom(r.Context()).ID, r.PathValue("sid")) {
		writeError(w, http.StatusNotFound, "not_found", "no such stream")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// nativeStreamProxy serves /apps/native/<stream id>/… from that stream's
// loopback noVNC bridge, to the stream's owner only.
func (s *Server) nativeStreamProxy(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, nativePrefix)
	sid, _, _ := strings.Cut(rest, "/")
	if !webapps.ValidStreamID(sid) || s.Streams == nil {
		writeError(w, http.StatusNotFound, "not_found", "no such stream")
		return
	}
	if rest == sid { // /apps/native/<id> → /apps/native/<id>/
		http.Redirect(w, r, nativePrefix+sid+"/", http.StatusFound)
		return
	}
	userID, session, ok := s.streamCookie(r, nativeCookie)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "open the app from NoCapOS first")
		return
	}
	if _, err := s.currentUser(r.Context(), userID, session, false); err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "open the app from NoCapOS first")
		return
	}
	port, ok := s.Streams.Target(userID, sid)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "that app isn't streaming (yet)")
		return
	}
	// Signing out or losing access ends the VNC WebSocket; while it's open
	// the stream counts as in use.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer s.live.add(userID, session, cancel)()
	defer s.Streams.Hold(sid)()
	s.proxyStream(w, r.WithContext(ctx), nativePrefix+sid, port, "http", "App")
}
