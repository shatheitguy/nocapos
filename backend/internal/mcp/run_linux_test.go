//go:build linux

package mcp

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// Sandboxed code sees none of the server's storage: pools, shares, drives
// and cloud folders live outside /home, often world-readable.
func TestSandboxHidesStorage(t *testing.T) {
	props := sandboxProps(time.Minute)
	i := slices.IndexFunc(props, func(p string) bool { return strings.HasPrefix(p, "InaccessiblePaths=") })
	if i < 1 || props[i-1] != "-p" {
		t.Fatalf("no InaccessiblePaths: %q", props)
	}
	for _, want := range []string{"-/srv", "-/mnt", "-/media", "-/run/media"} {
		if !slices.Contains(strings.Fields(strings.TrimPrefix(props[i], "InaccessiblePaths=")), want) {
			t.Errorf("%s not hidden: %s", want, props[i])
		}
	}
	for _, want := range []string{"PrivateNetwork=yes", "ProtectHome=yes", "DynamicUser=yes"} {
		if !slices.Contains(props, want) {
			t.Errorf("missing %s", want)
		}
	}
}
