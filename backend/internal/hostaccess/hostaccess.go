// Package hostaccess holds the switches for how this machine itself is used:
// whether other devices may open NoCapOS (a flag file both alfad and the cap
// CLI read and write), and NoCap Desktop, NoCapOS full screen on the
// server's own monitor (a systemd unit install.sh sets up).
package hostaccess

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
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

// ---- NoCap Desktop ----

// DesktopUnit is the systemd unit install.sh writes for NoCap Desktop.
const DesktopUnit = "nocapos-desktop.service"

// DesktopUnitPath is where it lives.
const DesktopUnitPath = "/etc/systemd/system/" + DesktopUnit

// DesktopEnvFile is written by the running session: KEY=VALUE lines naming
// the Wayland display (and X display when there is one) and the user, so
// native apps can be opened on this screen.
const DesktopEnvFile = "/run/nocapos/desktop.env"

// Runner runs a command and returns its combined output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// ExecRunner runs real commands.
func ExecRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// Desktop controls nocapos-desktop.service.
type Desktop struct {
	Run    Runner
	Exists func(string) bool
	// Native: NoCapOS runs on Linux as root outside a container.
	Native bool
}

// DesktopState is what Settings shows.
type DesktopState struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	Enabled   bool   `json:"enabled"`
	Active    bool   `json:"active"`
	Display   bool   `json:"display"` // a monitor is plugged in
}

// ErrDesktopUnavailable means NoCap Desktop isn't set up on this server.
var ErrDesktopUnavailable = errors.New("NoCap Desktop isn't set up on this server")

func (d *Desktop) exists(p string) bool {
	if d.Exists != nil {
		return d.Exists(p)
	}
	_, err := os.Stat(p)
	return err == nil
}

// State reports whether NoCap Desktop is set up, on and running.
func (d *Desktop) State(ctx context.Context) DesktopState {
	st := DesktopState{Display: DisplayConnected("/sys/class/drm")}
	switch {
	case !d.Native:
		st.Reason = "NoCap Desktop needs NoCapOS installed straight on Linux (not in Docker)."
		return st
	case !d.exists(DesktopUnitPath):
		st.Reason = "Not set up yet. Plug in a screen and run the installer again with --desktop: sudo bash install.sh --desktop"
		return st
	}
	st.Available = true
	out, _ := d.Run(ctx, "systemctl", "is-enabled", DesktopUnit)
	st.Enabled = strings.TrimSpace(string(out)) == "enabled"
	out, _ = d.Run(ctx, "systemctl", "is-active", DesktopUnit)
	st.Active = strings.TrimSpace(string(out)) == "active"
	return st
}

// Set turns NoCap Desktop on (enable + start) or off (disable + stop).
func (d *Desktop) Set(ctx context.Context, on bool) error {
	if !d.Native || !d.exists(DesktopUnitPath) {
		return ErrDesktopUnavailable
	}
	args := []string{"disable", "--now", DesktopUnit}
	if on {
		args = []string{"enable", "--now", DesktopUnit}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if out, err := d.Run(ctx, "systemctl", args...); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("systemctl %s: %s", args[0], msg)
	}
	if !on {
		// The desktop took over tty1; give the text console its login back.
		_, _ = d.Run(ctx, "systemctl", "--no-block", "start", "getty@tty1.service")
	}
	return nil
}

// DisplayConnected reports whether any graphics output under drmDir (normally
// /sys/class/drm) has a monitor plugged in.
func DisplayConnected(drmDir string) bool {
	matches, _ := filepath.Glob(filepath.Join(drmDir, "*", "status"))
	for _, m := range matches {
		if b, err := os.ReadFile(m); err == nil && strings.TrimSpace(string(b)) == "connected" {
			return true
		}
	}
	return false
}
