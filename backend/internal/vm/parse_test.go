package vm

import (
	"testing"
	"time"
)

func TestParseState(t *testing.T) {
	for in, want := range map[string][2]string{
		"running (booted)\n":     {StateRunning, "booted"},
		"shut off (destroyed)\n": {StateOff, "destroyed"},
		"paused (user)":          {StatePaused, "user"},
		"in shutdown (user)":     {StateStopping, "user"},
		"crashed (panicked)":     {StateCrashed, "panicked"},
		"pmsuspended (unknown)":  {StateSuspended, "unknown"},
		"idle":                   {StateRunning, ""},
	} {
		s, r := parseState(in)
		if s != want[0] || r != want[1] {
			t.Errorf("parseState(%q) = %q, %q", in, s, r)
		}
	}
}

func TestParseInfo(t *testing.T) {
	out := "Id:             3\nName:           debian12\nUUID:           0f1e\nState:          running\nCPU(s):         2\nAutostart:      enable\n"
	m := parseInfo(out)
	if m["Autostart"] != "enable" || m["State"] != "running" || m["Name"] != "debian12" {
		t.Errorf("parseInfo = %v", m)
	}
}

const domstatsSample = `Domain: 'Windows 11'
  state.state=1
  state.reason=1
  cpu.time=128340000000
  cpu.user=90000000000
  cpu.system=38340000000
  balloon.current=8388608
  balloon.maximum=8388608
  balloon.available=8257536
  balloon.unused=3145728
  balloon.rss=8500000
  vcpu.current=4
  vcpu.maximum=4

Domain: 'debian12'
  state.state=1
  cpu.time=5000000000
  balloon.current=2097152
  balloon.maximum=4194304
  balloon.rss=1048576
  vcpu.current=2
`

func TestParseDomStats(t *testing.T) {
	st := parseDomStats(domstatsSample)
	w := st["Windows 11"]
	if w.CPUTime != 128340000000 || w.VCPUs != 4 || w.MemTotal != 8388608<<10 || w.MemUsed != (8257536-3145728)<<10 {
		t.Errorf("Windows 11: %+v", w)
	}
	d := st["debian12"]
	// No guest balloon driver: fall back to the process's resident memory.
	if d.MemUsed != 1048576<<10 || d.MemTotal != 2097152<<10 || d.VCPUs != 2 {
		t.Errorf("debian12: %+v", d)
	}
}

func TestParseVNCDisplay(t *testing.T) {
	for in, want := range map[string]int{":0\n": 5900, "127.0.0.1:1": 5901, "[::1]:3": 5903, "localhost:12": 5912} {
		if got, ok := parseVNCDisplay(in); !ok || got != want {
			t.Errorf("parseVNCDisplay(%q) = %d, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "error", ":x"} {
		if _, ok := parseVNCDisplay(in); ok {
			t.Errorf("parseVNCDisplay(%q) should fail", in)
		}
	}
}

func TestParseSnapshots(t *testing.T) {
	out := ` Name             Creation Time               State
---------------------------------------------------------
 fresh-install    2026-09-01 10:00:00 +0000   shutoff
 before-updates   2026-10-08 21:15:42 +0200   running
`
	snaps := parseSnapshots(out)
	if len(snaps) != 2 {
		t.Fatalf("got %d snapshots", len(snaps))
	}
	if snaps[0].Name != "fresh-install" || snaps[0].State != StateOff || !snaps[0].Created.Equal(time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("first: %+v", snaps[0])
	}
	if snaps[1].State != StateRunning || !snaps[1].Created.Equal(time.Date(2026, 10, 8, 19, 15, 42, 0, time.UTC)) {
		t.Errorf("second: %+v", snaps[1])
	}
	if len(parseSnapshots(" Name   Creation Time   State\n------\n")) != 0 {
		t.Error("an empty table has no snapshots")
	}
}

func TestParseImgInfo(t *testing.T) {
	size, used, format, err := parseImgInfo([]byte(`{"virtual-size": 85899345920, "filename": "/d/disk-1.qcow2", "format": "qcow2", "actual-size": 33285996544, "dirty-flag": false}`))
	if err != nil || size != 85899345920 || used != 33285996544 || format != "qcow2" {
		t.Errorf("parseImgInfo = %d %d %q %v", size, used, format, err)
	}
}

func TestParseMemTotal(t *testing.T) {
	if got := parseMemTotal([]byte("MemTotal:       32768000 kB\nMemFree:  1 kB\n")); got != 32000 {
		t.Errorf("parseMemTotal = %d", got)
	}
}
