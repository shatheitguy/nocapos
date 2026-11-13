//go:build linux

package cloudimport

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

func mountSupported() bool { return os.Geteuid() == 0 }

// mountExec runs a command (an argument list, never a shell).
var mountExec = func(ctx context.Context, argv []string) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		s := strings.TrimSpace(string(out))
		if i := strings.LastIndex(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		if s == "" {
			s = err.Error()
		}
		return errors.New(s)
	}
	return nil
}

func mountedAt(path string) bool {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if fields := strings.Fields(sc.Text()); len(fields) > 4 && fields[4] == path {
			return true
		}
	}
	return false
}
