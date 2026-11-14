//go:build linux

package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// ServeClipSocket serves the desktop clipboard agent on a Unix socket until
// ctx ends. Anyone on this machine may connect; each connection is the
// Linux account the kernel says it is (SO_PEERCRED), and reaches only that
// account's own clipboard.
func (s *Server) ServeClipSocket(ctx context.Context, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_ = os.Remove(path) // left over from the last run
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o666); err != nil {
		_ = ln.Close()
		return err
	}
	srv := &http.Server{
		Handler:           s.ClipAgentHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
		ConnContext: func(c context.Context, conn net.Conn) context.Context {
			if name := peerAccount(conn); name != "" {
				return WithClipPeer(c, name)
			}
			return c
		},
	}
	go func() {
		<-ctx.Done()
		sh, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sh)
		_ = os.Remove(path)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// peerAccount is the Linux account on the other end of a Unix socket.
func peerAccount(conn net.Conn) string {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return ""
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return ""
	}
	var cred *syscall.Ucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil || cerr != nil || cred == nil {
		return ""
	}
	u, err := user.LookupId(strconv.FormatUint(uint64(cred.Uid), 10))
	if err != nil {
		return ""
	}
	return u.Username
}
