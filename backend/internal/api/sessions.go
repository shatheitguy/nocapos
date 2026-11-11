package api

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/notify"
	"alfaos/alfad/internal/store"
	"alfaos/alfad/internal/useragent"
)

// Sign-in awareness: every account can see where it's signed in (browsers,
// NoCap Desktop on this screen, linked phones, scripts), end any of those
// sessions, and hears about each new sign-in on its other sessions: a
// banner on the web desktop, a notification on its phones. Administrators
// also get a notice when anyone signs in from a device not seen before.

// Session sources: a login session (refresh token family) or a linked device.
const (
	sourceSession = "session"
	sourceDevice  = "device"
)

// touchEvery: how often a session's "last active" is written at most.
const touchEvery = time.Minute

// sessionJSON is one place an account is signed in.
type sessionJSON struct {
	ID         string    `json:"id"`
	Source     string    `json:"source"` // session | device
	Kind       string    `json:"kind"`   // web | phone | desktop | cli
	DeviceName string    `json:"device_name,omitempty"`
	Browser    string    `json:"browser,omitempty"`
	OS         string    `json:"os,omitempty"`
	IP         string    `json:"ip,omitempty"`
	Network    string    `json:"network,omitempty"` // local | lan | internet
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	Current    bool      `json:"current"`
	Username   string    `json:"username,omitempty"`
}

// signinAlert tells an account about a new sign-in.
type signinAlert struct {
	ID         int64     `json:"id"`
	SessionID  string    `json:"session_id"` // what "Not you? Sign it out" ends
	Source     string    `json:"source"`
	Kind       string    `json:"kind"`
	DeviceName string    `json:"device_name,omitempty"`
	Browser    string    `json:"browser,omitempty"`
	OS         string    `json:"os,omitempty"`
	IP         string    `json:"ip,omitempty"`
	Network    string    `json:"network,omitempty"`
	At         time.Time `json:"at"`
	// The phone shows these as they are (the web desktop words its own).
	Title string          `json:"title"`
	Body  string          `json:"body"`
	GenZ  *notify.Variant `json:"genz,omitempty"`

	deviceID string // the linked device involved: it doesn't hear about itself
}

// ---------- live feed ----------

// sessionEvent goes out on the account.sessions topic.
type sessionEvent struct {
	Type  string       `json:"type"` // signin | changed
	Alert *signinAlert `json:"alert,omitempty"`
}

// sessionFeed fans session events out to each person's open desktops.
type sessionFeed struct {
	mu   sync.Mutex
	subs map[string]map[chan sessionEvent]struct{}
	seen sync.Map // session ID → unix time its "last active" was written
}

func (f *sessionFeed) subscribe(uid string) (<-chan sessionEvent, func()) {
	ch := make(chan sessionEvent, 8)
	f.mu.Lock()
	if f.subs == nil {
		f.subs = map[string]map[chan sessionEvent]struct{}{}
	}
	if f.subs[uid] == nil {
		f.subs[uid] = map[chan sessionEvent]struct{}{}
	}
	f.subs[uid][ch] = struct{}{}
	f.mu.Unlock()
	return ch, func() {
		f.mu.Lock()
		delete(f.subs[uid], ch)
		if len(f.subs[uid]) == 0 {
			delete(f.subs, uid)
		}
		f.mu.Unlock()
	}
}

func (f *sessionFeed) publish(uid string, e sessionEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for ch := range f.subs[uid] {
		select {
		case ch <- e:
		default: // a stuck reader reloads the list anyway
		}
	}
}

// accountSessionsSource feeds the account.sessions topic for one person.
func (s *Server) accountSessionsSource(uid string) func(ctx context.Context, emit func(any)) error {
	return func(ctx context.Context, emit func(any)) error {
		ch, cancel := s.sessions.subscribe(uid)
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return nil
			case e := <-ch:
				emit(e)
			}
		}
	}
}

// sessionsChanged tells uid's desktops to reload their sessions list.
func (s *Server) sessionsChanged(uid string) {
	s.sessions.publish(uid, sessionEvent{Type: "changed"})
}

// ---------- describing sessions ----------

// netOf says roughly where an address is, without asking anyone: this
// machine, the local network, or the internet.
func netOf(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	a = a.Unmap()
	switch {
	case a.IsLoopback():
		return "local"
	case a.IsPrivate(), a.IsLinkLocalUnicast(), cgnat.Contains(a):
		return "lan"
	}
	return "internet"
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// sessionKind decides how a web session signed in.
func sessionKind(ua useragent.Info, ip, deviceID string) string {
	switch {
	case deviceID != "":
		return store.SessionPhone
	case ua.CLI:
		return store.SessionCLI
	case netOf(ip) == "local":
		return store.SessionDesktop // NoCap Desktop on this machine's own screen
	}
	return store.SessionWeb
}

// sameDeviceKey groups sign-ins from "the same device" for the new-device check.
func sameDeviceKey(kind string, ua useragent.Info, deviceID string) string {
	if deviceID != "" {
		return "device:" + deviceID
	}
	browser, _, _ := strings.Cut(ua.Browser, " ") // not the version: browsers update
	return strings.ToLower(kind + "|" + browser + "|" + ua.OS)
}

func webSessionJSON(a store.ActiveSession, current string, devNames map[string]string, withUser bool) sessionJSON {
	ua := useragent.Parse(a.UserAgent)
	kind := a.Kind
	if kind == "" {
		kind = sessionKind(ua, a.IP, a.DeviceID)
	}
	out := sessionJSON{
		ID: a.ID, Source: sourceSession, Kind: kind, Browser: ua.Browser, OS: ua.OS,
		IP: a.IP, Network: netOf(a.IP), CreatedAt: a.CreatedAt, LastSeenAt: a.LastSeenAt,
		Current: a.ID == current, DeviceName: devNames[a.DeviceID],
	}
	if withUser {
		out.Username = a.Username
	}
	return out
}

func deviceSessionJSON(d store.Device, currentDevice string, withUser bool) sessionJSON {
	os := "Android"
	switch d.Platform {
	case "ios":
		os = "iOS"
	case "other":
		os = ""
	}
	out := sessionJSON{
		ID: d.ID, Source: sourceDevice, Kind: store.SessionPhone, DeviceName: d.Name,
		Browser: "NoCap OS app", OS: os, IP: d.LastIP, Network: netOf(d.LastIP),
		CreatedAt: d.CreatedAt, LastSeenAt: d.LastSeenAt, Current: d.ID == currentDevice,
	}
	if d.AppVersion != "" {
		out.Browser += " " + d.AppVersion
	}
	if out.LastSeenAt.IsZero() {
		out.LastSeenAt = d.CreatedAt
	}
	if withUser {
		out.Username = d.Username
	}
	return out
}

// listSessions is where userID is signed in ("" = everyone), most recently
// active first. current marks the caller's own session or device.
func (s *Server) listSessions(ctx context.Context, userID, currentSession, currentDevice string) ([]sessionJSON, error) {
	all := userID == ""
	web, err := s.Store.ActiveSessions(ctx, userID, time.Now())
	if err != nil {
		return nil, err
	}
	devs, err := s.Store.ListDevices(ctx, userID)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(devs))
	for _, d := range devs {
		names[d.ID] = d.Name
	}
	out := make([]sessionJSON, 0, len(web)+len(devs))
	for _, a := range web {
		out = append(out, webSessionJSON(a, currentSession, names, all))
	}
	for _, d := range devs {
		out = append(out, deviceSessionJSON(d, currentDevice, all))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Current != out[j].Current {
			return out[i].Current
		}
		return out[i].LastSeenAt.After(out[j].LastSeenAt)
	})
	return out, nil
}

// touchSession keeps a session's "last active" and address current, at most
// once a minute per session.
func (s *Server) touchSession(r *http.Request, sid string) {
	if sid == "" {
		return
	}
	now := time.Now()
	if v, ok := s.sessions.seen.Load(sid); ok && now.Unix()-v.(int64) < int64(touchEvery/time.Second) {
		return
	}
	s.sessions.seen.Store(sid, now.Unix())
	if err := s.Store.TouchSession(r.Context(), sid, clientIP(r, s.Config.TrustedProxies), truncate(r.UserAgent(), 256), now); err != nil {
		s.Log.Warn("session last active not saved", "err", err)
	}
}

// ---------- signing in ----------

// alertText is the alert's wording for the phone, in both voices.
func alertText(label, ip, network string, at time.Time) (title, body string, genz *notify.Variant) {
	where := ip
	switch network {
	case "local":
		where += " (this machine)"
	case "lan":
		where += " (local network)"
	}
	if where == "" {
		where = "an unknown address"
	}
	when := at.Local().Format("Mon 2 Jan 15:04")
	title = "New sign-in to your account"
	body = label + " signed in from " + where + " at " + when + ". Not you? Sign it out."
	genz = &notify.Variant{
		Title: "New sign-in just dropped",
		Body:  label + " pulled up from " + where + " at " + when + ". Not you? Sign it out, no cap.",
	}
	return
}

// labelFor names what signed in: "Chrome 131 on Windows", "Pixel".
func labelFor(kind string, ua useragent.Info, deviceName string) string {
	switch {
	case deviceName != "":
		return deviceName
	case ua.Label() != "":
		return ua.Label()
	case kind == store.SessionDesktop:
		return "NoCap Desktop on this machine"
	}
	return "An unknown device"
}

// signedIn records a new web session's details and tells the account (and,
// for a device never seen before, the administrators).
func (s *Server) signedIn(r *http.Request, sess *auth.Session, device *store.Device) {
	ctx := r.Context()
	ip := clientIP(r, s.Config.TrustedProxies)
	ua := useragent.Parse(r.UserAgent())
	deviceID, deviceName := "", ""
	if device != nil {
		deviceID, deviceName = device.ID, device.Name
	}
	kind := sessionKind(ua, ip, deviceID)
	key := sameDeviceKey(kind, ua, deviceID)
	now := time.Now()
	known, hasHistory, err := s.Store.KnownDevice(ctx, sess.User.ID, key, sess.ID)
	if err != nil {
		s.Log.Warn("new-device check", "err", err)
		known = true
	}
	if err := s.Store.SaveSessionInfo(ctx, &store.SessionInfo{
		ID: sess.ID, UserID: sess.User.ID, Kind: kind, DeviceID: deviceID, DeviceKey: key,
		UserAgent: truncate(r.UserAgent(), 256), IP: ip, CreatedAt: now, LastSeenAt: now,
	}); err != nil {
		s.Log.Warn("session details not saved", "err", err)
	}
	s.sessions.seen.Store(sess.ID, now.Unix())
	a := signinAlert{
		SessionID: sess.ID, Source: sourceSession, Kind: kind, DeviceName: deviceName,
		Browser: ua.Browser, OS: ua.OS, IP: ip, Network: netOf(ip), At: now.UTC().Truncate(time.Second),
		deviceID: deviceID,
	}
	a.Title, a.Body, a.GenZ = alertText(labelFor(kind, ua, deviceName), ip, a.Network, now)
	// A linked phone was announced when it was linked; the very first
	// sign-in on record (a new account, or right after an update) has
	// nothing to compare with.
	s.announceSignin(ctx, r, sess.User, a, sess.ID, deviceID, !known && hasHistory && deviceID == "")
}

// devicePaired tells the account a phone was just linked to it.
func (s *Server) devicePaired(r *http.Request, u *store.User, d *store.Device) {
	ip := clientIP(r, s.Config.TrustedProxies)
	if err := s.Store.SetDeviceIP(r.Context(), d.ID, ip); err != nil {
		s.Log.Warn("device address not saved", "err", err)
	}
	now := time.Now()
	a := signinAlert{
		SessionID: d.ID, Source: sourceDevice, Kind: store.SessionPhone, DeviceName: d.Name,
		Browser: "NoCap OS app", OS: deviceSessionJSON(*d, "", false).OS, IP: ip, Network: netOf(ip),
		At: now.UTC().Truncate(time.Second), deviceID: d.ID,
	}
	a.Title, a.Body, a.GenZ = alertText(d.Name+" (NoCap OS app)", ip, a.Network, now)
	s.announceSignin(r.Context(), r, u, a, "", d.ID, true)
}

// announceSignin sends the alert to the account's other sessions, if it
// has any, and the new-device notice to administrators.
func (s *Server) announceSignin(ctx context.Context, r *http.Request, u *store.User, a signinAlert, exceptSession, exceptDevice string, newDevice bool) {
	others, err := s.otherSessions(ctx, u.ID, exceptSession, exceptDevice)
	if err != nil {
		s.Log.Warn("sign-in alert: count sessions", "err", err)
	}
	if others > 0 {
		a.ID = s.link().addAlert(u.ID, a) // phones pick it up from their long-poll
		s.sessions.publish(u.ID, sessionEvent{Type: "signin", Alert: &a})
		s.audit(r, u.ID, "sessions.signin_alert", u.Username, true, labelOf(a)+" from "+a.IP)
	}
	s.sessionsChanged(u.ID)
	if newDevice && s.notify != nil {
		s.add(ctx, newDeviceNotice(u.Username, a))
	}
}

func labelOf(a signinAlert) string {
	return labelFor(a.Kind, useragent.Info{Browser: a.Browser, OS: a.OS}, a.DeviceName)
}

func newDeviceNotice(username string, a signinAlert) notify.Notification {
	where := a.IP
	if where == "" {
		where = "an unknown address"
	}
	label := labelOf(a)
	return notify.Notification{
		Key: "signin:" + a.SessionID, Kind: notify.KindSecurity, Level: "warning",
		Title: username + " signed in from a new device",
		Body:  label + " from " + where + ". If that's not them, end the session in Users.",
		GenZ: &notify.Variant{Title: username + " pulled up on a new device",
			Body: label + " from " + where + ". Not them? End the session in Users."},
		Icon: "settings", Action: &notify.Action{App: "settings", Props: map[string]any{"section": "users"}},
	}
}

// otherSessions counts uid's live sessions and linked devices, leaving out
// the ones named.
func (s *Server) otherSessions(ctx context.Context, uid, exceptSession, exceptDevice string) (int, error) {
	web, err := s.Store.ActiveSessions(ctx, uid, time.Now())
	if err != nil {
		return 0, err
	}
	devs, err := s.Store.ListDevices(ctx, uid)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, a := range web {
		if a.ID != exceptSession {
			n++
		}
	}
	for _, d := range devs {
		if d.ID != exceptDevice {
			n++
		}
	}
	return n, nil
}

// ---------- ending sessions ----------

var errNoSession = errors.New("no such session")

// endSession ends one session or unlinks one device. With owner set, only
// that person's match. It returns whose it was and what it was.
func (s *Server) endSession(ctx context.Context, id, owner string) (uid, username, label string, err error) {
	if id == "" {
		return "", "", "", errNoSession
	}
	if a, err := s.Store.ActiveSession(ctx, id, time.Now()); err == nil {
		if owner != "" && a.UserID != owner {
			return "", "", "", errNoSession
		}
		if err := s.Store.RevokeFamily(ctx, a.ID, time.Now()); err != nil {
			return "", "", "", err
		}
		s.live.closeSession(a.UserID, a.ID)
		s.sessions.seen.Delete(a.ID)
		s.sessionsChanged(a.UserID)
		ua := useragent.Parse(a.UserAgent)
		return a.UserID, a.Username, labelFor(a.Kind, ua, "") + " " + a.IP, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return "", "", "", err
	}
	d, err := s.Store.RevokeDevice(ctx, id, owner, time.Now())
	if errors.Is(err, store.ErrNotFound) {
		return "", "", "", errNoSession
	}
	if err != nil {
		return "", "", "", err
	}
	s.link().forgetDevice(d.UserID, d.ID)
	s.sessionsChanged(d.UserID)
	return d.UserID, d.Username, d.Name + " (linked device)", nil
}

// endAllSessions signs uid out everywhere but keepSession and keepDevice
// (web sessions and linked devices) and reports how many it ended.
func (s *Server) endAllSessions(ctx context.Context, uid, keepSession, keepDevice string) (int, error) {
	ids, err := s.Store.RevokeUserSessionsExcept(ctx, uid, keepSession, time.Now())
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		s.live.closeSession(uid, id)
		s.sessions.seen.Delete(id)
	}
	n := len(ids)
	devs, err := s.Store.ListDevices(ctx, uid)
	if err != nil {
		return n, err
	}
	for _, d := range devs {
		if d.ID == keepDevice {
			continue
		}
		if _, err := s.Store.RevokeDevice(ctx, d.ID, uid, time.Now()); err != nil && !errors.Is(err, store.ErrNotFound) {
			return n, err
		}
		s.link().forgetDevice(uid, d.ID)
		n++
	}
	s.sessionsChanged(uid)
	return n, nil
}

// ---------- routes ----------

func (s *Server) sessionRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/auth/sessions", s.authed(s.authSessions))
	mux.Handle("DELETE /api/v1/auth/sessions/{id}", s.authed(s.authSessionEnd))
	mux.Handle("POST /api/v1/auth/sessions/end-others", s.authed(s.authSessionsEndOthers))
	mux.Handle("GET /api/v1/sessions", s.admin(s.adminSessions))
	mux.Handle("POST /api/v1/users/{name}/sessions/end", s.admin(s.adminUserSessionsEnd))
	mux.Handle("GET /api/v1/device/sessions", s.device(s.deviceSessions))
	mux.Handle("DELETE /api/v1/device/sessions/{id}", s.device(s.deviceSessionEnd))
}

type sessionsResponse struct {
	Sessions []sessionJSON `json:"sessions"`
	Alerts   []signinAlert `json:"alerts"`
}

// currentDevice is the linked device that opened the caller's session, if any.
func (s *Server) currentDevice(ctx context.Context, sid string) string {
	if a, err := s.Store.ActiveSession(ctx, sid, time.Now()); err == nil {
		return a.DeviceID
	}
	return ""
}

func (s *Server) authSessions(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	list, err := s.listSessions(r.Context(), u.ID, sessionFrom(r.Context()), "")
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sessionsResponse{Sessions: list, Alerts: s.link().alerts(u.ID, "")})
}

func (s *Server) authSessionEnd(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	owner := u.ID
	if u.Role == store.RoleAdmin {
		owner = "" // administrators may end anyone's
	}
	s.endOneAndAnswer(w, r, u, r.PathValue("id"), owner)
}

func (s *Server) endOneAndAnswer(w http.ResponseWriter, r *http.Request, actor *store.User, id, owner string) {
	uid, username, label, err := s.endSession(r.Context(), id, owner)
	if errors.Is(err, errNoSession) {
		writeError(w, http.StatusNotFound, "not_found", "That session already ended.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	detail := label
	if uid != actor.ID {
		detail += " (owner " + username + ")"
	}
	s.audit(r, actor.ID, "sessions.end", username, true, detail)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) authSessionsEndOthers(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	sid := sessionFrom(r.Context())
	n, err := s.endAllSessions(r.Context(), u.ID, sid, s.currentDevice(r.Context(), sid))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, u.ID, "sessions.end_others", u.Username, true, plural(n, "session"))
	writeJSON(w, http.StatusOK, map[string]int{"ended": n})
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return strconv.Itoa(n) + " " + what + "s"
}

// adminSessions lists everyone's sessions, or one person's (?user=name).
func (s *Server) adminSessions(w http.ResponseWriter, r *http.Request) {
	uid := ""
	if name := r.URL.Query().Get("user"); name != "" {
		p, err := s.Store.UserByUsername(r.Context(), name)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "No one by that name.")
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		uid = p.ID
	}
	list, err := s.listSessions(r.Context(), uid, sessionFrom(r.Context()), "")
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if uid != "" {
		for i := range list { // one person's list still says whose it is
			list[i].Username = r.URL.Query().Get("user")
		}
	}
	writeJSON(w, http.StatusOK, sessionsResponse{Sessions: list, Alerts: []signinAlert{}})
}

// adminUserSessionsEnd signs someone out everywhere (their phones too). An
// administrator doing it to themselves keeps the session they're using.
func (s *Server) adminUserSessionsEnd(w http.ResponseWriter, r *http.Request) {
	actor := userFrom(r.Context())
	p, err := s.Store.UserByUsername(r.Context(), r.PathValue("name"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "No one by that name.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	keepSession, keepDevice := "", ""
	if p.ID == actor.ID {
		keepSession = sessionFrom(r.Context())
		keepDevice = s.currentDevice(r.Context(), keepSession)
	}
	n, err := s.endAllSessions(r.Context(), p.ID, keepSession, keepDevice)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, actor.ID, "sessions.end_all", p.Username, true, plural(n, "session"))
	writeJSON(w, http.StatusOK, map[string]int{"ended": n})
}

// deviceSessions: the NoCap OS app's view of where its owner is signed in,
// plus the day's sign-in alerts.
func (s *Server) deviceSessions(w http.ResponseWriter, r *http.Request) {
	d, u := deviceFrom(r.Context()), userFrom(r.Context())
	list, err := s.listSessions(r.Context(), u.ID, "", d.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sessionsResponse{Sessions: list, Alerts: s.link().alerts(u.ID, d.ID)})
}

// deviceSessionEnd: "Not you? Sign it out" from the phone. Only its owner's
// sessions.
func (s *Server) deviceSessionEnd(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	s.endOneAndAnswer(w, r, u, r.PathValue("id"), u.ID)
}
