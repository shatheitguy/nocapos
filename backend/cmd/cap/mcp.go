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
	"path/filepath"
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
	if len(args) > 0 && args[0] == "pair" {
		return cmdMCPPair(args[1:], stdout, stderr)
	}
	b := &bridge{url: os.Getenv("NOCAP_MCP_URL"), token: os.Getenv("NOCAP_MCP_TOKEN"), out: stdout}
	tokenFile := ""
	insecure := false
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
		case a == "--insecure":
			insecure = true
		default:
			return fmt.Errorf("unknown option %q; usage: cap mcp [--url URL] [--token-file FILE] [--insecure] | cap mcp pair --url URL --code CODE", a)
		}
	}
	saved := tokenFile == "" && b.token == ""
	if saved {
		tokenFile = savedPath("mcp-token") // from cap mcp pair
	}
	if tokenFile != "" {
		raw, err := os.ReadFile(tokenFile)
		if err != nil && !saved {
			return fmt.Errorf("reading the token file: %w", err)
		}
		b.token = strings.TrimSpace(string(raw))
	}
	if b.token == "" {
		return errors.New("no agent token: set NOCAP_MCP_TOKEN, or pair with `cap mcp pair` (make one in NoCapOS → Settings → AI Agents)")
	}
	if b.url == "" && saved {
		if raw, err := os.ReadFile(savedPath("mcp-url")); err == nil {
			b.url = strings.TrimSpace(string(raw))
		}
	}
	if b.url == "" {
		b.url = defaultMCPURL
	}
	u, err := url.Parse(b.url)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("bad NoCap MCP address %q", b.url)
	}
	b.client = mcpClient(u, insecure)
	fmt.Fprintln(stderr, "cap mcp: forwarding to", b.url)
	return b.serve(stdin)
}

// savedPath is where cap mcp pair keeps the token and address ("" if
// there's no config folder).
func savedPath(name string) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "nocap", name)
}

// cmdMCPPair swaps a pairing code from Settings → AI Agents for this
// agent's token and saves it (readable only by you) for cap mcp.
func cmdMCPPair(args []string, stdout, stderr io.Writer) error {
	var base, code string
	insecure := false
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
			base = next()
		case strings.HasPrefix(a, "--url="):
			base = strings.TrimPrefix(a, "--url=")
		case a == "--code":
			code = next()
		case strings.HasPrefix(a, "--code="):
			code = strings.TrimPrefix(a, "--code=")
		case a == "--insecure":
			insecure = true
		default:
			return fmt.Errorf("unknown option %q; usage: cap mcp pair --url https://your-server --code ABCD-EFGH [--insecure]", a)
		}
	}
	if base == "" || code == "" {
		return errors.New("usage: cap mcp pair --url https://your-server --code ABCD-EFGH [--insecure]")
	}
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("bad NoCapOS address %q", base)
	}
	u.Path = pairPath(u.Path)
	body, _ := json.Marshal(map[string]string{"code": code})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := mcpClient(u, insecure).Do(req)
	if err != nil {
		return fmt.Errorf("can't reach NoCapOS at %s: %w", u.Host, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Token string `json:"token"`
		Name  string `json:"name"`
		URL   string `json:"url"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusOK || out.Token == "" {
		if out.Error.Message != "" {
			return errors.New(out.Error.Message)
		}
		return fmt.Errorf("NoCapOS answered HTTP %d", resp.StatusCode)
	}
	tokPath, urlPath := savedPath("mcp-token"), savedPath("mcp-url")
	if tokPath == "" {
		fmt.Fprintln(stdout, out.Token)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(tokPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(tokPath, []byte(out.Token+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(urlPath, []byte(out.URL+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "Paired as %q. Token saved to %s; `cap mcp` now connects to %s.\n", out.Name, tokPath, out.URL)
	if insecure && !isLoopback(u) {
		fmt.Fprintln(stderr, "This server's certificate wasn't checked: run `cap mcp --insecure` to connect the same way.")
	}
	return nil
}

func isLoopback(u *url.URL) bool {
	ip := net.ParseIP(u.Hostname())
	return u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()
}

// pairPath turns what someone pasted (the server, its /mcp address or its
// /mcp/pair address) into the pairing address.
func pairPath(p string) string {
	p = strings.TrimRight(p, "/")
	p = strings.TrimSuffix(p, "/mcp/pair")
	p = strings.TrimSuffix(p, "/mcp")
	return strings.TrimRight(p, "/") + "/mcp/pair"
}

// mcpClient trusts NoCapOS's own self-signed certificate on loopback (the
// traffic never leaves this machine), or anywhere when asked to with
// --insecure; otherwise TLS is checked.
func mcpClient(u *url.URL, insecure bool) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if u.Scheme == "https" && (insecure || isLoopback(u)) {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // loopback, or the person asked for it
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
