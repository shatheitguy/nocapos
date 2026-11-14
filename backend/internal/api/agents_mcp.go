package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/devices"
	"alfaos/alfad/internal/mcp"
	"alfaos/alfad/internal/nativeapps"
	"alfaos/alfad/internal/notify"
	"alfaos/alfad/internal/store"
)

// NoCap MCP: AI agents reach NoCapOS's tools with a per-agent token from
// Settings → AI Agents, over Streamable HTTP (POST /mcp) or the older
// HTTP+SSE transport (GET /mcp/sse + POST /mcp/messages). Local agents
// may only connect from this machine; remote ones from the network when
// it's allowed, and only from their IP allowlist. Every tool needs a
// level of a capability in the agent's scopes; actions wait for the
// owner's approval unless the owner relaxed that capability.

const (
	agentsEnabledKey  = "agents.enabled"       // "1" = on (off by default)
	agentsLoopbackKey = "agents.loopback_only" // "0" = the network may use it (on by default)
	agentTokenPrefix  = "ncmcp_"
	maxMCPBody        = 4 << 20
	agentUsedEvery    = time.Minute
	pairCodeTTL       = 10 * time.Minute
	sseKeepAlive      = 15 * time.Second
	maxSSESessions    = 64
)

type agentsState struct {
	once   sync.Once
	broker *mcp.Broker
	screen mcp.Screen

	mu   sync.Mutex
	runs map[*agentRun]struct{}
	sse  map[string]*sseSession
}

// agentRun is one tool call in progress (so Stop all agents can end it).
type agentRun struct {
	tokenID, userID string
	cancel          context.CancelFunc
	stopped         bool
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

// Pairing codes: 8 characters people can read out (no 0/O, 1/I/L).
const pairAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func newPairCode() (display, hash string) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = pairAlphabet[int(b[i])%len(pairAlphabet)]
	}
	code := string(b)
	return code[:4] + "-" + code[4:], pairHash(code)
}

func pairHash(code string) string {
	c := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
	return devices.HashToken("pair:" + c)
}

func mcpError(w http.ResponseWriter, status int, msg string) {
	writeError(w, status, "mcp", msg)
}

// agentScopes is an agent's profile: its saved scopes, or what its old
// read/act scope allowed. A profile that doesn't parse allows nothing.
func agentScopes(tok *store.AgentToken) mcp.Scopes {
	if tok.Scopes == "" {
		return mcp.LegacyScopes(tok.Scope)
	}
	var sc mcp.Scopes
	if json.Unmarshal([]byte(tok.Scopes), &sc) != nil {
		return mcp.Scopes{}
	}
	n, err := sc.Normalize()
	if err != nil {
		return mcp.Scopes{}
	}
	return n
}

func encodeScopes(sc mcp.Scopes) string {
	b, _ := json.Marshal(sc)
	return string(b)
}

// peerIP is the agent's address (through trusted proxies) and whether
// it's this machine.
func (s *Server) peerIP(r *http.Request) (string, bool) {
	ip := clientIP(r, s.Config.TrustedProxies)
	a, err := netip.ParseAddr(ip)
	return ip, err == nil && a.Unmap().IsLoopback()
}

// agentGate turns away requests while NoCap MCP is off, network requests
// while only this machine may use it, and web pages.
func (s *Server) agentGate(w http.ResponseWriter, r *http.Request) bool {
	enabled, loopbackOnly := s.agentSettings(r.Context())
	if !enabled {
		mcpError(w, http.StatusForbidden, "NoCap MCP is off. Turn it on in Settings → AI Agents.")
		return false
	}
	if _, local := s.peerIP(r); loopbackOnly && !local {
		mcpError(w, http.StatusForbidden, "NoCap MCP only takes agents running on this machine. Change that in Settings → AI Agents.")
		return false
	}
	// Browsers send Origin: a web page must never drive this (DNS rebinding).
	if o := r.Header.Get("Origin"); o != "" && o != requestOrigin(r) {
		mcpError(w, http.StatusForbidden, "cross-origin requests aren't allowed")
		return false
	}
	return true
}

// agentReach checks where this agent may connect from.
func (s *Server) agentReach(r *http.Request, tok *store.AgentToken) string {
	ip, local := s.peerIP(r)
	switch tok.Kind {
	case store.AgentKindLocal:
		if !local {
			return "This agent may only connect from this machine."
		}
	case store.AgentKindRemote:
		_, loopbackOnly := s.agentSettings(r.Context())
		if loopbackOnly && !local {
			return "NoCap MCP only takes agents running on this machine right now."
		}
	default:
		return "This agent can't connect over MCP."
	}
	if !mcp.AllowListed(tok.IPAllow, ip) {
		return "This agent isn't allowed to connect from " + ip + "."
	}
	return ""
}

// agentUsable checks the agent itself (paused, expired) and its owner.
// status is 0 when it's fine.
func (s *Server) agentUsable(ctx context.Context, tok *store.AgentToken) (*store.User, int, string, error) {
	if tok.Expired(time.Now()) {
		return nil, http.StatusUnauthorized, "this agent's access has expired; renew it in Settings → AI Agents", nil
	}
	if tok.Paused {
		return nil, http.StatusForbidden, "this agent is paused; resume it in Settings → AI Agents", nil
	}
	u, err := s.Store.UserByID(ctx, tok.UserID)
	if err == nil {
		err = s.Auth.DesktopAllowed(ctx, u)
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, auth.ErrInvalidToken) || errors.Is(err, auth.ErrFilesOnly) {
		return nil, http.StatusUnauthorized, "the account this agent belongs to can't use NoCapOS right now", nil
	}
	if err != nil {
		return nil, 0, "", err
	}
	return u, 0, "", nil
}

// agentAuth checks "Authorization: Bearer <agent token>", where the agent
// connects from, and that it and its owner may still use NoCapOS.
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
	if msg := s.agentReach(r, tok); msg != "" {
		mcpError(w, http.StatusForbidden, msg)
		return nil, nil, false
	}
	u, status, msg, err := s.agentUsable(ctx, tok)
	if err != nil {
		s.internalError(w, r, err)
		return nil, nil, false
	}
	if status != 0 {
		if status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", `Bearer realm="nocap-mcp", error="invalid_token"`)
		}
		mcpError(w, status, msg)
		return nil, nil, false
	}
	if now := time.Now(); now.Sub(tok.LastUsedAt) >= agentUsedEvery {
		if err := s.Store.TouchAgentToken(ctx, tok.ID, now); err != nil {
			s.Log.Warn("agent token last used not saved", "err", err)
		}
	}
	return tok, u, true
}

func (s *Server) mcpInfo() mcp.ServerInfo {
	return mcp.ServerInfo{
		Name: "nocap", Title: "NoCapOS", Version: s.Version,
		Instructions: "Tools for a NoCapOS server. You only see the tools the owner allowed this agent. Read-only tools answer straight away. " +
			"Tools that change something may wait (up to 2 minutes) until the owner approves them in NoCapOS; a denial or no answer means it didn't run.",
	}
}

func readMCPBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxMCPBody))
	if err != nil {
		mcpError(w, http.StatusRequestEntityTooLarge, "message too large")
		return nil, false
	}
	return body, true
}

// mcpEndpoint is the Streamable HTTP transport: one JSON-RPC message (or
// batch) per POST, answered with JSON. There is no server-to-client
// stream here (GET answers 405, as the spec allows); /mcp/sse has one.
func (s *Server) mcpEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		mcpError(w, http.StatusMethodNotAllowed, "NoCap MCP takes JSON-RPC messages by POST.")
		return
	}
	if !s.agentGate(w, r) {
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
	body, ok := readMCPBody(w, r)
	if !ok {
		return
	}
	b := newAgentBackend(s, r, tok, u)
	reply := mcp.Handle(r.Context(), b, s.mcpInfo(), body)
	if reply == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(reply)
}

// ---- HTTP+SSE transport (for MCP clients that only speak the older one) ----

type sseSession struct {
	id, tokenID, userID string
	ctx                 context.Context
	cancel              context.CancelFunc
	out                 chan []byte
}

// mcpSSE opens the event stream: first an "endpoint" event naming where to
// POST messages, then each answer as a "message" event.
func (s *Server) mcpSSE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		mcpError(w, http.StatusMethodNotAllowed, "open the event stream with GET")
		return
	}
	if !s.agentGate(w, r) {
		return
	}
	tok, _, ok := s.agentAuth(w, r)
	if !ok {
		return
	}
	idb := make([]byte, 16)
	_, _ = rand.Read(idb)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	sess := &sseSession{id: hex.EncodeToString(idb), tokenID: tok.ID, userID: tok.UserID, ctx: ctx, cancel: cancel, out: make(chan []byte, 16)}
	s.agents.mu.Lock()
	if s.agents.sse == nil {
		s.agents.sse = map[string]*sseSession{}
	}
	if len(s.agents.sse) >= maxSSESessions {
		s.agents.mu.Unlock()
		mcpError(w, http.StatusServiceUnavailable, "too many open agent streams")
		return
	}
	s.agents.sse[sess.id] = sess
	s.agents.mu.Unlock()
	defer func() {
		s.agents.mu.Lock()
		delete(s.agents.sse, sess.id)
		s.agents.mu.Unlock()
	}()

	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprintf(w, "event: endpoint\ndata: /mcp/messages?session=%s\n\n", sess.id); err != nil {
		return
	}
	if rc.Flush() != nil {
		return
	}
	tick := time.NewTicker(sseKeepAlive)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-sess.out:
			if _, err := fmt.Fprintf(w, "event: message\ndata: %s\n\n", msg); err != nil {
				return
			}
		case <-tick.C:
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
				return
			}
		}
		if rc.Flush() != nil {
			return
		}
	}
}

// mcpSSEMessage takes one message for an open stream; the answer goes
// down the stream.
func (s *Server) mcpSSEMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		mcpError(w, http.StatusMethodNotAllowed, "send messages by POST")
		return
	}
	if !s.agentGate(w, r) {
		return
	}
	tok, u, ok := s.agentAuth(w, r)
	if !ok {
		return
	}
	s.agents.mu.Lock()
	sess := s.agents.sse[r.URL.Query().Get("session")]
	s.agents.mu.Unlock()
	if sess == nil || sess.tokenID != tok.ID {
		mcpError(w, http.StatusNotFound, "no such stream; open /mcp/sse first")
		return
	}
	body, ok := readMCPBody(w, r)
	if !ok {
		return
	}
	b := newAgentBackend(s, r, tok, u)
	go func() {
		reply := mcp.Handle(sess.ctx, b, s.mcpInfo(), body)
		if reply == nil {
			return
		}
		select {
		case sess.out <- reply:
		case <-sess.ctx.Done():
		}
	}()
	w.WriteHeader(http.StatusAccepted)
}

// closeAgentStreams ends the open event streams of one person's agents
// (userID "" = everyone's; tokenID "" = all of theirs).
func (s *Server) closeAgentStreams(userID, tokenID string) {
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	for _, sess := range s.agents.sse {
		if (userID == "" || sess.userID == userID) && (tokenID == "" || sess.tokenID == tokenID) {
			sess.cancel()
			delete(s.agents.sse, sess.id)
		}
	}
}

// ---- pairing ----

type pairRequest struct {
	Code string `json:"code"`
}

// mcpPair swaps a one-time pairing code (shown in Settings → AI Agents)
// for the agent's token. Codes expire after 10 minutes and are rate
// limited per address.
func (s *Server) mcpPair(w http.ResponseWriter, r *http.Request) {
	if !s.agentGate(w, r) {
		return
	}
	if s.limited(w, r) {
		return
	}
	var req pairRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	ph := pairHash(req.Code)
	tok, err := s.Store.AgentPairingByHash(ctx, ph)
	if err == nil && !time.Now().Before(tok.PairExpiresAt) {
		err = store.ErrNotFound
	}
	if errors.Is(err, store.ErrNotFound) {
		mcpError(w, http.StatusBadRequest, "That pairing code is wrong or has expired. Make a new one in Settings → AI Agents.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if msg := s.agentReach(r, tok); msg != "" {
		mcpError(w, http.StatusForbidden, msg)
		return
	}
	if _, status, msg, err := s.agentUsable(ctx, tok); err != nil || status != 0 {
		if err != nil {
			s.internalError(w, r, err)
		} else {
			mcpError(w, status, msg)
		}
		return
	}
	raw, hash := newAgentToken()
	if err := s.Store.ClaimAgentPairing(ctx, tok.ID, ph, hash, time.Now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			mcpError(w, http.StatusBadRequest, "That pairing code was just used.")
			return
		}
		s.internalError(w, r, err)
		return
	}
	s.auditCtx(ctx, tok.UserID, "agents.paired", tok.Name, clientIP(r, s.Config.TrustedProxies), true, tok.Kind)
	writeJSON(w, http.StatusOK, map[string]string{"token": raw, "name": tok.Name, "url": requestOrigin(r) + "/mcp"})
}

// ---- running tools ----

// agentBackend is what one agent request sees.
type agentBackend struct {
	s   *Server
	r   *http.Request
	tok *store.AgentToken
	u   *store.User
	sc  mcp.Scopes
}

func newAgentBackend(s *Server, r *http.Request, tok *store.AgentToken, u *store.User) *agentBackend {
	return &agentBackend{s: s, r: r, tok: tok, u: u, sc: agentScopes(tok)}
}

// agentMayUse: the agent's scopes (deny by default), its owner's role and
// its own tool list.
func agentMayUse(t *agentTool, tok *store.AgentToken, u *store.User, sc mcp.Scopes) bool {
	if t.Cap == "" || !sc.Allows(t.Cap, t.Need) {
		return false
	}
	if t.admin && u.Role != store.RoleAdmin {
		return false
	}
	return tok.Tools == nil || slices.Contains(tok.Tools, t.Name)
}

// stillMayRun re-checks an approved call against the agent as it is now.
func stillMayRun(t *agentTool, cur *store.AgentToken, u *store.User, now time.Time) bool {
	return cur != nil && !cur.Paused && !cur.Expired(now) && agentMayUse(t, cur, u, agentScopes(cur))
}

func (b *agentBackend) Tools() []mcp.Tool {
	out := []mcp.Tool{}
	for i := range agentTools {
		if t := &agentTools[i]; agentMayUse(t, b.tok, b.u, b.sc) {
			out = append(out, t.describe())
		}
	}
	return out
}

func (b *agentBackend) Call(ctx context.Context, name string, args map[string]any) (mcp.Result, bool) {
	t := findAgentTool(name)
	if t == nil || !agentMayUse(t, b.tok, b.u, b.sc) {
		return mcp.Result{}, false
	}
	return b.s.runAgentTool(ctx, b, t, args), true
}

// asks reports whether a call of t by this agent waits for approval.
func (b *agentBackend) asks(t *agentTool) bool {
	if !t.action {
		return false
	}
	c := mcp.FindCapability(t.Cap)
	if c == nil || c.NeverAuto {
		return true
	}
	return b.sc.Asks(t.Cap) && !slices.Contains(b.tok.AlwaysAllow, t.Name)
}

// trackRun registers a tool call so Stop all agents can end it.
func (s *Server) trackRun(ctx context.Context, tok *store.AgentToken) (context.Context, *agentRun, func()) {
	ctx, cancel := context.WithCancel(ctx)
	run := &agentRun{tokenID: tok.ID, userID: tok.UserID, cancel: cancel}
	s.agents.mu.Lock()
	if s.agents.runs == nil {
		s.agents.runs = map[*agentRun]struct{}{}
	}
	s.agents.runs[run] = struct{}{}
	s.agents.mu.Unlock()
	return ctx, run, func() {
		s.agents.mu.Lock()
		delete(s.agents.runs, run)
		s.agents.mu.Unlock()
		cancel()
	}
}

// stopRuns ends the tool calls in progress of one person's agents ("" = everyone's).
func (s *Server) stopRuns(userID string) int {
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	n := 0
	for run := range s.agents.runs {
		if userID == "" || run.userID == userID {
			run.stopped = true
			run.cancel()
			n++
		}
	}
	return n
}

func (s *Server) runStopped(run *agentRun) bool {
	s.agents.mu.Lock()
	defer s.agents.mu.Unlock()
	return run.stopped
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
	ctx, run, done := s.trackRun(ctx, b.tok)
	defer done()
	if b.asks(t) {
		out := s.agentBroker().Ask(ctx, mcp.Approval{
			UserID: b.u.ID, TokenID: b.tok.ID, Agent: b.tok.Name,
			Tool: t.Name, ToolTitle: t.Title, Summary: summary, Arguments: argsForCard(args),
			Cap: t.Cap, Risk: mcp.LevelRisk(t.Cap, t.Need),
		})
		switch {
		case s.runStopped(run):
			record("stopped", "Stop all agents")
			return mcp.Errorf("The owner stopped all agents, so it didn't run.")
		case out.TimedOut:
			record("timeout", "nobody answered")
			return mcp.Errorf("Nobody approved this in NoCapOS within 2 minutes, so it didn't run.")
		case out.Decision == mcp.Deny:
			record("denied", "")
			return mcp.Errorf("The owner said no in NoCapOS, so it didn't run.")
		case out.Decision == mcp.Always:
			if c := mcp.FindCapability(t.Cap); c != nil && !c.NeverAuto {
				s.alwaysAllow(context.WithoutCancel(ctx), b.tok, t.Name)
			}
		}
		// The agent may have been paused, revoked, expired or narrowed while
		// it waited: it runs with what it may do now, not what it had then.
		cur, err := s.Store.AgentTokenByID(ctx, b.tok.ID, b.tok.UserID)
		if err != nil || !stillMayRun(t, cur, b.u, time.Now()) {
			record("stopped", "paused, revoked, expired or no longer allowed while waiting")
			return mcp.Errorf("This agent was paused, revoked or lost this permission, so it didn't run.")
		}
		nb := *b // b may be shared by other calls on the same connection
		nb.tok, nb.sc = cur, agentScopes(cur)
		b = &nb
	}
	res := s.execAgentTool(ctx, b, t, args)
	switch {
	case s.runStopped(run):
		record("stopped", "Stop all agents")
	case res.IsError:
		msg := ""
		if len(res.Content) > 0 {
			msg = res.Content[0].Text
		}
		record("error", msg)
	default:
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
	return t.run(&toolCall{s: s, ctx: ctx, b: b, sc: b.sc, args: args})
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

var cardShowsWhole = map[string]bool{"code": true, "command": true, "content": true}

// cardWholeMax covers every command (16 KB) and snippet (64 KB) in full;
// only a very long file's new text is cut, and the summary says how long.
const cardWholeMax = 64 << 10

// argsForCard keeps long values (file contents) readable on the card and
// never shows a password. What would actually run (code, a command, a
// file's new contents) is always shown whole, so nothing can hide past the
// cut; those are already capped by the tools themselves.
func argsForCard(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		if strings.Contains(strings.ToLower(k), "password") {
			v = "••••••"
		} else if str, ok := v.(string); ok {
			limit := 2000
			if cardShowsWhole[k] {
				limit = cardWholeMax
			}
			if len(str) > limit {
				v = str[:limit] + fmt.Sprintf("… (%d more characters)", len(str)-limit)
			}
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
