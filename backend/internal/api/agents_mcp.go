package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/devices"
	"alfaos/alfad/internal/hostaccess"
	"alfaos/alfad/internal/mcp"
	"alfaos/alfad/internal/nativeapps"
	"alfaos/alfad/internal/notify"
	"alfaos/alfad/internal/store"
)

// NoCap MCP: AI agents (on this machine, or on the network when the owner
// allows it) reach NoCapOS's tools at /mcp with a per-agent access token
// from Settings → AI Agents. Looking around runs straight away; anything
// that changes something waits for the token owner to approve it.

const (
	agentsEnabledKey  = "agents.enabled"       // "1" = on (off by default)
	agentsLoopbackKey = "agents.loopback_only" // "0" = the network may use it (on by default)
	agentTokenPrefix  = "ncmcp_"
	maxMCPBody        = 4 << 20
	agentUsedEvery    = time.Minute
)

type agentsState struct {
	once   sync.Once
	broker *mcp.Broker
	screen mcp.Screen
}

func (s *Server) agentBroker() *mcp.Broker {
	s.agents.once.Do(func() {
		if s.agents.broker == nil {
			s.agents.broker = &mcp.Broker{OnAsk: s.onApprovalAsked}
		}
		if s.agents.screen.EnvFile == "" {
			s.agents.screen = mcp.Screen{EnvFile: nativeapps.DesktopEnvPath}
		}
	})
	return s.agents.broker
}

func (s *Server) agentScreen() mcp.Screen {
	s.agentBroker()
	return s.agents.screen
}

// agentSettings: whether NoCap MCP is on, and whether only this machine may use it.
func (s *Server) agentSettings(ctx context.Context) (enabled, loopbackOnly bool) {
	on, _ := s.Store.Setting(ctx, agentsEnabledKey)
	lo, _ := s.Store.Setting(ctx, agentsLoopbackKey)
	return on == "1", lo != "0"
}

func newAgentToken() (raw, hash string) {
	r, _ := devices.NewToken()
	raw = agentTokenPrefix + r
	return raw, devices.HashToken(raw)
}

func mcpError(w http.ResponseWriter, status int, msg string) {
	writeError(w, status, "mcp", msg)
}

// mcpEndpoint is the Streamable HTTP transport: one JSON-RPC message (or
// batch) per POST, answered with JSON. There is no server-to-client
// stream, so GET answers 405 as the spec allows.
func (s *Server) mcpEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		mcpError(w, http.StatusMethodNotAllowed, "NoCap MCP takes JSON-RPC messages by POST.")
		return
	}
	ctx := r.Context()
	enabled, loopbackOnly := s.agentSettings(ctx)
	if !enabled {
		mcpError(w, http.StatusForbidden, "NoCap MCP is off. Turn it on in Settings → AI Agents.")
		return
	}
	if loopbackOnly && !hostaccess.LoopbackPeer(r.RemoteAddr) {
		mcpError(w, http.StatusForbidden, "NoCap MCP only takes agents running on this machine. Change that in Settings → AI Agents.")
		return
	}
	// Browsers send Origin: a web page must never drive this (DNS rebinding).
	if o := r.Header.Get("Origin"); o != "" && o != requestOrigin(r) {
		mcpError(w, http.StatusForbidden, "cross-origin requests aren't allowed")
		return
	}
	tok, u, ok := s.agentAuth(w, r)
	if !ok {
		return
	}
	if v := r.Header.Get("MCP-Protocol-Version"); v != "" && !mcp.SupportedProtocol(v) {
		mcpError(w, http.StatusBadRequest, "unsupported MCP-Protocol-Version "+truncate(v, 32))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxMCPBody))
	if err != nil {
		mcpError(w, http.StatusRequestEntityTooLarge, "message too large")
		return
	}
	if now := time.Now(); now.Sub(tok.LastUsedAt) >= agentUsedEvery {
		if err := s.Store.TouchAgentToken(ctx, tok.ID, now); err != nil {
			s.Log.Warn("agent token last used not saved", "err", err)
		}
	}
	b := &agentBackend{s: s, r: r, tok: tok, u: u}
	reply := mcp.Handle(ctx, b, mcp.ServerInfo{
		Name: "nocap", Title: "NoCapOS", Version: s.Version,
		Instructions: "Tools for the NoCapOS server you're running on. Read-only tools answer straight away. " +
			"Tools that change something wait (up to 2 minutes) until the owner approves them in NoCapOS; a denial or no answer means it didn't run.",
	}, body)
	if reply == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(reply)
}

// agentAuth checks "Authorization: Bearer <agent token>" and that its owner
// may still use NoCapOS.
func (s *Server) agentAuth(w http.ResponseWriter, r *http.Request) (*store.AgentToken, *store.User, bool) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	raw = strings.TrimSpace(raw)
	if !ok || !strings.HasPrefix(raw, agentTokenPrefix) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="nocap-mcp"`)
		mcpError(w, http.StatusUnauthorized, "missing agent token; make one in Settings → AI Agents")
		return nil, nil, false
	}
	ctx := r.Context()
	tok, err := s.Store.AgentTokenByHash(ctx, devices.HashToken(raw))
	if errors.Is(err, store.ErrNotFound) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="nocap-mcp", error="invalid_token"`)
		mcpError(w, http.StatusUnauthorized, "this agent token was revoked or never existed")
		return nil, nil, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return nil, nil, false
	}
	u, err := s.Store.UserByID(ctx, tok.UserID)
	if err == nil {
		err = s.Auth.DesktopAllowed(ctx, u)
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, auth.ErrInvalidToken) || errors.Is(err, auth.ErrFilesOnly) {
		mcpError(w, http.StatusUnauthorized, "the account this token belongs to can't use NoCapOS right now")
		return nil, nil, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return nil, nil, false
	}
	return tok, u, true
}

// agentBackend is what one agent request sees.
type agentBackend struct {
	s   *Server
	r   *http.Request
	tok *store.AgentToken
	u   *store.User
}

// allowed: the token's scope, its owner's role and its own tool list.
func agentMayUse(t *agentTool, tok *store.AgentToken, u *store.User) bool {
	if t.action && tok.Scope != store.AgentScopeAct {
		return false
	}
	if t.admin && u.Role != store.RoleAdmin {
		return false
	}
	return tok.Tools == nil || slices.Contains(tok.Tools, t.Name)
}

func (b *agentBackend) Tools() []mcp.Tool {
	out := []mcp.Tool{}
	for i := range agentTools {
		if t := &agentTools[i]; agentMayUse(t, b.tok, b.u) {
			out = append(out, t.describe())
		}
	}
	return out
}

func (b *agentBackend) Call(ctx context.Context, name string, args map[string]any) (mcp.Result, bool) {
	t := findAgentTool(name)
	if t == nil || !agentMayUse(t, b.tok, b.u) {
		return mcp.Result{}, false
	}
	return b.s.runAgentTool(ctx, b, t, args), true
}

// runAgentTool asks for approval when the tool changes something, runs it,
// and records what happened.
func (s *Server) runAgentTool(ctx context.Context, b *agentBackend, t *agentTool, args map[string]any) mcp.Result {
	summary := truncate(t.summarize(args), 300)
	record := func(outcome, detail string) {
		_ = s.Store.AddAgentActivity(context.WithoutCancel(ctx), store.AgentActivity{
			TokenID: b.tok.ID, UserID: b.u.ID, Agent: b.tok.Name, Tool: t.Name,
			Summary: summary, Outcome: outcome, Detail: truncate(detail, 300),
		})
		s.auditCtx(context.WithoutCancel(ctx), b.u.ID, "agents."+t.Name, b.tok.Name, clientIP(b.r, s.Config.TrustedProxies),
			outcome == "ok", outcome+": "+summary)
	}
	if t.action && !slices.Contains(b.tok.AlwaysAllow, t.Name) {
		out := s.agentBroker().Ask(ctx, mcp.Approval{
			UserID: b.u.ID, TokenID: b.tok.ID, Agent: b.tok.Name,
			Tool: t.Name, ToolTitle: t.Title, Summary: summary, Arguments: argsForCard(args),
		})
		switch {
		case out.TimedOut:
			record("timeout", "nobody answered")
			return mcp.Errorf("Nobody approved this in NoCapOS within 2 minutes, so it didn't run.")
		case out.Decision == mcp.Deny:
			record("denied", "")
			return mcp.Errorf("The owner said no in NoCapOS, so it didn't run.")
		case out.Decision == mcp.Always:
			s.alwaysAllow(context.WithoutCancel(ctx), b.tok, t.Name)
		}
	}
	res := s.execAgentTool(ctx, b, t, args)
	if res.IsError {
		msg := ""
		if len(res.Content) > 0 {
			msg = res.Content[0].Text
		}
		record("error", msg)
	} else {
		record("ok", "")
	}
	return res
}

// execAgentTool runs a tool, turning a panic into a failed result.
func (s *Server) execAgentTool(ctx context.Context, b *agentBackend, t *agentTool, args map[string]any) (res mcp.Result) {
	defer func() {
		if v := recover(); v != nil {
			s.Log.Error("agent tool panic", "tool", t.Name, "panic", v)
			res = mcp.Errorf("%s isn't available on this server right now.", t.Title)
		}
	}()
	return t.run(&toolCall{s: s, ctx: ctx, b: b, args: args})
}

// alwaysAllow remembers "Always allow this tool for this agent".
func (s *Server) alwaysAllow(ctx context.Context, tok *store.AgentToken, tool string) {
	cur, err := s.Store.AgentTokenByID(ctx, tok.ID, tok.UserID)
	if err != nil {
		return
	}
	if !slices.Contains(cur.AlwaysAllow, tool) {
		cur.AlwaysAllow = append(cur.AlwaysAllow, tool)
	}
	if err := s.Store.SetAgentTokenTools(ctx, cur.ID, cur.UserID, cur.Tools, cur.AlwaysAllow); err != nil {
		s.Log.Warn("always allow not saved", "err", err)
	}
	tok.AlwaysAllow = cur.AlwaysAllow
}

// argsForCard keeps long values (file contents) readable on the card.
func argsForCard(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		if str, ok := v.(string); ok && len(str) > 2000 {
			v = str[:2000] + fmt.Sprintf("… (%d more characters)", len(str)-2000)
		}
		out[k] = v
	}
	return out
}

// onApprovalAsked pings the owner's Notification Center (administrators
// have one); the approval card itself comes over the live socket.
func (s *Server) onApprovalAsked(a mcp.Approval) {
	if s.notify == nil {
		return
	}
	u, err := s.Store.UserByID(context.Background(), a.UserID)
	if err != nil || u.Role != store.RoleAdmin {
		return
	}
	_, _ = s.notify.Add(context.Background(), notify.Notification{
		Key: "agent:" + a.ID, Kind: notify.KindAgent, Level: "warning",
		Title: a.Agent + " wants to " + strings.ToLower(a.ToolTitle),
		Body:  a.Summary + " — approve or deny it on your NoCapOS screen.",
		Icon:  "settings", Action: &notify.Action{App: "settings", Props: map[string]any{"section": "agents"}},
		CreatedAt: time.Now(),
	})
}
