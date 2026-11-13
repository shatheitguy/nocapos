//go:build !linux

package cloudimport

import (
	"context"
	"errors"
)

func mountSupported() bool { return false }

var mountExec = func(context.Context, []string) error {
	return errors.New("cloud folders need Linux")
}

func mountedAt(string) bool { return false }
