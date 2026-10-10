package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"alfaos/alfad/internal/storage"
	"alfaos/alfad/internal/store"
)

// Storage manager: disks, SMART, ZFS pools, datasets and snapshots. Admin
// only; every change is audited. The safety rules live in internal/storage.

func (s *Server) storageRoutes(mux *http.ServeMux) {
	const p = "/api/v1/storage"
	mux.Handle("GET "+p+"/status", s.admin(s.stStatus))
	mux.Handle("POST "+p+"/tools/{tool}/install", s.admin(s.stInstall))
	mux.Handle("GET "+p+"/disks", s.admin(s.stDisks))
	mux.Handle("GET "+p+"/disks/{name}/smart", s.admin(s.stSmart))
	mux.Handle("POST "+p+"/disks/{name}/smart-test", s.admin(s.stSmartTest))
	mux.Handle("POST "+p+"/disks/{name}/format", s.admin(s.stFormat))
	mux.Handle("POST "+p+"/disks/{name}/files", s.admin(s.stDiskFiles))
	mux.Handle("GET "+p+"/pools", s.admin(s.stPools))
	mux.Handle("POST "+p+"/pools", s.admin(s.stCreatePool))
	mux.Handle("POST "+p+"/pools/import", s.admin(s.stImport))
	mux.Handle("POST "+p+"/pools/{name}/export", s.admin(s.stExport))
	mux.Handle("DELETE "+p+"/pools/{name}", s.admin(s.stDestroy))
	mux.Handle("POST "+p+"/pools/{name}/scrub", s.admin(s.stScrub))
	mux.Handle("POST "+p+"/pools/{name}/add", s.admin(s.stAddVdev))
	mux.Handle("POST "+p+"/pools/{name}/replace", s.admin(s.stReplace))
	mux.Handle("POST "+p+"/pools/{name}/files", s.admin(s.stPoolFiles))
	mux.Handle("GET "+p+"/pools/{name}/datasets", s.admin(s.stDatasets))
	mux.Handle("POST "+p+"/datasets", s.admin(s.stCreateDataset))
	mux.Handle("PUT "+p+"/datasets", s.admin(s.stUpdateDataset))
	mux.Handle("DELETE "+p+"/datasets", s.admin(s.stDeleteDataset))
	mux.Handle("GET "+p+"/snapshots", s.admin(s.stSnapshots))
	mux.Handle("POST "+p+"/snapshots", s.admin(s.stCreateSnapshot))
	mux.Handle("POST "+p+"/snapshots/rollback", s.admin(s.stRollback))
	mux.Handle("DELETE "+p+"/snapshots", s.admin(s.stDeleteSnapshot))
	mux.Handle("GET "+p+"/settings", s.admin(s.stSettings))
	mux.Handle("PUT "+p+"/settings", s.admin(s.stSetSettings))
	mux.Handle("GET "+p+"/jobs", s.admin(s.stJobs))
	mux.Handle("GET "+p+"/jobs/{id}", s.admin(s.stJob))
	mux.Handle("GET "+p+"/alerts", s.admin(s.stAlerts))
}

func (s *Server) stError(w http.ResponseWriter, err error) {
	var se *storage.Error
	if errors.As(err, &se) {
		code := "storage_error"
		switch se.Status {
		case http.StatusNotFound:
			code = "not_found"
		case http.StatusConflict:
			code = "busy"
		case http.StatusServiceUnavailable:
			code = "unsupported"
		}
		writeError(w, se.Status, code, se.Msg)
		return
	}
	writeError(w, http.StatusBadRequest, "storage_error", err.Error())
}

// stAudit records an action; for background jobs, done records the outcome.
func (s *Server) stAudit(r *http.Request, action, target string, err error) {
	s.audit(r, userFrom(r.Context()).ID, "storage."+action, target, err == nil, errText(err))
}

func (s *Server) stJobDone(r *http.Request, action, target string) func(error) {
	uid, ip := userFrom(r.Context()).ID, clientIP(r, s.Config.TrustedProxies)
	return func(err error) {
		if aerr := s.Store.Audit(context.Background(), store.AuditEntry{UserID: uid, Action: "storage." + action + ".finished",
			Target: target, IP: ip, Success: err == nil, Detail: errText(err)}); aerr != nil {
			s.Log.Error("audit write failed", "action", action, "err", aerr)
		}
		s.FileIndex.Touch()
	}
}

func (s *Server) stJobResult(w http.ResponseWriter, r *http.Request, action, target string, j *storage.Job, err error) {
	s.stAudit(r, action, target, err)
	if err != nil {
		s.stError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job": j})
}

func (s *Server) stStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Storage.Status(r.Context()))
}

func (s *Server) stInstall(w http.ResponseWriter, r *http.Request) {
	tool := r.PathValue("tool")
	err := s.Storage.Install(r.Context(), tool)
	s.stAudit(r, "install", tool, err)
	if err != nil {
		s.stError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.Storage.Status(r.Context()))
}

func (s *Server) stDisks(w http.ResponseWriter, r *http.Request) {
	disks, err := s.Storage.Disks(r.Context())
	if err != nil {
		s.stError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"disks": disks})
}

func (s *Server) stSmart(w http.ResponseWriter, r *http.Request) {
	rep, err := s.Storage.SmartDetail(r.Context(), r.PathValue("name"))
	if err != nil {
		s.stError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) stSmartTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type string `json:"type"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	j, err := s.Storage.SmartTest(r.Context(), name, req.Type, s.stJobDone(r, "smart-test", name))
	s.stJobResult(w, r, "smart-test", name+" "+req.Type, j, err)
}

func (s *Server) stFormat(w http.ResponseWriter, r *http.Request) {
	var req storage.FormatRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	j, err := s.Storage.Format(r.Context(), name, req, s.stJobDone(r, "format", name))
	s.stJobResult(w, r, "format", name+" as "+req.Label, j, err)
}

func (s *Server) stDiskFiles(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	err := s.Storage.SetDiskFiles(r.Context(), name, req.Enabled)
	s.stAudit(r, "disk.files", name, err)
	if err != nil {
		s.stError(w, err)
		return
	}
	s.FileIndex.Touch()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) stPools(w http.ResponseWriter, r *http.Request) {
	res, err := s.Storage.Pools(r.Context())
	if err != nil {
		s.stError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) stCreatePool(w http.ResponseWriter, r *http.Request) {
	var req storage.CreatePoolRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	j, err := s.Storage.CreatePool(r.Context(), req, s.stJobDone(r, "pool.create", req.Name))
	s.stJobResult(w, r, "pool.create", req.Name+" "+req.Layout+" "+strings.Join(req.Disks, ","), j, err)
}

func (s *Server) stImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	err := s.Storage.Import(r.Context(), req.Name)
	s.stAudit(r, "pool.import", req.Name, err)
	if err != nil {
		s.stError(w, err)
		return
	}
	s.FileIndex.Touch()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) stExport(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	err := s.Storage.Export(r.Context(), name)
	s.stAudit(r, "pool.export", name, err)
	if err != nil {
		s.stError(w, err)
		return
	}
	s.FileIndex.Touch()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) stDestroy(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	err := s.Storage.Destroy(r.Context(), name, r.URL.Query().Get("confirm"))
	s.stAudit(r, "pool.destroy", name, err)
	if err != nil {
		s.stError(w, err)
		return
	}
	s.FileIndex.Touch()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) stScrub(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	err := s.Storage.Scrub(r.Context(), name, req.Action)
	s.stAudit(r, "pool.scrub", name+" "+req.Action, err)
	if err != nil {
		s.stError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) stAddVdev(w http.ResponseWriter, r *http.Request) {
	var req storage.AddVdevRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	j, err := s.Storage.AddVdev(r.Context(), name, req, s.stJobDone(r, "pool.add", name))
	s.stJobResult(w, r, "pool.add", name+" "+req.Layout+" "+strings.Join(req.Disks, ","), j, err)
}

func (s *Server) stReplace(w http.ResponseWriter, r *http.Request) {
	var req storage.ReplaceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	j, err := s.Storage.Replace(r.Context(), name, req, s.stJobDone(r, "pool.replace", name))
	s.stJobResult(w, r, "pool.replace", name+" "+req.Old+" -> "+req.Disk, j, err)
}

func (s *Server) stPoolFiles(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	err := s.Storage.SetPoolFiles(r.Context(), name, req.Enabled)
	s.stAudit(r, "pool.files", name, err)
	if err != nil {
		s.stError(w, err)
		return
	}
	s.FileIndex.Touch()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) stDatasets(w http.ResponseWriter, r *http.Request) {
	ds, err := s.Storage.Datasets(r.Context(), r.PathValue("name"))
	if err != nil {
		s.stError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ds)
}

func (s *Server) stCreateDataset(w http.ResponseWriter, r *http.Request) {
	var req storage.DatasetRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	err := s.Storage.CreateDataset(r.Context(), req)
	s.stAudit(r, "dataset.create", req.Name, err)
	if err != nil {
		s.stError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) stUpdateDataset(w http.ResponseWriter, r *http.Request) {
	var req storage.DatasetRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	err := s.Storage.UpdateDataset(r.Context(), req)
	s.stAudit(r, "dataset.update", req.Name, err)
	if err != nil {
		s.stError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) stDeleteDataset(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	err := s.Storage.DeleteDataset(r.Context(), q.Get("name"), q.Get("confirm"))
	s.stAudit(r, "dataset.delete", q.Get("name"), err)
	if err != nil {
		s.stError(w, err)
		return
	}
	s.FileIndex.Touch()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) stSnapshots(w http.ResponseWriter, r *http.Request) {
	snaps, err := s.Storage.Snapshots(r.Context(), r.URL.Query().Get("dataset"))
	if err != nil {
		s.stError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snaps)
}

func (s *Server) stCreateSnapshot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dataset string `json:"dataset"`
		Name    string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	err := s.Storage.CreateSnapshot(r.Context(), req.Dataset, req.Name)
	s.stAudit(r, "snapshot.create", req.Dataset+"@"+req.Name, err)
	if err != nil {
		s.stError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) stRollback(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Confirm string `json:"confirm"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	err := s.Storage.Rollback(r.Context(), req.Name, req.Confirm)
	s.stAudit(r, "snapshot.rollback", req.Name, err)
	if err != nil {
		s.stError(w, err)
		return
	}
	s.FileIndex.Touch()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) stDeleteSnapshot(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	err := s.Storage.DeleteSnapshot(r.Context(), name)
	s.stAudit(r, "snapshot.delete", name, err)
	if err != nil {
		s.stError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) stSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.Storage.Settings(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) stSetSettings(w http.ResponseWriter, r *http.Request) {
	var req storage.Settings
	if !decodeJSON(w, r, &req) {
		return
	}
	err := s.Storage.SetSettings(r.Context(), req)
	detail := "auto_scrub=off"
	if req.AutoScrub {
		detail = "auto_scrub=on"
	}
	s.stAudit(r, "settings", detail, err)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) stJobs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"jobs": s.Storage.Jobs()})
}

func (s *Server) stJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.Storage.Job(r.PathValue("id"))
	if err != nil {
		s.stError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) stAlerts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"alerts": s.Storage.Alerts()})
}

// storageAlertsSource feeds the storage.alerts topic: the list now, then on change.
func (s *Server) storageAlertsSource(ctx context.Context, emit func(any)) error {
	ch, cancel := s.Storage.SubscribeAlerts()
	defer cancel()
	emit(s.Storage.Alerts())
	for {
		select {
		case <-ctx.Done():
			return nil
		case list := <-ch:
			emit(list)
		}
	}
}

// storageInUse lists host folders that containers bind-mount (app data).
func (s *Server) storageInUse(ctx context.Context) []storage.PathUse {
	cs, err := s.Docker.ListContainers(ctx, true)
	if err != nil {
		return nil
	}
	var out []storage.PathUse
	for _, c := range cs {
		d, err := s.Docker.InspectContainer(ctx, c.ID)
		if err != nil {
			continue
		}
		for _, m := range d.Mounts {
			if m.Type == "bind" && strings.HasPrefix(m.Source, "/") {
				out = append(out, storage.PathUse{Path: m.Source, App: c.Name})
			}
		}
	}
	return out
}
