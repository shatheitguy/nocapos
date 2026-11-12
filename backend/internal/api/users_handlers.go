package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"alfaos/alfad/internal/accounts"
	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/store"
)

type userView struct {
	accounts.Account
	TOTP   bool   `json:"totp"`
	You    bool   `json:"you"`
	Access string `json:"access"` // desktop | files
	// What the people list shows about each person.
	Online    bool       `json:"online"`               // has NoCapOS open right now
	LastSeen  *time.Time `json:"last_seen,omitempty"`  // last active anywhere
	Sessions  int        `json:"sessions"`             // signed-in sessions and linked devices
	Avatar    bool       `json:"avatar"`               // has a profile photo
	Groups    []string   `json:"groups"`               // group names
	CreatedAt *time.Time `json:"created_at,omitempty"` // joined NoCapOS
}

func (s *Server) usersList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, err := s.Accounts.List(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	access, err := s.Store.UserAccessAll(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	groups, err := s.Store.GroupsByMember(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// Activity is nice to have: the list still loads without it.
	seen, err := s.Store.LastSeenAll(ctx)
	if err != nil {
		s.Log.Warn("users: last seen", "err", err)
	}
	photos, err := s.Store.AvatarOwners(ctx)
	if err != nil {
		s.Log.Warn("users: avatars", "err", err)
	}
	me := userFrom(ctx)
	out := make([]userView, 0, len(list))
	for _, a := range list {
		key := strings.ToLower(a.Username)
		v := userView{Account: a, You: a.Username == me.Username, Access: store.AccessDesktop, Groups: groups[key]}
		if v.Groups == nil {
			v.Groups = []string{}
		}
		if a.Role != store.RoleAdmin && access[key] == store.AccessFiles {
			v.Access = store.AccessFiles
		}
		if u, err := s.Store.UserByUsername(ctx, a.Username); err == nil {
			v.TOTP = u.TOTPEnabled
			created := u.CreatedAt
			v.CreatedAt = &created
			v.Avatar = photos[u.ID]
			v.Online = s.live.count(u.ID) > 0
			if t, ok := seen[u.ID]; ok {
				v.LastSeen = &t
			}
			if n, err := s.otherSessions(ctx, u.ID, "", ""); err == nil {
				v.Sessions = n
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"mode": s.Accounts.Mode(), "users": out})
}

// userAvatarGet: anyone's profile photo, for administrators' people list.
func (s *Server) userAvatarGet(w http.ResponseWriter, r *http.Request) {
	u, err := s.Store.UserByUsername(r.Context(), r.PathValue("name"))
	if err == nil {
		var data []byte
		var mime string
		data, mime, err = s.Store.Avatar(r.Context(), u.ID)
		if err == nil {
			w.Header().Set("Content-Type", mime)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Cache-Control", "private, no-cache")
			_, _ = w.Write(data)
			return
		}
	}
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no profile photo")
		return
	}
	s.internalError(w, r, err)
}

type createUserRequest struct {
	Username string `json:"username"`
	FullName string `json:"full_name"`
	Password string `json:"password"`
	Role     string `json:"role"`
	Access   string `json:"access"` // desktop (default) | files
}

func validAccess(role, access string) error {
	switch {
	case access != store.AccessDesktop && access != store.AccessFiles:
		return errors.New("access must be desktop or files")
	case role == store.RoleAdmin && access == store.AccessFiles:
		return errors.New("administrators always have the desktop")
	}
	return nil
}

func (s *Server) usersCreate(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Access == "" {
		req.Access = store.AccessDesktop
	}
	if err := validAccess(req.Role, req.Access); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	err := s.Accounts.Create(r.Context(), accounts.NewAccount{Username: req.Username, FullName: req.FullName, Password: req.Password, Role: req.Role})
	s.audit(r, userFrom(r.Context()).ID, "user.create", req.Username, err == nil, "access="+req.Access+" "+errText(err))
	if err != nil {
		s.accountError(w, r, err)
		return
	}
	ctx := r.Context()
	if err := s.Store.SetUserAccess(ctx, req.Username, req.Access); err != nil {
		s.Log.Warn("set access type", "user", req.Username, "err", err)
	}
	if s.Spaces != nil {
		if _, err := s.Spaces.EnsureHome(req.Username); err != nil {
			s.Log.Warn("spaces: create home", "user", req.Username, "err", err)
		}
		s.Spaces.SecureHome(req.Username) // Linux homes: private, owned by them
	}
	s.syncSharingPassword(ctx, req.Username, req.Password)
	s.spacesChanged(ctx)
	writeJSON(w, http.StatusCreated, map[string]string{"username": req.Username, "access": req.Access})
}

// syncSharingPassword gives a person's new password to their Samba account
// (no-op where Samba can't run).
func (s *Server) syncSharingPassword(ctx context.Context, username, password string) {
	if s.NetDrives == nil {
		return
	}
	if err := s.NetDrives.SyncUser(ctx, username, password); err != nil {
		s.Log.Warn("sharing: set Samba password", "user", username, "err", err)
	}
}

// spacesChanged regenerates the sharing setup after account changes.
func (s *Server) spacesChanged(ctx context.Context) {
	if s.Spaces != nil {
		s.Spaces.Changed(context.WithoutCancel(ctx))
	}
}

type updateUserRequest struct {
	Role     *string `json:"role"`
	Disabled *bool   `json:"disabled"`
	Password *string `json:"password"`
	Access   *string `json:"access"`
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
		writeError(w, http.StatusBadRequest, "self", "You can't demote or disable your own account — ask another administrator to do it.")
		return
	}
	if (req.Role != nil && *req.Role != store.RoleAdmin || req.Disabled != nil && *req.Disabled) && s.lastAdmin(r, name) {
		writeError(w, http.StatusBadRequest, "last_admin", "This is the only administrator. Make someone else an administrator first, then come back.")
		return
	}

	var err error
	switch {
	case req.Role != nil:
		if *req.Role == store.RoleAdmin {
			// Administrators always have the desktop.
			if aerr := s.Store.SetUserAccess(ctx, name, store.AccessDesktop); aerr != nil {
				s.Log.Warn("set access type", "user", name, "err", aerr)
			}
		}
		err = s.Accounts.SetRole(ctx, name, *req.Role)
		s.audit(r, me.ID, "user.role", name+" → "+*req.Role, err == nil, errText(err))
		if err == nil {
			s.syncRecord(r, name, func(u *store.User) error {
				if *req.Role != store.RoleAdmin {
					// Demoted: open terminals, remote desktops and admin feeds end now.
					defer s.signOutEverywhere(ctx, u)
				} else {
					defer s.closeLive(u) // reconnects with the new rights
				}
				return s.Store.SetRole(ctx, u.ID, *req.Role)
			})
			s.spacesChanged(ctx)
		}
	case req.Access != nil:
		if self {
			writeError(w, http.StatusBadRequest, "self", "You can't change your own access.")
			return
		}
		a, lerr := s.Accounts.Lookup(ctx, name)
		if lerr != nil {
			s.accountError(w, r, lerr)
			return
		}
		if verr := validAccess(a.Role, *req.Access); verr != nil {
			writeError(w, http.StatusBadRequest, "bad_request", verr.Error())
			return
		}
		err = s.Store.SetUserAccess(ctx, a.Username, *req.Access)
		s.audit(r, me.ID, "user.access", name+" → "+*req.Access, err == nil, errText(err))
		if err == nil && *req.Access == store.AccessFiles {
			// File sharing only: end their desktop sessions now.
			s.syncRecord(r, name, func(u *store.User) error { s.signOutEverywhere(ctx, u); return nil })
		}
	case req.Disabled != nil:
		err = s.Accounts.SetDisabled(ctx, name, *req.Disabled)
		s.audit(r, me.ID, "user.disable", name+" → "+onOffWord(*req.Disabled), err == nil, errText(err))
		if err == nil {
			s.syncRecord(r, name, func(u *store.User) error {
				if err := s.Store.SetDisabled(ctx, u.ID, *req.Disabled); err != nil {
					return err
				}
				if *req.Disabled {
					s.signOutEverywhere(ctx, u)
				}
				return nil
			})
			if s.NetDrives != nil {
				s.NetDrives.SetUserEnabled(ctx, name, !*req.Disabled)
			}
			s.spacesChanged(ctx)
		}
	case req.Password != nil:
		err = s.Accounts.SetPassword(ctx, name, *req.Password)
		s.audit(r, me.ID, "user.password", name, err == nil, errText(err))
		if err == nil {
			s.syncSharingPassword(ctx, name, *req.Password)
		}
		if err == nil && !self {
			// A password reset signs that person out everywhere.
			s.syncRecord(r, name, func(u *store.User) error { s.signOutEverywhere(ctx, u); return nil })
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
		writeError(w, http.StatusBadRequest, "last_admin", "This is the only administrator. Make someone else an administrator first, then come back.")
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
		s.live.closeUser(u.ID)
		_ = s.Store.DeleteUser(r.Context(), u.ID)
	}
	// Their access type, folder permissions and app access go too; their
	// home folder stays on disk (an administrator can remove it in Files).
	if err := s.Store.ForgetUser(r.Context(), name); err != nil {
		s.Log.Warn("forget user", "user", name, "err", err)
	}
	if s.NetDrives != nil {
		s.NetDrives.RemoveUser(r.Context(), name)
	}
	if s.Spaces != nil {
		_ = s.Spaces.Reload(r.Context())
	}
	s.spacesChanged(r.Context())
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
		writeError(w, http.StatusNotFound, "not_found", "Lowkey can't find that user anymore.")
	case errors.Is(err, accounts.ErrExists):
		writeError(w, http.StatusConflict, "exists", err.Error())
	case errors.Is(err, accounts.ErrProtected), errors.Is(err, accounts.ErrBadName):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, auth.ErrInvalidUsername):
		writeError(w, http.StatusBadRequest, "bad_request", "User names are 3–32 letters, digits, '.', '_' or '-'")
	case errors.Is(err, auth.ErrWeakPassword):
		writeError(w, http.StatusBadRequest, "weak_password", "Too short — passwords need at least 10 characters.")
	default:
		// Tool output (useradd/chpasswd/…) is the most useful thing to show.
		writeError(w, http.StatusBadRequest, "account_failed", err.Error())
		s.Log.Warn("account change failed", "path", r.URL.Path, "err", err)
	}
}
