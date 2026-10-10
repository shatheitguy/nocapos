//go:build linux

package rdp

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
)

// installGuacd installs the native RDP engine where the distro packages it
// (Fedora: guacd + libguac-client-rdp; Ubuntu releases that still ship it).
// The VNC client plugin comes too, for Virtual Desk's virtual machine screens.
const installGuacd = `set -e
as_root() { if [ "$(id -u)" = 0 ]; then "$@"; elif command -v sudo >/dev/null && sudo -n true 2>/dev/null; then sudo -n "$@"; else exit 3; fi; }
command -v guacd >/dev/null && exit 0
if command -v dnf >/dev/null; then as_root dnf install -y -q guacd libguac-client-rdp libguac-client-vnc || as_root dnf install -y -q guacd libguac-client-rdp
elif command -v apt-get >/dev/null; then
  export DEBIAN_FRONTEND=noninteractive
  apt-cache show guacd >/dev/null 2>&1 || exit 4
  as_root apt-get install -y -q guacd libguac-client-rdp0 libguac-client-vnc0 || as_root apt-get install -y -q guacd libguac-client-rdp0 || as_root apt-get install -y -q guacd
else exit 4; fi
command -v guacd >/dev/null`

func (g *Guacd) native(ctx context.Context) (string, error) {
	if _, err := exec.LookPath("guacd"); err != nil {
		g.set(PhasePreparing, "Installing the remote desktop engine (guacd)…")
		if out, err := exec.CommandContext(ctx, "bash", "-c", installGuacd).CombinedOutput(); err != nil {
			return "", errors.New("native guacd install failed: " + strings.TrimSpace(string(out)))
		}
	}
	addr := "127.0.0.1:" + strconv.Itoa(guacdPort)
	// -f: stay in the foreground so it lives (and dies) with alfad.
	cmd := exec.Command("guacd", "-f", "-b", "127.0.0.1", "-l", strconv.Itoa(guacdPort))
	if err := cmd.Start(); err != nil {
		return "", err
	}
	go func() { _ = cmd.Wait() }()
	return waitReachable(addr)
}
