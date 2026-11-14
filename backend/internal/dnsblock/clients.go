package dnsblock

import (
	"net"
	"net/netip"
	"sync"
	"time"
)

// The blocker answers this machine and its own networks only. Bound to a
// public address it would otherwise be an open resolver anyone could use
// (amplification attacks, snooping, filling the query log).

// cgnat (100.64.0.0/10) is where VPN overlays put their devices.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// clientFilter knows the networks this machine is on, refreshed now and
// then so a new address (DHCP, a new interface) is picked up.
type clientFilter struct {
	mu   sync.Mutex
	nets []netip.Prefix
	at   time.Time
	now  func() time.Time
	read func() []netip.Prefix
}

func newClientFilter() *clientFilter {
	return &clientFilter{now: time.Now, read: interfaceNets}
}

const clientNetsEvery = 30 * time.Second

func (f *clientFilter) allowed(a netip.Addr) bool {
	f.mu.Lock()
	if now := f.now(); f.at.IsZero() || now.Sub(f.at) >= clientNetsEvery {
		f.nets, f.at = f.read(), now
	}
	nets := f.nets
	f.mu.Unlock()
	return localClient(a, nets)
}

// localClient: loopback, private (RFC 1918, fc00::/7), link-local, the
// shared VPN range, or on one of the machine's own networks (an IPv6 home
// network hands out global addresses).
func localClient(a netip.Addr, nets []netip.Prefix) bool {
	if !a.IsValid() {
		return false
	}
	a = a.Unmap()
	if a.IsUnspecified() || a.IsMulticast() {
		return false
	}
	if a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || cgnat.Contains(a) {
		return true
	}
	for _, p := range nets {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// interfaceNets are the IPv6 networks of this machine's addresses (an IPv6
// home network hands its devices global addresses). IPv4 networks are
// private already; a public IPv4 subnet (a hosted server's neighbours) is
// not "local". A wide prefix (misconfigured) is skipped.
func interfaceNets() []netip.Prefix {
	var out []netip.Prefix
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		p, err := netip.ParsePrefix(a.String())
		if err != nil || !p.Addr().Is6() || p.Addr().Is4In6() || p.Bits() < 48 {
			continue
		}
		out = append(out, p.Masked())
	}
	return out
}
