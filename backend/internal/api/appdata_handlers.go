package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"alfaos/alfad/internal/appdata"
	"alfaos/alfad/internal/backup"
	"alfaos/alfad/internal/docker"
	"alfaos/alfad/internal/files"
)

// The App data folder (one folder per app): its location, moving apps'
// Docker volumes into it, moving it, cleaning up old volumes, and its SMB
// share. Administrators only.

// setupAppData connects the App data folder to Files, the App Store,
// stacks, sharing and backups.
func (s *Server) setupAppData() {
	if s.Docker == nil || s.Files == nil {
		return
	}
	fallback := appdata.DefaultRootFor(s.Config.AppDataDir, s.Config.DataDir)
	mv := appdata.NewMover(s.Docker, s.Store, s.Files.Protected, fallback)
	if s.Stacks != nil {
		mv.Stacks = s.Stacks
	}
	if s.AppStore != nil {
		mv.AppStore = s.AppStore
		s.AppStore.SetAppData(mv.Root)
	}
	s.appMover = mv
	s.Files.AddResolver(s.resolveAppDataRoot)
	if s.NetDrives != nil {
		s.NetDrives.UseAppData(func(ctx context.Context) string {
			if v, _ := s.Store.Setting(ctx, appdata.KeyShare); v != "1" {
				return ""
			}
			return mv.Root(ctx)
		})
	}
	if s.Backup != nil {
		s.Backup.UseAppData(backupAppData(mv))
	}
}

// resolveAppDataRoot opens the whole App data folder in Files ("appdata";
// only administrators reach locations that aren't shared folders).
func (s *Server) resolveAppDataRoot(id string) (*files.Root, bool) {
	if id != appdata.RootID || s.appMover == nil {
		return nil, false
	}
	ctx := context.Background()
	root := s.appMover.Root(ctx)
	if err := appdata.ValidateRoot(root, s.Files.Protected(), s.appMover.DockerRoot(ctx)); err != nil {
		return nil, false
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, false
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	return &files.Root{ID: appdata.RootID, Name: "App data", Path: root}, true
}

// appFolderView is an app's folder in App data, as Files shows it.
type appFolderView struct {
	appdata.AppFolder
	Title    string    `json:"title"` // the app's name
	Icon     string    `json:"icon,omitempty"`
	Logo     string    `json:"logo,omitempty"`
	LogoMono bool      `json:"logo_mono,omitempty"`
	Tile     [2]string `json:"tile,omitempty"`
}

// appDataOverview: where App data is, each app's folder, the apps still on
// Docker volumes, the running move and the share.
func (s *Server) appDataOverview(w http.ResponseWriter, r *http.Request) {
	if s.appMover == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	ctx := r.Context()
	folders, err := s.appMover.Folders(ctx)
	if err != nil {
		folders = []appdata.AppFolder{}
	}
	views := make([]appFolderView, 0, len(folders))
	for _, f := range folders {
		v := appFolderView{AppFolder: f, Title: f.Name}
		if s.AppStore != nil {
			if a, err := s.AppStore.App(f.Name); err == nil {
				v.Title, v.Icon, v.Logo, v.LogoMono, v.Tile = a.Name, a.Icon, a.Logo, a.LogoMono, a.Tile
			}
		}
		views = append(views, v)
	}
	out := map[string]any{
		"available": true,
		"state":     s.appMover.State(ctx),
		"folders":   views,
		"job":       s.appMover.Job(),
		"share":     s.appDataShareOn(ctx),
	}
	if err := s.Docker.Ping(ctx); err != nil {
		out["docker"] = false
		out["candidates"] = []appdata.Candidate{}
	} else if c, err := s.appMover.Candidates(ctx); err == nil {
		out["docker"] = true
		out["candidates"] = c
	} else {
		out["candidates"] = []appdata.Candidate{}
		out["candidates_error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) appDataShareOn(ctx context.Context) bool {
	v, _ := s.Store.Setting(ctx, appdata.KeyShare)
	return v == "1"
}

func (s *Server) appDataReady(w http.ResponseWriter) bool {
	if s.appMover == nil {
		writeError(w, http.StatusServiceUnavailable, "docker_down", "Docker isn't available on this server.")
		return false
	}
	return true
}

func (s *Server) appDataJobError(w http.ResponseWriter, err error) {
	if errors.Is(err, appdata.ErrBusy) {
		writeError(w, http.StatusConflict, "busy", err.Error())
		return
	}
	writeError(w, http.StatusBadRequest, "bad_request", err.Error())
}

// appDataMove moves one app (or, with all, every app) into App data.
func (s *Server) appDataMove(w http.ResponseWriter, r *http.Request) {
	if !s.appDataReady(w) {
		return
	}
	var req struct {
		App string `json:"app"`
		All bool   `json:"all"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	uid := userFrom(r.Context()).ID
	// Containers are stopped and recreated on purpose while they move.
	if cands, err := s.appMover.Candidates(r.Context()); err == nil {
		for _, c := range cands {
			if req.All || c.App == req.App {
				s.crashes.Expect(append([]string{"app:" + c.App}, c.Containers...)...)
			}
		}
	}
	var (
		j   *appdata.Job
		err error
	)
	if req.All {
		j, err = s.appMover.OrganizeAll(r.Context())
		s.audit(r, uid, "appdata.organize", "all apps", err == nil, errText(err))
	} else {
		j, err = s.appMover.Move(r.Context(), req.App)
		s.audit(r, uid, "appdata.move", req.App, err == nil, errText(err))
	}
	if err != nil {
		s.appDataJobError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, j)
}

func (s *Server) appDataJob(w http.ResponseWriter, r *http.Request) {
	if !s.appDataReady(w) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": s.appMover.Job()})
}

// appDataSetRoot moves the App data folder.
func (s *Server) appDataSetRoot(w http.ResponseWriter, r *http.Request) {
	if !s.appDataReady(w) {
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	j, err := s.appMover.MoveRoot(r.Context(), req.Path)
	s.audit(r, userFrom(r.Context()).ID, "appdata.root", req.Path, err == nil, errText(err))
	if err != nil {
		s.appDataJobError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, j)
}

// appDataOldRoot removes (or keeps and forgets) the copy left by a move.
func (s *Server) appDataOldRoot(w http.ResponseWriter, r *http.Request) {
	if !s.appDataReady(w) {
		return
	}
	var req struct {
		Action string `json:"action"` // remove | keep
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	old := s.appMover.State(ctx).OldRoot
	var err error
	switch req.Action {
	case "remove":
		err = s.appMover.RemoveOldRoot(ctx)
	case "keep":
		err = s.appMover.KeepOldRoot(ctx)
	default:
		err = errors.New("unknown action")
	}
	s.audit(r, userFrom(ctx).ID, "appdata.old_root."+req.Action, old, err == nil, errText(err))
	if err != nil {
		s.appDataJobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.appMover.State(ctx))
}

func (s *Server) appDataOldVolumes(w http.ResponseWriter, r *http.Request) {
	if !s.appDataReady(w) {
		return
	}
	list, err := s.appMover.OldVolumes(r.Context())
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"volumes": list})
}

func (s *Server) appDataRemoveOldVolumes(w http.ResponseWriter, r *http.Request) {
	if !s.appDataReady(w) {
		return
	}
	var req struct {
		Volumes []string `json:"volumes"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	removed, err := s.appMover.RemoveOldVolumes(r.Context(), req.Volumes)
	s.audit(r, userFrom(r.Context()).ID, "appdata.cleanup", strings.Join(req.Volumes, ", "), err == nil, errText(err))
	resp := map[string]any{"removed": removed}
	if err != nil {
		resp["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// appDataShare turns the admin-only "appdata" SMB share on or off.
func (s *Server) appDataShare(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	v := ""
	if req.Enabled {
		v = "1"
	}
	ctx := r.Context()
	if err := s.Store.SetSetting(ctx, appdata.KeyShare, v); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.audit(r, userFrom(ctx).ID, "appdata.share", v, true, "")
	if s.NetDrives != nil {
		if err := s.NetDrives.ApplySamba(ctx); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"enabled": req.Enabled, "warning": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": req.Enabled})
}

// appDataSuggest is the folder offered for new storage:
// <App data>/<container or project>/<name>.
func (s *Server) appDataSuggest(w http.ResponseWriter, r *http.Request) {
	if !s.appDataReady(w) {
		return
	}
	q := r.URL.Query()
	owner := q.Get("owner")
	if strings.TrimSpace(owner) == "" {
		owner = "app"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path": appdata.Suggest(s.appMover.Root(r.Context()), owner, q.Get("name")),
		"root": filepath.ToSlash(s.appMover.Root(r.Context())),
	})
}

// checkBinds checks the folders on the server a container is given. Folders
// it already had (keep) are left alone, so existing setups stay editable.
func (s *Server) checkBinds(ctx context.Context, mounts []docker.SpecMount, keep []docker.SpecMount) error {
	if s.appMover == nil {
		return nil
	}
	had := map[string]bool{}
	for _, m := range keep {
		if m.Type == "bind" {
			had[filepath.Clean(m.Source)] = true
		}
	}
	for _, m := range mounts {
		if m.Type != "bind" || had[filepath.Clean(m.Source)] {
			continue
		}
		if err := s.appMover.CheckBind(ctx, m.Source); err != nil {
			return err
		}
	}
	return nil
}

// checkComposeBinds checks the folders a compose file gives its services
// (ones the stack already had are left alone).
func (s *Server) checkComposeBinds(ctx context.Context, compose, before string) error {
	if s.appMover == nil {
		return nil
	}
	now, err := appdata.ComposeBinds(compose)
	if err != nil {
		return nil // Compose reports a broken file itself
	}
	had := map[string]bool{}
	if before != "" {
		if old, err := appdata.ComposeBinds(before); err == nil {
			for _, p := range old {
				had[p] = true
			}
		}
	}
	for _, p := range now {
		if had[p] {
			continue
		}
		if err := s.appMover.CheckBind(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

// backupAppData lets backups include the App data folder.
func backupAppData(mv *appdata.Mover) backup.AppData {
	return backup.AppData{Root: func(ctx context.Context) string { return mv.Root(ctx) }, StopApps: mv.StopApps}
}
