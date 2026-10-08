//go:build linux

package terminal

import (
	"os"
	"os/exec"
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

func openHostPTY(shell []string, cols, rows int) (Session, error) {
	cmd := exec.Command(shell[0], shell[1:]...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ws := &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}
	if cols <= 0 || rows <= 0 {
		ws = &pty.Winsize{Cols: 80, Rows: 24}
	}
	ptmx, err := pty.StartWithSize(cmd, ws)
	if err != nil {
		return nil, err
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
		_ = h.cmd.Process.Kill()
		_, _ = h.cmd.Process.Wait()
	}
	return err
}
