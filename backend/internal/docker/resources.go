package docker

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Images, volumes and disk usage for the Containers app: sizes, who uses
// what, tag, remove and prune.

// --- environment ---

// EnvEntry is one environment variable as the details page shows it.
type EnvEntry struct {
	Key    string `json:"key"`
	Value  string `json:"value"` // "" when Secret
	Secret bool   `json:"secret"`
}

// secretKeyRe matches variable names that usually hold credentials.
var secretKeyRe = regexp.MustCompile(`(?i)(pass|pwd|secret|token|key|credential|private|auth|salt|cookie|session|cert|signature|dsn)`)

// userinfoRe finds a password inside a URL (scheme://user:password@host).
var userinfoRe = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^/@\s:]*:[^/@\s]+@`)

// SecretEnv reports whether a variable should be masked.
func SecretEnv(key, value string) bool {
	return secretKeyRe.MatchString(key) || userinfoRe.MatchString(value)
}

// MaskEnv turns KEY=value pairs into entries, masking anything that looks
// like a password, token or key (and URLs with a password in them).
func MaskEnv(env []string) []EnvEntry {
	out := make([]EnvEntry, 0, len(env))
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if k == "" {
			continue
		}
		e := EnvEntry{Key: k, Value: v}
		if SecretEnv(k, v) {
			e.Value, e.Secret = "", true
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// --- disk usage ---

// Usage is what Docker's disk usage report says, keyed for quick lookups.
type Usage struct {
	Containers map[string]ContainerSize `json:"containers"` // by id
	Images     map[string]int64         `json:"images"`     // size by image id
	Volumes    map[string]VolumeSize    `json:"volumes"`    // by name
	Time       time.Time                `json:"time"`
}

type ContainerSize struct {
	RW     int64 `json:"rw"`     // what the container wrote
	RootFS int64 `json:"rootfs"` // with its image
}

type VolumeSize struct {
	Size     int64 `json:"size"` // -1 when Docker doesn't know
	RefCount int64 `json:"ref_count"`
}

// DiskUsage asks the engine how much room containers, images and volumes
// take. It can take a few seconds on a busy server; callers cache it.
func (c *Client) DiskUsage(ctx context.Context) (*Usage, error) {
	var raw struct {
		Images []struct {
			ID   string `json:"Id"`
			Size int64
		}
		Containers []struct {
			ID         string `json:"Id"`
			SizeRw     int64
			SizeRootFs int64
		}
		Volumes []struct {
			Name      string
			UsageData *struct {
				Size     int64
				RefCount int64
			}
		}
	}
	if err := c.do(ctx, http.MethodGet, "/system/df", nil, &raw); err != nil {
		return nil, err
	}
	u := &Usage{Containers: map[string]ContainerSize{}, Images: map[string]int64{}, Volumes: map[string]VolumeSize{}, Time: time.Now().UTC()}
	for _, ct := range raw.Containers {
		u.Containers[ct.ID] = ContainerSize{RW: ct.SizeRw, RootFS: ct.SizeRootFs}
	}
	for _, im := range raw.Images {
		u.Images[im.ID] = im.Size
	}
	for _, v := range raw.Volumes {
		vs := VolumeSize{Size: -1}
		if v.UsageData != nil {
			vs = VolumeSize{Size: v.UsageData.Size, RefCount: v.UsageData.RefCount}
		}
		u.Volumes[v.Name] = vs
	}
	return u, nil
}

// --- images ---

var imageRefRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:/@-]{0,254}$`)

// ValidImageRef reports whether ref looks like an image name, name:tag,
// name@digest or id, and can't step out of the API path it goes into.
func ValidImageRef(ref string) bool {
	return imageRefRe.MatchString(ref) && !strings.Contains(ref, "..") && !strings.Contains(ref, "//") && !strings.HasSuffix(ref, "/")
}

// TagImage gives an image another name (repo:tag).
func (c *Client) TagImage(ctx context.Context, source, repo, tag string) error {
	q := url.Values{"repo": {repo}}
	if tag != "" {
		q.Set("tag", tag)
	}
	return c.do(ctx, http.MethodPost, "/images/"+source+"/tag", q, nil)
}

// RemoveImage deletes an image (or one of its names). The engine refuses
// when a container uses it.
func (c *Client) RemoveImage(ctx context.Context, ref string) ([]string, error) {
	var out []map[string]string
	if err := c.do(ctx, http.MethodDelete, "/images/"+ref, nil, &out); err != nil {
		return nil, err
	}
	gone := []string{}
	for _, m := range out {
		for _, v := range m {
			gone = append(gone, v)
		}
	}
	return gone, nil
}

// PruneResult is what a prune removed.
type PruneResult struct {
	Removed   []string `json:"removed"`
	Reclaimed int64    `json:"reclaimed"`
}

// PruneImages removes images no container uses: all of them when all is
// set, otherwise only the untagged leftovers.
func (c *Client) PruneImages(ctx context.Context, all bool) (*PruneResult, error) {
	filters := `{"dangling":["true"]}`
	if all {
		filters = `{"dangling":["false"]}`
	}
	var raw struct {
		ImagesDeleted  []map[string]string
		SpaceReclaimed int64
	}
	if err := c.do(ctx, http.MethodPost, "/images/prune", url.Values{"filters": {filters}}, &raw); err != nil {
		return nil, err
	}
	res := &PruneResult{Removed: []string{}, Reclaimed: raw.SpaceReclaimed}
	for _, m := range raw.ImagesDeleted {
		for _, v := range m {
			res.Removed = append(res.Removed, v)
		}
	}
	return res, nil
}

// --- volumes ---

// VolumeInfo is a named volume with its details.
type VolumeInfo struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Mountpoint string            `json:"mountpoint,omitempty"`
	Created    string            `json:"created,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	Project    string            `json:"project,omitempty"`
}

// VolumeDetails lists the named volumes with their mountpoints and labels.
func (c *Client) VolumeDetails(ctx context.Context) ([]VolumeInfo, error) {
	var out struct {
		Volumes []struct {
			Name       string
			Driver     string
			Mountpoint string
			CreatedAt  string
			Labels     map[string]string
		}
	}
	if err := c.do(ctx, http.MethodGet, "/volumes", nil, &out); err != nil {
		return nil, err
	}
	vols := make([]VolumeInfo, 0, len(out.Volumes))
	for _, v := range out.Volumes {
		vols = append(vols, VolumeInfo{Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint, Created: v.CreatedAt, Labels: v.Labels, Project: v.Labels["com.docker.compose.project"]})
	}
	sort.Slice(vols, func(i, j int) bool { return vols[i].Name < vols[j].Name })
	return vols, nil
}

// DeleteVolume removes a named volume; the engine refuses one in use.
func (c *Client) DeleteVolume(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/volumes/"+url.PathEscape(name), nil, nil)
}

// PruneVolumes removes volumes no container uses (named ones too when all).
func (c *Client) PruneVolumes(ctx context.Context, all bool) (*PruneResult, error) {
	q := url.Values{}
	if all {
		q.Set("filters", `{"all":["true"]}`)
	}
	var raw struct {
		VolumesDeleted []string
		SpaceReclaimed int64
	}
	if err := c.do(ctx, http.MethodPost, "/volumes/prune", q, &raw); err != nil {
		return nil, err
	}
	res := &PruneResult{Removed: raw.VolumesDeleted, Reclaimed: raw.SpaceReclaimed}
	if res.Removed == nil {
		res.Removed = []string{}
	}
	return res, nil
}

// --- networks ---

// DisconnectNetwork takes a container off a network.
func (c *Client) DisconnectNetwork(ctx context.Context, network, container string) error {
	return c.doBody(ctx, http.MethodPost, "/networks/"+url.PathEscape(network)+"/disconnect", nil,
		map[string]any{"Container": container, "Force": false}, nil)
}
