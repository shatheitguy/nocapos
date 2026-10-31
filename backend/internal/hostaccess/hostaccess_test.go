package hostaccess

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSetRemoteTogglesFlagFile(t *testing.T) {
	dir := t.TempDir()
	if !RemoteEnabled(dir) {
		t.Fatal("remote access should be on by default")
	}
	if err := SetRemote(dir, false); err != nil {
		t.Fatal(err)
	}
	if RemoteEnabled(dir) {
		t.Fatal("remote access should be off after SetRemote(false)")
	}
	if _, err := os.Stat(filepath.Join(dir, RemoteOffFile)); err != nil {
		t.Fatal("flag file missing:", err)
	}
	if err := SetRemote(dir, false); err != nil { // twice is fine
		t.Fatal(err)
	}
	if err := SetRemote(dir, true); err != nil {
		t.Fatal(err)
	}
	if !RemoteEnabled(dir) {
		t.Fatal("remote access should be back on")
	}
	if err := SetRemote(dir, true); err != nil { // already on
		t.Fatal(err)
	}
}

func TestGateCachesAndNoticesChanges(t *testing.T) {
	dir := t.TempDir()
	g := NewGate(dir)
	now := time.Unix(1000, 0)
	g.now = func() time.Time { return now }
	if g.LocalOnly() {
		t.Fatal("should start open")
	}
	if err := SetRemote(dir, false); err != nil {
		t.Fatal(err)
	}
	if g.LocalOnly() {
		t.Fatal("cached answer should hold within the TTL")
	}
	now = now.Add(2 * time.Second)
	if !g.LocalOnly() {
		t.Fatal("change should show up after the TTL")
	}
	_ = SetRemote(dir, true)
	g.Refresh()
	if g.LocalOnly() {
		t.Fatal("Refresh should re-read the flag")
	}
	var nilGate *Gate
	if nilGate.LocalOnly() {
		t.Fatal("a nil gate never blocks")
	}
}

func TestLoopbackPeer(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:5000":         true,
		"127.8.9.1:1":            true,
		"[::1]:443":              true,
		"[::ffff:127.0.0.1]:443": true,
		"192.168.1.20:5000":      false,
		"10.0.0.2:1":             false,
		"[fe80::1%eth0]:1":       false,
		"[2001:db8::1]:443":      false,
		"garbage":                false,
		"":                       false,
	} {
		if got := LoopbackPeer(addr); got != want {
			t.Errorf("LoopbackPeer(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestDataDirFromEnvFile(t *testing.T) {
	dir := t.TempDir()
	if got := DataDir(filepath.Join(dir, "missing.env")); got != "/var/lib/nocapos" {
		t.Errorf("default = %q", got)
	}
	env := filepath.Join(dir, "nocapos.env")
	_ = os.WriteFile(env, []byte("# comment\nALFA_LISTEN_ADDR=:443\nALFA_DATA_DIR=\"/srv/nocap-data\"\n"), 0o600)
	if got := DataDir(env); got != "/srv/nocap-data" {
		t.Errorf("DataDir = %q", got)
	}
}

func TestDisplayConnected(t *testing.T) {
	dir := t.TempDir()
	if DisplayConnected(dir) {
		t.Fatal("no outputs means no display")
	}
	for name, status := range map[string]string{"card0-HDMI-A-1": "disconnected\n", "card0-HDMI-A-2": "unknown\n"} {
		_ = os.MkdirAll(filepath.Join(dir, name), 0o755)
		_ = os.WriteFile(filepath.Join(dir, name, "status"), []byte(status), 0o644)
	}
	if DisplayConnected(dir) {
		t.Fatal("disconnected outputs aren't a display")
	}
	_ = os.MkdirAll(filepath.Join(dir, "card1-DP-1"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "card1-DP-1", "status"), []byte("connected\n"), 0o644)
	if !DisplayConnected(dir) {
		t.Fatal("connected output not seen")
	}
}

func TestDesktopStateAndSet(t *testing.T) {
	var calls []string
	d := &Desktop{
		Native: true,
		Exists: func(p string) bool { return p == DesktopUnitPath },
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			switch args[0] {
			case "is-enabled":
				return []byte("enabled\n"), nil
			case "is-active":
				return []byte("inactive\n"), errors.New("exit 3")
			}
			return nil, nil
		},
	}
	st := d.State(context.Background())
	if !st.Available || !st.Enabled || st.Active {
		t.Fatalf("state = %+v", st)
	}
	calls = nil
	if err := d.Set(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if err := d.Set(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	want := []string{"systemctl enable --now " + DesktopUnit, "systemctl disable --now " + DesktopUnit, "systemctl --no-block start getty@tty1.service"}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %v", calls)
	}

	d.Exists = func(string) bool { return false }
	if st := d.State(context.Background()); st.Available || st.Reason == "" {
		t.Fatalf("missing unit should be unavailable with a reason: %+v", st)
	}
	if err := d.Set(context.Background(), true); !errors.Is(err, ErrDesktopUnavailable) {
		t.Fatalf("Set without the unit = %v", err)
	}
	d.Native = false
	if st := d.State(context.Background()); st.Available {
		t.Fatal("not native: unavailable")
	}
}
