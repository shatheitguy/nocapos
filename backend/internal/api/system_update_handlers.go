package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"alfaos/alfad/internal/release"
)

// Settings → General → Software Update: compares this build with the latest
// NoCapOS release on GitHub (cached for an hour; ?refresh=1 checks again).
// The version-check logic itself lives in internal/release, shared with the
// cap CLI's "cap update".

type updateInfo struct {
	Current   string    `json:"current"`
	Latest    string    `json:"latest,omitempty"`
	Available bool      `json:"available"`
	Dev       bool      `json:"dev"` // a development build: versions can't be compared
	Name      string    `json:"name,omitempty"`
	Notes     string    `json:"notes,omitempty"`
	URL       string    `json:"url,omitempty"`
	Published time.Time `json:"published,omitzero"`
	Checked   time.Time `json:"checked"`
	Command   string    `json:"command"`
	Error     string    `json:"error,omitempty"`
}

var updateCache struct {
	sync.Mutex
	info *updateInfo
}

func (s *Server) systemUpdate(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, releaseFor(s, r, r.URL.Query().Get("refresh") != ""))
}

// checkRelease adapts release.Check's result to this API's wire shape.
func checkRelease(ctx context.Context, current string) updateInfo {
	rel := release.Check(ctx, current)
	return updateInfo{
		Current: rel.Current, Latest: rel.Latest, Available: rel.Available, Dev: rel.Dev,
		Name: rel.Name, Notes: rel.Notes, URL: rel.URL, Published: rel.Published,
		Checked: rel.Checked, Command: release.UpdateCommand, Error: rel.Error,
	}
}
