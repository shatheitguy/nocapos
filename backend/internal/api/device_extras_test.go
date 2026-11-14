package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"alfaos/alfad/internal/ai"
	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/config"
	"alfaos/alfad/internal/devices"
	"alfaos/alfad/internal/spaces"
	"alfaos/alfad/internal/store"
)

// deviceWorld is the spaces world plus sign-in rules and a data directory,
// so the device middleware works.
func deviceWorld(t *testing.T) *spacesWorld {
	t.Helper()
	w := newSpacesWorld(t)
	w.s.Auth = auth.NewService(w.s.Store, auth.NewIssuer([]byte("0123456789abcdef0123456789abcdef"), time.Minute), auth.Options{RefreshTTL: time.Hour, SessionMaxAge: time.Hour})
	w.s.Config.DataDir = t.TempDir()
	return w
}

// linkDevice links a phone for u straight in the store and returns its token.
func linkDevice(t *testing.T, s *Server, u *store.User) string {
	t.Helper()
	token, hash := devices.NewToken()
	now := time.Now()
	d := &store.Device{ID: store.NewID(), UserID: u.ID, Name: "Pixel 9", Platform: "android", Battery: -1, CreatedAt: now, LastSeenAt: now}
	if err := s.Store.CreateDevice(context.Background(), d, hash); err != nil {
		t.Fatal(err)
	}
	return token
}

// devDo runs h through the device middleware for a prepared request.
func devDo(s *Server, h http.HandlerFunc, r *http.Request, token string) *httptest.ResponseRecorder {
	if token != "" {
		r.Header.Set("Authorization", "Device "+token)
	}
	rec := httptest.NewRecorder()
	s.device(h).ServeHTTP(rec, r)
	return rec
}

func TestDeviceSummary(t *testing.T) {
	w := deviceWorld(t)
	tok := linkDevice(t, w.s, w.users["alex"])
	if rec := asDevice(w.s, w.s.deviceSummary, "GET", "/api/v1/device/summary", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	rec := asDevice(w.s, w.s.deviceSummary, "GET", "/api/v1/device/summary", tok, "")
	var got deviceSummaryJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("summary: %d %s", rec.Code, rec.Body)
	}
	if got.Ready || got.Server.NocapID == "" || got.CPU.Cores < 1 || got.Time.IsZero() {
		t.Fatalf("summary: %s", rec.Body)
	}
	for _, k := range []string{`"storage":{"total":0,"used":0,"percent":0}`, `"uptime":0`} {
		if !strings.Contains(rec.Body.String(), k) {
			t.Fatalf("summary missing %s: %s", k, rec.Body)
		}
	}
}

func TestDevicePrefsMapping(t *testing.T) {
	cases := []struct{ in, wall, lingo, accent string }{
		{`{"wallpaper":"custom-abc","lingo":"standard","accent":"#A1b2c3"}`, "custom", "standard", "#A1b2c3"},
		{`{"wallpaper":"custom","lingo":"genz","accent":"red"}`, "custom", "genz", ""},
		{`{"wallpaper":"nebula","lingo":"pirate"}`, "nebula", "", ""},
		{`{"wallpaper":"../etc","accent":"#12345"}`, "", "", ""},
		{``, "", "", ""},
	}
	for _, c := range cases {
		got := devicePrefsFrom(c.in)
		if got["wallpaper"] != c.wall || got["lingo"] != c.lingo || got["accent"] != c.accent {
			t.Errorf("%s → %v", c.in, got)
		}
	}

	w := deviceWorld(t)
	alex := w.users["alex"]
	tok := linkDevice(t, w.s, alex)
	if err := w.s.Store.SetUserPrefs(context.Background(), alex.ID, `{"wallpaper":"custom-x1","lingo":"genz","accent":"#2dd4bf","theme":"dark"}`); err != nil {
		t.Fatal(err)
	}
	rec := asDevice(w.s, w.s.devicePrefs, "GET", "/api/v1/device/prefs", tok, "")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"accent":"#2dd4bf","lingo":"genz","wallpaper":"custom"}` {
		t.Fatalf("prefs: %d %s", rec.Code, rec.Body)
	}
}

func multipartSend(t *testing.T, names map[string]string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("note", "ignored")
	for name, body := range names {
		fw, err := mw.CreateFormFile("file", name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write([]byte(body))
	}
	_ = mw.Close()
	r := httptest.NewRequest("POST", "/api/v1/device/send", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	return r
}

type sendResult struct {
	Root   string   `json:"root"`
	Folder string   `json:"folder"`
	Paths  []string `json:"paths"`
}

func TestDeviceSend(t *testing.T) {
	w := deviceWorld(t)
	alex := w.users["alex"]
	tok := linkDevice(t, w.s, alex)
	home := w.s.Spaces.HomePath("alex")
	if home == "" {
		t.Fatal("no home path")
	}

	// Files land in <home>/Drops/<device>, keeping both on a repeat.
	var paths []string
	for i := 0; i < 2; i++ {
		rec := devDo(w.s, w.s.deviceSend, multipartSend(t, map[string]string{`C:\phone\pic.jpg`: "jpeg"}), tok)
		var res sendResult
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if rec.Code != http.StatusCreated || res.Root != spaces.HomeRootID("alex") || res.Folder != "/Drops/Pixel 9" || len(res.Paths) != 1 {
			t.Fatalf("send file: %d %s", rec.Code, rec.Body)
		}
		paths = append(paths, res.Paths[0])
	}
	if paths[0] != "/Drops/Pixel 9/pic.jpg" || paths[1] == paths[0] || !strings.HasPrefix(paths[1], "/Drops/Pixel 9/pic") {
		t.Fatalf("paths: %v", paths)
	}
	if b, err := os.ReadFile(filepath.Join(home, "Drops", "Pixel 9", "pic.jpg")); err != nil || string(b) != "jpeg" {
		t.Fatalf("saved file: %q %v", b, err)
	}

	// Text becomes a .txt named after the title.
	body := `{"text":"buy milk","title":"Shopping / list"}`
	for i := 0; i < 2; i++ {
		rec := asDevice(w.s, w.s.deviceSend, "POST", "/api/v1/device/send", tok, body)
		var res sendResult
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if rec.Code != http.StatusCreated || len(res.Paths) != 1 || !strings.HasPrefix(res.Paths[0], "/Drops/Pixel 9/Shopping  list ") || !strings.HasSuffix(res.Paths[0], ".txt") {
			t.Fatalf("send text: %d %s", rec.Code, rec.Body)
		}
		b, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(res.Paths[0], "/"))))
		if err != nil || string(b) != "buy milk" {
			t.Fatalf("saved text: %q %v", b, err)
		}
	}
	ents, _ := os.ReadDir(filepath.Join(home, "Drops", "Pixel 9"))
	if len(ents) != 4 {
		t.Fatalf("want 4 files (kept both), got %d", len(ents))
	}

	// No title: named after the device.
	rec := asDevice(w.s, w.s.deviceSend, "POST", "/api/v1/device/send", tok, `{"text":"hi"}`)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), "/Drops/Pixel 9/From Pixel 9 ") {
		t.Fatalf("untitled: %d %s", rec.Code, rec.Body)
	}
	// Validation.
	if rec := asDevice(w.s, w.s.deviceSend, "POST", "/api/v1/device/send", tok, `{"text":""}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty text: %d", rec.Code)
	}
	if rec := devDo(w.s, w.s.deviceSend, multipartSend(t, nil), tok); rec.Code != http.StatusBadRequest {
		t.Fatalf("no files: %d", rec.Code)
	}

	// An administrator's own home works too.
	admTok := linkDevice(t, w.s, w.users["admin"])
	if rec := asDevice(w.s, w.s.deviceSend, "POST", "/api/v1/device/send", admTok, `{"text":"x"}`); rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"root":"`+spaces.HomeRootID("admin")+`"`) {
		t.Fatalf("admin send: %d %s", rec.Code, rec.Body)
	}

	// No per-person spaces: no home.
	w.s.Spaces = nil
	rec = asDevice(w.s, w.s.deviceSend, "POST", "/api/v1/device/send", tok, body)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"no_home"`) {
		t.Fatalf("no spaces: %d %s", rec.Code, rec.Body)
	}
}

func TestDeviceLayout(t *testing.T) {
	w := deviceWorld(t)
	alex := w.users["alex"]
	tok := linkDevice(t, w.s, alex)

	rec := asDevice(w.s, w.s.deviceLayoutGet, "GET", "/api/v1/device/layout", tok, "")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"layout":null}` {
		t.Fatalf("empty layout: %d %s", rec.Code, rec.Body)
	}
	if rec := asDevice(w.s, w.s.deviceLayoutPut, "PUT", "/api/v1/device/layout", tok, `{"layout":{"pages":[["a","b"]],"dock":["c"]}}`); rec.Code != http.StatusNoContent {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	rec = asDevice(w.s, w.s.deviceLayoutGet, "GET", "/api/v1/device/layout", tok, "")
	var got struct {
		Layout    map[string]any `json:"layout"`
		UpdatedAt *time.Time     `json:"updated_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Layout["dock"] == nil || got.UpdatedAt == nil {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(w.s.Config.DataDir, "device-layouts", alex.ID+".json")); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{"layout":[1,2]}`, `{"layout":"x"}`, `{"layout":null}`, `{"layout":{"x":"` + strings.Repeat("a", 300<<10) + `"}}`} {
		if rec := asDevice(w.s, w.s.deviceLayoutPut, "PUT", "/api/v1/device/layout", tok, bad); rec.Code != http.StatusBadRequest {
			t.Fatalf("bad layout %.30s: %d", bad, rec.Code)
		}
	}
	// Another person's phone sees only their own.
	other := linkDevice(t, w.s, w.users["sam"])
	if rec := asDevice(w.s, w.s.deviceLayoutGet, "GET", "/api/v1/device/layout", other, ""); strings.TrimSpace(rec.Body.String()) != `{"layout":null}` {
		t.Fatalf("sam: %s", rec.Body)
	}
}

func TestDeviceNotificationsAndUnlink(t *testing.T) {
	w := deviceWorld(t)
	tok := linkDevice(t, w.s, w.users["alex"])
	rec := asDevice(w.s, w.s.deviceNotifications, "GET", "/api/v1/device/notifications?after=0&limit=5", tok, "")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"admin":false,"items":[]}` {
		t.Fatalf("non-admin: %d %s", rec.Code, rec.Body)
	}
	admTok := linkDevice(t, w.s, w.users["admin"])
	rec = asDevice(w.s, w.s.deviceNotifications, "GET", "/api/v1/device/notifications", admTok, "")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"admin":true,"items":[]}` {
		t.Fatalf("admin: %d %s", rec.Code, rec.Body)
	}

	if rec := asDevice(w.s, w.s.deviceUnlink, "DELETE", "/api/v1/device/me", tok, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("unlink: %d %s", rec.Code, rec.Body)
	}
	if rec := asDevice(w.s, w.s.deviceMe, "GET", "/api/v1/device/me", tok, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("after unlink: %d", rec.Code)
	}
	if rec := asDevice(w.s, w.s.deviceMe, "GET", "/api/v1/device/me", admTok, ""); rec.Code != http.StatusOK {
		t.Fatalf("other device: %d", rec.Code)
	}
}

func TestDeviceFilesAndNoIndex(t *testing.T) {
	w := deviceWorld(t)
	tok := linkDevice(t, w.s, w.users["alex"])
	rec := asDevice(w.s, w.s.filesRoots, "GET", "/api/v1/device/files/roots", tok, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), spaces.HomeRootID("alex")) {
		t.Fatalf("roots: %d %s", rec.Code, rec.Body)
	}
	if rec := asDevice(w.s, w.s.filesList, "GET", list(spaces.ShareRootID(w.secret), "/"), tok, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("secret: %d", rec.Code)
	}
	w.s.FileIndex = nil
	for _, h := range []http.HandlerFunc{w.s.withIndex(w.s.filesRecent), w.s.withIndex(w.s.filesSearch)} {
		rec := asDevice(w.s, h, "GET", "/api/v1/device/files/search?q=x", tok, "")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"results":[]`) {
			t.Fatalf("no index: %d %s", rec.Code, rec.Body)
		}
	}
}

func TestDeviceAI(t *testing.T) {
	w := deviceWorld(t)
	alexTok := linkDevice(t, w.s, w.users["alex"])
	samTok := linkDevice(t, w.s, w.users["sam"])

	// No AI on this server: 503, not a panic.
	if rec := asDevice(w.s, w.s.withAI(w.s.aiListConversations), "GET", "/api/v1/device/ai/conversations", alexTok, ""); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"no_ai"`) {
		t.Fatalf("no ai: %d %s", rec.Code, rec.Body)
	}

	box, err := ai.NewSecretBox(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	w.s.AI = ai.NewService(w.s.Store, box, slog.New(slog.NewTextHandler(io.Discard, nil)))

	rec := asDevice(w.s, w.s.withAI(w.s.aiCreateConversation), "POST", "/api/v1/device/ai/conversations", alexTok, `{}`)
	var conv struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &conv); rec.Code != http.StatusCreated || err != nil || conv.ID == "" {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	withID := func(method, target, body string) *http.Request {
		r := httptest.NewRequest(method, target, strings.NewReader(body))
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		r.SetPathValue("id", conv.ID)
		return r
	}
	if rec := asDevice(w.s, w.s.withAI(w.s.aiListConversations), "GET", "/api/v1/device/ai/conversations", alexTok, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), conv.ID) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	if rec := asDevice(w.s, w.s.withAI(w.s.aiListConversations), "GET", "/api/v1/device/ai/conversations", samTok, ""); strings.Contains(rec.Body.String(), conv.ID) {
		t.Fatalf("sam sees alex's chat: %s", rec.Body)
	}
	target := "/api/v1/device/ai/conversations/" + conv.ID
	if rec := devDo(w.s, w.s.withAI(w.s.aiGetConversation), withID("GET", target, ""), alexTok); rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	if rec := devDo(w.s, w.s.withAI(w.s.aiGetConversation), withID("GET", target, ""), samTok); rec.Code != http.StatusNotFound {
		t.Fatalf("sam get: %d", rec.Code)
	}
	if rec := devDo(w.s, w.s.withAI(w.s.aiDeleteConversation), withID("DELETE", target, ""), samTok); rec.Code != http.StatusNotFound {
		t.Fatalf("sam delete: %d", rec.Code)
	}

	// No provider: the stream carries an error line.
	rec = devDo(w.s, w.s.withAI(w.s.aiSend), withID("POST", target+"/send", `{"text":"hello"}`), alexTok)
	var line map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(rec.Body.Bytes()), &line); rec.Code != http.StatusOK || err != nil || line["error"] == nil {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("content type %q", ct)
	}

	if rec := devDo(w.s, w.s.withAI(w.s.aiDeleteConversation), withID("DELETE", target, ""), alexTok); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := devDo(w.s, w.s.withAI(w.s.aiGetConversation), withID("GET", target, ""), alexTok); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: %d", rec.Code)
	}
}

// TestDeviceRoutesRegistered checks the real router knows the new paths
// (an unknown /api path would be the JSON "no such endpoint" 404).
func TestDeviceRoutesRegistered(t *testing.T) {
	w := deviceWorld(t)
	var h http.Handler
	func() {
		defer func() {
			if v := recover(); v != nil {
				t.Skipf("server needs more to build: %v", v)
			}
		}()
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		deps := w.s.Deps
		deps.Config = &config.Config{DataDir: w.s.Config.DataDir, MaxUpload: 1 << 20}
		h = New(ctx, deps).Handler()
	}()
	for _, rt := range []string{
		"GET /api/v1/device/summary", "GET /api/v1/device/prefs", "GET /api/v1/device/files/roots",
		"GET /api/v1/device/files/list", "GET /api/v1/device/files/recent", "GET /api/v1/device/files/search",
		"POST /api/v1/device/files/ticket", "POST /api/v1/device/send", "GET /api/v1/device/layout",
		"PUT /api/v1/device/layout", "GET /api/v1/device/notifications", "DELETE /api/v1/device/me",
		"GET /api/v1/device/ai/conversations", "POST /api/v1/device/ai/conversations",
		"GET /api/v1/device/ai/conversations/x", "DELETE /api/v1/device/ai/conversations/x",
		"POST /api/v1/device/ai/conversations/x/send",
		"POST /api/v1/device/phone-notifications", "DELETE /api/v1/device/phone-notifications/0%7Ccom.chat%2F1",
		"POST /api/v1/device/clipboard", "GET /api/v1/device/events?after=0&wait=0",
		"GET /api/v1/device/wifi/saved", "POST /api/v1/device/wifi/share",
		// NoCap OS's side (a session, not a device token).
		"GET /api/v1/phone-notifications", "DELETE /api/v1/phone-notifications/d1/k%2F1",
		"GET /api/v1/clipboard", "POST /api/v1/clipboard",
	} {
		method, target, _ := strings.Cut(rt, " ")
		r := httptest.NewRequest(method, target, nil)
		r.RemoteAddr = "127.0.0.1:5000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d %s", rt, rec.Code, rec.Body)
		}
	}
}
