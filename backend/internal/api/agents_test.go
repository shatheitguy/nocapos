package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/mcp"
	"alfaos/alfad/internal/store"
)

// agentWorld is the auth test world with NoCap MCP switched on.
func agentWorld(t *testing.T) *authWorld {
	t.Helper()
	w := newAuthWorld(t)
	w.s.agents.broker = &mcp.Broker{Timeout: 5 * time.Second}
	if err := w.st.SetSetting(context.Background(), agentsEnabledKey, "1"); err != nil {
		t.Fatal(err)
	}
	return w
}

func makeAgentToken(t *testing.T, w *authWorld, as *store.User, scope string) (raw, id string) {
	t.Helper()
	rec := w.do(t, w.s.agentTokenCreate, as, "POST", "/api/v1/agents/tokens", map[string]string{"name": "Hermes", "scope": scope})
	var out struct {
		Token string         `json:"token"`
		Agent agentTokenJSON `json:"agent"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusCreated || !strings.HasPrefix(out.Token, agentTokenPrefix) {
		t.Fatalf("create token: %d %s", rec.Code, rec.Body)
	}
	return out.Token, out.Agent.ID
}

type mcpReq struct {
	token, remote, origin string
	body                  string
}

func mcpPost(s *Server, q mcpReq) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/mcp", strings.NewReader(q.body))
	r.Header.Set("Content-Type", "application/json")
	if q.token != "" {
		r.Header.Set("Authorization", "Bearer "+q.token)
	}
	if q.origin != "" {
		r.Header.Set("Origin", q.origin)
	}
	r.RemoteAddr = "127.0.0.1:50000"
	if q.remote != "" {
		r.RemoteAddr = q.remote
	}
	rec := httptest.NewRecorder()
	s.mcpEndpoint(rec, r)
	return rec
}

type rpcReply struct {
	Result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
		Content []mcp.Content `json:"content"`
		IsError bool          `json:"isError"`
	} `json:"result"`
	Error *mcp.RPCError `json:"error"`
}

func rpc(t *testing.T, s *Server, token, body string) rpcReply {
	t.Helper()
	rec := mcpPost(s, mcpReq{token: token, body: body})
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
	}
	var r rpcReply
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func toolNames(r rpcReply) []string {
	var out []string
	for _, t := range r.Result.Tools {
		out = append(out, t.Name)
	}
	return out
}

func callTool(name string, args map[string]any) string {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	return string(b)
}

func TestMCPAuth(t *testing.T) {
	w := newAuthWorld(t)
	boss := w.users["boss"]
	tok, id := makeAgentToken(t, w, boss, store.AgentScopeRead)
	ping := `{"jsonrpc":"2.0","id":1,"method":"ping"}`

	// Off by default.
	if rec := mcpPost(w.s, mcpReq{token: tok, body: ping}); rec.Code != http.StatusForbidden {
		t.Fatalf("off: %d", rec.Code)
	}
	_ = w.st.SetSetting(context.Background(), agentsEnabledKey, "1")
	if rec := mcpPost(w.s, mcpReq{token: tok, body: ping}); rec.Code != http.StatusOK {
		t.Fatalf("on: %d %s", rec.Code, rec.Body)
	}
	// No token, a wrong one, someone's login token.
	for _, bad := range []string{"", agentTokenPrefix + "nope", "eyJhbGciOi.x.y"} {
		if rec := mcpPost(w.s, mcpReq{token: bad, body: ping}); rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: %d", bad, rec.Code)
		}
	}
	// Loopback only (the default) keeps the network out; the switch lets it in.
	if rec := mcpPost(w.s, mcpReq{token: tok, remote: "192.168.1.20:4000", body: ping}); rec.Code != http.StatusForbidden {
		t.Fatalf("remote: %d", rec.Code)
	}
	_ = w.st.SetSetting(context.Background(), agentsLoopbackKey, "0")
	if rec := mcpPost(w.s, mcpReq{token: tok, remote: "192.168.1.20:4000", body: ping}); rec.Code != http.StatusOK {
		t.Fatalf("remote allowed: %d", rec.Code)
	}
	// Web pages can't drive it.
	if rec := mcpPost(w.s, mcpReq{token: tok, origin: "https://evil.example", body: ping}); rec.Code != http.StatusForbidden {
		t.Fatalf("origin: %d", rec.Code)
	}
	// Notifications get 202, GET gets 405.
	if rec := mcpPost(w.s, mcpReq{token: tok, body: `{"jsonrpc":"2.0","method":"notifications/initialized"}`}); rec.Code != http.StatusAccepted {
		t.Fatalf("notification: %d", rec.Code)
	}
	get := httptest.NewRecorder()
	w.s.mcpEndpoint(get, httptest.NewRequest("GET", "/mcp", nil))
	if get.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", get.Code)
	}
	// A disabled owner's token stops working.
	_ = w.st.SetDisabled(context.Background(), boss.ID, true)
	if rec := mcpPost(w.s, mcpReq{token: tok, body: ping}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled owner: %d", rec.Code)
	}
	_ = w.st.SetDisabled(context.Background(), boss.ID, false)
	// Revoked.
	if rec := w.do(t, w.s.agentTokenRevoke, w.users["alex"], "DELETE", "/api/v1/agents/tokens/"+id, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("someone else revoked it: %d", rec.Code)
	}
	r := httptest.NewRequest("DELETE", "/api/v1/agents/tokens/"+id, nil)
	r.SetPathValue("id", id)
	r = r.WithContext(context.WithValue(r.Context(), userKey, boss))
	rec := httptest.NewRecorder()
	w.s.agentTokenRevoke(rec, r)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d", rec.Code)
	}
	if rec := mcpPost(w.s, mcpReq{token: tok, body: ping}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked: %d", rec.Code)
	}
	// The token is only stored hashed.
	if _, err := w.st.AgentTokenByHash(context.Background(), tok); err == nil {
		t.Fatal("raw token found as a hash")
	}
}

func TestMCPScopes(t *testing.T) {
	w := agentWorld(t)
	list := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

	read, _ := makeAgentToken(t, w, w.users["boss"], store.AgentScopeRead)
	names := toolNames(rpc(t, w.s, read, list))
	if !slices.Contains(names, "read_file") || !slices.Contains(names, "list_containers") || slices.Contains(names, "write_file") || slices.Contains(names, "run_command") {
		t.Fatalf("read-only admin tools: %v", names)
	}
	// Read-only tokens can't call action tools at all.
	if r := rpc(t, w.s, read, callTool("open_url", map[string]any{"url": "https://x.example"})); r.Error == nil || r.Error.Code != mcp.CodeInvalidParams {
		t.Fatalf("read token called an action: %+v", r)
	}

	act, actID := makeAgentToken(t, w, w.users["boss"], store.AgentScopeAct)
	names = toolNames(rpc(t, w.s, act, list))
	if !slices.Contains(names, "write_file") || !slices.Contains(names, "click") || len(names) != len(agentTools) {
		t.Fatalf("act admin tools: %v", names)
	}

	// Non-admins never see administrators' tools.
	alexAct, _ := makeAgentToken(t, w, w.users["alex"], store.AgentScopeAct)
	names = toolNames(rpc(t, w.s, alexAct, list))
	for _, n := range names {
		if findAgentTool(n).admin {
			t.Fatalf("non-admin sees %s", n)
		}
	}
	if !slices.Contains(names, "run_command") || slices.Contains(names, "list_containers") {
		t.Fatalf("non-admin tools: %v", names)
	}
	if r := rpc(t, w.s, alexAct, callTool("list_containers", nil)); r.Error == nil {
		t.Fatalf("non-admin called an admin tool: %+v", r)
	}

	// The per-tool list narrows a token down.
	tools := []string{"system_info", "read_file"}
	rec := patchToken(t, w, w.users["boss"], actID, map[string]any{"tools": tools})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	names = toolNames(rpc(t, w.s, act, list))
	if !slices.Equal(names, tools) {
		t.Fatalf("narrowed: %v", names)
	}
	if rec := patchToken(t, w, w.users["boss"], actID, map[string]any{"tools": []string{"rm_rf"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown tool: %d", rec.Code)
	}
	if rec := patchToken(t, w, w.users["boss"], actID, map[string]any{"always_allow": []string{"read_file"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("always allow a read tool: %d", rec.Code)
	}
	if rec := patchToken(t, w, w.users["alex"], actID, map[string]any{"all_tools": true}); rec.Code != http.StatusNotFound {
		t.Fatalf("someone else's token: %d", rec.Code)
	}
	if rec := patchToken(t, w, w.users["boss"], actID, map[string]any{"all_tools": true}); rec.Code != http.StatusOK {
		t.Fatalf("all tools: %d", rec.Code)
	}
	if names = toolNames(rpc(t, w.s, act, list)); len(names) != len(agentTools) {
		t.Fatalf("all tools again: %v", names)
	}
}

func patchToken(t *testing.T, w *authWorld, as *store.User, id string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("PATCH", "/api/v1/agents/tokens/"+id, strings.NewReader(string(b)))
	r.Header.Set("Content-Type", "application/json")
	r.SetPathValue("id", id)
	r = r.WithContext(context.WithValue(r.Context(), userKey, as))
	rec := httptest.NewRecorder()
	w.s.agentTokenUpdate(rec, r)
	return rec
}

// decideNext answers the next approval card for as.
func decideNext(t *testing.T, w *authWorld, as *store.User, d mcp.Decision) mcp.Approval {
	t.Helper()
	b := w.s.agentBroker()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if p := b.Pending(as.ID); len(p) > 0 {
			body := `{"decision":"` + string(d) + `"}`
			r := httptest.NewRequest("POST", "/api/v1/agents/approvals/"+p[0].ID, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.SetPathValue("id", p[0].ID)
			r = r.WithContext(context.WithValue(r.Context(), userKey, as))
			rec := httptest.NewRecorder()
			w.s.agentApprovalDecide(rec, r)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("decide: %d %s", rec.Code, rec.Body)
			}
			return p[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no approval card")
	return mcp.Approval{}
}

func asyncRPC(t *testing.T, s *Server, token, body string) <-chan rpcReply {
	ch := make(chan rpcReply, 1)
	go func() {
		rec := mcpPost(s, mcpReq{token: token, body: body})
		var r rpcReply
		_ = json.Unmarshal(rec.Body.Bytes(), &r)
		ch <- r
	}()
	return ch
}

func resultText(r rpcReply) string {
	if len(r.Result.Content) == 0 {
		return ""
	}
	return r.Result.Content[0].Text
}

func TestMCPApprovalFlow(t *testing.T) {
	w := agentWorld(t)
	boss := w.users["boss"]
	tok, id := makeAgentToken(t, w, boss, store.AgentScopeAct)
	// open_url with a non-web address: harmless once approved (it's refused
	// by the tool itself), so the test sees exactly when it ran.
	call := callTool("open_url", map[string]any{"url": "file:///etc/shadow"})

	// Deny: it doesn't run.
	res := asyncRPC(t, w.s, tok, call)
	card := decideNext(t, w, boss, mcp.Deny)
	if card.Agent != "Hermes" || card.Tool != "open_url" || card.Arguments["url"] != "file:///etc/shadow" || !strings.Contains(card.Summary, "file:///etc/shadow") {
		t.Fatalf("card: %+v", card)
	}
	if r := <-res; !r.Result.IsError || !strings.Contains(resultText(r), "said no") {
		t.Fatalf("denied: %+v", r)
	}

	// Approve once: it runs (and fails on its own terms), and asks again next time.
	res = asyncRPC(t, w.s, tok, call)
	decideNext(t, w, boss, mcp.Once)
	if r := <-res; !r.Result.IsError || !strings.Contains(resultText(r), "http") {
		t.Fatalf("approved: %+v", r)
	}

	// Another person can't answer someone else's card.
	res = asyncRPC(t, w.s, tok, call)
	var pending []mcp.Approval
	for i := 0; i < 300 && len(pending) == 0; i++ {
		pending = w.s.agentBroker().Pending(boss.ID)
		time.Sleep(5 * time.Millisecond)
	}
	if len(pending) == 0 {
		t.Fatal("no card")
	}
	if err := w.s.agentBroker().Decide(pending[0].ID, w.users["admin2"].ID, mcp.Once); err == nil {
		t.Fatal("another admin answered it")
	}
	// Always: runs, and never asks again for this tool and agent.
	decideNext(t, w, boss, mcp.Always)
	<-res
	cur, err := w.st.AgentTokenByID(context.Background(), id, boss.ID)
	if err != nil || !slices.Contains(cur.AlwaysAllow, "open_url") {
		t.Fatalf("always allow not saved: %+v %v", cur, err)
	}
	r := rpc(t, w.s, tok, call) // would block without the rule
	if !r.Result.IsError || !strings.Contains(resultText(r), "http") {
		t.Fatalf("always allowed: %+v", r)
	}

	// Activity shows each outcome.
	acts, _ := w.st.ListAgentActivity(context.Background(), boss.ID, 10)
	var outcomes []string
	for _, a := range acts {
		outcomes = append(outcomes, a.Outcome)
	}
	if !slices.Equal(outcomes, []string{"error", "error", "error", "denied"}) || acts[0].Agent != "Hermes" {
		t.Fatalf("activity: %v", outcomes)
	}
}

func TestMCPApprovalTimeout(t *testing.T) {
	w := newAuthWorld(t)
	w.s.agents.broker = &mcp.Broker{Timeout: 50 * time.Millisecond}
	_ = w.st.SetSetting(context.Background(), agentsEnabledKey, "1")
	tok, _ := makeAgentToken(t, w, w.users["alex"], store.AgentScopeAct)
	r := rpc(t, w.s, tok, callTool("run_command", map[string]any{"command": "id"}))
	if !r.Result.IsError || !strings.Contains(resultText(r), "2 minutes") {
		t.Fatalf("timeout: %+v", r)
	}
	acts, _ := w.st.ListAgentActivity(context.Background(), w.users["alex"].ID, 1)
	if len(acts) != 1 || acts[0].Outcome != "timeout" {
		t.Fatalf("activity: %+v", acts)
	}
}

func TestMCPFileSafety(t *testing.T) {
	w := agentWorld(t)
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	_ = os.MkdirAll(dataDir, 0o755)
	_ = os.WriteFile(filepath.Join(dataDir, "notes.txt"), []byte("hello"), 0o644)
	_ = os.WriteFile(filepath.Join(base, "secret.txt"), []byte("nope"), 0o644)
	fs, err := files.New([]files.Root{{ID: "data", Name: "Data", Path: dataDir}})
	if err != nil {
		t.Fatal(err)
	}
	w.s.Files = fs
	alex, _ := makeAgentToken(t, w, w.users["alex"], store.AgentScopeRead)
	boss, _ := makeAgentToken(t, w, w.users["boss"], store.AgentScopeRead)

	r := rpc(t, w.s, boss, callTool("read_file", map[string]any{"root": "data", "path": "/notes.txt"}))
	if r.Result.IsError || resultText(r) != "hello" {
		t.Fatalf("read: %+v", r)
	}
	// Escaping the location never works.
	for _, p := range []string{"../secret.txt", "/../../secret.txt", `..\secret.txt`} {
		r := rpc(t, w.s, boss, callTool("read_file", map[string]any{"root": "data", "path": p}))
		if !r.Result.IsError || strings.Contains(resultText(r), "nope") {
			t.Fatalf("%s: %+v", p, r)
		}
	}
	// People only read where they may.
	r = rpc(t, w.s, alex, callTool("read_file", map[string]any{"root": "data", "path": "/notes.txt"}))
	if !r.Result.IsError || !strings.Contains(resultText(r), "access") {
		t.Fatalf("non-admin read: %+v", r)
	}
	// Bad arguments never reach the tool.
	r = rpc(t, w.s, boss, callTool("read_file", map[string]any{"root": "data"}))
	if !r.Result.IsError || !strings.Contains(resultText(r), "invalid arguments") {
		t.Fatalf("missing path: %+v", r)
	}
}

func TestAgentToolSchemas(t *testing.T) {
	seen := map[string]bool{}
	for i := range agentTools {
		tool := &agentTools[i]
		d := tool.describe()
		if seen[d.Name] || d.Name == "" || d.Title == "" || d.Description == "" {
			t.Fatalf("tool %+v", d)
		}
		seen[d.Name] = true
		if d.InputSchema["type"] != "object" {
			t.Fatalf("%s schema %+v", d.Name, d.InputSchema)
		}
		if _, err := json.Marshal(d); err != nil {
			t.Fatalf("%s: %v", d.Name, err)
		}
		if d.Annotations["readOnlyHint"] != !tool.action {
			t.Fatalf("%s annotations %+v", d.Name, d.Annotations)
		}
		if tool.action && !strings.Contains(d.Description, "approval") {
			t.Fatalf("%s doesn't say it needs approval", d.Name)
		}
	}
	for _, want := range []string{"system_info", "list_files", "read_file", "search_files", "list_apps", "list_containers", "container_logs",
		"list_vms", "network_status", "storage_status", "screenshot", "write_file", "move_file", "delete_file", "app_action",
		"container_action", "vm_action", "run_command", "open_url", "type_text", "click"} {
		if !seen[want] {
			t.Fatalf("missing tool %s", want)
		}
	}
	for _, name := range []string{"system_info", "read_file", "list_files", "search_files", "screenshot"} {
		if findAgentTool(name).action {
			t.Fatalf("%s needs approval", name)
		}
	}
	for _, name := range []string{"write_file", "delete_file", "run_command", "click", "type_text", "open_url"} {
		if !findAgentTool(name).action {
			t.Fatalf("%s runs without approval", name)
		}
	}
}

func TestAgentsOverview(t *testing.T) {
	w := agentWorld(t)
	makeAgentToken(t, w, w.users["alex"], store.AgentScopeRead)
	rec := w.do(t, w.s.agentsOverview, w.users["alex"], "GET", "/api/v1/agents", nil)
	var out struct {
		Enabled   bool             `json:"enabled"`
		Loopback  bool             `json:"loopback_only"`
		CanManage bool             `json:"can_manage"`
		LocalURL  string           `json:"local_url"`
		Tokens    []agentTokenJSON `json:"tokens"`
		Tools     []agentToolJSON  `json:"tools"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusOK || !out.Enabled || !out.Loopback || out.CanManage || len(out.Tokens) != 1 || !strings.HasSuffix(out.LocalURL, "/mcp") {
		t.Fatalf("overview: %d %s", rec.Code, rec.Body)
	}
	for _, tl := range out.Tools {
		if tl.Admin {
			t.Fatalf("non-admin sees %s", tl.Name)
		}
	}
	if strings.Contains(rec.Body.String(), agentTokenPrefix) {
		t.Fatal("token shown again")
	}
	// The other person's tokens stay theirs.
	rec = w.do(t, w.s.agentsOverview, w.users["boss"], "GET", "/api/v1/agents", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Tokens) != 0 || !out.CanManage {
		t.Fatalf("boss overview: %s", rec.Body)
	}
	if rec := w.do(t, w.s.agentTokenCreate, w.users["boss"], "POST", "/api/v1/agents/tokens", map[string]string{"name": "x", "scope": "root"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad scope: %d", rec.Code)
	}
}
