//go:build linux

package webapps

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"time"
)

// OldBrowserCleanup removes the streamed browser earlier versions set up
// (see oldbrowser.go). It runs once; alfad calls it in the background at
// start. removeContainer may be nil.
func OldBrowserCleanup(ctx context.Context, log *slog.Logger, dataDir string, removeContainer func(context.Context, string) error) {
	if os.Geteuid() != 0 {
		return // only a root alfad set it up
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	h := oldBrowserHost{
		dataDir: dataDir, root: "/", log: log, removeContainer: removeContainer,
		run: func(ctx context.Context, name string, args ...string) bool {
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
			return cmd.Run() == nil
		},
	}
	h.cleanup(ctx)
}
