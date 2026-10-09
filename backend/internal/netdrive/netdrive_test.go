package netdrive

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

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

// fakeHost stands in for mount/umount/systemctl/useradd/smbpasswd.
type fakeHost struct {
	mu       sync.Mutex
	calls    []string
	stdin    []string
	mounts   map[string]bool
	failNext string // output for the next mount, as an error
}

func (h *fakeHost) run(_ context.Context, stdin, name string, args ...string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, name+" "+strings.Join(args, " "))
	h.stdin = append(h.stdin, stdin)
	switch name {
	case "mount":
		if h.failNext != "" {
			out := h.failNext
			h.failNext = ""
			return out, errors.New("exit status 32")
		}
		h.mounts[args[3]] = true
	case "umount":
		delete(h.mounts, args[len(args)-1])
	case "id":
		return "", errors.New("no such user")
	case "systemctl":
		if len(args) > 0 && args[0] == "list-unit-files" {
			return "smbd.service enabled enabled", nil
		}
	}
	return "", nil
}

func (h *fakeHost) mounted(p string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.mounts[p]
}

func (h *fakeHost) called(prefix string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, c := range h.calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func setup(t *testing.T) (*Manager, *fakeHost, string) {
	t.Helper()
	dir := t.TempDir()
	drive := filepath.Join(dir, "drive")
	for _, p := range []string{"Media/Movies", "Docs"} {
		if err := os.MkdirAll(filepath.Join(drive, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	data := filepath.Join(dir, "data")
	_ = os.MkdirAll(data, 0o700)
	st, err := store.Open(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fsvc, err := files.New([]files.Root{{ID: "drive", Name: "Drive", Path: drive}})
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(st, plainBox{}, fsvc, data, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := &fakeHost{mounts: map[string]bool{}}
	m.run, m.mounted, m.native = h.run, h.mounted, func() bool { return true }
	m.lookPath = func(string) (string, error) { return "/usr/bin/x", nil }
	m.mountBase = filepath.Join(dir, "mnt")
	m.sambaDir = filepath.Join(dir, "samba")
	return m, h, drive
}

func TestValidate(t *testing.T) {
	ok := []NewDrive{
		{Name: "NAS", Kind: "smb", Host: "192.168.1.20", Share: "Media"},
		{Name: "NAS", Kind: "smb", Host: `\\nas.local`, Share: `Media\Movies`, Username: `WORKGROUP\sha`},
		{Name: "NFS", Kind: "nfs", Host: "nas", Share: "/volume1/media"},
	}
	for _, in := range ok {
		if err := validate(&in); err != nil {
			t.Errorf("%+v: %v", in, err)
		}
	}
	bad := []NewDrive{
		{Name: "", Kind: "smb", Host: "nas", Share: "Media"},
		{Name: "x", Kind: "smb", Host: "nas; rm -rf /", Share: "Media"},
		{Name: "x", Kind: "smb", Host: "nas", Share: "../etc"},
		{Name: "x", Kind: "smb", Host: "nas", Share: "Media", Username: "a,b"},
		{Name: "x", Kind: "smb", Host: "nas", Share: "Media", Password: "a\nusername=root"},
		{Name: "x", Kind: "nfs", Host: "nas", Share: "volume1"},
		{Name: "x", Kind: "ftp", Host: "nas", Share: "/x"},
	}
	for _, in := range bad {
		if err := validate(&in); err == nil {
			t.Errorf("%+v should be refused", in)
		}
	}
}

func TestDrives(t *testing.T) {
	ctx := context.Background()
	m, h, _ := setup(t)

	// A refused password: nothing is saved, the message is plain.
	h.failNext = "mount error(13): Permission denied"
	if _, err := m.Add(ctx, NewDrive{Name: "NAS", Kind: "smb", Host: "nas", Share: "Media", Username: "sha", Password: "wrong"}); err == nil || !strings.Contains(err.Error(), "user name or password was refused") {
		t.Fatalf("expected a refused-password error, got %v", err)
	}
	if ds, _ := m.Drives(ctx); len(ds) != 0 {
		t.Fatalf("failed drive was kept: %+v", ds)
	}

	d, err := m.Add(ctx, NewDrive{Name: "Living Room NAS", Kind: "smb", Host: "nas", Share: "Media", Username: `HOME\sha`, Password: "s3cret", Auto: true})
	if err != nil {
		t.Fatal(err)
	}
	if !d.Mounted || d.RootID == "" || d.Address != `\\nas\Media` {
		t.Fatalf("drive %+v", d)
	}
	mounts := h.called("mount ")
	last := mounts[len(mounts)-1]
	if !strings.Contains(last, "-t cifs //nas/Media ") || !strings.Contains(last, "credentials=") || strings.Contains(last, "s3cret") {
		t.Fatalf("mount command %q (the password must not be on the command line)", last)
	}
	cred, err := os.ReadFile(m.credFile(d.ID))
	if err != nil || string(cred) != "username=sha\npassword=s3cret\ndomain=HOME\n" {
		t.Fatalf("credentials file %q %v", cred, err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(m.credFile(d.ID)); fi.Mode().Perm() != 0o600 {
			t.Errorf("credentials file mode %v", fi.Mode().Perm())
		}
	}
	if r, err := m.files.Root(d.RootID); err != nil || r.Name != "Living Room NAS" {
		t.Fatalf("storage location %v %v", r, err)
	}

	if d, err = m.Disconnect(ctx, d.ID); err != nil || d.Mounted {
		t.Fatalf("disconnect %+v %v", d, err)
	}
	if _, err := m.files.Root(d.RootID); err == nil {
		t.Error("disconnected drive still a storage location")
	}
	if d, err = m.Connect(ctx, d.ID); err != nil || !d.Mounted {
		t.Fatalf("connect %+v %v", d, err)
	}

	nfs, err := m.Add(ctx, NewDrive{Name: "Archive", Kind: "nfs", Host: "nas", Share: "/volume1/archive"})
	if err != nil {
		t.Fatal(err)
	}
	if c := h.called("mount -t nfs nas:/volume1/archive "); len(c) != 1 {
		t.Errorf("nfs mount: %v", h.called("mount"))
	}

	if err := m.Delete(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.credFile(d.ID)); !os.IsNotExist(err) {
		t.Error("credentials file left behind")
	}
	if ds, _ := m.Drives(ctx); len(ds) != 1 || ds[0].ID != nfs.ID {
		t.Fatalf("after delete: %+v", ds)
	}
}

func TestSharingSamba(t *testing.T) {
	ctx := context.Background()
	m, h, _ := setup(t)
	on := true
	if err := m.SetProtocols(ctx, &on, nil); err == nil {
		t.Fatal("sharing must need a password first")
	}
	if err := m.SetPassword(ctx, "short"); err == nil {
		t.Error("short password accepted")
	}
	if err := m.SetPassword(ctx, "share-pass-1"); err != nil {
		t.Fatal(err)
	}
	if c := h.called("useradd --system"); len(c) != 1 {
		t.Errorf("useradd: %v", h.calls)
	}
	var sawPw bool
	for i, c := range h.calls {
		if strings.HasPrefix(c, "smbpasswd -a -s nocapos") && h.stdin[i] == "share-pass-1\nshare-pass-1\n" {
			sawPw = true
		}
	}
	if !sawPw {
		t.Error("samba password not set through stdin")
	}
	if !m.CheckPassword(ctx, "NoCapOS", "share-pass-1") || m.CheckPassword(ctx, "nocapos", "nope") || m.CheckPassword(ctx, "root", "share-pass-1") {
		t.Error("password check")
	}

	if err := m.SetProtocols(ctx, &on, &on); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddShare(ctx, "Media", "drive", "/Media", false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddShare(ctx, "media", "drive", "/Docs", false); err == nil {
		t.Error("duplicate share name accepted")
	}
	for _, bad := range [][3]string{{"IPC$", "drive", "/Docs"}, {"Docs", "system", "/etc"}, {"Docs", "drive", "/nope"}, {"[x]", "drive", "/Docs"}} {
		if _, err := m.AddShare(ctx, bad[0], bad[1], bad[2], false); err == nil {
			t.Errorf("share %v accepted", bad)
		}
	}
	if _, err := m.AddShare(ctx, "Docs", "drive", "/Docs", true); err != nil {
		t.Fatal(err)
	}
	conf, _ := os.ReadFile(filepath.Join(m.sambaDir, confName))
	for _, want := range []string{"[Media]", "valid users = nocapos", "read only = no", "[Docs]", "read only = yes"} {
		if !strings.Contains(string(conf), want) {
			t.Errorf("nocapos.conf lacks %q:\n%s", want, conf)
		}
	}
	main, _ := os.ReadFile(filepath.Join(m.sambaDir, "smb.conf"))
	if strings.Count(string(main), includeLine) != 1 {
		t.Errorf("smb.conf include line count wrong:\n%s", main)
	}
	if len(h.called("systemctl enable --now smbd")) == 0 {
		t.Errorf("samba not started: %v", h.called("systemctl"))
	}

	off := false
	if err := m.SetProtocols(ctx, &off, nil); err != nil {
		t.Fatal(err)
	}
	conf, _ = os.ReadFile(filepath.Join(m.sambaDir, confName))
	if strings.Contains(string(conf), "[Media]") {
		t.Error("shares still configured after SMB was turned off")
	}
}

func TestWebDAV(t *testing.T) {
	ctx := context.Background()
	m, _, drive := setup(t)
	m.lookPath = func(string) (string, error) { return "", errors.New("not found") } // no Samba: WebDAV only
	if err := m.SetPassword(ctx, "share-pass-1"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(m.DAVHandler(ClientIP))
	defer srv.Close()
	do := func(method, path, user, pw, body string, hdr ...string) *http.Response {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if user != "" {
			req.SetBasicAuth(user, pw)
		}
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}

	if r := do("PROPFIND", "/dav/", "nocapos", "share-pass-1", "", "Depth", "1"); r.StatusCode != http.StatusNotFound {
		t.Fatalf("WebDAV off: %d", r.StatusCode)
	}
	on := true
	if err := m.SetProtocols(ctx, nil, &on); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddShare(ctx, "Media", "drive", "/Media", false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddShare(ctx, "Docs", "drive", "/Docs", true); err != nil {
		t.Fatal(err)
	}

	if r := do("PROPFIND", "/dav/", "", "", "", "Depth", "1"); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no password: %d", r.StatusCode)
	}
	if r := do("PROPFIND", "/dav/", "nocapos", "share-pass-1", "", "Depth", "1"); r.StatusCode != http.StatusMultiStatus {
		t.Fatalf("list shares: %d", r.StatusCode)
	}
	if r := do("PUT", "/dav/Media/hello.txt", "nocapos", "share-pass-1", "hi there"); r.StatusCode != http.StatusCreated {
		t.Fatalf("put: %d", r.StatusCode)
	}
	if b, _ := os.ReadFile(filepath.Join(drive, "Media", "hello.txt")); string(b) != "hi there" {
		t.Fatalf("file on disk %q", b)
	}
	if r := do("PUT", "/dav/Docs/nope.txt", "nocapos", "share-pass-1", "x"); r.StatusCode < 400 {
		t.Errorf("write to a read-only share: %d", r.StatusCode)
	}
	if r := do("DELETE", "/dav/Media/", "nocapos", "share-pass-1", ""); r.StatusCode < 400 {
		t.Errorf("deleting a share's own folder: %d", r.StatusCode)
	}
	if r := do("MOVE", "/dav/Media/hello.txt", "nocapos", "share-pass-1", "", "Destination", srv.URL+"/dav/Docs/hello.txt"); r.StatusCode < 400 {
		t.Errorf("move into a read-only share: %d", r.StatusCode)
	}
	if r := do("PUT", "/dav/hello.txt", "nocapos", "share-pass-1", "x"); r.StatusCode < 400 {
		t.Errorf("write at the top level: %d", r.StatusCode)
	}
	if r := do("DELETE", "/dav/Media/hello.txt", "nocapos", "share-pass-1", ""); r.StatusCode != http.StatusNoContent {
		t.Errorf("delete a file: %d", r.StatusCode)
	}

	// Guessing gets slowed down.
	for i := 0; i < 5; i++ {
		do("PROPFIND", "/dav/", "nocapos", "guess", "", "Depth", "0")
	}
	if r := do("PROPFIND", "/dav/", "nocapos", "share-pass-1", "", "Depth", "0"); r.StatusCode != http.StatusTooManyRequests {
		t.Errorf("after 5 wrong passwords: %d", r.StatusCode)
	}
}
