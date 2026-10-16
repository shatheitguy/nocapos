//go:build linux

package terminal

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/creack/pty"
)

const (
	hostSupported = true
	resizeTimeout = 3 * time.Second
)

// detectHostShell prefers the user's $SHELL, then bash, then sh.
func detectHostShell() []string {
	if sh := os.Getenv("SHELL"); sh != "" {
		if _, err := os.Stat(sh); err == nil {
			return []string{sh}
		}
	}
	for _, sh := range []string{"/bin/bash", "/bin/sh"} {
		if _, err := os.Stat(sh); err == nil {
			return []string{sh}
		}
	}
	return []string{"/bin/sh"}
}

type hostSession struct {
	ptmx *os.File
	cmd  *exec.Cmd
}

// openHostPTY starts shell as NoCapOS's own account (acct nil), or as a
// login shell of acct: their uid, gid and groups, home, shell and a clean
// environment, the way a console login would.
func openHostPTY(shell []string, acct *Account, cols, rows int) (Session, error) {
	ws := &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}
	if cols <= 0 || rows <= 0 {
		ws = &pty.Winsize{Cols: 80, Rows: 24}
	}
	if acct == nil {
		cmd := exec.Command(shell[0], shell[1:]...)
		cmd.Env = append(os.Environ(), "TERM=xterm-256color")
		ptmx, err := pty.StartWithSize(cmd, ws)
		if err != nil {
			return nil, err
		}
		return &hostSession{ptmx: ptmx, cmd: cmd}, nil
	}

	sh := acct.Shell
	if sh == "" {
		sh = "/bin/sh"
	}
	if noLogin(sh) {
		return nil, ErrNoLogin
	}
	cmd := exec.Command(sh)
	cmd.Args = []string{"-" + filepath.Base(sh)} // a login shell: reads the profile, shows the usual prompt
	cmd.Dir = "/"
	if st, err := os.Stat(acct.Home); err == nil && st.IsDir() {
		cmd.Dir = acct.Home
	}
	lang := os.Getenv("LANG")
	if lang == "" {
		lang = "C.UTF-8"
	}
	cmd.Env = []string{
		"HOME=" + acct.Home, "USER=" + acct.Username, "LOGNAME=" + acct.Username, "SHELL=" + sh,
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "TERM=xterm-256color", "LANG=" + lang,
	}

	ptmx, tty, err := pty.Open()
	if err != nil {
		return nil, err
	}
	defer tty.Close()
	// The terminal device is theirs while they use it, as after a login.
	_ = tty.Chown(int(acct.UID), -1)
	if err := pty.Setsize(ptmx, ws); err != nil {
		ptmx.Close()
		return nil, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, Setctty: true,
		Credential: &syscall.Credential{Uid: acct.UID, Gid: acct.GID, Groups: acct.Groups},
	}
	if err := cmd.Start(); err != nil {
		ptmx.Close()
		return nil, errors.New("couldn't start a shell as " + acct.Username + ": " + err.Error())
	}
	return &hostSession{ptmx: ptmx, cmd: cmd}, nil
}

func (h *hostSession) Read(p []byte) (int, error)  { return h.ptmx.Read(p) }
func (h *hostSession) Write(p []byte) (int, error) { return h.ptmx.Write(p) }

func (h *hostSession) Resize(cols, rows int) error {
	return pty.Setsize(h.ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (h *hostSession) Close() error {
	err := h.ptmx.Close()
	if h.cmd.Process != nil {
		// The whole session (the shell and what it started) ends with it.
		_ = syscall.Kill(-h.cmd.Process.Pid, syscall.SIGHUP)
		_ = h.cmd.Process.Kill()
		_, _ = h.cmd.Process.Wait()
	}
	return err
}
