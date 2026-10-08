//go:build !windows

package scripts

import (
	"os/exec"
	"syscall"
)

// prepare puts the script in its own process group so cancel kills children too.
func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
