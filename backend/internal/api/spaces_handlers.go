package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/spaces"
	"alfaos/alfad/internal/store"
)

// Per-person spaces: who may use which locations in Files, shared folders
// and their permissions, app access, and personal desktop preferences.
// Every check here runs on the server; the UI only hides what it can't use.

func (s *Server) viewer(r *http.Request) spaces.Viewer {
	u := userFrom(r.Context())
	return spaces.Viewer{Name: u.Username, Admin: u.Role == store.RoleAdmin}
}

func viewerOf(u *store.User) spaces.Viewer {
	return spaces.Viewer{Name: u.Username, Admin: u.Role == store.RoleAdmin}
}

// access is what the signed-in person may do at rel in root.
func (s *Server) access(r *http.Request, root, rel string) spaces.Perm {
	if s.Spaces == nil {
		if userFrom(r.Context()).Role == store.RoleAdmin {
			return spaces.ReadWrite
		}
		return spaces.None
	}
	return s.Spaces.Access(s.viewer(r), root, rel)
}

// canUse checks the person may read (or, with write, change) every path in
// root, answering 403 when not.
func (s *Server) canUse(w http.ResponseWriter, r *http.Request, root string, write bool, rels ...string) bool {
	if len(rels) == 0 {
		rels = []string{"."}
	}
	for _, rel := range rels {
		p := s.access(r, root, rel)
		if !p.CanRead() {
			writeError(w, http.StatusForbidden, "forbidden", "You don't have access to this folder.")
			return false
		}
		if write && !p.CanWrite() {
			writeError(w, http.StatusForbidden, "read_only", "You can look at this folder but not change it.")
			return false
		}
	}
	return true
}

// ownFiles hands files NoCapOS (running as root) just made in someone's
// Linux home to that person.
func (s *Server) ownFiles(root string, rels ...string) {
	if s.Spaces != nil {
		s.Spaces.Own(root, rels...)
	}
}

// ownResult does ownFiles for a finished background job.
func (s *Server) ownResult(root string, j files.Job) {
	if j.Result != nil {
		s.ownFiles(root, j.Result.Paths...)
	}
}

// ---------- shared folders (admin) ----------

type sharesOverview struct {
	Folders        []spaces.Folder `json:"folders"`
	People         []spaces.Person `json:"people"`
	AdminsSeeHomes bool            `json:"admins_see_homes"`
	Storage        *rootView       `json:"storage,omitempty"`
	Locations      []rootView      `json:"locations"`
	PerUser        bool            `json:"per_user"`
	Support        any             `json:"support"`
	SMB            bool            `json:"smb"`
	WebDAV         bool            `json:"webdav"`
	SharedAccount  bool            `json:"shared_account"` // the old "nocapos" account has a password
}

func (s *Server) sharesList(w http.ResponseWriter, r *http.Request) {
	people, err := s.Spaces.People(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	sharing, err := s.NetDrives.Sharing(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := sharesOverview{
		Folders: s.Spaces.Folders(), People: people, AdminsSeeHomes: s.Spaces.AdminsSeeHomes(),
		Locations: []rootView{}, PerUser: s.Spaces.PerUserSharing(), Support: s.NetDrives.Support(),
		SMB: sharing.SMB, WebDAV: sharing.WebDAV, SharedAccount: sharing.PasswordSet,
	}
	if out.People == nil {
		out.People = []spaces.Person{}
	}
	if st, ok := s.Spaces.Storage(); ok {
		out.Storage = &rootView{ID: st.ID, Name: st.Name}
	}
	for _, rt := range s.Files.Roots() {
		if rt.ID != "system" {
			out.Locations = append(out.Locations, rootView{ID: rt.ID, Name: rt.Name})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) shareError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such shared folder")
	case errors.Is(err, spaces.ErrOutside):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	default:
		writeError(w, http.StatusBadRequest, "share_error", err.Error())
		s.Log.Debug("shared folder change refused", "path", r.URL.Path, "err", err)
	}
}

func (s *Server) sharesCreate(w http.ResponseWriter, r *http.Request) {
	var req spaces.FolderInput
	if !decodeJSON(w, r, &req) {
		return
	}
	f, err := s.Spaces.CreateFolder(r.Context(), req)
	name := ""
	if req.Name != nil {
		name = *req.Name
	}
	s.audit(r, userFrom(r.Context()).ID, "share.create", name+" = "+req.Root+":"+req.Path, err == nil, errText(err))
	if err != nil {
		s.shareError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, f)
}

func (s *Server) sharesUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req spaces.FolderInput
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Root != "" || req.Path != "" {
		writeError(w, http.StatusBadRequest, "bad_request", "A shared folder can't be moved; share the other folder instead.")
		return
	}
	f, err := s.Spaces.UpdateFolder(r.Context(), id, req)
	s.audit(r, userFrom(r.Context()).ID, "share.update", strconv.FormatInt(id, 10), err == nil, folderChange(req)+errText(err))
	if err != nil {
		s.shareError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func folderChange(in spaces.FolderInput) string {
	var parts []string
	if in.Name != nil {
		parts = append(parts, "name="+*in.Name)
	}
	if in.Description != nil {
		parts = append(parts, "description")
	}
	for k, v := range map[string]*bool{"recycle": in.Recycle, "network": in.Network, "read_only": in.ReadOnly} {
		if v != nil {
			parts = append(parts, k+"="+strconv.FormatBool(*v))
		}
	}
	if in.Guest != nil {
		g := *in.Guest
		if g == "" {
			g = "off"
		}
		parts = append(parts, "guest="+g)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ") + " "
}

// deleteDataConfirm must be typed to erase a shared folder's files.
const deleteDataConfirm = "delete data"

func (s *Server) sharesDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		DeleteData bool   `json:"delete_data"`
		Confirm    string `json:"confirm"`
	}
	if r.ContentLength > 0 && !decodeJSON(w, r, &req) {
		return
	}
	if req.DeleteData && strings.TrimSpace(strings.ToLower(req.Confirm)) != deleteDataConfirm {
		writeError(w, http.StatusBadRequest, "confirm", `Type "delete data" to erase the folder's files.`)
		return
	}
	f, _ := s.Spaces.Folder(id)
	err := s.Spaces.DeleteFolder(r.Context(), id, req.DeleteData)
	detail := "files kept"
	if req.DeleteData {
		detail = "files deleted"
	}
	if err != nil {
		detail = errText(err)
	}
	s.audit(r, userFrom(r.Context()).ID, "share.delete", f.Name+" ("+strconv.FormatInt(id, 10)+")", err == nil, detail)
	if err != nil {
		s.shareError(w, r, err)
		return
	}
	s.FileIndex.Touch()
	w.WriteHeader(http.StatusNoContent)
}

// sharesPermissions sets several people's permissions on one folder:
// {"permissions": {"alex": "rw", "sam": "none"}}.
func (s *Server) sharesPermissions(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Permissions map[string]string `json:"permissions"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if _, err := s.Spaces.Folder(id); err != nil {
		s.shareError(w, r, err)
		return
	}
	known, err := s.knownPeople(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for name, perm := range req.Permissions {
		p, err := spaces.ParsePerm(perm)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		canonical, ok := known[strings.ToLower(name)]
		if !ok {
			writeError(w, http.StatusBadRequest, "bad_request", "No such user: "+name)
			return
		}
		err = s.Spaces.SetPermission(r.Context(), id, canonical, p)
		s.audit(r, userFrom(r.Context()).ID, "share.permission", strconv.FormatInt(id, 10)+" "+canonical+" → "+string(p), err == nil, errText(err))
		if err != nil {
			s.shareError(w, r, err)
			return
		}
	}
	f, err := s.Spaces.Folder(id)
	if err != nil {
		s.shareError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// knownPeople maps lower-case user names to their stored spelling.
func (s *Server) knownPeople(ctx context.Context) (map[string]string, error) {
	people, err := s.Spaces.People(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, p := range people {
		out[strings.ToLower(p.Name)] = p.Name
	}
	return out, nil
}

func (s *Server) sharesSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AdminsSeeHomes *bool `json:"admins_see_homes"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.AdminsSeeHomes == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "nothing to change")
		return
	}
	err := s.Spaces.SetAdminsSeeHomes(r.Context(), *req.AdminsSeeHomes)
	s.audit(r, userFrom(r.Context()).ID, "share.admin_homes", onOffWord(*req.AdminsSeeHomes), err == nil, errText(err))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- one person's spaces (admin, Users & Groups) ----------

type personFolder struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Perm     string `json:"perm"` // rw | ro | none
	ReadOnly bool   `json:"read_only"`
}

type personApp struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Allowed  bool   `json:"allowed"`
	Everyone bool   `json:"everyone"`
}

func (s *Server) person(w http.ResponseWriter, r *http.Request) (spaces.Person, bool) {
	people, err := s.Spaces.People(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return spaces.Person{}, false
	}
	for _, p := range people {
		if strings.EqualFold(p.Name, r.PathValue("name")) {
			return p, true
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "No such user")
	return spaces.Person{}, false
}

func (s *Server) userSpaces(w http.ResponseWriter, r *http.Request) {
	p, ok := s.person(w, r)
	if !ok {
		return
	}
	perms := s.Spaces.PermissionsOf(p.Name)
	folders := []personFolder{}
	for _, f := range s.Spaces.Folders() {
		perm := perms[f.ID]
		if perm == "" {
			perm = string(spaces.None)
		}
		if p.Admin {
			perm = string(spaces.ReadWrite)
		}
		folders = append(folders, personFolder{ID: f.ID, Name: f.Name, Perm: perm, ReadOnly: f.ReadOnly})
	}
	apps, err := s.installedApps(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	rules, err := s.Spaces.AppRules(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	appList := []personApp{}
	for _, a := range apps {
		rule := rules[a.ID]
		appList = append(appList, personApp{ID: a.ID, Name: a.Name, Allowed: spaces.AppAllowed(rule, spaces.Viewer{Name: p.Name, Admin: p.Admin}), Everyone: rule == nil || rule.Everyone})
	}
	writeJSON(w, http.StatusOK, map[string]any{"access": p.Access, "admin": p.Admin, "home": s.Spaces.HomePath(p.Name) != "", "folders": folders, "apps": appList})
}

func (s *Server) userFolderPerm(w http.ResponseWriter, r *http.Request) {
	p, ok := s.person(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Perm string `json:"perm"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	perm, err := spaces.ParsePerm(req.Perm)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	err = s.Spaces.SetPermission(r.Context(), id, p.Name, perm)
	s.audit(r, userFrom(r.Context()).ID, "share.permission", strconv.FormatInt(id, 10)+" "+p.Name+" → "+string(perm), err == nil, errText(err))
	if err != nil {
		s.shareError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) userAppAccess(w http.ResponseWriter, r *http.Request) {
	p, ok := s.person(w, r)
	if !ok {
		return
	}
	appID := r.PathValue("app")
	if _, err := s.AppStore.App(appID); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "No such app")
		return
	}
	var req struct {
		Allowed bool `json:"allowed"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	err := s.Spaces.SetAppAllowed(r.Context(), appID, p.Name, req.Allowed)
	s.audit(r, userFrom(r.Context()).ID, "app.access", appID+" "+p.Name+" → "+map[bool]string{true: "allowed", false: "not allowed"}[req.Allowed], err == nil, errText(err))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- app access (admin, App Store) ----------

func (s *Server) appAccessList(w http.ResponseWriter, r *http.Request) {
	rules, err := s.Spaces.AppRules(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

func (s *Server) appAccessSet(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")
	if _, err := s.AppStore.App(appID); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "No such app")
		return
	}
	var req store.AppAccess
	if !decodeJSON(w, r, &req) {
		return
	}
	known, err := s.knownPeople(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	users := []string{}
	if !req.Everyone {
		for _, u := range req.Users {
			name, ok := known[strings.ToLower(u)]
			if !ok {
				writeError(w, http.StatusBadRequest, "bad_request", "No such user: "+u)
				return
			}
			users = append(users, name)
		}
	}
	err = s.Spaces.SetAppAccess(r.Context(), appID, store.AppAccess{Everyone: req.Everyone, Users: users})
	detail := "everyone"
	if !req.Everyone {
		detail = "only " + strings.Join(users, ", ")
		if len(users) == 0 {
			detail = "administrators only"
		}
	}
	s.audit(r, userFrom(r.Context()).ID, "app.access", appID, err == nil, detail+" "+errText(err))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- personal preferences ----------

const maxPrefsBytes = 32 << 10

func (s *Server) prefsGet(w http.ResponseWriter, r *http.Request) {
	data, at, err := s.Store.UserPrefs(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	var prefs json.RawMessage
	if data != "" {
		prefs = json.RawMessage(data)
	}
	out := map[string]any{"prefs": prefs}
	if !at.IsZero() {
		out["updated_at"] = at
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) prefsPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prefs json.RawMessage `json:"prefs"`
	}
	if !decodeJSONLimit(w, r, &req, maxPrefsBytes+1024) {
		return
	}
	raw := bytes.TrimSpace(req.Prefs)
	var obj map[string]any
	if len(raw) == 0 || len(raw) > maxPrefsBytes || raw[0] != '{' || json.Unmarshal(raw, &obj) != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "prefs must be a JSON object under 32 KB")
		return
	}
	if err := s.Store.SetUserPrefs(r.Context(), userFrom(r.Context()).ID, string(raw)); err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
