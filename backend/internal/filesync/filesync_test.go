package filesync_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"alfaos/alfad/internal/filesync"
	"alfaos/alfad/internal/store"
)

// side is one server of a pair: its store (index) and its folder.
type side struct {
	id, name string
	st       *store.Store
	idx      *store.FileIndex
	loc      filesync.Location
	cursor   int64 // how far it has read the other's index
	acked    int64 // how far the other has read its index
	fetches  int
}

func newSide(t *testing.T, id, name string) *side {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &side{id: id, name: name, st: st, idx: st.FileIndex(), loc: filesync.Location{ID: "drive", Path: t.TempDir()}}
}

func (s *side) write(t *testing.T, rel, body string) {
	t.Helper()
	p := filepath.Join(s.loc.Path, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// A distinct time, as a later edit would have.
	mt := time.Now().Add(time.Duration(len(body)) * time.Millisecond)
	_ = os.Chtimes(p, mt, mt)
}

func (s *side) read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(s.loc.Path, filepath.FromSlash(rel)))
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

func (s *side) scan(t *testing.T) {
	t.Helper()
	if _, err := filesync.Scan(context.Background(), s.idx, s.loc); err != nil {
		t.Fatal(err)
	}
}

// pull: to reads from's index and applies it, as the failover pull does.
func pull(t *testing.T, from, to *side) filesync.ApplyResult {
	t.Helper()
	ctx := context.Background()
	to.acked = from.cursor // the sender says how far it has read the reader's index
	from.scan(t)
	to.scan(t)
	changes, next, _, err := from.idx.Changes(ctx, to.cursor, []string{"drive"}, from.id, to.id, 0)
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]filesync.Entry, len(changes))
	for i, c := range changes {
		entries[i] = c.Entry
	}
	res, err := filesync.ApplyChanges(ctx, to.idx, to.loc, entries, filesync.Apply{
		Self: to.id, SelfName: to.name, Peer: from.id, Acked: to.acked,
		Fetch: func(_ context.Context, loc, rel string, offset int64) (io.ReadCloser, error) {
			from.fetches++
			p, err := filesync.Join(from.loc.Path, rel)
			if err != nil {
				return nil, err
			}
			f, err := os.Open(p)
			if err != nil {
				return nil, err
			}
			if _, err := f.Seek(offset, io.SeekStart); err != nil {
				f.Close()
				return nil, err
			}
			return f, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	to.cursor = next
	return res
}

func TestFileSyncCreateModifyDelete(t *testing.T) {
	a, b := newSide(t, "nc-a", "Alpha"), newSide(t, "nc-b", "Bravo")
	a.write(t, "docs/one.txt", "hello")
	a.write(t, "two.bin", strings.Repeat("x", 100000))
	if res := pull(t, a, b); res.Applied != 2 {
		t.Fatalf("applied %+v", res)
	}
	if b.read(t, "docs/one.txt") != "hello" || len(b.read(t, "two.bin")) != 100000 {
		t.Fatal("files didn't arrive")
	}
	// Same times on both sides (so neither rescans it as changed).
	fa, _ := os.Stat(filepath.Join(a.loc.Path, "docs", "one.txt"))
	fb, _ := os.Stat(filepath.Join(b.loc.Path, "docs", "one.txt"))
	if !fa.ModTime().Equal(fb.ModTime()) {
		t.Fatalf("times differ: %v %v", fa.ModTime(), fb.ModTime())
	}
	// Nothing echoes back.
	if res := pull(t, b, a); res.Applied != 0 || res.Conflicts != 0 {
		t.Fatalf("echo: %+v", res)
	}

	// Modify on B, it reaches A.
	b.write(t, "docs/one.txt", "hello again")
	pull(t, b, a)
	if a.read(t, "docs/one.txt") != "hello again" {
		t.Fatalf("modify: %q", a.read(t, "docs/one.txt"))
	}

	// Delete on A: B's copy goes to its recycle bin, not away.
	if err := os.Remove(filepath.Join(a.loc.Path, "two.bin")); err != nil {
		t.Fatal(err)
	}
	pull(t, b, a) // A has read B's latest, so the deletion isn't a conflict
	pull(t, a, b)
	if b.read(t, "two.bin") != "<missing>" {
		t.Fatal("delete didn't propagate")
	}
	matches, _ := filepath.Glob(filepath.Join(b.loc.Path, filesync.TrashDir, "*_two.bin"))
	if len(matches) != 1 {
		t.Fatalf("not in the trash: %v", matches)
	}
	// The recycle bin and partial downloads never sync.
	if res := pull(t, b, a); res.Applied != 0 {
		t.Fatalf("trash synced: %+v", res)
	}
}

func TestFileSyncRenameUsesLocalCopy(t *testing.T) {
	a, b := newSide(t, "nc-a", "Alpha"), newSide(t, "nc-b", "Bravo")
	a.write(t, "big.iso", strings.Repeat("z", 50000))
	pull(t, a, b)
	before := a.fetches
	if err := os.Rename(filepath.Join(a.loc.Path, "big.iso"), filepath.Join(a.loc.Path, "moved.iso")); err != nil {
		t.Fatal(err)
	}
	pull(t, b, a)
	pull(t, a, b)
	if b.read(t, "big.iso") != "<missing>" || len(b.read(t, "moved.iso")) != 50000 {
		t.Fatal("rename didn't sync")
	}
	if a.fetches != before {
		t.Fatalf("a rename downloaded the file again (%d fetches)", a.fetches-before)
	}
}

func TestFileSyncConflictCopies(t *testing.T) {
	a, b := newSide(t, "nc-a", "Alpha"), newSide(t, "nc-b", "Bravo")
	a.write(t, "notes/plan.md", "v1")
	pull(t, a, b)
	pull(t, b, a)
	// Both edit before syncing. B's clock runs ahead (it edits twice).
	a.write(t, "notes/plan.md", "alpha's edit")
	a.scan(t)
	b.write(t, "notes/plan.md", "bravo edit 1")
	b.scan(t)
	b.write(t, "notes/plan.md", "bravo's second edit")
	b.scan(t)
	resA := pull(t, b, a)
	pull(t, a, b)
	pull(t, b, a)
	if resA.Conflicts == 0 {
		t.Fatalf("no conflict on A: %+v", resA)
	}
	for _, s := range []*side{a, b} {
		if got := s.read(t, "notes/plan.md"); got != "bravo's second edit" {
			t.Fatalf("%s has %q", s.name, got)
		}
		if got := s.read(t, "notes/plan (conflict from Alpha).md"); got != "alpha's edit" {
			t.Fatalf("%s's conflict copy: %q", s.name, got)
		}
	}
}

func TestFileSyncResumesPartialDownload(t *testing.T) {
	a, b := newSide(t, "nc-a", "Alpha"), newSide(t, "nc-b", "Bravo")
	body := strings.Repeat("0123456789", 3000)
	a.write(t, "f.dat", body)
	a.scan(t)
	e, err := a.idx.Entry(context.Background(), "drive", "f.dat")
	if err != nil {
		t.Fatal(err)
	}
	// Half of it arrived before the connection dropped.
	if err := os.MkdirAll(filepath.Join(b.loc.Path, filesync.PartialDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.loc.Path, filesync.PartialDir, e.Hash+".part"), []byte(body[:15000]), 0o600); err != nil {
		t.Fatal(err)
	}
	var asked int64 = -1
	_, err = filesync.ApplyChanges(context.Background(), b.idx, b.loc, []filesync.Entry{*e}, filesync.Apply{
		Self: "nc-b", SelfName: "Bravo", Peer: "nc-a",
		Fetch: func(_ context.Context, _, _ string, offset int64) (io.ReadCloser, error) {
			asked = offset
			return io.NopCloser(bytes.NewReader([]byte(body[offset:]))), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if asked != 15000 || b.read(t, "f.dat") != body {
		t.Fatalf("resume from %d", asked)
	}
	// A download that doesn't match its checksum never replaces the file.
	e.Hash = strings.Repeat("ab", 32)
	e.Lamport += 10
	e.Path = "g.dat"
	_, err = filesync.ApplyChanges(context.Background(), b.idx, b.loc, []filesync.Entry{*e}, filesync.Apply{
		Self: "nc-b", SelfName: "Bravo", Peer: "nc-a",
		Fetch: func(context.Context, string, string, int64) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(body)), nil
		},
	})
	if err == nil || b.read(t, "g.dat") != "<missing>" {
		t.Fatalf("bad checksum accepted: %v", err)
	}
}

func TestFileSyncPaths(t *testing.T) {
	for _, p := range []string{"../x", "/etc/passwd", "a/../../b", "", ".", "a\\b", "a//b"} {
		if filesync.ValidPath(p) {
			t.Errorf("%q accepted", p)
		}
	}
	for _, p := range []string{"a", "a/b.txt", ".hidden", "x y/z"} {
		if !filesync.ValidPath(p) {
			t.Errorf("%q refused", p)
		}
	}
	if got := filesync.ConflictName("dir/report.final.pdf", "Bravo"); got != "dir/report.final (conflict from Bravo).pdf" {
		t.Errorf("conflict name %q", got)
	}
	if got := filesync.ConflictName(".bashrc", "A/B"); got != ".bashrc (conflict from A-B)" {
		t.Errorf("conflict name %q", got)
	}
	// A location that went missing (an unplugged disk) isn't "everything deleted".
	a := newSide(t, "nc-a", "Alpha")
	a.write(t, "keep.txt", "x")
	a.scan(t)
	a.loc.Path = filepath.Join(a.loc.Path, "gone")
	if _, err := filesync.Scan(context.Background(), a.idx, a.loc); err == nil {
		t.Fatal("scanned a missing folder")
	}
	if e, _ := a.idx.Entry(context.Background(), "drive", "keep.txt"); e == nil || e.Deleted {
		t.Fatal("a missing folder deleted its files")
	}
}

func TestLimiter(t *testing.T) {
	if filesync.NewLimiter(0) != nil {
		t.Fatal("no limit should be nil")
	}
	l := filesync.NewLimiter(200 << 10) // 200 KiB/s
	start := time.Now()
	n, _ := io.Copy(io.Discard, l.Reader(bytes.NewReader(make([]byte, 400<<10))))
	if n != 400<<10 {
		t.Fatal(n)
	}
	// A second's worth may be saved up front; the rest waits.
	if d := time.Since(start); d < 800*time.Millisecond {
		t.Fatalf("400 KiB at 200 KiB/s took %v", d)
	}
}
