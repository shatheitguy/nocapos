// Package webaddr decides where "Open" sends people for an app or container:
// the admin's own web address when one is set, the container's own LAN
// address when it sits on a macvlan/ipvlan network, or (left to the browser)
// this server's address and the published port.
package webaddr

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

// Attachment is one network a container is on.
type Attachment struct {
	Driver string // bridge, macvlan, ipvlan, host…
	IP     string
}

// Direct reports whether containers on this network driver get their own
// address on the LAN (reachable without a published port).
func Direct(driver string) bool { return driver == "macvlan" || driver == "ipvlan" }

// ContainerURL is the address of a container that has its own LAN IP, at its
// internal port, or "" when it has none (then host + published port applies).
func ContainerURL(nets []Attachment, port int, path string) string {
	if port <= 0 || port > 65535 {
		return ""
	}
	for _, n := range nets {
		if !Direct(n.Driver) || n.IP == "" || net.ParseIP(n.IP) == nil {
			continue
		}
		scheme := "http"
		if port == 443 || port == 8443 {
			scheme = "https"
		}
		return scheme + "://" + net.JoinHostPort(n.IP, strconv.Itoa(port)) + cleanPath(path)
	}
	return ""
}

// webPorts are the ports a web UI usually listens on, most likely first.
var webPorts = []int{80, 8080, 443, 3000, 8000, 8443, 5000, 9000, 8081, 8888}

// PickPort guesses which of a container's TCP ports serves its web UI.
func PickPort(ports []int) int {
	have := map[int]bool{}
	low := 0
	for _, p := range ports {
		if p <= 0 || p > 65535 {
			continue
		}
		have[p] = true
		if low == 0 || p < low {
			low = p
		}
	}
	for _, p := range webPorts {
		if have[p] {
			return p
		}
	}
	return low
}

// Resolve picks the address "Open" uses: the override, else the container's
// own LAN address, else "" (the browser uses this server + published port).
func Resolve(override string, nets []Attachment, port int, path string) string {
	if override != "" {
		return override
	}
	return ContainerURL(nets, port, path)
}

func cleanPath(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}

// ErrInvalid is returned for addresses that aren't plain http(s) URLs.
var ErrInvalid = errors.New("enter a web address starting with http:// or https://, like https://jellyfin.home.lan or http://10.0.0.50:8096")

// Normalize validates an address an admin typed. "" clears it.
func Normalize(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if len(raw) > 2048 || strings.ContainsAny(raw, " \t\r\n\"'<>\\`") {
		return "", ErrInvalid
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return "", ErrInvalid
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "", ErrInvalid
		}
	}
	u.Fragment = ""
	return u.String(), nil
}

// Keys in the saved map.
func AppKey(id string) string         { return "app:" + id }
func ContainerKey(name string) string { return "container:" + name }

// Settings is where the addresses are kept (one JSON server setting).
type Settings interface {
	Setting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
}

const settingKey = "web_addresses"

// Book keeps the admin's web addresses for apps and containers.
type Book struct {
	st Settings
	mu sync.Mutex
}

func NewBook(st Settings) *Book { return &Book{st: st} }

// All returns every saved address by key.
func (b *Book) All(ctx context.Context) (map[string]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.load(ctx)
}

func (b *Book) load(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	v, err := b.st.Setting(ctx, settingKey)
	if err != nil || v == "" {
		return out, err
	}
	if err := json.Unmarshal([]byte(v), &out); err != nil {
		return map[string]string{}, nil // a damaged value just starts over
	}
	return out, nil
}

// Set saves (or with "" removes) the address for key; the address must have
// been through Normalize.
func (b *Book) Set(ctx context.Context, key, addr string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	all, err := b.load(ctx)
	if err != nil {
		return err
	}
	if addr == "" {
		delete(all, key)
	} else {
		all[key] = addr
	}
	data, err := json.Marshal(all)
	if err != nil {
		return err
	}
	return b.st.SetSetting(ctx, settingKey, string(data))
}
