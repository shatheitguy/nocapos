//go:build linux

package api

import (
	"context"
	"os/exec"
	"sync"
)

// mdnsPublisher announces local addresses on the home network through
// Avahi: one `avahi-publish -a -R <name> <ip>` per name, pointing at this
// server (which proxies to the container). Names that go away are
// withdrawn; ones whose publisher stopped are started again.
type mdnsPublisher struct {
	mu   sync.Mutex
	bin  string
	ip   string
	runs map[string]*mdnsRun
}

type mdnsRun struct {
	stop context.CancelFunc
	done chan struct{}
}

// maxMDNSNames bounds how many Avahi helpers run at once.
const maxMDNSNames = 200

func newMDNSPublisher() *mdnsPublisher {
	p := &mdnsPublisher{runs: map[string]*mdnsRun{}}
	if !inContainer() {
		p.bin, _ = exec.LookPath("avahi-publish")
	}
	return p
}

func (p *mdnsPublisher) available() bool { return p.bin != "" }

func (p *mdnsPublisher) publish(names []string, ip string) {
	if p.bin == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if ip != p.ip { // a new address: announce everything again
		p.stopAllLocked()
		p.ip = ip
	}
	want := map[string]bool{}
	if ip != "" {
		for i, n := range names {
			if i >= maxMDNSNames {
				break
			}
			want[n] = true
		}
	}
	for n, r := range p.runs {
		select {
		case <-r.done: // Avahi restarted or the helper died
			delete(p.runs, n)
			continue
		default:
		}
		if !want[n] {
			r.stop()
			delete(p.runs, n)
		}
	}
	for n := range want {
		if _, ok := p.runs[n]; ok {
			continue
		}
		ctx, stop := context.WithCancel(context.Background())
		r := &mdnsRun{stop: stop, done: make(chan struct{})}
		cmd := exec.CommandContext(ctx, p.bin, "-a", "-R", n, ip)
		if err := cmd.Start(); err != nil {
			stop()
			continue
		}
		go func() {
			_ = cmd.Wait()
			close(r.done)
		}()
		p.runs[n] = r
	}
}

func (p *mdnsPublisher) stopAllLocked() {
	for n, r := range p.runs {
		r.stop()
		delete(p.runs, n)
	}
}
