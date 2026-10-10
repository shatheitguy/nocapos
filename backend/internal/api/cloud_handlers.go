package api

import (
	"errors"
	"net/http"
	"strconv"

	"alfaos/alfad/internal/cloudimport"
	"alfaos/alfad/internal/store"
)

// Cloud imports: cloud accounts and imports into storage (rclone). Admin only.

func (s *Server) cloudError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, store.ErrInUse):
		writeError(w, http.StatusConflict, "in_use", "an import still uses this account; delete it first")
	default:
		writeError(w, http.StatusBadRequest, "cloud_error", err.Error())
	}
}

func (s *Server) cloudOverview(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.Cloud.Accounts(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	imports, err := s.Cloud.Imports(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": s.Cloud.Status(r.Context()), "accounts": accounts, "imports": imports})
}

func (s *Server) cloudInstall(w http.ResponseWriter, r *http.Request) {
	err := s.Cloud.InstallRclone(r.Context())
	s.audit(r, userFrom(r.Context()).ID, "cloud.install", "rclone", err == nil, errText(err))
	if err != nil {
		s.cloudError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": s.Cloud.Status(r.Context())})
}

func (s *Server) cloudAddAccount(w http.ResponseWriter, r *http.Request) {
	var req cloudimport.NewAccount
	if !decodeJSON(w, r, &req) {
		return
	}
	a, err := s.Cloud.AddAccount(r.Context(), req)
	s.audit(r, userFrom(r.Context()).ID, "cloud.account.add", req.Kind+":"+req.Name, err == nil, errText(err))
	if err != nil {
		s.cloudError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) cloudSignInStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind string `json:"kind"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	session, url, err := s.Cloud.SignInStart(r.Context(), req.Kind)
	if err != nil {
		s.cloudError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"session": session, "url": url})
}

func (s *Server) cloudSignInFinish(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Session string `json:"session"`
		Address string `json:"address"`
		Name    string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	a, err := s.Cloud.SignInFinish(r.Context(), req.Session, req.Address, req.Name)
	s.audit(r, userFrom(r.Context()).ID, "cloud.account.add", req.Name, err == nil, errText(err))
	if err != nil {
		s.cloudError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) cloudDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := s.Cloud.DeleteAccount(r.Context(), id)
	s.audit(r, userFrom(r.Context()).ID, "cloud.account.delete", strconv.FormatInt(id, 10), err == nil, errText(err))
	if err != nil {
		s.cloudError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) cloudFolders(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	entries, err := s.Cloud.Folders(r.Context(), id, r.URL.Query().Get("path"))
	if err != nil {
		s.cloudError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"folders": entries})
}

func (s *Server) cloudSaveImport(w http.ResponseWriter, r *http.Request) {
	var req cloudimport.Import
	if !decodeJSON(w, r, &req) {
		return
	}
	req.ID = 0
	if r.Method == http.MethodPut {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		req.ID = id
	}
	id, err := s.Cloud.SaveImport(r.Context(), req)
	s.audit(r, userFrom(r.Context()).ID, "cloud.import.save", req.Name, err == nil, errText(err))
	if err != nil {
		s.cloudError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) cloudDeleteImport(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := s.Cloud.DeleteImport(r.Context(), id)
	s.audit(r, userFrom(r.Context()).ID, "cloud.import.delete", strconv.FormatInt(id, 10), err == nil, errText(err))
	if err != nil {
		s.cloudError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) cloudRunImport(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	job, err := s.Cloud.Run(r.Context(), id)
	s.audit(r, userFrom(r.Context()).ID, "cloud.import.run", strconv.FormatInt(id, 10), err == nil, errText(err))
	if err != nil {
		s.cloudError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) cloudJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.Cloud.JobByID(r.PathValue("job"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such job")
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) cloudCancel(w http.ResponseWriter, r *http.Request) {
	if !s.Cloud.Cancel(r.PathValue("job")) {
		writeError(w, http.StatusNotFound, "not_found", "no running import with that id")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
