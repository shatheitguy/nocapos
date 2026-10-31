package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/release"
)

// Settings → General → Software Update → Update Now: runs the installer
// against this server itself, so updating never needs a terminal.
//
// The installer stops and restarts nocapos.service partway through. Stopping
// a unit kills every process in its cgroup, and a new session (setsid) is
// still in that cgroup — so an updater started as a plain child of alfad
// dies the moment install.sh stops the service, halfway through installing.
// It therefore runs in a transient unit of its own (systemd-run), which a
// stop or restart of nocapos.service doesn't touch. Everything it needs to
// report survives the restart on disk in DataDir:
//
//	update.log          what the installer printed (progress is derived from its "==>" lines)
//	update.running      lock: present while the updater runs (JSON: started, from, to, unit)
//	update-result.json  written by the updater itself when it ends: ok, exit code, when
//
// install.sh keeps a copy of the previous version and the database, and rolls
// back if the new build doesn't come up.

// releaseDownloadBase + tag is where one release's files live. The update is
// pinned to the exact release the check found (never .../releases/latest),
// so it installs the version Settings says it will.
const releaseDownloadBase = "https://github.com/shatheitguy/nocapos/releases/download/"

const (
	updateLogMax        = 64 << 10         // tail kept in the status response
	updateLogParseMax   = 1 << 20          // how much of the log the progress parser reads
	updateStaleAfter    = 90 * time.Minute // a lock with no unit to check is given up on after this
	updateUnitGrace     = 20 * time.Second // a just-started unit may not be visible to systemctl yet
	updateUnitPrefix    = "nocapos-update-"
	updateResultTimeFmt = "+%Y-%m-%dT%H:%M:%SZ"
)

var updateStarting sync.Mutex // guards against a double-click before the lock file lands

// updateTagRe: release tags as the release workflow makes them (v1.2.3),
// strict enough to be safe inside a shell command.
var updateTagRe = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$`)

func (s *Server) updateLogPath() string  { return filepath.Join(s.Config.DataDir, "update.log") }
func (s *Server) updateLockPath() string { return filepath.Join(s.Config.DataDir, "update.running") }
func (s *Server) updateResultPath() string {
	return filepath.Join(s.Config.DataDir, "update-result.json")
}

// canSelfUpdate: only a native install, running as root, can replace its own
// binary and restart its own service.
func (s *Server) canSelfUpdate() bool { return s.Config.SystemRoot }

// updateLock is the content of update.running.
type updateLock struct {
	Started time.Time `json:"started"`
	From    string    `json:"from"`
	To      string    `json:"to"`
	Unit    string    `json:"unit,omitempty"` // the transient systemd unit, when there is one
}

// updateResult is update-result.json: how the last update ended.
type updateResult struct {
	OK         bool      `json:"ok"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	ExitCode   int       `json:"exit_code"`
	FinishedAt time.Time `json:"finished_at"`
	RolledBack bool      `json:"rolled_back"`
	// NoCapOS itself is as it was: the run failed before replacing it, or it
	// was rolled back.
	Unchanged bool   `json:"unchanged"`
	Error     string `json:"error,omitempty"`
}

// updateRunStatus is GET /api/v1/system/update/run.
type updateRunStatus struct {
	Running     bool          `json:"running"`
	Supported   bool          `json:"supported"`
	Version     string        `json:"version"`
	Target      string        `json:"target,omitempty"`
	Stage       string        `json:"stage,omitempty"`
	Percent     int           `json:"percent"`
	RollingBack bool          `json:"rolling_back,omitempty"`
	Started     *time.Time    `json:"started,omitempty"`
	Log         string        `json:"log,omitempty"`
	Result      *updateResult `json:"result"`
}

// updateInstallCommand: the one-line installer, pinned to tag both for
// install.sh itself and for the bundle it downloads (NOCAP_RELEASE_URL).
func updateInstallCommand(tag string) (string, error) {
	if !updateTagRe.MatchString(tag) {
		return "", fmt.Errorf("unexpected release tag %q", tag)
	}
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	base := shellQuote(releaseDownloadBase + tag)
	return "curl -fsSL " + base + "/install.sh | NOCAP_RELEASE_URL=" + base + " bash -s -- --yes", nil
}

// updateScript wraps the install command: output appended to the log, then
// the result file written (atomically) and the lock removed, success or not.
func updateScript(installCmd, logPath, resultPath, lockPath, from, to string) string {
	head, _ := json.Marshal(map[string]string{"from": from, "to": to})
	prefix := strings.TrimSuffix(string(head), "}") + ","
	res, tmp := shellQuote(resultPath), shellQuote(resultPath+".tmp")
	return strings.Join([]string{
		"exec >>" + shellQuote(logPath) + " 2>&1",
		"(" + installCmd + "); rc=$?",
		`if [ "$rc" -eq 0 ]; then ok=true; else ok=false; fi`,
		`printf '%s"ok":%s,"exit_code":%d,"finished_at":"%s"}\n' ` + shellQuote(prefix) + ` "$ok" "$rc" "$(date -u ` + updateResultTimeFmt + `)" >` + tmp + " && mv -f " + tmp + " " + res,
		"rm -f " + shellQuote(lockPath),
		`exit "$rc"`,
	}, "\n")
}

// updateLaunch builds the command that starts script: in its own transient
// systemd unit when systemd-run is available (systemdRun is its path), else
// as a plain detached child (see update_detach_*.go) — which only survives
// install.sh restarting the service if alfad isn't running under systemd.
func updateLaunch(systemdRun, unit, script string) (name string, args []string) {
	if systemdRun != "" {
		return systemdRun, []string{
			"--unit=" + unit, "--collect", "--quiet",
			"--description=NoCapOS update",
			"--property=Type=exec",
			"/bin/bash", "-c", script,
		}
	}
	return "bash", []string{"-c", script}
}

// lookSystemdRun is replaced in tests.
var lookSystemdRun = func() string {
	p, err := exec.LookPath("systemd-run")
	if err != nil {
		return ""
	}
	return p
}

// updateUnitAlive reports whether a transient update unit is still running;
// replaced in tests.
var updateUnitAlive = func(unit string) bool {
	out, _ := exec.Command("systemctl", "is-active", unit).Output()
	switch strings.TrimSpace(string(out)) {
	case "active", "activating", "deactivating", "reloading":
		return true
	}
	return false
}

// releaseFor returns the release info for the update, refreshing the cached
// check when asked to; replaced in tests.
var releaseFor = func(s *Server, r *http.Request, refresh bool) updateInfo {
	updateCache.Lock()
	defer updateCache.Unlock()
	if c := updateCache.info; c != nil && !refresh && time.Since(c.Checked) < time.Hour && c.Error == "" {
		return *c
	}
	info := checkRelease(r.Context(), s.Version)
	updateCache.info = &info
	return info
}

func sameVersion(a, b string) bool {
	return strings.TrimPrefix(strings.TrimSpace(a), "v") == strings.TrimPrefix(strings.TrimSpace(b), "v")
}

func (s *Server) systemUpdateRun(w http.ResponseWriter, r *http.Request) {
	if !s.canSelfUpdate() {
		writeError(w, http.StatusConflict, "unsupported",
			"Updating from here needs NoCapOS installed directly on Linux, running as root. "+
				"Run the installer yourself: "+release.UpdateCommand)
		return
	}
	// The version the UI showed, so what gets installed is what was promised.
	var req struct {
		Target string `json:"target"`
	}
	if r.ContentLength > 0 && !decodeJSON(w, r, &req) {
		return
	}
	if !updateStarting.TryLock() {
		writeError(w, http.StatusConflict, "already_running", "An update is already running")
		return
	}
	defer updateStarting.Unlock()
	if _, running := s.updateState(); running {
		writeError(w, http.StatusConflict, "already_running", "An update is already running")
		return
	}

	info := releaseFor(s, r, false)
	if req.Target != "" && info.Error == "" && !sameVersion(req.Target, info.Latest) {
		info = releaseFor(s, r, true)
	}
	if info.Error != "" {
		writeError(w, http.StatusBadGateway, "check_failed", info.Error)
		return
	}
	if req.Target != "" && !sameVersion(req.Target, info.Latest) {
		writeError(w, http.StatusConflict, "release_changed",
			"There's a newer release now ("+info.Latest+"). Check again, then update.")
		return
	}
	if !info.Available && !info.Dev {
		writeError(w, http.StatusConflict, "up_to_date", "You're already on the latest version")
		return
	}
	installCmd, err := updateInstallCommand(info.Latest)
	if err != nil {
		writeError(w, http.StatusBadGateway, "check_failed", "The latest release has an unexpected version number")
		return
	}
	to := strings.TrimPrefix(info.Latest, "v")

	if err := s.startUpdate(installCmd, to); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "", "system.update.run", "", true, s.Version+" -> "+to)
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true, "from": s.Version, "target": to})
}

func (s *Server) startUpdate(installCmd, to string) error {
	logPath, lockPath, resultPath := s.updateLogPath(), s.updateLockPath(), s.updateResultPath()
	header := fmt.Sprintf("==> Updating NoCapOS from %s to %s\n", s.Version, to)
	if err := os.WriteFile(logPath, []byte(header), 0o600); err != nil {
		return err
	}
	os.Remove(resultPath)

	now := time.Now().UTC().Truncate(time.Second) // the result's time has whole seconds
	systemdRun := lookSystemdRun()
	lock := updateLock{Started: now, From: s.Version, To: to}
	if systemdRun != "" {
		lock.Unit = updateUnitPrefix + strconv.FormatInt(now.Unix(), 10)
	}
	b, _ := json.Marshal(lock)
	if err := os.WriteFile(lockPath, b, 0o600); err != nil {
		return err
	}

	name, args := updateLaunch(systemdRun, lock.Unit, updateScript(installCmd, logPath, resultPath, lockPath, s.Version, to))
	cmd := exec.Command(name, args...)
	if systemdRun != "" {
		// systemd-run returns once the unit has started; its own output is
		// only an error message, if any.
		if out, err := cmd.CombinedOutput(); err != nil {
			os.Remove(lockPath)
			return fmt.Errorf("systemd-run: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	detachFromParent(cmd)
	if err := cmd.Start(); err != nil {
		os.Remove(lockPath)
		return err
	}
	go cmd.Wait() //nolint:errcheck // reap it; the script reports through files
	return nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// readUpdateLock reads update.running. An older alfad wrote just a
// timestamp; that still counts as a lock (started = the file's time).
func (s *Server) readUpdateLock() (*updateLock, error) {
	path := s.updateLockPath()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var l updateLock
	if json.Unmarshal(b, &l) != nil || l.Started.IsZero() {
		l = updateLock{}
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b))); err == nil {
			l.Started = t
		} else if fi, err := os.Stat(path); err == nil {
			l.Started = fi.ModTime()
		}
	}
	return &l, nil
}

// updateState: the lock (if any) and whether the updater is really still
// running. A lock whose updater is gone is stale.
func (s *Server) updateState() (*updateLock, bool) {
	l, err := s.readUpdateLock()
	if err != nil {
		return nil, false
	}
	age := time.Since(l.Started)
	if l.Unit != "" {
		return l, age < updateUnitGrace || updateUnitAlive(l.Unit)
	}
	return l, age < updateStaleAfter
}

func (s *Server) updateIsRunning() bool {
	_, running := s.updateState()
	return running
}

func (s *Server) readUpdateResult() *updateResult {
	b, err := os.ReadFile(s.updateResultPath())
	if err != nil {
		return nil
	}
	var res updateResult
	if json.Unmarshal(b, &res) != nil {
		return nil
	}
	return &res
}

// readUpdateLog returns the whole log (up to updateLogParseMax from the end)
// with terminal colour codes removed.
func (s *Server) readUpdateLog() string {
	f, err := os.Open(s.updateLogPath())
	if err != nil {
		return ""
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > updateLogParseMax {
		f.Seek(fi.Size()-updateLogParseMax, 0) //nolint:errcheck
	}
	b := make([]byte, updateLogParseMax)
	n, _ := f.Read(b)
	return stripANSI(string(b[:n]))
}

func (s *Server) systemUpdateRunStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.updateRunStatus())
}

func (s *Server) updateRunStatus() updateRunStatus {
	out := updateRunStatus{Version: s.Version, Supported: s.canSelfUpdate()}
	log := s.readUpdateLog()
	prog := parseUpdateProgress(log)
	lock, running := s.updateState()
	res := s.readUpdateResult()

	if lock != nil && !running {
		// The updater is gone without saying how it ended (killed, or the
		// machine went down): record that, so this isn't "running" forever.
		if res == nil || res.FinishedAt.Before(lock.Started) {
			res = &updateResult{OK: false, From: lock.From, To: lock.To, ExitCode: -1, FinishedAt: time.Now().UTC(),
				Error: "The updater stopped before it finished."}
			if b, err := json.Marshal(res); err == nil {
				os.WriteFile(s.updateResultPath(), b, 0o600) //nolint:errcheck
			}
		}
		os.Remove(s.updateLockPath())
		lock = nil
	}

	out.Running = running
	if res != nil {
		res.RolledBack = prog.RolledBack
		res.Unchanged = !res.OK && (prog.RolledBack || prog.Percent < updateInstallPercent)
		if !res.OK && res.Error == "" {
			res.Error = prog.Error
		}
		if res.To == "" && lock != nil {
			res.To = lock.To
		}
	}
	switch {
	case running:
		out.Target = lock.To
		out.Started = &lock.Started
		out.Stage, out.Percent, out.RollingBack = prog.Stage, prog.Percent, prog.RollingBack
		if out.Stage == "" {
			out.Stage = "download"
		}
		if out.Stage == "failed" {
			out.Stage = "restart" // a rollback is still running
		}
		// A result from an earlier run says nothing about this one.
		if res != nil && res.FinishedAt.Before(lock.Started) {
			res = nil
		}
		if res != nil {
			// Finished; the lock goes a moment later.
			out.Running = false
		}
	case res != nil:
		out.Target = res.To
	}
	if !out.Running && res != nil {
		out.Percent = 100
		out.Stage = "done"
		if !res.OK {
			out.Stage = "failed"
		}
	}
	out.Result = res

	if len(log) > updateLogMax {
		log = "…\n" + log[len(log)-updateLogMax:]
	}
	out.Log = log
	return out
}
