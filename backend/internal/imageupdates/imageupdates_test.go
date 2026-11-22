package imageupdates

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseRef(t *testing.T) {
	cases := []struct {
		in   string
		want Ref
		ok   bool
	}{
		{"nginx", Ref{dockerHub, "library/nginx", "latest"}, true},
		{"nginx:1.27", Ref{dockerHub, "library/nginx", "1.27"}, true},
		{"docker.io/jellyfin/jellyfin:10.9", Ref{dockerHub, "jellyfin/jellyfin", "10.9"}, true},
		{"ghcr.io/org/app", Ref{"ghcr.io", "org/app", "latest"}, true},
		{"localhost:5000/team/x:dev", Ref{"localhost:5000", "team/x", "dev"}, true},
		{"lscr.io/linuxserver/plex:latest", Ref{"lscr.io", "linuxserver/plex", "latest"}, true},
		{"nginx@sha256:0123", Ref{}, false},
		{"", Ref{}, false},
	}
	for _, c := range cases {
		got, ok := ParseRef(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("ParseRef(%q) = %+v, %v; want %+v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// fakeRegistry is a registry that wants an anonymous token, like Docker Hub.
func fakeRegistry(t *testing.T, digests map[string]string, limited *atomic.Bool) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var heads atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			if !strings.HasPrefix(r.URL.Query().Get("scope"), "repository:") {
				http.Error(w, "scope", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"token":"t0k"}`))
		case strings.HasPrefix(r.URL.Path, "/v2/"):
			if r.Method != http.MethodHead {
				t.Errorf("method %s: only HEAD requests (they don't count as pulls)", r.Method)
			}
			if !strings.Contains(r.Header.Get("Accept"), "manifest.list.v2") {
				t.Errorf("Accept misses the multi-platform list: %q", r.Header.Get("Accept"))
			}
			if r.Header.Get("Authorization") != "Bearer t0k" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="fake",scope="repository:any:pull"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			heads.Add(1)
			if limited != nil && limited.Load() {
				w.Header().Set("Retry-After", "7200")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			d, ok := digests[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Docker-Content-Digest", d)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &heads
}

func TestRegistryDigest(t *testing.T) {
	var limited atomic.Bool
	srv, _ := fakeRegistry(t, map[string]string{"/v2/library/nginx/manifests/1.27": "sha256:aaa"}, &limited)
	host := strings.TrimPrefix(srv.URL, "http://")
	g := &Registry{HTTP: srv.Client(), Scheme: "http"}
	ctx := context.Background()

	d, err := g.Digest(ctx, Ref{host, "library/nginx", "1.27"})
	if err != nil || d != "sha256:aaa" {
		t.Fatalf("Digest = %q, %v", d, err)
	}
	if _, err := g.Digest(ctx, Ref{host, "library/nginx", "9.9"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing tag: %v", err)
	}
	limited.Store(true)
	_, err = g.Digest(ctx, Ref{host, "library/nginx", "1.27"})
	var rl *RateLimitError
	if !errors.As(err, &rl) || rl.RetryAfter != 2*time.Hour {
		t.Fatalf("rate limit: %v", err)
	}
}

func TestChallenge(t *testing.T) {
	p := parseChallenge(`Bearer realm="https://auth.example/token",service="registry.example",scope="repository:a/b:pull"`)
	if p["realm"] != "https://auth.example/token" || p["service"] != "registry.example" || p["scope"] != "repository:a/b:pull" {
		t.Fatalf("parsed %v", p)
	}
	if len(parseChallenge("Basic realm=x")) != 0 {
		t.Fatal("Basic is not a token challenge")
	}
}

// A checker against the fake registry, with a clock the test moves.
func TestCheckerEndToEnd(t *testing.T) {
	var limited atomic.Bool
	digests := map[string]string{
		"/v2/library/nginx/manifests/1.27": "sha256:new",
		"/v2/team/db/manifests/16":         "sha256:same",
	}
	srv, heads := fakeRegistry(t, digests, &limited)
	host := strings.TrimPrefix(srv.URL, "http://")
	images := []Image{
		{App: "web", Ref: host + "/library/nginx:1.27", ImageID: "img-nginx"},
		{App: "web", Ref: host + "/team/db:16", ImageID: "img-db"},
		{App: "notes", Ref: host + "/team/db:16", ImageID: "img-db"},
		{App: "pinned", Ref: host + "/library/nginx@sha256:old", ImageID: "img-pinned"},
		{App: "local", Ref: "my-own-build:dev", ImageID: "img-local"},
	}
	local := map[string][]string{
		"img-nginx": {host + "/library/nginx@sha256:old"},
		"img-db":    {host + "/team/db@sha256:same"},
	}
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	var paused []time.Duration
	var found []string
	c := New(
		func(context.Context) ([]Image, error) { return images, nil },
		func(_ context.Context, id string) ([]string, error) { return local[id], nil },
		&Registry{HTTP: srv.Client(), Scheme: "http"},
	)
	c.now = func() time.Time { return now }
	c.sleep = func(_ context.Context, d time.Duration) error { paused = append(paused, d); return nil }
	c.OnFound = func(app string, u []Update) { found = append(found, app+":"+u[0].Digest) }
	ctx := context.Background()

	if err := c.Check(ctx); err != nil {
		t.Fatal(err)
	}
	// One request per reference, spaced out; the pinned and local images are never asked about.
	if heads.Load() != 2 {
		t.Fatalf("%d registry requests, want 2", heads.Load())
	}
	if len(paused) != 1 || paused[0] != 2*time.Second {
		t.Fatalf("pauses %v", paused)
	}
	if !c.Has("web") || c.Has("notes") || c.Has("pinned") || c.Has("local") {
		t.Fatalf("updates: web %v notes %v", c.Updates("web"), c.Updates("notes"))
	}
	if u := c.Updates("web"); len(u) != 1 || u[0].Ref != host+"/library/nginx:1.27" || u[0].Digest != "sha256:new" {
		t.Fatalf("web updates %+v", u)
	}
	if len(found) != 1 || found[0] != "web:sha256:new" {
		t.Fatalf("found %v", found)
	}

	// The same update again: no second notice.
	if err := c.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("told twice: %v", found)
	}

	// The registry says to slow down: the update stays known, and the
	// registry isn't asked again until the wait is over.
	limited.Store(true)
	before := heads.Load()
	if err := c.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if !c.Has("web") {
		t.Fatal("a rate limit must not hide a known update")
	}
	asked := heads.Load() - before
	if err := c.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if heads.Load()-before != asked {
		t.Fatal("asked a registry that is backing off")
	}
	limited.Store(false)
	now = now.Add(3 * time.Hour) // past Retry-After (2 h) and the 30 min floor
	if err := c.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if heads.Load()-before == asked {
		t.Fatal("never asked again after the wait")
	}

	// Updated: the new image is the one running now.
	local["img-nginx"] = []string{host + "/library/nginx@sha256:new"}
	c.Forget("web")
	if err := c.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if c.Has("web") {
		t.Fatal("still flagged after the update")
	}

	// Removed apps are dropped.
	digests["/v2/library/nginx/manifests/1.27"] = "sha256:newer"
	if err := c.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if !c.Has("web") {
		t.Fatal("a newer image again")
	}
	images = images[1:]
	images = append(images[:0], images[1:]...) // web is gone entirely
	if err := c.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if c.Has("web") {
		t.Fatal("a removed app keeps its update")
	}
}

func TestBackoff(t *testing.T) {
	if d := backoff(errors.New("x"), 0); d != 15*time.Minute {
		t.Fatal(d)
	}
	if d := backoff(errors.New("x"), 10); d != 6*time.Hour {
		t.Fatal(d)
	}
	if d := backoff(&RateLimitError{}, 0); d != 30*time.Minute {
		t.Fatal(d)
	}
	if d := backoff(&RateLimitError{RetryAfter: 48 * time.Hour}, 0); d != 48*time.Hour {
		t.Fatal(d)
	}
	if d := backoff(&RateLimitError{}, 9); d != 24*time.Hour {
		t.Fatal(d)
	}
	for i := 0; i < 100; i++ {
		if j := Jitter(6*time.Hour, 0.1); j < 324*time.Minute || j > 396*time.Minute {
			t.Fatal(j)
		}
	}
}
