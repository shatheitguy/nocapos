//go:build !linux

package api

// mdnsPublisher does nothing where there's no Avahi (local addresses still
// work through the Shield DNS blocker or the devices' own hosts files).
type mdnsPublisher struct{}

func newMDNSPublisher() *mdnsPublisher                     { return &mdnsPublisher{} }
func (p *mdnsPublisher) available() bool                   { return false }
func (p *mdnsPublisher) publish(names []string, ip string) {}
