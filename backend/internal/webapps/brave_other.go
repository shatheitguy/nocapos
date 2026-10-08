//go:build !linux

package webapps

import (
	"context"
	"errors"
	"os"
)

// Supported reports whether native Brave can run on this host.
const Supported = false

func (b *Brave) provision(context.Context) (int, error) {
	return 0, errors.New("Native Brave runs when NoCapOS is installed on a Linux host. " +
		"This NoCapOS instance is running on a non-Linux system, so Brave can't be installed here.")
}

func stopProcess(p *os.Process) { _ = p.Kill() }
