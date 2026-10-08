package docker

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ExecConn is a raw, bidirectional connection to a container process. With a
// TTY the stream is unmultiplexed: read stdout/stderr, write stdin.
type ExecConn struct {
	conn net.Conn
	br   *bufio.Reader
	id   string
}

func (e *ExecConn) Read(p []byte) (int, error)  { return e.br.Read(p) }
func (e *ExecConn) Write(p []byte) (int, error) { return e.conn.Write(p) }
func (e *ExecConn) Close() error                { return e.conn.Close() }
func (e *ExecConn) ExecID() string              { return e.id }

type execConfig struct {
	AttachStdin  bool     `json:"AttachStdin"`
	AttachStdout bool     `json:"AttachStdout"`
	AttachStderr bool     `json:"AttachStderr"`
	Tty          bool     `json:"Tty"`
	Cmd          []string `json:"Cmd"`
	Env          []string `json:"Env,omitempty"`
}

// Exec starts an interactive TTY exec in a container and returns a raw
// bidirectional stream. cmd is the shell to run (e.g. ["/bin/sh"]).
func (c *Client) Exec(ctx context.Context, container string, cmd []string) (*ExecConn, error) {
	version, err := c.ensure(ctx)
	if err != nil {
		return nil, err
	}
	cfg := execConfig{AttachStdin: true, AttachStdout: true, AttachStderr: true, Tty: true, Cmd: cmd,
		Env: []string{"TERM=xterm-256color"}}
	var created struct {
		ID string `json:"Id"`
	}
	if err := c.doBody(ctx, http.MethodPost, "/containers/"+url.PathEscape(container)+"/exec", nil, cfg, &created); err != nil {
		return nil, err
	}
	if created.ID == "" {
		return nil, fmt.Errorf("docker: exec create returned no id")
	}
	return c.hijack(ctx, version, created.ID)
}

// hijack opens the exec stream by writing the upgrade request on a raw
// connection (net/http does not expose the underlying conn for bidirectional use).
func (c *Client) hijack(ctx context.Context, version, execID string) (*ExecConn, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	host := strings.TrimPrefix(c.base, "http://")
	body := `{"Tty":true}`
	req := "POST /v" + version + "/exec/" + execID + "/start HTTP/1.1\r\n" +
		"Host: " + host + "\r\n" +
		"Content-Type: application/json\r\n" +
		"Connection: Upgrade\r\n" +
		"Upgrade: tcp\r\n" +
		"Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		conn.Close()
		return nil, err
	}
	// 101 Switching Protocols (upgraded) or 200 (some engines attach directly).
	if resp.StatusCode != http.StatusSwitchingProtocols && resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, &APIError{Status: resp.StatusCode, Message: "exec start failed"}
	}
	return &ExecConn{conn: conn, br: br, id: execID}, nil
}

// ExecResize tells the engine the terminal's new size so programs render correctly.
func (c *Client) ExecResize(ctx context.Context, execID string, cols, rows int) error {
	q := url.Values{"h": {strconv.Itoa(rows)}, "w": {strconv.Itoa(cols)}}
	return c.do(ctx, http.MethodPost, "/exec/"+url.PathEscape(execID)+"/resize", q, nil)
}

// DetectShell returns the first usable shell in a container (bash, then sh).
func (c *Client) DetectShell(ctx context.Context, container string) string {
	for _, sh := range []string{"/bin/bash", "/bin/sh"} {
		if c.shellWorks(ctx, container, sh) {
			return sh
		}
	}
	return "/bin/sh"
}

func (c *Client) shellWorks(ctx context.Context, container, sh string) bool {
	cfg := execConfig{Cmd: []string{sh, "-c", "exit 0"}}
	var created struct {
		ID string `json:"Id"`
	}
	if err := c.doBody(ctx, http.MethodPost, "/containers/"+url.PathEscape(container)+"/exec", nil, cfg, &created); err != nil || created.ID == "" {
		return false
	}
	if err := c.doBody(ctx, http.MethodPost, "/exec/"+created.ID+"/start", nil, map[string]bool{"Detach": true, "Tty": false}, nil); err != nil {
		return false
	}
	var inspect struct {
		Running  bool `json:"Running"`
		ExitCode int  `json:"ExitCode"`
	}
	if err := c.do(ctx, http.MethodGet, "/exec/"+created.ID+"/json", nil, &inspect); err != nil {
		return false
	}
	return !inspect.Running && inspect.ExitCode == 0
}
