package api

import (
	"fmt"
	"net/http"
	"strings"

	"alfaos/alfad/internal/storage"
)

// Storage extras: the boot drive and its mirror, dry-run plans, speed-boost
// drives, spin-down, snapshot schedules, encrypted folders and the flexible
// array. Admin only and audited, like the rest of Storage. Passphrases are
// never written to the audit log.

func (s *Server) storageExtraRoutes(mux *http.ServeMux, p string) {
	mux.Handle("GET "+p+"/boot", s.admin(s.stBoot))
	mux.Handle("POST "+p+"/boot/mirror/plan", s.admin(s.stBootPlan))
	mux.Handle("POST "+p+"/boot/mirror", s.admin(s.stBootMirror))
	mux.Handle("POST "+p+"/plan", s.admin(s.stPlan))
	mux.Handle("POST "+p+"/pools/{name}/support", s.admin(s.stAddSupport))
	mux.Handle("DELETE "+p+"/pools/{name}/support", s.admin(s.stRemoveSupport))
	mux.Handle("POST "+p+"/pools/{name}/expand", s.admin(s.stExpand))
	mux.Handle("GET "+p+"/pools/{name}/scrubs", s.admin(s.stScrubHistory))
	mux.Handle("GET "+p+"/pools/{name}/encryption", s.admin(s.stEncryption))
	mux.Handle("GET "+p+"/arc", s.admin(s.stARC))
	mux.Handle("GET "+p+"/spindown", s.admin(s.stSpindown))
	mux.Handle("PUT "+p+"/spindown/{name}", s.admin(s.stSetSpindown))
	mux.Handle("GET "+p+"/snapshots/overview", s.admin(s.stSnapOverview))
	mux.Handle("GET "+p+"/snapshots/schedules", s.admin(s.stSnapSchedules))
	mux.Handle("PUT "+p+"/snapshots/schedules", s.admin(s.stSetSnapSchedule))
	mux.Handle("POST "+p+"/snapshots/clone", s.admin(s.stClone))
	mux.Handle("GET "+p+"/snapshots/browse", s.admin(s.stBrowseSnap))
	mux.Handle("POST "+p+"/encrypted", s.admin(s.stCreateEncrypted))
	mux.Handle("POST "+p+"/encrypted/unlock", s.admin(s.stUnlock))
	mux.Handle("POST "+p+"/encrypted/lock", s.admin(s.stLock))
	mux.Handle("PUT "+p+"/encrypted/auto-lock", s.admin(s.stAutoLock))
	mux.Handle("POST "+p+"/flex/plan", s.admin(s.stFlexPlan))
	mux.Handle("POST "+p+"/flex", s.admin(s.stFlexCreate))
}

// stReply writes v, or the storage error.
func (s *Server) stReply(w http.ResponseWriter, v any, err error) {
	if err != nil {
		s.stError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// stDone audits a quick action and answers 204.
func (s *Server) stDone(w http.ResponseWriter, r *http.Request, action, target string, err error) {
	s.stAudit(r, action, target, err)
	if err != nil {
		s.stError(w, err)
		return
	}
	s.FileIndex.Touch()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) stBoot(w http.ResponseWriter, r *http.Request) {
	b, err := s.Storage.Boot(r.Context())
	s.stReply(w, b, err)
}

func (s *Server) stBootPlan(w http.ResponseWriter, r *http.Request) {
	var req storage.BootMirrorRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	p, err := s.Storage.PlanBootMirror(r.Context(), req)
	s.stReply(w, p, err)
}

func (s *Server) stBootMirror(w http.ResponseWriter, r *http.Request) {
	var req storage.BootMirrorRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	j, err := s.Storage.MirrorBoot(r.Context(), req, s.stJobDone(r, "boot.mirror", req.Disk))
	s.stJobResult(w, r, "boot.mirror", req.Disk, j, err)
}

func (s *Server) stPlan(w http.ResponseWriter, r *http.Request) {
	var req storage.PlanRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	p, err := s.Storage.PlanPool(r.Context(), req)
	s.stReply(w, p, err)
}

func (s *Server) stAddSupport(w http.ResponseWriter, r *http.Request) {
	var req storage.SupportRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	j, err := s.Storage.AddSupport(r.Context(), name, req, s.stJobDone(r, "pool.support", name))
	s.stJobResult(w, r, "pool.support", name+" "+req.Kind+" "+strings.Join(req.Disks, ","), j, err)
}

func (s *Server) stRemoveSupport(w http.ResponseWriter, r *http.Request) {
	name, dev := r.PathValue("name"), r.URL.Query().Get("dev")
	s.stDone(w, r, "pool.support.remove", name+" "+dev, s.Storage.RemoveSupport(r.Context(), name, dev))
}

func (s *Server) stExpand(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.stDone(w, r, "pool.expand", name, s.Storage.Expand(r.Context(), name))
}

func (s *Server) stScrubHistory(w http.ResponseWriter, r *http.Request) {
	h, err := s.Storage.ScrubHistory(r.Context(), r.PathValue("name"))
	s.stReply(w, h, err)
}

func (s *Server) stEncryption(w http.ResponseWriter, r *http.Request) {
	list, err := s.Storage.Encryption(r.Context(), r.PathValue("name"))
	s.stReply(w, list, err)
}

func (s *Server) stARC(w http.ResponseWriter, r *http.Request) {
	a, err := s.Storage.ARC(r.Context())
	s.stReply(w, a, err)
}

func (s *Server) stSpindown(w http.ResponseWriter, r *http.Request) {
	list, err := s.Storage.Spindown(r.Context())
	s.stReply(w, map[string]any{"disks": list, "choices": storage.SpindownChoices}, err)
}

func (s *Server) stSetSpindown(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Minutes int `json:"minutes"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	s.stDone(w, r, "disk.spindown", fmt.Sprintf("%s %dmin", name, req.Minutes), s.Storage.SetSpindown(r.Context(), name, req.Minutes))
}

func (s *Server) stSnapOverview(w http.ResponseWriter, r *http.Request) {
	o, err := s.Storage.SnapshotOverview(r.Context())
	s.stReply(w, o, err)
}

func (s *Server) stSnapSchedules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Storage.SnapSchedules(r.Context()))
}

func (s *Server) stSetSnapSchedule(w http.ResponseWriter, r *http.Request) {
	var req storage.SnapSchedule
	if !decodeJSON(w, r, &req) {
		return
	}
	detail := fmt.Sprintf("%s h%d d%d w%d m%d", req.Dataset, req.Hourly, req.Daily, req.Weekly, req.Monthly)
	s.stDone(w, r, "snapshot.schedule", detail, s.Storage.SetSnapSchedule(r.Context(), req))
}

func (s *Server) stClone(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string `json:"name"`
		Target string `json:"target"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	s.stDone(w, r, "snapshot.clone", req.Name+" -> "+req.Target, s.Storage.Clone(r.Context(), req.Name, req.Target))
}

func (s *Server) stBrowseSnap(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, err := s.Storage.BrowseSnapshot(r.Context(), q.Get("name"), q.Get("path"))
	s.stReply(w, list, err)
}

func (s *Server) stCreateEncrypted(w http.ResponseWriter, r *http.Request) {
	var req storage.EncryptedRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	s.stDone(w, r, "encrypted.create", req.Name, s.Storage.CreateEncrypted(r.Context(), req))
}

func (s *Server) stUnlock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string `json:"name"`
		Passphrase string `json:"passphrase"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	s.stDone(w, r, "encrypted.unlock", req.Name, s.Storage.Unlock(r.Context(), req.Name, req.Passphrase))
}

func (s *Server) stLock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	s.stDone(w, r, "encrypted.lock", req.Name, s.Storage.Lock(r.Context(), req.Name))
}

func (s *Server) stAutoLock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Minutes int    `json:"minutes"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	s.stDone(w, r, "encrypted.auto_lock", fmt.Sprintf("%s %dmin", req.Name, req.Minutes), s.Storage.SetAutoLock(r.Context(), req.Name, req.Minutes))
}

func (s *Server) stFlexPlan(w http.ResponseWriter, r *http.Request) {
	var req storage.FlexRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	p, err := s.Storage.PlanFlex(r.Context(), req)
	s.stReply(w, p, err)
}

func (s *Server) stFlexCreate(w http.ResponseWriter, r *http.Request) {
	var req storage.FlexRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	target := strings.Join(req.Data, ",") + " parity " + strings.Join(req.Parity, ",")
	j, err := s.Storage.CreateFlex(r.Context(), req, s.stJobDone(r, "flex.create", target))
	s.stJobResult(w, r, "flex.create", target, j, err)
}
