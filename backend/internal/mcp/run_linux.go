//go:build linux

package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// RunCommand runs command with /bin/sh as acct (never root), in its home,
// with a clean environment, killed with everything it started after
// timeout; output (stdout and stderr together) is capped at maxOut bytes.
func RunCommand(ctx context.Context, acct Account, command string, timeout time.Duration, maxOut int) (RunResult, error) {
	return runAs(ctx, acct, []string{"/bin/sh", "-c", command}, timeout, maxOut)
}

// RunCode runs a snippet on the host as acct (never root), like RunCommand.
func RunCode(ctx context.Context, acct Account, lang, code string, timeout time.Duration, maxOut int) (RunResult, error) {
	if acct.UID == 0 || acct.Username == "root" {
		return RunResult{}, ErrRoot
	}
	argv, err := codeArgv(lang, code, exec.LookPath)
	if err != nil {
		return RunResult{}, err
	}
	return runAs(ctx, acct, argv, timeout, maxOut)
}

func cleanEnv(home, user string) []string {
	lang := os.Getenv("LANG")
	if lang == "" {
		lang = "C.UTF-8"
	}
	return []string{
		"HOME=" + home, "USER=" + user, "LOGNAME=" + user, "SHELL=/bin/sh",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LANG=" + lang, "TERM=dumb",
	}
}

func runAs(ctx context.Context, acct Account, argv []string, timeout time.Duration, maxOut int) (RunResult, error) {
	if acct.UID == 0 || acct.Username == "root" {
		return RunResult{}, ErrRoot
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = "/"
	if st, err := os.Stat(acct.Home); err == nil && st.IsDir() {
		cmd.Dir = acct.Home
	}
	cmd.Env = cleanEnv(acct.Home, acct.Username)
	cred := &syscall.Credential{Uid: acct.UID, Gid: acct.GID, Groups: acct.Groups}
	if int(acct.UID) == os.Getuid() && int(acct.GID) == os.Getgid() {
		cred.NoSetGroups = true // already this account: no privileges needed
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Credential: cred}
	return finish(ctx, cmd, maxOut, nil)
}

// finish runs cmd with capped output, killing its process group (and
// calling stop, if any) when ctx ends.
func finish(ctx context.Context, cmd *exec.Cmd, maxOut int, stop func()) (RunResult, error) {
	out := &capWriter{max: maxOut}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.Stdin = nil
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	cmd.Cancel = func() error {
		// The whole process group: the shell and everything it started.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if stop != nil {
			stop()
		}
		return nil
	}
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	res := RunResult{Output: out.String(), Truncated: out.truncated}
	if ctx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
		res.ExitCode = -1
		return res, nil
	}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		res.ExitCode = exit.ExitCode()
	default:
		return res, err
	}
	return res, nil
}

// sandboxProps lock a transient systemd unit down: its own throwaway user,
// no network, no devices, home folders hidden, the system read-only, and
// memory, process and time limits.
// sandboxHidden are hidden from sandboxed code ("-": fine if missing).
var sandboxHidden = []string{"-/mnt", "-/media", "-/run/media", "-/srv"}

func sandboxProps(timeout time.Duration) []string {
	props := []string{
		"DynamicUser=yes", "PrivateNetwork=yes", "PrivateTmp=yes", "PrivateDevices=yes", "PrivateUsers=yes",
		"ProtectSystem=strict", "ProtectHome=yes", "ProtectKernelTunables=yes", "ProtectKernelModules=yes",
		"ProtectKernelLogs=yes", "ProtectControlGroups=yes", "ProtectClock=yes", "ProtectHostname=yes",
		"ProtectProc=invisible", "ProcSubset=pid", "NoNewPrivileges=yes", "RestrictNamespaces=yes",
		"RestrictSUIDSGID=yes", "RestrictRealtime=yes", "LockPersonality=yes", "CapabilityBoundingSet=",
		"RestrictAddressFamilies=AF_UNIX", "MemoryMax=512M", "TasksMax=64", "CPUQuota=100%",
		"WorkingDirectory=/tmp", "RuntimeMaxSec=" + strconv.Itoa(int(timeout/time.Second)+1),
		// "No access to your files": storage pools, shares, mounted drives,
		// cloud folders and app data live outside /home, often world-readable.
		"InaccessiblePaths=" + strings.Join(sandboxHidden, " "),
	}
	out := make([]string, 0, 2*len(props))
	for _, p := range props {
		out = append(out, "-p", p)
	}
	return out
}

// RunSandboxed runs a snippet in a throwaway systemd sandbox. It needs
// NoCapOS running as root (it starts the sandbox, never the code, as root).
func RunSandboxed(ctx context.Context, lang, code string, timeout time.Duration, maxOut int) (RunResult, error) {
	if os.Geteuid() != 0 {
		return RunResult{}, ErrSandboxUnavailable
	}
	sr, err := exec.LookPath("systemd-run")
	if err != nil {
		return RunResult{}, ErrSandboxUnavailable
	}
	argv, err := codeArgv(lang, code, exec.LookPath)
	if err != nil {
		return RunResult{}, err
	}
	idb := make([]byte, 6)
	_, _ = rand.Read(idb)
	unit := "nocap-agent-code-" + hex.EncodeToString(idb)
	args := append([]string{"--quiet", "--pipe", "--wait", "--collect", "--service-type=exec", "--unit=" + unit,
		"-E", "HOME=/tmp", "-E", "LANG=C.UTF-8", "-E", "PATH=/usr/local/bin:/usr/bin:/bin"},
		sandboxProps(timeout)...)
	args = append(append(args, "--"), argv...)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, sr, args...)
	cmd.Env = cleanEnv("/tmp", "root")
	stop := func() { _ = exec.Command("systemctl", "stop", unit+".service").Run() }
	return finish(ctx, cmd, maxOut, stop)
}
