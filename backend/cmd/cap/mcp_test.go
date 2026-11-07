package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestMCPBridge(t *testing.T) {
	var (
		mu       sync.Mutex
		versions []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ncmcp_good" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"mcp","message":"this agent token was revoked"}}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &m)
		mu.Lock()
		versions = append(versions, m.Method+"="+r.Header.Get("MCP-Protocol-Version"))
		mu.Unlock()
		switch {
		case len(m.ID) == 0:
			w.WriteHeader(http.StatusAccepted)
		case m.Method == "initialize":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(m.ID) + `,"result":{"protocolVersion":"2025-06-18"}}`))
		default:
			_, _ = w.Write([]byte("{\"jsonrpc\":\"2.0\",\"id\":" + string(m.ID) + ",\"result\":{}}\n"))
		}
	}))
	defer srv.Close()

	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		``,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
	}, "\n")
	var out bytes.Buffer
	t.Setenv("NOCAP_MCP_TOKEN", "ncmcp_good")
	if err := cmdMCP([]string{"--url", srv.URL + "/mcp"}, strings.NewReader(in), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"id":1`) || !strings.Contains(lines[1], `"id":2`) {
		t.Fatalf("output:\n%s", out.String())
	}
	mu.Lock()
	defer mu.Unlock()
	for _, v := range versions[1:] {
		if !strings.HasSuffix(v, "=2025-06-18") {
			t.Fatalf("protocol header not sent after initialize: %v", versions)
		}
	}

	// HTTP failures come back as JSON-RPC errors for that request.
	out.Reset()
	t.Setenv("NOCAP_MCP_TOKEN", "ncmcp_bad")
	if err := cmdMCP([]string{"--url=" + srv.URL + "/mcp"}, strings.NewReader(`{"jsonrpc":"2.0","id":"x","method":"ping"}`), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var e struct {
		ID    string `json:"id"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &e); err != nil || e.ID != "x" || !strings.Contains(e.Error.Message, "revoked") {
		t.Fatalf("error line %q: %v", out.String(), err)
	}

	// No token, no start.
	t.Setenv("NOCAP_MCP_TOKEN", "")
	if err := cmdMCP(nil, strings.NewReader(""), &out, io.Discard); err == nil {
		t.Fatal("started without a token")
	}
}
