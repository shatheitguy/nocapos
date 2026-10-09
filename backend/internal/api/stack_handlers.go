package api

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"alfaos/alfad/internal/docker"
	"alfaos/alfad/internal/stacks"
)

// Stacks: Compose files kept by NoCapOS and deployed with the engine's own
// Compose tool. Projects started elsewhere are listed too (read-only).

type stackSummary struct {
	Name       string             `json:"name"`
	Managed    bool               `json:"managed"` // its files live in NoCapOS
	Updated    time.Time          `json:"updated,omitzero"`
	Op         *stacks.Op         `json:"op,omitempty"`
	Containers []docker.Container `json:"containers"`
}

type composeInfo struct {
	Installed  bool   `json:"installed"`
	Version    string `json:"version,omitempty"`
	CanInstall bool   `json:"can_install"`
}

func (s *Server) composeInfo(r *http.Request) composeInfo {
	v, err := s.Stacks.ComposeVersion(r.Context())
	return composeInfo{Installed: err == nil, Version: v, CanInstall: err != nil && stacks.CanInstall()}
}

// stackContainers groups all containers by Compose project.
func (s *Server) stackContainers(r *http.Request) (map[string][]docker.Container, error) {
	list, err := s.Docker.ListContainers(r.Context(), true)
	if err != nil {
		return nil, err
	}
	by := map[string][]docker.Container{}
	for _, c := range list {
		if c.Project != "" {
			by[c.Project] = append(by[c.Project], c)
		}
	}
	return by, nil
}

func (s *Server) stackError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, stacks.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, stacks.ErrBusy), errors.Is(err, stacks.ErrExists):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, stacks.ErrNoCompose):
		writeError(w, http.StatusServiceUnavailable, "no_compose", err.Error())
	default:
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	}
}

func (s *Server) listStacks(w http.ResponseWriter, r *http.Request) {
	saved, err := s.Stacks.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not read stacks")
		return
	}
	by, err := s.stackContainers(r)
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	out := []stackSummary{}
	for _, st := range saved {
		cs := by[st.Name]
		if cs == nil {
			cs = []docker.Container{}
		}
		out = append(out, stackSummary{Name: st.Name, Managed: true, Updated: st.Updated, Op: st.Op, Containers: cs})
		delete(by, st.Name)
	}
	for name, cs := range by {
		out = append(out, stackSummary{Name: name, Containers: cs})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"compose": s.composeInfo(r), "stacks": out})
}

type stackBody struct {
	Name    string `json:"name,omitempty"`
	Compose string `json:"compose"`
	Env     string `json:"env"`
	Deploy  bool   `json:"deploy"`
}

const maxStackBytes = 1 << 20

func (s *Server) createStack(w http.ResponseWriter, r *http.Request) {
	var b stackBody
	if !decodeJSONLimit(w, r, &b, maxStackBytes) {
		return
	}
	s.saveStack(w, r, b.Name, b, true)
}

func (s *Server) updateStack(w http.ResponseWriter, r *http.Request) {
	var b stackBody
	if !decodeJSONLimit(w, r, &b, maxStackBytes) {
		return
	}
	s.saveStack(w, r, r.PathValue("name"), b, false)
}

func (s *Server) saveStack(w http.ResponseWriter, r *http.Request, name string, b stackBody, create bool) {
	uid := userFrom(r.Context()).ID
	action := "stack.update"
	if create {
		action = "stack.create"
		// Don't take over a project started elsewhere.
		if by, err := s.stackContainers(r); err == nil && len(by[name]) > 0 {
			writeError(w, http.StatusConflict, "conflict", "containers from another stack already use this name; pick another")
			return
		}
	}
	err := s.Stacks.Save(r.Context(), name, b.Compose, b.Env, create)
	s.audit(r, uid, action, name, err == nil, errText(err))
	if err != nil {
		s.stackError(w, err)
		return
	}
	if b.Deploy {
		err = s.Stacks.Run(r.Context(), name, "up")
		s.audit(r, uid, "stack.up", name, err == nil, errText(err))
		if err != nil {
			s.stackError(w, err)
			return
		}
	}
	status := http.StatusOK
	if create {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]string{"name": name})
}

func (s *Server) getStack(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !stacks.ValidName(name) {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid stack name")
		return
	}
	compose, env, err := s.Stacks.Files(name)
	if err != nil {
		s.stackError(w, err)
		return
	}
	by, err := s.stackContainers(r)
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	cs := by[name]
	if cs == nil {
		cs = []docker.Container{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name": name, "compose": compose, "env": env, "dir": s.Stacks.Dir(name),
		"op": s.Stacks.Op(name), "containers": cs,
	})
}

func (s *Server) stackAction(w http.ResponseWriter, r *http.Request) {
	name, action := r.PathValue("name"), r.PathValue("action")
	if !stacks.ValidName(name) || !stacks.ValidAction(action) {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid stack or action")
		return
	}
	err := s.Stacks.Run(r.Context(), name, action)
	s.audit(r, userFrom(r.Context()).ID, "stack."+action, name, err == nil, errText(err))
	if err != nil {
		s.stackError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) stackLogs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !stacks.ValidName(name) {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid stack name")
		return
	}
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	if tail <= 0 || tail > 5000 {
		tail = 300
	}
	out, err := s.Stacks.Logs(r.Context(), name, tail)
	if err != nil {
		s.stackError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"logs": out})
}

func (s *Server) deleteStack(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.Stacks.Exists(name) {
		writeError(w, http.StatusNotFound, "not_found", stacks.ErrNotFound.Error())
		return
	}
	volumes := r.URL.Query().Get("volumes") == "1"
	uid := userFrom(r.Context()).ID
	if by, err := s.stackContainers(r); err == nil && len(by[name]) > 0 {
		if err := s.Stacks.Down(r.Context(), name, volumes); err != nil {
			s.audit(r, uid, "stack.delete", name, false, errText(err))
			s.stackError(w, err)
			return
		}
	}
	err := s.Stacks.Delete(name)
	s.audit(r, uid, "stack.delete", name, err == nil, errText(err))
	if err != nil {
		s.stackError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) installCompose(w http.ResponseWriter, r *http.Request) {
	err := s.Stacks.InstallCompose(r.Context())
	s.audit(r, userFrom(r.Context()).ID, "compose.install", "", err == nil, errText(err))
	if err != nil {
		writeError(w, http.StatusBadRequest, "install_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.composeInfo(r))
}
