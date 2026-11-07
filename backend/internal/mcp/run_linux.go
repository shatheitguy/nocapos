//go:build linux

package mcp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// RunCommand runs command with /bin/sh as acct (never root), in its home,
// with a clean environment, killed with everything it started after
// timeout; output (stdout and stderr together) is capped at maxOut bytes.
func RunCommand(ctx context.Context, acct Account, command string, timeout time.Duration, maxOut int) (RunResult, error) {
	if acct.UID == 0 || acct.Username == "root" {
		return RunResult{}, ErrRoot
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir = "/"
	if st, err := os.Stat(acct.Home); err == nil && st.IsDir() {
		cmd.Dir = acct.Home
	}
	lang := os.Getenv("LANG")
	if lang == "" {
		lang = "C.UTF-8"
	}
	cmd.Env = []string{
		"HOME=" + acct.Home, "USER=" + acct.Username, "LOGNAME=" + acct.Username, "SHELL=/bin/sh",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LANG=" + lang, "TERM=dumb",
	}
	out := &capWriter{max: maxOut}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.Stdin = nil
	cred := &syscall.Credential{Uid: acct.UID, Gid: acct.GID, Groups: acct.Groups}
	if int(acct.UID) == os.Getuid() && int(acct.GID) == os.Getgid() {
		cred.NoSetGroups = true // already this account: no privileges needed
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Credential: cred}
	cmd.Cancel = func() error {
		// The whole process group: the shell and everything it started.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
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
