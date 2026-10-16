//go:build !linux

package webapps

import (
	"context"
	"errors"
	"io"
	"os"
)

// Supported reports whether native Brave can run on this host.
const Supported = false

func (b *Brave) provision(context.Context) (int, error) {
	return 0, errors.New("Native Brave runs when NoCapOS is installed on a Linux host. " +
		"This NoCapOS instance is running on a non-Linux system, so Brave can't be installed here.")
}

// InstallNative installs Brave on a Linux host; elsewhere it can't.
func InstallNative(context.Context, io.Writer) error {
	return errors.New("Brave can only be installed when NoCapOS runs on Linux")
}

// NativeInstalled reports whether Brave is installed on this host.
func NativeInstalled() bool { return false }

func stopProcess(p *os.Process) { _ = p.Kill() }
