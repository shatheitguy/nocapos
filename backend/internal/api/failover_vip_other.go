//go:build !linux

package api

import (
	"context"
	"errors"
)

func vipTake(context.Context, string, string) error {
	return errors.New("a shared address needs Linux")
}

func vipRelease(context.Context, string, string) error { return nil }
