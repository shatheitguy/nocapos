//go:build linux

package webapps

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

//go:embed app-stream.sh
var appStreamScript []byte

// systemdRun is systemd-run's path when the host runs systemd ("" if not).
func systemdRun() string {
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return ""
	}
	p, err := exec.LookPath("systemd-run")
	if err != nil {
		return ""
	}
	return p
}

// writeStreamScript puts app-stream.sh where the units (and the person's
// account) can read it: root-owned, on the host's /run.
func writeStreamScript() error {
	if err := os.MkdirAll(filepath.Dir(StreamScriptPath), 0o755); err != nil {
		return err
	}
	if cur, err := os.ReadFile(StreamScriptPath); err == nil && bytes.Equal(cur, appStreamScript) {
		return nil
	}
	tmp := StreamScriptPath + ".new"
	if err := os.WriteFile(tmp, appStreamScript, 0o755); err != nil {
		return err
	}
	_ = os.Chmod(tmp, 0o755)
	return os.Rename(tmp, StreamScriptPath)
}

// startStream runs a session in its own transient systemd unit, as the
// session's account, outside nocapos.service's sandbox (see launcher.go),
// and waits for its noVNC bridge to answer.
func startStream(ctx context.Context, s *streamSession, logDir string) (func(), <-chan struct{}, error) {
	dir := filepath.Join(logDir, "streams")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, err
	}
	logPath := filepath.Join(dir, s.id+".log")
	prep, unit, err := streamPlan(s, logPath, envOr("LANG", "C.UTF-8"))
	if err != nil {
		return nil, nil, err
	}
	if portOpen(s.slot.WebPort) {
		return nil, nil, fmt.Errorf("port %d is already in use on this server", s.slot.WebPort)
	}
	if err := writeStreamScript(); err != nil {
		return nil, nil, fmt.Errorf("could not write the stream launcher: %w", err)
	}
	// Truncate, then append: the unit appends to the same file.
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		return nil, nil, err
	}
	logf, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, err
	}

	var (
		cmd  *exec.Cmd
		halt func()
	)
	if sdRun := systemdRun(); sdRun != "" {
		if prep != nil {
			out, err := exec.CommandContext(ctx, sdRun, SystemdRunArgs(*prep)...).CombinedOutput()
			if err != nil {
				logf.Close()
				return nil, nil, fmt.Errorf("couldn't get the server ready for %s: %s", s.spec.Title, tail(out, 2))
			}
		}
		cmd = exec.Command(sdRun, SystemdRunArgs(unit)...)
		halt = func() { stopUnit(unit.Unit) }
	} else {
		// No systemd: run it directly (still as the account, never root).
		if prep != nil {
			if out, err := exec.CommandContext(ctx, prep.Argv[0], prep.Argv[1:]...).CombinedOutput(); err != nil {
				logf.Close()
				return nil, nil, fmt.Errorf("couldn't get the server ready for %s: %s", s.spec.Title, tail(out, 2))
			}
		}
		a := s.spec.RunAs
		cmd = exec.Command(unit.Argv[0], unit.Argv[1:]...)
		cmd.Env = unit.Env
		cmd.Dir = a.Home
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Setpgid:    true, // stop takes the whole group down
			Credential: &syscall.Credential{Uid: a.UID, Gid: a.GID, Groups: a.Groups},
		}
		halt = func() { stopProcess(cmd.Process) }
	}
	cmd.Stdout, cmd.Stderr = logf, logf
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
		halt()
		out, _ := os.ReadFile(logPath)
		msg := fmt.Sprintf("%s didn't start: %s", s.spec.Title, tail(out, 3))
		if hint := sandboxHint(string(out), SandboxStatus()); hint != "" {
			msg += " — " + hint
		}
		return nil, nil, errors.New(msg)
	}
	return halt, done, nil
}

// stopUnit stops a stream's unit and waits (a little) for it to go.
func stopUnit(unit string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "systemctl", "stop", unit).Run()
}

// cleanupStreams stops stream units an earlier alfad left running.
func cleanupStreams() {
	if systemdRun() == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "systemctl", "stop", AppUnitPrefix+"*.service").Run()
}

// SandboxStatus checks this host's user namespaces for Linux app sandboxes.
func SandboxStatus() SandboxCheck {
	setuid := false
	if p, err := exec.LookPath("bwrap"); err == nil {
		if st, err := os.Stat(p); err == nil && st.Mode()&fs.ModeSetuid != 0 {
			setuid = true
		}
	}
	return CheckUserNamespaces(os.ReadFile, setuid)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
