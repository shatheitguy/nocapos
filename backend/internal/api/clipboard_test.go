package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"alfaos/alfad/internal/store"
)

type clipsResp struct {
	Items []linkClip `json:"items"`
}

var (
	pngPic  = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 64)...)
	jpegPic = append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte{2}, 64)...)
)

// clipBody sends a raw body (a picture) through h as a device or a person.
func clipBody(s *Server, h http.Handler, target, ctype string, body []byte, token string, u *store.User) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", target, bytes.NewReader(body))
	r.Header.Set("Content-Type", ctype)
	if token != "" {
		r.Header.Set("Authorization", "Device "+token)
	}
	if u != nil {
		r = r.WithContext(context.WithValue(r.Context(), userKey, u))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func clipItems(t *testing.T, w *spacesWorld, who string) []linkClip {
	t.Helper()
	var got clipsResp
	rec := webCall(w, who, w.s.clipboardList, "GET", "/api/v1/clipboard", "", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	return got.Items
}

func TestClipboardSync(t *testing.T) {
	w := deviceWorld(t)
	tok := linkDevice(t, w.s, w.users["alex"])
	linkDevice(t, w.s, w.users["sam"])
	samDev := deviceID(t, w.s, w.users["sam"])

	rec := asDevice(w.s, w.s.deviceClipboard, "POST", "/api/v1/device/clipboard", tok, `{"text":"from phone"}`)
	var res struct {
		OK bool  `json:"ok"`
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || !res.OK || res.ID == 0 {
		t.Fatalf("phone clip: %d %s", rec.Code, rec.Body)
	}
	if c := clipItems(t, w, "alex"); len(c) != 1 || c[0].Text != "from phone" || c[0].Source != "phone" || c[0].Kind != "text" || c[0].From != "Pixel 9" || c[0].DeviceID == "" || c[0].At.IsZero() {
		t.Fatalf("phone clip: %+v", c)
	}
	// Size limits: text up to 1 MB.
	for body, code := range map[string]int{
		`{"text":""}`: http.StatusBadRequest,
		`{"text":"` + strings.Repeat("x", linkMaxClip+1) + `"}`: http.StatusRequestEntityTooLarge,
		`{"text":"x","device_id":"d"}`:                          http.StatusBadRequest, // a phone can't target
		`{"text":"` + strings.Repeat("\\u00e9", 200_000) + `"}`: http.StatusOK,         // escapes are fine
	} {
		if rec := asDevice(w.s, w.s.deviceClipboard, "POST", "/api/v1/device/clipboard", tok, body); rec.Code != code {
			t.Fatalf("%.40s: %d %s", body, rec.Code, rec.Body)
		}
	}
	if rec := asDevice(w.s, w.s.deviceClipboard, "POST", "/api/v1/device/clipboard", tok, `{"text":"`+strings.Repeat("x", linkMaxClip)+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("1 MB: %d", rec.Code)
	}

	if rec := webCall(w, "alex", w.s.clipboardSend, "POST", "/api/v1/clipboard", `{"text":"from web"}`, nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("web clip: %d %s", rec.Code, rec.Body)
	}
	// Someone else's phone can't be targeted.
	if rec := webCall(w, "alex", w.s.clipboardSend, "POST", "/api/v1/clipboard", `{"text":"x","device_id":"`+samDev+`"}`, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("other's device: %d", rec.Code)
	}
	// Only the last item is kept.
	if c := clipItems(t, w, "alex"); len(c) != 1 || c[0].Text != "from web" || c[0].Source != "web" || c[0].From != "NoCap OS" || c[0].DeviceID != "" || c[0].ID <= res.ID {
		t.Fatalf("newest: %+v", c)
	}
	// Owner only.
	if c := clipItems(t, w, "sam"); len(c) != 0 {
		t.Fatalf("sam sees: %+v", c)
	}
	if rec := webCall(w, "sam", w.s.clipboardSend, "POST", "/api/v1/clipboard", `{"text":"for sam","device_id":"`+samDev+`"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("sam target: %d %s", rec.Code, rec.Body)
	}
	if c := clipItems(t, w, "alex"); len(c) != 1 || c[0].Text != "from web" {
		t.Fatalf("alex after sam: %+v", c)
	}
}

func TestClipboardImages(t *testing.T) {
	w := deviceWorld(t)
	alex, sam := w.users["alex"], w.users["sam"]
	tok := linkDevice(t, w.s, alex)
	dev := s2h(w.s.device(w.s.deviceClipboard))

	if rec := clipBody(w.s, dev, "/api/v1/device/clipboard", "image/png", pngPic, tok, nil); rec.Code != http.StatusOK {
		t.Fatalf("png: %d %s", rec.Code, rec.Body)
	}
	c := clipItems(t, w, "alex")
	if len(c) != 1 || c[0].Kind != "image" || c[0].Mime != "image/png" || c[0].Size != len(pngPic) || c[0].Text != "" {
		t.Fatalf("png clip: %+v", c)
	}
	id := itoa(c[0].ID)
	// The owner gets the picture; nobody else does.
	rec := webCall(w, "alex", w.s.clipboardData, "GET", "/", "", map[string]string{"id": id})
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), pngPic) || rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("data: %d %q", rec.Code, rec.Header())
	}
	if rec := webCall(w, "sam", w.s.clipboardData, "GET", "/", "", map[string]string{"id": id}); rec.Code != http.StatusNotFound {
		t.Fatalf("sam data: %d", rec.Code)
	}
	if rec := devPath(w.s, w.s.deviceClipboardData, "GET", "/", tok, map[string]string{"id": id}); rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), pngPic) {
		t.Fatalf("device data: %d", rec.Code)
	}
	samTok := linkDevice(t, w.s, sam)
	if rec := devPath(w.s, w.s.deviceClipboardData, "GET", "/", samTok, map[string]string{"id": id}); rec.Code != http.StatusNotFound {
		t.Fatalf("sam's phone: %d", rec.Code)
	}

	// Only real PNG/JPEG, up to 10 MB.
	web := s2h(http.HandlerFunc(w.s.clipboardSend))
	if rec := clipBody(w.s, web, "/api/v1/clipboard", "image/jpeg", jpegPic, "", alex); rec.Code != http.StatusOK {
		t.Fatalf("jpeg: %d %s", rec.Code, rec.Body)
	}
	if rec := clipBody(w.s, web, "/api/v1/clipboard", "image/png", jpegPic, "", alex); rec.Code != http.StatusBadRequest {
		t.Fatalf("mislabelled: %d", rec.Code)
	}
	if rec := clipBody(w.s, web, "/api/v1/clipboard", "image/gif", []byte("GIF89a"), "", alex); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("gif: %d", rec.Code)
	}
	big := append(append([]byte{}, pngPic...), make([]byte, linkMaxClipImage)...)
	if rec := clipBody(w.s, web, "/api/v1/clipboard", "image/png", big, "", alex); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too big: %d", rec.Code)
	}
	ok := append(append([]byte{}, pngPic...), make([]byte, linkMaxClipImage-len(pngPic))...)
	if rec := clipBody(w.s, web, "/api/v1/clipboard", "image/png", ok, "", alex); rec.Code != http.StatusOK {
		t.Fatalf("10 MB: %d", rec.Code)
	}

	// Phones that don't say they take pictures don't get them.
	start, _ := pollEvents(t, w.s, samTok, 0, "")
	clipBody(w.s, web, "/api/v1/clipboard", "image/png", pngPic, "", sam)
	if r, _ := pollEvents(t, w.s, samTok, start.Cursor, "0"); len(r.Clipboard) != 0 {
		t.Fatalf("old phone got a picture: %+v", r.Clipboard)
	}
	rec = asDevice(w.s, w.s.deviceEvents, "GET", "/api/v1/device/events?images=1&wait=0&after="+itoa(start.Cursor), samTok, "")
	if !strings.Contains(rec.Body.String(), `"kind":"image"`) {
		t.Fatalf("images=1: %s", rec.Body)
	}
}

func s2h(h http.Handler) http.Handler { return h }

func TestClipboardHistoryAndOtherPhones(t *testing.T) {
	w := deviceWorld(t)
	alex := w.users["alex"]
	tok1 := linkDevice(t, w.s, alex)
	tok2 := linkDevice(t, w.s, alex)

	// Settings: the defaults, then a short history.
	rec := webCall(w, "alex", w.s.clipboardSettingsGet, "GET", "/", "", nil)
	if strings.TrimSpace(rec.Body.String()) != `{"history":false,"desktop":true}` {
		t.Fatalf("defaults: %s", rec.Body)
	}
	if rec := webCall(w, "alex", w.s.clipboardSettingsPut, "PUT", "/", `{"history":true}`, nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"history":true,"desktop":true`) {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	if rec := webCall(w, "sam", w.s.clipboardSettingsGet, "GET", "/", "", nil); !strings.Contains(rec.Body.String(), `"history":false`) {
		t.Fatalf("sam's own: %s", rec.Body)
	}
	start2, _ := pollEvents(t, w.s, tok2, 0, "")
	for i := 0; i < 15; i++ {
		asDevice(w.s, w.s.deviceClipboard, "POST", "/api/v1/device/clipboard", tok1, `{"text":"n`+itoa(int64(i))+`"}`)
	}
	if c := clipItems(t, w, "alex"); len(c) != linkClipHistory || c[0].Text != "n14" {
		t.Fatalf("history: %d", len(c))
	}
	// The other phone gets what the first one copied; the first one doesn't get its own back.
	if r, _ := pollEvents(t, w.s, tok2, start2.Cursor, "0"); len(r.Clipboard) == 0 || r.Clipboard[len(r.Clipboard)-1].Text != "n14" {
		t.Fatalf("other phone: %+v", r.Clipboard)
	}
	start1, _ := pollEvents(t, w.s, tok1, 0, "")
	asDevice(w.s, w.s.deviceClipboard, "POST", "/api/v1/device/clipboard", tok1, `{"text":"mine"}`)
	if r, _ := pollEvents(t, w.s, tok1, start1.Cursor, "0"); len(r.Clipboard) != 0 {
		t.Fatalf("own clip came back: %+v", r.Clipboard)
	}
	// History off: back to the last item only.
	webCall(w, "alex", w.s.clipboardSettingsPut, "PUT", "/", `{"history":false}`, nil)
	if c := clipItems(t, w, "alex"); len(c) != 1 || c[0].Text != "mine" {
		t.Fatalf("after history off: %+v", c)
	}
	webCall(w, "alex", w.s.clipboardClear, "DELETE", "/", "", nil)
	if c := clipItems(t, w, "alex"); len(c) != 0 {
		t.Fatalf("after clear: %+v", c)
	}
	// A day at most.
	w.s.link().addClip(alex.ID, linkClip{Text: "x", Source: "web"}, 1)
	w.s.link().now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	if c := clipItems(t, w, "alex"); len(c) != 0 {
		t.Fatalf("after a day: %+v", c)
	}
}

// agentCall runs the desktop agent's handler as the Linux account peer.
func agentCall(s *Server, peer, method, target, ctype string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, bytes.NewReader(body))
	if ctype != "" {
		r.Header.Set("Content-Type", ctype)
	}
	if peer != "" {
		r = r.WithContext(WithClipPeer(r.Context(), peer))
	}
	rec := httptest.NewRecorder()
	s.ClipAgentHandler().ServeHTTP(rec, r)
	return rec
}

func TestClipAgent(t *testing.T) {
	w := deviceWorld(t)
	alex := w.users["alex"]
	tok := linkDevice(t, w.s, alex)

	// Only NoCapOS accounts, each only their own.
	if rec := agentCall(w.s, "", "GET", "/v1/events", "", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("no peer: %d", rec.Code)
	}
	if rec := agentCall(w.s, "gdm", "GET", "/v1/events", "", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("not an account: %d", rec.Code)
	}
	var ev clipAgentEvents
	rec := agentCall(w.s, "alex", "GET", "/v1/events?after=0", "", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &ev); err != nil || ev.Cursor == 0 || !ev.Enabled || ev.Clip != nil {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}

	// Copy on the phone → the desktop gets it.
	asDevice(w.s, w.s.deviceClipboard, "POST", "/api/v1/device/clipboard", tok, `{"text":"to desktop"}`)
	rec = agentCall(w.s, "alex", "GET", "/v1/events?wait=0&after="+itoa(ev.Cursor), "", nil)
	var got clipAgentEvents
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Clip == nil || got.Clip.Text != "to desktop" {
		t.Fatalf("phone → desktop: %s", rec.Body)
	}
	// Sam's desktop session hears nothing of it.
	rec = agentCall(w.s, "sam", "GET", "/v1/events?wait=0&after="+itoa(ev.Cursor), "", nil)
	if strings.Contains(rec.Body.String(), "to desktop") {
		t.Fatalf("sam got: %s", rec.Body)
	}

	// Copy on the desktop → the phone gets it; the desktop doesn't get its own back.
	start, _ := pollEvents(t, w.s, tok, 0, "")
	if rec := agentCall(w.s, "alex", "POST", "/v1/clip", "application/json", []byte(`{"text":"from desktop"}`)); rec.Code != http.StatusOK {
		t.Fatalf("desktop post: %d %s", rec.Code, rec.Body)
	}
	if r, _ := pollEvents(t, w.s, tok, start.Cursor, "0"); len(r.Clipboard) != 1 || r.Clipboard[0].Source != "desktop" || r.Clipboard[0].From != "NoCap Desktop" {
		t.Fatalf("desktop → phone: %+v", r.Clipboard)
	}
	rec = agentCall(w.s, "alex", "GET", "/v1/events?wait=0&after="+itoa(got.Cursor), "", nil)
	if strings.Contains(rec.Body.String(), `"clip"`) {
		t.Fatalf("own clip back: %s", rec.Body)
	}
	// Pictures, with the same limits.
	if rec := agentCall(w.s, "alex", "POST", "/v1/clip", "image/png", pngPic); rec.Code != http.StatusOK {
		t.Fatalf("desktop png: %d", rec.Code)
	}
	if rec := agentCall(w.s, "alex", "POST", "/v1/clip", "application/json", []byte(`{"text":"`+strings.Repeat("x", linkMaxClip+1)+`"}`)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("desktop too big: %d", rec.Code)
	}
	id := clipItems(t, w, "alex")[0].ID
	if rec := agentCall(w.s, "sam", "GET", "/v1/clip/"+itoa(id)+"/data", "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("sam's agent read alex's picture: %d", rec.Code)
	}
	if rec := agentCall(w.s, "alex", "GET", "/v1/clip/"+itoa(id)+"/data", "", nil); rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), pngPic) {
		t.Fatalf("alex's picture: %d", rec.Code)
	}

	// Switched off: nothing in, nothing out.
	webCall(w, "alex", w.s.clipboardSettingsPut, "PUT", "/", `{"desktop":false}`, nil)
	if rec := agentCall(w.s, "alex", "POST", "/v1/clip", "application/json", []byte(`{"text":"x"}`)); rec.Code != http.StatusForbidden {
		t.Fatalf("off post: %d", rec.Code)
	}
	cur := w.s.link().seq
	asDevice(w.s, w.s.deviceClipboard, "POST", "/api/v1/device/clipboard", tok, `{"text":"not for desktop"}`)
	rec = agentCall(w.s, "alex", "GET", "/v1/events?wait=0&after="+itoa(cur), "", nil)
	if strings.Contains(rec.Body.String(), "not for desktop") || !strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Fatalf("off events: %s", rec.Body)
	}
	// A disabled account is turned away.
	if err := w.s.Store.SetDisabled(context.Background(), alex.ID, true); err != nil {
		t.Fatal(err)
	} else {
		if rec := agentCall(w.s, "alex", "GET", "/v1/events?wait=0", "", nil); rec.Code != http.StatusForbidden {
			t.Fatalf("disabled: %d", rec.Code)
		}
	}
}
