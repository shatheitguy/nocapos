package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"alfaos/alfad/internal/mcp"
	"alfaos/alfad/internal/store"
)

// Settings → AI Agents: the NoCap MCP switch (administrators), each
// person's agent tokens, their per-tool lists, recent activity, and the
// approval cards' answers.

func (s *Server) agentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/mcp", s.mcpEndpoint)
	const p = "/api/v1/agents"
	mux.Handle("GET "+p, s.authed(s.agentsOverview))
	mux.Handle("PUT "+p+"/settings", s.admin(s.agentsSettingsSet))
	mux.Handle("POST "+p+"/tokens", s.authed(s.agentTokenCreate))
	mux.Handle("PATCH "+p+"/tokens/{id}", s.authed(s.agentTokenUpdate))
	mux.Handle("DELETE "+p+"/tokens/{id}", s.authed(s.agentTokenRevoke))
	mux.Handle("GET "+p+"/approvals", s.authed(s.agentApprovals))
	mux.Handle("POST "+p+"/approvals/{id}", s.authed(s.agentApprovalDecide))
}

type agentToolJSON struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Action      bool   `json:"action"`
	Admin       bool   `json:"admin"`
}

type agentTokenJSON struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Scope       string     `json:"scope"`
	Tools       []string   `json:"tools"` // null = every tool its scope allows
	AlwaysAllow []string   `json:"always_allow"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
}

func toAgentTokenJSON(t store.AgentToken) agentTokenJSON {
	out := agentTokenJSON{ID: t.ID, Name: t.Name, Scope: t.Scope, Tools: t.Tools, AlwaysAllow: t.AlwaysAllow, CreatedAt: t.CreatedAt}
	if out.AlwaysAllow == nil {
		out.AlwaysAllow = []string{}
	}
	if !t.LastUsedAt.IsZero() {
		at := t.LastUsedAt
		out.LastUsedAt = &at
	}
	return out
}

// localMCPURL is the address agents on this machine use: the loopback
// desktop listener when there is one (plain HTTP, no certificate warning),
// else this server's own port on 127.0.0.1.
func (s *Server) localMCPURL() string {
	if s.Config.DesktopAddr != "" && s.Config.TLSEnabled() {
		return "http://" + s.Config.DesktopAddr + "/mcp"
	}
	port := "8080"
	if _, p, err := net.SplitHostPort(s.Config.ListenAddr); err == nil && p != "" {
		port = p
	}
	scheme := "http"
	if s.Config.TLSEnabled() {
		scheme = "https"
	}
	return scheme + "://127.0.0.1:" + port + "/mcp"
}

func (s *Server) agentsOverview(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	ctx := r.Context()
	enabled, loopbackOnly := s.agentSettings(ctx)
	tokens, err := s.Store.ListAgentTokens(ctx, u.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	activity, err := s.Store.ListAgentActivity(ctx, u.ID, 50)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	tools := []agentToolJSON{}
	for _, t := range agentTools {
		if t.admin && u.Role != store.RoleAdmin {
			continue
		}
		tools = append(tools, agentToolJSON{Name: t.Name, Title: t.Title, Description: t.Description, Action: t.action, Admin: t.admin})
	}
	toks := make([]agentTokenJSON, 0, len(tokens))
	for _, t := range tokens {
		toks = append(toks, toAgentTokenJSON(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": enabled, "loopback_only": loopbackOnly, "can_manage": u.Role == store.RoleAdmin,
		"local_url": s.localMCPURL(), "network_url": requestOrigin(r) + "/mcp",
		"tools": tools, "tokens": toks, "activity": activity,
		"pending": s.agentBroker().Pending(u.ID),
	})
}

type agentSettingsRequest struct {
	Enabled      *bool `json:"enabled"`
	LoopbackOnly *bool `json:"loopback_only"`
}

func boolSetting(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func (s *Server) agentsSettingsSet(w http.ResponseWriter, r *http.Request) {
	var req agentSettingsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	u := userFrom(r.Context())
	if req.Enabled != nil {
		if err := s.Store.SetSetting(r.Context(), agentsEnabledKey, boolSetting(*req.Enabled)); err != nil {
			s.internalError(w, r, err)
			return
		}
		s.audit(r, u.ID, "agents.enabled", boolSetting(*req.Enabled), true, "")
	}
	if req.LoopbackOnly != nil {
		if err := s.Store.SetSetting(r.Context(), agentsLoopbackKey, boolSetting(*req.LoopbackOnly)); err != nil {
			s.internalError(w, r, err)
			return
		}
		s.audit(r, u.ID, "agents.loopback_only", boolSetting(*req.LoopbackOnly), true, "")
	}
	enabled, loopbackOnly := s.agentSettings(r.Context())
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": enabled, "loopback_only": loopbackOnly})
}

type agentTokenRequest struct {
	Name  string `json:"name"`
	Scope string `json:"scope"`
}

func validAgentName(n string) bool {
	if n == "" || utf8.RuneCountInString(n) > 64 {
		return false
	}
	for _, r := range n {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func (s *Server) agentTokenCreate(w http.ResponseWriter, r *http.Request) {
	var req agentTokenRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if !validAgentName(req.Name) {
		writeError(w, http.StatusBadRequest, "validation", "Name your agent (up to 64 characters).")
		return
	}
	if req.Scope != store.AgentScopeRead && req.Scope != store.AgentScopeAct {
		writeError(w, http.StatusBadRequest, "validation", "scope must be read or act")
		return
	}
	u := userFrom(r.Context())
	raw, hash := newAgentToken()
	t := &store.AgentToken{ID: store.NewID(), UserID: u.ID, Name: req.Name, Scope: req.Scope, CreatedAt: time.Now()}
	if err := s.Store.CreateAgentToken(r.Context(), t, hash); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, u.ID, "agents.token_create", t.Name, true, t.Scope)
	writeJSON(w, http.StatusCreated, map[string]any{"token": raw, "agent": toAgentTokenJSON(*t)})
}

type agentToolsRequest struct {
	Tools       *[]string `json:"tools"` // null = every tool
	AlwaysAllow []string  `json:"always_allow"`
	AllTools    bool      `json:"all_tools"`
}

func (s *Server) agentTokenUpdate(w http.ResponseWriter, r *http.Request) {
	var req agentToolsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	u := userFrom(r.Context())
	cur, err := s.Store.AgentTokenByID(r.Context(), r.PathValue("id"), u.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "That agent token isn't around anymore.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	tools := cur.Tools
	switch {
	case req.AllTools:
		tools = nil
	case req.Tools != nil:
		tools = []string{}
		for _, n := range *req.Tools {
			if findAgentTool(n) == nil {
				writeError(w, http.StatusBadRequest, "validation", "unknown tool "+truncate(n, 40))
				return
			}
			if !slices.Contains(tools, n) {
				tools = append(tools, n)
			}
		}
	}
	always := cur.AlwaysAllow
	if req.AlwaysAllow != nil {
		always = []string{}
		for _, n := range req.AlwaysAllow {
			if t := findAgentTool(n); t == nil || !t.action {
				writeError(w, http.StatusBadRequest, "validation", "only action tools can be always allowed")
				return
			}
			if !slices.Contains(always, n) {
				always = append(always, n)
			}
		}
	}
	if err := s.Store.SetAgentTokenTools(r.Context(), cur.ID, u.ID, tools, always); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, u.ID, "agents.token_tools", cur.Name, true, "")
	cur.Tools, cur.AlwaysAllow = tools, always
	writeJSON(w, http.StatusOK, toAgentTokenJSON(*cur))
}

func (s *Server) agentTokenRevoke(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	err := s.Store.RevokeAgentToken(r.Context(), r.PathValue("id"), u.ID, time.Now())
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "That agent token isn't around anymore.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, u.ID, "agents.token_revoke", r.PathValue("id"), true, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) agentApprovals(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.agentBroker().Pending(userFrom(r.Context()).ID))
}

func (s *Server) agentApprovalDecide(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Decision mcp.Decision `json:"decision"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !mcp.ValidDecision(req.Decision) {
		writeError(w, http.StatusBadRequest, "validation", "decision must be once, always or deny")
		return
	}
	u := userFrom(r.Context())
	err := s.agentBroker().Decide(r.PathValue("id"), u.ID, req.Decision)
	if errors.Is(err, mcp.ErrNoApproval) {
		writeError(w, http.StatusNotFound, "not_found", "Too late — that request isn't waiting anymore.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, u.ID, "agents.approval", r.PathValue("id"), req.Decision != mcp.Deny, string(req.Decision))
	w.WriteHeader(http.StatusNoContent)
}

// agentApprovalsSource feeds the agents.approvals topic: a person's waiting
// approvals now and whenever they change.
func (s *Server) agentApprovalsSource(userID string) func(ctx context.Context, emit func(any)) error {
	return func(ctx context.Context, emit func(any)) error {
		b := s.agentBroker()
		ch, cancel := b.Watch(userID)
		defer cancel()
		emit(b.Pending(userID))
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ch:
				emit(b.Pending(userID))
			}
		}
	}
}
