package webapps

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// Brave keeps working as it did before app streams existed: same lifecycle,
// same loopback port, same launcher script.
func TestBraveLifecycleUnchanged(t *testing.T) {
	b := NewBrave(slog.New(slog.NewTextHandler(io.Discard, nil)), t.TempDir())
	if st := b.Status(); st.Phase != PhaseIdle || st.Ready {
		t.Fatalf("new Brave: %+v", st)
	}
	if port, scheme := b.Target(); port != 0 || scheme != "http" {
		t.Fatalf("idle target = %d %s", port, scheme)
	}
	p := &os.Process{Pid: 42}
	b.mu.Lock()
	b.proc, b.port, b.phase, b.message = p, braveWebPort, PhaseReady, "Ready"
	b.mu.Unlock()
	if port, _ := b.Target(); port != 6080 {
		t.Fatalf("ready target = %d, want 6080", port)
	}
	if st := b.Status(); !st.Ready {
		t.Fatalf("ready status: %+v", st)
	}
	b.exited(&os.Process{Pid: 7}) // some other process: ignored
	if !b.Status().Ready {
		t.Fatal("another process exiting must not stop Brave")
	}
	b.exited(p)
	if st := b.Status(); st.Ready || st.Phase != PhaseIdle || st.Message != "Brave stopped" {
		t.Fatalf("after exit: %+v", st)
	}
}

func TestBraveScriptUnchanged(t *testing.T) {
	b, err := os.ReadFile("brave-run.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`DISPLAY_NUM="${BRAVE_DISPLAY:-:99}"`, `WEB_PORT="${BRAVE_WEB_PORT:-6080}"`, `VNC_PORT="${BRAVE_VNC_PORT:-5999}"`, "install) install ;;", "run) run ;;"} {
		if !strings.Contains(s, want) {
			t.Errorf("brave-run.sh lost %q", want)
		}
	}
}
