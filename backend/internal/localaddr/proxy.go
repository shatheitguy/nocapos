package localaddr

import (
	"context"
	"errors"
	"html"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Route is where one local name leads.
type Route struct {
	Name      string `json:"name"`      // api.myapp.nocap.local
	Container string `json:"container"` // the container's name
	ID        string `json:"id"`
	Target    string `json:"target"` // host:port the proxy connects to
	Source    string `json:"source"` // label | container | service
}

// Table is the current name → route map. It's replaced as a whole.
type Table struct {
	mu     sync.RWMutex
	routes map[string]Route
}

func NewTable() *Table { return &Table{routes: map[string]Route{}} }

// Set replaces every route. The first route for a name wins, so callers
// put chosen (label) names first.
func (t *Table) Set(routes []Route) {
	m := make(map[string]Route, len(routes))
	for _, r := range routes {
		if _, dup := m[r.Name]; !dup && IsLocalName(r.Name) && r.Target != "" {
			m[r.Name] = r
		}
	}
	t.mu.Lock()
	t.routes = m
	t.mu.Unlock()
}

// Lookup finds the route for a Host header (port ignored, any case).
func (t *Table) Lookup(host string) (Route, bool) {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if !IsLocalName(host) {
		return Route{}, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	r, ok := t.routes[host]
	return r, ok
}

// All lists the routes, sorted by name.
func (t *Table) All() []Route {
	t.mu.RLock()
	out := make([]Route, 0, len(t.routes))
	for _, r := range t.routes {
		out = append(out, r)
	}
	t.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Names lists every name in the table.
func (t *Table) Names() []string {
	all := t.All()
	out := make([]string, len(all))
	for i, r := range all {
		out[i] = r.Name
	}
	return out
}

// ByContainer maps container ids to their names.
func (t *Table) ByContainer() map[string][]string {
	out := map[string][]string{}
	for _, r := range t.All() {
		out[r.ID] = append(out[r.ID], r.Name)
	}
	return out
}

// Proxy forwards requests for local names to their containers. Allowed
// decides who may use it (the API keeps it to the local network).
type Proxy struct {
	Table   *Table
	Allowed func(r *http.Request) bool

	transport http.RoundTripper
}

func NewProxy(t *Table, allowed func(*http.Request) bool) *Proxy {
	return &Proxy{Table: t, Allowed: allowed, transport: &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 2 * time.Minute,
		ForceAttemptHTTP2:     false,
	}}
}

// Handles reports whether a request is for a local name this proxy knows.
func (p *Proxy) Handles(r *http.Request) bool {
	_, ok := p.Table.Lookup(r.Host)
	return ok
}

// ServeHTTP proxies to the container behind the Host header (websockets too).
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route, ok := p.Table.Lookup(r.Host)
	if !ok {
		page(w, http.StatusNotFound, "Nothing lives at this address", "No container on this server uses "+r.Host+" right now.")
		return
	}
	if p.Allowed != nil && !p.Allowed(r) {
		page(w, http.StatusForbidden, "Local network only", "Local addresses only open from devices on this server's home network.")
		return
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	target := &url.URL{Scheme: "http", Host: route.Target}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = r.Host // apps build links from it
			pr.SetXForwarded()
			pr.Out.Header.Set("X-Forwarded-Proto", scheme)
			// The caller's NoCapOS session never reaches the app.
			stripNoCapCookies(pr.Out)
			pr.Out.Header.Del("Authorization")
		},
		Transport:     p.transport,
		FlushInterval: -1, // stream (server-sent events, logs)
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			msg := "The container didn't answer. Is it running?"
			if errors.Is(err, context.Canceled) {
				return
			}
			page(w, http.StatusBadGateway, route.Container+" isn't answering", msg)
		},
	}
	rp.ServeHTTP(w, r)
}

// stripNoCapCookies drops NoCapOS's own cookies (they're scoped to the
// server's name, but a browser may still send them to a subdomain).
func stripNoCapCookies(r *http.Request) {
	cookies := r.Cookies()
	if len(cookies) == 0 {
		return
	}
	r.Header.Del("Cookie")
	for _, c := range cookies {
		if strings.HasPrefix(strings.ToLower(c.Name), "alfa") || strings.HasPrefix(strings.ToLower(c.Name), "nocap") {
			continue
		}
		r.AddCookie(c)
	}
}

func page(w http.ResponseWriter, code int, title, msg string) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` +
		html.EscapeString(title) + `</title><style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#0b0b10;color:#f2f2f7;font:16px/1.5 system-ui,sans-serif}main{max-width:30rem;padding:2rem;text-align:center}h1{font-size:1.3rem;margin:0 0 .5rem;color:#ff453a}p{color:#c7c7cc}</style></head><body><main><h1>` +
		html.EscapeString(title) + `</h1><p>` + html.EscapeString(msg) + `</p></main></body></html>`))
}
