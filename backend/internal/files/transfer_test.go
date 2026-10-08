package files

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTransferConflictModes(t *testing.T) {
	s, dir := newTestService(t)
	write(t, dir, "a/note.txt", "new")
	write(t, dir, "b/note.txt", "old")
	ctx := context.Background()

	if names, _ := s.Conflicts("t", []string{"a/note.txt"}, "b"); len(names) != 1 || names[0] != "note.txt" {
		t.Fatalf("conflicts = %v", names)
	}

	res, err := s.TransferWith(ctx, "t", []string{"a/note.txt"}, "b", TransferOptions{Conflict: ConflictSkip})
	if err != nil || len(res.Skipped) != 1 || read(t, dir, "b/note.txt") != "old" {
		t.Fatalf("skip: %+v %v", res, err)
	}
	res, err = s.TransferWith(ctx, "t", []string{"a/note.txt"}, "b", TransferOptions{Conflict: ConflictRename})
	if err != nil || res.Paths[0] != "b/note (1).txt" {
		t.Fatalf("keep both: %+v %v", res, err)
	}
	var last Progress
	res, err = s.TransferWith(ctx, "t", []string{"a/note.txt"}, "b", TransferOptions{Conflict: ConflictReplace, OnProgress: func(p Progress) { last = p }})
	if err != nil || read(t, dir, "b/note.txt") != "new" || res.Paths[0] != "b/note.txt" {
		t.Fatalf("replace: %+v %v", res, err)
	}
	if last.BytesDone != 3 || last.BytesTotal != 3 || last.ItemsDone != 1 {
		t.Fatalf("progress = %+v", last)
	}
	// Moving with replace removes the source.
	write(t, dir, "a/note.txt", "newest")
	if _, err := s.TransferWith(ctx, "t", []string{"a/note.txt"}, "b", TransferOptions{Move: true, Conflict: ConflictReplace}); err != nil {
		t.Fatal(err)
	}
	if read(t, dir, "b/note.txt") != "newest" {
		t.Fatal("move+replace didn't replace")
	}
	if _, err := os.Stat(filepath.Join(dir, "a", "note.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("move left the source behind")
	}
	// No temporary files left over.
	entries, _ := os.ReadDir(filepath.Join(dir, "b"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), uploadPrefix) {
			t.Fatalf("temp file left: %s", e.Name())
		}
	}
}

func TestReplaceRespectsProtection(t *testing.T) {
	s, dir := newTestService(t)
	write(t, dir, "src/keep", "x")
	write(t, dir, "dst/keep/inside.txt", "precious")
	s.Protect(filepath.Join(dir, "dst", "keep"))
	if _, err := s.TransferWith(context.Background(), "t", []string{"src/keep"}, "dst", TransferOptions{Conflict: ConflictReplace}); !errors.Is(err, ErrProtected) {
		t.Fatalf("replacing a protected folder = %v", err)
	}
	if read(t, dir, "dst/keep/inside.txt") != "precious" {
		t.Fatal("protected folder was touched")
	}
}

func TestTransferCancelRemovesPartialCopy(t *testing.T) {
	s, dir := newTestService(t)
	big := strings.Repeat("x", 4<<20)
	for i := 0; i < 6; i++ {
		write(t, dir, filepath.ToSlash(filepath.Join("bigdir", "f"+string(rune('a'+i)))), big)
	}
	if err := os.MkdirAll(filepath.Join(dir, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err := s.TransferWith(ctx, "t", []string{"bigdir"}, "out", TransferOptions{OnProgress: func(p Progress) {
		if p.BytesDone > 5<<20 {
			cancel()
		}
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "bigdir")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial copy left behind")
	}
}

func TestJobsRunAndCancel(t *testing.T) {
	s, dir := newTestService(t)
	write(t, dir, "a.txt", "hello")
	if err := os.MkdirAll(filepath.Join(dir, "dest"), 0o755); err != nil {
		t.Fatal(err)
	}
	js := NewJobs(s)
	done := make(chan Job, 1)
	j := js.Start("u1", "t", []string{"a.txt"}, "dest", false, ConflictRename, func(p string) string { return "/" + p }, func(j Job) { done <- j })
	got := <-done
	if got.Status != "done" || got.Result.Paths[0] != "/dest/a.txt" || got.Progress.BytesDone != 5 {
		t.Fatalf("job = %+v", got)
	}
	if _, ok := js.Get(j.ID, "someone-else"); ok {
		t.Fatal("other users must not see the job")
	}
	if list := js.List("u1"); len(list) != 1 {
		t.Fatalf("list = %+v", list)
	}
}

func TestUploadConflict(t *testing.T) {
	s, dir := newTestService(t)
	write(t, dir, "doc.txt", "old")
	if p, err := s.UploadWith("t", ".", "doc.txt", bytes.NewBufferString("new"), ConflictSkip); err != nil || p != "" || read(t, dir, "doc.txt") != "old" {
		t.Fatalf("skip: %q %v", p, err)
	}
	if p, err := s.UploadWith("t", ".", "doc.txt", bytes.NewBufferString("new"), ConflictReplace); err != nil || p != "doc.txt" || read(t, dir, "doc.txt") != "new" {
		t.Fatalf("replace: %q %v", p, err)
	}
	if p, err := s.UploadWith("t", ".", "doc.txt", bytes.NewBufferString("third"), ConflictRename); err != nil || p != "doc (1).txt" {
		t.Fatalf("keep both: %q %v", p, err)
	}
}
