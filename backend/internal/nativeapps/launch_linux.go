//go:build linux

package nativeapps

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"alfaos/alfad/internal/terminal"
	"alfaos/alfad/internal/webapps"
)

// startOnScreen runs argv as the local session's user, on its display. It
// runs in a transient systemd unit of its own, outside nocapos.service's
// sandbox (Flatpak's bubblewrap can't start inside it), and outlives the
// request and alfad restarts. A launch that fails straight away reports
// the app's error.
func startOnScreen(env DesktopEnv, argv []string) error {
	acct, err := terminal.LookupAccount(env.User)
	if err != nil || acct.UID == 0 {
		return errors.New("the server's screen has no usable account")
	}
	ra := &webapps.RunAs{Username: acct.Username, UID: acct.UID, GID: acct.GID, Groups: acct.Groups, Home: acct.Home}
	unit := ScreenUnit(env, ra, argv, randomID(), langOr("C.UTF-8"), fileExists)
	sdRun := systemdRun()
	var cmd *exec.Cmd
	if sdRun != "" {
		cmd = exec.Command(sdRun, webapps.SystemdRunArgs(unit)...)
	} else {
		cmd = exec.Command(argv[0], argv[1:]...)
		cmd.Env = unit.Env
		cmd.Dir = acct.Home
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Setsid:     true,
			Credential: &syscall.Credential{Uid: acct.UID, Gid: acct.GID, Groups: acct.Groups},
		}
	}
	out := &tailBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			msg := out.String()
			if sdRun != "" {
				msg += "\n" + unitLog(unit.Unit)
			}
			return errors.New(lastLine(msg, err.Error()))
		}
		return nil // handed off to an already-running instance
	case <-time.After(2 * time.Second):
		return nil // it's up; Wait keeps reaping it
	}
}

// unitLog is the last lines a finished unit wrote to the journal.
func unitLog(unit string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "journalctl", "-u", unit, "-n", "5", "-o", "cat", "--no-pager").Output()
	return string(out)
}

func systemdRun() string {
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return ""
	}
	p, err := exec.LookPath("systemd-run")
	if err != nil {
		return ""
	}
	return p
}

func randomID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// lookupAccount is a person's own Linux account for streamed apps.
func lookupAccount(username string) *webapps.RunAs {
	if username == "" {
		return nil
	}
	a, err := terminal.LookupAccount(username)
	if err != nil {
		return nil
	}
	return &webapps.RunAs{Username: a.Username, UID: a.UID, GID: a.GID, Groups: a.Groups, Home: a.Home}
}

var appsAccountMu sync.Mutex

// ensureAppsAccount finds AppsUser, making it (a system account that can't
// log in) when it's missing. install.sh makes it too.
func ensureAppsAccount() (*webapps.RunAs, error) {
	appsAccountMu.Lock()
	defer appsAccountMu.Unlock()
	if a := lookupAccount(AppsUser); a != nil {
		return a, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "useradd", AppsAccountArgs()...).CombinedOutput()
	if a := lookupAccount(AppsUser); a != nil {
		return a, nil
	}
	if err != nil {
		return nil, errors.New("couldn't create the " + AppsUser + " account for Linux apps: " + lastLine(string(out), err.Error()))
	}
	return nil, webapps.ErrNoAccount
}

// tailBuffer keeps the last few KB an app prints (it may run for days).
type tailBuffer struct {
	mu sync.Mutex
	b  []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > 4096 {
		t.b = t.b[len(t.b)-4096:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func langOr(def string) string {
	if v := os.Getenv("LANG"); v != "" {
		return v
	}
	return def
}

func lastLine(s, def string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
		return l
	}
	return def
}
