// Package virt tells whether NoCapOS runs inside a system container (a whole
// Linux system with its own init, such as an LXC container on a hypervisor),
// as opposed to on a machine of its own or inside an app container (Docker,
// Podman: config.InContainer).
//
// In a system container NoCapOS is still the system: it runs as root under
// systemd, manages its accounts, shares, apps and firewall. What it can't do
// is touch the hardware the host keeps for itself: raw disks (ZFS pools,
// formatting, spin-down, a mirrored boot drive), a screen (NoCap Desktop),
// Wi-Fi radios, and virtual machines unless /dev/kvm is passed through.
// Features ask System() and explain themselves instead of failing.
package virt

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// EnvOverride forces the answer (e.g. ALFA_CONTAINER=lxc to preview the
// container behaviour, ALFA_CONTAINER=none to switch it off).
const EnvOverride = "ALFA_CONTAINER"

// systemKinds are container managers that run a whole system with its own
// init (systemd's names for them, as in systemd-detect-virt --container).
var systemKinds = map[string]bool{
	"lxc": true, "lxc-libvirt": true, "systemd-nspawn": true, "openvz": true, "incus": true,
}

// Detect reads the container kind from a root filesystem (root is "/" on a
// real system, a test directory in tests). It returns "" outside a
// container, and for app containers (Docker, Podman), which config.InContainer
// handles.
func Detect(root string) string {
	if v := os.Getenv(EnvOverride); v != "" {
		if v == "none" || v == "0" || v == "false" {
			return ""
		}
		return v
	}
	for _, f := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(filepath.Join(root, f)); err == nil {
			return ""
		}
	}
	// systemd (PID 1) writes the container manager's name here when it
	// starts inside one.
	if b, err := os.ReadFile(filepath.Join(root, "run/systemd/container")); err == nil {
		if k := kind(strings.TrimSpace(string(b))); k != "" {
			return k
		}
	}
	// Otherwise the manager's own hint in PID 1's environment (readable by
	// root, which alfad is when it manages the system).
	if b, err := os.ReadFile(filepath.Join(root, "proc/1/environ")); err == nil {
		for _, kv := range bytes.Split(b, []byte{0}) {
			if v, ok := strings.CutPrefix(string(kv), "container="); ok {
				return kind(v)
			}
		}
	}
	return ""
}

// kind keeps system container managers and drops app containers.
func kind(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if systemKinds[v] {
		return v
	}
	return ""
}

var detected = sync.OnceValue(func() string { return Detect("/") })

// Kind is this system's container kind ("lxc", "systemd-nspawn", ...) or ""
// when NoCapOS isn't in a system container. Read once per process.
func Kind() string { return detected() }

// System reports whether NoCapOS runs inside a system container.
func System() bool { return Kind() != "" }

// Explanations shown by the features that can't work in a system container.
const (
	DisksReason = "NoCapOS is running in a system container, so the host keeps the physical disks. " +
		"Create pools, format disks and set spin-down on the host, then give this container storage " +
		"(a bigger root disk or a mount point) and add it to Files."
	BootMirrorReason = "NoCapOS is running in a system container: its system disk is a volume on the host, " +
		"so protect it there (a mirrored or backed-up host storage) and with the host's container backups."
	DesktopReason = "NoCap Desktop draws on a screen plugged into the server. NoCapOS is running in a system " +
		"container, which has no screen, so use NoCapOS from a browser."
	NetworkReason = "NoCapOS is running in a system container, so its network (and any Wi-Fi) is set up on the host. " +
		"Change the container's network settings there."
	VMReason = "NoCapOS is running in a system container without access to hardware virtualization. " +
		"To run virtual machines here, pass /dev/kvm through to the container on the host, or use the host's own virtual machines."
)
