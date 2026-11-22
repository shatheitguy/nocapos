package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"alfaos/alfad/internal/config"
)

// TestFailoverFilesEndToEnd: two paired servers on localhost sync a Files
// location over their HTTPS link: choosing it on one tells the other, new
// and changed files arrive, deletions go to the recycle bin, and the data folder
// only shares stacks and the Shield's settings.
func TestFailoverFilesEndToEnd(t *testing.T) {
	ctx := context.Background()
	a, b := newSquadNode(t), newSquadNode(t)
	for _, n := range []*squadNode{a, b} {
		n.w.s.Config.FileRoots = []config.FileRoot{{ID: "drive", Name: "NoCap Drive", Path: t.TempDir()}}
	}
	boss := pairNodes(t, a, b)
	driveA, driveB := a.w.s.Config.FileRoots[0].Path, b.w.s.Config.FileRoots[0].Path

	rec := squadCall(a.w.s, a.w.s.failoverSetFiles, "PUT", "/", map[string]any{"files": []string{"drive", "nope"}, "limit_kbps": 0}, callOpts{as: boss})
	if rec.Code != http.StatusOK {
		t.Fatalf("set files: %d %s", rec.Code, rec.Body)
	}
	pb, _ := b.w.st.FailoverPair(ctx, a.id)
	if o := parseFailoverOptions(pb.Options); len(o.Files) != 1 || o.Files[0] != "drive" {
		t.Fatalf("B's options: %s", pb.Options)
	}

	write := func(dir, rel, body string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(dir, rel string) string {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return "<missing>"
		}
		return string(b)
	}
	sync := func(n *squadNode) {
		t.Helper()
		if err := n.w.s.failoverFilesOnce(ctx, true); err != nil {
			t.Fatalf("files: %v", err)
		}
	}
	write(driveA, "photos/cat.jpg", "meow")
	write(a.w.s.Config.DataDir, "stacks/web/compose.yaml", "services: {}")
	write(a.w.s.Config.DataDir, "secret.db", "never")
	sync(a) // scans A
	sync(b) // B takes it
	if read(driveB, "photos/cat.jpg") != "meow" {
		t.Fatal("file didn't arrive")
	}
	if read(b.w.s.Config.DataDir, "stacks/web/compose.yaml") != "services: {}" {
		t.Fatal("stack didn't arrive")
	}
	if read(b.w.s.Config.DataDir, "secret.db") != "<missing>" {
		t.Fatal("a data-folder file that isn't config travelled")
	}

	// Changed on B, back to A; deleted on A, into B's recycle bin.
	write(driveB, "photos/cat.jpg", "purr")
	sync(b)
	sync(a)
	if read(driveA, "photos/cat.jpg") != "purr" {
		t.Fatalf("change on B: %q", read(driveA, "photos/cat.jpg"))
	}
	if err := os.Remove(filepath.Join(driveA, "photos", "cat.jpg")); err != nil {
		t.Fatal(err)
	}
	sync(a)
	sync(b)
	if read(driveB, "photos/cat.jpg") != "<missing>" {
		t.Fatal("delete didn't reach B")
	}
	if m, _ := filepath.Glob(filepath.Join(driveB, ".recycle", "*_cat.jpg")); len(m) != 1 {
		t.Fatalf("not in B's recycle bin: %v", m)
	}

	// The status shows the location.
	j := a.failover(t, boss).Pair
	if j.Files == nil || len(j.Files.Locations) == 0 {
		t.Fatalf("no files status: %+v", j)
	}

	// A file request for a location that doesn't sync, or a path out of it, is refused.
	mux := http.NewServeMux()
	failoverPeerRoutes(b.w.s, mux)
	la, _ := a.w.st.ServerLink(ctx, b.id)
	tok, _ := a.w.s.openPeerToken(la)
	for _, q := range []string{"loc=homes&path=x&offset=0", "loc=drive&path=../x&offset=0", "loc=config&path=secret.db&offset=0"} {
		r, _ := http.NewRequest("GET", "/api/v1/failover/peer/files/file?"+q, nil)
		r.Header.Set("Authorization", peerAuthPrefix+tok)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, r)
		if rr.Code == http.StatusOK {
			t.Errorf("%s: served", q)
		}
	}
}
