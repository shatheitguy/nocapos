package api

import (
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"alfaos/alfad/internal/hostctl"
	"alfaos/alfad/internal/store"
)

// Saved Wi-Fi networks (Settings → Network → Wi-Fi): list, connect, forget,
// auto-connect / priority, change the password or enterprise sign-in, and
// share (password + join QR code). Admin only; every change and every
// password reveal is audited. Revealing a password also needs the admin's
// own account password again.

func (s *Server) wifiRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/system/wifi/saved", s.admin(s.wifiSavedList))
	mux.Handle("POST /api/v1/system/wifi/saved/{uuid}/connect", s.admin(s.wifiSavedConnect))
	mux.Handle("DELETE /api/v1/system/wifi/saved/{uuid}", s.admin(s.wifiSavedForget))
	mux.Handle("PATCH /api/v1/system/wifi/saved/{uuid}", s.admin(s.wifiSavedSettings))
	mux.Handle("PUT /api/v1/system/wifi/saved/{uuid}/password", s.admin(s.wifiSavedPassword))
	mux.Handle("PUT /api/v1/system/wifi/saved/{uuid}/eap", s.admin(s.wifiSavedEAP))
	mux.Handle("POST /api/v1/system/wifi/saved/{uuid}/share", s.admin(s.wifiSavedShare))
}

func (s *Server) wifiSavedList(w http.ResponseWriter, r *http.Request) {
	list, err := hostctl.SavedWiFiList(r.Context())
	if err != nil {
		s.hostError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) savedError(w http.ResponseWriter, err error) {
	if errors.Is(err, hostctl.ErrNoSuchProfile) {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	if errors.Is(err, hostctl.ErrNotEditable) {
		writeError(w, http.StatusConflict, "not_editable", err.Error())
		return
	}
	s.hostError(w, err)
}

func (s *Server) wifiSavedConnect(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("uuid")
	name := hostctl.SavedWiFiName(r.Context(), id)
	err := hostctl.SavedWiFiConnect(r.Context(), id)
	s.audit(r, userFrom(r.Context()).ID, "wifi.saved.connect", name, err == nil, errText(err))
	if err != nil {
		s.savedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hostctl.Network(r.Context()))
}

func (s *Server) wifiSavedForget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("uuid")
	name := hostctl.SavedWiFiName(r.Context(), id)
	err := hostctl.SavedWiFiForget(r.Context(), id)
	s.audit(r, userFrom(r.Context()).ID, "wifi.saved.forget", name, err == nil, errText(err))
	if err != nil {
		s.savedError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) wifiSavedSettings(w http.ResponseWriter, r *http.Request) {
	var req hostctl.SavedSettings
	if !decodeJSON(w, r, &req) {
		return
	}
	id := r.PathValue("uuid")
	name := hostctl.SavedWiFiName(r.Context(), id)
	err := hostctl.SavedWiFiSet(r.Context(), id, req)
	detail := ""
	if req.AutoConnect != nil {
		detail += "auto-connect " + onOffWord(*req.AutoConnect) + " "
	}
	if req.Priority != nil {
		detail += "priority " + strconv.Itoa(*req.Priority)
	}
	s.audit(r, userFrom(r.Context()).ID, "wifi.saved.edit", name, err == nil, joinDetail(detail, errText(err)))
	if err != nil {
		s.savedError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) wifiSavedPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	id := r.PathValue("uuid")
	name := hostctl.SavedWiFiName(r.Context(), id)
	err := hostctl.SavedWiFiSetPassword(r.Context(), id, req.Password)
	s.audit(r, userFrom(r.Context()).ID, "wifi.saved.password", name, err == nil, errText(err)) // never the password
	if err != nil {
		s.savedError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) wifiSavedEAP(w http.ResponseWriter, r *http.Request) {
	var req hostctl.EAPConfig
	if !decodeJSON(w, r, &req) {
		return
	}
	id := r.PathValue("uuid")
	name := hostctl.SavedWiFiName(r.Context(), id)
	err := hostctl.SavedWiFiSetEAP(r.Context(), id, req)
	s.audit(r, userFrom(r.Context()).ID, "wifi.saved.eap", name, err == nil, joinDetail(req.Method, errText(err))) // never secrets
	if err != nil {
		s.savedError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// wifiSavedShare reveals a saved network's password for the share sheet
// (password + QR code), after the admin types their own password again.
func (s *Server) wifiSavedShare(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"` // the admin's own account password
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	s.shareWiFi(w, r, r.PathValue("uuid"), req.Password, "wifi.saved.share")
}

func (s *Server) shareWiFi(w http.ResponseWriter, r *http.Request, id, accountPassword, action string) {
	u := userFrom(r.Context())
	name := hostctl.SavedWiFiName(r.Context(), id)
	if !s.reauth(r, u, accountPassword) {
		s.audit(r, u.ID, action, name, false, "wrong account password")
		writeError(w, http.StatusForbidden, "reauth_failed", "That's not your account password.")
		return
	}
	sh, err := hostctl.SavedWiFiShare(r.Context(), id)
	s.audit(r, u.ID, action, name, err == nil, errText(err)) // the password itself is never logged
	if err != nil {
		s.savedError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, sh)
}

// ---------- re-authentication ----------

// reauthLimit slows down guessing an account password through a stolen
// session: a few misses per user, then a pause.
var reauthLimit = struct {
	sync.Mutex
	misses map[string][]time.Time
}{misses: map[string][]time.Time{}}

const (
	reauthMaxMisses = 5
	reauthWindow    = 10 * time.Minute
)

// reauth checks an administrator's own password again before something
// sensitive (revealing a Wi-Fi password).
func (s *Server) reauth(r *http.Request, u *store.User, password string) bool {
	if u == nil || u.Role != store.RoleAdmin || password == "" {
		return false
	}
	now := time.Now()
	reauthLimit.Lock()
	recent := reauthLimit.misses[u.ID][:0]
	for _, t := range reauthLimit.misses[u.ID] {
		if now.Sub(t) < reauthWindow {
			recent = append(recent, t)
		}
	}
	reauthLimit.misses[u.ID] = recent
	blocked := len(recent) >= reauthMaxMisses
	reauthLimit.Unlock()
	if blocked {
		return false
	}
	_, admin, ok := s.Auth.CheckPassword(r.Context(), u.Username, password)
	if ok && admin {
		reauthLimit.Lock()
		delete(reauthLimit.misses, u.ID)
		reauthLimit.Unlock()
		return true
	}
	reauthLimit.Lock()
	reauthLimit.misses[u.ID] = append(reauthLimit.misses[u.ID], now)
	reauthLimit.Unlock()
	return false
}

// ---------- NoCap OS app ----------

// deviceWifiSaved lists the server's shareable Wi-Fi networks for a phone
// linked to an administrator's account (names only, no passwords).
func (s *Server) deviceWifiSaved(w http.ResponseWriter, r *http.Request) {
	if u := userFrom(r.Context()); u == nil || u.Role != store.RoleAdmin {
		writeError(w, http.StatusForbidden, "forbidden", "Only administrators can share the server's Wi-Fi.")
		return
	}
	list, err := hostctl.SavedWiFiList(r.Context())
	if err != nil {
		s.hostError(w, err)
		return
	}
	type item struct {
		UUID   string `json:"uuid"`
		SSID   string `json:"ssid"`
		Active bool   `json:"active"`
	}
	out := []item{}
	for _, n := range list {
		if n.Shareable {
			out = append(out, item{n.UUID, n.SSID, n.Active})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// deviceWifiShare is the phone's "Share Wi-Fi": the same password + QR code
// as Settings, after the administrator's account password.
func (s *Server) deviceWifiShare(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UUID     string `json:"uuid"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if u := userFrom(r.Context()); u == nil || u.Role != store.RoleAdmin {
		writeError(w, http.StatusForbidden, "forbidden", "Only administrators can share the server's Wi-Fi.")
		return
	}
	s.shareWiFi(w, r, req.UUID, req.Password, "wifi.saved.share.device")
}

func joinDetail(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + ": " + b
}
