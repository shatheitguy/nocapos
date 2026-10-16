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
	writeJSON(w, http.StatusOK, map[string]any{"updates": ups})
}

type pkgActionReq struct {
	Action  string `json:"action"`
	Name    string `json:"name,omitempty"`
	Confirm string `json:"confirm,omitempty"` // removing: the package name typed again
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
		syspkg.ActHold, syspkg.ActUnhold, syspkg.ActAutoremove, syspkg.ActClean:
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "unknown action")
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
	plan, err := syspkg.Plan(st.Manager, req.Action, req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.startPkgJob(w, r, req.Action, req.Name, syspkg.StepsFor(plan), nil)
}

// startPkgJob runs steps as a background job, audited when it ends.
func (s *Server) startPkgJob(w http.ResponseWriter, r *http.Request, action, target string, steps []syspkg.Step, cleanup func()) {
	ar := r.Clone(context.WithoutCancel(r.Context()))
	uid := userFrom(r.Context()).ID
	job, err := s.pkgJobs.Start(action, target, steps, func(j syspkg.Job) {
		s.pkgAdmin.Invalidate()
		if cleanup != nil {
			cleanup()
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
