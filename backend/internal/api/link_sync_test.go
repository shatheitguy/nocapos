package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"alfaos/alfad/internal/hardware"
	"alfaos/alfad/internal/notify"
	"alfaos/alfad/internal/store"
)

// deviceID is the id of u's only linked phone.
func deviceID(t *testing.T, s *Server, u *store.User) string {
	t.Helper()
	list, err := s.Store.ListDevices(context.Background(), u.ID)
	if err != nil || len(list) == 0 {
		t.Fatalf("devices: %v %d", err, len(list))
	}
	return list[0].ID
}

// webCall runs a web (session) handler as who, with path values.
func webCall(w *spacesWorld, who string, h http.HandlerFunc, method, target, body string, path map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range path {
		r.SetPathValue(k, v)
	}
	r = r.WithContext(context.WithValue(r.Context(), userKey, w.users[who]))
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

func devPath(s *Server, h http.HandlerFunc, method, target, token string, path map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	for k, v := range path {
		r.SetPathValue(k, v)
	}
	return devDo(s, h, r, token)
}

type phoneNotesResp struct {
	Items []phoneNote `json:"items"`
}

func TestPhoneNotifications(t *testing.T) {
	w := deviceWorld(t)
	tok := linkDevice(t, w.s, w.users["alex"])
	dev := deviceID(t, w.s, w.users["alex"])

	if rec := asDevice(w.s, w.s.devicePhoneNote, "POST", "/api/v1/device/phone-notifications", "", `{"key":"a"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	post := func(body string) *httptest.ResponseRecorder {
		return asDevice(w.s, w.s.devicePhoneNote, "POST", "/api/v1/device/phone-notifications", tok, body)
	}
	if rec := post(`{"key":"","title":"x"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("no key: %d", rec.Code)
	}
	if rec := post(`{"key":"` + strings.Repeat("k", 201) + `"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("long key: %d", rec.Code)
	}
	if rec := post(`{"key":"0|chat|1","app":"Chat","package":"com.chat","title":"Hi","text":"first","icon":"aGk=","posted":1000}`); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		t.Fatalf("post: %d %s", rec.Code, rec.Body)
	}
	// Upsert by key; a too-big icon is dropped, long text is cut.
	big := strings.Repeat("A", 70000)
	if rec := post(`{"key":"0|chat|1","app":"Chat","title":"Hi","text":"` + strings.Repeat("x", 2500) + `","icon":"` + big + `","posted":3000}`); rec.Code != http.StatusOK {
		t.Fatalf("upsert: %d %s", rec.Code, rec.Body)
	}
	if rec := post(`{"key":"mail","app":"Mail","title":"New","text":"t","posted":2000}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}

	var got phoneNotesResp
	rec := webCall(w, "alex", w.s.phoneNotesList, "GET", "/api/v1/phone-notifications", "", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Items) != 2 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	first := got.Items[0]
	if first.Key != "0|chat|1" || first.DeviceID != dev || first.DeviceName != "Pixel 9" || first.Icon != "" || len(first.Text) != linkMaxText || got.Items[1].Key != "mail" {
		t.Fatalf("items: %+v", got.Items)
	}
	// Only the owner sees them.
	rec = webCall(w, "sam", w.s.phoneNotesList, "GET", "/api/v1/phone-notifications", "", nil)
	if strings.TrimSpace(rec.Body.String()) != `{"items":[]}` {
		t.Fatalf("sam: %s", rec.Body)
	}
	// Sam can't dismiss Alex's.
	webCall(w, "sam", w.s.phoneNotesDismiss, "DELETE", "/", "", map[string]string{"device_id": dev, "key": "mail"})
	// The phone dismissed one there.
	if rec := devPath(w.s, w.s.devicePhoneNoteGone, "DELETE", "/", tok, map[string]string{"key": "0|chat|1"}); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		t.Fatalf("phone dismiss: %d %s", rec.Code, rec.Body)
	}
	if rec := devPath(w.s, w.s.devicePhoneNoteGone, "DELETE", "/", tok, map[string]string{"key": "missing"}); rec.Code != http.StatusOK {
		t.Fatalf("missing: %d", rec.Code)
	}
	// Dismissed on the web.
	if rec := webCall(w, "alex", w.s.phoneNotesDismiss, "DELETE", "/", "", map[string]string{"device_id": dev, "key": "mail"}); rec.Code != http.StatusOK {
		t.Fatalf("web dismiss: %d", rec.Code)
	}
	rec = webCall(w, "alex", w.s.phoneNotesList, "GET", "/api/v1/phone-notifications", "", nil)
	if strings.TrimSpace(rec.Body.String()) != `{"items":[]}` {
		t.Fatalf("after dismiss: %s", rec.Body)
	}

	// At most 100 per person; a day at most.
	h := w.s.link()
	for i := 0; i < 120; i++ {
		h.putNote("p", phoneNote{DeviceID: "d", Key: string(rune('a'+i%26)) + strings.Repeat("x", i)})
	}
	if n := len(h.notes("p")); n != linkMaxPhoneNotes {
		t.Fatalf("kept %d", n)
	}
	h.now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	if n := len(h.notes("p")); n != 0 {
		t.Fatalf("after a day: %d", n)
	}
}

type eventsResp struct {
	Cursor    int64                 `json:"cursor"`
	Notices   []notify.Notification `json:"notices"`
	Clipboard []linkClip            `json:"clipboard"`
	Dismissed []string              `json:"dismissed"`
}

func pollEvents(t *testing.T, s *Server, tok string, after int64, wait string) (eventsResp, time.Duration) {
	t.Helper()
	start := time.Now()
	target := "/api/v1/device/events?after=" + itoa(after)
	if wait != "" {
		target += "&wait=" + wait
	}
	rec := asDevice(s, s.deviceEvents, "GET", target, tok, "")
	var out eventsResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("events: %d %s", rec.Code, rec.Body)
	}
	if out.Notices == nil || out.Clipboard == nil || out.Dismissed == nil {
		t.Fatalf("lists must be [] not null: %s", rec.Body)
	}
	return out, time.Since(start)
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestDeviceEvents(t *testing.T) {
	w := deviceWorld(t)
	alex := w.users["alex"]
	tok := linkDevice(t, w.s, alex)
	dev := deviceID(t, w.s, alex)
	samTok := linkDevice(t, w.s, w.users["sam"])
	admTok := linkDevice(t, w.s, w.users["admin"])

	if rec := asDevice(w.s, w.s.deviceEvents, "GET", "/api/v1/device/events", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	// Something before the phone starts following: no history dump.
	w.s.link().addClip(alex.ID, linkClip{Text: "old", Source: "web"}, linkClipHistory)
	start, took := pollEvents(t, w.s, tok, 0, "")
	if start.Cursor == 0 || len(start.Clipboard)+len(start.Notices)+len(start.Dismissed) != 0 || took > 2*time.Second {
		t.Fatalf("after=0: %+v in %v", start, took)
	}
	// Nothing new: wait=1 times out empty.
	got, took := pollEvents(t, w.s, tok, start.Cursor, "1")
	if len(got.Clipboard)+len(got.Notices)+len(got.Dismissed) != 0 || took < 900*time.Millisecond || got.Cursor < start.Cursor {
		t.Fatalf("timeout: %+v in %v", got, took)
	}

	// A clip from the web wakes a waiting poll.
	done := make(chan eventsResp, 1)
	go func() {
		rec := asDevice(w.s, w.s.deviceEvents, "GET", "/api/v1/device/events?after="+itoa(start.Cursor)+"&wait=10", tok, "")
		var r eventsResp
		_ = json.Unmarshal(rec.Body.Bytes(), &r)
		done <- r
	}()
	time.Sleep(150 * time.Millisecond)
	if rec := webCall(w, "alex", w.s.clipboardSend, "POST", "/api/v1/clipboard", `{"text":"wake up"}`, nil); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	select {
	case r := <-done:
		if len(r.Clipboard) != 1 || r.Clipboard[0].Text != "wake up" || r.Cursor <= start.Cursor {
			t.Fatalf("woken: %+v", r)
		}
		start.Cursor = r.Cursor
	case <-time.After(5 * time.Second):
		t.Fatal("poll wasn't woken")
	}

	// Clips from phones and for other phones aren't delivered; dismissals are.
	asDevice(w.s, w.s.deviceClipboard, "POST", "/api/v1/device/clipboard", tok, `{"text":"mine"}`)
	w.s.link().addClip(alex.ID, linkClip{Text: "elsewhere", Source: "web", DeviceID: "another"}, linkClipHistory)
	asDevice(w.s, w.s.devicePhoneNote, "POST", "/api/v1/device/phone-notifications", tok, `{"key":"k1","app":"Chat","title":"t"}`)
	webCall(w, "alex", w.s.phoneNotesDismiss, "DELETE", "/", "", map[string]string{"device_id": dev, "key": "k1"})
	w.s.link().addClip(alex.ID, linkClip{Text: "for this phone", Source: "web", DeviceID: dev}, linkClipHistory)
	got, _ = pollEvents(t, w.s, tok, start.Cursor, "0")
	if len(got.Dismissed) != 1 || got.Dismissed[0] != "k1" || len(got.Clipboard) != 1 || got.Clipboard[0].Text != "for this phone" {
		t.Fatalf("events: %+v", got)
	}
	// Sam's phone hears nothing of Alex's.
	samStart, _ := pollEvents(t, w.s, samTok, 0, "")
	w.s.link().addClip(alex.ID, linkClip{Text: "alex only", Source: "web"}, linkClipHistory)
	if r, _ := pollEvents(t, w.s, samTok, samStart.Cursor, "0"); len(r.Clipboard) != 0 {
		t.Fatalf("sam got: %+v", r)
	}

	// Server notifications: administrators get all; everyone gets new versions.
	w.s.notify = notify.New(w.s.Store, w.s.Log)
	w.s.notify.OnAdd(w.s.link().noticeAdded)
	admStart, _ := pollEvents(t, w.s, admTok, 0, "")
	alexStart, _ := pollEvents(t, w.s, tok, 0, "")
	ctx := context.Background()
	if _, err := w.s.notify.Add(ctx, notify.Notification{Key: "storage:x", Kind: notify.KindStorage, Title: "Disk"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.s.notify.Add(ctx, releaseNotification(updateInfo{Latest: "v9.9.9", Current: "v1.0.0"})); err != nil {
		t.Fatal(err)
	}
	if r, _ := pollEvents(t, w.s, admTok, admStart.Cursor, "0"); len(r.Notices) != 2 {
		t.Fatalf("admin notices: %+v", r.Notices)
	}
	if r, _ := pollEvents(t, w.s, tok, alexStart.Cursor, "0"); len(r.Notices) != 1 || r.Notices[0].Key != "update:v9.9.9" {
		t.Fatalf("alex notices: %+v", r.Notices)
	}
	rec := asDevice(w.s, w.s.deviceNotifications, "GET", "/api/v1/device/notifications", tok, "")
	if !strings.Contains(rec.Body.String(), `"admin":false`) || !strings.Contains(rec.Body.String(), "update:v9.9.9") || strings.Contains(rec.Body.String(), "storage:x") {
		t.Fatalf("alex notifications: %s", rec.Body)
	}
	rec = asDevice(w.s, w.s.deviceNotifications, "GET", "/api/v1/device/notifications", admTok, "")
	if !strings.Contains(rec.Body.String(), "storage:x") || !strings.Contains(rec.Body.String(), "update:v9.9.9") {
		t.Fatalf("admin notifications: %s", rec.Body)
	}

	// A cursor from before a restart (ahead of this run) starts over at once.
	if r, took := pollEvents(t, w.s, tok, start.Cursor+1_000_000_000, "10"); took > 2*time.Second || r.Cursor > start.Cursor+1_000_000_000 {
		t.Fatalf("ahead: %+v in %v", r, took)
	}
}

func TestDeviceSummaryNetwork(t *testing.T) {
	w := deviceWorld(t)
	tok := linkDevice(t, w.s, w.users["alex"])
	rec := asDevice(w.s, w.s.deviceSummary, "GET", "/api/v1/device/summary", tok, "")
	for _, k := range []string{`"network":{"rx_rate":0,"tx_rate":0}`, `"updates":`} {
		if !strings.Contains(rec.Body.String(), k) {
			t.Fatalf("summary missing %s: %s", k, rec.Body)
		}
	}
	got := netTotals([]hardware.NetStat{{Interface: "lo", RxRate: 100, TxRate: 100}, {Interface: "eth0", RxRate: 10, TxRate: 2}, {Interface: "wlan0", RxRate: 5, TxRate: 1}})
	if got.RxRate != 15 || got.TxRate != 3 {
		t.Fatalf("totals: %+v", got)
	}
}
