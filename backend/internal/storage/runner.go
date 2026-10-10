package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
)

// Runner runs a host command as an argument list (never through a shell).
// It returns stdout; a failure's error carries the last line of stderr.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// InputRunner is a Runner that can also feed stdin (sfdisk scripts).
type InputRunner interface {
	RunInput(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error)
}

// ExecRunner runs real commands with a C locale so output is parseable.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return ExecRunner{}.RunInput(ctx, nil, name, args...)
}

func (ExecRunner) RunInput(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if err != nil {
		msg := lastLine(errb.String())
		if msg == "" {
			msg = lastLine(out.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		if ctx.Err() != nil {
			msg = name + " took too long and was stopped"
		}
		return out.Bytes(), errors.New(msg)
	}
	return out.Bytes(), nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		s = strings.TrimSpace(s[i+1:])
	}
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
