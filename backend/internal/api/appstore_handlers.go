package api

import (
	"context"
	"errors"
	"net/http"

	"alfaos/alfad/internal/appstore"
	"alfaos/alfad/internal/docker"
	"alfaos/alfad/internal/spaces"
	"alfaos/alfad/internal/store"
)

// App Store: one-click installs of catalog apps as containers. Installing an
// app runs software on the host, so changes are admin-only and audited.
// Everyone signed in can list the installed apps they may use (to open them
// from the home screen); that list is filtered here, on the server.

type storeApp struct {
	appstore.App
	Installed *appstore.Installed `json:"installed,omitempty"`
	Job       *appstore.Job       `json:"job,omitempty"`    // running, or ended in the last few minutes
	Access    *store.AppAccess    `json:"access,omitempty"` // admins: who can use it (nil = everyone)
}

// installedApps lists the catalog apps that are installed.
func (s *Server) installedApps(ctx context.Context) ([]appstore.App, error) {
	status, err := s.AppStore.Status(ctx)
	if err != nil {
		return nil, err
	}
	var out []appstore.App
	for _, a := range s.AppStore.Catalog() {
		if status[a.ID] != nil {
			out = append(out, a)
		}
	}
	return out, nil
}

// visibleStoreApps is the non-admin view: installed apps this person may
// use, without the app's own admin credentials or container details.
func visibleStoreApps(catalog []appstore.App, status map[string]*appstore.Installed, rules map[string]*store.AppAccess, v spaces.Viewer) []storeApp {
	apps := []storeApp{}
	for _, a := range catalog {
		in := status[a.ID]
		if in == nil || !spaces.AppAllowed(rules[a.ID], v) {
			continue
		}
		c := *in
		c.Credentials, c.Containers = nil, nil
		apps = append(apps, storeApp{App: a, Installed: &c})
	}
	return apps
}

func (s *Server) appstoreList(w http.ResponseWriter, r *http.Request) {
	status, err := s.AppStore.Status(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.fillAppWebURLs(r.Context(), status)
	var rules map[string]*store.AppAccess
	if s.Spaces != nil {
		if rules, err = s.Spaces.AppRules(r.Context()); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	if v := s.viewer(r); !v.Admin {
		writeJSON(w, http.StatusOK, map[string]any{"apps": visibleStoreApps(s.AppStore.Catalog(), status, rules, v), "docker": true})
		return
	}
	jobs := s.AppStore.Jobs()
	apps := make([]storeApp, 0, len(s.AppStore.Catalog()))
	for _, a := range s.AppStore.Catalog() {
		sa := storeApp{App: a, Installed: status[a.ID], Access: rules[a.ID]}
		if j, ok := jobs[a.ID]; ok {
			sa.Job = &j
		}
		apps = append(apps, sa)
	}
	dockerUp := s.Docker.Ping(r.Context()) == nil
	writeJSON(w, http.StatusOK, map[string]any{"apps": apps, "docker": dockerUp})
}

func (s *Server) appstoreAction(w http.ResponseWriter, r *http.Request) {
	id, action := r.PathValue("id"), r.PathValue("action")
	u := userFrom(r.Context())
	// Install/update/uninstall finish after this request: audit with a detached copy.
	ar := r.Clone(context.WithoutCancel(r.Context()))
	// NoCapOS stops and recreates the app's containers now: no crash notifications.
	s.crashes.Expect("app:" + id)
	audit := func(err error) {
		s.audit(ar, u.ID, "app."+action, id, err == nil, errText(err))
	}
	// done runs when an install, update or uninstall job ends.
	done := func(err error) {
		audit(err)
		s.crashes.Expect("app:" + id)
		s.notifyAppJob(id, action, err)
	}
	ctx := r.Context()
	if err := s.Docker.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "docker_down", "Docker is not running, so apps can't be changed right now.")
		return
	}
	var (
		jobID string
		err   error
	)
	switch action {
	case "install":
		jobID, err = s.AppStore.Install(ctx, id, done)
	case "update":
		jobID, err = s.AppStore.Update(ctx, id, done)
	case "uninstall":
		var req struct {
			DeleteData bool `json:"delete_data"`
		}
		if r.ContentLength > 0 && !decodeJSON(w, r, &req) {
			return
		}
		jobID, err = s.AppStore.Uninstall(ctx, id, req.DeleteData, done)
	case "start", "stop", "restart":
		err = s.AppStore.Control(ctx, id, action)
		audit(err)
		if err == nil {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			return
		}
	default:
		writeError(w, http.StatusNotFound, "not_found", "unknown action")
		return
	}
	if err != nil {
		if jobID == "" && action != "start" && action != "stop" && action != "restart" {
			audit(err)
		}
		s.appstoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job": jobID})
}

func (s *Server) appstoreJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.AppStore.Job(r.PathValue("job"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such job")
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) appstoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, appstore.ErrUnknownApp):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, appstore.ErrBusy), errors.Is(err, appstore.ErrInstalled):
		writeError(w, http.StatusConflict, "busy", err.Error())
	case errors.Is(err, appstore.ErrNotInstall):
		writeError(w, http.StatusConflict, "not_installed", err.Error())
	case errors.Is(err, docker.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "docker_down", "Docker is not running, so apps can't be changed right now.")
	default:
		s.internalError(w, r, err)
	}
}
