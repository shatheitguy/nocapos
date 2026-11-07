package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type fakeBackend struct {
	tools []Tool
	calls []string
}

func (f *fakeBackend) Tools() []Tool { return f.tools }

func (f *fakeBackend) Call(_ context.Context, name string, args map[string]any) (Result, bool) {
	f.calls = append(f.calls, name)
	return Text("hi " + Str(args, "who")), true
}

func newFake() *fakeBackend {
	return &fakeBackend{tools: []Tool{
		{Name: "greet", Description: "says hi", InputSchema: Schema(Prop{Name: "who", Type: "string", Required: true, Description: "name"})},
	}}
}

type reply struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  map[string]any  `json:"result"`
	Error   *RPCError       `json:"error"`
}

func call(t *testing.T, b Backend, body string) reply {
	t.Helper()
	out := Handle(context.Background(), b, ServerInfo{Name: "nocap", Version: "1.0"}, []byte(body))
	if out == nil {
		t.Fatalf("no reply to %s", body)
	}
	var r reply
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("bad reply %s: %v", out, err)
	}
	if r.JSONRPC != "2.0" {
		t.Fatalf("jsonrpc %q", r.JSONRPC)
	}
	return r
}

func TestInitializeNegotiatesVersion(t *testing.T) {
	b := newFake()
	r := call(t, b, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"x","version":"1"}}}`)
	if r.Error != nil || r.Result["protocolVersion"] != "2025-06-18" || string(r.ID) != "1" {
		t.Fatalf("initialize: %+v", r)
	}
	caps, _ := r.Result["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Fatalf("no tools capability: %+v", r.Result)
	}
	info, _ := r.Result["serverInfo"].(map[string]any)
	if info["name"] != "nocap" {
		t.Fatalf("serverInfo %+v", info)
	}
	// Versions it doesn't know get the latest.
	r = call(t, b, `{"jsonrpc":"2.0","id":"a","method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
	if r.Result["protocolVersion"] != LatestProtocol || string(r.ID) != `"a"` {
		t.Fatalf("fallback: %+v", r)
	}
}

func TestNotificationsAndResponsesGetNoReply(t *testing.T) {
	b := newFake()
	for _, body := range []string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`,
		`{"jsonrpc":"2.0","id":5,"result":{}}`,
		`[{"jsonrpc":"2.0","method":"notifications/initialized"}]`,
	} {
		if out := Handle(context.Background(), b, ServerInfo{}, []byte(body)); out != nil {
			t.Fatalf("%s got reply %s", body, out)
		}
	}
}

func TestErrors(t *testing.T) {
	b := newFake()
	cases := []struct {
		body string
		code int
	}{
		{`{not json`, CodeParseError},
		{`{"jsonrpc":"1.0","id":1,"method":"ping"}`, CodeInvalidRequest},
		{`{"jsonrpc":"2.0","id":1}`, CodeInvalidRequest},
		{`{"jsonrpc":"2.0","id":null,"method":"ping"}`, CodeInvalidRequest},
		{`{"jsonrpc":"2.0","id":{"x":1},"method":"ping"}`, CodeInvalidRequest},
		{`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`, CodeMethodNotFound},
		{`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`, CodeInvalidParams},
		{`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope"}}`, CodeInvalidParams},
		{`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"greet","arguments":{"who":"x"}}}`, 0},
		{`[]`, CodeInvalidRequest},
	}
	for _, c := range cases {
		r := call(t, b, c.body)
		got := 0
		if r.Error != nil {
			got = r.Error.Code
		}
		if got != c.code {
			t.Errorf("%s: code %d, want %d", c.body, got, c.code)
		}
	}
}

func TestPingAndToolsList(t *testing.T) {
	b := newFake()
	if r := call(t, b, `{"jsonrpc":"2.0","id":7,"method":"ping"}`); r.Error != nil || r.Result == nil {
		t.Fatalf("ping: %+v", r)
	}
	r := call(t, b, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	tools, _ := r.Result["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools: %+v", r.Result)
	}
	tool := tools[0].(map[string]any)
	schema := tool["inputSchema"].(map[string]any)
	if tool["name"] != "greet" || schema["type"] != "object" {
		t.Fatalf("tool: %+v", tool)
	}
}

func TestToolsCall(t *testing.T) {
	b := newFake()
	r := call(t, b, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"greet","arguments":{"who":"bestie"}}}`)
	content := r.Result["content"].([]any)
	if r.Error != nil || content[0].(map[string]any)["text"] != "hi bestie" || r.Result["isError"] != nil {
		t.Fatalf("call: %+v", r)
	}
	// Bad arguments are a tool error the agent sees, and the tool never runs.
	b.calls = nil
	r = call(t, b, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"greet","arguments":{"who":3}}}`)
	if r.Result["isError"] != true || len(b.calls) != 0 {
		t.Fatalf("bad args: %+v calls %v", r, b.calls)
	}
	r = call(t, b, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"greet","arguments":{"who":"x","extra":1}}}`)
	if r.Result["isError"] != true {
		t.Fatalf("extra arg: %+v", r)
	}
}

func TestBatch(t *testing.T) {
	out := Handle(context.Background(), newFake(), ServerInfo{}, []byte(`[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":2,"method":"tools/list"}]`))
	var rs []reply
	if err := json.Unmarshal(out, &rs); err != nil || len(rs) != 2 {
		t.Fatalf("batch: %s", out)
	}
}

func TestValidateArgs(t *testing.T) {
	s := Schema(
		Prop{Name: "n", Type: "integer", Required: true},
		Prop{Name: "f", Type: "number"},
		Prop{Name: "b", Type: "boolean"},
		Prop{Name: "e", Type: "string", Enum: []string{"a", "b"}},
	)
	ok := []map[string]any{
		{"n": 1.0}, {"n": 2.0, "f": 0.5, "b": true, "e": "a"},
	}
	bad := []map[string]any{
		{}, {"n": 1.5}, {"n": "1"}, {"n": 1.0, "b": "yes"}, {"n": 1.0, "e": "c"}, {"n": 1.0, "zz": 1.0},
	}
	for _, a := range ok {
		if err := ValidateArgs(s, a); err != nil {
			t.Errorf("%v: %v", a, err)
		}
	}
	for _, a := range bad {
		if err := ValidateArgs(s, a); err == nil {
			t.Errorf("%v: accepted", a)
		}
	}
	if !strings.Contains(ValidateArgs(s, map[string]any{}).Error(), `"n"`) {
		t.Error("missing arg not named")
	}
}

func TestCheckURL(t *testing.T) {
	for _, u := range []string{"https://example.com/x", "http://10.0.0.2:8080/"} {
		if _, err := CheckURL(u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, u := range []string{"file:///etc/passwd", "javascript:alert(1)", "ftp://x", "https://", "notaurl"} {
		if _, err := CheckURL(u); err == nil {
			t.Errorf("%s accepted", u)
		}
	}
}
