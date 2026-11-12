package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"alfaos/alfad/internal/devices"
	"alfaos/alfad/internal/store"
)

// Linked Devices limits: an administrator sets how many devices each person
// may link (a server-wide default, plus an override per person). Making a
// pairing code and redeeming one both check it; 0 means no limit.

const deviceLimitKey = "devices.max_per_user"

// deviceQuota is one person's linked devices against their limit.
type deviceQuota struct {
	Used  int `json:"used"`
	Limit int `json:"limit"` // 0 = no limit
}

// Full: no room for another device.
func (q deviceQuota) Full() bool { return devices.AtLimit(q.Used, q.Limit) }

// defaultDeviceLimit is the server-wide default (devices.DefaultLimit until
// an administrator changes it).
func (s *Server) defaultDeviceLimit(ctx context.Context) (int, error) {
	v, err := s.Store.Setting(ctx, deviceLimitKey)
	if err != nil || v == "" {
		return devices.DefaultLimit, err
	}
	n, err := strconv.Atoi(v)
	if err != nil || !devices.ValidLimit(n) {
		return devices.DefaultLimit, nil
	}
	return n, nil
}

// deviceLimitFor is the limit that applies to one person.
func (s *Server) deviceLimitFor(ctx context.Context, userID string) (int, error) {
	n, ok, err := s.Store.DeviceLimit(ctx, userID)
	if err != nil || ok {
		return n, err
	}
	return s.defaultDeviceLimit(ctx)
}

func (s *Server) deviceQuota(ctx context.Context, userID string) (deviceQuota, error) {
	limit, err := s.deviceLimitFor(ctx, userID)
	if err != nil {
		return deviceQuota{}, err
	}
	used, err := s.Store.CountDevices(ctx, userID)
	return deviceQuota{Used: used, Limit: limit}, err
}

// linkDevice saves a newly paired device unless its owner has no room left,
// in which case it returns their quota and saves nothing. Every way of
// linking a device goes through here.
func (s *Server) linkDevice(r *http.Request, u *store.User, d *store.Device, tokenHash string) (*deviceQuota, error) {
	s.pairing.addMu.Lock()
	defer s.pairing.addMu.Unlock()
	q, err := s.deviceQuota(r.Context(), u.ID)
	if err != nil {
		return nil, err
	}
	if q.Full() {
		return &q, nil
	}
	return nil, s.Store.CreateDevice(r.Context(), d, tokenHash)
}

// writeDeviceLimit answers 409 device_limit, with the numbers so the page
// can offer to unlink an old device.
func writeDeviceLimit(w http.ResponseWriter, q deviceQuota) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"error": errorBody{
			Code: "device_limit",
			Message: fmt.Sprintf("You've linked %d of %d devices, the most this account can have. "+
				"Unlink one you don't use in Settings → Linked Devices, then try again with a fresh code.", q.Used, q.Limit),
		},
		"used": q.Used, "limit": q.Limit,
	})
}

type userDeviceLimit struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Used     int    `json:"used"`
	Limit    int    `json:"limit"`
	Override *int   `json:"override"` // nil = the default
}

type deviceLimitsJSON struct {
	deviceQuota
	Default int `json:"default"`
	Max     int `json:"max"`
	// Administrators only: everyone's numbers, and all linked devices.
	Users []userDeviceLimit `json:"users,omitempty"`
	Total *int              `json:"total,omitempty"`
}

func (s *Server) devicesLimits(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	def, err := s.defaultDeviceLimit(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	q, err := s.deviceQuota(ctx, u.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := deviceLimitsJSON{deviceQuota: q, Default: def, Max: devices.MaxLimit}
	if u.Role == store.RoleAdmin {
		users, err := s.Store.ListUsers(ctx)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		counts, err := s.Store.DeviceCounts(ctx)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		limits, err := s.Store.DeviceLimits(ctx)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		total := 0
		out.Users = make([]userDeviceLimit, 0, len(users))
		for _, x := range users {
			row := userDeviceLimit{ID: x.ID, Username: x.Username, Role: x.Role, Used: counts[x.ID], Limit: def}
			if n, ok := limits[x.ID]; ok {
				n := n
				row.Limit, row.Override = n, &n
			}
			total += row.Used
			out.Users = append(out.Users, row)
		}
		out.Total = &total
	}
	writeJSON(w, http.StatusOK, out)
}

type defaultLimitRequest struct {
	Default *int `json:"default"`
}

// devicesSetDefaultLimit changes the server-wide default (administrators).
func (s *Server) devicesSetDefaultLimit(w http.ResponseWriter, r *http.Request) {
	var req defaultLimitRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Default == nil || !devices.ValidLimit(*req.Default) {
		writeError(w, http.StatusBadRequest, "validation", fmt.Sprintf("The limit is a number from 0 (no limit) to %d.", devices.MaxLimit))
		return
	}
	if err := s.Store.SetSetting(r.Context(), deviceLimitKey, strconv.Itoa(*req.Default)); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, userFrom(r.Context()).ID, "devices.limit_default", strconv.Itoa(*req.Default), true, "")
	w.WriteHeader(http.StatusNoContent)
}

type userLimitRequest struct {
	Limit *int `json:"limit"` // null = back to the default
}

// devicesSetUserLimit sets or clears one person's limit (administrators).
// Lowering it below what they've linked unlinks nothing: they just can't
// link more until they're under it.
func (s *Server) devicesSetUserLimit(w http.ResponseWriter, r *http.Request) {
	var req userLimitRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Limit != nil && !devices.ValidLimit(*req.Limit) {
		writeError(w, http.StatusBadRequest, "validation", fmt.Sprintf("The limit is a number from 0 (no limit) to %d.", devices.MaxLimit))
		return
	}
	target, err := s.Store.UserByID(r.Context(), r.PathValue("user"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "No such user.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.Store.SetDeviceLimit(r.Context(), target.ID, req.Limit); err != nil {
		s.internalError(w, r, err)
		return
	}
	detail := "default"
	if req.Limit != nil {
		detail = strconv.Itoa(*req.Limit)
	}
	s.audit(r, userFrom(r.Context()).ID, "devices.limit_user", target.Username, true, detail)
	w.WriteHeader(http.StatusNoContent)
}

type renameDeviceRequest struct {
	Name string `json:"name"`
}

// devicesRename renames a linked device: your own, or anyone's for
// administrators.
func (s *Server) devicesRename(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var req renameDeviceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if !devices.ValidName(name) {
		writeError(w, http.StatusBadRequest, "validation", "Give the device a name (up to 64 characters).")
		return
	}
	owner := u.ID
	if u.Role == store.RoleAdmin {
		owner = ""
	}
	d, err := s.Store.RenameDevice(r.Context(), r.PathValue("id"), owner, name)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "That device isn't linked (anymore).")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, u.ID, "devices.rename", d.Name, true, "owner "+d.Username)
	writeJSON(w, http.StatusOK, toDeviceJSON(*d, u.Role == store.RoleAdmin))
}
