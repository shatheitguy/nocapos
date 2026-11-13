package api

import (
	"net/http"
	"strconv"
)

// Cloud accounts opened as folders (admin, audited).

func (s *Server) cloudMountRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/cloud/mounts", s.admin(s.cloudMounts))
	mux.Handle("POST /api/v1/cloud/accounts/{id}/mount", s.admin(s.cloudMount))
	mux.Handle("DELETE /api/v1/cloud/accounts/{id}/mount", s.admin(s.cloudUnmount))
}

func (s *Server) cloudMounts(w http.ResponseWriter, r *http.Request) {
	list, err := s.Cloud.Mounts(r.Context())
	if err != nil {
		s.cloudError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) cloudMount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		ReadOnly bool `json:"read_only"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	err := s.Cloud.MountAccount(r.Context(), id, req.ReadOnly)
	s.audit(r, userFrom(r.Context()).ID, "cloud.mount", strconv.FormatInt(id, 10), err == nil, errText(err))
	if err != nil {
		s.cloudError(w, err)
		return
	}
	s.FileIndex.Touch()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) cloudUnmount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := s.Cloud.UnmountAccount(r.Context(), id)
	s.audit(r, userFrom(r.Context()).ID, "cloud.unmount", strconv.FormatInt(id, 10), err == nil, errText(err))
	if err != nil {
		s.cloudError(w, err)
		return
	}
	s.FileIndex.Touch()
	w.WriteHeader(http.StatusNoContent)
}
