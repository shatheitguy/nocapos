package mcp

import (
	"context"
	"os"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRunCommandRefusesRoot(t *testing.T) {
	for _, a := range []Account{{Username: "root", UID: 0}, {Username: "x", UID: 0}, {Username: "root", UID: 1000}} {
		if _, err := RunCommand(context.Background(), a, "id", time.Second, 100); err != ErrRoot {
			t.Fatalf("%+v: %v", a, err)
		}
	}
}

func TestClampTimeout(t *testing.T) {
	if ClampTimeout(0) != DefaultRunTimeout || ClampTimeout(5) != 5*time.Second || ClampTimeout(99999) != MaxRunTimeout {
		t.Fatal("clamp")
	}
}

func TestCapWriter(t *testing.T) {
	c := &capWriter{max: 5}
	_, _ = c.Write([]byte("abc"))
	_, _ = c.Write([]byte("defg"))
	_, _ = c.Write([]byte("h"))
	if c.String() != "abcde" || !c.truncated {
		t.Fatalf("%q %v", c.String(), c.truncated)
	}
}

// selfAccount is the (non-root) account the tests run as.
func selfAccount(t *testing.T) Account {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("commands as an account need Linux")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root")
	}
	u, err := user.Current()
	if err != nil {
		t.Skip(err)
	}
	uid, _ := strconv.ParseUint(u.Uid, 10, 32)
	gid, _ := strconv.ParseUint(u.Gid, 10, 32)
	return Account{Username: u.Username, UID: uint32(uid), GID: uint32(gid), Home: t.TempDir()}
}

func TestRunCommandAsAccount(t *testing.T) {
	a := selfAccount(t)
	res, err := RunCommand(context.Background(), a, "id -u; echo $HOME; echo oops >&2; exit 3", 10*time.Second, MaxRunOutput)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(res.Output), "\n")
	if res.ExitCode != 3 || len(lines) != 3 || lines[0] != strconv.Itoa(int(a.UID)) || lines[1] != a.Home || lines[2] != "oops" {
		t.Fatalf("%+v", res)
	}
	if lines[0] == "0" {
		t.Fatal("ran as root")
	}
}

func TestRunCommandTimeoutAndCap(t *testing.T) {
	a := selfAccount(t)
	start := time.Now()
	res, err := RunCommand(context.Background(), a, "echo started; sleep 30 & sleep 30", 300*time.Millisecond, MaxRunOutput)
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || time.Since(start) > 10*time.Second || !strings.Contains(res.Output, "started") {
		t.Fatalf("timeout: %+v after %v", res, time.Since(start))
	}
	res, err = RunCommand(context.Background(), a, "head -c 100000 /dev/zero", 10*time.Second, 1000)
	if err != nil || len(res.Output) != 1000 || !res.Truncated {
		t.Fatalf("cap: %d %+v %v", len(res.Output), res.Truncated, err)
	}
}
