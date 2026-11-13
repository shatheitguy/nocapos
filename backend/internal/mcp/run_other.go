//go:build !linux

package mcp

import (
	"context"
	"time"
)

// RunCommand needs Linux (switching to a person's own account).
func RunCommand(_ context.Context, acct Account, _ string, _ time.Duration, _ int) (RunResult, error) {
	if acct.UID == 0 || acct.Username == "root" {
		return RunResult{}, ErrRoot
	}
	return RunResult{}, ErrRunUnsupported
}

// RunCode needs Linux, like RunCommand.
func RunCode(_ context.Context, acct Account, _, _ string, _ time.Duration, _ int) (RunResult, error) {
	if acct.UID == 0 || acct.Username == "root" {
		return RunResult{}, ErrRoot
	}
	return RunResult{}, ErrRunUnsupported
}

// RunSandboxed needs Linux with systemd.
func RunSandboxed(_ context.Context, _, _ string, _ time.Duration, _ int) (RunResult, error) {
	return RunResult{}, ErrSandboxUnavailable
}
