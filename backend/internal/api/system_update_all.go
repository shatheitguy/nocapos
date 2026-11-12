package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"alfaos/alfad/internal/syspkg"
)

// Settings → Glow Up → Update everything: one run that repairs the system's
// packages when they need it, installs the system updates, then updates
// NoCapOS itself. The package part is a Packages job (one package task at a
// time, audited); the NoCapOS part is the usual self-update, which restarts
// alfad. The run is kept in update-all.json, so how it ended is still known
// after that restart.

const (
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
	From     string     `json:"from,omitempty"`   // NoCapOS version before
	Target   string     `json:"target,omitempty"` // NoCapOS version to install ("" none)
	OK       bool       `json:"ok"`
	Error    string     `json:"error,omitempty"`
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
	u.Phase, u.Ended, u.OK, u.Error = updateAllDone, &now, ok, msg
}

// updateAllReq is the body of POST /api/v1/system/update/all.
type updateAllReq struct {
	// Target: the NoCapOS version the UI showed ("" = only system packages).
	Target string `json:"target,omitempty"`
	// AllowRemove: removals the person saw in the preview and confirmed.
	AllowRemove []string `json:"allow_remove,omitempty"`
}

func (s *Server) systemUpdateAll(w http.ResponseWriter, r *http.Request) {
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
	if len(steps) == 0 && installCmd == "" {
		writeError(w, http.StatusConflict, "up_to_date", "Everything is up to date.")
		return
	}

	ar := r.Clone(context.WithoutCancel(r.Context()))
	uid := userFrom(r.Context()).ID
	if len(steps) == 0 {
		if err := s.startUpdate(installCmd, run.Target); err != nil {
			s.internalError(w, r, err)
			return
		}
		run.Phase = updateAllNoCapOS
		s.writeUpdateAll(run)
		s.audit(r, uid, "system.update.all", "", true, s.Version+" -> "+run.Target)
		writeJSON(w, http.StatusAccepted, map[string]any{"run": run})
		return
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
		if errors.Is(err, syspkg.ErrBusy) {
			writeError(w, http.StatusConflict, "busy", err.Error())
			return
		}
		s.internalError(w, r, err)
		return
	}
	sysPkgCache.Lock()
	sysPkgCache.jobID = job.ID
	sysPkgCache.Unlock()
	run.Phase, run.PkgJob = updateAllPackages, job.ID
	s.writeUpdateAll(run)
	writeJSON(w, http.StatusAccepted, map[string]any{"run": run, "job": job})
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
