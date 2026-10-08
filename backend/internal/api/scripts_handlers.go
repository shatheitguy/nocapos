package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"alfaos/alfad/internal/scripts"
	"alfaos/alfad/internal/store"
)

// Quick Script Launcher: saved scripts an admin runs on the host with one
// click. Running a script is a host shell, so every route is admin-only and
// every change and run is audited.

const maxScriptBody = 64 << 10

type scriptRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Confirm     bool   `json:"confirm"`
}

func (req *scriptRequest) validate() string {
	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)
	switch {
	case req.Name == "" || utf8.RuneCountInString(req.Name) > 60:
		return "Name must be 1–60 characters."
	case utf8.RuneCountInString(req.Description) > 200:
		return "Description must be at most 200 characters."
	case strings.TrimSpace(req.Body) == "":
		return "The script is empty."
	case len(req.Body) > maxScriptBody:
		return "The script is larger than 64 KB."
	}
	return ""
}

func (s *Server) scriptsList(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListScripts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not list scripts")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"host": s.Scripts.Info(), "scripts": list})
}

func (s *Server) scriptsCreate(w http.ResponseWriter, r *http.Request) {
	var req scriptRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if msg := req.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, "bad_request", msg)
		return
	}
	sc := &store.Script{Name: req.Name, Description: req.Description, Body: req.Body, Confirm: req.Confirm}
	err := s.Store.CreateScript(r.Context(), sc)
	s.audit(r, userFrom(r.Context()).ID, "script.create", req.Name, err == nil, errText(err))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not save the script")
		return
	}
	writeJSON(w, http.StatusCreated, sc)
}

func (s *Server) scriptsUpdate(w http.ResponseWriter, r *http.Request) {
	var req scriptRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if msg := req.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, "bad_request", msg)
		return
	}
	sc := &store.Script{ID: r.PathValue("id"), Name: req.Name, Description: req.Description, Body: req.Body, Confirm: req.Confirm}
	err := s.Store.UpdateScript(r.Context(), sc)
	s.audit(r, userFrom(r.Context()).ID, "script.update", req.Name, err == nil, errText(err))
	if !s.scriptStoreErr(w, err) {
		return
	}
	updated, err := s.Store.GetScript(r.Context(), sc.ID)
	if !s.scriptStoreErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) scriptsDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	target := id
	if sc, err := s.Store.GetScript(r.Context(), id); err == nil {
		target = sc.Name
	}
	err := s.Store.DeleteScript(r.Context(), id)
	s.audit(r, userFrom(r.Context()).ID, "script.delete", target, err == nil, errText(err))
	if !s.scriptStoreErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) scriptsRun(w http.ResponseWriter, r *http.Request) {
	sc, err := s.Store.GetScript(r.Context(), r.PathValue("id"))
	if !s.scriptStoreErr(w, err) {
		return
	}
	st := s.Store
	runID, err := s.Scripts.Start(sc.ID, sc.Name, sc.Body, func(exit int, at time.Time) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = st.SetScriptResult(ctx, sc.ID, at, exit)
	})
	s.audit(r, userFrom(r.Context()).ID, "script.run", sc.Name, err == nil, errText(err))
	if errors.Is(err, scripts.ErrDisabled) {
		writeError(w, http.StatusForbidden, "disabled", "Running scripts on the host is turned off (ALFA_ALLOW_HOST_TERMINAL).")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"run_id": runID})
}

func (s *Server) scriptsRunGet(w http.ResponseWriter, r *http.Request) {
	run, ok := s.Scripts.Get(r.PathValue("run"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "That run is no longer available.")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) scriptsRunCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("run")
	run, ok := s.Scripts.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "That run is no longer available.")
		return
	}
	s.Scripts.Cancel(id)
	s.audit(r, userFrom(r.Context()).ID, "script.cancel", run.Name, true, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
}

// scriptStoreErr writes the error response for a store error; true means ok.
func (s *Server) scriptStoreErr(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such script.")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "script store error")
	}
	return false
}
