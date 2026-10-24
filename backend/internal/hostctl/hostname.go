package hostctl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

// Host name: what the machine calls itself on the network (and as
// <name>.local). Changing it takes effect right away, no restart.

// Paths and lookups, replaceable in tests.
var (
	hostsFile    = "/etc/hosts"
	hostnameFile = "/etc/hostname"
	lookPath     = exec.LookPath
	canRename    = canRenameHost
)

var labelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidHostname checks a name against RFC 1123: dot-separated labels of
// letters, digits and hyphens (not at either end), each up to 63
// characters, 253 in all. Upper case is accepted and lowered.
func ValidHostname(name string) (string, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" || len(n) > 253 {
		return "", errors.New("enter a name of 1 to 63 letters, digits or hyphens")
	}
	for _, l := range strings.Split(n, ".") {
		if !labelRe.MatchString(l) {
			return "", errors.New("use only letters, digits and hyphens (not at the start or end), e.g. living-room-nas")
		}
	}
	if strings.Trim(n, "0123456789.") == "" {
		return "", errors.New("the name can't be only digits")
	}
	return n, nil
}

func canRenameHost() error {
	if runtime.GOOS != "linux" || InContainer() {
		return ErrUnsupported
	}
	return nil
}

// SetHostname renames the machine live: hostnamectl when systemd is there
// (otherwise /etc/hostname and `hostname`), the 127.0.1.1 line in
// /etc/hosts so sudo and local lookups keep working, and Avahi so
// <name>.local follows.
func SetHostname(ctx context.Context, name string) (string, error) {
	if err := canRename(); err != nil {
		return "", err
	}
	n, err := ValidHostname(name)
	if err != nil {
		return "", err
	}
	// /etc/hosts first, so the new name resolves the moment it's in use.
	if b, err := os.ReadFile(hostsFile); err == nil || errors.Is(err, os.ErrNotExist) {
		if next := hostsWithName(string(b), n); next != string(b) {
			if err := writeAtomic(hostsFile, []byte(next), 0o644); err != nil {
				return "", fmt.Errorf("couldn't update %s: %w", hostsFile, err)
			}
		}
	}
	set := false
	if _, err := lookPath("hostnamectl"); err == nil {
		if _, err := runCmd(ctx, "hostnamectl", "set-hostname", n); err == nil {
			set = true
		}
	}
	if !set {
		if err := writeAtomic(hostnameFile, []byte(n+"\n"), 0o644); err != nil {
			return "", fmt.Errorf("couldn't write %s: %w", hostnameFile, err)
		}
		if out, err := runCmd(ctx, "hostname", n); err != nil {
			return "", fmt.Errorf("couldn't set the host name: %s", out)
		}
	}
	followAvahi(ctx, n)
	return n, nil
}

// followAvahi tells a running Avahi the new name (restarting it when it
// can't be told), so <name>.local answers right away.
func followAvahi(ctx context.Context, name string) {
	if _, err := lookPath("avahi-daemon"); err != nil {
		return
	}
	short, _, _ := strings.Cut(name, ".")
	if _, err := lookPath("avahi-set-host-name"); err == nil {
		if _, err := runCmd(ctx, "avahi-set-host-name", short); err == nil {
			return
		}
	}
	_, _ = runCmd(ctx, "systemctl", "try-restart", "avahi-daemon")
}

// hostsWithName points the 127.0.1.1 line at name (Debian's convention for
// the machine's own name), adding one after localhost when missing.
func hostsWithName(content, name string) string {
	short, _, _ := strings.Cut(name, ".")
	entry := "127.0.1.1\t" + name
	if short != name {
		entry += " " + short
	}
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if content == "" {
		lines = nil
	}
	for i, l := range lines {
		if f := strings.Fields(l); len(f) > 0 && f[0] == "127.0.1.1" {
			lines[i] = entry
			return strings.Join(lines, "\n") + "\n"
		}
	}
	at := len(lines)
	for i, l := range lines {
		if f := strings.Fields(l); len(f) > 0 && f[0] == "127.0.0.1" {
			at = i + 1
			break
		}
	}
	lines = append(lines[:at], append([]string{entry}, lines[at:]...)...)
	return strings.Join(lines, "\n") + "\n"
}

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".nocapos-tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		// /etc/hosts is often a bind mount (containers) where rename fails.
		_ = os.Remove(tmp)
		return os.WriteFile(path, data, perm)
	}
	return nil
}
