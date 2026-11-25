package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"alfaos/alfad/internal/syspkg"
)

// Settings → Glow Up → Update everything: one run that updates the App
// Store apps that have updates, repairs the system's packages when they need
// it, installs the system updates, then updates NoCapOS itself. The package part is a Packages job (one package task at a
// time, audited); the NoCapOS part is the usual self-update, which restarts
// alfad. The run is kept in update-all.json, so how it ended is still known
// after that restart.

const (
	updateAllApps     = "apps"     // App Store apps are being updated
	updateAllPackages = "packages" // system packages are being repaired / updated
	updateAllNoCapOS  = "nocapos"  // NoCapOS is updating itself
	updateAllDone     = "done"
)

// updateAll is update-all.json: the latest "Update everything" run.
type updateAll struct {
	Started  time.Time  `json:"started"`
	Ended    *time.Time `json:"ended,omitempty"`
	Phase    string     `json:"phase"`
	Packages int        `json:"packages"`         // system updates it included
	Security int        `json:"security"`         // ... of which security fixes
	Repair   bool       `json:"repair,omitempty"` // the packages needed a repair first
	Kernel   bool       `json:"kernel,omitempty"` // a kernel was among them
	PkgJob   string     `json:"pkg_job,omitempty"`
	// App Store apps it updates first, how many are done, and the ones that failed.
	Apps       []string `json:"apps,omitempty"`
	AppsDone   int      `json:"apps_done,omitempty"`
	AppsFailed []string `json:"apps_failed,omitempty"`
	From       string   `json:"from,omitempty"`   // NoCapOS version before
	Target     string   `json:"target,omitempty"` // NoCapOS version to install ("" none)
	OK         bool     `json:"ok"`
	Error      string   `json:"error,omitempty"`
	// PhaseStarted: when each phase began, so a slow update shows which
	// part took the time.
	PhaseStarted map[string]time.Time `json:"phase_started,omitempty"`
}

// updateAllStatus is GET /api/v1/system/update/all.
type updateAllStatus struct {
	Run           *updateAll       `json:"run"`
	Running       bool             `json:"running"`
	Job           *syspkg.Job      `json:"job,omitempty"`     // the package part (with its output)
	NoCapOS       *updateRunStatus `json:"nocapos,omitempty"` // the NoCapOS part, once it started
	RestartNeeded bool             `json:"restart_needed"`
}

var updateAllMu sync.Mutex

// updateAllAppsRunning: the apps part of a run is going (it lives in this
// process, so a restart ends it).
var updateAllAppsRunning atomic.Bool

func (s *Server) updateAllPath() string { return filepath.Join(s.Config.DataDir, "update-all.json") }

func (s *Server) readUpdateAll() *updateAll {
	b, err := os.ReadFile(s.updateAllPath())
	if err != nil {
		return nil
	}
	var u updateAll
	if json.Unmarshal(b, &u) != nil || u.Phase == "" {
		return nil
	}
	return &u
}

func (s *Server) writeUpdateAll(u *updateAll) {
	if _, ok := u.PhaseStarted[u.Phase]; !ok && u.Phase != "" {
		if u.PhaseStarted == nil {
			u.PhaseStarted = map[string]time.Time{}
		}
		u.PhaseStarted[u.Phase] = time.Now().UTC()
	}
	b, err := json.Marshal(u)
	if err != nil {
		return
	}
	tmp := s.updateAllPath() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		os.Rename(tmp, s.updateAllPath()) //nolint:errcheck
	}
}

func (u *updateAll) finish(ok bool, msg string) {
	now := time.Now().UTC()
	if len(u.AppsFailed) > 0 {
		ok = false
		note := "These apps didn't update: " + strings.Join(u.AppsFailed, ", ") + "."
		if msg == "" {
			msg = note
		} else {
			msg += " " + note
		}
	}
	u.Phase, u.Ended, u.OK, u.Error = updateAllDone, &now, ok, msg
}

// updateAllReq is the body of POST /api/v1/system/update/all.
type updateAllReq struct {
	// Target: the NoCapOS version the UI showed ("" = only system packages).
	Target string `json:"target,omitempty"`
	// AllowRemove: removals the person saw in the preview and confirmed.
	AllowRemove []string `json:"allow_remove,omitempty"`
	// Apps: App Store apps with updates to update first.
	Apps []string `json:"apps,omitempty"`
}

func (s *Server) systemUpdateAll(w http.ResponseWriter, r *http.Request) {
	if s.imageOS() {
		writeError(w, http.StatusConflict, "image_os", imageOSMessage)
		return
	}
	var req updateAllReq
	if r.ContentLength > 0 && !decodeJSON(w, r, &req) {
		return
	}
	if req.Target != "" && !s.canSelfUpdate() {
		writeError(w, http.StatusConflict, "unsupported", "Updating NoCapOS from here needs it installed directly on Linux, running as root.")
		return
	}
	if !updateStarting.TryLock() {
		writeError(w, http.StatusConflict, "already_running", "An update is already running")
		return
	}
	defer updateStarting.Unlock()
	updateAllMu.Lock()
	defer updateAllMu.Unlock()
	if s.updateIsRunning() {
		writeError(w, http.StatusConflict, "already_running", "An update is already running")
		return
	}
	if prev := s.readUpdateAll(); prev != nil && prev.Phase == updateAllPackages && s.pkgJobs != nil {
		if j, ok := s.pkgJobs.Get(prev.PkgJob); ok && j.Running {
			writeError(w, http.StatusConflict, "already_running", "An update is already running")
			return
		}
	}
	if updateAllAppsRunning.Load() {
		writeError(w, http.StatusConflict, "already_running", "An update is already running")
		return
	}
	if s.pkgJobs != nil && s.pkgJobs.Busy() {
		writeError(w, http.StatusConflict, "busy", "A package task is running. Let it finish, then try again.")
		return
	}

	run := &updateAll{Started: time.Now().UTC(), From: s.Version}

	// The NoCapOS part: the release the UI promised.
	var installCmd string
	if req.Target != "" {
		cmd, to, ok := s.updateTarget(w, r, req.Target)
		if !ok {
			return
		}
		installCmd, run.Target = cmd, to
	}

	// The system part: pending updates, or a broken package state to repair.
	var steps []syspkg.Step
	if s.pkgAdmin != nil && s.pkgJobs != nil {
		if st := s.pkgAdmin.Status(); st.Supported {
			if ups, err := s.sysPkgList(r.Context(), sysPkgListMaxAge); err == nil {
				for _, u := range ups {
					if u.Held {
						continue
					}
					run.Packages++
					if u.Security {
						run.Security++
					}
					run.Kernel = run.Kernel || u.Kernel
				}
			}
			run.Repair = s.pkgHealth(r.Context()).Broken
			if run.Packages > 0 || run.Repair {
				var err error
				steps, err = s.pkgChangeSteps(r.Context(), st.Manager, syspkg.ActUpgradeAll, nil, req.AllowRemove)
				if err != nil {
					writeError(w, http.StatusBadRequest, "bad_request", err.Error())
					return
				}
			}
		}
	}
	// The apps part: the ones asked for that do have an update.
	run.Apps = s.appsToUpdate(r.Context(), req.Apps)
	if len(steps) == 0 && installCmd == "" && len(run.Apps) == 0 {
		writeError(w, http.StatusConflict, "up_to_date", "Everything is up to date.")
		return
	}

	ar := r.Clone(context.WithoutCancel(r.Context()))
	uid := userFrom(r.Context()).ID
	if len(run.Apps) > 0 {
		// The apps first (in the background), then the rest of the run.
		run.Phase = updateAllApps
		s.writeUpdateAll(run)
		updateAllAppsRunning.Store(true)
		go s.updateAllAppsThenRest(run.Apps, steps, installCmd, ar, uid)
		writeJSON(w, http.StatusAccepted, map[string]any{"run": run})
		return
	}
	job, err := s.updateAllRest(run, steps, installCmd, ar, uid)
	if err != nil {
		if errors.Is(err, syspkg.ErrBusy) {
			writeError(w, http.StatusConflict, "busy", err.Error())
			return
		}
		s.internalError(w, r, err)
		return
	}
	out := map[string]any{"run": run}
	if job != nil {
		out["job"] = job
	}
	writeJSON(w, http.StatusAccepted, out)
}

// appsToUpdate keeps the asked-for App Store apps that are installed and have an update.
func (s *Server) appsToUpdate(ctx context.Context, ids []string) []string {
	if len(ids) == 0 || s.AppStore == nil {
		return nil
	}
	status, err := s.AppStore.Status(ctx)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		if in := status[id]; in != nil && in.UpdateAvailable && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// updateAllAppsThenRest updates the apps one at a time, then goes on with
// the system packages and NoCapOS. An app that fails doesn't stop the rest;
// the run ends naming it.
func (s *Server) updateAllAppsThenRest(apps []string, steps []syspkg.Step, installCmd string, r *http.Request, uid string) {
	defer updateAllAppsRunning.Store(false)
	var failed []string
	for i, id := range apps {
		done := make(chan error, 1)
		_, err := s.AppStore.Update(context.Background(), id, func(e error) { done <- e })
		if err == nil {
			err = <-done
		}
		if err != nil {
			s.Log.Warn("update everything: app", "app", id, "err", err)
			failed = append(failed, s.appName(id))
		}
		s.audit(r, uid, "appstore.update", id, err == nil, errText(err))
		updateAllMu.Lock()
		if run := s.readUpdateAll(); run != nil && run.Phase == updateAllApps {
			run.AppsDone, run.AppsFailed = i+1, failed
			s.writeUpdateAll(run)
		}
		updateAllMu.Unlock()
	}
	updateAllMu.Lock()
	run := s.readUpdateAll()
	updateAllMu.Unlock()
	if run == nil || run.Phase != updateAllApps {
		return
	}
	if _, err := s.updateAllRest(run, steps, installCmd, r, uid); err != nil {
		updateAllMu.Lock()
		run.finish(false, "The system updates didn't start: "+err.Error())
		s.writeUpdateAll(run)
		updateAllMu.Unlock()
	}
}

// updateAllRest starts the system packages (then NoCapOS), or NoCapOS alone,
// or ends the run when there's nothing else to do. It writes the run.
func (s *Server) updateAllRest(run *updateAll, steps []syspkg.Step, installCmd string, ar *http.Request, uid string) (*syspkg.Job, error) {
	if len(steps) == 0 {
		if installCmd == "" {
			run.finish(true, "")
			s.writeUpdateAll(run)
			s.audit(ar, uid, "system.update.all", "", run.OK, run.Error)
			return nil, nil
		}
		if err := s.startUpdate(installCmd, run.Target); err != nil {
			return nil, err
		}
		run.Phase = updateAllNoCapOS
		s.writeUpdateAll(run)
		s.audit(ar, uid, "system.update.all", "", true, s.Version+" -> "+run.Target)
		return nil, nil
	}

	installed := s.sysPkgInstalled(run.Kernel)
	job, err := s.pkgJobs.Start(syspkg.ActUpgradeAll, "system", steps, func(j syspkg.Job) {
		s.pkgAdmin.Invalidate()
		sysPkgForget()
		installed(j)
		s.audit(ar, uid, "system.packages."+syspkg.ActUpgradeAll, "", j.OK, j.Error)
		s.updateAllPackagesDone(j, installCmd, ar, uid)
	})
	if err != nil {
		return nil, err
	}
	sysPkgCache.Lock()
	sysPkgCache.jobID = job.ID
	sysPkgCache.Unlock()
	run.Phase, run.PkgJob = updateAllPackages, job.ID
	s.writeUpdateAll(run)
	return &job, nil
}

// updateAllPackagesDone moves the run on once the system packages are done:
// on to NoCapOS, or finished.
func (s *Server) updateAllPackagesDone(j syspkg.Job, installCmd string, r *http.Request, uid string) {
	updateAllMu.Lock()
	defer updateAllMu.Unlock()
	run := s.readUpdateAll()
	if run == nil || run.PkgJob != j.ID {
		return
	}
	switch {
	case !j.OK:
		msg := j.Error
		if msg == "" {
			msg = "The system updates didn't install."
		}
		if run.Target != "" {
			msg += " NoCapOS wasn't updated."
		}
		run.finish(false, msg)
	case installCmd == "":
		run.finish(true, "")
	default:
		if err := s.startUpdate(installCmd, run.Target); err != nil {
			run.finish(false, "The system updates installed, but the NoCapOS update didn't start: "+err.Error())
		} else {
			run.Phase = updateAllNoCapOS
		}
	}
	s.writeUpdateAll(run)
	detail := ""
	if run.Target != "" {
		detail = s.Version + " -> " + run.Target
	}
	if run.Phase == updateAllDone {
		s.audit(r, uid, "system.update.all", "", run.OK, run.Error)
	} else {
		s.audit(r, uid, "system.update.all", "", true, detail)
	}
}

func (s *Server) systemUpdateAllStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.updateAllStatus())
}

func (s *Server) updateAllStatus() updateAllStatus {
	updateAllMu.Lock()
	defer updateAllMu.Unlock()
	out := updateAllStatus{RestartNeeded: s.restartNeeded()}
	run := s.readUpdateAll()
	if run == nil {
		return out
	}
	if run.PkgJob != "" && s.pkgJobs != nil {
		if j, ok := s.pkgJobs.Get(run.PkgJob); ok {
			out.Job = &j
		}
	}
	switch run.Phase {
	case updateAllApps:
		if !updateAllAppsRunning.Load() {
			run.finish(false, "NoCapOS restarted while the apps were updating. Check for updates and try again.")
			s.writeUpdateAll(run)
		}
	case updateAllPackages:
		if out.Job == nil {
			// alfad restarted while the packages were installing.
			run.finish(false, "NoCapOS restarted while the system updates were installing. Check for updates and try again.")
			s.writeUpdateAll(run)
		}
	case updateAllNoCapOS:
		st := s.updateRunStatus()
		out.NoCapOS = &st
		if !st.Running {
			switch {
			case st.Result != nil:
				run.finish(st.Result.OK, st.Result.Error)
			case sameVersion(s.Version, run.Target):
				run.finish(true, "")
			case time.Since(run.Started) > updateUnitGrace:
				run.finish(false, "The NoCapOS update stopped before it finished.")
			}
			if run.Phase == updateAllDone {
				s.writeUpdateAll(run)
			}
		}
	}
	out.Run = run
	out.Running = run.Phase != updateAllDone
	return out
}
