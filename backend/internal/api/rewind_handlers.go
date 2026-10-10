package api

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"alfaos/alfad/internal/backup"
	"alfaos/alfad/internal/store"
)

// Backup & Rewind (admin only; the one-click database download is in backup_handlers.go). Destinations, plans, runs, and browsing and
// restoring old versions; restic does the work.

func (s *Server) backupError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, store.ErrInUse):
		writeError(w, http.StatusConflict, "in_use", "this destination is still used by a backup; delete that backup first")
	default:
		writeError(w, http.StatusBadRequest, "backup_error", err.Error())
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return 0, false
	}
	return id, true
}

func (s *Server) backupOverview(w http.ResponseWriter, r *http.Request) {
	repos, err := s.Backup.Repos(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	plans, err := s.Backup.Plans(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": s.Backup.Status(r.Context()), "repos": repos, "plans": plans})
}

func (s *Server) backupInstall(w http.ResponseWriter, r *http.Request) {
	err := s.Backup.InstallRestic(r.Context())
	s.audit(r, userFrom(r.Context()).ID, "backup.install", "restic", err == nil, errText(err))
	if err != nil {
		s.backupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": s.Backup.Status(r.Context())})
}

func (s *Server) backupAddRepo(w http.ResponseWriter, r *http.Request) {
	var req backup.NewRepo
	if !decodeJSON(w, r, &req) {
		return
	}
	repo, key, err := s.Backup.AddRepo(r.Context(), req)
	s.audit(r, userFrom(r.Context()).ID, "backup.repo.add", req.Kind+":"+req.Name, err == nil, errText(err))
	if err != nil {
		s.backupError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"repo": repo, "recovery_key": key})
}

func (s *Server) backupDeleteRepo(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := s.Backup.DeleteRepo(r.Context(), id)
	s.audit(r, userFrom(r.Context()).ID, "backup.repo.delete", strconv.FormatInt(id, 10), err == nil, errText(err))
	if err != nil {
		s.backupError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// backupRepoKey reveals a destination's recovery key (audited).
func (s *Server) backupRepoKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	key, err := s.Backup.RecoveryKey(r.Context(), id)
	s.audit(r, userFrom(r.Context()).ID, "backup.repo.key", strconv.FormatInt(id, 10), err == nil, "recovery key shown")
	if err != nil {
		s.backupError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"recovery_key": key})
}

func (s *Server) backupSavePlan(w http.ResponseWriter, r *http.Request) {
	var p backup.Plan
	if !decodeJSON(w, r, &p) {
		return
	}
	p.ID = 0
	if r.Method == http.MethodPut {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		p.ID = id
	}
	id, err := s.Backup.SavePlan(r.Context(), p)
	s.audit(r, userFrom(r.Context()).ID, "backup.plan.save", p.Name, err == nil, errText(err))
	if err != nil {
		s.backupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) backupDeletePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := s.Backup.DeletePlan(r.Context(), id)
	s.audit(r, userFrom(r.Context()).ID, "backup.plan.delete", strconv.FormatInt(id, 10), err == nil, errText(err))
	if err != nil {
		s.backupError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) backupRunPlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	job, err := s.Backup.RunPlan(r.Context(), id)
	s.audit(r, userFrom(r.Context()).ID, "backup.run", strconv.FormatInt(id, 10), err == nil, errText(err))
	if err != nil {
		s.backupError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) backupSnapshots(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	snaps, err := s.Backup.Snapshots(r.Context(), id)
	if err != nil {
		s.backupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": snaps})
}

func (s *Server) backupBrowse(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	nodes, err := s.Backup.Browse(r.Context(), id, r.PathValue("snap"), r.URL.Query().Get("path"))
	if err != nil {
		s.backupError(w, err)
		return
	}
	if nodes == nil {
		nodes = []backup.Node{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": nodes})
}

// backupDump downloads a file (or a folder as .tar) from a backup.
func (s *Server) backupDump(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	p := path.Clean("/" + q.Get("path"))
	name := path.Base(p)
	if q.Get("dir") == "1" {
		name += ".tar"
	}
	s.audit(r, userFrom(r.Context()).ID, "backup.download", fmt.Sprintf("%d:%s", id, p), true, r.PathValue("snap"))
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	h.Set("Cache-Control", "no-store")
	// Headers go out with the first byte, so an early failure can still be an error response.
	lw := &lazyWriter{w: w}
	if err := s.Backup.Dump(r.Context(), id, r.PathValue("snap"), p, lw); err != nil && !lw.started {
		h.Del("Content-Disposition")
		s.backupError(w, err)
	}
}

type lazyWriter struct {
	w       http.ResponseWriter
	started bool
}

func (l *lazyWriter) Write(p []byte) (int, error) {
	l.started = true
	return l.w.Write(p)
}

func (s *Server) backupRestore(w http.ResponseWriter, r *http.Request) {
	var req backup.RestoreRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	job, err := s.Backup.Restore(r.Context(), req)
	s.audit(r, userFrom(r.Context()).ID, "backup.restore", fmt.Sprintf("%d:%s", req.PlanID, strings.Join(req.Paths, ", ")), err == nil, req.Mode+" "+errText(err))
	if err != nil {
		s.backupError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) backupJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.Backup.JobByID(r.PathValue("job"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such job")
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) backupCancel(w http.ResponseWriter, r *http.Request) {
	if !s.Backup.Cancel(r.PathValue("job")) {
		writeError(w, http.StatusNotFound, "not_found", "no running job with that id")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
