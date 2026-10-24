//go:build !linux

package api

import "os/exec"

// Self-update only runs on native Linux (see canSelfUpdate); elsewhere there
// is nothing to detach from.
func detachFromParent(cmd *exec.Cmd) {}
