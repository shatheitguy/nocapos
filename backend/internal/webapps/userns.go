package webapps

import (
	"errors"
	"os"
	"strings"
)

// Linux apps from Flathub run inside bubblewrap (bwrap), which needs
// unprivileged user namespaces unless it's installed setuid root. Some
// kernels and distributions turn those off; then every app fails with
// "bwrap: … Operation not permitted". SandboxCheck finds out why, in plain
// words, with the fix.

// SandboxCheck is whether Linux app sandboxes can start here.
type SandboxCheck struct {
	OK bool `json:"ok"`
	// Problem says what's wrong, Fix what to run (both empty when OK).
	Problem string `json:"problem,omitempty"`
	Fix     string `json:"fix,omitempty"`
}

// Files CheckUserNamespaces reads.
const (
	usernsClonePath    = "/proc/sys/kernel/unprivileged_userns_clone"
	usernsMaxPath      = "/proc/sys/user/max_user_namespaces"
	usernsAppArmorPath = "/proc/sys/kernel/apparmor_restrict_unprivileged_userns"
)

// bwrapProfiles are AppArmor profiles that let bwrap make namespaces while
// the AppArmor restriction is on.
var bwrapProfiles = []string{"/etc/apparmor.d/bwrap-userns-restrict", "/etc/apparmor.d/flatpak", "/etc/apparmor.d/bwrap"}

// CheckUserNamespaces reads the kernel switches (read is os.ReadFile on a
// real host). bwrapSetuid: bwrap is setuid root and needs none of them.
func CheckUserNamespaces(read func(string) ([]byte, error), bwrapSetuid bool) SandboxCheck {
	if bwrapSetuid {
		return SandboxCheck{OK: true}
	}
	val := func(p string) (string, bool) {
		b, err := read(p)
		if err != nil {
			return "", false
		}
		return strings.TrimSpace(string(b)), true
	}
	if v, ok := val(usernsClonePath); ok && v == "0" {
		return SandboxCheck{
			Problem: "This kernel has unprivileged user namespaces turned off (kernel.unprivileged_userns_clone = 0), so Linux apps can't start their sandbox.",
			Fix:     "In a terminal on the server run: echo kernel.unprivileged_userns_clone=1 | sudo tee /etc/sysctl.d/90-nocapos-apps.conf && sudo sysctl --system",
		}
	}
	if v, ok := val(usernsMaxPath); ok && v == "0" {
		return SandboxCheck{
			Problem: "This kernel allows no user namespaces (user.max_user_namespaces = 0), so Linux apps can't start their sandbox.",
			Fix:     "In a terminal on the server run: echo user.max_user_namespaces=15000 | sudo tee /etc/sysctl.d/90-nocapos-apps.conf && sudo sysctl --system",
		}
	}
	if v, ok := val(usernsAppArmorPath); ok && v == "1" {
		for _, p := range bwrapProfiles {
			if _, err := read(p); err == nil || !errors.Is(err, os.ErrNotExist) {
				return SandboxCheck{OK: true} // a profile lets bwrap through
			}
		}
		return SandboxCheck{
			Problem: "AppArmor blocks unprivileged user namespaces (kernel.apparmor_restrict_unprivileged_userns = 1) and there's no AppArmor profile for bwrap, so Linux apps can't start their sandbox.",
			Fix:     "In a terminal on the server run: sudo apt install apparmor && sudo systemctl reload apparmor (it brings a bwrap profile), or turn the restriction off: echo kernel.apparmor_restrict_unprivileged_userns=0 | sudo tee /etc/sysctl.d/90-nocapos-apps.conf && sudo sysctl --system",
		}
	}
	return SandboxCheck{OK: true}
}

// sandboxHint adds the fix to a stream's error when the log shows the
// sandbox couldn't start and the kernel explains why.
func sandboxHint(log string, chk SandboxCheck) string {
	if chk.OK {
		return ""
	}
	l := strings.ToLower(log)
	if strings.Contains(l, "bwrap") || strings.Contains(l, "namespace") {
		return chk.Problem + " Fix: " + chk.Fix
	}
	return ""
}
