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

const (
	toolsList = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	pingMsg   = `{"jsonrpc":"2.0","id":1,"method":"ping"}`
)

// doAt runs a handler as someone, with path values.
func doAt(t *testing.T, h http.HandlerFunc, as *store.User, method, target string, body any, pv map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, target, strings.NewReader(string(b)))
	r.Header.Set("Content-Type", "application/json")
	for k, v := range pv {
		r.SetPathValue(k, v)
	}
	if as != nil {
		r = r.WithContext(context.WithValue(r.Context(), userKey, as))
	}
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

func notAllowed(r rpcReply) bool { return r.Error != nil && r.Error.Code == mcp.CodeInvalidParams }

func TestAgentScopesDefaultAndValidation(t *testing.T) {
	w := agentWorld(t)
	boss := w.users["boss"]
	// Nothing asked for: nothing allowed.
	tok, _ := makeAgent(t, w, boss, map[string]any{"name": "Blank"})
	if names := toolNames(rpc(t, w.s, tok, toolsList)); len(names) != 0 {
		t.Fatalf("default agent sees %v", names)
	}
	if r := rpc(t, w.s, tok, callTool("system_info", nil)); !notAllowed(r) {
		t.Fatalf("default agent called a tool: %+v", r)
	}
	for _, body := range []map[string]any{
		{"name": "x", "scopes": map[string]any{"levels": map[string]string{"root": "on"}}},
		{"name": "x", "scopes": map[string]any{"levels": map[string]string{"files": "everything"}}},
		{"name": "x", "scopes": map[string]any{"levels": map[string]string{"files": "read"}, "folders": []string{"nope"}}},
		{"name": "x", "preset": "god_mode"},
		{"name": "x", "kind": "assistant"},
		{"name": "x", "ip_allow": []string{"example.com"}},
		{"name": "x", "expires_in_days": -1},
		{"name": ""},
	} {
		if rec := w.do(t, w.s.agentTokenCreate, boss, "POST", "/api/v1/agents/tokens", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%v: %d %s", body, rec.Code, rec.Body)
		}
	}
}

func TestAgentScopesEnforced(t *testing.T) {
	w := agentWorld(t)
	boss := w.users["boss"]
	tok, id := makeAgent(t, w, boss, map[string]any{"name": "Reader", "scopes": map[string]any{
		"levels": map[string]string{"system": "view", "code": "sandbox"},
		"auto":   []string{"code", "power"},
	}})
	names := toolNames(rpc(t, w.s, tok, toolsList))
	if !slices.Equal(names, []string{"system_info", "storage_status", "run_code"}) {
		t.Fatalf("tools: %v", names)
	}
	for _, n := range []string{"set_hostname", "read_file", "run_command", "power", "list_users"} {
		if r := rpc(t, w.s, tok, callTool(n, map[string]any{"hostname": "x", "root": "a", "path": "/b", "command": "id", "action": "reboot"})); !notAllowed(r) {
			t.Fatalf("%s ran: %+v", n, r)
		}
	}
	// Sandbox only: host code is refused (code runs without asking here).
	r := rpc(t, w.s, tok, callTool("run_code", map[string]any{"language": "sh", "code": "id", "where": "host"}))
	if !r.Result.IsError || !strings.Contains(resultText(r), "sandbox") {
		t.Fatalf("host code: %+v", r)
	}
	// Lowering the scopes takes effect on the next call.
	if rec := patchToken(t, w, boss, id, map[string]any{"preset": "off"}); rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	if names := toolNames(rpc(t, w.s, tok, toolsList)); len(names) != 0 {
		t.Fatalf("after off: %v", names)
	}
	// Power always asks: it can't be relaxed or always allowed.
	if rec := patchToken(t, w, boss, id, map[string]any{"scopes": map[string]any{
		"levels": map[string]string{"power": "on"}, "auto": []string{"power"}}}); rec.Code != http.StatusOK {
		t.Fatalf("patch power: %d", rec.Code)
	}
	cur, _ := w.st.AgentTokenByID(context.Background(), id, boss.ID)
	if sc := agentScopes(cur); slices.Contains(sc.Auto, mcp.CapPower) || !sc.Asks(mcp.CapPower) {
		t.Fatalf("power relaxed: %+v", sc)
	}
	if rec := patchToken(t, w, boss, id, map[string]any{"always_allow": []string{"power"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("always allow power: %d", rec.Code)
	}
	// Non-admins can't reach administrators' tools whatever their scopes say.
	alex, _ := makeAgent(t, w, w.users["alex"], map[string]any{"name": "A", "preset": "full"})
	for _, n := range toolNames(rpc(t, w.s, alex, toolsList)) {
		if findAgentTool(n).admin {
			t.Fatalf("non-admin sees %s", n)
		}
	}
	// A broken saved profile allows nothing.
	cur.Scopes = `{"levels":{"files":"read","nope":"on"}}`
	_ = w.st.UpdateAgentProfile(context.Background(), cur)
	if names := toolNames(rpc(t, w.s, tok, toolsList)); len(names) != 0 {
		t.Fatalf("broken profile: %v", names)
	}
}

func TestAgentAutoAndFolders(t *testing.T) {
	w := agentWorld(t)
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	_ = os.MkdirAll(filepath.Join(dataDir, "Docs"), 0o755)
	_ = os.MkdirAll(filepath.Join(dataDir, "Private"), 0o755)
	_ = os.WriteFile(filepath.Join(dataDir, "Docs", "a.txt"), []byte("in"), 0o644)
	_ = os.WriteFile(filepath.Join(dataDir, "Private", "b.txt"), []byte("out"), 0o644)
	fs, err := files.New([]files.Root{{ID: "data", Name: "Data", Path: dataDir}, {ID: "other", Name: "Other", Path: base}})
	if err != nil {
		t.Fatal(err)
	}
	w.s.Files = fs
	boss := w.users["boss"]
	tok, _ := makeAgent(t, w, boss, map[string]any{"name": "Docs", "scopes": map[string]any{
		"levels": map[string]string{"files": "write"}, "folders": []string{"data:/Docs"}, "auto": []string{"files"},
	}})
	read := func(p string) rpcReply {
		return rpc(t, w.s, tok, callTool("read_file", map[string]any{"root": "data", "path": p}))
	}
	if r := read("/Docs/a.txt"); r.Result.IsError || resultText(r) != "in" {
		t.Fatalf("inside: %+v", r)
	}
	for _, p := range []string{"/Private/b.txt", "/Docs/../Private/b.txt", `\Docs\..\Private\b.txt`, "/DocsX"} {
		if r := read(p); !r.Result.IsError || strings.Contains(resultText(r), "out") || !strings.Contains(resultText(r), "folders") {
			t.Fatalf("%s: %+v", p, r)
		}
	}
	// Locations outside the folders aren't even listed.
	r := rpc(t, w.s, tok, callTool("list_files", nil))
	if r.Result.IsError || !strings.Contains(resultText(r), `"data"`) || strings.Contains(resultText(r), `"other"`) {
		t.Fatalf("roots: %+v", r)
	}
	// Relaxed: writes inside run straight away (no approval card)...
	r = rpc(t, w.s, tok, callTool("write_file", map[string]any{"root": "data", "path": "/Docs/new.txt", "content": "hi"}))
	if r.Result.IsError {
		t.Fatalf("auto write: %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(dataDir, "Docs", "new.txt")); string(b) != "hi" {
		t.Fatalf("written: %q", b)
	}
	// ...and outside they're refused, moves included.
	r = rpc(t, w.s, tok, callTool("write_file", map[string]any{"root": "data", "path": "/Private/x.txt", "content": "hi"}))
	if !r.Result.IsError {
		t.Fatalf("write outside: %+v", r)
	}
	r = rpc(t, w.s, tok, callTool("move_file", map[string]any{"root": "data", "path": "/Docs/a.txt", "to_folder": "/Private"}))
	if !r.Result.IsError {
		t.Fatalf("move outside: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "Docs", "a.txt")); err != nil {
		t.Fatal("moved anyway")
	}
	r = rpc(t, w.s, tok, callTool("delete_file", map[string]any{"root": "other", "path": "/data/Private/b.txt"}))
	if !r.Result.IsError {
		t.Fatalf("delete in another location: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "Private", "b.txt")); err != nil {
		t.Fatal("deleted anyway")
	}
	// Photo tools keep to the folders too.
	r = rpc(t, w.s, tok, callTool("delete_photo", map[string]any{"root": "data", "path": "/Private/b.txt"}))
	if !notAllowed(r) { // photos are off for this agent
		t.Fatalf("photo tool without photos: %+v", r)
	}
}

func TestAgentPhotoFolders(t *testing.T) {
	w := agentWorld(t)
	boss := w.users["boss"]
	tok, _ := makeAgent(t, w, boss, map[string]any{"name": "Pics", "scopes": map[string]any{
		"levels": map[string]string{"photos": "write"}, "folders": []string{"data:/Pictures"}, "auto": []string{"photos"},
	}})
	for _, n := range []string{"delete_photo", "favorite_photo"} {
		r := rpc(t, w.s, tok, callTool(n, map[string]any{"root": "data", "path": "/Private/x.jpg"}))
		if !r.Result.IsError || !strings.Contains(resultText(r), "folders") {
			t.Fatalf("%s outside the folders: %+v", n, r)
		}
	}
}

func TestAgentReachExpiryPause(t *testing.T) {
	w := agentWorld(t)
	ctx := context.Background()
	boss := w.users["boss"]
	_ = w.st.SetSetting(ctx, agentsLoopbackKey, "0")
	tok, id := makeAgent(t, w, boss, map[string]any{"name": "Remote", "kind": "remote", "scope": "read", "ip_allow": []string{"10.0.0.0/8"}})
	if rec := mcpPost(w.s, mcpReq{token: tok, remote: "10.1.2.3:5000", body: pingMsg}); rec.Code != http.StatusOK {
		t.Fatalf("allowlisted: %d %s", rec.Code, rec.Body)
	}
	for _, addr := range []string{"192.168.1.9:5000", "127.0.0.1:5000"} {
		if rec := mcpPost(w.s, mcpReq{token: tok, remote: addr, body: pingMsg}); rec.Code != http.StatusForbidden {
			t.Fatalf("%s: %d", addr, rec.Code)
		}
	}
	if rec := patchToken(t, w, boss, id, map[string]any{"ip_allow": []string{}}); rec.Code != http.StatusOK {
		t.Fatalf("clear allowlist: %d", rec.Code)
	}
	if rec := mcpPost(w.s, mcpReq{token: tok, remote: "192.168.1.9:5000", body: pingMsg}); rec.Code != http.StatusOK {
		t.Fatalf("no allowlist: %d", rec.Code)
	}

	// Paused, then resumed.
	if rec := patchToken(t, w, boss, id, map[string]any{"paused": true}); rec.Code != http.StatusOK {
		t.Fatalf("pause: %d", rec.Code)
	}
	if rec := mcpPost(w.s, mcpReq{token: tok, body: pingMsg}); rec.Code != http.StatusForbidden {
		t.Fatalf("paused: %d", rec.Code)
	}
	_ = patchToken(t, w, boss, id, map[string]any{"paused": false})
	if rec := mcpPost(w.s, mcpReq{token: tok, body: pingMsg}); rec.Code != http.StatusOK {
		t.Fatalf("resumed: %d", rec.Code)
	}

	// Expired.
	cur, _ := w.st.AgentTokenByID(ctx, id, boss.ID)
	cur.ExpiresAt = time.Now().Add(-time.Minute)
	_ = w.st.UpdateAgentProfile(ctx, cur)
	if rec := mcpPost(w.s, mcpReq{token: tok, body: pingMsg}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired: %d", rec.Code)
	}
	if rec := patchToken(t, w, boss, id, map[string]any{"expires_in_days": 7}); rec.Code != http.StatusOK {
		t.Fatalf("renew: %d", rec.Code)
	}
	if rec := mcpPost(w.s, mcpReq{token: tok, body: pingMsg}); rec.Code != http.StatusOK {
		t.Fatalf("renewed: %d", rec.Code)
	}

	// Rotating: the old token stops at once.
	rec := doAt(t, w.s.agentTokenRotate, boss, "POST", "/x", map[string]any{}, map[string]string{"id": id})
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusOK || out.Token == "" || out.Token == tok {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body)
	}
	if rec := mcpPost(w.s, mcpReq{token: tok, body: pingMsg}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old token: %d", rec.Code)
	}
	if rec := mcpPost(w.s, mcpReq{token: out.Token, body: pingMsg}); rec.Code != http.StatusOK {
		t.Fatalf("new token: %d", rec.Code)
	}
	if rec := doAt(t, w.s.agentTokenRotate, w.users["alex"], "POST", "/x", map[string]any{}, map[string]string{"id": id}); rec.Code != http.StatusNotFound {
		t.Fatalf("someone else rotated it: %d", rec.Code)
	}
}

func pairPost(s *Server, code string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/mcp/pair", strings.NewReader(`{"code":"`+code+`"}`))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "127.0.0.1:50000"
	rec := httptest.NewRecorder()
	s.mcpPair(rec, r)
	return rec
}

func TestAgentPairing(t *testing.T) {
	w := agentWorld(t)
	boss := w.users["boss"]
	rec := w.do(t, w.s.agentTokenCreate, boss, "POST", "/api/v1/agents/tokens", map[string]any{"name": "Pal", "preset": "read_only", "pairing": true})
	var out struct {
		Code  string         `json:"code"`
		Token string         `json:"token"`
		Agent agentTokenJSON `json:"agent"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusCreated || len(out.Code) != 9 || out.Token != "" || !out.Agent.Pairing {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	if rec := pairPost(w.s, "AAAA-AAAA"); rec.Code != http.StatusBadRequest {
		t.Fatalf("wrong code: %d", rec.Code)
	}
	// Lower case and no dash work too.
	rec = pairPost(w.s, strings.ToLower(strings.ReplaceAll(out.Code, "-", "")))
	var got struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != http.StatusOK || !strings.HasPrefix(got.Token, agentTokenPrefix) {
		t.Fatalf("pair: %d %s", rec.Code, rec.Body)
	}
	if rec := pairPost(w.s, out.Code); rec.Code != http.StatusBadRequest {
		t.Fatalf("code used twice: %d", rec.Code)
	}
	if names := toolNames(rpc(t, w.s, got.Token, toolsList)); !slices.Contains(names, "read_file") || slices.Contains(names, "write_file") {
		t.Fatalf("paired tools: %v", names)
	}
	// A new code: the old token stops.
	rec = doAt(t, w.s.agentTokenRotate, boss, "POST", "/x", map[string]any{"pairing": true}, map[string]string{"id": out.Agent.ID})
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusOK || out.Code == "" {
		t.Fatalf("new code: %d %s", rec.Code, rec.Body)
	}
	if rec := mcpPost(w.s, mcpReq{token: got.Token, body: pingMsg}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("token after re-pairing: %d", rec.Code)
	}
	// Off means no pairing either.
	_ = w.st.SetSetting(context.Background(), agentsEnabledKey, "0")
	if rec := pairPost(w.s, out.Code); rec.Code != http.StatusForbidden {
		t.Fatalf("pairing while off: %d", rec.Code)
	}
	_ = w.st.SetSetting(context.Background(), agentsEnabledKey, "1")
	// An expired code doesn't work.
	if err := w.st.SetAgentSecret(context.Background(), out.Agent.ID, boss.ID, "pending:x", pairHash(out.Code), time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if rec := pairPost(w.s, out.Code); rec.Code != http.StatusBadRequest {
		t.Fatalf("expired code: %d", rec.Code)
	}
}

func TestAgentsStopAll(t *testing.T) {
	w := agentWorld(t)
	ctx := context.Background()
	boss, alex := w.users["boss"], w.users["alex"]
	bt, _ := makeAgentToken(t, w, boss, store.AgentScopeAct)
	at, _ := makeAgentToken(t, w, alex, store.AgentScopeAct)

	// A waiting action is denied by Stop all agents.
	res := asyncRPC(t, w.s, at, callTool("run_command", map[string]any{"command": "id"}))
	for i := 0; i < 300 && len(w.s.agentBroker().Pending(alex.ID)) == 0; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	rec := w.do(t, w.s.agentsStopAll, alex, "POST", "/api/v1/agents/stop", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"denied":1`) {
		t.Fatalf("stop own: %d %s", rec.Code, rec.Body)
	}
	if r := <-res; !r.Result.IsError {
		t.Fatalf("stopped call ran: %+v", r)
	}
	// Someone's own stop leaves other people's agents alone.
	if rec := mcpPost(w.s, mcpReq{token: at, body: pingMsg}); rec.Code != http.StatusForbidden {
		t.Fatalf("alex's agent after stop: %d", rec.Code)
	}
	if rec := mcpPost(w.s, mcpReq{token: bt, body: pingMsg}); rec.Code != http.StatusOK {
		t.Fatalf("boss's agent after alex's stop: %d", rec.Code)
	}
	// An administrator's stops everyone's and switches NoCap MCP off.
	if rec := w.do(t, w.s.agentsStopAll, boss, "POST", "/api/v1/agents/stop", nil); rec.Code != http.StatusOK {
		t.Fatalf("stop all: %d", rec.Code)
	}
	if on, _ := w.s.agentSettings(ctx); on {
		t.Fatal("still on")
	}
	_ = w.st.SetSetting(ctx, agentsEnabledKey, "1")
	if rec := mcpPost(w.s, mcpReq{token: bt, body: pingMsg}); rec.Code != http.StatusForbidden {
		t.Fatalf("boss's agent after stop all: %d", rec.Code)
	}
}

func TestAIAssistantProfile(t *testing.T) {
	w := agentWorld(t)
	boss := w.users["boss"]
	rec := w.do(t, w.s.assistantTools, boss, "GET", "/api/v1/agents/assistant/tools", nil)
	var out struct {
		Tools []mcp.Tool `json:"tools"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusOK || len(out.Tools) != 0 {
		t.Fatalf("assistant starts with everything off: %d %s", rec.Code, rec.Body)
	}
	call := func() *httptest.ResponseRecorder {
		return w.do(t, w.s.assistantCall, boss, "POST", "/api/v1/agents/assistant/call", map[string]any{"name": "system_info"})
	}
	if rec := call(); rec.Code != http.StatusForbidden {
		t.Fatalf("assistant called a tool while off: %d", rec.Code)
	}
	asst, err := w.st.AgentAssistant(context.Background(), boss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec := patchToken(t, w, boss, asst.ID, map[string]any{"scopes": map[string]any{"levels": map[string]string{"system": "view"}}}); rec.Code != http.StatusOK {
		t.Fatalf("assistant scopes: %d %s", rec.Code, rec.Body)
	}
	// Allowed now (system info itself isn't available in tests, so it fails on its own terms).
	if rec := call(); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"content"`) {
		t.Fatalf("assistant call: %d %s", rec.Code, rec.Body)
	}
	// It isn't listed with the agents, can't be revoked or given a token.
	rec = w.do(t, w.s.agentsOverview, boss, "GET", "/api/v1/agents", nil)
	var ov struct {
		Tokens    []agentTokenJSON `json:"tokens"`
		Assistant agentTokenJSON   `json:"assistant"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &ov)
	if len(ov.Tokens) != 0 || ov.Assistant.ID != asst.ID || ov.Assistant.Scopes.Level(mcp.CapSystem) != mcp.View {
		t.Fatalf("overview: %s", rec.Body)
	}
	if rec := doAt(t, w.s.agentTokenRevoke, boss, "DELETE", "/x", nil, map[string]string{"id": asst.ID}); rec.Code != http.StatusNotFound {
		t.Fatalf("revoke assistant: %d", rec.Code)
	}
	if rec := doAt(t, w.s.agentTokenRotate, boss, "POST", "/x", map[string]any{}, map[string]string{"id": asst.ID}); rec.Code != http.StatusNotFound {
		t.Fatalf("token for the assistant: %d", rec.Code)
	}
	if _, err := w.st.AgentTokenByHash(context.Background(), "assistant:"+asst.ID); err == nil {
		t.Fatal("assistant found by its placeholder hash")
	}
	// Paused.
	_ = patchToken(t, w, boss, asst.ID, map[string]any{"paused": true})
	if rec := call(); rec.Code != http.StatusForbidden {
		t.Fatalf("paused assistant: %d", rec.Code)
	}
	// Another person's assistant is theirs.
	if rec := patchToken(t, w, w.users["alex"], asst.ID, map[string]any{"paused": false}); rec.Code != http.StatusNotFound {
		t.Fatalf("someone else's assistant: %d", rec.Code)
	}
}
