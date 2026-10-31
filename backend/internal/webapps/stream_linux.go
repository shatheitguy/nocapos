//go:build linux

package webapps

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

//go:embed app-stream.sh
var appStreamScript string

// startStream runs app-stream.sh for a session, as the session's account,
// in its own process group, and waits for its noVNC bridge to answer.
func startStream(ctx context.Context, s *streamSession, logDir string) (*os.Process, <-chan struct{}, error) {
	if len(s.spec.Command) == 0 {
		return nil, nil, errors.New("nothing to run")
	}
	if portOpen(s.slot.WebPort) {
		return nil, nil, fmt.Errorf("port %d is already in use on this server", s.slot.WebPort)
	}
	dir := filepath.Join(logDir, "streams")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, err
	}
	logPath := filepath.Join(dir, s.id+".log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, nil, err
	}

	// bash -c <script> <$0> <app argv…>: the argv never passes through a shell string.
	cmd := exec.Command("bash", append([]string{"-c", appStreamScript, "nocap-app-stream"}, s.spec.Command...)...)
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"LANG=" + envOr("LANG", "C.UTF-8"),
		"NOCAP_DISPLAY=" + s.slot.Display,
		"NOCAP_VNC_PORT=" + strconv.Itoa(s.slot.VNCPort),
		"NOCAP_WEB_PORT=" + strconv.Itoa(s.slot.WebPort),
	}
	attr := &syscall.SysProcAttr{Setpgid: true} // stop takes the whole group down
	if a := s.spec.RunAs; a != nil && a.UID != 0 {
		runtime := runtimeDir(a)
		env = append(env, "HOME="+a.Home, "USER="+a.Username, "LOGNAME="+a.Username, "XDG_RUNTIME_DIR="+runtime)
		attr.Credential = &syscall.Credential{Uid: a.UID, Gid: a.GID, Groups: a.Groups}
		cmd.Dir = a.Home
	} else {
		home := s.spec.Home
		if home == "" {
			home = filepath.Join(logDir, "app-home")
		}
		if err := os.MkdirAll(home, 0o700); err != nil {
			logf.Close()
			return nil, nil, err
		}
		runtime := filepath.Join(home, ".runtime")
		_ = os.MkdirAll(runtime, 0o700)
		env = append(env, "HOME="+home, "USER=root", "LOGNAME=root", "XDG_RUNTIME_DIR="+runtime)
		cmd.Dir = home
	}
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = attr
	if err := cmd.Start(); err != nil {
		logf.Close()
		return nil, nil, fmt.Errorf("could not start %s: %w", s.spec.Title, err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		logf.Close()
		close(done)
	}()
	if err := waitForPort(ctx, s.slot.WebPort, done); err != nil {
		stopProcess(cmd.Process)
		out, _ := os.ReadFile(logPath)
		return nil, nil, fmt.Errorf("%s didn't start: %s", s.spec.Title, tail(out, 3))
	}
	return cmd.Process, done, nil
}

// runtimeDir is the account's XDG runtime dir: its login one when it has
// one, else a private one NoCapOS makes for it.
func runtimeDir(a *RunAs) string {
	login := "/run/user/" + strconv.FormatUint(uint64(a.UID), 10)
	if st, err := os.Stat(login); err == nil && st.IsDir() {
		return login
	}
	// Under root-owned /run/nocapos (not world-writable /tmp), so nobody can
	// plant a symlink for root to chown.
	parent := "/run/nocapos/xdg"
	_ = os.MkdirAll(parent, 0o755)
	dir := filepath.Join(parent, strconv.FormatUint(uint64(a.UID), 10))
	_ = os.Mkdir(dir, 0o700)
	if st, err := os.Lstat(dir); err == nil && st.IsDir() {
		_ = os.Lchown(dir, int(a.UID), int(a.GID))
		_ = os.Chmod(dir, 0o700)
	}
	return dir
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
