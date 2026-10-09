package files

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIndexUnderAndRemove(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"Photos/a.jpg", "Photos/trip/b.jpg", "Photos/notes.txt", "Docs/c.jpg"} {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	svc, err := New([]Root{{ID: "d", Name: "D", Path: dir}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	defer close(done)
	ix := NewIndex(svc, done)
	for i := 0; i < 100; i++ {
		if n, building, _ := ix.Stats(); n > 0 && !building {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	jpg := func(n string) bool { return strings.HasSuffix(n, ".jpg") }
	paths := func() (out []string) {
		for _, h := range ix.Under("Photos", jpg) {
			out = append(out, h.Path)
		}
		return out
	}
	if got := paths(); len(got) != 2 {
		t.Fatalf("Under = %v, want the two jpgs in Photos", got)
	}
	ix.Remove("d", []string{"Photos/trip"})
	if got := paths(); len(got) != 1 || got[0] != "/Photos/a.jpg" {
		t.Fatalf("after removing the folder: %v", got)
	}
	var nilIndex *Index
	nilIndex.Remove("d", []string{"x"}) // must not panic
}
