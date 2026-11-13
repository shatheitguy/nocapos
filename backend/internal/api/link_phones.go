package api

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/store"
)

// NoCap Link, always connected: each phone's live state (battery, network,
// nearby or remote, online), things NoCap OS asks a phone to do (reply,
// decline a call, ring, open a link, take a file) and links a phone hands
// to NoCap OS. In memory only; the phone follows its actions through the
// same long-poll as everything else, and desktops follow the link.phone
// topic. Everything is the phone owner's alone: administrators don't see
// other people's phones here.

const (
	linkActionKeep  = 10 * time.Minute // undelivered actions go stale
	linkHandoffKeep = 10 * time.Minute
	linkMaxActions  = 50               // per person
	linkMaxHandoffs = 20               // per person
	linkOnlineGrace = 40 * time.Second // between two long-polls a phone still counts as online
	linkMaxURL      = 2048
	linkMaxReply    = 2000
)

// phoneState is what a phone last said about itself.
type phoneState struct {
	Battery  int       `json:"battery"` // 0-100, -1 unknown
	Charging bool      `json:"charging"`
	Network  string    `json:"network"` // wifi | cellular | ethernet | other | none | ""
	Signal   int       `json:"signal"`  // 0-4, -1 unknown
	Route    string    `json:"route"`   // local | remote | ""
	Ringing  bool      `json:"ringing"` // "Find my phone" is ringing
	At       time.Time `json:"at"`
}

// linkPoll tracks a phone's long-polls: open ones and when the last ended.
type linkPoll struct {
	open int
	last time.Time
	net  string // local | lan | internet, from the address it called from
}

// phoneAction is something NoCap OS asks one phone to do.
type phoneAction struct {
	ID       int64     `json:"id"`
	DeviceID string    `json:"device_id"`
	Kind     string    `json:"kind"` // reply | decline | ring | stop_ring | open | file
	Key      string    `json:"key,omitempty"`
	Text     string    `json:"text,omitempty"`
	URL      string    `json:"url,omitempty"`
	Root     string    `json:"root,omitempty"`
	Path     string    `json:"path,omitempty"`
	Name     string    `json:"name,omitempty"`
	Size     int64     `json:"size,omitempty"`
	Open     bool      `json:"open,omitempty"` // file: open it on the phone once it's saved
	At       time.Time `json:"at"`
}

// linkHandoff is a link (or one of the person's files) a phone asked NoCap
// OS to open.
type linkHandoff struct {
	ID         int64     `json:"id"`
	DeviceID   string    `json:"device_id"`
	DeviceName string    `json:"device_name"`
	URL        string    `json:"url,omitempty"`
	Title      string    `json:"title"`
	Root       string    `json:"root,omitempty"`
	Path       string    `json:"path,omitempty"`
	At         time.Time `json:"at"`
}

// linkEvent goes out on the link.phone topic.
type linkEvent struct {
	Type    string       `json:"type"` // changed | open | clip
	Handoff *linkHandoff `json:"handoff,omitempty"`
	Clip    *linkClip    `json:"clip,omitempty"`
}

// tell passes ev to uid's desktops. It doesn't take mu.
func (h *linkHub) tell(uid string, ev linkEvent) {
	if h.changed != nil {
		h.changed(uid, ev)
	}
}

// connected marks a phone's long-poll as open; call the result when it ends.
func (h *linkHub) connected(uid, deviceID, netKind string) func() {
	h.mu.Lock()
	p := h.polls[deviceID]
	if p == nil {
		p = &linkPoll{}
		h.polls[deviceID] = p
	}
	wasOnline := p.open > 0 || h.now().Sub(p.last) < linkOnlineGrace
	p.open++
	p.net = netKind
	h.mu.Unlock()
	if !wasOnline {
		h.tell(uid, linkEvent{Type: "changed"})
	}
	return func() {
		h.mu.Lock()
		if p.open > 0 {
			p.open--
		}
		p.last = h.now()
		h.mu.Unlock()
	}
}

// online says whether a phone is connected now, and from where. Call with mu held.
func (h *linkHub) online(deviceID string) (bool, string) {
	p := h.polls[deviceID]
	if p == nil {
		return false, ""
	}
	return p.open > 0 || h.now().Sub(p.last) < linkOnlineGrace, p.net
}

func (h *linkHub) setState(uid, deviceID string, st phoneState) {
	h.mu.Lock()
	u := h.user(uid)
	old := u.phones[deviceID]
	st.At = h.now().UTC().Truncate(time.Second)
	same := old != nil && old.Battery == st.Battery && old.Charging == st.Charging && old.Network == st.Network &&
		old.Signal == st.Signal && old.Route == st.Route && old.Ringing == st.Ringing
	u.phones[deviceID] = &st
	h.mu.Unlock()
	if !same {
		h.tell(uid, linkEvent{Type: "changed"})
	}
}

// addAction queues a for its phone and wakes the long-polls.
func (h *linkHub) addAction(uid string, a phoneAction) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.user(uid)
	a.ID = h.bump()
	a.At = h.now().UTC().Truncate(time.Second)
	u.actions = append(u.actions, a)
	if len(u.actions) > linkMaxActions {
		u.actions = u.actions[len(u.actions)-linkMaxActions:]
	}
	return a.ID
}

func (h *linkHub) addHandoff(uid string, x linkHandoff) linkHandoff {
	h.mu.Lock()
	u := h.user(uid)
	x.ID = h.bump()
	x.At = h.now().UTC().Truncate(time.Second)
	u.handoffs = append(u.handoffs, x)
	if len(u.handoffs) > linkMaxHandoffs {
		u.handoffs = u.handoffs[len(u.handoffs)-linkMaxHandoffs:]
	}
	h.mu.Unlock()
	h.tell(uid, linkEvent{Type: "open", Handoff: &x})
	return x
}

// ---------- live feed (link.phone topic) ----------

type linkFeed struct {
	mu   sync.Mutex
	subs map[string]map[chan linkEvent]struct{}
}

func (f *linkFeed) subscribe(uid string) (<-chan linkEvent, func()) {
	ch := make(chan linkEvent, 16)
	f.mu.Lock()
	if f.subs == nil {
		f.subs = map[string]map[chan linkEvent]struct{}{}
	}
	if f.subs[uid] == nil {
		f.subs[uid] = map[chan linkEvent]struct{}{}
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

func (f *linkFeed) publish(uid string, e linkEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for ch := range f.subs[uid] {
		select {
		case ch <- e:
		default: // a stuck reader reloads on its next poll anyway
		}
	}
}

// linkPhoneSource feeds the link.phone topic for one person.
func (s *Server) linkPhoneSource(uid string) func(ctx context.Context, emit func(any)) error {
	return func(ctx context.Context, emit func(any)) error {
		s.link() // make sure the hub reports to the feed
		ch, cancel := s.linkFeed.subscribe(uid)
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

// ---------- phone side ----------

// deviceState: the phone reports battery, network and how it reaches us.
func (s *Server) deviceState(w http.ResponseWriter, r *http.Request) {
	d, u := deviceFrom(r.Context()), userFrom(r.Context())
	var req struct {
		Battery  *int   `json:"battery"`
		Charging bool   `json:"charging"`
		Network  string `json:"network"`
		Signal   *int   `json:"signal"`
		Route    string `json:"route"`
		Ringing  bool   `json:"ringing"`
	}
	if !decodeJSONLimit(w, r, &req, 8<<10) {
		return
	}
	st := phoneState{Battery: -1, Signal: -1, Charging: req.Charging, Ringing: req.Ringing}
	if req.Battery != nil {
		if *req.Battery < 0 || *req.Battery > 100 {
			writeError(w, http.StatusBadRequest, "validation", "battery must be 0-100")
			return
		}
		st.Battery = *req.Battery
	}
	if req.Signal != nil {
		if *req.Signal < 0 || *req.Signal > 4 {
			writeError(w, http.StatusBadRequest, "validation", "signal must be 0-4")
			return
		}
		st.Signal = *req.Signal
	}
	switch req.Network {
	case "", "wifi", "cellular", "ethernet", "other", "none":
		st.Network = req.Network
	default:
		writeError(w, http.StatusBadRequest, "validation", "network must be wifi, cellular, ethernet, other or none")
		return
	}
	switch req.Route {
	case "", "local", "remote":
		st.Route = req.Route
	default:
		writeError(w, http.StatusBadRequest, "validation", "route must be local or remote")
		return
	}
	if st.Battery >= 0 {
		// Settings → Devices shows it too.
		_ = s.Store.TouchDevice(r.Context(), d.ID, time.Now(), &st.Battery, nil)
	}
	s.link().setState(u.ID, d.ID, st)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// webURL accepts only http(s) links of a sane size.
func webURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > linkMaxURL {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	return u.String(), true
}

// deviceOpen: "Open on NoCap" from the phone: a web link, or one of the
// person's own files (say, one it just sent with "Send to NoCap").
func (s *Server) deviceOpen(w http.ResponseWriter, r *http.Request) {
	d, u := deviceFrom(r.Context()), userFrom(r.Context())
	var req struct {
		URL   string `json:"url"`
		Title string `json:"title"`
		Root  string `json:"root"`
		Path  string `json:"path"`
	}
	if !decodeJSONLimit(w, r, &req, 16<<10) {
		return
	}
	if req.Root == "" && req.Path != "" {
		req.Root = s.sendHome(r) // a file it just sent: its NoCap Link folder is in the home
	}
	x := linkHandoff{DeviceID: d.ID, DeviceName: d.Name, Title: cutRunes(strings.TrimSpace(req.Title), linkMaxTitle)}
	if req.Root != "" || req.Path != "" {
		if req.Root == "" || req.Path == "" || len(req.Root) > 200 || len(req.Path) > 4096 || !s.canSendFile(r, u, req.Root, req.Path) {
			writeError(w, http.StatusNotFound, "not_found", "That file isn't there (or isn't yours to open).")
			return
		}
		x.Root, x.Path = req.Root, req.Path
		if x.Title == "" {
			x.Title = cutRunes(req.Path[strings.LastIndex(req.Path, "/")+1:], linkMaxTitle)
		}
	} else {
		link, ok := webURL(req.URL)
		if !ok {
			writeError(w, http.StatusBadRequest, "validation", "Send an http or https link (up to 2048 characters).")
			return
		}
		x.URL = link
	}
	x = s.link().addHandoff(u.ID, x)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": x.ID})
}

// ---------- NoCap OS side ----------

// phoneJSON is one of the person's phones for the Notification Center.
type phoneJSON struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Platform string      `json:"platform"`
	Online   bool        `json:"online"`
	Route    string      `json:"route"` // local | remote | "" (offline or unknown)
	LastSeen *time.Time  `json:"last_seen,omitempty"`
	State    *phoneState `json:"state,omitempty"`
}

// phonesList: the person's own linked phones with their live state, and
// links handed over from them in the last few minutes. Never anyone
// else's, administrators included.
func (s *Server) phonesList(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	list, err := s.Store.ListDevices(r.Context(), u.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	h := s.link()
	h.mu.Lock()
	lu := h.user(u.ID)
	out := make([]phoneJSON, 0, len(list))
	for _, d := range list {
		if d.UserID != u.ID || !d.RevokedAt.IsZero() {
			continue
		}
		p := phoneJSON{ID: d.ID, Name: d.Name, Platform: d.Platform}
		if !d.LastSeenAt.IsZero() {
			t := d.LastSeenAt
			p.LastSeen = &t
		}
		var netKind string
		p.Online, netKind = h.online(d.ID)
		if st := lu.phones[d.ID]; st != nil {
			c := *st
			p.State = &c
			if p.Online {
				p.Route = st.Route
			}
		}
		if p.Online && p.Route == "" {
			p.Route = routeOf(netKind)
		}
		if p.State == nil && d.Battery >= 0 {
			p.State = &phoneState{Battery: d.Battery, Signal: -1}
		}
		out = append(out, p)
	}
	handoffs := make([]linkHandoff, 0, len(lu.handoffs))
	for i := len(lu.handoffs) - 1; i >= 0; i-- {
		handoffs = append(handoffs, lu.handoffs[i])
	}
	h.mu.Unlock()
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		return out[i].Name < out[j].Name
	})
	writeJSON(w, http.StatusOK, map[string]any{"phones": out, "handoffs": handoffs})
}

// routeOf guesses nearby or remote from the address a phone called from.
func routeOf(netKind string) string {
	switch netKind {
	case "local", "lan":
		return "local"
	case "internet":
		return "remote"
	}
	return ""
}

// phoneActionSend: NoCap OS asks one of the person's phones to do something.
func (s *Server) phoneActionSend(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	devID := r.PathValue("device_id")
	var req struct {
		Kind string `json:"kind"`
		Key  string `json:"key"`
		Text string `json:"text"`
		URL  string `json:"url"`
		Root string `json:"root"`
		Path string `json:"path"`
		Open bool   `json:"open"`
	}
	if !decodeJSONLimit(w, r, &req, 16<<10) {
		return
	}
	// Only the owner's own phones; an administrator gets "no such device"
	// for anyone else's, like everyone.
	d, err := s.Store.DeviceByID(r.Context(), devID)
	if err != nil || d.UserID != u.ID || !d.RevokedAt.IsZero() {
		writeError(w, http.StatusNotFound, "not_found", "no such device")
		return
	}
	a := phoneAction{DeviceID: d.ID, Kind: req.Kind}
	bad := func(msg string) { writeError(w, http.StatusBadRequest, "validation", msg) }
	switch req.Kind {
	case "ring", "stop_ring":
	case "reply", "decline":
		if req.Key == "" || utf8.RuneCountInString(req.Key) > linkMaxKey {
			bad("key is required")
			return
		}
		h := s.link()
		h.mu.Lock()
		n := h.user(u.ID).notes[noteKey(d.ID, req.Key)]
		ok := n != nil && ((req.Kind == "reply" && n.CanReply) || (req.Kind == "decline" && n.CanDecline))
		h.mu.Unlock()
		if !ok {
			writeError(w, http.StatusConflict, "gone", "That notification isn't on the phone anymore.")
			return
		}
		a.Key = req.Key
		if req.Kind == "reply" {
			text := strings.TrimSpace(req.Text)
			if text == "" || utf8.RuneCountInString(text) > linkMaxReply {
				bad("Write a reply (up to 2000 characters).")
				return
			}
			a.Text = text
		}
	case "open":
		link, ok := webURL(req.URL)
		if !ok {
			bad("Send an http or https link (up to 2048 characters).")
			return
		}
		a.URL = link
	case "file":
		if req.Root == "" || req.Path == "" || len(req.Root) > 200 || len(req.Path) > 4096 {
			bad("root and path are required")
			return
		}
		// The phone fetches it with its own token, so it gets no more than
		// its owner may read; this only checks the file is there now.
		size, ok := s.fileSize(r, u, req.Root, req.Path)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "That file isn't there (or isn't yours to send).")
			return
		}
		a.Root, a.Path, a.Size, a.Open = req.Root, req.Path, size, req.Open
		a.Name = req.Path[strings.LastIndex(req.Path, "/")+1:]
	default:
		bad("kind must be reply, decline, ring, stop_ring, open or file")
		return
	}
	id := s.link().addAction(u.ID, a)
	s.audit(r, u.ID, "link.phone_action", d.Name, true, a.Kind)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

// canSendFile: u may read root/path and it's a regular file.
func (s *Server) canSendFile(r *http.Request, u *store.User, root, path string) bool {
	_, ok := s.fileSize(r, u, root, path)
	return ok
}

// fileSize is the size of root/path when u may read it and it's a regular file.
func (s *Server) fileSize(r *http.Request, u *store.User, root, path string) (int64, bool) {
	rel, err := files.Clean(path)
	if err != nil || s.Files == nil || !s.ticketHolderCan(r.Context(), u, root, rel) {
		return 0, false
	}
	f, fi, err := s.Files.Open(root, rel)
	if err != nil {
		return 0, false
	}
	_ = f.Close()
	return fi.Size(), fi.Mode().IsRegular()
}
