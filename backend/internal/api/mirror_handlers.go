package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"alfaos/alfad/internal/mirror"
	"alfaos/alfad/internal/store"
)

// Phone mirroring: a linked phone shows its live screen in the Phone app of
// NoCap OS, and the window's taps and keys go back to it. Only the phone's
// owner may see or control it: administrators can't open other people's
// phones. The phone streams over its own device token; windows open a
// one-time ticket that is bound to the person, their sign-in and the phone.

const (
	mirrorTicketTTL = 30 * time.Second
	// mirrorRecheck: how often an open stream checks that the phone is
	// still linked and its owner still allowed in.
	mirrorRecheck = 20 * time.Second
)

type mirrorState struct {
	once    sync.Once
	hub     *mirror.Hub
	tickets *mirror.Tickets
}

func (s *Server) mirrorHub() *mirror.Hub {
	s.mirrorInit()
	return s.mirror.hub
}

func (s *Server) mirrorTickets() *mirror.Tickets {
	s.mirrorInit()
	return s.mirror.tickets
}

func (s *Server) mirrorInit() {
	s.mirror.once.Do(func() {
		s.mirror.hub = mirror.NewHub()
		s.mirror.tickets = mirror.NewTickets(mirrorTicketTTL)
	})
}

func (s *Server) mirrorRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/mirror/phones", s.authed(s.mirrorDevices))
	mux.Handle("POST /api/v1/mirror/phones/{id}/ask", s.authed(s.mirrorAsk))
	mux.Handle("POST /api/v1/mirror/phones/{id}/ticket", s.authed(s.mirrorTicket))
	mux.HandleFunc("GET /ws/mirror", s.mirrorView)
	mux.Handle("GET /api/v1/device/mirror", s.device(s.mirrorStream))
	mux.Handle("POST /api/v1/device/mirror/state", s.device(s.mirrorPhoneState))
}

type mirrorDeviceJSON struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	Platform   string        `json:"platform"`
	LastSeenAt *time.Time    `json:"last_seen_at"`
	Battery    *int          `json:"battery"`
	AppVersion string        `json:"app_version"`
	Mirror     mirror.Status `json:"mirror"`
}

// ownPhone is the caller's own linked device id, or nil after answering 404.
// Administrators get no exception: someone else's phone is "not found".
func (s *Server) ownPhone(w http.ResponseWriter, r *http.Request, u *store.User, id string) *store.Device {
	d, err := s.Store.DeviceByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !mirrorOwns(u, d)) {
		writeError(w, http.StatusNotFound, "not_found", "That phone isn't linked to your account.")
		return nil
	}
	if err != nil {
		s.internalError(w, r, err)
		return nil
	}
	return d
}

// mirrorOwns: only a phone's owner may mirror it, whatever their role.
func mirrorOwns(u *store.User, d *store.Device) bool {
	return u != nil && d != nil && d.UserID != "" && d.UserID == u.ID && d.RevokedAt.IsZero()
}

// mirrorDevices lists the caller's own phones (never anyone else's, even
// for administrators) and whether each is streaming.
func (s *Server) mirrorDevices(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	list, err := s.Store.ListDevices(r.Context(), u.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]mirrorDeviceJSON, 0, len(list))
	for _, d := range list {
		if !mirrorOwns(u, &d) {
			continue
		}
		j := mirrorDeviceJSON{ID: d.ID, Name: d.Name, Platform: d.Platform, AppVersion: d.AppVersion, Mirror: s.mirrorHub().Status(d.ID)}
		if !d.LastSeenAt.IsZero() {
			t := d.LastSeenAt.UTC()
			j.LastSeenAt = &t
		}
		if d.Battery >= 0 {
			b := d.Battery
			j.Battery = &b
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, out)
}

// mirrorAsk asks the phone (through its events long-poll) to start
// mirroring; it shows "Allow on your phone" there.
func (s *Server) mirrorAsk(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	d := s.ownPhone(w, r, u, r.PathValue("id"))
	if d == nil {
		return
	}
	a, streaming := s.mirrorRequest(r, u, d)
	if streaming {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "streaming": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": a.ID})
}

// mirrorRequest asks d to start streaming unless it already is; the ask
// wakes the phone's events long-poll.
func (s *Server) mirrorRequest(r *http.Request, u *store.User, d *store.Device) (mirror.Ask, bool) {
	if st := s.mirrorHub().Status(d.ID); st.Online {
		return mirror.Ask{}, true
	}
	if a := s.mirrorHub().AskSince(d.ID, 0); a != nil {
		return *a, false // still waiting on the phone: don't ask twice
	}
	h := s.link()
	h.mu.Lock()
	a := s.mirrorHub().SetAsk(d.ID, h.bump()) // bump wakes the phone's long-poll
	h.mu.Unlock()
	if room, err := s.mirrorHub().Room(d.ID, u.ID); err == nil {
		room.SetState("asking")
		s.mirrorHub().Release(room)
	}
	s.audit(r, u.ID, "mirror.request", d.Name, true, "")
	return a, false
}

// mirrorForget ends any mirroring of an unlinked phone.
func (s *Server) mirrorForget(deviceID string) { s.mirrorHub().Forget(deviceID) }

// mirrorLink is "nearby" when the phone reaches the server from a private
// or local address (the same network), else "remote".
func mirrorLink(ip string) string {
	a := net.ParseIP(ip)
	if a != nil && (a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast()) {
		return "nearby"
	}
	return "remote"
}

// mirrorTicket is a one-time ticket for the viewer WebSocket.
func (s *Server) mirrorTicket(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var req struct {
		JPEG bool `json:"jpeg"`
	}
	if r.ContentLength > 0 && !decodeJSON(w, r, &req) {
		return
	}
	d := s.ownPhone(w, r, u, r.PathValue("id"))
	if d == nil {
		return
	}
	s.mirrorRequest(r, u, d) // opening a window asks the phone to start
	tk := s.mirrorTickets().Issue(mirror.Ticket{UserID: u.ID, Session: sessionFrom(r.Context()), DeviceID: d.ID, JPEG: req.JPEG})
	writeJSON(w, http.StatusOK, map[string]any{"ticket": tk, "expires_in": int(mirrorTicketTTL.Seconds())})
}

// mirrorStillOwned re-checks a stream: the phone is still linked to userID
// and that person may still sign in.
func (s *Server) mirrorStillOwned(ctx context.Context, userID, deviceID string) bool {
	d, err := s.Store.DeviceByID(ctx, deviceID)
	if err != nil || d.UserID != userID {
		return false
	}
	u, err := s.Store.UserByID(ctx, userID)
	if err != nil {
		return false
	}
	return s.Auth.DesktopAllowed(ctx, u) == nil
}

// mirrorView is a window's WebSocket: frames and phone messages out, input in.
func (s *Server) mirrorView(w http.ResponseWriter, r *http.Request) {
	tk, ok := s.mirrorTickets().Redeem(r.URL.Query().Get("ticket"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid_ticket", "invalid or expired ticket")
		return
	}
	u, err := s.currentUser(r.Context(), tk.UserID, tk.Session, false)
	if err != nil || !s.mirrorStillOwned(r.Context(), u.ID, tk.DeviceID) {
		writeError(w, http.StatusUnauthorized, "invalid_ticket", "invalid or expired ticket")
		return
	}
	room, err := s.mirrorHub().Room(tk.DeviceID, u.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "That phone isn't linked to your account.")
		return
	}
	v, err := room.AddViewer(tk.JPEG)
	if err != nil {
		s.mirrorHub().Release(room)
		writeError(w, http.StatusConflict, "too_many", err.Error())
		return
	}
	defer room.RemoveViewer(v)
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.Config.AllowedOrigins})
	if err != nil {
		return
	}
	conn.SetReadLimit(mirror.MaxControl)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer s.live.add(u.ID, tk.Session, func() {
		cancel()
		conn.Close(websocket.StatusPolicyViolation, "session changed")
	})()
	name := tk.DeviceID
	if d, err := s.Store.DeviceByID(ctx, tk.DeviceID); err == nil {
		name = d.Name
	}
	start := time.Now()
	s.audit(r, u.ID, "mirror.view", name, true, "")
	defer func() {
		s.auditCtx(context.WithoutCancel(ctx), u.ID, "mirror.view_end", name, clientIP(r, s.Config.TrustedProxies), true,
			"watched "+time.Since(start).Round(time.Second).String())
	}()

	go s.mirrorWatch(ctx, cancel, u.ID, tk.DeviceID)
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case <-v.Done:
				conn.Close(websocket.StatusGoingAway, "phone unlinked")
				return
			case o := <-v.Out:
				typ := websocket.MessageBinary
				if o.Text {
					typ = websocket.MessageText
				}
				wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Write(wctx, typ, o.Data)
				wcancel()
				if err != nil {
					return
				}
			}
		}
	}()
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			conn.Close(websocket.StatusNormalClosure, "")
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		_ = room.FromViewer(v, data) // unknown input is ignored
	}
}

// mirrorWatch ends a stream when the phone is unlinked or its owner loses access.
func (s *Server) mirrorWatch(ctx context.Context, cancel context.CancelFunc, userID, deviceID string) {
	t := time.NewTicker(mirrorRecheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !s.mirrorStillOwned(ctx, userID, deviceID) {
				if ctx.Err() == nil {
					s.mirrorHub().Forget(deviceID)
					cancel()
				}
				return
			}
		}
	}
}

// mirrorStream is the phone's WebSocket: encoded frames in, input out.
func (s *Server) mirrorStream(w http.ResponseWriter, r *http.Request) {
	d, u := deviceFrom(r.Context()), userFrom(r.Context())
	room, err := s.mirrorHub().Room(d.ID, d.UserID)
	if err == nil && !mirrorOwns(u, d) {
		s.mirrorHub().Release(room)
		err = mirror.ErrNotYours
	}
	if err != nil {
		writeError(w, http.StatusForbidden, "forbidden", "This phone can't mirror here.")
		return
	}
	// The phone's app sends no Origin; a browser can't send a device token
	// header on a WebSocket, so this can't be opened cross-site.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.Config.AllowedOrigins})
	if err != nil {
		s.mirrorHub().Release(room)
		return
	}
	conn.SetReadLimit(mirror.MaxFrame)
	p := room.AttachPhoneVia(mirrorLink(clientIP(r, s.Config.TrustedProxies)))
	defer room.DetachPhone(p)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	start := time.Now()
	s.audit(r, u.ID, "mirror.start", d.Name, true, "phone screen shared")
	defer func() {
		s.auditCtx(context.WithoutCancel(ctx), u.ID, "mirror.stop", d.Name, clientIP(r, s.Config.TrustedProxies), true,
			"shared "+time.Since(start).Round(time.Second).String())
	}()

	go s.mirrorWatch(ctx, cancel, u.ID, d.ID)
	go func() {
		defer cancel()
		ping := time.NewTicker(15 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-p.Done:
				conn.Close(websocket.StatusGoingAway, "replaced")
				return
			case <-ping.C:
				pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Ping(pctx)
				pcancel()
				if err != nil {
					return
				}
			case b := <-p.Control:
				wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Write(wctx, websocket.MessageText, b)
				wcancel()
				if err != nil {
					return
				}
			}
		}
	}()
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			conn.Close(websocket.StatusNormalClosure, "")
			return
		}
		if err := room.FromPhone(p, typ == websocket.MessageText, data); err != nil && !errors.Is(err, mirror.ErrBadPacket) {
			return
		}
	}
}

// mirrorPhoneState: before streaming, the phone says how the ask went:
// "asking" (its prompt is up), "denied", "unsupported" or "stopped".
func (s *Server) mirrorPhoneState(w http.ResponseWriter, r *http.Request) {
	d, u := deviceFrom(r.Context()), userFrom(r.Context())
	var req struct {
		State string `json:"state"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	st := strings.TrimSpace(req.State)
	switch st {
	case "asking", "denied", "unsupported", "stopped":
	default:
		writeError(w, http.StatusBadRequest, "validation", "unknown state")
		return
	}
	if st != "asking" {
		s.mirrorHub().ClearAsk(d.ID)
	}
	if st == "denied" {
		s.audit(r, u.ID, "mirror.denied", d.Name, false, "declined on the phone")
	}
	if room, err := s.mirrorHub().Room(d.ID, d.UserID); err == nil {
		room.SetState(st)
		s.mirrorHub().Release(room)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
