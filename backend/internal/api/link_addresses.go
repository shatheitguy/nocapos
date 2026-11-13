package api

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"sort"
	"strings"

	"alfaos/alfad/internal/store"
)

// NoCap Link, always connected: where a phone can reach this server. At
// home it talks to a LAN address directly ("Nearby"); away it uses the
// remote address ("Remote"). The phone checks every address it tries is
// this same server (NoCap ID) behind the same pinned certificate, so a
// wrong guess here only costs it a failed probe.

const (
	linkRemoteKey = "link.remote_address"
	linkLANKey    = "link.lan_addresses" // extra LAN addresses, comma-separated
	linkMaxLAN    = 6
)

type linkAddresses struct {
	NocapID string   `json:"nocap_id"`
	LAN     []string `json:"lan"`    // origins (scheme://host:port), best first
	Remote  string   `json:"remote"` // origin for away from home, "" when none is set
	// Saved by an administrator (the rest is found automatically).
	SavedLAN    []string `json:"saved_lan"`
	SavedRemote string   `json:"saved_remote"`
}

// interfaceAddrs lists this machine's addresses with their interface
// names; a variable so tests can stand in.
var interfaceAddrs = func() map[string][]netip.Addr {
	out := map[string][]netip.Addr{}
	ifs, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil {
				out[i.Name] = append(out[i.Name], p.Addr())
			}
		}
	}
	return out
}

// inContainer is true inside Docker, where the interfaces aren't the LAN's.
var inContainer = func() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// virtualIface: bridges and tunnels that aren't the home network.
func notHomeIface(name string) bool {
	for _, p := range []string{"docker", "br-", "veth", "virbr", "vnet", "lxc", "lxd", "cni", "flannel", "kube", "podman", "tap", "tun", "wg", "tailscale", "zt"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// listenPort is the port the server itself listens on, and whether it
// listens on more than loopback.
func listenPort(addr string) (string, bool) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", false
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return port, true
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return port, true // a host name: assume it's reachable
	}
	return port, !a.IsLoopback()
}

// originFor builds scheme://ip[:port].
func originFor(scheme string, a netip.Addr, port string) string {
	host := a.String()
	if a.Is6() {
		host = "[" + host + "]"
	}
	if port == "" || (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		return scheme + "://" + host
	}
	return scheme + "://" + host + ":" + port
}

// cleanOrigin turns "https://x:8443/" into "https://x:8443"; "" when it
// isn't an http(s) address.
func cleanOrigin(raw string) string {
	link, ok := webURL(raw)
	if !ok {
		return ""
	}
	u, err := url.Parse(link)
	if err != nil || u.User != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func splitOrigins(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if o := cleanOrigin(p); o != "" {
			out = append(out, o)
		}
	}
	return out
}

// addresses works out where phones can reach this server, given the
// request a phone (or desktop) made.
func (s *Server) addresses(r *http.Request) linkAddresses {
	out := linkAddresses{LAN: []string{}, SavedLAN: []string{}}
	out.NocapID, _ = s.nocapID()
	seen := map[string]bool{}
	add := func(o string) {
		if o != "" && !seen[o] && len(out.LAN) < linkMaxLAN {
			seen[o] = true
			out.LAN = append(out.LAN, o)
		}
	}
	saved, _ := s.Store.Setting(r.Context(), linkLANKey)
	out.SavedLAN = append(out.SavedLAN, splitOrigins(saved)...)
	out.SavedRemote, _ = s.Store.Setting(r.Context(), linkRemoteKey)
	out.SavedRemote = cleanOrigin(out.SavedRemote)
	out.Remote = out.SavedRemote

	from := netOf(clientIP(r, s.Config.TrustedProxies))
	// The address this request came in on, when it's a home-network one.
	if from == "lan" {
		if a, err := netip.ParseAddr(strings.Trim(requestHostname(r), "[]")); err == nil && netOf(a.String()) == "lan" {
			add(requestOrigin(r))
		} else if strings.HasSuffix(requestHostname(r), ".local") {
			add(requestOrigin(r))
		}
	}
	for _, o := range out.SavedLAN {
		add(o)
	}
	if port, open := listenPort(s.Config.ListenAddr); open && !inContainer() {
		scheme := "http"
		if s.Config.TLSEnabled() {
			scheme = "https"
		}
		ifs := interfaceAddrs()
		names := make([]string, 0, len(ifs))
		for n := range ifs {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if notHomeIface(n) {
				continue
			}
			for _, a := range ifs[n] {
				a = a.Unmap()
				if a.Is4() && a.IsPrivate() {
					add(originFor(scheme, a, port))
				}
			}
		}
	}
	// Reached from the internet and nothing saved: that address is the remote one.
	if out.Remote == "" && from == "internet" {
		out.Remote = requestOrigin(r)
	}
	return out
}

// deviceAddresses: the phone asks where else it can reach us.
func (s *Server) deviceAddresses(w http.ResponseWriter, r *http.Request) {
	a := s.addresses(r)
	a.SavedLAN, a.SavedRemote = nil, ""
	writeJSON(w, http.StatusOK, a)
}

// linkAddressesGet: the same, for Settings → Devices.
func (s *Server) linkAddressesGet(w http.ResponseWriter, r *http.Request) {
	a := s.addresses(r)
	if userFrom(r.Context()).Role != store.RoleAdmin {
		a.SavedLAN, a.SavedRemote = nil, ""
	}
	writeJSON(w, http.StatusOK, a)
}

// linkAddressesPut: an administrator saves the remote address (and any
// extra LAN addresses) phones should use.
func (s *Server) linkAddressesPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Remote string   `json:"remote"`
		LAN    []string `json:"lan"`
	}
	if !decodeJSONLimit(w, r, &req, 16<<10) {
		return
	}
	remote := ""
	if strings.TrimSpace(req.Remote) != "" {
		if remote = cleanOrigin(req.Remote); remote == "" {
			writeError(w, http.StatusBadRequest, "validation", "The remote address must be an http or https address, like https://nocap.example.com.")
			return
		}
	}
	if len(req.LAN) > linkMaxLAN {
		writeError(w, http.StatusBadRequest, "validation", "Up to 6 home-network addresses.")
		return
	}
	lan := make([]string, 0, len(req.LAN))
	for _, l := range req.LAN {
		if strings.TrimSpace(l) == "" {
			continue
		}
		o := cleanOrigin(l)
		if o == "" {
			writeError(w, http.StatusBadRequest, "validation", "Home-network addresses must be http or https addresses, like https://192.168.1.20:8443.")
			return
		}
		lan = append(lan, o)
	}
	if err := s.Store.SetSetting(r.Context(), linkRemoteKey, remote); err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.Store.SetSetting(r.Context(), linkLANKey, strings.Join(lan, ",")); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, userFrom(r.Context()).ID, "link.addresses", remote, true, strings.Join(lan, ","))
	s.linkAddressesGet(w, r)
}

// linkService is the mDNS service type phones browse for.
const linkService = "_nocap._tcp"
