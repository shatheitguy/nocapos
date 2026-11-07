package mcp

import (
	"errors"
	"sync"
	"time"
)

// Account is the Linux account a command runs as.
type Account struct {
	Username string
	UID, GID uint32
	Groups   []uint32
	Home     string
}

// RunResult is what run_command hands back.
type RunResult struct {
	Output    string `json:"output"`
	ExitCode  int    `json:"exit_code"`
	TimedOut  bool   `json:"timed_out,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

const (
	// DefaultRunTimeout and MaxRunTimeout bound run_command.
	DefaultRunTimeout = 60 * time.Second
	MaxRunTimeout     = 10 * time.Minute
	// MaxRunOutput is how much output run_command keeps.
	MaxRunOutput = 64 << 10
)

var (
	// ErrRoot: agents never run anything as root.
	ErrRoot = errors.New("agents never run commands as root; this account is root")
	// ErrRunUnsupported: commands as a person's own account need Linux.
	ErrRunUnsupported = errors.New("running commands as your own account needs NoCapOS on Linux")
)

// ClampTimeout keeps a requested timeout in seconds within the limits.
func ClampTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return DefaultRunTimeout
	}
	d := time.Duration(seconds) * time.Second
	return min(d, MaxRunTimeout)
}

// capWriter keeps the first max bytes written and notes the rest was cut.
type capWriter struct {
	mu        sync.Mutex
	max       int
	b         []byte
	truncated bool
}

func (c *capWriter) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := c.max - len(c.b); room > 0 {
		if len(p) > room {
			c.b = append(c.b, p[:room]...)
			c.truncated = true
		} else {
			c.b = append(c.b, p...)
		}
	} else if len(p) > 0 {
		c.truncated = true
	}
	return len(p), nil
}

func (c *capWriter) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.b)
}
