package syspkg

import (
	"os"
	"os/exec"
	"regexp"
)

// Feature is a group of host packages a NoCapOS feature needs. The installer
// (deploy/install.sh, PKG_TABLE) installs the same table during install; the
// Packages app installs it again as a repair path. A test keeps the two equal.
type Feature struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Bins are the programs that show the feature is installed.
	Bins []string `json:"-"`
	// Packages per package manager; a manager without an entry can't install it.
	Packages Names `json:"-"`
}

// Features lists the host packages NoCapOS installs, in install order.
var Features = []Feature{
	{
		ID: "virtualization", Title: "Virtual machines (QEMU/KVM, libvirt, UEFI, TPM)", Bins: []string{"virsh"},
		Packages: Names{
			"apt":    {"qemu-system-x86", "qemu-utils", "libvirt-daemon-system", "libvirt-clients", "ovmf", "swtpm", "swtpm-tools"},
			"dnf":    {"qemu-kvm", "qemu-img", "libvirt", "edk2-ovmf", "swtpm", "swtpm-tools"},
			"pacman": {"qemu-base", "libvirt", "edk2-ovmf", "swtpm", "dnsmasq"},
			"zypper": {"qemu-kvm", "qemu-tools", "libvirt", "qemu-ovmf-x86_64", "swtpm"},
		},
	},
	{
		ID: "sharing", Title: "File sharing and network drives (Samba, SMB and NFS clients)", Bins: []string{"smbd", "mount.cifs"},
		Packages: Names{
			"apt":    {"samba", "smbclient", "cifs-utils", "nfs-common"},
			"dnf":    {"samba", "samba-client", "cifs-utils", "nfs-utils"},
			"pacman": {"samba", "smbclient", "cifs-utils", "nfs-utils"},
			"zypper": {"samba", "samba-client", "cifs-utils", "nfs-client"},
		},
	},
	{
		ID: "remote-desktop", Title: "Remote Desktop engine (guacd with RDP and VNC)", Bins: []string{"guacd"},
		Packages: Names{
			"apt": {"guacd", "libguac-client-rdp0", "libguac-client-vnc0"},
			"dnf": {"guacd", "libguac-client-rdp", "libguac-client-vnc"},
		},
	},
	{
		ID: "mdns", Title: "Local name nocap.local (Avahi mDNS)", Bins: []string{"avahi-daemon"},
		Packages: Names{
			"apt":    {"avahi-daemon", "libnss-mdns"},
			"dnf":    {"avahi", "nss-mdns"},
			"pacman": {"avahi", "nss-mdns"},
			"zypper": {"avahi", "nss-mdns"},
		},
	},
	{
		ID: "smart", Title: "Disk health (smartmontools)", Bins: []string{"smartctl"},
		Packages: Names{"apt": {"smartmontools"}, "dnf": {"smartmontools"}, "pacman": {"smartmontools"}, "zypper": {"smartmontools"}},
	},
	{
		// Ubuntu's kernels carry the module; on Debian and Raspberry Pi OS it's
		// built with DKMS from contrib (ZFSDebianSteps, install.sh's
		// install_zfs_debian).
		ID: "zfs", Title: "ZFS pools (OpenZFS)", Bins: []string{"zfs"},
		Packages: Names{"apt": {"zfsutils-linux"}},
	},
	{
		ID: "media", Title: "Video previews (FFmpeg)", Bins: []string{"ffmpeg"},
		Packages: Names{"apt": {"ffmpeg"}, "dnf": {"ffmpeg-free"}, "pacman": {"ffmpeg"}, "zypper": {"ffmpeg"}},
	},
	{
		ID: "backup", Title: "Backups and cloud imports (restic, rclone)", Bins: []string{"restic", "rclone"},
		Packages: Names{"apt": {"restic", "rclone"}, "dnf": {"restic", "rclone"}, "pacman": {"restic", "rclone"}, "zypper": {"restic", "rclone"}},
	},
}

// FeatureState is a feature as the Packages app shows it.
type FeatureState struct {
	Feature
	Installed  bool `json:"installed"`
	CanInstall bool `json:"can_install"`
}

// FeatureStates reports which features are installed on this host.
func FeatureStates() []FeatureState {
	mgr := Manager()
	out := make([]FeatureState, 0, len(Features))
	for _, f := range Features {
		st := FeatureState{Feature: f, Installed: true}
		for _, b := range f.Bins {
			if !hasBin(b) {
				st.Installed = false
			}
		}
		st.CanInstall = Native() && len(f.Packages[mgr]) > 0
		out = append(out, st)
	}
	return out
}

// FeatureByID finds a feature.
func FeatureByID(id string) (Feature, bool) {
	for _, f := range Features {
		if f.ID == id {
			return f, true
		}
	}
	return Feature{}, false
}

// hasBin looks in PATH and the sbin folders (smbd, avahi-daemon live there).
func hasBin(b string) bool {
	if _, err := exec.LookPath(b); err == nil {
		return true
	}
	for _, d := range []string{"/usr/sbin/", "/sbin/", "/usr/local/sbin/"} {
		if _, err := os.Stat(d + b); err == nil {
			return true
		}
	}
	return false
}

var ubuntuRe = regexp.MustCompile(`(?m)^ID="?ubuntu"?$`)

func isUbuntu() bool {
	b, err := os.ReadFile("/etc/os-release")
	return err == nil && ubuntuRe.Match(b)
}
