package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"alfaos/alfad/internal/syspkg"
	"alfaos/alfad/internal/webapps"
)

// Packages: the host's own system packages: list, updates, install, remove,
// hold, and local .deb/.rpm files. Admin only; every change is audited and
// runs as a background job whose output the UI follows.

func (s *Server) packageRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/packages/status", s.admin(s.pkgStatus))
	mux.Handle("GET /api/v1/packages", s.admin(s.pkgList))
	mux.Handle("GET /api/v1/packages/updates", s.admin(s.pkgUpdates))
	mux.Handle("POST /api/v1/packages/actions", s.admin(s.pkgAction))
	mux.Handle("POST /api/v1/packages/preview", s.admin(s.pkgPreview))
	mux.Handle("POST /api/v1/packages/upload", s.admin(s.pkgUpload))
	mux.Handle("POST /api/v1/packages/features/{id}/install", s.admin(s.pkgFeatureInstall))
	mux.Handle("GET /api/v1/packages/jobs/{id}", s.admin(s.pkgJob))
}

// browserFeature is Brave: installed from its own repository by the same
// script Brave's first launch uses, not from the feature table.
var browserFeature = syspkg.Feature{ID: "browser", Title: "Brave browser and its stream tools"}

func (s *Server) pkgStatus(w http.ResponseWriter, r *http.Request) {
	st := s.pkgAdmin.Status()
	features := syspkg.FeatureStates()
	features = append(features, syspkg.FeatureState{Feature: browserFeature, Installed: webapps.NativeInstalled(),
		CanInstall: st.Supported && (st.Manager == "apt" || st.Manager == "dnf")})
	writeJSON(w, http.StatusOK, map[string]any{"status": st, "features": features, "jobs": s.pkgJobs.Recent()})
}

func (s *Server) pkgSupported(w http.ResponseWriter) (syspkg.Status, bool) {
	st := s.pkgAdmin.Status()
	if !st.Supported {
		writeError(w, http.StatusNotImplemented, "unsupported", st.Reason)
	}
	return st, st.Supported
}

// pkgList pages through installed packages: ?q=&offset=&limit=&held=1.
func (s *Server) pkgList(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.pkgSupported(w); !ok {
		return
	}
	all, err := s.pkgAdmin.InstalledCached(r.Context(), 30*time.Second)
	if err != nil {
		writeError(w, http.StatusBadGateway, "pkg_error", err.Error())
		return
	}
	q := r.URL.Query()
	needle := strings.ToLower(strings.TrimSpace(q.Get("q")))
	heldOnly := q.Get("held") == "1"
	var (
		match     []syspkg.Package
		totalSize int64
	)
	for _, p := range all {
		totalSize += p.Size
		if heldOnly && !p.Held {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(p.Name), needle) && !strings.Contains(strings.ToLower(p.Description), needle) {
			continue
		}
		match = append(match, p)
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	offset = max(0, min(offset, len(match)))
	end := min(len(match), offset+limit)
	page := match[offset:end]
	if page == nil {
		page = []syspkg.Package{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"packages": page, "total": len(match), "installed": len(all), "size": totalSize})
}

func (s *Server) pkgUpdates(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.pkgSupported(w); !ok {
		return
	}
	ups, err := s.pkgAdmin.Updates(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "pkg_error", err.Error())
		return
	}
	if ups == nil {
		ups = []syspkg.Update{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"updates": ups, "health": s.pkgHealth(r.Context())})
}

// pkgHealth: whether apt/dpkg is in a broken state (not re-checked while a
// package task holds the lock; the last result is kept then).
func (s *Server) pkgHealth(ctx context.Context) syspkg.Health {
	maxAge := 20 * time.Second
	if s.pkgJobs.Busy() {
		maxAge = 24 * time.Hour
	}
	return s.pkgAdmin.HealthCached(ctx, maxAge)
}

type pkgPreviewReq struct {
	Action string   `json:"action"` // repair | upgrade-all | upgrade
	Names  []string `json:"names,omitempty"`
}

// pkgPreview simulates a repair or an update, so the UI can show what it
// would remove before anything changes. supported=false: this package
// manager can't simulate; the UI goes ahead without a preview.
func (s *Server) pkgPreview(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.pkgSupported(w); !ok {
		return
	}
	var req pkgPreviewReq
	if !decodeJSON(w, r, &req) {
		return
	}
	switch req.Action {
	case syspkg.ActRepair, syspkg.ActUpgradeAll, syspkg.ActUpgrade:
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "unknown action")
		return
	}
	if s.pkgJobs.Busy() {
		writeError(w, http.StatusConflict, "busy", syspkg.ErrBusy.Error())
		return
	}
	p, err := s.pkgAdmin.Preview(r.Context(), req.Action, req.Names)
	if errors.Is(err, syspkg.ErrUnsupported) {
		writeJSON(w, http.StatusOK, map[string]any{"supported": false})
		return
	}
	out := map[string]any{"supported": true, "preview": p}
	if err != nil {
		out["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

// pkgChangeSteps are the job steps of an action. On apt, repairing and
// updating go through syspkg.AptSteps: dpkg's unfinished work first, then a
// simulation that stops the job before removing anything not in allow.
func (s *Server) pkgChangeSteps(ctx context.Context, mgr, action string, names, allow []string) ([]syspkg.Step, error) {
	if mgr == "apt" && (action == syspkg.ActRepair || action == syspkg.ActUpgradeAll || action == syspkg.ActUpgrade) {
		o := syspkg.Options{Names: names, AllowRemove: allow}
		if action == syspkg.ActUpgrade {
			o.KeepAuto = s.pkgAdmin.AutoInstalled(ctx, names)
		}
		return syspkg.AptSteps(action, o, s.pkgAdmin.Run)
	}
	arg := ""
	if len(names) > 0 {
		if len(names) > 1 {
			return nil, errors.New("one package at a time with this package manager")
		}
		arg = names[0]
	}
	plan, err := syspkg.Plan(mgr, action, arg)
	if err != nil {
		return nil, err
	}
	return syspkg.StepsFor(plan), nil
}

type pkgActionReq struct {
	Action  string `json:"action"`
	Name    string `json:"name,omitempty"`
	Confirm string `json:"confirm,omitempty"` // removing: the package name typed again
	// Names: upgrade several packages together (a kernel with its headers
	// and meta-packages), instead of Name.
	Names []string `json:"names,omitempty"`
	// AllowRemove: removals the person saw in the preview and confirmed.
	AllowRemove []string `json:"allow_remove,omitempty"`
}

func (s *Server) pkgAction(w http.ResponseWriter, r *http.Request) {
	st, ok := s.pkgSupported(w)
	if !ok {
		return
	}
	var req pkgActionReq
	if !decodeJSON(w, r, &req) {
		return
	}
	switch req.Action {
	case syspkg.ActRefresh, syspkg.ActUpgradeAll, syspkg.ActUpgrade, syspkg.ActInstall, syspkg.ActRemove,
		syspkg.ActHold, syspkg.ActUnhold, syspkg.ActAutoremove, syspkg.ActClean, syspkg.ActRepair:
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "unknown action")
		return
	}
	names := req.Names
	if len(names) == 0 && req.Name != "" {
		names = []string{req.Name}
	}
	if len(names) > 1 && req.Action != syspkg.ActUpgrade {
		writeError(w, http.StatusBadRequest, "bad_request", "only updates take several packages")
		return
	}
	if (req.Action == syspkg.ActRemove || req.Action == syspkg.ActAutoremove) && s.pkgHealth(r.Context()).Broken {
		writeError(w, http.StatusConflict, "needs_repair", "The system's packages need a repair first (Repair packages), then try again.")
		return
	}
	if req.Action == syspkg.ActRemove {
		if req.Confirm != req.Name {
			writeError(w, http.StatusBadRequest, "confirm", "type the package name to confirm removing it")
			return
		}
		if err := s.pkgAdmin.CheckRemove(r.Context(), req.Name); err != nil {
			s.audit(r, userFrom(r.Context()).ID, "package.remove", req.Name, false, err.Error())
			writeError(w, http.StatusForbidden, "protected", err.Error())
			return
		}
	}
	steps, err := s.pkgChangeSteps(r.Context(), st.Manager, req.Action, names, req.AllowRemove)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	target := strings.Join(names, " ")
	kernel := false
	for _, n := range names {
		kernel = kernel || syspkg.IsKernelPackage(n) || strings.HasPrefix(n, "linux-image-")
	}
	var after func(syspkg.Job)
	if req.Action == syspkg.ActUpgrade && kernel {
		after = func(j syspkg.Job) {
			if j.OK {
				s.markRestartNeeded()
			}
		}
	}
	s.startPkgJobThen(w, r, req.Action, target, steps, nil, after)
}

// startPkgJob runs steps as a background job, audited when it ends.
func (s *Server) startPkgJob(w http.ResponseWriter, r *http.Request, action, target string, steps []syspkg.Step, cleanup func()) {
	s.startPkgJobThen(w, r, action, target, steps, cleanup, nil)
}

// startPkgJobThen is startPkgJob with after run when the job ends.
func (s *Server) startPkgJobThen(w http.ResponseWriter, r *http.Request, action, target string, steps []syspkg.Step, cleanup func(), after func(syspkg.Job)) {
	ar := r.Clone(context.WithoutCancel(r.Context()))
	uid := userFrom(r.Context()).ID
	job, err := s.pkgJobs.Start(action, target, steps, func(j syspkg.Job) {
		s.pkgAdmin.Invalidate()
		sysPkgForget()
		if cleanup != nil {
			cleanup()
		}
		if after != nil {
			after(j)
		}
		s.audit(ar, uid, "package."+action, target, j.OK, j.Error)
	})
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		if errors.Is(err, syspkg.ErrBusy) {
			writeError(w, http.StatusConflict, "busy", err.Error())
			return
		}
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job})
}

func (s *Server) pkgJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.pkgJobs.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "That task is no longer available.")
		return
	}
	writeJSON(w, http.StatusOK, j)
}

// pkgFeatureInstall installs (or repairs) a NoCapOS feature's host packages.
func (s *Server) pkgFeatureInstall(w http.ResponseWriter, r *http.Request) {
	st, ok := s.pkgSupported(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if id == browserFeature.ID {
		step := syspkg.Step{Label: "install Brave and its stream tools", Fn: webapps.InstallNative}
		s.startPkgJob(w, r, "feature", id, []syspkg.Step{step}, nil)
		return
	}
	f, found := syspkg.FeatureByID(id)
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "no such feature")
		return
	}
	steps := syspkg.FeatureSteps(f, st.Manager)
	if len(steps) == 0 {
		writeError(w, http.StatusBadRequest, "unsupported", "NoCapOS has no packages for this on "+st.Manager+" systems")
		return
	}
	s.startPkgJob(w, r, "feature", id, steps, nil)
}

const maxPackageUpload = 1 << 30

var pkgFileRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+~-]{0,200}\.(deb|rpm)$`)

// pkgUpload takes one .deb/.rpm file, checks it with the package tool, and
// installs it with the package manager (which also fetches dependencies).
func (s *Server) pkgUpload(w http.ResponseWriter, r *http.Request) {
	st, ok := s.pkgSupported(w)
	if !ok {
		return
	}
	if st.FileTypes == "" {
		writeError(w, http.StatusBadRequest, "unsupported", "installing package files isn't supported with "+st.Manager)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPackageUpload)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "expected a file upload")
		return
	}
	part, err := mr.NextPart()
	if err != nil || part.FormName() != "file" {
		writeError(w, http.StatusBadRequest, "bad_request", "expected a file upload")
		return
	}
	name := filepath.Base(part.FileName())
	if !pkgFileRe.MatchString(name) || !strings.HasSuffix(name, st.FileTypes) {
		writeError(w, http.StatusBadRequest, "bad_request", "choose a "+st.FileTypes+" package file")
		return
	}
	dir, err := filepath.Abs(filepath.Join(s.Config.DataDir, "package-uploads"))
	if err == nil {
		err = os.MkdirAll(dir, 0o700)
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	tmp, err := os.MkdirTemp(dir, "up-")
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	path := filepath.Join(tmp, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		_, err = io.Copy(f, part)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		cleanup()
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "package files can be up to 1 GB")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", "the upload didn't finish")
		return
	}
	info, err := s.pkgAdmin.InspectFile(r.Context(), path)
	if err != nil {
		cleanup()
		s.audit(r, userFrom(r.Context()).ID, "package.install-file", name, false, err.Error())
		writeError(w, http.StatusBadRequest, "invalid_package", err.Error())
		return
	}
	plan, err := syspkg.Plan(st.Manager, syspkg.ActInstallFile, path)
	if err != nil {
		cleanup()
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	steps := append([]syspkg.Step{{Label: "check " + name, Fn: func(_ context.Context, out io.Writer) error {
		_, err := io.WriteString(out, info+"\n")
		return err
	}}}, syspkg.StepsFor(plan)...)
	s.startPkgJob(w, r, syspkg.ActInstallFile, name, steps, cleanup)
}
