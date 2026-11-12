package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestPairPath(t *testing.T) {
	for in, want := range map[string]string{
		"": "/mcp/pair", "/": "/mcp/pair", "/mcp": "/mcp/pair", "/mcp/": "/mcp/pair",
		"/mcp/pair": "/mcp/pair", "/nocap": "/nocap/mcp/pair", "/nocap/mcp/pair/": "/nocap/mcp/pair",
	} {
		if got := pairPath(in); got != want {
			t.Fatalf("pairPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMCPPair(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AppData", dir)
	t.Setenv("HOME", dir)
	t.Setenv("NOCAP_MCP_TOKEN", "")
	t.Setenv("NOCAP_MCP_URL", "")
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mcp/pair":
			var req struct{ Code string }
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Code != "ABCD-EFGH" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"That pairing code is wrong or has expired."}}`))
				return
			}
			_, _ = w.Write([]byte(`{"token":"ncmcp_paired","name":"Laptop","url":"` + srv.URL + `/mcp"}`))
		case "/mcp":
			if r.Header.Get("Authorization") != "Bearer ncmcp_paired" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	if err := cmdMCP([]string{"pair", "--url", srv.URL, "--code", "WRONG-ONE"}, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("wrong code: %v", err)
	}
	if err := cmdMCP([]string{"pair", "--url", srv.URL + "/mcp", "--code=ABCD-EFGH"}, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	p := savedPath("mcp-token")
	raw, err := os.ReadFile(p)
	if err != nil || strings.TrimSpace(string(raw)) != "ncmcp_paired" {
		t.Fatalf("saved token: %q %v", raw, err)
	}
	if st, err := os.Stat(p); err == nil && st.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("token file is readable by others: %v", st.Mode())
	}
	// cap mcp then uses the saved token and address.
	var out strings.Builder
	if err := cmdMCP(nil, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"result"`) {
		t.Fatalf("bridge with the saved token: %s", out.String())
	}
}
