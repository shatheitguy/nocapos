package api

import (
	"net/http"
	"regexp"
	"time"
)

// Container IDs (hex) and names ([a-zA-Z0-9][a-zA-Z0-9_.-]+).
var containerRefRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

func validContainerRef(ref string) bool { return containerRefRe.MatchString(ref) }

func (s *Server) listContainers(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("all") != "false"
	list, err := s.Docker.ListContainers(r.Context(), all)
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) inspectContainer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validContainerRef(id) {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid container id")
		return
	}
	d, err := s.Docker.InspectContainer(r.Context(), id)
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

const stopTimeout = 10 * time.Second

func (s *Server) containerAction(w http.ResponseWriter, r *http.Request) {
	id, action := r.PathValue("id"), r.PathValue("action")
	if !validContainerRef(id) {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid container id")
		return
	}
	if action != "start" && action != "stop" && action != "restart" {
		writeError(w, http.StatusNotFound, "not_found", "unknown action")
		return
	}
	u := userFrom(r.Context())

	d, err := s.Docker.InspectContainer(r.Context(), id)
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	// Stopping alfad/Traefik from the UI would lock the user out of the OS.
	if d.System && action != "start" {
		s.audit(r, u.ID, "container."+action, d.Name, false, "system container is protected")
		writeError(w, http.StatusForbidden, "protected", "system containers cannot be stopped from the API")
		return
	}

	switch action {
	case "start":
		err = s.Docker.StartContainer(r.Context(), d.ID)
	case "stop":
		err = s.Docker.StopContainer(r.Context(), d.ID, stopTimeout)
	case "restart":
		err = s.Docker.RestartContainer(r.Context(), d.ID, stopTimeout)
	}
	s.audit(r, u.ID, "container."+action, d.Name, err == nil, "")
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listImages(w http.ResponseWriter, r *http.Request) {
	imgs, err := s.Docker.ListImages(r.Context())
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, imgs)
}
