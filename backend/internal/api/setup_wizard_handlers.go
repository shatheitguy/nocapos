package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"alfaos/alfad/internal/hostctl"
)

// The setup wizard after the first account: the look is the person's own
// prefs; the machine-wide part (name, network, time zone, the screen) is
// done once per server, which is what these endpoints track.

const settingMachineSetup = "setup.machine_done"

type machineSetupState struct {
	Done     bool                  `json:"done"` // an administrator finished (or skipped) the machine steps
	Hostname string                `json:"hostname"`
	TimeZone hostctl.TimeZoneState `json:"timezone"`
}

func (s *Server) setupMachineGet(w http.ResponseWriter, r *http.Request) {
	v, err := s.Store.Setting(r.Context(), settingMachineSetup)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	st := machineSetupState{Done: v != "", TimeZone: hostctl.TimeZone(r.Context())}
	if s.Sampler != nil {
		st.Hostname = s.Sampler.HostInfo().Hostname
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) setupMachineDone(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if err := s.Store.SetSetting(r.Context(), settingMachineSetup, time.Now().UTC().Format(time.RFC3339)+" "+u.Username); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, u.ID, "setup.machine", "", true, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) timezoneGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, hostctl.TimeZone(r.Context()))
}

func (s *Server) timezoneSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Zone string `json:"zone"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	zone, err := hostctl.SetTimeZone(r.Context(), req.Zone)
	s.audit(r, userFrom(r.Context()).ID, "system.timezone", truncate(req.Zone, 64), err == nil, errText(err))
	if err != nil {
		if errors.Is(err, hostctl.ErrUnsupported) {
			writeError(w, http.StatusNotImplemented, "unsupported", hostctl.TimeZone(r.Context()).Reason)
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, hostctl.TimeZoneState{Zone: zone, Supported: true})
}

// ipConfirmWindow: how long a new address setup waits for the browser to
// reach the server on it before putting the old one back.
const ipConfirmWindow = 60 * time.Second

// ipSet changes a connection's IPv4 (and optionally IPv6) setup. It is kept
// only if the browser reaches the server afterwards (ipConfirm) within the
// window; otherwise the old setup comes back by itself.
func (s *Server) ipSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Connection string `json:"connection"`
		hostctl.IPChange
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	token, err := hostctl.ChangeIP(r.Context(), req.Connection, req.IPChange, ipConfirmWindow)
	detail := "ipv4 " + req.IPv4.Method
	if req.IPv4.Method == "manual" {
		detail += " " + req.IPv4.Address
	}
	if req.IPv6 != nil {
		detail += ", ipv6 " + req.IPv6.Method
		if req.IPv6.Method == "manual" {
			detail += " " + req.IPv6.Address
		}
	}
	s.audit(r, userFrom(r.Context()).ID, "network.ip", truncate(req.Connection+": "+detail, 200), err == nil, errText(err))
	if err != nil {
		s.hostError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"token": token, "revert_in": int(ipConfirmWindow.Seconds())})
}

// ipConfirm keeps a pending address change. It needs no session: the browser
// may now be on a different address (a new origin without its sign-in), and
// the 128-bit token from ipSet is the proof it's the same person. Reaching
// this endpoint at all is the proof the new address works.
func (s *Server) ipConfirm(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	tok := strings.TrimSpace(req.Token)
	if len(tok) != 32 {
		writeError(w, http.StatusGone, "expired", "That change is no longer waiting — the previous settings were restored.")
		return
	}
	switch hostctl.ConfirmIP(tok) {
	case hostctl.ConfirmKept:
		s.audit(r, "", "network.ip.confirm", "", true, "reached on the new address")
		writeJSON(w, http.StatusOK, map[string]string{"status": hostctl.ConfirmKept})
	case hostctl.ConfirmPending:
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusAccepted, map[string]string{"status": hostctl.ConfirmPending})
	default:
		writeError(w, http.StatusGone, "expired", "That change is no longer waiting — the previous settings were restored.")
	}
}
