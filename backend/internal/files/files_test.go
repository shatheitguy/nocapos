package files

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := New([]Root{{ID: "t", Name: "Test", Path: dir}})
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func TestCleanNeutralizesTraversal(t *testing.T) {
	cases := map[string]string{
		"":              ".",
		"/":             ".",
		"../../etc":     "etc",
		`..\..\Windows`: "Windows",
		"/a/./b/../c":   "a/c",
		"a//b/":         "a/b",
	}
	for in, want := range cases {
		got, err := Clean(in)
		if err != nil || got != want {
			t.Errorf("Clean(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := Clean("a\x00b"); err == nil {
		t.Error("NUL byte must be rejected")
	}
}

func TestValidName(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "a/b", `a\b`, "a:b", "con?", " lead", "trail.", "tab\t"} {
		if ValidName(bad) == nil {
			t.Errorf("ValidName(%q) should fail", bad)
		}
	}
	for _, good := range []string{"report.pdf", ".env", "Photos 2024", "naïve résumé.txt"} {
		if err := ValidName(good); err != nil {
			t.Errorf("ValidName(%q) = %v", good, err)
		}
	}
}

func TestSymlinkEscapeBlocked(t *testing.T) {
	s, dir := newTestService(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Skip("symlinks unavailable:", err) // Windows without developer mode
	}
	if _, _, err := s.Open("t", "escape/secret.txt"); err == nil {
		t.Fatal("reading through a symlink that leaves the root must fail")
	}
}

func TestLifecycle(t *testing.T) {
	s, dir := newTestService(t)

	if _, err := s.Mkdir("t", ".", "docs"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Mkdir("t", ".", "docs"); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("duplicate mkdir: %v", err)
	}

	p, err := s.Upload("t", "docs", "a.txt", strings.NewReader("hello"))
	if err != nil || p != "docs/a.txt" {
		t.Fatalf("upload: %q %v", p, err)
	}
	p2, err := s.Upload("t", "docs", "a.txt", strings.NewReader("again"))
	if err != nil || p2 != "docs/a (1).txt" {
		t.Fatalf("conflicting upload should be renamed, got %q %v", p2, err)
	}

	text, st, err := s.ReadText("t", "docs/a.txt", 1<<20)
	if err != nil || text != "hello" {
		t.Fatalf("read: %q %v", text, err)
	}
	mt := st.ModTime()
	if _, err := s.WriteText("t", "docs/a.txt", "edited", &mt); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := s.WriteText("t", "docs/a.txt", "stale", &mt); !errors.Is(err, ErrChanged) && err != nil {
		// Coarse mtime filesystems may not detect the change; only a wrong error type is a failure.
		t.Fatalf("stale write: %v", err)
	}

	if _, err := s.Rename("t", "docs/a (1).txt", "b.txt"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := s.Transfer("t", []string{"docs"}, "docs", false); !errors.Is(err, ErrIntoSelf) {
		t.Fatalf("copy into self: %v", err)
	}
	if _, err := s.Mkdir("t", ".", "backup"); err != nil {
		t.Fatal(err)
	}
	copied, err := s.Transfer("t", []string{"docs"}, "backup", false)
	if err != nil || len(copied) != 1 || copied[0] != "backup/docs" {
		t.Fatalf("copy: %v %v", copied, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "backup", "docs", "b.txt")); err != nil {
		t.Fatalf("copied file missing: %v", err)
	}

	// Delete → recycle bin (hidden from the root listing) → empty bin.
	if err := s.Delete("t", []string{"backup"}, false); err != nil {
		t.Fatal(err)
	}
	root, _ := s.List("t", ".")
	for _, e := range root {
		if e.Name == RecycleDir || e.Name == "backup" {
			t.Fatalf("unexpected entry in root listing: %s", e.Name)
		}
	}
	bin, err := s.List("t", RecycleDir)
	if err != nil || len(bin) != 1 || !strings.HasSuffix(bin[0].Name, "_backup") {
		t.Fatalf("recycle bin: %+v %v", bin, err)
	}
	if err := s.Delete("t", []string{RecycleDir}, true); err != nil {
		t.Fatal(err)
	}
	if bin, _ := s.List("t", RecycleDir); len(bin) != 0 {
		t.Fatalf("bin should be empty: %+v", bin)
	}
	if err := s.Delete("t", []string{"."}, true); !errors.Is(err, ErrIsRoot) {
		t.Fatalf("deleting the root must be refused: %v", err)
	}
}

func TestListReportsModes(t *testing.T) {
	s, dir := newTestService(t)
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\n"), 0o754); err != nil {
		t.Fatal(err)
	}
	if UnixPerms {
		_ = os.Chmod(filepath.Join(dir, "run.sh"), 0o754) // umask-proof
	}
	entries, err := s.List("t", ".")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Entry{}
	for _, e := range entries {
		by[e.Name] = e
	}
	if m := by["sub"].Mode; !strings.HasPrefix(m, "d") || len(m) != 10 {
		t.Errorf("dir mode = %q", m)
	}
	if m := by["run.sh"].Mode; !strings.HasPrefix(m, "-") || len(m) != 10 {
		t.Errorf("file mode = %q", m)
	}
	if UnixPerms {
		if p := by["run.sh"].Perm; p != 0o754 {
			t.Errorf("perm = %o, want 754", p)
		}
		if by["run.sh"].Owner == "" || by["run.sh"].Group == "" {
			t.Errorf("owner/group missing: %+v", by["run.sh"])
		}
	}
}

func TestProtectedFolders(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "AppData", "NoCap")
	app := filepath.Join(home, "Downloads", "NoCapOS")
	drive := filepath.Join(data, "files")
	for _, d := range []string{filepath.Join(app, "backend", "bin"), filepath.Join(app, "frontend"), drive, filepath.Join(home, "Documents")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(drive, "photo.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "alfa.db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New([]Root{{ID: "home", Name: "Home", Path: home}, {ID: "drive", Name: "Drive", Path: drive}})
	if err != nil {
		t.Fatal(err)
	}
	s.Protect(data, app)

	blocked := []struct{ root, rel string }{
		{"home", "Downloads/NoCapOS"},          // the program folder itself
		{"home", "Downloads"},                  // a folder that contains it
		{"home", "Downloads/NoCapOS/frontend"}, // inside the source tree
		{"home", "AppData"},                    // contains the data folder
		{"home", "AppData/NoCap/alfa.db"},      // inside the data folder
		{"home", "AppData/NoCap/files"},        // a storage location's own folder
	}
	for _, b := range blocked {
		if err := s.Delete(b.root, []string{b.rel}, true); !errors.Is(err, ErrProtected) {
			t.Errorf("delete %s:%s = %v, want ErrProtected", b.root, b.rel, err)
		}
		if _, err := s.Rename(b.root, b.rel, "x"); !errors.Is(err, ErrProtected) {
			t.Errorf("rename %s:%s = %v, want ErrProtected", b.root, b.rel, err)
		}
		if _, err := s.Transfer(b.root, []string{b.rel}, "Documents", true); !errors.Is(err, ErrProtected) {
			t.Errorf("move %s:%s = %v, want ErrProtected", b.root, b.rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(app, "frontend")); err != nil {
		t.Fatal("protected folder was removed")
	}
	// Ordinary files keep working, including inside a storage location that
	// lives in the data folder.
	if err := s.Delete("drive", []string{"photo.jpg"}, true); err != nil {
		t.Errorf("delete in drive: %v", err)
	}
	if err := s.Delete("home", []string{"AppData/NoCap/files/../files"}, true); !errors.Is(err, ErrProtected) {
		t.Errorf("drive folder via home should be protected, got %v", err)
	}
	if err := s.Delete("home", []string{"Documents"}, true); err != nil {
		t.Errorf("delete Documents: %v", err)
	}
	// Copy (not move) out of a protected folder is fine.
	if _, err := s.Transfer("home", []string{"Downloads/NoCapOS/frontend"}, ".", false); err != nil {
		t.Errorf("copy from protected folder: %v", err)
	}
}

func TestProtectPointsKeepContentsUsable(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"home/alice/docs", "srv/media"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New([]Root{{ID: "system", Name: "System", Path: root}})
	if err != nil {
		t.Fatal(err)
	}
	s.ProtectPoints(filepath.Join(root, "home"), filepath.Join(root, "srv"))

	for _, rel := range []string{"home", "srv"} {
		if err := s.Delete("system", []string{rel}, true); !errors.Is(err, ErrProtected) {
			t.Errorf("delete %s = %v, want ErrProtected", rel, err)
		}
		if _, err := s.Rename("system", rel, "x"); !errors.Is(err, ErrProtected) {
			t.Errorf("rename %s = %v, want ErrProtected", rel, err)
		}
	}
	// Contents stay ordinary files and folders.
	if _, err := s.Rename("system", "home/alice/docs", "papers"); err != nil {
		t.Errorf("rename inside /home: %v", err)
	}
	if err := s.Delete("system", []string{"srv/media"}, true); err != nil {
		t.Errorf("delete inside /srv: %v", err)
	}
}
