//go:build linux

package webapps

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Supported reports whether streamed apps can run on this host.
const Supported = true

func stopProcess(p *os.Process) {
	_ = syscall.Kill(-p.Pid, syscall.SIGTERM) // whole group
}

func portOpen(port int) bool {
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func tail(b []byte, n int) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	s := strings.Join(lines, " | ")
	if s == "" {
		s = "no output"
	}
	return s
}

// waitForPort waits until a loopback port answers (nil), the process exits
// (done closes) or ctx ends.
func waitForPort(ctx context.Context, port int, done <-chan struct{}) error {
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if portOpen(port) {
				return nil
			}
		case <-done:
			return errors.New("exited")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
