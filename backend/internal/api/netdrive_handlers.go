package api

import (
	"errors"
	"net/http"
	"strconv"

	"alfaos/alfad/internal/netdrive"
	"alfaos/alfad/internal/store"
)

// Network drives (SMB/NFS shares mounted as storage locations) and file
// sharing (SMB via Samba, WebDAV at /dav/). Admin only; changes are audited.

func (s *Server) netError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	default:
		writeError(w, http.StatusBadRequest, "network_drive_error", err.Error())
	}
}

func (s *Server) netOverview(w http.ResponseWriter, r *http.Request) {
	drives, err := s.NetDrives.Drives(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	sharing, err := s.NetDrives.Sharing(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"support": s.NetDrives.Support(), "drives": drives, "sharing": sharing})
}

func (s *Server) netInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tool string `json:"tool"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	err := s.NetDrives.Install(r.Context(), req.Tool)
	s.audit(r, userFrom(r.Context()).ID, "netdrive.install", req.Tool, err == nil, errText(err))
	if err != nil {
		s.netError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"support": s.NetDrives.Support()})
}

func (s *Server) netAdd(w http.ResponseWriter, r *http.Request) {
	var req netdrive.NewDrive
	if !decodeJSON(w, r, &req) {
		return
	}
	d, err := s.NetDrives.Add(r.Context(), req)
	s.audit(r, userFrom(r.Context()).ID, "netdrive.add", req.Kind+"://"+req.Host+"/"+req.Share, err == nil, errText(err))
	if err != nil {
		s.netError(w, err)
		return
	}
	s.FileIndex.Touch()
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) netConnect(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	connect := r.PathValue("action") == "connect"
	var d netdrive.Drive
	var err error
	if connect {
		d, err = s.NetDrives.Connect(r.Context(), id)
	} else {
		d, err = s.NetDrives.Disconnect(r.Context(), id)
	}
	s.audit(r, userFrom(r.Context()).ID, "netdrive."+r.PathValue("action"), strconv.FormatInt(id, 10), err == nil, errText(err))
	if err != nil {
		s.netError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) netUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Auto bool `json:"auto"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.NetDrives.SetAuto(r.Context(), id, req.Auto); err != nil {
		s.netError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) netDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := s.NetDrives.Delete(r.Context(), id)
	s.audit(r, userFrom(r.Context()).ID, "netdrive.delete", strconv.FormatInt(id, 10), err == nil, errText(err))
	if err != nil {
		s.netError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) netExports(w http.ResponseWriter, r *http.Request) {
	exports, err := s.NetDrives.Exports(r.Context(), r.URL.Query().Get("host"))
	if err != nil {
		s.netError(w, err)
		return
	}
	if exports == nil {
		exports = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"exports": exports})
}

func (s *Server) sharePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	err := s.NetDrives.SetPassword(r.Context(), req.Password)
	s.audit(r, userFrom(r.Context()).ID, "sharing.password", netdrive.ShareUser, err == nil, errText(err))
	if err != nil {
		s.netError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) shareProtocols(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SMB    *bool `json:"smb"`
		WebDAV *bool `json:"webdav"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	err := s.NetDrives.SetProtocols(r.Context(), req.SMB, req.WebDAV)
	detail := ""
	if req.SMB != nil {
		detail += "smb=" + strconv.FormatBool(*req.SMB) + " "
	}
	if req.WebDAV != nil {
		detail += "webdav=" + strconv.FormatBool(*req.WebDAV)
	}
	s.audit(r, userFrom(r.Context()).ID, "sharing.protocols", detail, err == nil, errText(err))
	if err != nil {
		s.netError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) shareAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Root     string `json:"root"`
		Path     string `json:"path"`
		ReadOnly bool   `json:"read_only"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	sh, err := s.NetDrives.AddShare(r.Context(), req.Name, req.Root, req.Path, req.ReadOnly)
	s.audit(r, userFrom(r.Context()).ID, "sharing.add", req.Name+" = "+req.Root+":"+req.Path, err == nil, errText(err))
	if err != nil {
		s.netError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sh)
}

func (s *Server) shareUpdate(w http.ResponseWriter, r *http.Request) {
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
	err := s.NetDrives.SetShareReadOnly(r.Context(), id, req.ReadOnly)
	s.audit(r, userFrom(r.Context()).ID, "sharing.update", strconv.FormatInt(id, 10), err == nil, "read_only="+strconv.FormatBool(req.ReadOnly))
	if err != nil {
		s.netError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) shareDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := s.NetDrives.DeleteShare(r.Context(), id)
	s.audit(r, userFrom(r.Context()).ID, "sharing.delete", strconv.FormatInt(id, 10), err == nil, errText(err))
	if err != nil {
		s.netError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
