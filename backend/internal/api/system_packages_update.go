package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/notify"
	"alfaos/alfad/internal/syspkg"
)

// Settings → Software Update → System packages: the host's own package
// updates, next to NoCapOS's. Checking and installing run as Packages jobs
// (one package task at a time, audited); the UI follows them through
// GET /api/v1/packages/jobs/{id}. A kernel update leaves a "restart needed"
// mark that lasts until the machine boots again.

const (
	sysPkgListMaxAge   = time.Minute      // re-read the update list at most this often
	sysPkgRefreshEvery = 20 * time.Hour   // the background check refreshes the lists about daily
	sysPkgCheckEvery   = 24 * time.Hour   // ... and looks for security updates this often
	sysPkgListTimeout  = 90 * time.Second // reading the update list
)

var sysPkgCache struct {
	sync.Mutex
	updates   []syspkg.Update
	readAt    time.Time // when updates was read
	refreshed time.Time // when this alfad last refreshed the package lists
	checked   time.Time // last background check
	jobID     string    // the latest check/install job started from Software Update
}

// sysPkgStatus is GET /api/v1/system/update/packages.
type sysPkgStatus struct {
	Supported     bool            `json:"supported"`
	Reason        string          `json:"reason,omitempty"`
	Manager       string          `json:"manager,omitempty"`
	Checked       *time.Time      `json:"checked,omitempty"` // when the package lists were last refreshed
	Updates       []syspkg.Update `json:"updates"`
	Security      int             `json:"security"`
	Kernel        bool            `json:"kernel"`         // a pending update needs a restart afterwards
	RestartNeeded bool            `json:"restart_needed"` // an installed update needs a restart now
	Job           *syspkg.Job     `json:"job,omitempty"`  // the latest check/install from here (without output)
	Health        syspkg.Health   `json:"health"`         // apt/dpkg left in a broken state: offer a repair
	Error         string          `json:"error,omitempty"`
}

func (s *Server) restartMarkPath() string { return filepath.Join(s.Config.DataDir, "restart-needed") }

func bootID() string {
	b, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(b))
}

// restartNeeded: the distribution says so (/run/reboot-required), or a
// kernel update was installed from here since the machine last booted.
func (s *Server) restartNeeded() bool {
	if _, err := os.Stat("/run/reboot-required"); err == nil {
		return true
	}
	b, err := os.ReadFile(s.restartMarkPath())
	if err != nil {
		return false
	}
	if id := bootID(); id == "" || strings.TrimSpace(string(b)) != id {
		os.Remove(s.restartMarkPath()) // the machine has restarted since
		return false
	}
	return true
}

func (s *Server) markRestartNeeded() {
	if id := bootID(); id != "" {
		os.WriteFile(s.restartMarkPath(), []byte(id+"\n"), 0o600) //nolint:errcheck
	}
}

// pkgListsRefreshedAt: when the package lists were last fetched, as far as
// this machine shows (apt and dnf keep them on disk).
func pkgListsRefreshedAt(mgr string) time.Time {
	var dirs []string
	switch mgr {
	case "apt":
		dirs = []string{"/var/lib/apt/lists", "/var/lib/apt/lists/partial"}
	case "dnf":
		dirs = []string{"/var/cache/dnf"}
	}
	var t time.Time
	for _, d := range dirs {
		if fi, err := os.Stat(d); err == nil && fi.ModTime().After(t) {
			t = fi.ModTime()
		}
	}
	return t
}

// sysPkgList returns the pending updates, re-reading them when the cached
// list is older than maxAge.
func (s *Server) sysPkgList(ctx context.Context, maxAge time.Duration) ([]syspkg.Update, error) {
	sysPkgCache.Lock()
	if sysPkgCache.updates != nil && time.Since(sysPkgCache.readAt) < maxAge {
		ups := sysPkgCache.updates
		sysPkgCache.Unlock()
		return ups, nil
	}
	sysPkgCache.Unlock()
	ctx, cancel := context.WithTimeout(ctx, sysPkgListTimeout)
	defer cancel()
	ups, err := s.pkgAdmin.Updates(ctx)
	if err != nil {
		return nil, err
	}
	if ups == nil {
		ups = []syspkg.Update{}
	}
	sortUpdates(ups)
	sysPkgCache.Lock()
	sysPkgCache.updates, sysPkgCache.readAt = ups, time.Now()
	sysPkgCache.Unlock()
	return ups, nil
}

// sortUpdates: security first, then kernels, then by name.
func sortUpdates(ups []syspkg.Update) {
	sort.SliceStable(ups, func(i, j int) bool {
		a, b := ups[i], ups[j]
		if a.Security != b.Security {
			return a.Security
		}
		if a.Kernel != b.Kernel {
			return a.Kernel
		}
		return a.Name < b.Name
	})
}

func sysPkgForget() {
	sysPkgCache.Lock()
	sysPkgCache.updates = nil
	sysPkgCache.Unlock()
}

func (s *Server) sysPkgUpdates(w http.ResponseWriter, r *http.Request) {
	st := s.pkgAdmin.Status()
	out := sysPkgStatus{Supported: st.Supported, Reason: st.Reason, Manager: st.Manager, Updates: []syspkg.Update{}}
	if !st.Supported {
		writeJSON(w, http.StatusOK, out)
		return
	}
	sysPkgCache.Lock()
	jobID, refreshed := sysPkgCache.jobID, sysPkgCache.refreshed
	sysPkgCache.Unlock()
	if jobID != "" {
		if j, ok := s.pkgJobs.Get(jobID); ok {
			j.Output = ""
			out.Job = &j
		}
	}
	if t := pkgListsRefreshedAt(st.Manager); t.After(refreshed) {
		refreshed = t
	}
	if !refreshed.IsZero() {
		out.Checked = &refreshed
	}
	out.RestartNeeded = s.restartNeeded()
	out.Health = s.pkgHealth(r.Context())
	// While a package task runs, the package database is busy: show the last list.
	maxAge := sysPkgListMaxAge
	if s.pkgJobs.Busy() {
		maxAge = 24 * time.Hour
	}
	ups, err := s.sysPkgList(r.Context(), maxAge)
	if err != nil {
		out.Error = err.Error()
	} else {
		out.Updates = ups
	}
	out.Security = syspkg.CountSecurity(out.Updates)
	for _, u := range out.Updates {
		out.Kernel = out.Kernel || u.Kernel
	}
	writeJSON(w, http.StatusOK, out)
}

// sysPkgReq: the optional body of install / repair.
type sysPkgReq struct {
	// AllowRemove: removals the person saw in the preview and confirmed.
	AllowRemove []string `json:"allow_remove,omitempty"`
}

// sysPkgStart runs a check, an install or a repair as a Packages job.
func (s *Server) sysPkgStart(w http.ResponseWriter, r *http.Request, action string, onDone func(syspkg.Job)) {
	st, ok := s.pkgSupported(w)
	if !ok {
		return
	}
	var req sysPkgReq
	if r.ContentLength != 0 && r.Header.Get("Content-Type") != "" && !decodeJSON(w, r, &req) {
		return
	}
	if s.updateIsRunning() {
		writeError(w, http.StatusConflict, "busy", "NoCapOS is updating itself right now. Try again when it's done.")
		return
	}
	steps, err := s.pkgChangeSteps(r.Context(), st.Manager, action, nil, req.AllowRemove)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ar := r.Clone(context.WithoutCancel(r.Context()))
	uid := userFrom(r.Context()).ID
	job, err := s.pkgJobs.Start(action, "system", steps, func(j syspkg.Job) {
		s.pkgAdmin.Invalidate()
		sysPkgForget()
		if onDone != nil {
			onDone(j)
		}
		s.audit(ar, uid, "system.packages."+action, "", j.OK, j.Error)
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
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job})
}

func (s *Server) sysPkgRefresh(w http.ResponseWriter, r *http.Request) {
	s.sysPkgStart(w, r, syspkg.ActRefresh, func(j syspkg.Job) {
		if j.OK {
			sysPkgCache.Lock()
			sysPkgCache.refreshed = time.Now()
			sysPkgCache.Unlock()
		}
	})
}

// sysPkgRepair finishes what dpkg left half-done and fixes unmet
// dependencies (POST /api/v1/system/update/packages/repair).
func (s *Server) sysPkgRepair(w http.ResponseWriter, r *http.Request) {
	s.sysPkgStart(w, r, syspkg.ActRepair, nil)
}

func (s *Server) sysPkgInstall(w http.ResponseWriter, r *http.Request) {
	s.sysPkgStart(w, r, syspkg.ActUpgradeAll, s.sysPkgInstalled(s.kernelPending(r.Context())))
}

// kernelPending: whether a kernel is among the pending (not held) updates,
// which decides the "restart needed" mark after installing them.
func (s *Server) kernelPending(ctx context.Context) bool {
	ups, err := s.sysPkgList(ctx, sysPkgListMaxAge)
	if err != nil {
		return false
	}
	for _, u := range ups {
		if u.Kernel && !u.Held {
			return true
		}
	}
	return false
}

// sysPkgInstalled is what happens after the system updates were installed.
func (s *Server) sysPkgInstalled(kernel bool) func(syspkg.Job) {
	return func(j syspkg.Job) {
		if kernel && (j.OK || strings.Contains(j.Output, "linux-image") || strings.Contains(j.Output, "raspberrypi-kernel")) {
			s.markRestartNeeded()
		}
		if j.OK {
			sysPkgCache.Lock()
			sysPkgCache.refreshed = time.Now()
			sysPkgCache.Unlock()
		}
	}
}

// checkSystemUpdates is the background check (from watchUpdates): about once
// a day it refreshes the package lists and, when the distribution marks
// security updates, says so in a notification.
func (s *Server) checkSystemUpdates(ctx context.Context) {
	if s.pkgAdmin == nil || s.pkgJobs == nil || !s.canSelfUpdate() {
		return
	}
	st := s.pkgAdmin.Status()
	if !st.Supported || s.pkgJobs.Busy() || s.updateIsRunning() {
		return
	}
	sysPkgCache.Lock()
	last, refreshed := sysPkgCache.checked, sysPkgCache.refreshed
	sysPkgCache.Unlock()
	if !last.IsZero() && time.Since(last) < sysPkgCheckEvery {
		return
	}
	if t := pkgListsRefreshedAt(st.Manager); t.After(refreshed) {
		refreshed = t
	}
	// Only refresh where that alone is safe (pacman -Sy without -u isn't).
	if (st.Manager == "apt" || st.Manager == "dnf") && time.Since(refreshed) > sysPkgRefreshEvery {
		if plan, err := syspkg.Plan(st.Manager, syspkg.ActRefresh, ""); err == nil {
			for _, p := range plan {
				if _, err := s.pkgAdmin.Run(ctx, p...); err != nil {
					s.Log.Info("system package lists refresh", "err", err)
				}
			}
			sysPkgCache.Lock()
			sysPkgCache.refreshed = time.Now()
			sysPkgCache.Unlock()
		}
	}
	sysPkgForget()
	ups, err := s.sysPkgList(ctx, 0)
	sysPkgCache.Lock()
	sysPkgCache.checked = time.Now()
	sysPkgCache.Unlock()
	if err != nil {
		return
	}
	if n := securityNotification(ups); n != nil {
		s.add(ctx, *n)
	}
}

// securityNotification: one per set of pending security updates (the key
// changes when the set does), nil when there are none.
func securityNotification(ups []syspkg.Update) *notify.Notification {
	var names []string
	for _, u := range ups {
		if u.Security && !u.Held {
			names = append(names, u.Name+"="+u.Available)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	sum := sha256.Sum256([]byte(strings.Join(names, "\n")))
	n := len(names)
	title := strconv.Itoa(n) + " security updates for this system"
	if n == 1 {
		title = "1 security update for this system"
	}
	return &notify.Notification{
		Key: "sysupdate:security:" + hex.EncodeToString(sum[:8]), Kind: notify.KindUpdate, Level: "warning",
		Title: title,
		Body:  "Security patches for the system packages are available. Install them in Software Update; it takes about a minute.",
		GenZ: &notify.Variant{Title: title + " just landed",
			Body: "Patches for the system packages just landed. Install them in Glow Up — takes a minute, no cap."},
		Icon: "settings", Action: &notify.Action{App: "settings", Props: map[string]any{"section": "update"}},
	}
}
