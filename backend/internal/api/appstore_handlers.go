package api

import (
	"errors"
	"net/http"

	"alfaos/alfad/internal/appstore"
	"alfaos/alfad/internal/docker"
)

// App Store: one-click installs of catalog apps as containers. Installing an
// app runs software on the host, so every route is admin-only and audited.

type storeApp struct {
	appstore.App
	Installed *appstore.Installed `json:"installed,omitempty"`
}

func (s *Server) appstoreList(w http.ResponseWriter, r *http.Request) {
	status, err := s.AppStore.Status(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	apps := make([]storeApp, 0, len(s.AppStore.Catalog()))
	for _, a := range s.AppStore.Catalog() {
		apps = append(apps, storeApp{App: a, Installed: status[a.ID]})
	}
	dockerUp := s.Docker.Ping(r.Context()) == nil
	writeJSON(w, http.StatusOK, map[string]any{"apps": apps, "docker": dockerUp})
}

func (s *Server) appstoreAction(w http.ResponseWriter, r *http.Request) {
	id, action := r.PathValue("id"), r.PathValue("action")
	u := userFrom(r.Context())
	audit := func(err error) {
		s.audit(r, u.ID, "app."+action, id, err == nil, errText(err))
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
		jobID, err = s.AppStore.Install(ctx, id, audit)
	case "update":
		jobID, err = s.AppStore.Update(ctx, id, audit)
	case "uninstall":
		var req struct {
			DeleteData bool `json:"delete_data"`
		}
		if r.ContentLength > 0 && !decodeJSON(w, r, &req) {
			return
		}
		jobID, err = s.AppStore.Uninstall(ctx, id, req.DeleteData, audit)
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
