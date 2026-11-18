package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"alfaos/alfad/internal/store"
)

// Remote Desktop's saved computers: each person's own list, on the server.
// Passwords are kept only when the person ticks "Remember password", sealed
// with the server's secret box, and never sent back.

const maxRemoteConns = 200

func (s *Server) rdpSavedRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/rdp/connections", s.admin(s.remoteConnList))
	mux.Handle("POST /api/v1/rdp/connections", s.admin(s.remoteConnCreate))
	mux.Handle("PUT /api/v1/rdp/connections/{id}", s.admin(s.remoteConnUpdate))
	mux.Handle("DELETE /api/v1/rdp/connections/{id}", s.admin(s.remoteConnDelete))
	mux.Handle("POST /api/v1/rdp/connections/{id}/duplicate", s.admin(s.remoteConnDuplicate))
}

// remoteConnView is a saved computer as the app sees it.
type remoteConnView struct {
	*store.RemoteConn
	Options            rdpOptions `json:"options"`
	HasPassword        bool       `json:"has_password"`
	HasGatewayPassword bool       `json:"has_gateway_password"`
}

func viewRemoteConn(c *store.RemoteConn) remoteConnView {
	v := remoteConnView{RemoteConn: c, HasPassword: len(c.PasswordEnc) > 0, HasGatewayPassword: len(c.GatewayEnc) > 0}
	_ = json.Unmarshal([]byte(c.Settings), &v.Options)
	return v
}

// remoteConnInput is what the app saves.
type remoteConnInput struct {
	Name     string     `json:"name"`
	Protocol string     `json:"protocol"`
	Hostname string     `json:"hostname"`
	Port     int        `json:"port"`
	Username string     `json:"username"`
	Domain   string     `json:"domain"`
	Options  rdpOptions `json:"options"`
	// RememberPassword keeps Password (and GatewayPassword); an empty one
	// keeps what's remembered already. Unticked, both are forgotten.
	RememberPassword bool   `json:"remember_password"`
	Password         string `json:"password"`
	GatewayPassword  string `json:"gateway_password"`
}

func (in *remoteConnInput) check() string {
	in.Name = strings.TrimSpace(in.Name)
	in.Hostname = strings.TrimSpace(in.Hostname)
	in.Username = strings.TrimSpace(in.Username)
	in.Domain = strings.TrimSpace(in.Domain)
	if in.Protocol == "" {
		in.Protocol = "rdp"
	}
	switch {
	case !validProtocol(in.Protocol):
		return "protocol must be rdp or vnc"
	case !validHost(in.Hostname):
		return "enter a valid host name or IP address"
	case in.Port < 0 || in.Port > 65535:
		return "invalid port"
	case len(in.Name) > 120:
		return "the name is too long"
	case len(in.Username) > 256 || len(in.Domain) > 256 || len(in.Password) > 1024 || len(in.GatewayPassword) > 1024:
		return "too long"
	}
	if in.Name == "" {
		in.Name = in.Hostname
	}
	return in.Options.check()
}

func newRemoteID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// openSecret opens a sealed password ("" when there's none or it can't be read).
func (s *Server) openSecret(sealed []byte) string {
	if len(sealed) == 0 || s.Secrets == nil {
		return ""
	}
	v, err := s.Secrets.Open(sealed)
	if err != nil {
		return ""
	}
	return v
}

func (s *Server) remoteConnError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "That saved computer is gone.")
	case errors.Is(err, store.ErrLimit):
		writeError(w, http.StatusConflict, "limit", "You have as many saved computers as NoCapOS keeps. Delete one first.")
	default:
		s.internalError(w, r, err)
	}
}

// apply copies in onto c, sealing or forgetting the passwords.
func (s *Server) applyRemoteInput(w http.ResponseWriter, c *store.RemoteConn, in remoteConnInput) bool {
	settings, _ := json.Marshal(in.Options)
	c.Name, c.Protocol, c.Hostname, c.Port, c.Username, c.Domain, c.Settings =
		in.Name, in.Protocol, in.Hostname, in.Port, in.Username, in.Domain, string(settings)
	if !in.RememberPassword {
		c.PasswordEnc, c.GatewayEnc = nil, nil
		return true
	}
	if s.Secrets == nil {
		writeError(w, http.StatusServiceUnavailable, "unsupported", "This server can't keep passwords safely right now.")
		return false
	}
	if in.Password != "" {
		c.PasswordEnc = s.Secrets.Seal(in.Password)
	}
	if in.GatewayPassword != "" {
		c.GatewayEnc = s.Secrets.Seal(in.GatewayPassword)
	}
	if in.Options.GatewayHost == "" {
		c.GatewayEnc = nil
	}
	return true
}

func (s *Server) remoteConnList(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListRemoteConns(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]remoteConnView, 0, len(list))
	for _, c := range list {
		out = append(out, viewRemoteConn(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": out, "can_remember": s.Secrets != nil})
}

func (s *Server) remoteConnCreate(w http.ResponseWriter, r *http.Request) {
	var in remoteConnInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if msg := in.check(); msg != "" {
		writeError(w, http.StatusBadRequest, "bad_request", msg)
		return
	}
	now := time.Now().UnixMilli()
	c := &store.RemoteConn{ID: newRemoteID(), CreatedAt: now, UpdatedAt: now}
	if !s.applyRemoteInput(w, c, in) {
		return
	}
	uid := userFrom(r.Context()).ID
	if err := s.Store.CreateRemoteConn(r.Context(), uid, c, maxRemoteConns); err != nil {
		s.remoteConnError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, viewRemoteConn(c))
}

func (s *Server) remoteConnUpdate(w http.ResponseWriter, r *http.Request) {
	var in remoteConnInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if msg := in.check(); msg != "" {
		writeError(w, http.StatusBadRequest, "bad_request", msg)
		return
	}
	uid := userFrom(r.Context()).ID
	c, err := s.Store.RemoteConn(r.Context(), uid, r.PathValue("id"))
	if err != nil {
		s.remoteConnError(w, r, err)
		return
	}
	if !s.applyRemoteInput(w, c, in) {
		return
	}
	c.UpdatedAt = time.Now().UnixMilli()
	if err := s.Store.UpdateRemoteConn(r.Context(), uid, c); err != nil {
		s.remoteConnError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, viewRemoteConn(c))
}

func (s *Server) remoteConnDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteRemoteConn(r.Context(), userFrom(r.Context()).ID, r.PathValue("id")); err != nil {
		s.remoteConnError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// remoteConnDuplicate copies a saved computer, remembered passwords too.
func (s *Server) remoteConnDuplicate(w http.ResponseWriter, r *http.Request) {
	uid := userFrom(r.Context()).ID
	c, err := s.Store.RemoteConn(r.Context(), uid, r.PathValue("id"))
	if err != nil {
		s.remoteConnError(w, r, err)
		return
	}
	now := time.Now().UnixMilli()
	cp := *c
	cp.ID, cp.CreatedAt, cp.UpdatedAt, cp.LastUsed = newRemoteID(), now, now, nil
	cp.Name = copyName(c.Name)
	if err := s.Store.CreateRemoteConn(r.Context(), uid, &cp, maxRemoteConns); err != nil {
		s.remoteConnError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, viewRemoteConn(&cp))
}

// copyName: "Office PC" → "Office PC (copy)", kept within the length limit.
func copyName(name string) string {
	const suffix = " (copy)"
	if len(name)+len(suffix) > 120 {
		name = strings.ToValidUTF8(name[:120-len(suffix)], "")
	}
	return name + suffix
}
