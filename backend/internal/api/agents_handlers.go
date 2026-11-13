package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"alfaos/alfad/internal/mcp"
	"alfaos/alfad/internal/store"
)

// Settings → AI Agents: the built-in AI Assistant's access, each person's
// agents (tokens or pairing codes, scopes, IP allowlist, expiry, pause),
// their activity, the approval inbox, Stop all agents, and the NoCap MCP
// switches (administrators).

func (s *Server) agentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/mcp", s.mcpEndpoint)
	mux.HandleFunc("/mcp/sse", s.mcpSSE)
	mux.HandleFunc("/mcp/messages", s.mcpSSEMessage)
	mux.HandleFunc("POST /mcp/pair", s.mcpPair)
	const p = "/api/v1/agents"
	mux.Handle("GET "+p, s.authed(s.agentsOverview))
	mux.Handle("PUT "+p+"/settings", s.admin(s.agentsSettingsSet))
	mux.Handle("POST "+p+"/tokens", s.authed(s.agentTokenCreate))
	mux.Handle("PATCH "+p+"/tokens/{id}", s.authed(s.agentTokenUpdate))
	mux.Handle("DELETE "+p+"/tokens/{id}", s.authed(s.agentTokenRevoke))
	mux.Handle("POST "+p+"/tokens/{id}/rotate", s.authed(s.agentTokenRotate))
	mux.Handle("GET "+p+"/activity", s.authed(s.agentActivity))
	mux.Handle("GET "+p+"/approvals", s.authed(s.agentApprovals))
	mux.Handle("POST "+p+"/approvals/{id}", s.authed(s.agentApprovalDecide))
	mux.Handle("POST "+p+"/stop", s.authed(s.agentsStopAll))
	mux.Handle("GET "+p+"/assistant/tools", s.authed(s.assistantTools))
	mux.Handle("POST "+p+"/assistant/call", s.authed(s.assistantCall))
}

type agentToolJSON struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Action      bool   `json:"action"`
	Admin       bool   `json:"admin"`
	Cap         string `json:"capability"`
	Need        string `json:"level"`
}

type agentTokenJSON struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Kind          string     `json:"kind"`
	Scope         string     `json:"scope"`
	Scopes        mcp.Scopes `json:"scopes"`
	Risk          mcp.Risk   `json:"risk"`
	Tools         []string   `json:"tools"` // null = every tool its scopes allow
	AlwaysAllow   []string   `json:"always_allow"`
	IPAllow       []string   `json:"ip_allow"`
	ExpiresAt     *time.Time `json:"expires_at"`
	Expired       bool       `json:"expired"`
	Paused        bool       `json:"paused"`
	Pairing       bool       `json:"pairing"`
	PairExpiresAt *time.Time `json:"pair_expires_at"`
	CreatedAt     time.Time  `json:"created_at"`
	LastUsedAt    *time.Time `json:"last_used_at"`
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func toAgentTokenJSON(t store.AgentToken) agentTokenJSON {
	sc := agentScopes(&t)
	out := agentTokenJSON{
		ID: t.ID, Name: t.Name, Kind: t.Kind, Scope: t.Scope, Scopes: sc, Risk: sc.Risk(),
		Tools: t.Tools, AlwaysAllow: t.AlwaysAllow, IPAllow: t.IPAllow,
		ExpiresAt: timePtr(t.ExpiresAt), Expired: t.Expired(time.Now()), Paused: t.Paused,
		Pairing: t.Pairing, PairExpiresAt: timePtr(t.PairExpiresAt),
		CreatedAt: t.CreatedAt, LastUsedAt: timePtr(t.LastUsedAt),
	}
	if out.AlwaysAllow == nil {
		out.AlwaysAllow = []string{}
	}
	if out.IPAllow == nil {
		out.IPAllow = []string{}
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

// agentAssistant is a person's AI Assistant profile, made (with
// everything off) the first time it's needed.
func (s *Server) agentAssistant(ctx context.Context, u *store.User) (*store.AgentToken, error) {
	t, err := s.Store.AgentAssistant(ctx, u.ID)
	if !errors.Is(err, store.ErrNotFound) {
		return t, err
	}
	off, _ := mcp.PresetScopes("off")
	t = &store.AgentToken{ID: store.NewID(), UserID: u.ID, Name: "AI Assistant", Scope: "custom",
		Kind: store.AgentKindAssistant, Scopes: encodeScopes(off), CreatedAt: time.Now()}
	// The assistant has no token: its token hash is a value no token hashes to.
	if err := s.Store.CreateAgentToken(ctx, t, "assistant:"+t.ID); err != nil {
		return nil, err
	}
	return t, nil
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
	assistant, err := s.agentAssistant(ctx, u)
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
		tools = append(tools, agentToolJSON{Name: t.Name, Title: t.Title, Description: t.Description,
			Action: t.action, Admin: t.admin, Cap: t.Cap, Need: t.Need})
	}
	toks := make([]agentTokenJSON, 0, len(tokens))
	for _, t := range tokens {
		toks = append(toks, toAgentTokenJSON(t))
	}
	origin := requestOrigin(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": enabled, "loopback_only": loopbackOnly, "can_manage": u.Role == store.RoleAdmin,
		"local_url": s.localMCPURL(), "network_url": origin + "/mcp",
		"sse_url": origin + "/mcp/sse", "pair_url": origin + "/mcp/pair",
		"capabilities": mcp.Capabilities, "presets": mcp.Presets,
		"assistant": toAgentTokenJSON(*assistant),
		"tools":     tools, "tokens": toks, "activity": withToolInfo(activity),
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

// maxAgentDays is the longest expiry a person can set.
const maxAgentDays = 3650

type agentTokenRequest struct {
	Name          string      `json:"name"`
	Kind          string      `json:"kind"`  // local (default) | remote
	Scope         string      `json:"scope"` // old read | act
	Preset        string      `json:"preset"`
	Scopes        *mcp.Scopes `json:"scopes"`
	IPAllow       []string    `json:"ip_allow"`
	ExpiresInDays int         `json:"expires_in_days"` // 0 = never
	Pairing       bool        `json:"pairing"`         // hand out a pairing code instead of the token
}

// requestedScopes picks the profile a create or update asks for: explicit
// scopes, a preset, or an old read/act scope. ok is false (and an error
// written) when it names something unknown; nil when it names nothing.
func requestedScopes(w http.ResponseWriter, preset, legacy string, sc *mcp.Scopes) (*mcp.Scopes, bool) {
	switch {
	case sc != nil:
		n, err := sc.Normalize()
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation", err.Error())
			return nil, false
		}
		return &n, true
	case preset != "":
		n, ok := mcp.PresetScopes(preset)
		if !ok {
			writeError(w, http.StatusBadRequest, "validation", "unknown preset "+truncate(preset, 40))
			return nil, false
		}
		return &n, true
	case legacy != "":
		if legacy != store.AgentScopeRead && legacy != store.AgentScopeAct {
			writeError(w, http.StatusBadRequest, "validation", "scope must be read or act")
			return nil, false
		}
		n := mcp.LegacyScopes(legacy)
		return &n, true
	}
	return nil, true
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
	if req.Kind == "" {
		req.Kind = store.AgentKindLocal
	}
	if req.Kind != store.AgentKindLocal && req.Kind != store.AgentKindRemote {
		writeError(w, http.StatusBadRequest, "validation", "kind must be local or remote")
		return
	}
	sc, ok := requestedScopes(w, req.Preset, req.Scope, req.Scopes)
	if !ok {
		return
	}
	if sc == nil { // nothing asked for: nothing allowed (least privilege)
		d, _ := mcp.PresetScopes("off")
		sc = &d
	}
	ips, err := mcp.ParseAllowList(req.IPAllow)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation", err.Error())
		return
	}
	if req.ExpiresInDays < 0 || req.ExpiresInDays > maxAgentDays {
		writeError(w, http.StatusBadRequest, "validation", "expiry must be 0 (never) to 3650 days")
		return
	}
	u := userFrom(r.Context())
	now := time.Now()
	t := &store.AgentToken{ID: store.NewID(), UserID: u.ID, Name: req.Name, Scope: "custom", Kind: req.Kind,
		Scopes: encodeScopes(*sc), IPAllow: ips, CreatedAt: now}
	if req.Scope != "" && req.Scopes == nil && req.Preset == "" {
		t.Scope = req.Scope
	}
	if req.ExpiresInDays > 0 {
		t.ExpiresAt = now.Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
	}
	out := map[string]any{}
	if req.Pairing {
		code, ph := newPairCode()
		t.PairExpiresAt = now.Add(pairCodeTTL)
		err = s.Store.CreateAgentToken(r.Context(), t, "pending:"+t.ID, ph)
		out["code"] = code
	} else {
		raw, hash := newAgentToken()
		err = s.Store.CreateAgentToken(r.Context(), t, hash)
		out["token"] = raw
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, u.ID, "agents.token_create", t.Name, true, t.Kind+" "+string(sc.Risk()))
	out["agent"] = toAgentTokenJSON(*t)
	writeJSON(w, http.StatusCreated, out)
}

type agentUpdateRequest struct {
	Tools       *[]string   `json:"tools"` // null = every tool
	AlwaysAllow []string    `json:"always_allow"`
	AllTools    bool        `json:"all_tools"`
	Name        *string     `json:"name"`
	Preset      string      `json:"preset"`
	Scopes      *mcp.Scopes `json:"scopes"`
	IPAllow     *[]string   `json:"ip_allow"`
	// ExpiresInDays: 0 = never, n = n days from now. Left out = unchanged.
	ExpiresInDays *int  `json:"expires_in_days"`
	Paused        *bool `json:"paused"`
}

func (s *Server) agentTokenUpdate(w http.ResponseWriter, r *http.Request) {
	var req agentUpdateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	u := userFrom(r.Context())
	ctx := r.Context()
	cur, err := s.Store.AgentTokenByID(ctx, r.PathValue("id"), u.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "That agent isn't around anymore.")
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
			t := findAgentTool(n)
			if t == nil || !t.action {
				writeError(w, http.StatusBadRequest, "validation", "only action tools can be always allowed")
				return
			}
			if c := mcp.FindCapability(t.Cap); c != nil && c.NeverAuto {
				writeError(w, http.StatusBadRequest, "validation", t.Title+" always asks first")
				return
			}
			if !slices.Contains(always, n) {
				always = append(always, n)
			}
		}
	}
	sc, ok := requestedScopes(w, req.Preset, "", req.Scopes)
	if !ok {
		return
	}
	changed := []string{}
	if sc != nil {
		cur.Scopes = encodeScopes(*sc)
		changed = append(changed, "scopes="+string(sc.Risk()))
	}
	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		if !validAgentName(n) {
			writeError(w, http.StatusBadRequest, "validation", "Name your agent (up to 64 characters).")
			return
		}
		cur.Name = n
		changed = append(changed, "name")
	}
	if req.IPAllow != nil {
		ips, err := mcp.ParseAllowList(*req.IPAllow)
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation", err.Error())
			return
		}
		cur.IPAllow = ips
		changed = append(changed, "ip_allow")
	}
	if req.ExpiresInDays != nil {
		d := *req.ExpiresInDays
		if d < 0 || d > maxAgentDays {
			writeError(w, http.StatusBadRequest, "validation", "expiry must be 0 (never) to 3650 days")
			return
		}
		cur.ExpiresAt = time.Time{}
		if d > 0 {
			cur.ExpiresAt = time.Now().Add(time.Duration(d) * 24 * time.Hour)
		}
		changed = append(changed, "expiry")
	}
	if req.Paused != nil {
		cur.Paused = *req.Paused
		changed = append(changed, "paused="+boolSetting(cur.Paused))
	}
	if err := s.Store.UpdateAgentProfile(ctx, cur); err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.Store.SetAgentTokenTools(ctx, cur.ID, u.ID, tools, always); err != nil {
		s.internalError(w, r, err)
		return
	}
	if cur.Paused {
		s.stopAgent(cur)
	}
	s.audit(r, u.ID, "agents.token_update", cur.Name, true, strings.Join(changed, " "))
	cur.Tools, cur.AlwaysAllow = tools, always
	writeJSON(w, http.StatusOK, toAgentTokenJSON(*cur))
}

// stopAgent ends what one agent is doing: its waiting approvals are
// denied, its running tools cancelled and its event streams closed.
func (s *Server) stopAgent(t *store.AgentToken) {
	b := s.agentBroker()
	for _, a := range b.Pending(t.UserID) {
		if a.TokenID == t.ID {
			_ = b.Decide(a.ID, t.UserID, mcp.Deny)
		}
	}
	s.agents.mu.Lock()
	for run := range s.agents.runs {
		if run.tokenID == t.ID {
			run.stopped = true
			run.cancel()
		}
	}
	s.agents.mu.Unlock()
	s.closeAgentStreams(t.UserID, t.ID)
}

func (s *Server) agentTokenRevoke(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	cur, err := s.Store.AgentTokenByID(r.Context(), r.PathValue("id"), u.ID)
	if err == nil && cur.Kind == store.AgentKindAssistant {
		err = store.ErrNotFound // the assistant is switched off, not revoked
	}
	if err == nil {
		err = s.Store.RevokeAgentToken(r.Context(), cur.ID, u.ID, time.Now())
	}
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "That agent isn't around anymore.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.stopAgent(cur)
	s.audit(r, u.ID, "agents.token_revoke", cur.Name, true, "")
	w.WriteHeader(http.StatusNoContent)
}

// agentTokenRotate gives an agent a new token (or pairing code); the old
// one stops working at once.
func (s *Server) agentTokenRotate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Pairing bool `json:"pairing"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	u := userFrom(r.Context())
	ctx := r.Context()
	cur, err := s.Store.AgentTokenByID(ctx, r.PathValue("id"), u.ID)
	if err == nil && cur.Kind == store.AgentKindAssistant {
		err = store.ErrNotFound
	}
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "That agent isn't around anymore.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := map[string]any{}
	if req.Pairing {
		code, ph := newPairCode()
		cur.Pairing, cur.PairExpiresAt = true, time.Now().Add(pairCodeTTL)
		err = s.Store.SetAgentSecret(ctx, cur.ID, u.ID, "pending:"+cur.ID+":"+ph[:12], ph, cur.PairExpiresAt)
		out["code"] = code
	} else {
		raw, hash := newAgentToken()
		cur.Pairing, cur.PairExpiresAt = false, time.Time{}
		err = s.Store.SetAgentSecret(ctx, cur.ID, u.ID, hash, "", time.Time{})
		out["token"] = raw
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.stopAgent(cur)
	s.audit(r, u.ID, "agents.token_rotate", cur.Name, true, onOff(req.Pairing, "pairing code", "token"))
	out["agent"] = toAgentTokenJSON(*cur)
	writeJSON(w, http.StatusOK, out)
}

// agentActivityJSON is an activity entry with what kind of thing it was.
type agentActivityJSON struct {
	store.AgentActivity
	Cap    string `json:"capability"`
	Effect string `json:"effect"` // read | change | run
}

func withToolInfo(list []store.AgentActivity) []agentActivityJSON {
	out := make([]agentActivityJSON, 0, len(list))
	for _, a := range list {
		e := agentActivityJSON{AgentActivity: a, Effect: "read"}
		if t := findAgentTool(a.Tool); t != nil {
			e.Cap = t.Cap
			switch {
			case t.Cap == mcp.CapTerminal || t.Cap == mcp.CapCode:
				e.Effect = "run"
			case t.action:
				e.Effect = "change"
			}
		}
		out = append(out, e)
	}
	return out
}

func (s *Server) agentActivity(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	list, err := s.Store.ListAgentActivityFor(r.Context(), userFrom(r.Context()).ID, q.Get("agent"), before, limit)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, withToolInfo(list))
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

// agentsStopAll is the kill switch. Everyone can stop their own agents
// (paused, waiting approvals denied, running tools cancelled, streams
// closed); for an administrator it stops every agent on the server and
// switches NoCap MCP off.
func (s *Server) agentsStopAll(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	ctx := r.Context()
	scope := u.ID
	if u.Role == store.RoleAdmin {
		scope = ""
		if err := s.Store.SetSetting(ctx, agentsEnabledKey, "0"); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	paused, err := s.Store.PauseAgents(ctx, scope)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	denied := s.agentBroker().DenyAll(scope)
	stopped := s.stopRuns(scope)
	s.closeAgentStreams(scope, "")
	s.audit(r, u.ID, "agents.stop_all", onOff(scope == "", "everyone", "own"), true,
		strconv.FormatInt(paused, 10)+" paused, "+strconv.Itoa(denied)+" denied, "+strconv.Itoa(stopped)+" stopped")
	writeJSON(w, http.StatusOK, map[string]any{
		"paused": paused, "denied": denied, "stopped": stopped, "everyone": scope == "", "mcp_off": scope == "",
	})
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

// ---- the built-in AI Assistant ----

// assistantBackend is the AI Assistant acting for the signed-in person,
// under its own profile.
func (s *Server) assistantBackend(w http.ResponseWriter, r *http.Request) (*agentBackend, bool) {
	u := userFrom(r.Context())
	t, err := s.agentAssistant(r.Context(), u)
	if err != nil {
		s.internalError(w, r, err)
		return nil, false
	}
	if t.Paused {
		writeError(w, http.StatusForbidden, "paused", "The AI Assistant's access is paused. Resume it in Settings → AI Agents.")
		return nil, false
	}
	return newAgentBackend(s, r, t, u), true
}

func (s *Server) assistantTools(w http.ResponseWriter, r *http.Request) {
	b, ok := s.assistantBackend(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": b.Tools(), "scopes": b.sc, "risk": b.sc.Risk()})
}

// assistantCall runs one tool for the AI Assistant: the same scope checks,
// approvals and activity log as any other agent.
func (s *Server) assistantCall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if !decodeJSONLimit(w, r, &req, maxMCPBody) {
		return
	}
	b, ok := s.assistantBackend(w, r)
	if !ok {
		return
	}
	t := findAgentTool(req.Name)
	if t == nil || !agentMayUse(t, b.tok, b.u, b.sc) {
		writeError(w, http.StatusForbidden, "not_allowed", "The AI Assistant isn't allowed to use "+truncate(req.Name, 40)+".")
		return
	}
	if req.Arguments == nil {
		req.Arguments = map[string]any{}
	}
	if err := mcp.ValidateArgs(t.describe().InputSchema, req.Arguments); err != nil {
		writeJSON(w, http.StatusOK, mcp.Errorf("invalid arguments: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, s.runAgentTool(r.Context(), b, t, req.Arguments))
}
