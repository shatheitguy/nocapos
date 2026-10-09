package backup

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

// Installing restic with the system's package manager: only when NoCapOS runs
// natively as root on Linux (the normal install), never inside a container.

func canInstall() bool {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return false
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return false
	}
	_, ok := packageManager()
	return ok
}

func packageManager() ([][]string, bool) {
	has := func(b string) bool { _, err := exec.LookPath(b); return err == nil }
	switch {
	case has("apt-get"):
		return [][]string{{"apt-get", "update"}, {"apt-get", "install", "-y", "restic"}}, true
	case has("dnf"):
		return [][]string{{"dnf", "install", "-y", "restic"}}, true
	case has("yum"):
		return [][]string{{"yum", "install", "-y", "restic"}}, true
	case has("pacman"):
		return [][]string{{"pacman", "-Sy", "--noconfirm", "restic"}}, true
	case has("zypper"):
		return [][]string{{"zypper", "--non-interactive", "install", "restic"}}, true
	case has("apk"):
		return [][]string{{"apk", "add", "restic"}}, true
	}
	return nil, false
}

// InstallRestic installs restic from the distribution's repositories.
func (m *Manager) InstallRestic(ctx context.Context) error {
	if !canInstall() {
		return errors.New("install restic on the server yourself (e.g. sudo apt install restic)")
	}
	steps, _ := packageManager()
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
			return fmt.Errorf("%s: %v %s", strings.Join(step, " "), err, msg)
		}
	}
	m.Refind()
	_, err := m.tool()
	return err
}
