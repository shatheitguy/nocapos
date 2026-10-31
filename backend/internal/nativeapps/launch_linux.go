//go:build linux

package nativeapps

import (
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

// startOnScreen runs argv as the local session's user, on its display, in a
// session of its own (it outlives the request and alfad restarts). A launch
// that fails straight away reports flatpak's error.
func startOnScreen(env DesktopEnv, argv []string) error {
	acct, err := terminal.LookupAccount(env.User)
	if err != nil || acct.UID == 0 {
		return errors.New("the server's screen has no usable account")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append([]string{
		"HOME=" + acct.Home, "USER=" + acct.Username, "LOGNAME=" + acct.Username,
		"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=" + langOr("C.UTF-8"),
	}, env.Env()...)
	if bus := strings.TrimRight(env.XDGRuntimeDir, "/") + "/bus"; fileExists(bus) {
		cmd.Env = append(cmd.Env, "DBUS_SESSION_BUS_ADDRESS=unix:path="+bus)
	}
	cmd.Dir = acct.Home
	out := &tailBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:     true,
		Credential: &syscall.Credential{Uid: acct.UID, Gid: acct.GID, Groups: acct.Groups},
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			return errors.New(lastLine(out.String(), err.Error()))
		}
		return nil // handed off to an already-running instance
	case <-time.After(2 * time.Second):
		return nil // it's up; Wait keeps reaping it
	}
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
