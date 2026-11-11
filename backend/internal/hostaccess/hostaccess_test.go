package hostaccess

import (
	"os"
	"path/filepath"
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
