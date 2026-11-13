//go:build !linux

package firewall

// hostSupported: the firewall is Linux's own ufw.
func hostSupported() (bool, string) {
	return false, "The firewall needs NoCapOS running on Linux"
}
