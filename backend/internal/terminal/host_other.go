//go:build !linux

package terminal

import "time"

const (
	hostSupported = false
	resizeTimeout = 3 * time.Second
)

func detectHostShell() []string { return nil }

func openHostPTY([]string, int, int) (Session, error) { return nil, ErrHostUnsupported }
