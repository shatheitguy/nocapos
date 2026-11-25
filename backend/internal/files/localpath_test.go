package files

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLocalPath(t *testing.T) {
	s, dir := newTestService(t)
	if err := os.MkdirAll(filepath.Join(dir, "Photos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Photos", "cat.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	base, _ := filepath.EvalSymlinks(dir)
	if p, err := s.LocalPath("t", "Photos/cat.png"); err != nil || p != filepath.Join(base, "Photos", "cat.png") {
		t.Fatalf("file: %q, %v", p, err)
	}
	if p, err := s.LocalPath("t", "Photos"); err != nil || p != "" {
		t.Fatalf("a folder isn't a file: %q, %v", p, err)
	}
	if p, err := s.LocalPath("t", "missing.txt"); err != nil || p != "" {
		t.Fatalf("missing: %q, %v", p, err)
	}
	if _, err := s.LocalPath("nope", "a"); err == nil {
		t.Fatal("unknown location must fail")
	}
	if runtime.GOOS == "windows" {
		return
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if p, err := s.LocalPath("t", "link.txt"); err != nil || p != "" {
		t.Fatalf("a link out of the location must not resolve: %q, %v", p, err)
	}
}
