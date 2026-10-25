package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"alfaos/alfad/internal/config"
)

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

func TestUpdateRunStatus(t *testing.T) {
	s := &Server{Deps: Deps{Config: &config.Config{DataDir: t.TempDir(), SystemRoot: true}, Version: "0.6.0"}}
	if s.updateIsRunning() {
		t.Fatal("nothing started yet")
	}

	lock := s.updateLockPath()
	if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, []byte("now"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !s.updateIsRunning() {
		t.Fatal("lock file present: should report running")
	}
	if err := os.WriteFile(s.updateLogPath(), []byte("$ installing…\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	s.systemUpdateRunStatus(rec, httptest.NewRequest(http.MethodGet, "/api/v1/system/update/run", nil))
	body := rec.Body.String()
	for _, want := range []string{`"running":true`, `"supported":true`, `installing`} {
		if !strings.Contains(body, want) {
			t.Errorf("status missing %q in %s", want, body)
		}
	}

	os.Remove(lock)
	if s.updateIsRunning() {
		t.Error("removed the lock: should no longer report running")
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote(`it's a /tmp/weird'path`); got != `'it'\''s a /tmp/weird'\''path'` {
		t.Errorf("shellQuote = %s", got)
	}
}
