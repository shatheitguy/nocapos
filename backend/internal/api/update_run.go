package api

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Settings → General → Software Update → Update Now: runs the installer
// against this server itself, so updating never needs a terminal. The
// installer replaces alfad's own binary and restarts the service partway
// through, which kills this process (and everything in its process group)
// — so the update command is started detached (its own session, see
// update_detach_*.go) and logs to a file instead of in memory, which a
// freshly-restarted alfad can still read. install.sh itself keeps a copy of
// the previous version and the database, and rolls back if the new build
// doesn't come up.

const updateRunCommand = "curl -fsSL https://github.com/shatheitguy/nocapos/releases/latest/download/install.sh | bash -s -- --yes"

const updateLogMax = 64 << 10 // tail kept in the status response

var updateStarting sync.Mutex // guards against a double-click before the marker file lands

func (s *Server) updateLogPath() string  { return filepath.Join(s.Config.DataDir, "update.log") }
func (s *Server) updateLockPath() string { return filepath.Join(s.Config.DataDir, "update.running") }

// canSelfUpdate: only a native install, running as root, can replace its own
// binary and restart its own service.
func (s *Server) canSelfUpdate() bool { return s.Config.SystemRoot }

func (s *Server) systemUpdateRun(w http.ResponseWriter, r *http.Request) {
	if !s.canSelfUpdate() {
		writeError(w, http.StatusConflict, "unsupported",
			"Updating from here needs NoCapOS installed directly on Linux, running as root. "+
				"Run the installer yourself: "+updateRunCommand)
		return
	}
	if !updateStarting.TryLock() {
		writeError(w, http.StatusConflict, "already_running", "An update is already running")
		return
	}
	defer updateStarting.Unlock()
	if s.updateIsRunning() {
		writeError(w, http.StatusConflict, "already_running", "An update is already running")
		return
	}

	logPath, lockPath := s.updateLogPath(), s.updateLockPath()
	logFile, err := os.Create(logPath)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := os.WriteFile(lockPath, []byte(time.Now().UTC().Format(time.RFC3339)), 0o600); err != nil {
		logFile.Close()
		s.internalError(w, r, err)
		return
	}

	// The shell removes its own lock file on exit, success or failure, so a
	// crash never leaves "update in progress" stuck forever.
	cmd := exec.Command("bash", "-c", fmt.Sprintf("(%s); rc=$?; rm -f %s; exit $rc", updateRunCommand, shellQuote(lockPath)))
	cmd.Stdout, cmd.Stderr = logFile, logFile
	detachFromParent(cmd)
	if err := cmd.Start(); err != nil {
		logFile.Close()
		os.Remove(lockPath)
		s.internalError(w, r, err)
		return
	}
	logFile.Close() // the child has its own duplicated fd; this one is just ours
	s.audit(r, "", "system.update.run", "", true, "")
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (s *Server) updateIsRunning() bool {
	_, err := os.Stat(s.updateLockPath())
	return err == nil
}

func (s *Server) systemUpdateRunStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"running": s.updateIsRunning(), "version": s.Version, "supported": s.canSelfUpdate()}
	if b, err := os.ReadFile(s.updateLogPath()); err == nil {
		if len(b) > updateLogMax {
			b = append([]byte("…\n"), b[len(b)-updateLogMax:]...)
		}
		out["log"] = string(b)
	}
	writeJSON(w, http.StatusOK, out)
}
