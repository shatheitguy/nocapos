package api

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// First-run setup creates the administrator the way any computer does on its
// first start: pick a name and a password. That's only safe from somewhere
// the owner already controls, so the setup token is skipped just for:
//
//   - this machine itself (loopback: the server's own screen, NoCap Desktop)
//   - the private local network: RFC 1918 IPv4, IPv4/IPv6 link-local, and
//     IPv6 unique local addresses (fc00::/7)
//
// Anything else, including carrier-grade NAT (100.64.0.0/10, shared by many
// customers) and every public address, still needs the one-time token.
//
// The decision uses the real TCP peer. X-Forwarded-For only counts when that
// peer is one of the configured trusted proxies, and then only its right-most
// entry (the address the proxy itself saw); anything a client wrote further
// left is never read.

// (cgnat, 100.64.0.0/10, is declared in sessions.go.)

// localNetworkAddr reports whether a is on this machine or its private local
// network.
func localNetworkAddr(a netip.Addr) bool {
	if !a.IsValid() {
		return false
	}
	a = a.Unmap()
	if a.IsUnspecified() || a.IsMulticast() || cgnat.Contains(a) {
		return false
	}
	// IsPrivate: 10/8, 172.16/12, 192.168/16 and fc00::/7.
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast()
}

// setupPeer is the address a setup request is judged by: the TCP peer, or
// what a trusted proxy in front reports. ok is false when it can't be told,
// which counts as "not local".
func setupPeer(r *http.Request, trusted []netip.Prefix) (addr netip.Addr, ok bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	// A zone (fe80::1%eth0) is fine to drop: the address decides.
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	peer = peer.Unmap()
	xff := r.Header.Values("X-Forwarded-For")
	if !inPrefixes(peer, trusted) {
		// A browser talking to us directly sends no proxy headers. With
		// them, the peer is a proxy we weren't told about (a tunnel or a
		// reverse proxy on the local network) forwarding someone whose
		// address we can't trust: fail closed.
		if len(xff) > 0 || forwardedVia(r) {
			return netip.Addr{}, false
		}
		return peer, true
	}
	if len(xff) == 0 {
		if forwardedVia(r) {
			return netip.Addr{}, false // a proxy that names the client some other way
		}
		return peer, true // the proxy is the client (e.g. a local tool)
	}
	parts := strings.Split(xff[len(xff)-1], ",")
	last := strings.TrimSpace(parts[len(parts)-1])
	if i := strings.IndexByte(last, '%'); i >= 0 {
		last = last[:i]
	}
	a, err := netip.ParseAddr(last)
	if err != nil {
		return netip.Addr{}, false // a proxy we trust sent something odd: fail closed
	}
	return a.Unmap(), true
}

// proxyHeaders are what proxies and tunnels use, besides X-Forwarded-For,
// to say who the real client is.
var proxyHeaders = []string{"Forwarded", "X-Real-Ip", "X-Client-Ip", "True-Client-Ip", "Cf-Connecting-Ip", "X-Original-Forwarded-For", "Via"}

// forwardedVia reports whether r came through a proxy that named the
// client some way other than X-Forwarded-For.
func forwardedVia(r *http.Request) bool {
	for _, h := range proxyHeaders {
		if r.Header.Get(h) != "" {
			return true
		}
	}
	return false
}

// setupFromLocal reports whether this setup request may skip the token.
func (s *Server) setupFromLocal(r *http.Request) bool {
	if onLocalDesktop(r) {
		return true // the loopback-only listener of the server's own screen
	}
	a, ok := setupPeer(r, s.Config.TrustedProxies)
	return ok && localNetworkAddr(a)
}
