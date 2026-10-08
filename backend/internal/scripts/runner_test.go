package scripts

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

func wait(t *testing.T, rn *Runner, id string) Run {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if r, _ := rn.Get(id); !r.Running {
			return r
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("run never finished")
	return Run{}
}

func TestRunOutputAndExit(t *testing.T) {
	rn := NewRunner(true)
	body := "echo hello; exit 3"
	if runtime.GOOS == "windows" {
		body = "Write-Output hello; exit 3"
	}
	done := make(chan int, 1)
	id, err := rn.Start("s1", "t", body, func(code int, _ time.Time) { done <- code })
	if err != nil {
		t.Fatal(err)
	}
	r := wait(t, rn, id)
	if !strings.Contains(r.Output, "hello") || r.ExitCode == nil || *r.ExitCode != 3 {
		t.Fatalf("run = %+v", r)
	}
	if c := <-done; c != 3 {
		t.Fatalf("onDone code = %d", c)
	}
}

func TestCancel(t *testing.T) {
	rn := NewRunner(true)
	body := "sleep 30"
	if runtime.GOOS == "windows" {
		body = "Start-Sleep -Seconds 30"
	}
	id, _ := rn.Start("s1", "t", body, nil)
	time.Sleep(500 * time.Millisecond)
	start := time.Now()
	rn.Cancel(id)
	r := wait(t, rn, id)
	if time.Since(start) > 10*time.Second || r.Error != "Stopped." {
		t.Fatalf("cancel: %+v after %v", r, time.Since(start))
	}
}

func TestDisabled(t *testing.T) {
	if _, err := NewRunner(false).Start("s", "n", "echo", nil); err != ErrDisabled {
		t.Fatalf("err = %v", err)
	}
}

func TestOutputCap(t *testing.T) {
	x := &run{}
	x.Write(make([]byte, maxOutput+10))
	if x.buf.Len() != maxOutput || !x.r.Truncated {
		t.Fatal("output not capped")
	}
}
