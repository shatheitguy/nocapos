package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func stubInstallerActive(t *testing.T, active bool) {
	t.Helper()
	old := updateInstallerActive
	updateInstallerActive = func() bool { return active }
	t.Cleanup(func() { updateInstallerActive = old })
}

func TestVersionAhead(t *testing.T) {
	for _, tc := range []struct {
		target, running string
		want            bool
	}{
		{"0.8.1", "0.8.0", true},
		{"v0.8.1", "0.8.0", true},
		{"0.8.0", "0.8.0", false},
		{"0.7.9", "0.8.0", false},
		{"", "0.8.0", false},
		{"  ", "0.8.0", false},
		{"0.8.1", "dev", true}, // can't compare: a different version counts
		{"dev", "dev", false},
	} {
		if got := versionAhead(tc.target, tc.running); got != tc.want {
			t.Errorf("versionAhead(%q, %q) = %v, want %v", tc.target, tc.running, got, tc.want)
		}
	}
}

func TestStaleFailure(t *testing.T) {
	since := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	before, after := since.Add(-time.Hour), since.Add(time.Hour)
	for _, tc := range []struct {
		name string
		res  *updateResult
		want bool
	}{
		{"nil", nil, false},
		{"ok results stay", &updateResult{OK: true, To: "0.8.0", FinishedAt: before}, false},
		{"no target", &updateResult{To: "", FinishedAt: after}, true},
		{"target running now", &updateResult{To: "0.8.0", FinishedAt: after}, true},
		{"older target", &updateResult{To: "0.7.2", FinishedAt: after}, true},
		{"before the version changed", &updateResult{To: "0.8.1", FinishedAt: before}, true},
		{"real failure (rolled back)", &updateResult{From: "0.8.0", To: "0.8.1", FinishedAt: after}, false},
	} {
		if got := staleFailure(tc.res, "0.8.0", since); got != tc.want {
			t.Errorf("%s: staleFailure = %v, want %v", tc.name, got, tc.want)
		}
	}
	if staleFailure(&updateResult{To: "0.8.1", FinishedAt: before}, "0.8.0", time.Time{}) {
		t.Error("with no version history, a real failure stays")
	}
}

// The Raspberry Pi case: an older NoCapOS's updater was killed with the
// service and left its bare-timestamp lock behind; the owner then updated
// with the one-line installer. That must not show up as a failed update.
func TestLegacyLockClearedSilently(t *testing.T) {
	s := newUpdateServer(t, "0.8.0")
	stubUnitAlive(t, false)
	stubInstallerActive(t, false)
	os.WriteFile(s.updateLockPath(), []byte(time.Now().Add(-10*time.Minute).UTC().Format(time.RFC3339)), 0o600)
	st := getRunStatus(t, s)
	if st.Running || st.Result != nil || st.Stage == "failed" {
		t.Errorf("legacy lock should be cleared without a result: %+v / %+v", st, st.Result)
	}
	if _, err := os.Stat(s.updateLockPath()); err == nil {
		t.Error("the lock should be gone")
	}
	if s.readUpdateResult() != nil {
		t.Error("no failure should be recorded")
	}

	// While an installer unit still runs, a recent legacy lock is trusted.
	stubInstallerActive(t, true)
	os.WriteFile(s.updateLockPath(), []byte(time.Now().Add(-10*time.Minute).UTC().Format(time.RFC3339)), 0o600)
	if !s.updateIsRunning() {
		t.Error("legacy lock with an installer running should count as running")
	}
}

func TestStaleLockForRunningVersionClearedSilently(t *testing.T) {
	// The update's target is what runs now: it got there (another way).
	s := newUpdateServer(t, "0.8.1")
	stubUnitAlive(t, false)
	writeLock(t, s, updateLock{Started: time.Now().Add(-10 * time.Minute), From: "0.8.0", To: "0.8.1", Unit: "nocapos-update-1"})
	st := getRunStatus(t, s)
	if st.Running || st.Result != nil {
		t.Errorf("should be cleared silently: %+v / %+v", st, st.Result)
	}
	if _, err := os.Stat(s.updateLockPath()); err == nil {
		t.Error("the lock should be gone")
	}
}

func TestBogusFailureDropped(t *testing.T) {
	// What the Pi has on disk now: the fake failure with no target.
	s := newUpdateServer(t, "0.8.0")
	res := `{"ok":false,"from":"","to":"","exit_code":-1,"finished_at":"2026-10-31T18:19:53Z","error":"The updater stopped before it finished."}`
	os.WriteFile(s.updateResultPath(), []byte(res), 0o600)
	st := getRunStatus(t, s)
	if st.Result != nil || st.Stage == "failed" {
		t.Errorf("a failure with no target should be dropped: %+v / %+v", st, st.Result)
	}
	if _, err := os.Stat(s.updateResultPath()); err == nil {
		t.Error("the bogus result should be removed")
	}

	// A failure from before this version first ran is old news.
	s = newUpdateServer(t, "0.8.1")
	seen, _ := json.Marshal(versionSeen{Version: "0.8.1", Since: time.Now().UTC()})
	os.WriteFile(filepath.Join(s.Config.DataDir, "version-seen.json"), seen, 0o600)
	res = `{"ok":false,"from":"0.8.1","to":"0.8.2","exit_code":1,"finished_at":"2026-10-31T18:19:53Z"}`
	os.WriteFile(s.updateResultPath(), []byte(res), 0o600)
	if st := getRunStatus(t, s); st.Result != nil {
		t.Errorf("an old failure should be dropped: %+v", st.Result)
	}

	// A fresh real failure stays.
	res = `{"ok":false,"from":"0.8.1","to":"0.8.2","exit_code":1,"finished_at":"` + time.Now().Add(time.Minute).UTC().Format(time.RFC3339) + `"}`
	os.WriteFile(s.updateResultPath(), []byte(res), 0o600)
	if st := getRunStatus(t, s); st.Result == nil || st.Result.OK || st.Stage != "failed" {
		t.Errorf("a real failure should stay: %+v", st)
	}
}

func TestVersionSinceRecordsChanges(t *testing.T) {
	s := newUpdateServer(t, "0.8.0")
	a := s.versionSince()
	if a.IsZero() {
		t.Fatal("no time recorded")
	}
	if b := s.versionSince(); !b.Equal(a) {
		t.Errorf("same version, new time: %v vs %v", a, b)
	}
	s.Version = "0.8.1"
	if c := s.versionSince(); c.Before(a) {
		t.Errorf("a new version should get a new time: %v", c)
	}
}
