//go:build linux

package firewall

import "os"

// hostSupported: ufw needs root on the machine itself, not in a container.
func hostSupported() (bool, string) {
	if os.Geteuid() != 0 {
		return false, "The firewall needs NoCapOS running as root"
	}
	for _, p := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(p); err == nil {
			return false, "NoCapOS is running in a container, so it can't change the host's firewall"
		}
	}
	return true, ""
}
