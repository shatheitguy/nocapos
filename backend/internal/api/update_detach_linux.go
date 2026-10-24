//go:build linux

package api

import (
	"os/exec"
	"syscall"
)

// detachFromParent puts the update command in its own session, so it
// survives install.sh restarting alfad's own service partway through
// (which would otherwise kill it along with this process's whole group).
func detachFromParent(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
