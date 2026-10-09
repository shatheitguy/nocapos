// Package syspkg installs system packages with the distribution's package
// manager, for features that need a host tool (cifs-utils, NFS, Samba).
// Only when NoCapOS runs natively as root on Linux, never in a container.
package syspkg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Names maps a package manager ("apt", "dnf", "pacman", "zypper", "apk") to
// the package names to install with it.
type Names map[string][]string

func has(bin string) bool { _, err := exec.LookPath(bin); return err == nil }

// Manager returns the package manager in use, or "".
func Manager() string {
	for _, m := range []struct{ name, bin string }{{"apt", "apt-get"}, {"dnf", "dnf"}, {"dnf", "yum"}, {"pacman", "pacman"}, {"zypper", "zypper"}, {"apk", "apk"}} {
		if has(m.bin) {
			return m.name
		}
	}
	return ""
}

// Native reports whether NoCapOS runs as root on Linux outside a container.
func Native() bool {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return false
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return false
	}
	return true
}

// CanInstall reports whether Install can work here.
func CanInstall() bool { return Native() && Manager() != "" }

// Install installs the packages for this system's package manager.
func Install(ctx context.Context, names Names) error {
	if !Native() {
		return errors.New("NoCapOS can only install system packages when it runs on Linux as root")
	}
	mgr := Manager()
	pkgs := names[mgr]
	if mgr == "" || len(pkgs) == 0 {
		return errors.New("no supported package manager found; install the package yourself")
	}
	var steps [][]string
	switch mgr {
	case "apt":
		steps = [][]string{{"apt-get", "update"}, append([]string{"apt-get", "install", "-y"}, pkgs...)}
	case "dnf":
		bin := "dnf"
		if !has(bin) {
			bin = "yum"
		}
		steps = [][]string{append([]string{bin, "install", "-y"}, pkgs...)}
	case "pacman":
		steps = [][]string{append([]string{"pacman", "-Sy", "--noconfirm"}, pkgs...)}
	case "zypper":
		steps = [][]string{append([]string{"zypper", "--non-interactive", "install"}, pkgs...)}
	case "apk":
		steps = [][]string{append([]string{"apk", "add"}, pkgs...)}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	for _, step := range steps {
		cmd := exec.CommandContext(ctx, step[0], step[1:]...)
		cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			msg := strings.TrimSpace(out.String())
			if i := strings.LastIndex(msg, "\n"); i >= 0 {
				msg = msg[i+1:]
			}
			return fmt.Errorf("%s failed: %s", strings.Join(step, " "), msg)
		}
	}
	return nil
}
