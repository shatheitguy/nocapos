package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"alfaos/alfad/internal/notify"
	"alfaos/alfad/internal/removable"
)

// USB drives and other external disks: ejecting one from Files, and the
// notifications when one is plugged in or out.

// filesEject unmounts an external disk and powers it off when it can
// (admin only, audited). Only mounts Files lists as external qualify.
func (s *Server) filesEject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if s.Removable == nil {
		writeError(w, http.StatusNotImplemented, "unsupported", "Ejecting drives needs NoCapOS installed directly on Linux.")
		return
	}
	var dev, name string
	for _, m := range externalMounts() {
		if m.Path == req.Path {
			dev, name = m.Device, m.Name
		}
	}
	if dev == "" {
		writeError(w, http.StatusNotFound, "not_found", "That drive isn't connected any more.")
		return
	}
	off, err := s.Removable.Eject(r.Context(), req.Path, dev)
	s.audit(r, userFrom(r.Context()).ID, "files.eject", dev+" "+req.Path, err == nil, errText(err))
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, removable.ErrBusy) {
			status = http.StatusConflict
		}
		writeError(w, status, "eject_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "powered_off": off})
}

// notifyDrives posts a notification for each drive plugged in or out.
func (s *Server) notifyDrives(ch removable.Change) {
	now := time.Now()
	for _, d := range ch.Added {
		s.add(context.Background(), driveNotification(d, true, now))
	}
	for _, d := range ch.Removed {
		s.add(context.Background(), driveNotification(d, false, now))
	}
}

func driveNotification(d removable.Drive, added bool, at time.Time) notify.Notification {
	n := notify.Notification{
		Key:  fmt.Sprintf("drive:%s:%s:%t:%d", d.Device, d.UUID, added, at.UnixNano()),
		Kind: notify.KindDevice, Level: "info", Icon: "files",
	}
	switch {
	case !added:
		n.Title, n.Body, n.Quiet = d.Name+" removed", "The drive was disconnected.", true
	case d.Mount != "":
		n.Title = d.Name + " connected — Open in Files"
		n.Body = "It's under External storage in Files."
		n.Action = &notify.Action{App: "files", Props: map[string]any{"root": "system", "path": d.Mount}}
	case d.Error != "":
		n.Level, n.Title = "warning", d.Name+" connected, but it can't be opened"
		n.Body = d.Error + "."
		n.Icon, n.Action = "storage", &notify.Action{App: "storage"}
	default:
		n.Title = d.Name + " connected"
		n.Body = "It isn't opened automatically (" + d.FSType + "). See it in Storage."
		n.Icon, n.Action = "storage", &notify.Action{App: "storage"}
	}
	return n
}
