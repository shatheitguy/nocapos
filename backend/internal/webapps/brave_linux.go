//go:build linux

package webapps

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed brave-run.sh
var braveScript []byte

// Supported reports whether native Brave can run on this host.
const Supported = true

func (b *Brave) provision(ctx context.Context) (int, error) {
	// A bridge from an earlier alfad run may still be serving — just reuse it.
	if portOpen(braveWebPort) {
		return braveWebPort, nil
	}

	script := filepath.Join(b.dataDir, "brave-run.sh")
	if err := os.WriteFile(script, braveScript, 0o755); err != nil {
		return 0, fmt.Errorf("could not write launcher: %w", err)
	}

	b.set(PhaseInstalling, "Installing Brave on this system… (first launch only)")
	install := exec.CommandContext(ctx, "bash", script, "install")
	if out, err := install.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("install failed: %s", tail(out, 3))
	}

	b.set(PhaseStarting, "Starting Brave…")
	logf, err := os.OpenFile(filepath.Join(b.dataDir, "brave.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return 0, err
	}
	run := exec.Command("bash", script, "run")
	run.Env = append(os.Environ(),
		"BRAVE_PROFILE="+filepath.Join(b.dataDir, "brave-profile"),
		"BRAVE_WEB_PORT="+strconv.Itoa(braveWebPort),
	)
	run.Stdout, run.Stderr = logf, logf
	// Own process group so stopProcess takes down Xvfb, Brave and x11vnc too.
	run.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := run.Start(); err != nil {
		logf.Close()
		return 0, fmt.Errorf("could not start Brave: %w", err)
	}
	b.mu.Lock()
	b.proc = run.Process
	b.mu.Unlock()
	done := make(chan struct{})
	go func() {
		_ = run.Wait()
		logf.Close()
		b.exited(run.Process)
		close(done)
	}()

	deadline := time.After(45 * time.Second)
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
wait:
	for {
		select {
		case <-tick.C:
			if portOpen(braveWebPort) {
				return braveWebPort, nil
			}
		case <-done:
			break wait
		case <-deadline:
			stopProcess(run.Process)
			break wait
		}
	}
	log, _ := os.ReadFile(filepath.Join(b.dataDir, "brave.log"))
	return 0, errors.New("Brave did not start: " + tail(log, 3))
}

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
