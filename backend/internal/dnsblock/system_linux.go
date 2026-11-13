//go:build linux

package dnsblock

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	resolvedDropIns = "/etc/systemd/resolved.conf.d"
	freeDropIn      = resolvedDropIns + "/nocap-dns-port.conf"
	useDropIn       = resolvedDropIns + "/nocap-dns-use.conf"
	resolvConf      = "/etc/resolv.conf"
	resolvedUpConf  = "/run/systemd/resolve/resolv.conf"
)

func sysRun(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			return errors.New(s)
		}
	}
	return err
}

func resolvedActive() bool {
	return sysRun("systemctl", "is-active", "--quiet", "systemd-resolved") == nil
}

func canManage() bool {
	if os.Geteuid() != 0 {
		return false
	}
	for _, p := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(p); err == nil {
			return false
		}
	}
	return true
}

// stubListening: something holds port 53 on 127.0.0.53 or 127.0.0.54
// (the system resolver's local listener).
func stubListening() bool {
	b, err := os.ReadFile("/proc/net/udp")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) > 1 && (f[1] == "3500007F:0035" || f[1] == "3600007F:0035") {
			return true
		}
	}
	return false
}

func systemPort53(dir string) PortInfo {
	_, err := os.Stat(freeDropIn)
	return PortInfo{Stub: stubListening(), Freed: err == nil, CanManage: canManage()}
}

func systemUpstreams() []string { return readNameservers(resolvedUpConf, resolvConf) }

func writeFile(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".nocap-tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// relink points /etc/resolv.conf at target, keeping what it was in saved.
func relink(target, saved string) error {
	if cur, err := os.Readlink(resolvConf); err == nil {
		if _, err := os.Stat(saved); err != nil {
			if err := os.WriteFile(saved, []byte(cur), 0o600); err != nil {
				return err
			}
		}
	}
	tmp := resolvConf + ".nocap-tmp"
	os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, resolvConf)
}

func freePort53(dir string) error {
	if !canManage() {
		return errors.New("NoCapOS can only change this when it runs on Linux as root")
	}
	if !resolvedActive() {
		return errors.New("the system resolver isn't running, so port 53 is already free (or another program holds it)")
	}
	if err := writeFile(freeDropIn, "# Written by NoCapOS's DNS blocker: it answers on port 53 instead.\n[Resolve]\nDNSStubListener=no\n"); err != nil {
		return err
	}
	if t, err := os.Readlink(resolvConf); err == nil && strings.HasSuffix(t, "stub-resolv.conf") {
		if err := relink(resolvedUpConf, filepath.Join(dir, "resolv-link")); err != nil {
			return err
		}
	}
	return sysRun("systemctl", "restart", "systemd-resolved")
}

func restorePort53(dir string) error {
	if !canManage() {
		return errors.New("NoCapOS can only change this when it runs on Linux as root")
	}
	if err := os.Remove(freeDropIn); err != nil && !os.IsNotExist(err) {
		return err
	}
	saved := filepath.Join(dir, "resolv-link")
	if b, err := os.ReadFile(saved); err == nil {
		tmp := resolvConf + ".nocap-tmp"
		os.Remove(tmp)
		if err := os.Symlink(strings.TrimSpace(string(b)), tmp); err == nil {
			if err := os.Rename(tmp, resolvConf); err != nil {
				return err
			}
			os.Remove(saved)
		}
	}
	if resolvedActive() {
		return sysRun("systemctl", "restart", "systemd-resolved")
	}
	return nil
}

// useOnServer points this machine's own lookups at the blocker (on) or
// back at what they were (off). Turning it off when it was never on does
// nothing.
func useOnServer(dir string, on bool) error {
	if !canManage() {
		if on {
			return errors.New("NoCapOS can only change this when it runs on Linux as root")
		}
		return nil
	}
	backup := filepath.Join(dir, "resolv.conf.backup")
	if on {
		if resolvedActive() {
			if err := writeFile(useDropIn, "# Written by NoCapOS's DNS blocker: this machine asks it too.\n[Resolve]\nDNS=127.0.0.1\nDomains=~.\n"); err != nil {
				return err
			}
			return sysRun("systemctl", "restart", "systemd-resolved")
		}
		if _, err := os.Stat(backup); err != nil {
			b, err := os.ReadFile(resolvConf)
			if err != nil {
				return err
			}
			if err := os.WriteFile(backup, b, 0o600); err != nil {
				return err
			}
		}
		if _, err := os.Readlink(resolvConf); err == nil {
			os.Remove(resolvConf) // a link to a generated file: replace it with our own
		}
		return writeFile(resolvConf, "# Written by NoCapOS's DNS blocker (Shield). Turn off \"Use on this server\" to undo.\nnameserver 127.0.0.1\n")
	}
	changed := false
	if _, err := os.Stat(useDropIn); err == nil {
		if err := os.Remove(useDropIn); err != nil {
			return err
		}
		changed = true
	}
	if b, err := os.ReadFile(backup); err == nil {
		if err := writeFile(resolvConf, string(b)); err != nil {
			return err
		}
		os.Remove(backup)
	}
	if changed && resolvedActive() {
		return sysRun("systemctl", "restart", "systemd-resolved")
	}
	return nil
}
