package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"alfaos/alfad/internal/spaces"
)

// NoCap Desktop's file picker asks where picked files are on this machine:
// only on the desktop listener, only for places the person may read.
func TestFilesLocalPaths(t *testing.T) {
	w := newSpacesWorld(t)
	s := w.s
	media := spaces.ShareRootID(w.media)
	secret := spaces.ShareRootID(w.secret)
	desk := func(rw http.ResponseWriter, r *http.Request) {
		MarkLocalDesktop(http.HandlerFunc(s.filesLocal)).ServeHTTP(rw, r)
	}

	// From the network: not there at all.
	if rec := w.call(t, "alex", s.filesLocal, "POST", "/api/v1/files/local", map[string]any{"root": media, "paths": []string{"/song.txt"}}); rec.Code != http.StatusNotFound {
		t.Fatalf("off the desktop listener = %d, want 404", rec.Code)
	}

	rec := w.call(t, "alex", desk, "POST", "/api/v1/files/local", map[string]any{"root": media, "paths": []string{"/song.txt", "/missing.txt", "/../Secret/plan.txt"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("local paths = %d (%s)", rec.Code, rec.Body)
	}
	var out struct {
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Paths) != 3 {
		t.Fatalf("answer: %s", rec.Body)
	}
	if !strings.HasSuffix(filepath.ToSlash(out.Paths[0]), "/Media/song.txt") {
		t.Errorf("song.txt is at %q", out.Paths[0])
	}
	// Missing files, and ".." (cleaned to a path inside Media), have no place.
	if out.Paths[1] != "" || out.Paths[2] != "" {
		t.Errorf("missing or climbing paths resolved: %q", out.Paths[1:])
	}

	// A shared folder alex can't open: refused like every other file call.
	if rec := w.call(t, "alex", desk, "POST", "/api/v1/files/local", map[string]any{"root": secret, "paths": []string{"/plan.txt"}}); rec.Code != http.StatusForbidden {
		t.Errorf("secret = %d, want 403", rec.Code)
	}
	if rec := w.call(t, "alex", desk, "POST", "/api/v1/files/local", map[string]any{"root": media, "paths": []string{}}); rec.Code != http.StatusBadRequest {
		t.Errorf("no paths = %d, want 400", rec.Code)
	}
}
