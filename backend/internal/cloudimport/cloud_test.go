package cloudimport

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/webdav"

	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/store"
)

type plainBox struct{}

func (plainBox) Seal(s string) []byte { return []byte("sealed:" + s) }
func (plainBox) Open(b []byte) (string, error) {
	s, ok := strings.CutPrefix(string(b), "sealed:")
	if !ok {
		return "", errors.New("not sealed")
	}
	return s, nil
}

func TestNextRun(t *testing.T) {
	last := time.Date(2026, 10, 9, 14, 0, 0, 0, time.Local) // Friday
	if got := nextRun(Schedule{Every: "day", At: "03:00"}, last, time.Time{}); !got.Equal(time.Date(2026, 10, 10, 3, 0, 0, 0, time.Local)) {
		t.Errorf("daily: %v", got)
	}
	if got := nextRun(Schedule{Every: "week", At: "03:00", Weekday: 1}, last, time.Time{}); !got.Equal(time.Date(2026, 10, 12, 3, 0, 0, 0, time.Local)) {
		t.Errorf("weekly: %v", got)
	}
	if got := nextRun(Schedule{Every: "manual"}, last, time.Time{}); !got.IsZero() {
		t.Errorf("manual: %v", got)
	}
}

func TestRcloneError(t *testing.T) {
	lines := []string{
		`{"level":"info","msg":"starting"}`,
		`{"level":"error","msg":"Failed to copy: couldn't list directory: 401 Unauthorized"}`,
	}
	if got := rcloneError(lines, errors.New("exit 1")).Error(); !strings.Contains(got, "refused the sign-in") {
		t.Errorf("got %q", got)
	}
	plain := []string{"2026/10/10 00:18:47 ERROR : Photos: directory not found"}
	if got := rcloneError(plain, errors.New("exit 3")).Error(); got != "that folder doesn't exist in the account" {
		t.Errorf("got %q", got)
	}
	auth := []string{`2026/10/10 00:35:52 Failed to lsjson with 2 errors: last error was: error in ListJSON: couldn't list files: 401 Unauthorized`}
	if got := rcloneError(auth, errors.New("exit 1")).Error(); !strings.Contains(got, "refused the sign-in") {
		t.Errorf("got %q", got)
	}
	other := []string{`2026/10/10 00:35:52 Failed to copy: disk quota exceeded`}
	if got := rcloneError(other, errors.New("exit 1")).Error(); got != "Failed to copy: disk quota exceeded" {
		t.Errorf("got %q", got)
	}
}

func TestSignInAddresses(t *testing.T) {
	a := &authorizer{current: &authSession{id: "abc", done: make(chan struct{})}}
	for _, bad := range []string{
		"https://evil.example/?code=1&state=2",
		"http://127.0.0.1:8080/?code=1&state=2",
		"http://127.0.0.1:53682/?state=2",
		"not a url at all",
	} {
		if _, err := a.finish("abc", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := a.finish("other", "http://127.0.0.1:53682/?code=1&state=2"); err == nil {
		t.Error("a stale sign-in session was accepted")
	}
	if _, err := followLocal("https://accounts.google.com/o/oauth2/auth"); err == nil {
		t.Error("followLocal must only follow rclone's own local link")
	}
}

// TestImportOverWebDAV needs rclone (on PATH or $ALFA_RCLONE). It serves a
// folder over WebDAV and imports it like a Nextcloud account.
func TestImportOverWebDAV(t *testing.T) {
	if os.Getenv("ALFA_RCLONE") == "" {
		if _, err := exec.LookPath("rclone"); err != nil {
			t.Skip("rclone not installed")
		}
	}
	ctx := context.Background()
	dir := t.TempDir()
	cloud := filepath.Join(dir, "cloud")
	put := func(rel, body string) {
		p := filepath.Join(cloud, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("Photos/2024/beach.jpg", strings.Repeat("b", 50_000))
	put("Photos/2024/hike.jpg", strings.Repeat("h", 70_000))
	put("Documents/cv.pdf", "cv")

	dav := &webdav.Handler{FileSystem: webdav.Dir(cloud), LockSystem: webdav.NewMemLS()}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "me" || p != "cloud-pass" {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		dav.ServeHTTP(w, r)
	}))
	defer srv.Close()

	drive := filepath.Join(dir, "drive")
	data := filepath.Join(dir, "data")
	_ = os.MkdirAll(data, 0o700)
	st, err := store.Open(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fsvc, err := files.New([]files.Root{{ID: "drive", Name: "Drive", Path: drive}})
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(st, plainBox{}, fsvc, data, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if s := m.Status(ctx); !s.Installed {
		t.Fatalf("status %+v", s)
	}

	if _, err := m.AddAccount(ctx, NewAccount{Name: "Nextcloud", Kind: "webdav", URL: srv.URL, User: "me", Password: "wrong"}); err == nil {
		t.Fatal("a wrong password must fail when adding the account")
	}
	acct, err := m.AddAccount(ctx, NewAccount{Name: "Nextcloud", Kind: "webdav", URL: srv.URL, Vendor: "other", User: "me", Password: "cloud-pass"})
	if err != nil {
		t.Fatal(err)
	}
	if acct.Detail != srv.URL {
		t.Errorf("detail %q", acct.Detail)
	}
	// The password is stored obscured, never as typed.
	raw, _ := st.CloudAccount(ctx, acct.ID)
	if strings.Contains(string(raw.Config), "cloud-pass") {
		t.Error("the plain password is stored")
	}

	top, err := m.Folders(ctx, acct.ID, "")
	if err != nil || len(top) != 2 || top[0].Name != "Documents" || top[1].Name != "Photos" {
		t.Fatalf("top folders %+v %v", top, err)
	}
	sub, err := m.Folders(ctx, acct.ID, "Photos")
	if err != nil || len(sub) != 1 || sub[0].Path != "Photos/2024" {
		t.Fatalf("Photos folders %+v %v", sub, err)
	}

	id, err := m.SaveImport(ctx, Import{AccountID: acct.ID, Name: "Cloud photos", Source: "Photos", DestRoot: "drive", DestPath: "/Imports/Nextcloud", Schedule: Schedule{Every: "manual"}})
	if err != nil {
		t.Fatal(err)
	}
	run := func() *Job {
		j, err := m.Run(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 300; i++ {
			if got, _ := m.JobByID(j.ID); got.Done {
				return got
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("import did not finish")
		return nil
	}
	j := run()
	if j.Error != "" || j.Progress.Files != 2 || j.Progress.Bytes != 120_000 {
		t.Fatalf("first import %+v", j)
	}
	got, err := os.ReadFile(filepath.Join(drive, "Imports", "Nextcloud", "2024", "hike.jpg"))
	if err != nil || len(got) != 70_000 {
		t.Fatalf("imported file: %d bytes, %v", len(got), err)
	}

	// Again: nothing new to copy. Then a changed file is copied, and a file
	// removed from the cloud stays on the server.
	if j := run(); j.Error != "" || j.Progress.Files != 0 {
		t.Fatalf("second import copied again: %+v", j)
	}
	put("Photos/2024/new.jpg", "new photo")
	_ = os.Remove(filepath.Join(cloud, "Photos", "2024", "beach.jpg"))
	if j := run(); j.Error != "" || j.Progress.Files != 1 {
		t.Fatalf("third import %+v", j)
	}
	if _, err := os.Stat(filepath.Join(drive, "Imports", "Nextcloud", "2024", "beach.jpg")); err != nil {
		t.Error("a file deleted in the cloud must stay on the server")
	}

	imps, _ := m.Imports(ctx)
	if len(imps) != 1 || imps[0].LastStatus != "ok" || imps[0].LastFiles != 1 {
		t.Fatalf("import status %+v", imps)
	}
	if err := m.DeleteAccount(ctx, acct.ID); !errors.Is(err, store.ErrInUse) {
		t.Errorf("deleting an account in use: %v", err)
	}
	if err := m.DeleteImport(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteAccount(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	if left, _ := filepath.Glob(filepath.Join(data, "secrets", "rclone", "*.conf")); len(left) != 0 {
		t.Errorf("temporary configs left behind: %v", left)
	}
}
