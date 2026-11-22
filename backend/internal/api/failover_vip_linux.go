//go:build linux

package api

import (
	"context"
	"fmt"
	"net/netip"
	"os/exec"
	"strings"
	"time"
)

// vipTake adds the pair's shared address to iface and tells the LAN
// (gratuitous ARP, when arping is installed).
func vipTake(ctx context.Context, cidr, iface string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ip", "addr", "add", cidr, "dev", iface).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "File exists") {
		return fmt.Errorf("ip addr add: %s", strings.TrimSpace(string(out)))
	}
	if pfx, err := netip.ParsePrefix(cidr); err == nil {
		if bin, err := exec.LookPath("arping"); err == nil {
			_ = exec.CommandContext(ctx, bin, "-U", "-c", "3", "-I", iface, pfx.Addr().String()).Run()
		}
	}
	return nil
}

// vipRelease takes the shared address off iface.
func vipRelease(ctx context.Context, cidr, iface string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ip", "addr", "del", cidr, "dev", iface).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "Cannot assign") {
		return fmt.Errorf("ip addr del: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
