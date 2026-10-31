package nativeapps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Flathub's public API (the same one flathub.org's own site uses):
//
//	POST {base}/api/v2/search           {"query": "...", "hits_per_page": n}
//	GET  {base}/api/v2/appstream/{id}   an app's metadata
const flathubBase = "https://flathub.org"

const (
	searchTTL  = 5 * time.Minute
	detailsTTL = 30 * time.Minute
	remoteTTL  = 30 * time.Minute
	cacheMax   = 300
	searchHits = 40
	iconMax    = 1 << 20
)

// Catalog searches Flathub, with a short cache, and falls back to
// `flatpak remote-ls` when the API can't be reached.
type Catalog struct {
	Base   string
	Client *http.Client
	// RemoteLs lists the remote with the flatpak CLI (nil = no fallback).
	RemoteLs func(ctx context.Context) (string, error)

	mu      sync.Mutex
	search  map[string]cached[[]App]
	details map[string]cached[*Details]
	remote  cached[[]App]
	icons   map[string]cached[Icon]
}

type cached[T any] struct {
	v  T
	at time.Time
}

// Icon is an app icon's bytes.
type Icon struct {
	Type string
	Data []byte
}

func NewCatalog(remoteLs func(ctx context.Context) (string, error)) *Catalog {
	client := &http.Client{
		Timeout: 12 * time.Second,
		// Never wander off Flathub.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 || !flathubHost(req.URL) {
				return errors.New("redirect off Flathub refused")
			}
			return nil
		},
	}
	return &Catalog{
		Base: flathubBase, Client: client, RemoteLs: remoteLs,
		search: map[string]cached[[]App]{}, details: map[string]cached[*Details]{}, icons: map[string]cached[Icon]{},
	}
}

// ErrOffline: neither Flathub nor the local catalogue answered.
var ErrOffline = errors.New("Couldn't reach Flathub right now. Check the server's internet and try again in a sec")

type searchHit struct {
	AppID     string `json:"app_id"`
	Name      string `json:"name"`
	Summary   string `json:"summary"`
	Icon      string `json:"icon"`
	Developer string `json:"developer_name"`
	Type      string `json:"type"`
}

// Search finds apps by text.
func (c *Catalog) Search(ctx context.Context, q string) ([]App, error) {
	q = strings.TrimSpace(q)
	if len(q) > 100 {
		q = q[:100]
	}
	key := strings.ToLower(q)
	c.mu.Lock()
	if hit, ok := c.search[key]; ok && time.Since(hit.at) < searchTTL {
		c.mu.Unlock()
		return hit.v, nil
	}
	c.mu.Unlock()

	apps, err := c.apiSearch(ctx, q)
	if err != nil {
		local, lerr := c.localSearch(ctx, q)
		if lerr != nil {
			return nil, ErrOffline
		}
		return local, nil
	}
	c.mu.Lock()
	trim(c.search)
	c.search[key] = cached[[]App]{apps, time.Now()}
	c.mu.Unlock()
	return apps, nil
}

func (c *Catalog) apiSearch(ctx context.Context, q string) ([]App, error) {
	body, _ := json.Marshal(map[string]any{"query": q, "hits_per_page": searchHits, "page": 1})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+"/api/v2/search", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	var res struct {
		Hits []searchHit `json:"hits"`
	}
	if err := c.doJSON(req, &res); err != nil {
		return nil, err
	}
	apps := make([]App, 0, len(res.Hits))
	for _, h := range res.Hits {
		if !ValidID(h.AppID) || (h.Type != "" && h.Type != "desktop-application") {
			continue
		}
		apps = append(apps, App{ID: h.AppID, Name: orID(h.Name, h.AppID), Summary: h.Summary, Icon: safeIconURL(h.Icon), Developer: h.Developer})
	}
	return apps, nil
}

func (c *Catalog) localSearch(ctx context.Context, q string) ([]App, error) {
	all, err := c.remoteList(ctx)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(q)
	out := []App{}
	for _, a := range all {
		if needle == "" || strings.Contains(strings.ToLower(a.ID+" "+a.Name+" "+a.Summary), needle) {
			out = append(out, a)
			if len(out) >= searchHits {
				break
			}
		}
	}
	return out, nil
}

func (c *Catalog) remoteList(ctx context.Context) ([]App, error) {
	c.mu.Lock()
	if c.remote.v != nil && time.Since(c.remote.at) < remoteTTL {
		defer c.mu.Unlock()
		return c.remote.v, nil
	}
	c.mu.Unlock()
	if c.RemoteLs == nil {
		return nil, ErrOffline
	}
	out, err := c.RemoteLs(ctx)
	if err != nil {
		return nil, err
	}
	apps := ParseRemoteLs(out)
	c.mu.Lock()
	c.remote = cached[[]App]{apps, time.Now()}
	c.mu.Unlock()
	return apps, nil
}

type appstream struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Developer   string `json:"developer_name"`
	License     string `json:"project_license"`
	URLs        struct {
		Homepage string `json:"homepage"`
	} `json:"urls"`
	Screenshots []struct {
		Sizes []struct {
			Src    string `json:"src"`
			Width  string `json:"width"`
			Height string `json:"height"`
		} `json:"sizes"`
	} `json:"screenshots"`
}

// Details is an app's page. Without Flathub it falls back to the local
// catalogue's name and summary.
func (c *Catalog) Details(ctx context.Context, id string) (*Details, error) {
	if !ValidID(id) {
		return nil, errors.New("not a valid app id")
	}
	c.mu.Lock()
	if hit, ok := c.details[id]; ok && time.Since(hit.at) < detailsTTL {
		c.mu.Unlock()
		d := *hit.v
		return &d, nil
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/api/v2/appstream/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	var a appstream
	if err := c.doJSON(req, &a); err != nil {
		if all, lerr := c.remoteList(ctx); lerr == nil {
			for _, x := range all {
				if x.ID == id {
					return &Details{App: x}, nil
				}
			}
		}
		for _, p := range Popular {
			if p.ID == id {
				return &Details{App: p}, nil
			}
		}
		if errors.Is(err, errNotFound) {
			return nil, errNotFound
		}
		return nil, ErrOffline
	}
	d := &Details{
		App:         App{ID: id, Name: orID(a.Name, id), Summary: a.Summary, Icon: safeIconURL(a.Icon), Developer: a.Developer},
		Description: plainText(a.Description),
		Homepage:    httpsURL(a.URLs.Homepage),
		License:     a.License,
	}
	for _, s := range a.Screenshots {
		if src := screenshotURL(s.Sizes); src != "" {
			d.Screenshots = append(d.Screenshots, src)
		}
		if len(d.Screenshots) >= 6 {
			break
		}
	}
	c.mu.Lock()
	trim(c.details)
	c.details[id] = cached[*Details]{d, time.Now()}
	c.mu.Unlock()
	out := *d
	return &out, nil
}

// Enrich fills in icons and fresher names for a list (the Popular page),
// fetching details in parallel. Apps Flathub doesn't know keep their text.
func (c *Catalog) Enrich(ctx context.Context, apps []App) []App {
	out := make([]App, len(apps))
	copy(out, apps)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i := range out {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			d, err := c.Details(ctx, out[i].ID)
			if err != nil {
				return
			}
			if d.Icon != "" {
				out[i].Icon = d.Icon
			}
			if d.Name != "" && d.Name != d.ID {
				out[i].Name = d.Name
			}
			if d.Summary != "" {
				out[i].Summary = d.Summary
			}
			if d.Developer != "" {
				out[i].Developer = d.Developer
			}
		}(i)
	}
	wg.Wait()
	return out
}

// RemoteIcon fetches an app's icon from Flathub (its details give the URL).
func (c *Catalog) RemoteIcon(ctx context.Context, id string) (Icon, error) {
	c.mu.Lock()
	if hit, ok := c.icons[id]; ok && time.Since(hit.at) < detailsTTL {
		c.mu.Unlock()
		return hit.v, nil
	}
	c.mu.Unlock()
	d, err := c.Details(ctx, id)
	if err != nil {
		return Icon{}, err
	}
	if d.Icon == "" {
		return Icon{}, errNotFound
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.Icon, nil)
	if err != nil {
		return Icon{}, err
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return Icon{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Icon{}, fmt.Errorf("icon: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, iconMax+1))
	if err != nil || len(data) > iconMax {
		return Icon{}, errors.New("icon too large")
	}
	ic := Icon{Type: iconType(d.Icon, data), Data: data}
	if ic.Type == "" {
		return Icon{}, errors.New("not an image")
	}
	c.mu.Lock()
	trim(c.icons)
	c.icons[id] = cached[Icon]{ic, time.Now()}
	c.mu.Unlock()
	return ic, nil
}

var errNotFound = errors.New("Flathub doesn't have that app")

func (c *Catalog) doJSON(req *http.Request, v any) error {
	req.Header.Set("User-Agent", "NoCapOS")
	resp, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("flathub: HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v)
}

func trim[T any](m map[string]cached[T]) {
	if len(m) < cacheMax {
		return
	}
	var oldK string
	var oldT time.Time
	for k, v := range m {
		if oldK == "" || v.at.Before(oldT) {
			oldK, oldT = k, v.at
		}
	}
	delete(m, oldK)
}

func orID(name, id string) string {
	if strings.TrimSpace(name) == "" {
		return id
	}
	return name
}

// Icons and screenshots are only ever fetched from Flathub's own hosts.
func flathubHost(u *url.URL) bool {
	return u.Scheme == "https" && (u.Host == "dl.flathub.org" || u.Host == "flathub.org")
}

func safeIconURL(s string) string {
	u, err := url.Parse(s)
	if err != nil || !flathubHost(u) {
		return ""
	}
	return u.String()
}

func screenshotURL(sizes []struct {
	Src    string `json:"src"`
	Width  string `json:"width"`
	Height string `json:"height"`
}) string {
	// A mid-sized one when there is one (the originals can be huge).
	best := ""
	for _, s := range sizes {
		u, err := url.Parse(s.Src)
		if err != nil || !flathubHost(u) {
			continue
		}
		if strings.Contains(s.Src, "1248x702") {
			return u.String()
		}
		if best == "" {
			best = u.String()
		}
	}
	return best
}

func httpsURL(s string) string {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	return u.String()
}

var (
	tagRe    = regexp.MustCompile(`<[^>]*>`)
	blockRe  = regexp.MustCompile(`(?i)</?(p|li|ul|ol|br)\b[^>]*>`)
	spacesRe = regexp.MustCompile(`[ \t]+`)
	blanksRe = regexp.MustCompile(`\n{3,}`)
)

// plainText turns AppStream's small HTML subset into text.
func plainText(s string) string {
	s = blockRe.ReplaceAllStringFunc(s, func(t string) string {
		if strings.HasPrefix(strings.ToLower(t), "<li") {
			return "\n• "
		}
		return "\n"
	})
	s = html.UnescapeString(tagRe.ReplaceAllString(s, ""))
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(spacesRe.ReplaceAllString(l, " "))
	}
	s = strings.Join(lines, "\n")
	s = blanksRe.ReplaceAllString(s, "\n\n")
	s = strings.ReplaceAll(s, "\n\n• ", "\n• ") // list items sit together
	return strings.TrimSpace(s)
}

func iconType(name string, data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG")):
		return "image/png"
	case strings.HasSuffix(name, ".svg") || bytes.Contains(data[:min(len(data), 512)], []byte("<svg")):
		return "image/svg+xml"
	}
	return ""
}
