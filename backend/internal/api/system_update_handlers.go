package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Settings → General → Software Update: compares this build with the latest
// NoCapOS release on GitHub (cached for an hour; ?refresh=1 checks again).

const releasesURL = "https://api.github.com/repos/shatheitguy/nocapos/releases/latest"

// UpdateCommand reinstalls the latest release over this one, keeping settings and data.
const updateCommand = "curl -fsSL https://github.com/shatheitguy/nocapos/releases/latest/download/install.sh | sudo bash"

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
	updateCache.Lock()
	defer updateCache.Unlock()
	if c := updateCache.info; c != nil && r.URL.Query().Get("refresh") == "" && time.Since(c.Checked) < time.Hour && c.Error == "" {
		writeJSON(w, http.StatusOK, c)
		return
	}
	info := checkRelease(r.Context(), s.Version)
	updateCache.info = &info
	writeJSON(w, http.StatusOK, info)
}

func checkRelease(ctx context.Context, current string) updateInfo {
	info := updateInfo{Current: current, Checked: time.Now(), Command: updateCommand}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "NoCapOS/"+current)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		info.Error = "Couldn't reach GitHub to check for updates. Check the internet connection and try again."
		return info
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		info.Error = "GitHub didn't answer the update check (HTTP " + strconv.Itoa(resp.StatusCode) + "). Try again later."
		return info
	}
	var rel struct {
		Tag       string    `json:"tag_name"`
		Name      string    `json:"name"`
		Body      string    `json:"body"`
		URL       string    `json:"html_url"`
		Published time.Time `json:"published_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil || rel.Tag == "" {
		info.Error = "The update check returned something unexpected. Try again later."
		return info
	}
	info.Latest, info.Name, info.Notes, info.Published = rel.Tag, rel.Name, rel.Body, rel.Published
	if strings.HasPrefix(rel.URL, "https://github.com/") {
		info.URL = rel.URL
	}
	cur, okCur := parseVersion(current)
	latest, okLatest := parseVersion(rel.Tag)
	info.Dev = !okCur
	info.Available = okCur && okLatest && compareVersion(latest, cur) > 0
	return info
}

// parseVersion reads "v1.2.3" / "1.2" (anything after a "-" or "+" is ignored).
func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if v == "" || len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func compareVersion(a, b [3]int) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] > b[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}
