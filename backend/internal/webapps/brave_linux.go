//go:build linux

package webapps

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
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

	wait, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	err = waitForPort(wait, braveWebPort, done)
	if err == nil {
		return braveWebPort, nil
	}
	if wait.Err() != nil {
		stopProcess(run.Process)
	}
	log, _ := os.ReadFile(filepath.Join(b.dataDir, "brave.log"))
	return 0, errors.New("Brave did not start: " + tail(log, 3))
}

// InstallNative installs Brave (from its official repository) and the stream
// tools, exactly as Brave's first launch does. The installer and the Packages
// app's repair button use it, so all three share one script.
func InstallNative(ctx context.Context, out io.Writer) error {
	dir, err := os.MkdirTemp("", "nocapos-brave-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	script := filepath.Join(dir, "brave-run.sh")
	if err := os.WriteFile(script, braveScript, 0o700); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "bash", script, "install")
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}

// NativeInstalled reports whether Brave is installed on this host.
func NativeInstalled() bool {
	for _, b := range []string{"brave-browser", "brave-browser-stable", "brave"} {
		if _, err := exec.LookPath(b); err == nil {
			return true
		}
	}
	return false
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

// waitForPort waits until a loopback port answers (nil), the process exits
// (done closes) or ctx ends. Brave and app streams both use it.
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
