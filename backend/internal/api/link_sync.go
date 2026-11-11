package api

import (
	"encoding/base64"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"alfaos/alfad/internal/notify"
	"alfaos/alfad/internal/store"
)

// NoCap Link sync: a person's phone and NoCap OS share notifications and a
// clipboard. Everything here lives in memory (a day at most); the phone
// follows it all through one long-poll, /api/v1/device/events.

const (
	linkKeep          = 24 * time.Hour
	linkMaxPhoneNotes = 100 // per person
	linkMaxClips      = 20  // per person
	linkMaxDismissed  = 500 // per person
	linkMaxNotices    = 200 // server notifications remembered for the long-poll
	linkMaxKey        = 200
	linkMaxTitle      = 300
	linkMaxText       = 2000
	linkMaxName       = 200
	linkMaxIcon       = 48 << 10 // decoded bytes
	linkMaxClip       = 64 << 10
	linkWaitDefault   = 25 * time.Second
	linkWaitMax       = 30 * time.Second
)

type phoneNote struct {
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	Key        string `json:"key"`
	App        string `json:"app"`
	Package    string `json:"package"`
	Title      string `json:"title"`
	Text       string `json:"text"`
	Icon       string `json:"icon"`
	Posted     int64  `json:"posted"`

	at time.Time // when it arrived here
}

// linkClip is one clipboard entry. DeviceID is the phone it came from
// (source "phone") or the phone it's for (source "web"; "" = all of them).
type linkClip struct {
	ID       int64     `json:"id"`
	Text     string    `json:"text"`
	From     string    `json:"from"`
	Source   string    `json:"source"` // phone | web
	DeviceID string    `json:"device_id"`
	At       time.Time `json:"at"`
}

type linkDismissal struct {
	seq      int64
	deviceID string
	key      string
	at       time.Time
}

type linkNotice struct {
	seq int64
	n   notify.Notification
	at  time.Time
}

type linkUser struct {
	notes     map[string]*phoneNote // by device id + "\x00" + key
	clips     []linkClip            // oldest first
	dismissed []linkDismissal       // oldest first
}

// linkHub holds everyone's synced state and the event sequence the
// long-poll cursor follows.
type linkHub struct {
	mu      sync.Mutex
	seq     int64
	wake    chan struct{} // closed (and replaced) on every new event
	users   map[string]*linkUser
	notices []linkNotice // oldest first
	now     func() time.Time
}

func newLinkHub() *linkHub {
	// Start from the clock so a cursor from before a restart is never ahead.
	return &linkHub{seq: time.Now().UnixMicro(), wake: make(chan struct{}), users: map[string]*linkUser{}, now: time.Now}
}

// link is the server's hub, made on first use.
func (s *Server) link() *linkHub {
	s.linkOnce.Do(func() { s.linkSync = newLinkHub() })
	return s.linkSync
}

// bump moves the sequence on and wakes waiting long-polls. Call with mu held.
func (h *linkHub) bump() int64 {
	h.seq++
	close(h.wake)
	h.wake = make(chan struct{})
	return h.seq
}

// user is uid's state, pruned of anything older than a day. Call with mu held.
func (h *linkHub) user(uid string) *linkUser {
	u := h.users[uid]
	if u == nil {
		u = &linkUser{notes: map[string]*phoneNote{}}
		h.users[uid] = u
	}
	cut := h.now().Add(-linkKeep)
	for k, n := range u.notes {
		if n.at.Before(cut) {
			delete(u.notes, k)
		}
	}
	i := 0
	for i < len(u.clips) && u.clips[i].At.Before(cut) {
		i++
	}
	u.clips = u.clips[i:]
	i = 0
	for i < len(u.dismissed) && u.dismissed[i].at.Before(cut) {
		i++
	}
	u.dismissed = u.dismissed[i:]
	return u
}

func noteKey(deviceID, key string) string { return deviceID + "\x00" + key }

func (h *linkHub) putNote(uid string, n phoneNote) {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.user(uid)
	n.at = h.now()
	u.notes[noteKey(n.DeviceID, n.Key)] = &n
	for len(u.notes) > linkMaxPhoneNotes {
		var oldest string
		var ot time.Time
		for k, x := range u.notes {
			if oldest == "" || x.at.Before(ot) {
				oldest, ot = k, x.at
			}
		}
		delete(u.notes, oldest)
	}
	h.bump()
}

// dropNote removes a phone notification. When the web dismissed it, the
// phone hears about it on its next poll.
func (h *linkHub) dropNote(uid, deviceID, key string, fromWeb bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.user(uid)
	k := noteKey(deviceID, key)
	if _, ok := u.notes[k]; !ok {
		return
	}
	delete(u.notes, k)
	if fromWeb {
		u.dismissed = append(u.dismissed, linkDismissal{seq: h.bump(), deviceID: deviceID, key: key, at: h.now()})
		if len(u.dismissed) > linkMaxDismissed {
			u.dismissed = u.dismissed[len(u.dismissed)-linkMaxDismissed:]
		}
	}
}

func (h *linkHub) notes(uid string) []phoneNote {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.user(uid)
	out := make([]phoneNote, 0, len(u.notes))
	for _, n := range u.notes {
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Posted != out[j].Posted {
			return out[i].Posted > out[j].Posted
		}
		return out[i].at.After(out[j].at)
	})
	return out
}

func (h *linkHub) addClip(uid string, c linkClip) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.user(uid)
	c.ID = h.bump()
	c.At = h.now().UTC().Truncate(time.Second)
	u.clips = append(u.clips, c)
	if len(u.clips) > linkMaxClips {
		u.clips = u.clips[len(u.clips)-linkMaxClips:]
	}
	return c.ID
}

func (h *linkHub) clips(uid string) []linkClip {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.user(uid)
	out := make([]linkClip, 0, len(u.clips))
	for i := len(u.clips) - 1; i >= 0; i-- {
		out = append(out, u.clips[i])
	}
	return out
}

// forgetDevice drops what an unlinked phone left behind.
func (h *linkHub) forgetDevice(uid, deviceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.users[uid]
	if u == nil {
		return
	}
	for k, n := range u.notes {
		if n.DeviceID == deviceID {
			delete(u.notes, k)
		}
	}
}

// noticeAdded remembers a new server notification for the long-poll.
func (h *linkHub) noticeAdded(n notify.Notification) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.notices = append(h.notices, linkNotice{seq: h.bump(), n: n, at: h.now()})
	cut := h.now().Add(-linkKeep)
	i := 0
	for i < len(h.notices) && (len(h.notices)-i > linkMaxNotices || h.notices[i].at.Before(cut)) {
		i++
	}
	h.notices = h.notices[i:]
}

// noticeForEveryone: server notifications every linked person gets, not
// only administrators (the Notification Center's other items are about
// running the server). Today that's a new NoCapOS version.
func noticeForEveryone(n notify.Notification) bool {
	return n.Kind == notify.KindUpdate && strings.HasPrefix(n.Key, "update:")
}

type linkEvents struct {
	Cursor    int64                 `json:"cursor"`
	Notices   []notify.Notification `json:"notices"`
	Clipboard []linkClip            `json:"clipboard"`
	Dismissed []string              `json:"dismissed"`
}

func (e linkEvents) empty() bool {
	return len(e.Notices) == 0 && len(e.Clipboard) == 0 && len(e.Dismissed) == 0
}

// events is what one phone hasn't seen since after. Call with mu held.
func (h *linkHub) events(uid, deviceID string, admin bool, after int64) linkEvents {
	out := linkEvents{Cursor: h.seq, Notices: []notify.Notification{}, Clipboard: []linkClip{}, Dismissed: []string{}}
	if after <= 0 || after > h.seq {
		return out // a fresh start (or a cursor from another run): no history dump
	}
	u := h.user(uid)
	for _, x := range h.notices {
		if x.seq > after && (admin || noticeForEveryone(x.n)) {
			out.Notices = append(out.Notices, x.n)
		}
	}
	for _, c := range u.clips {
		if c.ID > after && c.Source == "web" && (c.DeviceID == "" || c.DeviceID == deviceID) {
			out.Clipboard = append(out.Clipboard, c)
		}
	}
	for _, d := range u.dismissed {
		if d.seq > after && d.deviceID == deviceID {
			out.Dismissed = append(out.Dismissed, d.key)
		}
	}
	return out
}

// ---------- routes ----------

// linkRoutes are NoCap OS's side (a signed-in person, their own data only);
// the phone's side is in deviceRoutes.
func (s *Server) linkRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/phone-notifications", s.authed(s.phoneNotesList))
	mux.Handle("DELETE /api/v1/phone-notifications/{device_id}/{key}", s.authed(s.phoneNotesDismiss))
	mux.Handle("GET /api/v1/clipboard", s.authed(s.clipboardList))
	mux.Handle("POST /api/v1/clipboard", s.authed(s.clipboardSend))
}

// cutRunes trims s to at most n characters.
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// devicePhoneNote upserts a notification shown on the phone.
func (s *Server) devicePhoneNote(w http.ResponseWriter, r *http.Request) {
	d, u := deviceFrom(r.Context()), userFrom(r.Context())
	var req struct {
		Key     string `json:"key"`
		App     string `json:"app"`
		Package string `json:"package"`
		Title   string `json:"title"`
		Text    string `json:"text"`
		Icon    string `json:"icon"`
		Posted  int64  `json:"posted"`
	}
	if !decodeJSONLimit(w, r, &req, 256<<10) {
		return
	}
	if req.Key == "" || utf8.RuneCountInString(req.Key) > linkMaxKey {
		writeError(w, http.StatusBadRequest, "validation", "key is required (up to 200 characters)")
		return
	}
	icon := strings.TrimSpace(req.Icon)
	if icon != "" {
		// Keep only a small, well-formed picture; otherwise show it without one.
		if b, err := base64.StdEncoding.DecodeString(icon); err != nil || len(b) > linkMaxIcon {
			icon = ""
		}
	}
	if req.Posted <= 0 {
		req.Posted = time.Now().UnixMilli()
	}
	s.link().putNote(u.ID, phoneNote{
		DeviceID: d.ID, DeviceName: d.Name, Key: req.Key,
		App: cutRunes(req.App, linkMaxName), Package: cutRunes(req.Package, linkMaxName),
		Title: cutRunes(req.Title, linkMaxTitle), Text: cutRunes(req.Text, linkMaxText),
		Icon: icon, Posted: req.Posted,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// devicePhoneNoteGone: the phone's notification went away there.
func (s *Server) devicePhoneNoteGone(w http.ResponseWriter, r *http.Request) {
	d, u := deviceFrom(r.Context()), userFrom(r.Context())
	s.link().dropNote(u.ID, d.ID, r.PathValue("key"), false)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) phoneNotesList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.link().notes(userFrom(r.Context()).ID)})
}

func (s *Server) phoneNotesDismiss(w http.ResponseWriter, r *http.Request) {
	s.link().dropNote(userFrom(r.Context()).ID, r.PathValue("device_id"), r.PathValue("key"), true)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// clipText reads {"text": ...} plus whatever else v holds; false when it
// answered already.
func clipText(w http.ResponseWriter, r *http.Request, v any, text *string) bool {
	if !decodeJSONLimit(w, r, v, 8*linkMaxClip) {
		return false
	}
	if *text == "" || len(*text) > linkMaxClip {
		writeError(w, http.StatusBadRequest, "validation", "Send some text (up to 64 KB).")
		return false
	}
	return true
}

// deviceClipboard: the phone shares its clipboard.
func (s *Server) deviceClipboard(w http.ResponseWriter, r *http.Request) {
	d, u := deviceFrom(r.Context()), userFrom(r.Context())
	var req struct {
		Text string `json:"text"`
	}
	if !clipText(w, r, &req, &req.Text) {
		return
	}
	id := s.link().addClip(u.ID, linkClip{Text: req.Text, From: d.Name, Source: "phone", DeviceID: d.ID})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (s *Server) clipboardList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.link().clips(userFrom(r.Context()).ID)})
}

// clipboardSend: NoCap OS shares text with one of the person's phones, or all.
func (s *Server) clipboardSend(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var req struct {
		Text     string `json:"text"`
		DeviceID string `json:"device_id"`
	}
	if !clipText(w, r, &req, &req.Text) {
		return
	}
	if req.DeviceID != "" {
		list, err := s.Store.ListDevices(r.Context(), u.ID)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		found := false
		for _, d := range list {
			if d.ID == req.DeviceID && d.UserID == u.ID {
				found = true
				break
			}
		}
		if !found {
			writeError(w, http.StatusNotFound, "not_found", "no such device")
			return
		}
	}
	id := s.link().addClip(u.ID, linkClip{Text: req.Text, From: "NoCap OS", Source: "web", DeviceID: req.DeviceID})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

// deviceEvents is the phone's long-poll: it answers as soon as there's
// something newer than ?after (or after ?wait seconds with nothing).
func (s *Server) deviceEvents(w http.ResponseWriter, r *http.Request) {
	d, u := deviceFrom(r.Context()), userFrom(r.Context())
	q := r.URL.Query()
	after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
	wait := linkWaitDefault
	if v := q.Get("wait"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			wait = min(max(time.Duration(n)*time.Second, 0), linkWaitMax)
		}
	}
	admin := u.Role == store.RoleAdmin
	h := s.link()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		h.mu.Lock()
		out := h.events(u.ID, d.ID, admin, after)
		wake := h.wake
		h.mu.Unlock()
		if !out.empty() || after <= 0 || after > out.Cursor {
			writeJSON(w, http.StatusOK, out)
			return
		}
		select {
		case <-wake:
		case <-timer.C:
			writeJSON(w, http.StatusOK, out)
			return
		case <-r.Context().Done():
			return
		}
	}
}
