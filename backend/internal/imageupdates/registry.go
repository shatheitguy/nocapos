// Package imageupdates finds new images for the App Store's Docker apps: it
// asks each app's registry for the digest of the tag the app runs (one HEAD
// request per image, which registries don't count as a pull) and compares it
// with the digest the image was pulled with. Checks run at start and every
// few hours, spaced out, and back off when a registry says to slow down.
package imageupdates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Ref is an image reference split up: the registry host, the repository and
// the tag.
type Ref struct {
	Registry string
	Repo     string
	Tag      string
}

func (r Ref) String() string { return r.Registry + "/" + r.Repo + ":" + r.Tag }

const dockerHub = "registry-1.docker.io"

// ParseRef splits an image reference ("nginx", "ghcr.io/org/app:1.2",
// "localhost:5000/x"). ok is false for an image pinned by digest (it can't
// change) or one that can't be read.
func ParseRef(image string) (Ref, bool) {
	image = strings.TrimSpace(image)
	if image == "" || strings.Contains(image, "@") || strings.ContainsAny(image, " \t") {
		return Ref{}, false
	}
	name, tag := image, "latest"
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		name, tag = image[:i], image[i+1:]
	}
	if name == "" || tag == "" {
		return Ref{}, false
	}
	host := dockerHub
	parts := strings.SplitN(name, "/", 2)
	if len(parts) == 2 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost") {
		host, name = parts[0], parts[1]
	}
	if host == "docker.io" || host == "index.docker.io" {
		host = dockerHub
	}
	if host == dockerHub && !strings.Contains(name, "/") {
		name = "library/" + name
	}
	return Ref{Registry: host, Repo: name, Tag: tag}, true
}

// RateLimitError means the registry asked to slow down.
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("the registry asked to slow down (retry after %s)", e.RetryAfter)
}

// ErrNotFound means the registry doesn't have that tag (any more).
var ErrNotFound = errors.New("tag not found in the registry")

// Registry reads tag digests from image registries, anonymously.
type Registry struct {
	HTTP *http.Client
	// Scheme is "https" (tests use "http").
	Scheme string
}

// NewRegistry is a registry client with a short timeout.
func NewRegistry() *Registry {
	return &Registry{HTTP: &http.Client{Timeout: 20 * time.Second}, Scheme: "https"}
}

// The manifest types a tag can point at: a multi-platform list first (its
// digest is what `docker pull` records), then a single manifest.
var accept = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.docker.distribution.manifest.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
}, ", ")

// Digest returns the digest the registry has for r's tag now.
func (g *Registry) Digest(ctx context.Context, r Ref) (string, error) {
	u := g.Scheme + "://" + r.Registry + "/v2/" + r.Repo + "/manifests/" + url.PathEscape(r.Tag)
	res, err := g.head(ctx, u, "")
	if err != nil {
		return "", err
	}
	if res.StatusCode == http.StatusUnauthorized {
		token, terr := g.token(ctx, res.Header.Get("WWW-Authenticate"), r)
		if terr != nil {
			return "", terr
		}
		if res, err = g.head(ctx, u, token); err != nil {
			return "", err
		}
	}
	switch {
	case res.StatusCode == http.StatusTooManyRequests:
		return "", &RateLimitError{RetryAfter: retryAfter(res.Header.Get("Retry-After"))}
	case res.StatusCode == http.StatusNotFound:
		return "", ErrNotFound
	case res.StatusCode != http.StatusOK:
		return "", fmt.Errorf("%s: the registry answered %d", r, res.StatusCode)
	}
	d := res.Header.Get("Docker-Content-Digest")
	if !strings.HasPrefix(d, "sha256:") {
		return "", fmt.Errorf("%s: the registry sent no digest", r)
	}
	return d, nil
}

func (g *Registry) head(ctx context.Context, u, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := g.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	res.Body.Close()
	return res, nil
}

// token gets an anonymous pull token from the registry's auth service (the
// realm its 401 names), for this repository only.
func (g *Registry) token(ctx context.Context, challenge string, r Ref) (string, error) {
	params := parseChallenge(challenge)
	realm := params["realm"]
	ru, err := url.Parse(realm)
	if err != nil || realm == "" || (ru.Scheme != "https" && ru.Scheme != g.Scheme) {
		return "", fmt.Errorf("%s: the registry needs a sign-in", r)
	}
	q := ru.Query()
	if s := params["service"]; s != "" {
		q.Set("service", s)
	}
	scope := params["scope"]
	if scope == "" {
		scope = "repository:" + r.Repo + ":pull"
	}
	q.Set("scope", scope)
	ru.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ru.String(), nil)
	if err != nil {
		return "", err
	}
	res, err := g.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusTooManyRequests {
		return "", &RateLimitError{RetryAfter: retryAfter(res.Header.Get("Retry-After"))}
	}
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: the registry's sign-in answered %d", r, res.StatusCode)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	if body.Token != "" {
		return body.Token, nil
	}
	if body.AccessToken != "" {
		return body.AccessToken, nil
	}
	return "", fmt.Errorf("%s: the registry sent no token", r)
}

// parseChallenge reads `Bearer realm="…",service="…",scope="…"`.
func parseChallenge(h string) map[string]string {
	out := map[string]string{}
	h = strings.TrimSpace(h)
	if len(h) < 7 || !strings.EqualFold(h[:7], "bearer ") {
		return out
	}
	rest := h[7:]
	for rest != "" {
		eq := strings.IndexByte(rest, '=')
		if eq < 0 {
			break
		}
		key := strings.ToLower(strings.TrimSpace(rest[:eq]))
		rest = rest[eq+1:]
		var val string
		if strings.HasPrefix(rest, `"`) {
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				break
			}
			val, rest = rest[1:end+1], rest[end+2:]
		} else if c := strings.IndexByte(rest, ','); c >= 0 {
			val, rest = rest[:c], rest[c:]
		} else {
			val, rest = rest, ""
		}
		out[key] = val
		rest = strings.TrimLeft(rest, ", ")
	}
	return out
}

// retryAfter reads Retry-After (seconds or a date); 0 when absent.
func retryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
