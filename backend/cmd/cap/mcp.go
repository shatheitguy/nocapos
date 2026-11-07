package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

// cap mcp: NoCap MCP over stdio, for AI agents that launch MCP servers as a
// command. Each JSON-RPC line on stdin is forwarded to the local NoCapOS
// (/mcp, with the agent token) and the answer is written to stdout as one
// line. Messages are handled side by side: an action waiting for approval
// doesn't hold up pings or reads.

const defaultMCPURL = "http://127.0.0.1:8780/mcp"

type bridge struct {
	url     string
	token   string
	client  *http.Client
	version string // MCP-Protocol-Version, once initialize agreed one

	mu  sync.Mutex
	out io.Writer
}

func cmdMCP(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	b := &bridge{url: os.Getenv("NOCAP_MCP_URL"), token: os.Getenv("NOCAP_MCP_TOKEN"), out: stdout}
	tokenFile := ""
	for i := 0; i < len(args); i++ {
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch a := args[i]; {
		case a == "--url":
			b.url = next()
		case strings.HasPrefix(a, "--url="):
			b.url = strings.TrimPrefix(a, "--url=")
		case a == "--token-file":
			tokenFile = next()
		case strings.HasPrefix(a, "--token-file="):
			tokenFile = strings.TrimPrefix(a, "--token-file=")
		default:
			return fmt.Errorf("unknown option %q; usage: cap mcp [--url URL] [--token-file FILE]", a)
		}
	}
	if tokenFile != "" {
		raw, err := os.ReadFile(tokenFile)
		if err != nil {
			return fmt.Errorf("reading the token file: %w", err)
		}
		b.token = strings.TrimSpace(string(raw))
	}
	if b.token == "" {
		return errors.New("no agent token: set NOCAP_MCP_TOKEN (make one in NoCapOS → Settings → AI Agents)")
	}
	if b.url == "" {
		b.url = defaultMCPURL
	}
	u, err := url.Parse(b.url)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("bad NoCap MCP address %q", b.url)
	}
	b.client = mcpClient(u)
	fmt.Fprintln(stderr, "cap mcp: forwarding to", b.url)
	return b.serve(stdin)
}

// mcpClient trusts NoCapOS's own self-signed certificate only on loopback
// (the traffic never leaves this machine); anywhere else TLS is checked.
func mcpClient(u *url.URL) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if ip := net.ParseIP(u.Hostname()); u.Scheme == "https" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()) {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // loopback only
	}
	// No overall timeout: an action can wait minutes for approval.
	return &http.Client{Transport: tr}
}

func (b *bridge) serve(stdin io.Reader) error {
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	var wg sync.WaitGroup
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		msg := append([]byte(nil), line...)
		// initialize settles the protocol version: handle it before anything else.
		if isInitialize(msg) {
			b.forward(msg)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.forward(msg)
		}()
	}
	wg.Wait()
	return sc.Err()
}

type rpcHead struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
}

func isInitialize(msg []byte) bool {
	var h rpcHead
	return json.Unmarshal(msg, &h) == nil && h.Method == "initialize"
}

func (b *bridge) forward(msg []byte) {
	var h rpcHead
	_ = json.Unmarshal(msg, &h)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, b.url, bytes.NewReader(msg))
	if err != nil {
		b.fail(h.ID, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+b.token)
	if v := b.protocol(); v != "" {
		req.Header.Set("MCP-Protocol-Version", v)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		b.fail(h.ID, "can't reach NoCapOS at "+b.url+": "+err.Error())
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	switch {
	case resp.StatusCode == http.StatusAccepted:
		return
	case resp.StatusCode >= 400:
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		m := fmt.Sprintf("NoCapOS answered HTTP %d", resp.StatusCode)
		if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
			m = e.Error.Message
		}
		b.fail(h.ID, m)
		return
	}
	if h.Method == "initialize" {
		var r struct {
			Result struct {
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"result"`
		}
		if json.Unmarshal(body, &r) == nil && r.Result.ProtocolVersion != "" {
			b.mu.Lock()
			b.version = r.Result.ProtocolVersion
			b.mu.Unlock()
		}
	}
	b.write(bytes.TrimSpace(body))
}

func (b *bridge) protocol() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.version
}

// fail answers a request (not a notification) with a JSON-RPC error.
func (b *bridge) fail(id json.RawMessage, msg string) {
	if len(id) == 0 || string(id) == "null" {
		fmt.Fprintln(os.Stderr, "cap mcp:", msg)
		return
	}
	out, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": -32603, "message": msg},
	})
	b.write(out)
}

func (b *bridge) write(line []byte) {
	if len(line) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	_, _ = b.out.Write(append(line, '\n'))
}
