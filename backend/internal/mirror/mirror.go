// Package mirror relays a linked phone's live screen to its owner's NoCap OS
// windows, and their taps and keys back to the phone.
//
// The phone (NoCap OS app) captures its screen, encodes it with its hardware
// H.264 encoder and sends each encoded frame as one binary WebSocket message:
//
//	byte 0      type: 1 codec config (SPS/PPS), 2 key frame, 3 frame, 5 JPEG
//	bytes 1..8  presentation time, microseconds, big-endian
//	bytes 9..   Annex-B H.264 (or one JPEG image)
//
// Text messages are small JSON objects with a "t" field. The phone sends
// "meta" (size, rotation, encoder settings, whether control is on) and
// "state"; viewers send input ("tap", "swipe", "text", ...), which is passed
// to the phone as is once its type is known and its size is sane.
//
// One Room per phone. Each viewer has its own bounded queue: a viewer that
// falls behind skips frames up to the next key frame (and the phone is asked
// for one) instead of slowing the phone or the other viewers down.
//
// Who may watch which phone is decided by the caller (only the phone's
// owner); this package only keeps rooms apart.
package mirror

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Binary packet types (first byte).
const (
	TypeConfig byte = 1
	TypeKey    byte = 2
	TypeDelta  byte = 3
	TypeJPEG   byte = 5
)

// HeaderLen is the type byte plus the 8-byte timestamp.
const HeaderLen = 9

const (
	// MaxViewers per phone.
	MaxViewers = 4
	// viewerQueue: frames a viewer may lag behind before it skips ahead.
	viewerQueue = 120
	// phoneQueue: control messages waiting for the phone.
	phoneQueue = 256
	// MaxControl is the largest message a viewer may send (typed text).
	MaxControl = 8 << 10
	// MaxFrame is the largest frame a phone may send.
	MaxFrame = 4 << 20
	// MaxQueued is how many frame bytes one window may lag behind before it
	// skips ahead to the next key frame (memory per window stays bounded).
	MaxQueued = 16 << 20
	// keyEvery: at most one key frame request per this long.
	keyEvery = 750 * time.Millisecond
	// AskTTL: how long a "please start mirroring" stays for the phone.
	AskTTL = 2 * time.Minute
)

var (
	ErrTooManyViewers = errors.New("this phone is already shown in 4 windows")
	ErrNotYours       = errors.New("this phone belongs to someone else")
	ErrBadPacket      = errors.New("bad packet")
	ErrBadControl     = errors.New("unknown or oversized control message")
)

// Out is one message for a WebSocket: binary (a frame) or text (JSON).
type Out struct {
	Text bool
	Data []byte
	// counted: bytes this message holds of its viewer's MaxQueued budget.
	counted int
}

// controls is what a viewer may ask the phone to do.
var controls = map[string]bool{
	"tap": true, "long": true, "swipe": true, "scroll": true,
	"global": true, "text": true, "key": true, "vol": true,
	"keyframe": true, "quality": true,
}

// Hub holds every phone's room and pending start requests.
type Hub struct {
	mu    sync.Mutex
	rooms map[string]*Room
	asks  map[string]Ask
	now   func() time.Time
}

// Ask is a viewer's "please start mirroring", waiting for the phone.
type Ask struct {
	ID  string    `json:"id"`
	Seq int64     `json:"-"`
	At  time.Time `json:"at"`
}

func NewHub() *Hub {
	return &Hub{rooms: map[string]*Room{}, asks: map[string]Ask{}, now: time.Now}
}

// Room is deviceID's room, made on first use. A room belongs to one user:
// asking for it as anyone else fails.
func (h *Hub) Room(deviceID, userID string) (*Room, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.rooms[deviceID]
	if r == nil {
		r = &Room{hub: h, DeviceID: deviceID, UserID: userID, viewers: map[*Viewer]struct{}{}}
		h.rooms[deviceID] = r
	}
	if r.UserID != userID {
		return nil, ErrNotYours
	}
	r.mu.Lock()
	r.pending++
	r.mu.Unlock()
	return r, nil
}

// Release gives back a room from Room that wasn't joined (AttachPhone or
// AddViewer); an empty room is then dropped.
func (h *Hub) Release(r *Room) {
	r.mu.Lock()
	if r.pending > 0 {
		r.pending--
	}
	r.mu.Unlock()
	h.dropIfEmpty(r)
}

// Peek is deviceID's room if there is one (nil otherwise).
func (h *Hub) Peek(deviceID string) *Room {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.rooms[deviceID]
}

// Forget drops deviceID's room (the phone was unlinked), closing everything.
func (h *Hub) Forget(deviceID string) {
	h.mu.Lock()
	r := h.rooms[deviceID]
	delete(h.rooms, deviceID)
	delete(h.asks, deviceID)
	h.mu.Unlock()
	if r != nil {
		r.closeAll()
	}
}

// dropIfEmpty removes r when nobody is in it anymore.
func (h *Hub) dropIfEmpty(r *Room) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r.mu.Lock()
	empty := r.phone == nil && len(r.viewers) == 0 && r.pending == 0
	r.mu.Unlock()
	if empty && h.rooms[r.DeviceID] == r {
		delete(h.rooms, r.DeviceID)
	}
}

// SetAsk records that someone wants deviceID to start mirroring; seq is the
// event cursor the phone's long-poll follows.
func (h *Hub) SetAsk(deviceID string, seq int64) Ask {
	a := Ask{ID: NewToken(8), Seq: seq, At: h.now()}
	h.mu.Lock()
	h.asks[deviceID] = a
	h.mu.Unlock()
	return a
}

// AskSince is deviceID's pending ask newer than the cursor after, if any.
func (h *Hub) AskSince(deviceID string, after int64) *Ask {
	h.mu.Lock()
	defer h.mu.Unlock()
	a, ok := h.asks[deviceID]
	if !ok {
		return nil
	}
	if h.now().Sub(a.At) > AskTTL {
		delete(h.asks, deviceID)
		return nil
	}
	if a.Seq <= after {
		return nil
	}
	return &a
}

// ClearAsk forgets deviceID's pending ask (it started, or said no).
func (h *Hub) ClearAsk(deviceID string) {
	h.mu.Lock()
	delete(h.asks, deviceID)
	h.mu.Unlock()
}

// Status of one phone, for the device list.
type Status struct {
	Online  bool      `json:"online"` // the phone is streaming
	Viewers int       `json:"viewers"`
	State   string    `json:"state"` // "", asking, denied, streaming, unsupported
	Since   time.Time `json:"since,omitzero"`
	Control bool      `json:"control"` // the phone can take taps and keys
	Link    string    `json:"link"`    // "nearby" (same network), "remote" or "" (not streaming)
}

func (h *Hub) Status(deviceID string) Status {
	r := h.Peek(deviceID)
	if r == nil {
		return Status{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return Status{Online: r.phone != nil, Viewers: len(r.viewers), State: r.state, Since: r.since, Control: r.control, Link: r.link}
}

// Room is one phone and the windows showing it.
type Room struct {
	hub      *Hub
	DeviceID string
	UserID   string

	mu      sync.Mutex
	phone   *Phone
	viewers map[*Viewer]struct{}
	config  []byte // the last codec config packet
	meta    []byte // the phone's last "meta" message
	state   string
	since   time.Time
	control bool
	lastKey time.Time
	link    string // how the phone is connected: nearby or remote
	pending int    // handed out by Hub.Room, not joined yet
}

// Phone is the phone's side of a room.
type Phone struct {
	room *Room
	// Control carries viewers' input and the relay's own requests to the phone.
	Control chan []byte
	// Done closes when this phone connection is replaced or the room closes.
	Done chan struct{}
	once sync.Once
}

func (p *Phone) stop() { p.once.Do(func() { close(p.Done) }) }

// Viewer is one window showing the phone.
type Viewer struct {
	room *Room
	// Out carries frames and messages for the window.
	Out  chan Out
	Done chan struct{}
	once sync.Once
	jpeg bool
	// skipping: frames are dropped until the next key frame.
	skipping bool
	// queued: frame bytes waiting in Out (at most MaxQueued), so a slow
	// window holds a few seconds of video, not 120 of the largest frames.
	queued atomic.Int64
}

func (v *Viewer) stop() { v.once.Do(func() { close(v.Done) }) }

// Sent tells the room o left Out (call it for every message taken).
func (v *Viewer) Sent(o Out) {
	if o.counted > 0 {
		v.queued.Add(-int64(o.counted))
	}
}

// push queues frame bytes within the byte budget; false when full.
func (v *Viewer) push(data []byte) bool {
	n := int64(len(data))
	if v.queued.Load()+n > MaxQueued {
		return false
	}
	select {
	case v.Out <- Out{Data: data, counted: len(data)}:
		v.queued.Add(n)
		return true
	default:
		return false
	}
}

func textMsg(v any) Out {
	b, _ := json.Marshal(v)
	return Out{Text: true, Data: b}
}

// AttachPhone connects the phone. A phone that reconnects replaces its
// older connection, which is told to stop.
func (r *Room) AttachPhone() *Phone { return r.AttachPhoneVia("") }

// AttachPhoneVia is AttachPhone, noting how the phone reaches the server:
// "nearby" (the same network) or "remote".
func (r *Room) AttachPhoneVia(link string) *Phone {
	p := &Phone{room: r, Control: make(chan []byte, phoneQueue), Done: make(chan struct{})}
	r.mu.Lock()
	if r.pending > 0 {
		r.pending--
	}
	old := r.phone
	r.phone = p
	r.config, r.meta = nil, nil
	r.state = "streaming"
	r.since = r.hub.now()
	r.link = link
	n, jpeg := len(r.viewers), r.wantsJPEG()
	r.broadcastLocked(textMsg(map[string]any{"t": "phone", "online": true, "link": link}))
	r.broadcastLocked(textMsg(map[string]any{"t": "state", "state": r.state}))
	for v := range r.viewers {
		v.skipping = true
	}
	r.mu.Unlock()
	if old != nil {
		old.stop()
	}
	r.hub.ClearAsk(r.DeviceID)
	p.send(map[string]any{"t": "viewers", "n": n})
	p.send(map[string]any{"t": "mode", "m": modeName(jpeg)})
	return p
}

// DetachPhone is called when the phone's connection ends.
func (r *Room) DetachPhone(p *Phone) {
	p.stop()
	r.mu.Lock()
	if r.phone != p {
		r.mu.Unlock()
		return
	}
	r.phone = nil
	r.config, r.meta = nil, nil
	r.state = ""
	r.since = time.Time{}
	r.control = false
	r.link = ""
	r.broadcastLocked(textMsg(map[string]any{"t": "phone", "online": false, "link": ""}))
	r.mu.Unlock()
	r.hub.dropIfEmpty(r)
}

func modeName(jpeg bool) string {
	if jpeg {
		return "jpeg"
	}
	return "h264"
}

// send queues a control message for the phone; dropped when its queue is full.
func (p *Phone) send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	p.sendRaw(b)
}

func (p *Phone) sendRaw(b []byte) {
	select {
	case <-p.Done:
	case p.Control <- b:
	default:
	}
}

// wantsJPEG: some window can't decode H.264. Call with mu held.
func (r *Room) wantsJPEG() bool {
	for v := range r.viewers {
		if v.jpeg {
			return true
		}
	}
	return false
}

// askKeyLocked asks the phone for a key frame, at most every keyEvery.
func (r *Room) askKeyLocked() {
	if r.phone == nil {
		return
	}
	now := r.hub.now()
	if now.Sub(r.lastKey) < keyEvery {
		return
	}
	r.lastKey = now
	r.phone.send(map[string]any{"t": "keyframe"})
}

// FromPhone takes one message from the phone and passes it on.
func (r *Room) FromPhone(p *Phone, text bool, data []byte) error {
	if text {
		return r.phoneText(p, data)
	}
	if len(data) < HeaderLen || len(data) > MaxFrame {
		return ErrBadPacket
	}
	typ := data[0]
	switch typ {
	case TypeConfig, TypeKey, TypeDelta, TypeJPEG:
	default:
		return ErrBadPacket
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phone != p {
		return nil // replaced by a newer connection
	}
	if typ == TypeConfig {
		r.config = append([]byte(nil), data...)
	}
	need := false
	for v := range r.viewers {
		if !v.deliver(typ, data, r.config, r.meta) {
			need = true
		}
	}
	if need {
		r.askKeyLocked()
	}
	return nil
}

// deliver queues one frame for v. False when v needs a key frame to go on.
func (v *Viewer) deliver(typ byte, data, config, meta []byte) bool {
	switch typ {
	case TypeConfig:
		if v.skipping {
			return true // kept by the room; sent again with the next key frame
		}
	case TypeDelta:
		if v.skipping {
			return false
		}
	case TypeKey:
		if v.skipping {
			// Back in step: the decoder gets the latest settings first.
			if cap(v.Out)-len(v.Out) < 3 || v.queued.Load()+int64(len(config)+len(data)) > MaxQueued {
				return false
			}
			if meta != nil {
				v.Out <- Out{Text: true, Data: meta}
			}
			if config != nil && !v.push(config) {
				return false
			}
			v.skipping = false
		}
	case TypeJPEG:
		v.skipping = false
	}
	if v.push(data) {
		return true
	}
	v.skipping = true
	return false
}

// phoneText handles "meta" and "state" from the phone.
func (r *Room) phoneText(p *Phone, data []byte) error {
	if len(data) > 16<<10 {
		return ErrBadPacket
	}
	var m struct {
		T       string `json:"t"`
		State   string `json:"state"`
		Control *bool  `json:"control"`
	}
	if json.Unmarshal(data, &m) != nil {
		return ErrBadPacket
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phone != p {
		return nil
	}
	switch m.T {
	case "meta":
		r.meta = append([]byte(nil), data...)
		if m.Control != nil {
			r.control = *m.Control
		}
	case "state":
		switch m.State {
		case "streaming", "paused", "stopping":
			r.state = m.State
		default:
			return ErrBadPacket
		}
	default:
		return nil // something newer than this relay: ignore
	}
	r.broadcastLocked(Out{Text: true, Data: append([]byte(nil), data...)})
	return nil
}

// SetState is what the phone says before it streams: "asking" (the consent
// prompt is up), "denied" or "unsupported".
func (r *Room) SetState(state string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phone != nil && state != "stopped" {
		return // it's streaming; that wins
	}
	if state == "stopped" {
		state = ""
	}
	r.state = state
	r.broadcastLocked(textMsg(map[string]any{"t": "state", "state": state}))
}

// broadcastLocked queues a message for every viewer (best effort). Call with mu held.
func (r *Room) broadcastLocked(o Out) {
	for v := range r.viewers {
		select {
		case v.Out <- o:
		default:
			v.skipping = true
		}
	}
}

// AddViewer adds a window. jpeg: it can't decode H.264, so the phone sends
// pictures instead.
func (r *Room) AddViewer(jpeg bool) (*Viewer, error) {
	r.mu.Lock()
	if len(r.viewers) >= MaxViewers {
		r.mu.Unlock()
		return nil, ErrTooManyViewers
	}
	v := &Viewer{room: r, Out: make(chan Out, viewerQueue), Done: make(chan struct{}), jpeg: jpeg, skipping: true}
	if r.pending > 0 {
		r.pending--
	}
	before := r.wantsJPEG()
	r.viewers[v] = struct{}{}
	v.Out <- textMsg(map[string]any{"t": "hello", "online": r.phone != nil, "state": r.state, "control": r.control, "link": r.link})
	if r.meta != nil {
		v.Out <- Out{Text: true, Data: r.meta}
	}
	p, n, after := r.phone, len(r.viewers), r.wantsJPEG()
	if p != nil {
		r.lastKey = time.Time{}
		r.askKeyLocked()
	}
	r.mu.Unlock()
	if p != nil {
		p.send(map[string]any{"t": "viewers", "n": n})
		if before != after {
			p.send(map[string]any{"t": "mode", "m": modeName(after)})
		}
	}
	return v, nil
}

// RemoveViewer is called when a window's connection ends.
func (r *Room) RemoveViewer(v *Viewer) {
	v.stop()
	r.mu.Lock()
	if _, ok := r.viewers[v]; !ok {
		r.mu.Unlock()
		return
	}
	before := r.wantsJPEG()
	delete(r.viewers, v)
	p, n, after := r.phone, len(r.viewers), r.wantsJPEG()
	r.mu.Unlock()
	if p != nil {
		p.send(map[string]any{"t": "viewers", "n": n})
		if before != after {
			p.send(map[string]any{"t": "mode", "m": modeName(after)})
		}
	}
	r.hub.dropIfEmpty(r)
}

// FromViewer takes one input message from a window: answered here (ping),
// or checked and passed to the phone.
func (r *Room) FromViewer(v *Viewer, data []byte) error {
	if len(data) > MaxControl {
		return ErrBadControl
	}
	var m struct {
		T  string `json:"t"`
		ID int64  `json:"id"`
	}
	if json.Unmarshal(data, &m) != nil {
		return ErrBadControl
	}
	if m.T == "ping" {
		select {
		case v.Out <- textMsg(map[string]any{"t": "pong", "id": m.ID}):
		default:
		}
		return nil
	}
	if !controls[m.T] {
		return ErrBadControl
	}
	r.mu.Lock()
	p := r.phone
	if m.T == "keyframe" {
		v.skipping = true
		r.askKeyLocked()
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()
	if p != nil {
		p.sendRaw(append([]byte(nil), data...))
	}
	return nil
}

// closeAll ends the phone's and every window's connection.
func (r *Room) closeAll() {
	r.mu.Lock()
	p := r.phone
	vs := make([]*Viewer, 0, len(r.viewers))
	for v := range r.viewers {
		vs = append(vs, v)
	}
	r.mu.Unlock()
	if p != nil {
		p.stop()
	}
	for _, v := range vs {
		v.stop()
	}
}

// NewToken is n random bytes, hex.
func NewToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Ticket lets one window open the viewer WebSocket, once.
type Ticket struct {
	UserID   string
	Session  string
	DeviceID string
	JPEG     bool
	exp      time.Time
}

// Tickets are one-time viewer tickets.
type Tickets struct {
	mu  sync.Mutex
	m   map[string]Ticket
	ttl time.Duration
	now func() time.Time
}

func NewTickets(ttl time.Duration) *Tickets {
	return &Tickets{m: map[string]Ticket{}, ttl: ttl, now: time.Now}
}

func (t *Tickets) Issue(tk Ticket) string {
	id := NewToken(24)
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	for k, v := range t.m {
		if now.After(v.exp) {
			delete(t.m, k)
		}
	}
	tk.exp = now.Add(t.ttl)
	t.m[id] = tk
	return id
}

func (t *Tickets) Redeem(id string) (Ticket, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tk, ok := t.m[id]
	delete(t.m, id)
	if !ok || t.now().After(tk.exp) {
		return Ticket{}, false
	}
	return tk, true
}
