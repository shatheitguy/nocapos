package api

import (
	"errors"
	"net/http"
	"time"

	"alfaos/alfad/internal/accounts"
	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/store"
)

type userView struct {
	accounts.Account
	TOTP bool `json:"totp"`
	You  bool `json:"you"`
}

func (s *Server) usersList(w http.ResponseWriter, r *http.Request) {
	list, err := s.Accounts.List(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	me := userFrom(r.Context())
	out := make([]userView, 0, len(list))
	for _, a := range list {
		v := userView{Account: a, You: a.Username == me.Username}
		if u, err := s.Store.UserByUsername(r.Context(), a.Username); err == nil {
			v.TOTP = u.TOTPEnabled
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"mode": s.Accounts.Mode(), "users": out})
}

type createUserRequest struct {
	Username string `json:"username"`
	FullName string `json:"full_name"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func (s *Server) usersCreate(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	err := s.Accounts.Create(r.Context(), accounts.NewAccount{Username: req.Username, FullName: req.FullName, Password: req.Password, Role: req.Role})
	s.audit(r, userFrom(r.Context()).ID, "user.create", req.Username, err == nil, errText(err))
	if err != nil {
		s.accountError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"username": req.Username})
}

type updateUserRequest struct {
	Role     *string `json:"role"`
	Disabled *bool   `json:"disabled"`
	Password *string `json:"password"`
}

func (s *Server) usersUpdate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req updateUserRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	me := userFrom(r.Context())
	ctx := r.Context()
	self := name == me.Username
	if self && (req.Role != nil && *req.Role != store.RoleAdmin || req.Disabled != nil && *req.Disabled) {
		writeError(w, http.StatusBadRequest, "self", "You can't demote or disable your own account — ask another administrator.")
		return
	}
	if (req.Role != nil && *req.Role != store.RoleAdmin || req.Disabled != nil && *req.Disabled) && s.lastAdmin(r, name) {
		writeError(w, http.StatusBadRequest, "last_admin", "This is the only administrator. Make someone else an administrator first.")
		return
	}

	var err error
	switch {
	case req.Role != nil:
		err = s.Accounts.SetRole(ctx, name, *req.Role)
		s.audit(r, me.ID, "user.role", name+" → "+*req.Role, err == nil, errText(err))
		if err == nil {
			s.syncRecord(r, name, func(u *store.User) error { return s.Store.SetRole(ctx, u.ID, *req.Role) })
		}
	case req.Disabled != nil:
		err = s.Accounts.SetDisabled(ctx, name, *req.Disabled)
		s.audit(r, me.ID, "user.disable", name+" → "+onOffWord(*req.Disabled), err == nil, errText(err))
		if err == nil {
			s.syncRecord(r, name, func(u *store.User) error {
				if *req.Disabled {
					_ = s.Store.RevokeAllUserSessions(ctx, u.ID, time.Now())
				}
				return s.Store.SetDisabled(ctx, u.ID, *req.Disabled)
			})
		}
	case req.Password != nil:
		err = s.Accounts.SetPassword(ctx, name, *req.Password)
		s.audit(r, me.ID, "user.password", name, err == nil, errText(err))
		if err == nil && !self {
			// A password reset signs that person out everywhere.
			s.syncRecord(r, name, func(u *store.User) error { return s.Store.RevokeAllUserSessions(ctx, u.ID, time.Now()) })
		}
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "nothing to change")
		return
	}
	if err != nil {
		s.accountError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) usersDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	me := userFrom(r.Context())
	if name == me.Username {
		writeError(w, http.StatusBadRequest, "self", "You can't delete your own account.")
		return
	}
	if s.lastAdmin(r, name) {
		writeError(w, http.StatusBadRequest, "last_admin", "This is the only administrator. Make someone else an administrator first.")
		return
	}
	removeHome := r.URL.Query().Get("remove_home") == "1"
	err := s.Accounts.Delete(r.Context(), name, removeHome)
	detail := ""
	if removeHome {
		detail = "home removed"
	}
	if err != nil {
		detail = errText(err)
	}
	s.audit(r, me.ID, "user.delete", name, err == nil, detail)
	if err != nil {
		s.accountError(w, r, err)
		return
	}
	// Drop NoCapOS's own record too (sessions, chats and settings cascade).
	if u, err := s.Store.UserByUsername(r.Context(), name); err == nil {
		_ = s.Store.DeleteUser(r.Context(), u.ID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// lastAdmin reports whether name is the only enabled administrator.
func (s *Server) lastAdmin(r *http.Request, name string) bool {
	list, err := s.Accounts.List(r.Context())
	if err != nil {
		return false
	}
	admins, isAdmin := 0, false
	for _, a := range list {
		if a.Role == store.RoleAdmin && !a.Disabled {
			admins++
			if a.Username == name {
				isAdmin = true
			}
		}
	}
	return isAdmin && admins <= 1
}

// syncRecord applies a change to NoCapOS's own record for a user, if any.
func (s *Server) syncRecord(r *http.Request, name string, fn func(*store.User) error) {
	if u, err := s.Store.UserByUsername(r.Context(), name); err == nil {
		if err := fn(u); err != nil {
			s.Log.Warn("sync user record", "user", name, "err", err)
		}
	}
}

func (s *Server) accountError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, accounts.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such user")
	case errors.Is(err, accounts.ErrExists):
		writeError(w, http.StatusConflict, "exists", err.Error())
	case errors.Is(err, accounts.ErrProtected), errors.Is(err, accounts.ErrBadName):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, auth.ErrInvalidUsername):
		writeError(w, http.StatusBadRequest, "bad_request", "User names are 3–32 letters, digits, '.', '_' or '-'")
	case errors.Is(err, auth.ErrWeakPassword):
		writeError(w, http.StatusBadRequest, "weak_password", "Passwords need at least 10 characters")
	default:
		// Tool output (useradd/chpasswd/…) is the most useful thing to show.
		writeError(w, http.StatusBadRequest, "account_failed", err.Error())
		s.Log.Warn("account change failed", "path", r.URL.Path, "err", err)
	}
}
