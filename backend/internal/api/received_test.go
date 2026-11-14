package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"alfaos/alfad/internal/store"
)

// recvCall runs h as u with a JSON body (nil for none) and an optional {id}.
func recvCall(s *Server, h http.HandlerFunc, u *store.User, method, target, id string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	r := httptest.NewRequest(method, target, &buf)
	r.Header.Set("Content-Type", "application/json")
	if id != "" {
		r.SetPathValue("id", id)
	}
	r = r.WithContext(context.WithValue(r.Context(), userKey, u))
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

func recvInfo(t *testing.T, rec *httptest.ResponseRecorder) receivedJSON {
	t.Helper()
	var out receivedJSON
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("received: %d %s", rec.Code, rec.Body)
	}
	return out
}

func TestCleanFileName(t *testing.T) {
	for in, want := range map[string]string{
		"pic.jpg":                         "pic.jpg",
		`C:\phone\pic.jpg`:                "pic.jpg",
		"../../etc/passwd":                "passwd",
		"..":                              "",
		".":                               "",
		"a/..":                            "",
		"  spaced .txt ":                  "spaced .txt",
		"bad:name?.txt":                   "bad_name_.txt",
		"tab\there.txt":                   "tab_here.txt",
		"...hidden":                       "hidden",
		strings.Repeat("x", 300) + ".pdf": strings.Repeat("x", 116) + ".pdf",
	} {
		if got := cleanFileName(in); got != want {
			t.Errorf("cleanFileName(%q) = %q, want %q", in, got, want)
		}
	}
	if receivedDeviceDir("../..") != "My device" || receivedDeviceDir("Pixel 9") != "Pixel 9" {
		t.Error("device folder names")
	}
	for _, bad := range []string{"", "a/b", "..", ".hidden", "x:y", strings.Repeat("y", 65)} {
		if _, ok := receivedFolderName(bad); ok {
			t.Errorf("folder name %q accepted", bad)
		}
	}
}

func TestReceivedFolder(t *testing.T) {
	w := deviceWorld(t)
	alex := w.users["alex"]
	ctx := context.Background()
	home := w.s.Spaces.HomePath("alex")
	if home == "" {
		t.Fatal("no home path")
	}
	// Files from before: the old "NoCap Link" folder moves into Received.
	if err := os.MkdirAll(filepath.Join(home, "NoCap Link"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "NoCap Link", "old.jpg"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(filepath.Join(home, "NoCap Link", "old.jpg"), old, old)
	// Standard voice: "Received".
	if err := w.s.Store.SetUserPrefs(ctx, alex.ID, `{"lingo":"standard"}`); err != nil {
		t.Fatal(err)
	}
	info := recvInfo(t, recvCall(w.s, w.s.receivedGet, alex, "GET", "/api/v1/received", "", nil))
	if !info.Available || info.Folder != "Received" || info.Path != "/Received" || info.Default != "Received" {
		t.Fatalf("info: %+v", info)
	}
	if b, err := os.ReadFile(filepath.Join(home, "Received", "old.jpg")); err != nil || string(b) != "old" {
		t.Fatalf("legacy file not moved: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(home, "NoCap Link")); !os.IsNotExist(err) {
		t.Fatalf("legacy folder still there: %v", err)
	}
	if info.Unread != 0 {
		t.Fatalf("old files count as new: %d", info.Unread)
	}

	// A phone sends a file: it lands in Received/<device> and counts as new.
	tok := linkDevice(t, w.s, alex)
	feed, stop := w.s.linkFeed.subscribe(alex.ID)
	defer stop()
	_ = w.s.link()
	rec := devDo(w.s, w.s.deviceSend, multipartSend(t, map[string]string{"../../escape.txt": "hi"}), tok)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"/Received/Pixel 9/escape.txt"`) {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	select {
	case ev := <-feed:
		if ev.Type != "received" || ev.Received == nil || ev.Received.Folder != "/Received/Pixel 9" || len(ev.Received.Paths) != 1 {
			t.Fatalf("event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no received event")
	}
	info = recvInfo(t, recvCall(w.s, w.s.receivedGet, alex, "GET", "/api/v1/received", "", nil))
	if info.Unread != 1 || len(info.Devices) != 1 {
		t.Fatalf("after send: %+v", info)
	}
	for _, p := range info.Devices {
		if p != "/Received/Pixel 9" {
			t.Fatalf("device folder: %q", p)
		}
	}
	// Seen: nothing new.
	if rec := recvCall(w.s, w.s.receivedSeen, alex, "POST", "/api/v1/received/seen", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("seen: %d", rec.Code)
	}
	if info = recvInfo(t, recvCall(w.s, w.s.receivedGet, alex, "GET", "/api/v1/received", "", nil)); info.Unread != 0 {
		t.Fatalf("after seen: %d", info.Unread)
	}

	// Rename: the folder moves with it; bad names and clashes are refused.
	if rec := recvCall(w.s, w.s.receivedPut, alex, "PUT", "/api/v1/received", "", map[string]string{"folder": "../x"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad name: %d", rec.Code)
	}
	if err := os.MkdirAll(filepath.Join(home, "Taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if rec := recvCall(w.s, w.s.receivedPut, alex, "PUT", "/api/v1/received", "", map[string]string{"folder": "Taken"}); rec.Code != http.StatusConflict {
		t.Fatalf("clash: %d %s", rec.Code, rec.Body)
	}
	info = recvInfo(t, recvCall(w.s, w.s.receivedPut, alex, "PUT", "/api/v1/received", "", map[string]string{"folder": "From my phone"}))
	if info.Folder != "From my phone" {
		t.Fatalf("renamed: %+v", info)
	}
	if _, err := os.Stat(filepath.Join(home, "From my phone", "Pixel 9", "escape.txt")); err != nil {
		t.Fatalf("folder not renamed: %v", err)
	}
}

func TestReceivedOutboxOwnerOnly(t *testing.T) {
	w := deviceWorld(t)
	alex, admin := w.users["alex"], w.users["admin"]
	linkDevice(t, w.s, alex)
	list, err := w.s.Store.ListDevices(context.Background(), alex.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("devices: %v %d", err, len(list))
	}
	id := list[0].ID
	// Someone else (an administrator too) gets "not found".
	if rec := recvCall(w.s, w.s.receivedOutbox, admin, "POST", "/x", id, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("admin outbox: %d", rec.Code)
	}
	rec := recvCall(w.s, w.s.receivedOutbox, alex, "POST", "/x", id, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"/Drops/Pixel 9/Sent"`) {
		t.Fatalf("outbox: %d %s", rec.Code, rec.Body)
	}
	// Files sent from here don't count as received.
	home := w.s.Spaces.HomePath("alex")
	if err := os.WriteFile(filepath.Join(home, "Drops", "Pixel 9", "Sent", "out.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if info := recvInfo(t, recvCall(w.s, w.s.receivedGet, alex, "GET", "/api/v1/received", "", nil)); info.Unread != 0 {
		t.Fatalf("sent files counted: %d", info.Unread)
	}
}
