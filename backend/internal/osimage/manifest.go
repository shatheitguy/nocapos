package osimage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ManifestAsset is the release asset that describes a release's system
// images and update bundles (made by os/scripts/manifest.py).
const ManifestAsset = "nocapos-os.json"

// ReleasesAPI lists NoCapOS releases. Replaced in tests.
var ReleasesAPI = "https://api.github.com/repos/shatheitguy/nocapos/releases?per_page=30"

// Manifest is nocapos-os.json.
type Manifest struct {
	Version   string    `json:"version"`
	Channel   string    `json:"channel"` // stable or beta
	Published time.Time `json:"published"`
	// Security: a security refresh (Debian fixes only, NoCapOS unchanged).
	Security bool `json:"security"`
	// MinFrom: the oldest image version that may update straight to this one.
	MinFrom string `json:"min_from,omitempty"`
	// Snapshot: the Debian snapshot the images are built from.
	Snapshot string `json:"snapshot,omitempty"`
	Notes    string `json:"notes,omitempty"`
	// Fixes: the Debian security fixes in this release, in plain words.
	Fixes   []Fix             `json:"fixes,omitempty"`
	Targets map[string]Target `json:"targets"`
	// Filled in when fetched (not in the file).
	ReleaseURL string `json:"release_url,omitempty"`
	Tag        string `json:"tag,omitempty"`
}

// Fix is one package's security fix.
type Fix struct {
	Package string   `json:"package"`
	From    string   `json:"from,omitempty"`
	To      string   `json:"to"`
	CVEs    []string `json:"cves,omitempty"`
	Summary string   `json:"summary,omitempty"`
}

// Target is one target's files in a release.
type Target struct {
	Bundle     string `json:"bundle"`      // asset name of the update bundle
	URL        string `json:"url"`         // download URL of the update bundle
	Size       int64  `json:"size"`        // bytes
	SHA256     string `json:"sha256"`      // of the whole bundle file
	BundleHash string `json:"bundle_hash"` // Rugix bundle hash (rugix-ctrl --bundle-hash)
	Signed     bool   `json:"signed"`
	Image      string `json:"image,omitempty"` // flashable image asset (fresh installs)
	ImageSize  int64  `json:"image_size,omitempty"`
}

type ghRelease struct {
	Tag        string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	HTMLURL    string    `json:"html_url"`
	Published  time.Time `json:"published_at"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// HTTPClient is used for every request. Replaced in tests.
var HTTPClient = &http.Client{Timeout: 30 * time.Second}

// ErrNoRelease: no release of the channel has system images yet.
var ErrNoRelease = errors.New("no NoCapOS release with system images was found")

// Latest finds the newest release on channel that has a manifest with
// target in it. Stable sees only full releases; beta sees pre-releases too.
func Latest(ctx context.Context, channel, target, userAgent string) (*Manifest, error) {
	var rels []ghRelease
	if err := getJSON(ctx, ReleasesAPI, userAgent, 4<<20, &rels); err != nil {
		return nil, fmt.Errorf("checking GitHub releases: %w", err)
	}
	type cand struct {
		rel ghRelease
		url string
		v   Version
	}
	var cands []cand
	for _, r := range rels {
		if r.Draft || (r.Prerelease && channel != ChannelBeta) {
			continue
		}
		v, ok := ParseVersion(r.Tag)
		if !ok {
			continue
		}
		for _, a := range r.Assets {
			if a.Name == ManifestAsset && strings.HasPrefix(a.URL, "https://github.com/") {
				cands = append(cands, cand{r, a.URL, v})
			}
		}
	}
	// Newest first; the first whose manifest has this target wins.
	for len(cands) > 0 {
		best := 0
		for i := range cands {
			if cands[i].v.Compare(cands[best].v) > 0 {
				best = i
			}
		}
		c := cands[best]
		cands = append(cands[:best], cands[best+1:]...)
		var m Manifest
		if err := getJSON(ctx, c.url, userAgent, 1<<20, &m); err != nil {
			return nil, fmt.Errorf("reading the release manifest: %w", err)
		}
		if _, ok := m.Targets[target]; !ok {
			continue
		}
		m.Tag = c.rel.Tag
		if strings.HasPrefix(c.rel.HTMLURL, "https://github.com/") {
			m.ReleaseURL = c.rel.HTMLURL
		}
		if m.Version == "" {
			m.Version = strings.TrimPrefix(c.rel.Tag, "v")
		}
		if m.Published.IsZero() {
			m.Published = c.rel.Published
		}
		return &m, nil
	}
	return nil, ErrNoRelease
}

func getJSON(ctx context.Context, url, userAgent string, limit int64, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json, application/json;q=0.9, */*;q=0.1")
	req.Header.Set("User-Agent", userAgent)
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, limit)).Decode(out)
}

// Check validates a manifest's entry for target before it is installed.
func (m *Manifest) Check(target, running string) (Target, error) {
	t, ok := m.Targets[target]
	if !ok {
		return Target{}, fmt.Errorf("release %s has no system image for this machine (%s)", m.Version, target)
	}
	if !strings.HasPrefix(t.URL, "https://github.com/") && !strings.HasPrefix(t.URL, "https://objects.githubusercontent.com/") {
		return Target{}, fmt.Errorf("release %s's update comes from an unexpected address", m.Version)
	}
	if !validBundleHash(t.BundleHash) {
		return Target{}, fmt.Errorf("release %s's update has no valid bundle hash", m.Version)
	}
	if m.MinFrom != "" && running != "" && Newer(m.MinFrom, running) {
		return Target{}, fmt.Errorf("NoCapOS %s can't update straight to %s: install %s first", running, m.Version, m.MinFrom)
	}
	return t, nil
}

// validBundleHash accepts Rugix's "sha512-256:<hex>" / "sha256:<hex>".
func validBundleHash(h string) bool {
	algo, hexPart, ok := strings.Cut(h, ":")
	if !ok || (algo != "sha512-256" && algo != "sha256" && algo != "sha512") || len(hexPart) < 64 {
		return false
	}
	for _, c := range hexPart {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
