//go:build linux

package appdata

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// SystemCopy copies with rsync (owners, permissions, ACLs, extended
// attributes and hard links kept, ids as numbers) or, without it, GNU cp
// with everything preserved.
func SystemCopy(ctx context.Context, src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	var cmd *exec.Cmd
	if _, err := exec.LookPath("rsync"); err == nil {
		cmd = exec.CommandContext(ctx, "rsync", "-aHAX", "--numeric-ids", strings.TrimSuffix(src, "/")+"/", strings.TrimSuffix(dst, "/")+"/")
	} else {
		cmd = exec.CommandContext(ctx, "cp", "-a", "--preserve=all", "-T", src, dst)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(out.String())
		if len(msg) > 400 {
			msg = msg[len(msg)-400:]
		}
		return fmt.Errorf("copy %s: %v %s", src, err, msg)
	}
	return nil
}
