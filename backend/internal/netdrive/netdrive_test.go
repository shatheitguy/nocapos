package netdrive

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/spaces"
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
		h.mounts[args[len(args)-1]] = true
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
	sp, err := spaces.New(context.Background(), st, fsvc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	sp.SetPeople(func(context.Context) ([]spaces.Person, error) {
		return []spaces.Person{{Name: "admin", Admin: true}, {Name: "alex"}, {Name: "sam"}, {Name: "gone", Disabled: true}}, nil
	})
	passwords := map[string]string{"admin": "admin-pass-1", "alex": "alex-pass-1", "sam": "sam-pass-1"}
	m.UseSpaces(sp, func(_ context.Context, user, pw string) (string, bool, bool) {
		if p, ok := passwords[user]; ok && p == pw {
			return user, user == "admin", true
		}
		return "", false, false
	})
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
	if !strings.Contains(last, "-t cifs -o ") || !strings.Contains(last, " -- //nas/Media ") || !strings.Contains(last, "credentials=") || strings.Contains(last, "s3cret") {
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
	if c := h.called("mount -t nfs -o soft,timeo=150,retrans=2 -- nas:/volume1/archive "); len(c) != 1 {
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
