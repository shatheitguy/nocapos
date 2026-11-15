package api

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"alfaos/alfad/internal/dnsblock"
	"alfaos/alfad/internal/docker"
	"alfaos/alfad/internal/localaddr"
	"alfaos/alfad/internal/webaddr"
)

// Local addresses: every running container with a web port gets
// http(s)://<container>.nocap.local (and <service>.<project>.nocap.local,
// or the name its nocap.domain label picks). alfad answers those names
// itself: it proxies to the container, serves HTTPS with a certificate from
// NoCapOS's own name-constrained CA, and announces the names on the home
// network (mDNS, and the Shield DNS blocker when that's on). Only devices
// on the local network may use them; the "local-only" switch still applies.

const localAddrKey = "containers.local_addresses" // "off" turns them off

type localAddrState struct {
	table *localaddr.Table
	proxy *localaddr.Proxy

	mu      sync.Mutex
	enabled bool
	loaded  bool
	ca      *localaddr.CA
	caErr   error
	kick    chan struct{}
	lanIP   string
	mdns    *mdnsPublisher
	lastErr string
}

func (s *Server) initLocalAddresses(ctx context.Context) {
	st := &localAddrState{table: localaddr.NewTable(), kick: make(chan struct{}, 1), enabled: true, mdns: newMDNSPublisher()}
	st.proxy = localaddr.NewProxy(st.table, func(r *http.Request) bool {
		a, ok := setupPeer(r, s.Config.TrustedProxies)
		return ok && localNetworkAddr(a)
	})
	s.localAddr = st
	if s.Docker == nil || s.Store == nil {
		return
	}
	go s.runLocalAddresses(ctx)
}

// localAddrOn reports whether local addresses are on (read once, then kept).
func (s *Server) localAddrOn(ctx context.Context) bool {
	st := s.localAddr
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.loaded {
		v, err := s.Store.Setting(ctx, localAddrKey)
		if err != nil {
			return false
		}
		st.enabled, st.loaded = v != "off", true
	}
	return st.enabled
}

func (s *Server) kickLocalAddresses() {
	select {
	case s.localAddr.kick <- struct{}{}:
	default:
	}
}

func (s *Server) runLocalAddresses(ctx context.Context) {
	// Follow the engine's events so names come and go with the containers.
	go func() {
		for ctx.Err() == nil {
			_ = s.Docker.Events(ctx, func(e docker.Event) error {
				if e.Type == "container" {
					switch e.Action {
					case "start", "die", "destroy", "rename", "unpause", "pause":
						s.kickLocalAddresses()
					}
				}
				return nil
			})
			select {
			case <-ctx.Done():
				return
			case <-time.After(15 * time.Second):
			}
		}
	}()
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	defer s.localAddr.mdns.publish(nil, "")
	for {
		s.refreshLocalAddresses(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-s.localAddr.kick:
			// Several events usually come together; take them as one.
			select {
			case <-ctx.Done():
				return
			case <-time.After(700 * time.Millisecond):
			}
		}
	}
}

func (s *Server) refreshLocalAddresses(ctx context.Context) {
	st := s.localAddr
	if !s.localAddrOn(ctx) {
		st.table.Set(nil)
		st.mdns.publish(nil, "")
		s.publishLocalDNS(nil, "")
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	list, err := s.Docker.ListContainers(cctx, false)
	if err != nil {
		st.mu.Lock()
		st.lastErr = err.Error()
		st.mu.Unlock()
		return
	}
	routes := buildRoutes(list)
	st.table.Set(routes)
	ip := lanIPv4()
	st.mu.Lock()
	st.lastErr, st.lanIP = "", ip
	st.mu.Unlock()
	names := st.table.Names()
	st.mdns.publish(names, ip)
	s.publishLocalDNS(names, ip)
}

// buildRoutes turns running containers into routes: chosen (label) names
// first, so they win over automatic ones; NoCapOS's own containers never
// get one.
func buildRoutes(list []docker.Container) []localaddr.Route {
	var routes []localaddr.Route
	for _, c := range list {
		if c.System || c.State != "running" {
			continue
		}
		target := routeTarget(c)
		if target == "" {
			continue
		}
		add := func(name, src string) {
			if name != "" {
				routes = append(routes, localaddr.Route{Name: name, Container: c.Name, ID: c.ID, Target: target, Source: src})
			}
		}
		if label := c.Labels[localaddr.LabelDomain]; label != "" {
			n, _ := localaddr.NormalizeLabel(label) // invalid labels are ignored
			add(n, "label")
		}
		if slug := localaddr.Slug(c.Name); slug != "" {
			add(slug+"."+localaddr.Suffix, "container")
		}
		if p, sv := localaddr.Slug(c.Project), localaddr.Slug(c.Service); p != "" && sv != "" {
			add(sv+"."+p+"."+localaddr.Suffix, "service")
		}
	}
	rank := map[string]int{"label": 0, "container": 1, "service": 2}
	sort.SliceStable(routes, func(i, j int) bool { return rank[routes[i].Source] < rank[routes[j].Source] })
	return routes
}

// routeTarget is where alfad connects for a container's web port: its
// published port on this machine, else its address on a bridge network.
// macvlan/ipvlan addresses can't be reached from the host itself (a Linux
// limit), so those containers need a published port.
func routeTarget(c docker.Container) string {
	var tcp []int
	for _, p := range c.Ports {
		if p.Protocol == "" || p.Protocol == "tcp" {
			tcp = append(tcp, int(p.Private))
		}
	}
	port := webaddr.PickPort(tcp)
	if v, err := strconv.Atoi(c.Labels[localaddr.LabelPort]); err == nil && v > 0 && v < 65536 {
		port = v
	}
	if port == 0 {
		return ""
	}
	for _, p := range c.Ports {
		if int(p.Private) == port && p.Public != 0 && (p.Protocol == "" || p.Protocol == "tcp") {
			switch p.IP {
			case "", "0.0.0.0", "::", "127.0.0.1":
				return net.JoinHostPort("127.0.0.1", strconv.Itoa(int(p.Public)))
			}
		}
	}
	// Host-network containers aren't given one: a port on this machine's
	// loopback may belong to anything (even loopback-only services), and
	// they're reachable at the server's own address anyway.
	for _, n := range c.Networks {
		if n.IP != "" && !webaddr.Direct(n.Driver) {
			return net.JoinHostPort(n.IP, strconv.Itoa(port))
		}
	}
	return ""
}

// lanIPv4 is this server's address on the home network (the card with the
// default route first).
func lanIPv4() string {
	for _, it := range hostInterfaces() {
		if it.Address != "" {
			return it.Address
		}
	}
	return ""
}

func (s *Server) publishLocalDNS(names []string, ip string) {
	if s.shield == nil || s.shield.dns == nil {
		return
	}
	var recs []dnsblock.Record
	if ip != "" {
		for _, n := range names {
			recs = append(recs, dnsblock.Record{Name: n, Type: "A", Value: ip})
		}
	}
	s.shield.dns.SetExtraRecords(recs)
}

// localAddrRouter sends requests for a container's local name to it.
func (s *Server) localAddrRouter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if st := s.localAddr; st != nil && localaddr.IsLocalName(hostOnly(r.Host)) && s.localAddrOn(r.Context()) && st.proxy.Handles(r) {
			st.proxy.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// LocalAddresses wraps another listener (plain HTTP on port 80) so local
// names work there too.
func (s *Server) LocalAddresses(next http.Handler) http.Handler { return s.localAddrRouter(next) }

func hostOnly(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return h
}

func (s *Server) localCA() (*localaddr.CA, error) {
	st := s.localAddr
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.ca == nil && st.caErr == nil {
		st.ca, st.caErr = localaddr.LoadOrCreateCA(filepath.Join(s.Config.DataDir, "tls"))
	}
	return st.ca, st.caErr
}

// LocalAddressCert gives HTTPS connections for a container's local name a
// certificate from NoCapOS's own CA; anything else gets the server's usual
// certificate (nil, nil).
func (s *Server) LocalAddressCert(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	st := s.localAddr
	if st == nil || !localaddr.IsLocalName(hello.ServerName) {
		return nil, nil
	}
	if _, ok := st.table.Lookup(hello.ServerName); !ok || !s.localAddrOn(context.Background()) {
		return nil, nil
	}
	ca, err := s.localCA()
	if err != nil {
		return nil, nil
	}
	c, err := ca.Leaf(hello.ServerName)
	if err != nil {
		return nil, nil
	}
	return c, nil
}

// localURLs are the addresses a name opens at: https when this server
// serves HTTPS, and plain http on port 80 (or the main port without TLS).
func (s *Server) localURLs(name string) (httpURL, httpsURL string) {
	port, _ := listenPort(s.Config.ListenAddr)
	with := func(scheme, p, def string) string {
		if p == "" || p == def {
			return scheme + "://" + name
		}
		return scheme + "://" + name + ":" + p
	}
	if s.Config.TLSEnabled() {
		httpsURL = with("https", port, "443")
		if s.Config.HTTPRedirectAddr != "" {
			if _, p, err := net.SplitHostPort(s.Config.HTTPRedirectAddr); err == nil {
				httpURL = with("http", p, "80")
			}
		}
		return httpURL, httpsURL
	}
	return with("http", port, "80"), ""
}

// fillLocalAddresses adds each container's local addresses to a list.
func (s *Server) fillLocalAddresses(ctx context.Context, list []docker.Container) {
	if s.localAddr == nil || !s.localAddrOn(ctx) {
		return
	}
	by := s.localAddr.table.ByContainer()
	for i := range list {
		for _, n := range by[list[i].ID] {
			list[i].LocalAddresses = append(list[i].LocalAddresses, s.preferredURL(n))
		}
	}
}

func (s *Server) preferredURL(name string) string {
	h, hs := s.localURLs(name)
	if hs != "" {
		return hs
	}
	return h
}

type localAddrView struct {
	Name      string `json:"name"`
	Container string `json:"container"`
	ID        string `json:"id"`
	Source    string `json:"source"`
	HTTP      string `json:"http,omitempty"`
	HTTPS     string `json:"https,omitempty"`
}

// GET /api/v1/docker/local-addresses
func (s *Server) localAddrGet(w http.ResponseWriter, r *http.Request) {
	st := s.localAddr
	on := s.localAddrOn(r.Context())
	views := []localAddrView{}
	if on {
		for _, rt := range st.table.All() {
			h, hs := s.localURLs(rt.Name)
			views = append(views, localAddrView{Name: rt.Name, Container: rt.Container, ID: rt.ID, Source: rt.Source, HTTP: h, HTTPS: hs})
		}
	}
	st.mu.Lock()
	ip, lastErr := st.lanIP, st.lastErr
	st.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":   on,
		"https":     s.Config.TLSEnabled(),
		"suffix":    localaddr.Suffix,
		"lan_ip":    ip,
		"mdns":      st.mdns.available(),
		"dns":       s.shield != nil && s.shield.dns != nil && s.shield.dns.Settings().Enabled,
		"addresses": views,
		"error":     lastErr,
	})
}

// PUT /api/v1/docker/local-addresses {enabled}
func (s *Server) localAddrSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	v := "on"
	if !req.Enabled {
		v = "off"
	}
	err := s.Store.SetSetting(r.Context(), localAddrKey, v)
	s.audit(r, userFrom(r.Context()).ID, "containers.local_addresses", v, err == nil, errText(err))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	st := s.localAddr
	st.mu.Lock()
	st.enabled, st.loaded = req.Enabled, true
	st.mu.Unlock()
	if req.Enabled {
		_, _ = s.localCA() // ready before the first visit
	}
	s.refreshLocalAddresses(r.Context())
	s.localAddrGet(w, r)
}

// GET /api/v1/docker/local-addresses/ca.crt — the certificate devices
// install to trust local addresses (it can only vouch for *.nocap.local).
func (s *Server) localAddrCA(w http.ResponseWriter, r *http.Request) {
	ca, err := s.localCA()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="nocapos-local-ca.crt"`)
	_, _ = w.Write(ca.CertPEM())
}
