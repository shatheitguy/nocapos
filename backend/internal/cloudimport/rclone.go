package cloudimport

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Rclone runs the rclone command-line tool. Each run gets its own temporary
// config file (mode 0600, deleted afterwards) holding just one remote, "src".
type Rclone struct {
	Bin    string
	TmpDir string // where temporary configs go (inside NoCapOS's secrets folder)
}

func findRclone(tmpDir string) (*Rclone, error) {
	bin := os.Getenv("ALFA_RCLONE")
	if bin == "" {
		p, err := exec.LookPath("rclone")
		if err != nil {
			return nil, errors.New("rclone is not installed on this server")
		}
		bin = p
	}
	return &Rclone{Bin: bin, TmpDir: tmpDir}, nil
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Remote is one remote's settings (rclone config keys and values).
type Remote map[string]string

// writeConfig writes a config with the remote as "src"; the caller removes it.
func (r *Rclone) writeConfig(rem Remote) (string, error) {
	if err := os.MkdirAll(r.TmpDir, 0o700); err != nil {
		return "", err
	}
	keys := make([]string, 0, len(rem))
	for k, v := range rem {
		if strings.ContainsAny(k+v, "\r\n") || strings.ContainsAny(k, "[]= ") {
			return "", fmt.Errorf("invalid setting %q", k)
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("[src]\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s = %s\n", k, rem[k])
	}
	p := filepath.Join(r.TmpDir, "rclone-"+randHex(8)+".conf")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		return "", err
	}
	return p, nil
}

// readToken returns the token line from a config rclone may have updated.
func readToken(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == "token" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// run runs rclone against the remote. onErrLine (if set) gets stderr lines;
// it returns stdout, and the remote's token if rclone renewed it.
func (r *Rclone) run(ctx context.Context, rem Remote, args []string, stdin string, onErrLine func([]byte)) (out []byte, newToken string, err error) {
	conf := os.DevNull
	if rem != nil {
		if conf, err = r.writeConfig(rem); err != nil {
			return nil, "", err
		}
		defer os.Remove(conf)
	}
	cmd := exec.CommandContext(ctx, r.Bin, append([]string{"--config", conf}, args...)...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + r.TmpDir, "TMPDIR=" + os.TempDir()}
	cmd.WaitDelay = 10 * time.Second
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout bytes.Buffer
	var errTail []string
	cmd.Stdout = &stdout
	pipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, "", err
	}
	if err := cmd.Start(); err != nil {
		return nil, "", fmt.Errorf("start rclone: %w", err)
	}
	sc := bufio.NewScanner(pipe)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if onErrLine != nil {
			onErrLine(line)
		}
		errTail = append(errTail, string(line))
		if len(errTail) > 30 {
			errTail = errTail[1:]
		}
	}
	werr := cmd.Wait()
	if rem != nil {
		if t := readToken(conf); t != "" && t != rem["token"] {
			newToken = t
		}
	}
	if ctx.Err() != nil {
		return stdout.Bytes(), newToken, ctx.Err()
	}
	if werr != nil {
		return stdout.Bytes(), newToken, rcloneError(errTail, werr)
	}
	return stdout.Bytes(), newToken, nil
}

// rcloneError picks the most useful line of rclone's output.
func rcloneError(lines []string, err error) error {
	var msg string
	for i := len(lines) - 1; i >= 0 && msg == ""; i-- {
		l := strings.TrimSpace(lines[i])
		var j struct {
			Level string `json:"level"`
			Msg   string `json:"msg"`
		}
		if json.Unmarshal([]byte(l), &j) == nil {
			if j.Level == "error" || j.Level == "critical" {
				msg = j.Msg
			}
			continue
		}
		if strings.Contains(l, "ERROR") || strings.Contains(l, "Failed") || strings.Contains(l, "CRITICAL") {
			msg = l
		}
	}
	if msg == "" {
		return fmt.Errorf("rclone: %w", err)
	}
	// The usual failures, in plain words.
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "401 unauthorized") || strings.Contains(low, "invalid_grant") || strings.Contains(low, "unable to authenticate") || strings.Contains(low, "invalidaccesskeyid") || strings.Contains(low, "signaturedoesnotmatch"):
		return errors.New("the account refused the sign-in (check the user name, password or keys)")
	case strings.Contains(low, "403 forbidden") || strings.Contains(low, "accessdenied"):
		return errors.New("the account doesn't allow access to that")
	case strings.Contains(low, "no such host"):
		return errors.New("the server name couldn't be found")
	case strings.Contains(low, "connection refused") || strings.Contains(low, "i/o timeout") || strings.Contains(low, "no route to host"):
		return errors.New("the server can't be reached")
	case strings.Contains(low, "directory not found") || strings.Contains(low, "404 not found"):
		return errors.New("that folder doesn't exist in the account")
	}
	// Drop rclone's "2026/10/10 00:35:52 " timestamp.
	if len(msg) > 20 && msg[4] == '/' && msg[7] == '/' && msg[13] == ':' {
		msg = strings.TrimSpace(msg[20:])
	}
	// "2026/10/10 00:18:47 ERROR : x: msg" -> "x: msg"
	if i := strings.Index(msg, " : "); i >= 0 && i < 40 {
		msg = msg[i+3:]
	}
	if i := strings.Index(msg, "ERROR : "); i >= 0 {
		msg = msg[i+8:]
	}
	msg = strings.TrimSpace(msg)
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return errors.New(msg)
}

// Obscure turns a password into rclone's obscured form (passed on stdin).
func (r *Rclone) Obscure(ctx context.Context, pw string) (string, error) {
	out, _, err := r.run(ctx, nil, []string{"obscure", "-"}, pw+"\n", nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *Rclone) Version(ctx context.Context) (string, error) {
	out, _, err := r.run(ctx, nil, []string{"version"}, "", nil)
	if err != nil {
		return "", err
	}
	f := strings.Fields(string(out))
	if len(f) >= 2 {
		return strings.TrimPrefix(f[1], "v"), nil
	}
	return "", errors.New("unknown rclone version")
}

// Entry is a folder in a cloud account.
type Entry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Folders lists the folders directly inside dir of the remote.
func (r *Rclone) Folders(ctx context.Context, rem Remote, dir string) ([]Entry, string, error) {
	out, tok, err := r.run(ctx, rem, []string{"lsjson", "--dirs-only", "src:" + dir}, "", nil)
	if err != nil {
		return nil, tok, err
	}
	var items []struct {
		Path  string `json:"Path"`
		Name  string `json:"Name"`
		IsDir bool   `json:"IsDir"`
	}
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, tok, fmt.Errorf("read folder list: %w", err)
	}
	entries := make([]Entry, 0, len(items))
	for _, it := range items {
		p := it.Name
		if dir != "" {
			p = strings.TrimSuffix(dir, "/") + "/" + it.Name
		}
		entries = append(entries, Entry{Name: it.Name, Path: p})
	}
	sort.Slice(entries, func(i, j int) bool { return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name) })
	return entries, tok, nil
}

// Progress is a copy's status.
type Progress struct {
	Bytes      int64   `json:"bytes"`
	TotalBytes int64   `json:"total_bytes"`
	Files      int64   `json:"files"`
	TotalFiles int64   `json:"total_files"`
	Errors     int64   `json:"errors"`
	Speed      float64 `json:"speed"`
}

// Copy copies src:dir into dest, never deleting anything; progress is reported as it goes.
func (r *Rclone) Copy(ctx context.Context, rem Remote, dir, dest string, progress func(Progress)) (Progress, string, error) {
	var last Progress
	args := []string{"copy", "src:" + dir, dest, "--use-json-log", "--stats", "2s", "--stats-log-level", "NOTICE", "-v",
		"--transfers", "4", "--checkers", "8", "--create-empty-src-dirs"}
	_, tok, err := r.run(ctx, rem, args, "", func(line []byte) {
		var m struct {
			Stats *struct {
				Bytes          int64   `json:"bytes"`
				TotalBytes     int64   `json:"totalBytes"`
				Transfers      int64   `json:"transfers"`
				TotalTransfers int64   `json:"totalTransfers"`
				Errors         int64   `json:"errors"`
				Speed          float64 `json:"speed"`
			} `json:"stats"`
		}
		if json.Unmarshal(line, &m) != nil || m.Stats == nil {
			return
		}
		last = Progress{Bytes: m.Stats.Bytes, TotalBytes: m.Stats.TotalBytes, Files: m.Stats.Transfers, TotalFiles: m.Stats.TotalTransfers, Errors: m.Stats.Errors, Speed: m.Stats.Speed}
		if progress != nil {
			progress(last)
		}
	})
	return last, tok, err
}

// ---------- signing in (OAuth) ----------

// The sign-in runs "rclone authorize" on the server, which listens on the
// server's 127.0.0.1:53682. The browser can't reach that, so NoCapOS follows
// rclone's local link itself to find the provider's sign-in page, and after
// sign-in the user pastes the address their browser landed on (the
// 127.0.0.1:53682 page that couldn't load); NoCapOS passes it on to rclone.

const authPort = "53682"

var linkRe = regexp.MustCompile(`Please go to the following link: (\S+)`)

type authSession struct {
	id     string
	kind   string
	cancel context.CancelFunc
	stdout bytes.Buffer
	done   chan struct{}
	err    error
}

type authorizer struct {
	mu      sync.Mutex
	current *authSession
}

// start begins a sign-in and returns the provider's sign-in address.
func (a *authorizer) start(r *Rclone, kind string) (*authSession, string, error) {
	a.mu.Lock()
	if a.current != nil {
		a.current.cancel() // only one sign-in at a time: it owns the port
	}
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	s := &authSession{id: randHex(12), kind: kind, cancel: cancel, done: make(chan struct{})}
	cmd := exec.CommandContext(ctx, r.Bin, "--config", os.DevNull, "authorize", kind, "--auth-no-open-browser")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + r.TmpDir}
	cmd.Stdout = &s.stdout
	pipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, "", err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, "", fmt.Errorf("start rclone: %w", err)
	}
	linkCh := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(pipe)
		for sc.Scan() {
			if m := linkRe.FindStringSubmatch(sc.Text()); m != nil {
				select {
				case linkCh <- m[1]:
				default:
				}
			}
		}
		s.err = cmd.Wait()
		close(s.done)
	}()
	var link string
	select {
	case link = <-linkCh:
	case <-s.done:
		cancel()
		return nil, "", errors.New("rclone stopped before it was ready to sign in")
	case <-time.After(20 * time.Second):
		cancel()
		return nil, "", errors.New("rclone didn't start the sign-in in time")
	}
	consent, err := followLocal(link)
	if err != nil {
		cancel()
		return nil, "", err
	}
	a.mu.Lock()
	a.current = s
	a.mu.Unlock()
	return s, consent, nil
}

// followLocal reads where rclone's local sign-in page redirects to.
func followLocal(link string) (string, error) {
	u, err := url.Parse(link)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Port() != authPort {
		return "", fmt.Errorf("unexpected sign-in link from rclone: %s", link)
	}
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(link)
	if err != nil {
		return "", err
	}
	res.Body.Close()
	loc := res.Header.Get("Location")
	if !strings.HasPrefix(loc, "https://") {
		return "", errors.New("rclone didn't give a sign-in page")
	}
	return loc, nil
}

var (
	tokenStart = "--->"
	tokenEnd   = "<---End paste"
)

// finish passes the pasted address on to rclone and returns the token.
func (a *authorizer) finish(id, pasted string) (string, error) {
	a.mu.Lock()
	s := a.current
	a.mu.Unlock()
	if s == nil || s.id != id {
		return "", errors.New("this sign-in has expired; start again")
	}
	u, err := url.Parse(strings.TrimSpace(pasted))
	if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || u.Port() != authPort {
		return "", errors.New("paste the whole address of the page your browser opened after you allowed access (it starts with http://127.0.0.1:53682/)")
	}
	q := u.Query()
	if q.Get("error") != "" {
		return "", fmt.Errorf("the sign-in was not allowed: %s", q.Get("error"))
	}
	if q.Get("code") == "" || q.Get("state") == "" {
		return "", errors.New("that address doesn't contain the sign-in code; copy it again from the address bar")
	}
	local := url.URL{Scheme: "http", Host: "127.0.0.1:" + authPort, Path: u.Path, RawQuery: u.RawQuery}
	client := &http.Client{Timeout: 30 * time.Second}
	res, err := client.Get(local.String())
	if err != nil {
		return "", fmt.Errorf("pass the code to rclone: %w", err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	select {
	case <-s.done:
	case <-time.After(60 * time.Second):
		s.cancel()
		return "", errors.New("rclone didn't finish signing in")
	}
	out := s.stdout.String()
	i, j := strings.Index(out, tokenStart), strings.Index(out, tokenEnd)
	if i < 0 || j < i {
		if s.err != nil {
			return "", fmt.Errorf("sign-in failed: %v", s.err)
		}
		return "", errors.New("sign-in failed: rclone returned no token")
	}
	tok := strings.TrimSpace(out[i+len(tokenStart) : j])
	if !json.Valid([]byte(tok)) {
		return "", errors.New("sign-in failed: unexpected token from rclone")
	}
	a.mu.Lock()
	if a.current == s {
		a.current = nil
	}
	a.mu.Unlock()
	return tok, nil
}
