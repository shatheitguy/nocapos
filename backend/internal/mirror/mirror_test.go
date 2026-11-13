package mirror

import (
	"encoding/json"
	"testing"
	"time"
)

func frame(typ byte, body ...byte) []byte {
	return append([]byte{typ, 0, 0, 0, 0, 0, 0, 0, 1}, body...)
}

// drain is everything queued for v right now.
func drain(v *Viewer) []Out {
	var out []Out
	for {
		select {
		case o := <-v.Out:
			out = append(out, o)
		default:
			return out
		}
	}
}

func drainPhone(p *Phone) []map[string]any {
	var out []map[string]any
	for {
		select {
		case b := <-p.Control:
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			out = append(out, m)
		default:
			return out
		}
	}
}

func has(msgs []map[string]any, t string) bool {
	for _, m := range msgs {
		if m["t"] == t {
			return true
		}
	}
	return false
}

func TestRoomBelongsToOneUser(t *testing.T) {
	h := NewHub()
	if _, err := h.Room("dev1", "alex"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Room("dev1", "boss"); err != ErrNotYours {
		t.Fatalf("another user's room: %v", err)
	}
}

func TestViewerStartsAtKeyFrame(t *testing.T) {
	h := NewHub()
	r, _ := h.Room("dev1", "alex")
	p := r.AttachPhone()
	v, err := r.AddViewer(false)
	if err != nil {
		t.Fatal(err)
	}
	if !has(drainPhone(p), "keyframe") {
		t.Fatal("a new viewer should ask the phone for a key frame")
	}
	if hello := drain(v); len(hello) == 0 || !hello[0].Text {
		t.Fatalf("hello: %+v", hello)
	}
	_ = r.FromPhone(p, false, frame(TypeConfig, 0, 0, 0, 1, 0x67))
	_ = r.FromPhone(p, false, frame(TypeDelta, 1)) // before any key frame: skipped
	if got := drain(v); len(got) != 0 {
		t.Fatalf("frames before the first key frame: %d", len(got))
	}
	_ = r.FromPhone(p, true, []byte(`{"t":"meta","w":1080,"h":2400,"control":true}`))
	drain(v)
	_ = r.FromPhone(p, false, frame(TypeKey, 2))
	_ = r.FromPhone(p, false, frame(TypeDelta, 3))
	got := drain(v)
	// meta, config, key, delta
	if len(got) != 4 || !got[0].Text || got[1].Data[0] != TypeConfig || got[2].Data[0] != TypeKey || got[3].Data[0] != TypeDelta {
		t.Fatalf("after a key frame: %+v", got)
	}
	if !h.Status("dev1").Control || !h.Status("dev1").Online {
		t.Fatalf("status: %+v", h.Status("dev1"))
	}
}

func TestSlowViewerSkipsAhead(t *testing.T) {
	h := NewHub()
	r, _ := h.Room("dev1", "alex")
	p := r.AttachPhone()
	slow, _ := r.AddViewer(false)
	fast, _ := r.AddViewer(false)
	drain(slow)
	drain(fast)
	_ = r.FromPhone(p, false, frame(TypeKey))
	drain(fast)
	drainPhone(p)
	r.hub.now = func() time.Time { return time.Now().Add(time.Hour) } // past the key frame rate limit
	for i := 0; i < viewerQueue+10; i++ {
		_ = r.FromPhone(p, false, frame(TypeDelta))
		drain(fast) // the fast window keeps up
	}
	if !has(drainPhone(p), "keyframe") {
		t.Fatal("a full queue should ask for a key frame")
	}
	if !slow.skipping {
		t.Fatal("the slow window should be skipping")
	}
	if fast.skipping {
		t.Fatal("the fast window must not be held back")
	}
}

func TestViewerControls(t *testing.T) {
	h := NewHub()
	r, _ := h.Room("dev1", "alex")
	p := r.AttachPhone()
	v, _ := r.AddViewer(false)
	drain(v)
	drainPhone(p)
	if err := r.FromViewer(v, []byte(`{"t":"tap","x":0.5,"y":0.5}`)); err != nil {
		t.Fatal(err)
	}
	if got := drainPhone(p); len(got) != 1 || got[0]["t"] != "tap" {
		t.Fatalf("tap: %+v", got)
	}
	for _, bad := range []string{`{"t":"exec","cmd":"rm"}`, `not json`, `{"t":"text","s":"` + string(make([]byte, MaxControl)) + `"}`} {
		if err := r.FromViewer(v, []byte(bad)); err != ErrBadControl {
			t.Fatalf("%.30q: %v", bad, err)
		}
	}
	if len(drainPhone(p)) != 0 {
		t.Fatal("rejected input reached the phone")
	}
	_ = r.FromViewer(v, []byte(`{"t":"ping","id":7}`))
	if got := drain(v); len(got) != 1 || string(got[0].Data) != `{"id":7,"t":"pong"}` {
		t.Fatalf("pong: %+v", got)
	}
}

func TestViewerLimitAndJPEGMode(t *testing.T) {
	h := NewHub()
	r, _ := h.Room("dev1", "alex")
	p := r.AttachPhone()
	var vs []*Viewer
	for i := 0; i < MaxViewers; i++ {
		v, err := r.AddViewer(i == 0)
		if err != nil {
			t.Fatal(err)
		}
		vs = append(vs, v)
	}
	if _, err := r.AddViewer(false); err != ErrTooManyViewers {
		t.Fatalf("over the limit: %v", err)
	}
	msgs := drainPhone(p)
	jpeg := false
	for _, m := range msgs {
		if m["t"] == "mode" {
			jpeg = m["m"] == "jpeg"
		}
	}
	if !jpeg {
		t.Fatalf("a window without H.264 should switch the phone to pictures: %+v", msgs)
	}
	r.RemoveViewer(vs[0])
	msgs = drainPhone(p)
	for _, m := range msgs {
		if m["t"] == "mode" {
			jpeg = m["m"] == "jpeg"
		}
	}
	if jpeg {
		t.Fatalf("back to H.264 when that window leaves: %+v", msgs)
	}
}

func TestRoomsAreDroppedWhenEmpty(t *testing.T) {
	h := NewHub()
	r, _ := h.Room("dev1", "alex")
	v, _ := r.AddViewer(false)
	r.RemoveViewer(v)
	if h.Peek("dev1") != nil {
		t.Fatal("an empty room should be dropped")
	}
	r, _ = h.Room("dev1", "alex")
	h.Release(r)
	if h.Peek("dev1") != nil {
		t.Fatal("a released room should be dropped")
	}
	// Once empty, another user may have the device id's room (it was relinked).
	if _, err := h.Room("dev1", "boss"); err != nil {
		t.Fatal(err)
	}
}

func TestReconnectReplacesPhone(t *testing.T) {
	h := NewHub()
	r, _ := h.Room("dev1", "alex")
	p1 := r.AttachPhone()
	r2, _ := h.Room("dev1", "alex")
	p2 := r2.AttachPhone()
	select {
	case <-p1.Done:
	default:
		t.Fatal("the old connection should be told to stop")
	}
	r.DetachPhone(p1) // the old one ending must not take the new one down
	if !h.Status("dev1").Online {
		t.Fatal("still streaming on the new connection")
	}
	r.DetachPhone(p2)
	if h.Status("dev1").Online {
		t.Fatal("offline after the phone left")
	}
}

func TestAsks(t *testing.T) {
	h := NewHub()
	now := time.Now()
	h.now = func() time.Time { return now }
	h.SetAsk("dev1", 10)
	if h.AskSince("dev1", 9) == nil || h.AskSince("dev1", 10) != nil || h.AskSince("dev2", 0) != nil {
		t.Fatal("asks follow the cursor, per device")
	}
	now = now.Add(AskTTL + time.Second)
	if h.AskSince("dev1", 0) != nil {
		t.Fatal("asks expire")
	}
	h.SetAsk("dev1", 11)
	r, _ := h.Room("dev1", "alex")
	r.AttachPhone()
	if h.AskSince("dev1", 0) != nil {
		t.Fatal("streaming clears the ask")
	}
}

func TestTickets(t *testing.T) {
	tk := NewTickets(time.Minute)
	id := tk.Issue(Ticket{UserID: "alex", DeviceID: "dev1"})
	got, ok := tk.Redeem(id)
	if !ok || got.UserID != "alex" || got.DeviceID != "dev1" {
		t.Fatalf("redeem: %+v %v", got, ok)
	}
	if _, ok := tk.Redeem(id); ok {
		t.Fatal("tickets work once")
	}
	now := time.Now()
	tk.now = func() time.Time { return now }
	id = tk.Issue(Ticket{UserID: "alex"})
	now = now.Add(2 * time.Minute)
	if _, ok := tk.Redeem(id); ok {
		t.Fatal("tickets expire")
	}
}

func TestBadPhonePackets(t *testing.T) {
	h := NewHub()
	r, _ := h.Room("dev1", "alex")
	p := r.AttachPhone()
	for _, b := range [][]byte{{}, {9, 0, 0, 0, 0, 0, 0, 0, 0}, {TypeKey}} {
		if err := r.FromPhone(p, false, b); err != ErrBadPacket {
			t.Fatalf("%v: %v", b, err)
		}
	}
	if err := r.FromPhone(p, true, []byte(`{"t":"state","state":"hacked"}`)); err != ErrBadPacket {
		t.Fatalf("bad state: %v", err)
	}
}
