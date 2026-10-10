package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"alfaos/alfad/internal/notify"
	"alfaos/alfad/internal/rdp"
	"alfaos/alfad/internal/vm"
)

// Virtual Desk: virtual machines on QEMU/KVM through libvirt. Admin only;
// every change is audited. The rules live in internal/vm. A machine's screen
// is VNC on 127.0.0.1, shown through the same guacd tunnel as Remote Desktop.

func (s *Server) vmRoutes(mux *http.ServeMux) {
	const p = "/api/v1/vms"
	mux.Handle("GET "+p+"/status", s.admin(s.vmStatus))
	mux.Handle("POST "+p+"/install", s.admin(s.vmInstall))
	mux.Handle("GET "+p, s.admin(s.vmList))
	mux.Handle("POST "+p, s.admin(s.vmCreate))
	mux.Handle("GET "+p+"/stats", s.admin(s.vmStats))
	mux.Handle("GET "+p+"/{name}", s.admin(s.vmGet))
	mux.Handle("PATCH "+p+"/{name}", s.admin(s.vmUpdate))
	mux.Handle("DELETE "+p+"/{name}", s.admin(s.vmDelete))
	mux.Handle("POST "+p+"/{name}/power", s.admin(s.vmPower))
	mux.Handle("POST "+p+"/{name}/clone", s.admin(s.vmClone))
	mux.Handle("POST "+p+"/{name}/rename", s.admin(s.vmRename))
	mux.Handle("GET "+p+"/{name}/snapshots", s.admin(s.vmSnapshots))
	mux.Handle("POST "+p+"/{name}/snapshots", s.admin(s.vmCreateSnapshot))
	mux.Handle("POST "+p+"/{name}/snapshots/{snap}/revert", s.admin(s.vmRevertSnapshot))
	mux.Handle("DELETE "+p+"/{name}/snapshots/{snap}", s.admin(s.vmDeleteSnapshot))
	mux.Handle("POST "+p+"/{name}/console", s.admin(s.vmConsole))
}

func (s *Server) vmError(w http.ResponseWriter, err error) {
	var ve *vm.Error
	if errors.As(err, &ve) {
		code := "vm_error"
		switch ve.Status {
		case http.StatusNotFound:
			code = "not_found"
		case http.StatusConflict:
			code = "busy"
		case http.StatusServiceUnavailable:
			code = "unsupported"
		}
		writeError(w, ve.Status, code, ve.Msg)
		return
	}
	writeError(w, http.StatusBadRequest, "vm_error", err.Error())
}

func (s *Server) vmAudit(r *http.Request, action, target string, err error) {
	s.audit(r, userFrom(r.Context()).ID, "vm."+action, target, err == nil, errText(err))
}

func (s *Server) vmStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.VMs.Status(r.Context()))
}

func (s *Server) vmInstall(w http.ResponseWriter, r *http.Request) {
	err := s.VMs.Install(r.Context())
	s.vmAudit(r, "install", "", err)
	if err != nil {
		s.vmError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.VMs.Status(r.Context()))
}

func (s *Server) vmList(w http.ResponseWriter, r *http.Request) {
	vms, err := s.VMs.List(r.Context())
	if err != nil {
		s.vmError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"vms": vms})
}

func (s *Server) vmGet(w http.ResponseWriter, r *http.Request) {
	v, err := s.VMs.Get(r.Context(), r.PathValue("name"))
	if err != nil {
		s.vmError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) vmStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.VMs.Stats(r.Context())
	if err != nil {
		s.vmError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stats": st})
}

func (s *Server) vmCreate(w http.ResponseWriter, r *http.Request) {
	var req vm.CreateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	v, err := s.VMs.Create(r.Context(), req)
	detail := req.OS + " " + strconv.Itoa(req.CPUs) + " CPU " + strconv.Itoa(req.MemoryMiB) + " MiB"
	if v != nil && err != nil {
		// Created, but autostart or the first start failed.
		s.vmAudit(r, "create", req.Name+" "+detail, nil)
		writeJSON(w, http.StatusCreated, map[string]any{"vm": v, "warning": err.Error()})
		return
	}
	s.vmAudit(r, "create", req.Name+" "+detail, err)
	if err != nil {
		s.vmError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"vm": v})
}

func (s *Server) vmUpdate(w http.ResponseWriter, r *http.Request) {
	var req vm.UpdateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	err := s.VMs.Update(r.Context(), name, req)
	s.vmAudit(r, "update", name, err)
	if err != nil {
		s.vmError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) vmDelete(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name := r.PathValue("name")
	disks := q.Get("disks") == "1"
	err := s.VMs.Delete(r.Context(), name, q.Get("confirm"), disks)
	target := name
	if disks {
		target += " (with disks)"
	}
	s.vmAudit(r, "delete", target, err)
	if err != nil {
		s.vmError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) vmPower(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	err := s.VMs.Action(r.Context(), name, req.Action)
	s.vmAudit(r, req.Action, name, err)
	if err != nil {
		s.vmError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) vmClone(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	err := s.VMs.Clone(r.Context(), name, req.Name)
	s.vmAudit(r, "clone", name+" -> "+req.Name, err)
	if err != nil {
		s.vmError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) vmRename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	err := s.VMs.Rename(r.Context(), name, req.Name)
	s.vmAudit(r, "rename", name+" -> "+req.Name, err)
	if err != nil {
		s.vmError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) vmSnapshots(w http.ResponseWriter, r *http.Request) {
	snaps, err := s.VMs.Snapshots(r.Context(), r.PathValue("name"))
	if err != nil {
		s.vmError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": snaps})
}

func (s *Server) vmCreateSnapshot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	err := s.VMs.CreateSnapshot(r.Context(), name, req.Name)
	s.vmAudit(r, "snapshot.create", name+"@"+req.Name, err)
	if err != nil {
		s.vmError(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) vmRevertSnapshot(w http.ResponseWriter, r *http.Request) {
	name, snap := r.PathValue("name"), r.PathValue("snap")
	err := s.VMs.RevertSnapshot(r.Context(), name, snap)
	s.vmAudit(r, "snapshot.revert", name+"@"+snap, err)
	if err != nil {
		s.vmError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) vmDeleteSnapshot(w http.ResponseWriter, r *http.Request) {
	name, snap := r.PathValue("name"), r.PathValue("snap")
	err := s.VMs.DeleteSnapshot(r.Context(), name, snap)
	s.vmAudit(r, "snapshot.delete", name+"@"+snap, err)
	if err != nil {
		s.vmError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// vmConsole hands out a single-use ticket for the machine's screen. The
// browser opens the Remote Desktop WebSocket with it; guacd then speaks VNC
// to 127.0.0.1:<port>. In demo mode there is no screen, only a message.
func (s *Server) vmConsole(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := r.PathValue("name")
	c, err := s.VMs.Console(r.Context(), name)
	if err != nil {
		s.vmError(w, err)
		return
	}
	if c.Demo {
		writeJSON(w, http.StatusOK, c)
		return
	}
	uid := userFrom(r.Context()).ID
	id := s.rdpTickets.issue(rdpTicket{userID: uid, params: rdp.Params{
		Protocol: "vnc", Hostname: "127.0.0.1", Port: c.Port,
		Width: clampRange(req.Width, 640, 7680, 1280), Height: clampRange(req.Height, 480, 4320, 800), DPI: 96,
	}})
	s.vmAudit(r, "console", name, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ticket": id, "expires_in": int(rdpTicketTTL.Seconds())})
}

// watchVMs posts a notification when a machine stops without anyone asking.
func (s *Server) watchVMs(ctx context.Context) {
	s.VMs.Watch(ctx, func(v vm.VM, why string) {
		s.add(ctx, vmStopNotification(v, why, time.Now()))
	})
}

func vmStopNotification(v vm.VM, why string, at time.Time) notify.Notification {
	return notify.Notification{
		Key: "vmstop:" + v.UUID + ":" + strconv.FormatInt(at.UnixNano(), 10), Kind: notify.KindAppError, Level: "error",
		Title: v.Name + " stopped unexpectedly",
		Body:  "The virtual machine is off because " + strings.TrimSuffix(why, ".") + ". Open Virtual Desk to start it again.",
		Icon:  "virtualdesk", Action: &notify.Action{App: "virtualdesk", Props: map[string]any{"vm": v.Name}},
	}
}
