package api

import (
	"errors"
	"net/http"
	"time"

	"alfaos/alfad/internal/hostctl"
)

func (s *Server) networkState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, hostctl.Network(r.Context()))
}

type networkRequest struct {
	WiFi       *bool `json:"wifi"`
	Networking *bool `json:"networking"`
}

// networkSet flips the Wi-Fi radio and/or all networking (admin only).
func (s *Server) networkSet(w http.ResponseWriter, r *http.Request) {
	var req networkRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	uid := userFrom(r.Context()).ID
	apply := func(what string, on bool, fn func(bool) error) bool {
		err := fn(on)
		s.audit(r, uid, "network."+what, onOffWord(on), err == nil, errText(err))
		if err != nil {
			status, code := http.StatusBadGateway, "network_failed"
			if errors.Is(err, hostctl.ErrUnsupported) {
				status, code = http.StatusNotImplemented, "unsupported"
			}
			writeError(w, status, code, "Could not switch "+what+" "+onOffWord(on)+": "+err.Error())
			return false
		}
		return true
	}
	ctx := r.Context()
	if req.WiFi != nil && !apply("wifi", *req.WiFi, func(on bool) error { return hostctl.SetWiFi(ctx, on) }) {
		return
	}
	if req.Networking != nil && !apply("networking", *req.Networking, func(on bool) error { return hostctl.SetNetworking(ctx, on) }) {
		return
	}
	writeJSON(w, http.StatusOK, hostctl.Network(ctx))
}

func (s *Server) powerInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, hostctl.Power())
}

// powerAction restarts or shuts down the host (admin only). It answers first
// and acts after a short delay so the browser can show what happened.
func (s *Server) powerAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"` // reboot | shutdown
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Action != "reboot" && req.Action != "shutdown" {
		writeError(w, http.StatusBadRequest, "bad_request", "action must be reboot or shutdown")
		return
	}
	err := hostctl.Schedule(req.Action, 1500*time.Millisecond)
	s.audit(r, userFrom(r.Context()).ID, "power."+req.Action, "", err == nil, errText(err))
	if err != nil {
		writeError(w, http.StatusNotImplemented, "unsupported", hostctl.Power().Reason)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "scheduled", "action": req.Action})
}

func onOffWord(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ---------- Wi-Fi networks & IPv4 settings ----------

func (s *Server) wifiList(w http.ResponseWriter, r *http.Request) {
	list, err := hostctl.WiFiNetworks(r.Context(), r.URL.Query().Get("rescan") == "1")
	if err != nil {
		s.hostError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) wifiConnect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SSID     string `json:"ssid"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	err := hostctl.WiFiConnect(r.Context(), req.SSID, req.Password)
	s.audit(r, userFrom(r.Context()).ID, "wifi.connect", req.SSID, err == nil, errText(err)) // never the password
	if err != nil {
		s.hostError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hostctl.Network(r.Context()))
}

func (s *Server) wifiDisconnect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Device string `json:"device"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	err := hostctl.WiFiDisconnect(r.Context(), req.Device)
	s.audit(r, userFrom(r.Context()).ID, "wifi.disconnect", req.Device, err == nil, errText(err))
	if err != nil {
		s.hostError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hostctl.Network(r.Context()))
}

const ipv4KeepWindow = 90 * time.Second

func (s *Server) ipv4Set(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Connection string `json:"connection"`
		hostctl.IPv4Config
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	token, err := hostctl.ChangeIPv4(r.Context(), req.Connection, req.IPv4Config, ipv4KeepWindow)
	detail := req.Method
	if req.Method == "manual" {
		detail += " " + req.Address
	}
	s.audit(r, userFrom(r.Context()).ID, "network.ipv4", req.Connection+": "+detail, err == nil, errText(err))
	if err != nil {
		s.hostError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"token": token, "revert_in": int(ipv4KeepWindow.Seconds())})
}

func (s *Server) ipv4Keep(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !hostctl.KeepIPv4(req.Token) {
		writeError(w, http.StatusGone, "expired", "Too late — the previous settings were already restored.")
		return
	}
	s.audit(r, userFrom(r.Context()).ID, "network.ipv4.keep", "", true, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "kept"})
}

func (s *Server) ipv4Revert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ok := hostctl.RevertIPv4(req.Token)
	s.audit(r, userFrom(r.Context()).ID, "network.ipv4.revert", "", ok, "")
	writeJSON(w, http.StatusOK, map[string]bool{"reverted": ok})
}

func (s *Server) hostError(w http.ResponseWriter, err error) {
	if errors.Is(err, hostctl.ErrUnsupported) {
		writeError(w, http.StatusNotImplemented, "unsupported", "This needs NetworkManager on a Linux host.")
		return
	}
	writeError(w, http.StatusBadRequest, "network_failed", err.Error())
}
