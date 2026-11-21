package hostaccess

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fakeProc(t *testing.T, root string, pid, uid int, comm string, args ...string) {
	t.Helper()
	dir := filepath.Join(root, itoa(pid))
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "status"), []byte("Name:\t"+comm+"\nUid:\t"+itoa(uid)+"\t"+itoa(uid)+"\t"+itoa(uid)+"\t"+itoa(uid)+"\n"), 0o644)
	cmd := "/usr/libexec/nocapos/" + comm + "\x00"
	for _, a := range args {
		cmd += a + "\x00"
	}
	_ = os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmd), 0o644)
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestShellPIDSkipsAppWindows(t *testing.T) {
	root := t.TempDir()
	fakeProc(t, root, 300, 1000, "nocap-shell", "--app", "files")
	fakeProc(t, root, 200, 1000, "nocap-shell")
	fakeProc(t, root, 100, 1001, "nocap-shell")
	if got := shellPID(root, 1000); got != 200 {
		t.Fatalf("pid = %d", got)
	}
	if got := shellPID(root, 1002); got != 0 {
		t.Fatalf("pid = %d", got)
	}
}

func TestTrackConfirm(t *testing.T) {
	f, d, now := screenHost(t)
	ctx := context.Background()
	f.linked, f.target = "greetd.service", "graphical.target"
	f.active["greetd.service"] = true
	for _, p := range []string{ShellLibDir + "/nocap-shell", ShellLibDir + "/nocap-lock", sessionCmd, greeterCmd, ShellUIDir + "/shell.html"} {
		f.files[p] = ""
	}
	f.files[GreetdConfPath] = GreetdConfig("_greetd", "")
	f.files[sessionFilePath] = SessionDesktop()
	f.tools["labwc"], f.tools["Xwayland"] = true, true
	f.session = "Name=sam\nUser=1000\nType=wayland\nClass=user\nRemote=no\nState=active\n"
	fakeProc(t, d.ProcRoot, 50, 1000, "labwc")
	fakeProc(t, d.ProcRoot, 60, 1000, "nocap-shell")
	if h := d.Health(ctx); h.Status != "ok" {
		t.Fatalf("health = %+v", h)
	}
	tick := func(n int) DesktopConfirm {
		var c DesktopConfirm
		for i := 0; i < n; i++ {
			*now = now.Add(time.Minute)
			c = d.TrackConfirm(ctx)
		}
		return c
	}
	// Not signed in yet (the shell never got its session): nothing counts.
	if c := tick(30); c.Runs != 0 || c.Minutes != 0 {
		t.Fatalf("counted without a session: %+v", c)
	}
	d.NoteShellSession()
	c := tick(64)
	if c.Runs != 1 || c.Minutes < 55 || c.IsConfirmed() {
		t.Fatalf("one run: %+v", c)
	}
	// The shell restarts (a new run), and two hours in all: confirmed.
	_ = os.RemoveAll(filepath.Join(d.ProcRoot, "60"))
	fakeProc(t, d.ProcRoot, 70, 1000, "nocap-shell")
	c = tick(70)
	if c.Runs != 2 || !c.IsConfirmed() {
		t.Fatalf("two runs: %+v", c)
	}
	// Kept in the data folder.
	var saved DesktopConfirm
	if err := json.Unmarshal([]byte(f.files["/var/lib/nocapos/"+ConfirmFileName]), &saved); err != nil || !saved.IsConfirmed() {
		t.Fatalf("saved = %q", f.files["/var/lib/nocapos/"+ConfirmFileName])
	}
	d2 := f.desktop()
	if !d2.Confirmation().IsConfirmed() {
		t.Fatal("not read back")
	}
}
