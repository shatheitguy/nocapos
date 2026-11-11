//go:build !linux

package webapps

import (
	"context"
	"errors"
)

func startStream(context.Context, *streamSession, string) (func(), <-chan struct{}, error) {
	return nil, nil, errors.New("native apps stream when NoCapOS is installed directly on Linux")
}

func cleanupStreams() {}

// SandboxStatus: Linux app sandboxes only exist on Linux.
func SandboxStatus() SandboxCheck { return SandboxCheck{OK: true} }
