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

// ServeShellSocket serves NoCap Shell on a Unix socket until ctx ends. Each
// connection is the Linux account and program the kernel says it is
// (SO_PEERCRED, /proc/PID/exe); only NoCapOS's own nocap-shell gets a
// session, and only for its own account.
func (s *Server) ServeShellSocket(ctx context.Context, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o666); err != nil {
		_ = ln.Close()
		return err
	}
	srv := &http.Server{
		Handler:           s.ShellHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       time.Minute,
		MaxHeaderBytes:    16 << 10,
		ConnContext: func(c context.Context, conn net.Conn) context.Context {
			if p, ok := shellPeerOf(conn); ok {
				return WithShellPeer(c, p)
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

// shellPeerOf is the account and program on the other end of the socket.
func shellPeerOf(conn net.Conn) (shellPeer, bool) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return shellPeer{}, false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return shellPeer{}, false
	}
	var cred *syscall.Ucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil || cerr != nil || cred == nil || cred.Uid == 0 {
		return shellPeer{}, false
	}
	u, err := user.LookupId(strconv.FormatUint(uint64(cred.Uid), 10))
	if err != nil {
		return shellPeer{}, false
	}
	exe, err := os.Readlink("/proc/" + strconv.Itoa(int(cred.Pid)) + "/exe")
	if err != nil {
		return shellPeer{}, false
	}
	return shellPeer{Account: u.Username, Exe: exe}, true
}
