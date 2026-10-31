//go:build !linux

package webapps

import (
	"context"
	"errors"
	"os"
)

func startStream(context.Context, *streamSession, string) (*os.Process, <-chan struct{}, error) {
	return nil, nil, errors.New("native apps stream when NoCapOS is installed directly on Linux")
}
