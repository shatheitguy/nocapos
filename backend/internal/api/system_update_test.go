package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"alfaos/alfad/internal/config"
)

func newUpdateServer(t *testing.T, version string) *Server {
	t.Helper()
	return &Server{Deps: Deps{Config: &config.Config{DataDir: t.TempDir(), SystemRoot: true}, Version: version}}
}

func stubUnitAlive(t *testing.T, alive bool) {
	t.Helper()
	old := updateUnitAlive
	updateUnitAlive = func(string) bool { return alive }
	t.Cleanup(func() { updateUnitAlive = old })
}

func writeLock(t *testing.T, s *Server, l updateLock) {
	t.Helper()
	b, _ := json.Marshal(l)
	if err := os.WriteFile(s.updateLockPath(), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func getRunStatus(t *testing.T, s *Server) updateRunStatus {
	t.Helper()
	rec := httptest.NewRecorder()
	s.systemUpdateRunStatus(rec, httptest.NewRequest(http.MethodGet, "/api/v1/system/update/run", nil))
	var st updateRunStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("bad status JSON %s: %v", rec.Body.String(), err)
	}
	return st
}

func TestCanSelfUpdate(t *testing.T) {
	native := &Server{Deps: Deps{Config: &config.Config{SystemRoot: true}}}
	if !native.canSelfUpdate() {
		t.Error("a native, root install should be able to self-update")
	}
	other := &Server{Deps: Deps{Config: &config.Config{SystemRoot: false}}}
	if other.canSelfUpdate() {
		t.Error("Docker/Windows/non-root shouldn't offer self-update")
	}
}

func TestUpdateRunStatusRunning(t *testing.T) {
	s := newUpdateServer(t, "0.7.1")
	if s.updateIsRunning() {
		t.Fatal("nothing started yet")
	}
	stubUnitAlive(t, true)
	writeLock(t, s, updateLock{Started: time.Now().Add(-time.Minute), From: "0.7.1", To: "0.7.2", Unit: "nocapos-update-1"})
	log := "==> Updating NoCapOS from 0.7.1 to 0.7.2\n\x1b[1;31m==>\x1b[0m Downloading NoCapOS for arm64\n\x1b[1;31m==>\x1b[0m Checksum verified\n"
	if err := os.WriteFile(s.updateLogPath(), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	st := getRunStatus(t, s)
	if !st.Running || !st.Supported || st.Target != "0.7.2" || st.Stage != "verify" || st.Percent != 18 || st.Result != nil {
		t.Errorf("unexpected status %+v", st)
	}
	if strings.Contains(st.Log, "\x1b[") || !strings.Contains(st.Log, "Checksum verified") {
		t.Errorf("log should be there without colour codes: %q", st.Log)
	}
}

func TestUpdateRunStatusResultPersists(t *testing.T) {
	// What the new alfad sees after the restart: no lock, the result file the
	// updater wrote, and the log.
	s := newUpdateServer(t, "0.7.2")
	res := `{"from":"0.7.1","to":"0.7.2","ok":true,"exit_code":0,"finished_at":"2026-10-31T10:00:00Z"}`
	os.WriteFile(s.updateResultPath(), []byte(res), 0o600)
	os.WriteFile(s.updateLogPath(), []byte("==> Waiting for NoCapOS\n==> NoCapOS is running.\n"), 0o600)
	st := getRunStatus(t, s)
	if st.Running || st.Stage != "done" || st.Percent != 100 || st.Target != "0.7.2" || st.Version != "0.7.2" {
		t.Errorf("unexpected status %+v", st)
	}
	if st.Result == nil || !st.Result.OK || st.Result.From != "0.7.1" || st.Result.To != "0.7.2" || st.Result.FinishedAt.IsZero() {
		t.Errorf("unexpected result %+v", st.Result)
	}

	// A rolled-back update.
	res = `{"from":"0.7.1","to":"0.7.2","ok":false,"exit_code":1,"finished_at":"2026-10-31T10:00:00Z"}`
	os.WriteFile(s.updateResultPath(), []byte(res), 0o600)
	os.WriteFile(s.updateLogPath(), []byte("==> Installing /usr/local/bin/nocapos\n==> Waiting for NoCapOS\n"+
		"warning: NoCapOS 0.7.2 did not start; putting back 0.7.1\n"+
		"error: the update to 0.7.2 failed and was rolled back: 0.7.1 is running again with its database as it was.\n"), 0o600)
	st = getRunStatus(t, s)
	if st.Stage != "failed" || st.Result == nil || st.Result.OK || !st.Result.RolledBack || !st.Result.Unchanged ||
		!strings.Contains(st.Result.Error, "rolled back") {
		t.Errorf("rolled-back update: %+v / %+v", st, st.Result)
	}
}

func TestUpdateRunStatusStaleLock(t *testing.T) {
	// The updater's unit is gone but the lock is still there: it was killed.
	s := newUpdateServer(t, "0.7.1")
	stubUnitAlive(t, false)
	writeLock(t, s, updateLock{Started: time.Now().Add(-10 * time.Minute), From: "0.7.1", To: "0.7.2", Unit: "nocapos-update-1"})
	os.WriteFile(s.updateLogPath(), []byte("==> Downloading NoCapOS for arm64\n"), 0o600)
	st := getRunStatus(t, s)
	if st.Running || st.Stage != "failed" || st.Result == nil || st.Result.OK || !st.Result.Unchanged || st.Result.To != "0.7.2" {
		t.Errorf("stale lock should end as failed: %+v / %+v", st, st.Result)
	}
	if _, err := os.Stat(s.updateLockPath()); err == nil {
		t.Error("stale lock should be removed")
	}
	if s.readUpdateResult() == nil {
		t.Error("the failure should be saved")
	}

	// A just-started unit counts as running even before systemctl sees it.
	writeLock(t, s, updateLock{Started: time.Now(), From: "0.7.1", To: "0.7.2", Unit: "nocapos-update-2"})
	if !s.updateIsRunning() {
		t.Error("fresh lock should count as running")
	}
	// An older alfad's lock: just a timestamp, no unit; trusted for a while.
	os.WriteFile(s.updateLockPath(), []byte(time.Now().UTC().Format(time.RFC3339)), 0o600)
	if !s.updateIsRunning() {
		t.Error("recent legacy lock should count as running")
	}
	os.WriteFile(s.updateLockPath(), []byte(time.Now().Add(-3*time.Hour).UTC().Format(time.RFC3339)), 0o600)
	if s.updateIsRunning() {
		t.Error("old legacy lock should be stale")
	}
}

func TestParseUpdateProgress(t *testing.T) {
	steps := []struct {
		line, stage string
		pct         int
	}{
		{"==> Updating NoCapOS from 0.7.1 to 0.7.2", "download", 2},
		{"\x1b[1;31m==>\x1b[0m Downloading NoCapOS for arm64", "download", 8},
		{"==> Checksum verified", "verify", 18},
		{"==> Installing NoCapOS 0.7.2 (arm64, native mode)", "verify", 22},
		{"==> Installing host packages for virtual machines, sharing, remote desktop, mDNS, disks and media (skip with --minimal)", "packages", 30},
		{"    sharing: samba smbclient cifs-utils", "packages", 33},
		{"    smart: smartmontools", "packages", 36},
		{"==> Installing /usr/local/bin/nocapos", "install", 58},
		{"==> Kept the previous version and a copy of the database for rollback", "install", 64},
		{"==> Waiting for NoCapOS", "restart", 72},
		{"==> Installing Brave and its stream tools (from Brave's repository)", "restart", 86},
		{"==> NoCapOS is running.", "restart", 97},
		{"   Open:  https://10.0.0.2/", "restart", 97},
	}
	log, last := "", 0
	for _, st := range steps {
		log += st.line + "\n"
		p := parseUpdateProgress(log)
		if p.Stage != st.stage || p.Percent != st.pct {
			t.Errorf("after %q: got %s %d, want %s %d", st.line, p.Stage, p.Percent, st.stage, st.pct)
		}
		if p.Percent < last {
			t.Errorf("percent went backwards at %q", st.line)
		}
		last = p.Percent
	}

	// Package lines never push past the packages stage.
	many := "==> Installing host packages\n" + strings.Repeat("    x: y\n", 40)
	if p := parseUpdateProgress(many); p.Percent > updatePackagesMax || p.Stage != "packages" {
		t.Errorf("package lines overshot: %+v", p)
	}
	// A step printed again (rollback restarts and waits again) doesn't go back.
	again := "==> Waiting for NoCapOS\n==> Installing /usr/local/bin/nocapos\n"
	if p := parseUpdateProgress(again); p.Percent != 72 || p.Stage != "restart" {
		t.Errorf("went backwards: %+v", p)
	}
	fail := "==> Downloading NoCapOS for arm64\nerror: download failed: https://example/x.tar.gz\n"
	if p := parseUpdateProgress(fail); p.Stage != "failed" || p.Error != "download failed: https://example/x.tar.gz" || p.RolledBack {
		t.Errorf("failure: %+v", p)
	}
	rb := "warning: NoCapOS 0.7.2 did not start; putting back 0.7.1\n"
	if p := parseUpdateProgress(rb); !p.RollingBack {
		t.Errorf("rollback not seen: %+v", p)
	}
}

func TestUpdateInstallCommandPinsTheRelease(t *testing.T) {
	cmd, err := updateInstallCommand("v0.7.2")
	if err != nil {
		t.Fatal(err)
	}
	base := "'https://github.com/shatheitguy/nocapos/releases/download/v0.7.2'"
	if cmd != "curl -fsSL "+base+"/install.sh | NOCAP_RELEASE_URL="+base+" bash -s -- --yes" {
		t.Errorf("command = %s", cmd)
	}
	if strings.Contains(cmd, "latest") {
		t.Error("the update must not follow releases/latest")
	}
	if c, _ := updateInstallCommand("0.8.0"); !strings.Contains(c, "/download/v0.8.0") {
		t.Errorf("tag without v: %s", c)
	}
	for _, bad := range []string{"", "latest", "v1.2", "v1.2.3; rm -rf /", "v1.2.3'", "v1.2.3 x"} {
		if _, err := updateInstallCommand(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestUpdateLaunch(t *testing.T) {
	name, args := updateLaunch("/usr/bin/systemd-run", "nocapos-update-42", "echo hi")
	want := []string{"--unit=nocapos-update-42", "--collect", "--quiet", "--description=NoCapOS update", "--property=Type=exec", "/bin/bash", "-c", "echo hi"}
	if name != "/usr/bin/systemd-run" || strings.Join(args, "|") != strings.Join(want, "|") {
		t.Errorf("systemd-run launch: %s %q", name, args)
	}
	name, args = updateLaunch("", "", "echo hi")
	if name != "bash" || strings.Join(args, "|") != "-c|echo hi" {
		t.Errorf("fallback launch: %s %q", name, args)
	}
}

func TestUpdateScriptWritesResult(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	logPath, resPath, lockPath := filepath.Join(dir, "update.log"), filepath.Join(dir, "update-result.json"), filepath.Join(dir, "update.running")
	os.WriteFile(lockPath, []byte("{}"), 0o600)
	sh := func(p string) string { return filepath.ToSlash(p) } // Git Bash on Windows takes C:/... paths
	for _, tc := range []struct {
		cmd  string
		ok   bool
		code int
	}{{"echo installing; true", true, 0}, {"echo nope; exit 3", false, 3}} {
		os.WriteFile(lockPath, []byte("{}"), 0o600)
		script := updateScript(tc.cmd, sh(logPath), sh(resPath), sh(lockPath), "0.7.1", `0.7.2"x`)
		out, err := exec.Command(bash, "-c", script).CombinedOutput()
		if (err == nil) != tc.ok {
			t.Fatalf("%s: err=%v out=%s", tc.cmd, err, out)
		}
		b, err := os.ReadFile(resPath)
		if err != nil {
			t.Fatal(err)
		}
		var res updateResult
		if err := json.Unmarshal(b, &res); err != nil {
			t.Fatalf("result isn't JSON: %s", b)
		}
		if res.OK != tc.ok || res.ExitCode != tc.code || res.From != "0.7.1" || res.To != `0.7.2"x` || res.FinishedAt.IsZero() {
			t.Errorf("%s: result %+v", tc.cmd, res)
		}
		if _, err := os.Stat(lockPath); err == nil {
			t.Error("lock should be removed when the updater ends")
		}
	}
	if b, _ := os.ReadFile(logPath); !strings.Contains(string(b), "installing") || !strings.Contains(string(b), "nope") {
		t.Errorf("output should be appended to the log: %q", b)
	}
}

func TestSystemUpdateRunTarget(t *testing.T) {
	s := newUpdateServer(t, "0.7.1")
	old := releaseFor
	t.Cleanup(func() { releaseFor = old })
	refreshed := false
	releaseFor = func(_ *Server, _ *http.Request, refresh bool) updateInfo {
		refreshed = refreshed || refresh
		return updateInfo{Current: "0.7.1", Latest: "v0.7.3", Available: true, Checked: time.Now()}
	}
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/system/update/run", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.systemUpdateRun(rec, req)
		return rec
	}
	// The UI promised 0.7.2 but there's a newer release: re-check, then refuse.
	rec := post(`{"target":"0.7.2"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "release_changed") || !refreshed {
		t.Errorf("mismatched target: %d %s (refreshed=%v)", rec.Code, rec.Body, refreshed)
	}
	releaseFor = func(*Server, *http.Request, bool) updateInfo {
		return updateInfo{Current: "0.7.1", Latest: "v0.7.1", Available: false, Checked: time.Now()}
	}
	if rec := post(`{"target":"0.7.1"}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "up_to_date") {
		t.Errorf("up to date: %d %s", rec.Code, rec.Body)
	}
	if s.updateIsRunning() {
		t.Error("nothing should have started")
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote(`it's a /tmp/weird'path`); got != `'it'\''s a /tmp/weird'\''path'` {
		t.Errorf("shellQuote = %s", got)
	}
}
