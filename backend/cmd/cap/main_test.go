package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"alfaos/alfad/internal/hostaccess"
)

func TestCmdWebFlipsTheFlagFile(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	run := func(args ...string) string {
		t.Helper()
		out.Reset()
		if err := cmdWeb(&out, dir, args); err != nil {
			t.Fatalf("cap web %v: %v", args, err)
		}
		return out.String()
	}
	if s := run("status"); !strings.HasPrefix(s, "on") {
		t.Fatalf("status = %q", s)
	}
	run("off")
	if _, err := os.Stat(filepath.Join(dir, hostaccess.RemoteOffFile)); err != nil {
		t.Fatal("off should create the flag file:", err)
	}
	if s := run("status"); !strings.HasPrefix(s, "off") {
		t.Fatalf("status = %q", s)
	}
	run("on")
	if !hostaccess.RemoteEnabled(dir) {
		t.Fatal("on should remove the flag file")
	}

	for _, bad := range [][]string{nil, {"maybe"}, {"on", "off"}} {
		if err := cmdWeb(&out, dir, bad); err == nil {
			t.Errorf("cap web %v should fail", bad)
		}
	}
	if err := cmdWeb(&out, filepath.Join(dir, "nope"), []string{"off"}); err == nil {
		t.Error("a missing data folder should be an error")
	}
}
