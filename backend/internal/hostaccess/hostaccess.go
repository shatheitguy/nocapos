// Package hostaccess holds the switches for how this machine itself is used:
// whether other devices may open NoCapOS (a flag file both alfad and the cap
// CLI read and write), and NoCap Desktop, what the server's own monitor
// shows (the native NoCap Desktop; see desktop.go).
package hostaccess

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// RemoteOffFile is created in the data folder while other devices are kept
// out. A file (not a database row) so `sudo cap web on` can undo it from the
// server's own console even when NoCapOS can't be reached.
const RemoteOffFile = "remote-access-off"

// LocalOnlyMessage is what a blocked device is told.
const LocalOnlyMessage = "NoCapOS is local-only right now. Turn it back on from the server's own screen or run `sudo cap web on`."

// RemoteEnabled reports whether other devices may open NoCapOS.
func RemoteEnabled(dataDir string) bool {
	_, err := os.Stat(filepath.Join(dataDir, RemoteOffFile))
	return err != nil
}

// SetRemote lets other devices in (true) or keeps them out (false).
func SetRemote(dataDir string, enabled bool) error {
	p := filepath.Join(dataDir, RemoteOffFile)
	if enabled {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	note := fmt.Sprintf("Other devices can't open NoCapOS while this file exists.\nRemove it (or run: sudo cap web on) to let them back in.\nTurned off %s\n", time.Now().UTC().Format(time.RFC3339))
	return os.WriteFile(p, []byte(note), 0o644)
}

// Gate answers "are other devices kept out?" cheaply on every request: it
// looks at the flag file at most once per TTL, so a change made by cap or
// another process shows up within that time without a restart.
type Gate struct {
	dataDir string
	ttl     time.Duration
	now     func() time.Time

	mu      sync.Mutex
	checked time.Time
	off     bool
}

// NewGate watches dataDir's flag file, rechecking at most every 2 seconds.
func NewGate(dataDir string) *Gate {
	return &Gate{dataDir: dataDir, ttl: 2 * time.Second, now: time.Now}
}

// LocalOnly reports whether only this machine may open NoCapOS.
func (g *Gate) LocalOnly() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if now := g.now(); g.checked.IsZero() || now.Sub(g.checked) >= g.ttl {
		g.off = !RemoteEnabled(g.dataDir)
		g.checked = now
	}
	return g.off
}

// Refresh forgets the cached answer (after alfad flips the switch itself).
func (g *Gate) Refresh() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.checked = time.Time{}
	g.mu.Unlock()
}

// LoopbackPeer reports whether a connection's remote address (the real TCP
// peer, never a header) is this machine.
func LoopbackPeer(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	a, err := netip.ParseAddr(host)
	return err == nil && a.Unmap().IsLoopback()
}

// DataDir finds the data folder the installed service uses: ALFA_DATA_DIR
// from its environment file, else the installer's default.
func DataDir(envFile string) string {
	const def = "/var/lib/nocapos"
	f, err := os.Open(envFile)
	if err != nil {
		return def
	}
	defer f.Close()
	dir := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if v, ok := strings.CutPrefix(line, "ALFA_DATA_DIR="); ok {
			dir = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	if dir == "" {
		return def
	}
	return dir
}
