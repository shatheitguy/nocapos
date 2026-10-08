// Package terminal provides interactive shell sessions — into a Docker
// container (via exec) or the host (via a PTY on Linux) — bridged to the
// browser's Xterm.js over a WebSocket.
package terminal

import (
	"context"
	"errors"
	"io"
	"os"
	"os/user"

	"alfaos/alfad/internal/docker"
)

var (
	ErrHostUnsupported = errors.New("host terminal is only available on Linux")
	ErrBadTarget       = errors.New("invalid terminal target")
)

// Session is a live shell: read output, write input, resize, close.
type Session interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
}

type Service struct {
	docker    *docker.Client
	allowHost bool
	hostShell []string // resolved host shell command, e.g. ["/bin/bash"]
}

func NewService(dc *docker.Client, allowHost bool) *Service {
	return &Service{docker: dc, allowHost: allowHost, hostShell: detectHostShell()}
}

// HostAvailable reports whether a host terminal can be opened on this platform.
func (s *Service) HostAvailable() bool { return s.allowHost && hostSupported }

// HostUser is the account a host shell runs as (alfad's own), and whether
// that is root — the Terminal shows a warning badge for root host shells.
func (s *Service) HostUser() (name string, root bool) {
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	return name, os.Geteuid() == 0
}

// OpenContainer starts a shell in a container, auto-detecting bash or sh.
func (s *Service) OpenContainer(ctx context.Context, container string, cols, rows int) (Session, error) {
	shell := s.docker.DetectShell(ctx, container)
	conn, err := s.docker.Exec(ctx, container, []string{shell})
	if err != nil {
		return nil, err
	}
	// Apply the initial size (best effort; ignored before the shell draws).
	if cols > 0 && rows > 0 {
		_ = s.docker.ExecResize(ctx, conn.ExecID(), cols, rows)
	}
	return &containerSession{conn: conn, docker: s.docker}, nil
}

// OpenHost starts a shell on the host (Linux only).
func (s *Service) OpenHost(ctx context.Context, cols, rows int) (Session, error) {
	if !s.HostAvailable() {
		return nil, ErrHostUnsupported
	}
	return openHostPTY(s.hostShell, cols, rows)
}

// containerSession bridges a docker exec stream and resize endpoint.
type containerSession struct {
	conn   *docker.ExecConn
	docker *docker.Client
}

func (c *containerSession) Read(p []byte) (int, error)  { return c.conn.Read(p) }
func (c *containerSession) Write(p []byte) (int, error) { return c.conn.Write(p) }
func (c *containerSession) Close() error                { return c.conn.Close() }

func (c *containerSession) Resize(cols, rows int) error {
	ctx, cancel := context.WithTimeout(context.Background(), resizeTimeout)
	defer cancel()
	return c.docker.ExecResize(ctx, c.conn.ExecID(), cols, rows)
}
