// Package release checks GitHub for the latest NoCapOS release and compares
// it against the version currently running. Shared by the Settings →
// Software Update check and the cap CLI's "cap update".
package release

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	ReleasesURL   = "https://api.github.com/repos/shatheitguy/nocapos/releases/latest"
	UpdateCommand = "curl -fsSL https://github.com/shatheitguy/nocapos/releases/latest/download/install.sh | sudo bash"
)

// Info is what's known about the latest release relative to Current.
type Info struct {
	Current   string
	Latest    string
	Available bool
	Dev       bool // Current isn't a released version (a dev build): versions can't be compared
	Name      string
	Notes     string
	URL       string
	Published time.Time
	Checked   time.Time
	Error     string // set instead of the fields above when the check itself failed
}

// Check asks GitHub for the latest release and compares it with current.
func Check(ctx context.Context, current string) Info {
	info := Info{Current: current, Checked: time.Now()}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ReleasesURL, nil)
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
	cur, okCur := ParseVersion(current)
	latest, okLatest := ParseVersion(rel.Tag)
	info.Dev = !okCur
	info.Available = okCur && okLatest && CompareVersion(latest, cur) > 0
	return info
}

// ParseVersion reads "v1.2.3" / "1.2" (anything after a "-" or "+" is ignored).
func ParseVersion(v string) ([3]int, bool) {
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

func CompareVersion(a, b [3]int) int {
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
