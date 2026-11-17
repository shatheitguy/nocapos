package hostaccess

import "net"

// Listener wraps a network listener so that, while other devices are kept
// out (local-only, the "Desktop only" way of using NoCapOS), a connection
// from anywhere but this machine is closed the moment it arrives: nothing
// is served to the network at all, as if NoCapOS listened on loopback only.
// Unlike binding to loopback, the switch takes effect at once, both ways,
// without restarting NoCapOS.
func (g *Gate) Listener(ln net.Listener) net.Listener {
	if g == nil {
		return ln
	}
	return &gatedListener{Listener: ln, gate: g}
}

type gatedListener struct {
	net.Listener
	gate *Gate
}

func (l *gatedListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if l.gate.LocalOnly() && !LoopbackPeer(c.RemoteAddr().String()) {
			_ = c.Close()
			continue
		}
		return c, nil
	}
}
