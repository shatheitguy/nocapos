package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type phonesResp struct {
	Phones  []phoneJSON `json:"phones"`
	Carries []linkCarry `json:"carries"`
}

type eventsWithActions struct {
	Cursor  int64         `json:"cursor"`
	Actions []phoneAction `json:"actions"`
}

func pollActions(t *testing.T, s *Server, tok string, after int64) eventsWithActions {
	t.Helper()
	rec := asDevice(s, s.deviceEvents, "GET", "/api/v1/device/events?after="+itoa(after)+"&wait=0", tok, "")
	var out eventsWithActions
	if err := json.Unmarshal(rec.Body.Bytes(), &out); rec.Code != http.StatusOK || err != nil || out.Actions == nil {
		t.Fatalf("events: %d %s", rec.Code, rec.Body)
	}
	return out
}

// The device token alone is enough for the stream: no web session, and only
// that phone's own person's data.
func TestDeviceTokenStream(t *testing.T) {
	w := deviceWorld(t)
	tok := linkDevice(t, w.s, w.users["alex"])
	for _, bad := range []string{"", "nope"} {
		if rec := asDevice(w.s, w.s.deviceEvents, "GET", "/api/v1/device/events", bad, ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: %d", bad, rec.Code)
		}
	}
	// A web session cookie isn't a device token.
	r := webCall(w, "alex", func(rw http.ResponseWriter, r *http.Request) { w.s.device(w.s.deviceEvents).ServeHTTP(rw, r) }, "GET", "/api/v1/device/events", "", nil)
	if r.Code != http.StatusUnauthorized {
		t.Fatalf("web session on device route: %d", r.Code)
	}
	if rec := asDevice(w.s, w.s.deviceEvents, "GET", "/api/v1/device/events?wait=0", tok, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"actions":[]`) {
		t.Fatalf("token only: %d %s", rec.Code, rec.Body)
	}
	// Unlinked: the stream stops at once.
	dev := deviceID(t, w.s, w.users["alex"])
	if rec := devPath(w.s, w.s.deviceUnlink, "DELETE", "/", tok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("unlink: %d", rec.Code)
	}
	if rec := asDevice(w.s, w.s.deviceEvents, "GET", "/api/v1/device/events", tok, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("after unlink: %d", rec.Code)
	}
	_ = dev
}

func TestPhoneStateAndPresence(t *testing.T) {
	w := deviceWorld(t)
	tok := linkDevice(t, w.s, w.users["alex"])
	dev := deviceID(t, w.s, w.users["alex"])
	linkDevice(t, w.s, w.users["sam"])

	ch, stop := w.s.linkFeed.subscribe(w.users["alex"].ID)
	defer stop()

	for _, bad := range []string{`{"battery":101}`, `{"signal":5}`, `{"network":"carrier pigeon"}`, `{"route":"moon"}`} {
		if rec := asDevice(w.s, w.s.deviceState, "POST", "/api/v1/device/state", tok, bad); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", bad, rec.Code)
		}
	}
	if rec := asDevice(w.s, w.s.deviceState, "POST", "/api/v1/device/state", tok, `{"battery":64,"charging":true,"network":"wifi","signal":3,"route":"local"}`); rec.Code != http.StatusOK {
		t.Fatalf("state: %d %s", rec.Code, rec.Body)
	}
	select {
	case e := <-ch:
		if e.Type != "changed" {
			t.Fatalf("event: %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no live event for the state")
	}

	list := func(who string) phonesResp {
		var out phonesResp
		rec := webCall(w, who, w.s.phonesList, "GET", "/api/v1/phones", "", nil)
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("phones: %d %s", rec.Code, rec.Body)
		}
		return out
	}
	got := list("alex")
	if len(got.Phones) != 1 {
		t.Fatalf("alex phones: %+v", got.Phones)
	}
	p := got.Phones[0]
	if p.ID != dev || p.Online || p.Route != "" || p.State == nil || p.State.Battery != 64 || !p.State.Charging || p.State.Signal != 3 || p.State.Route != "local" {
		t.Fatalf("offline phone: %+v %+v", p, p.State)
	}
	// Following the long-poll = online, nearby as it said.
	done := make(chan struct{})
	go func() {
		asDevice(w.s, w.s.deviceEvents, "GET", "/api/v1/device/events?after=1&wait=2", tok, "")
		close(done)
	}()
	time.Sleep(200 * time.Millisecond)
	if p := list("alex").Phones[0]; !p.Online || p.Route != "local" {
		t.Fatalf("online: %+v", p)
	}
	<-done
	if p := list("alex").Phones[0]; !p.Online {
		t.Fatalf("between polls it's still online: %+v", p)
	}
	// Later, it's offline.
	w.s.link().now = func() time.Time { return time.Now().Add(time.Minute) }
	if p := list("alex").Phones[0]; p.Online || p.Route != "" {
		t.Fatalf("gone quiet: %+v", p)
	}
	w.s.link().now = time.Now
	// Sam sees only his own.
	if s := list("sam"); len(s.Phones) != 1 || s.Phones[0].ID == dev || s.Phones[0].State != nil {
		t.Fatalf("sam: %+v", s.Phones)
	}
}

func TestPhoneActions(t *testing.T) {
	w := deviceWorld(t)
	tok := linkDevice(t, w.s, w.users["alex"])
	dev := deviceID(t, w.s, w.users["alex"])
	samTok := linkDevice(t, w.s, w.users["sam"])
	samDev := deviceID(t, w.s, w.users["sam"])
	start := pollActions(t, w.s, tok, 0)
	samStart := pollActions(t, w.s, samTok, 0)

	act := func(who, device, body string) int {
		return webCall(w, who, w.s.phoneActionSend, "POST", "/", body, map[string]string{"device_id": device}).Code
	}
	if c := act("alex", samDev, `{"kind":"ring"}`); c != http.StatusNotFound {
		t.Fatalf("someone else's phone: %d", c)
	}
	for _, bad := range []string{`{"kind":"selfdestruct"}`, `{"kind":"open","url":"javascript:alert(1)"}`, `{"kind":"open","url":"file:///etc/passwd"}`, `{"kind":"file"}`} {
		if c := act("alex", dev, bad); c != http.StatusBadRequest {
			t.Fatalf("%s: %d", bad, c)
		}
	}
	// Reply and decline only for notifications that allow them.
	if c := act("alex", dev, `{"kind":"reply","key":"m1","text":"hi"}`); c != http.StatusConflict {
		t.Fatalf("reply to nothing: %d", c)
	}
	asDevice(w.s, w.s.devicePhoneNote, "POST", "/api/v1/device/phone-notifications", tok, `{"key":"m1","app":"Messages","title":"Sam","text":"Lunch?","kind":"message","sender":"Sam","can_reply":true}`)
	asDevice(w.s, w.s.devicePhoneNote, "POST", "/api/v1/device/phone-notifications", tok, `{"key":"c1","app":"Phone","title":"Mum","kind":"call","call_state":"incoming","number":"+971500000000","can_decline":true}`)
	asDevice(w.s, w.s.devicePhoneNote, "POST", "/api/v1/device/phone-notifications", tok, `{"key":"c2","app":"Phone","title":"Unknown","kind":"call","call_state":"missed","can_decline":true}`)
	if rec := asDevice(w.s, w.s.devicePhoneNote, "POST", "/api/v1/device/phone-notifications", tok, `{"key":"c3","kind":"call","call_state":"maybe"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad call state: %d", rec.Code)
	}
	if c := act("alex", dev, `{"kind":"reply","key":"m1","text":"  "}`); c != http.StatusBadRequest {
		t.Fatalf("empty reply: %d", c)
	}
	if c := act("alex", dev, `{"kind":"decline","key":"m1"}`); c != http.StatusConflict {
		t.Fatalf("decline a message: %d", c)
	}
	if c := act("alex", dev, `{"kind":"decline","key":"c2"}`); c != http.StatusConflict {
		t.Fatalf("decline a missed call: %d", c)
	}
	for _, ok := range []string{`{"kind":"reply","key":"m1","text":"Sure"}`, `{"kind":"decline","key":"c1"}`, `{"kind":"ring"}`, `{"kind":"open","url":"https://example.org/a?b=c"}`} {
		if c := act("alex", dev, ok); c != http.StatusOK {
			t.Fatalf("%s: %d", ok, c)
		}
	}
	got := pollActions(t, w.s, tok, start.Cursor)
	if len(got.Actions) != 4 || got.Actions[0].Kind != "reply" || got.Actions[0].Text != "Sure" || got.Actions[1].Key != "c1" || got.Actions[3].URL != "https://example.org/a?b=c" {
		t.Fatalf("actions: %+v", got.Actions)
	}
	if again := pollActions(t, w.s, tok, got.Cursor); len(again.Actions) != 0 {
		t.Fatalf("delivered twice: %+v", again.Actions)
	}
	if s := pollActions(t, w.s, samTok, samStart.Cursor); len(s.Actions) != 0 {
		t.Fatalf("sam's phone got alex's: %+v", s.Actions)
	}
	// Grouping data: calls and messages keep their details.
	var notes phoneNotesResp
	rec := webCall(w, "alex", w.s.phoneNotesList, "GET", "/api/v1/phone-notifications", "", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &notes); err != nil {
		t.Fatal(err)
	}
	byKey := map[string]phoneNote{}
	for _, n := range notes.Items {
		byKey[n.Key] = n
	}
	if n := byKey["c1"]; n.Kind != "call" || n.CallState != "incoming" || n.Number != "+971500000000" || !n.CanDecline {
		t.Fatalf("call: %+v", n)
	}
	if n := byKey["c2"]; n.CanDecline {
		t.Fatalf("missed call can't be declined: %+v", n)
	}
	if n := byKey["m1"]; n.Kind != "message" || n.Sender != "Sam" || !n.CanReply || n.Number != "" {
		t.Fatalf("message: %+v", n)
	}
	// Stale actions are dropped.
	w.s.link().now = func() time.Time { return time.Now().Add(time.Hour) }
	if late := pollActions(t, w.s, tok, start.Cursor); len(late.Actions) != 0 {
		t.Fatalf("stale actions: %+v", late.Actions)
	}
}

// Dismissing on either side reaches the other, live.
func TestPhoneDismissSync(t *testing.T) {
	w := deviceWorld(t)
	tok := linkDevice(t, w.s, w.users["alex"])
	dev := deviceID(t, w.s, w.users["alex"])
	ch, stop := w.s.linkFeed.subscribe(w.users["alex"].ID)
	defer stop()
	wait := func(what string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatalf("no live event: %s", what)
		}
	}
	start := pollActions(t, w.s, tok, 0)
	asDevice(w.s, w.s.devicePhoneNote, "POST", "/api/v1/device/phone-notifications", tok, `{"key":"a","title":"x"}`)
	wait("posted")
	asDevice(w.s, w.s.devicePhoneNote, "POST", "/api/v1/device/phone-notifications", tok, `{"key":"b","title":"y"}`)
	wait("posted b")
	// Phone → web.
	devPath(w.s, w.s.devicePhoneNoteGone, "DELETE", "/", tok, map[string]string{"key": "a"})
	wait("phone dismissed")
	// Web → phone.
	webCall(w, "alex", w.s.phoneNotesDismiss, "DELETE", "/", "", map[string]string{"device_id": dev, "key": "b"})
	wait("web dismissed")
	got, _ := pollEvents(t, w.s, tok, start.Cursor, "0")
	if len(got.Dismissed) != 1 || got.Dismissed[0] != "b" {
		t.Fatalf("phone hears: %+v", got.Dismissed)
	}
	if n := w.s.link().notes(w.users["alex"].ID); len(n) != 0 {
		t.Fatalf("left: %+v", n)
	}
}

func TestOpenOnNoCap(t *testing.T) {
	w := deviceWorld(t)
	tok := linkDevice(t, w.s, w.users["alex"])
	linkDevice(t, w.s, w.users["sam"])
	ch, stop := w.s.linkFeed.subscribe(w.users["alex"].ID)
	defer stop()
	if rec := asDevice(w.s, w.s.deviceOpen, "POST", "/api/v1/device/open", tok, `{"url":"intent://x"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad url: %d", rec.Code)
	}
	if rec := asDevice(w.s, w.s.deviceOpen, "POST", "/api/v1/device/open", tok, `{"url":"https://example.org/page","title":"A page"}`); rec.Code != http.StatusOK {
		t.Fatalf("open: %d %s", rec.Code, rec.Body)
	}
	select {
	case e := <-ch:
		if e.Type != "open" || e.Carry == nil || e.Carry.URL != "https://example.org/page" || e.Carry.DeviceName != "Pixel 9" {
			t.Fatalf("event: %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no open event")
	}
	var out phonesResp
	rec := webCall(w, "alex", w.s.phonesList, "GET", "/api/v1/phones", "", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Carries) != 1 || out.Carries[0].Title != "A page" {
		t.Fatalf("carries: %s", rec.Body)
	}
	rec = webCall(w, "sam", w.s.phonesList, "GET", "/api/v1/phones", "", nil)
	if strings.Contains(rec.Body.String(), "example.org") {
		t.Fatalf("sam sees alex's link: %s", rec.Body)
	}
}

func TestLinkAddresses(t *testing.T) {
	w := deviceWorld(t)
	tok := linkDevice(t, w.s, w.users["alex"])
	w.s.Config.ListenAddr = ":8443"
	w.s.Config.TLSAuto = true
	oldIfs, oldCont := interfaceAddrs, inContainer
	defer func() { interfaceAddrs, inContainer = oldIfs, oldCont }()
	inContainer = func() bool { return false }
	interfaceAddrs = func() map[string][]netip.Addr {
		return map[string][]netip.Addr{
			"eth0":    {netip.MustParseAddr("192.168.1.20"), netip.MustParseAddr("fe80::1")},
			"docker0": {netip.MustParseAddr("172.17.0.1")},
			"wlan0":   {netip.MustParseAddr("8.8.8.8")},
		}
	}
	put := func(body string) int {
		r := httptest.NewRequest("PUT", "/api/v1/link/addresses", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(context.WithValue(r.Context(), userKey, w.users["admin"]))
		rec := httptest.NewRecorder()
		w.s.linkAddressesPut(rec, r)
		return rec.Code
	}
	if c := put(`{"remote":"ftp://x"}`); c != http.StatusBadRequest {
		t.Fatalf("bad remote: %d", c)
	}
	if c := put(`{"lan":["javascript:x"]}`); c != http.StatusBadRequest {
		t.Fatalf("bad lan: %d", c)
	}
	if c := put(`{"remote":"https://nocap.example.com/","lan":["https://10.0.0.5:8443"]}`); c != http.StatusOK {
		t.Fatalf("save: %d", c)
	}
	rec := asDevice(w.s, w.s.deviceAddresses, "GET", "/api/v1/device/addresses", tok, "")
	var got linkAddresses
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("addresses: %d %s", rec.Code, rec.Body)
	}
	if got.Remote != "https://nocap.example.com" || len(got.SavedLAN) != 0 || got.SavedRemote != "" {
		t.Fatalf("remote: %+v", got)
	}
	want := map[string]bool{"https://10.0.0.5:8443": true, "https://192.168.1.20:8443": true}
	for _, o := range got.LAN {
		if !want[o] {
			t.Fatalf("unexpected LAN address %q in %+v", o, got.LAN)
		}
		delete(want, o)
	}
	if len(want) != 0 {
		t.Fatalf("missing %v in %+v", want, got.LAN)
	}
	if !notHomeIface("tailscale0") || notHomeIface("enp3s0") {
		t.Fatal("notHomeIface")
	}
}
