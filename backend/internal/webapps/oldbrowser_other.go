//go:build !linux

package webapps

import (
	"context"
	"log/slog"
)

// OldBrowserCleanup: the streamed browser only ever ran on Linux hosts.
func OldBrowserCleanup(context.Context, *slog.Logger, string, func(context.Context, string) error) {
}
