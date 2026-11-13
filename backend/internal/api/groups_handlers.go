package api

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"alfaos/alfad/internal/store"
)

// Groups of people (Settings → Users & Groups): administrators name a
// group, pick a color and choose who is in it. Every change is audited.

type groupRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Color       string   `json:"color"`
	Members     []string `json:"members"`
}

var groupColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (s *Server) groupsList(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.Groups(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": list})
}

// groupFromRequest checks a create/update body and resolves member names to
// the accounts' own spelling. Names in old (the group's current members) whose
// account has since gone — say, removed with userdel — are dropped quietly so
// the group can still be saved; other unknown names are refused. It writes the
// error itself and returns nil.
func (s *Server) groupFromRequest(w http.ResponseWriter, r *http.Request, old []string) *store.Group {
	var req groupRequest
	if !decodeJSON(w, r, &req) {
		return nil
	}
	g := &store.Group{Name: strings.TrimSpace(req.Name), Description: strings.TrimSpace(req.Description), Color: req.Color}
	if err := store.ValidGroupName(g.Name); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Group names are 1–40 characters.")
		return nil
	}
	if len([]rune(g.Description)) > 200 {
		writeError(w, http.StatusBadRequest, "bad_request", "Keep the description under 200 characters.")
		return nil
	}
	if g.Color != "" && !groupColor.MatchString(g.Color) {
		writeError(w, http.StatusBadRequest, "bad_request", "Colors look like #ff3b30.")
		return nil
	}
	list, err := s.Accounts.List(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return nil
	}
	known := make(map[string]string, len(list))
	for _, a := range list {
		known[strings.ToLower(a.Username)] = a.Username
	}
	stale := make(map[string]bool, len(old))
	for _, m := range old {
		stale[strings.ToLower(m)] = true
	}
	members := make([]string, 0, len(req.Members))
	for _, m := range store.SortedMembers(req.Members) {
		name, ok := known[strings.ToLower(m)]
		if !ok && stale[strings.ToLower(m)] {
			continue
		}
		if !ok {
			writeError(w, http.StatusBadRequest, "unknown_user", "No one called "+m+" here.")
			return nil
		}
		members = append(members, name)
	}
	g.Members = members
	return g
}

func (s *Server) groupsCreate(w http.ResponseWriter, r *http.Request) {
	g := s.groupFromRequest(w, r, nil)
	if g == nil {
		return
	}
	err := s.Store.CreateGroup(r.Context(), g)
	s.audit(r, userFrom(r.Context()).ID, "group.create", g.Name, err == nil, strings.Join(g.Members, ",")+" "+errText(err))
	if !s.groupError(w, r, err) {
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) groupsUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "No such group.")
		return
	}
	var old []string
	if cur, cerr := s.Store.Group(r.Context(), id); cerr == nil {
		old = cur.Members
	}
	g := s.groupFromRequest(w, r, old)
	if g == nil {
		return
	}
	g.ID = id
	err = s.Store.UpdateGroup(r.Context(), g)
	s.audit(r, userFrom(r.Context()).ID, "group.update", g.Name, err == nil, strings.Join(g.Members, ",")+" "+errText(err))
	if !s.groupError(w, r, err) {
		return
	}
	if fresh, err := s.Store.Group(r.Context(), id); err == nil {
		g = fresh
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) groupsDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "No such group.")
		return
	}
	name := r.PathValue("id")
	if g, gerr := s.Store.Group(r.Context(), id); gerr == nil {
		name = g.Name
	}
	err = s.Store.DeleteGroup(r.Context(), id)
	s.audit(r, userFrom(r.Context()).ID, "group.delete", name, err == nil, errText(err))
	if !s.groupError(w, r, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// groupError writes err, if any, and reports whether all went well.
func (s *Server) groupError(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "exists", "There's already a group with that name.")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such group.")
	default:
		s.internalError(w, r, err)
	}
	return false
}
