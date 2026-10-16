package spaces

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

// Own runs as root in a folder its owner controls: it must never change
// anything outside the home, whatever links are planted there.
func TestOwnStaysInsideHome(t *testing.T) {
	w := newWorld(t)
	base := t.TempDir()
	home := filepath.Join(base, "home")
	outside := filepath.Join(base, "etc")
	for _, d := range []string{filepath.Join(home, "alex", "Docs", "sub"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{filepath.Join(home, "alex", "Docs", "a.txt"), filepath.Join(home, "alex", "Docs", "sub", "b.txt"), filepath.Join(outside, "shadow")} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(home, "alex", "Docs", "evil")); err != nil {
		t.Skip("symlinks not available here:", err)
	}
	if err := os.Symlink(outside, filepath.Join(home, "alex", "Linked")); err != nil {
		t.Fatal(err)
	}
	w.m.SetHomes(LinuxHomes{Dir: home, Lookup: func(n string) (string, bool) { return filepath.Join(home, n), n == "alex" }})
	w.m.SetOwner(func(string) (int, int, bool) { return 1000, 1000, true })

	var mu sync.Mutex
	var changed []string
	old := lchownIn
	lchownIn = func(r *os.Root, name string, uid, gid int) error {
		fi, err := r.Lstat(name)
		if err == nil {
			mu.Lock()
			changed = append(changed, fi.Name())
			mu.Unlock()
		}
		return nil
	}
	t.Cleanup(func() { lchownIn = old })

	w.m.Own(HomeRootID("alex"), "/Docs", "Linked", "Linked/shadow", "../etc", "Docs/../../../etc")
	sort.Strings(changed)
	want := []string{"Docs", "Linked", "a.txt", "b.txt", "evil", "sub"}
	if len(changed) != len(want) {
		t.Fatalf("changed %v, want %v", changed, want)
	}
	for i := range want {
		if changed[i] != want[i] {
			t.Fatalf("changed %v, want %v", changed, want)
		}
	}
	// "shadow" (through the links) was never touched.
}

func TestEnsureHomeFolderRefusesLinks(t *testing.T) {
	w := newWorld(t)
	base := t.TempDir()
	home := filepath.Join(base, "home")
	outside := filepath.Join(base, "elsewhere")
	for _, d := range []string{filepath.Join(home, "alex"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(home, "alex", "Photos")); err != nil {
		t.Skip("symlinks not available here:", err)
	}
	w.m.SetHomes(LinuxHomes{Dir: home, Lookup: func(n string) (string, bool) { return filepath.Join(home, n), n == "alex" }})
	if err := w.m.EnsureHomeFolder("alex", "Photos"); err == nil {
		t.Error("a link where Photos should be was accepted")
	}
	if err := w.m.EnsureHomeFolder("alex", "Music"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(home, "alex", "Music")); err != nil || !fi.IsDir() {
		t.Errorf("Music not made: %v", err)
	}
}
