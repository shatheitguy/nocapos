//go:build !linux

package filesync

import (
	"context"
	"errors"
)

// Watch is Linux-only (inotify); elsewhere the periodic scan finds changes.
func Watch(context.Context, string, func(string)) error {
	return errors.New("watching folders needs Linux")
}
