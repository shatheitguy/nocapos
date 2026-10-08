// Package docker is a minimal Docker Engine API client.
//
// It talks to the Engine REST API directly instead of pulling in the Moby SDK:
// alfad only needs a small, stable subset of endpoints, the API is versioned,
// and owning the client keeps the attack surface and binary size small.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	minAPIVersion = "1.41" // Docker 20.10
	maxAPIVersion = "1.51" // highest version this client has been written against
)

// ErrUnavailable wraps failures to reach the engine (not running, no socket).
var ErrUnavailable = errors.New("docker engine unavailable")

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("docker: %s (HTTP %d)", e.Message, e.Status) }

func IsNotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

type Client struct {
	hc     *http.Client // bounded requests
	stream *http.Client // long-lived streams (stats, logs, events)
	base   string
	dial   dialFunc // raw connection, for hijacked exec streams

	mu      sync.Mutex
	version string // negotiated lazily; empty until the engine has answered
}

type dialFunc func(ctx context.Context) (net.Conn, error)

// New prepares a client for host:
//
//	unix:///var/run/docker.sock        Linux, rootless, Docker Desktop VM
//	npipe:////./pipe/docker_engine     Docker Desktop on Windows
//	tcp://host:2375                    socket proxies
//
// It does not contact the engine; the API version is negotiated on first use,
// so alfad can start (and keep serving) while Docker is down.
func New(host string) (*Client, error) {
	u, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("docker host %q: %w", host, err)
	}
	netDialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	var (
		dial dialFunc
		base = "http://docker"
	)
	switch u.Scheme {
	case "unix":
		dial = func(ctx context.Context) (net.Conn, error) { return netDialer.DialContext(ctx, "unix", u.Path) }
	case "npipe":
		if dial, err = pipeDialer(strings.ReplaceAll(u.Path, "/", `\`)); err != nil {
			return nil, fmt.Errorf("docker host %q: %w", host, err)
		}
	case "tcp", "http":
		dial = func(ctx context.Context) (net.Conn, error) { return netDialer.DialContext(ctx, "tcp", u.Host) }
		base = "http://" + u.Host
	default:
		return nil, fmt.Errorf("docker host %q: unsupported scheme %q", host, u.Scheme)
	}
	tr := &http.Transport{
		DialContext:         func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) },
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
	}
	return &Client{
		hc:     &http.Client{Transport: tr, Timeout: 30 * time.Second},
		stream: &http.Client{Transport: tr},
		base:   base,
		dial:   dial,
	}, nil
}

// APIVersion returns the negotiated version, or "" if the engine has not been reached.
func (c *Client) APIVersion() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version
}

// ensure negotiates the API version once the engine is reachable.
func (c *Client) ensure(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.version != "" {
		return c.version, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/_ping", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: ping returned HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	server := resp.Header.Get("Api-Version")
	if server == "" {
		server = minAPIVersion
	}
	if compareVersions(server, minAPIVersion) < 0 {
		return "", fmt.Errorf("docker API %s is older than the required %s", server, minAPIVersion)
	}
	c.version = server
	if compareVersions(server, maxAPIVersion) > 0 {
		c.version = maxAPIVersion
	}
	return c.version, nil
}

func (c *Client) Ping(ctx context.Context) error {
	resp, err := c.send(ctx, c.hc, http.MethodGet, "/_ping", nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// do issues a bounded request and decodes a JSON response into out (if non-nil).
func (c *Client) do(ctx context.Context, method, path string, q url.Values, out any) error {
	return c.doBody(ctx, method, path, q, nil, out)
}

// doBody is do() with an optional JSON request body.
func (c *Client) doBody(ctx context.Context, method, path string, q url.Values, in, out any) error {
	resp, err := c.sendBody(ctx, c.hc, method, path, q, in)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) send(ctx context.Context, hc *http.Client, method, path string, q url.Values) (*http.Response, error) {
	return c.sendBody(ctx, hc, method, path, q, nil)
}

func (c *Client) sendBody(ctx context.Context, hc *http.Client, method, path string, q url.Values, in any) (*http.Response, error) {
	version, err := c.ensure(ctx)
	if err != nil {
		return nil, err
	}
	u := c.base + "/v" + version + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var bodyReader io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
	if err != nil {
		return nil, err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			err = fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		return nil, fmt.Errorf("docker %s %s: %w", method, path, err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		var body struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body)
		if body.Message == "" {
			body.Message = http.StatusText(resp.StatusCode)
		}
		return nil, &APIError{Status: resp.StatusCode, Message: body.Message}
	}
	return resp, nil
}

// compareVersions compares dotted numeric versions ("1.41" < "1.47").
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}
