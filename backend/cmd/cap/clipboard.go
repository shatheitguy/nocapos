package main

// cap clipboard: NoCap Desktop's clipboard agent. It runs in the desktop
// session as a user service (nocapos-clipboard.service) and keeps the
// desktop's real clipboard in step with the person's other devices through
// alfad's local socket: copy on the phone, then Ctrl+V in any desktop app,
// and the other way round. Wayland compositors with the data-control
// protocol use wl-paste/wl-copy; everything else (Xorg, and GNOME on
// Wayland through Xwayland, which shares one clipboard) uses xclip.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// clipSocket is alfad's socket for this agent (api.ClipSocketPath).
const clipSocket = "/run/nocapos/clipboard.sock"

const (
	agentMaxText  = 1 << 20
	agentMaxImage = 10 << 20
)

// localClip is what's on the desktop's clipboard.
type localClip struct {
	mime string // "text" or image/png, image/jpeg
	data []byte
}

func (c localClip) hash() string {
	h := sha256.Sum256(append([]byte(c.mime+"\x00"), c.data...))
	return string(h[:])
}

// clipTool reads and writes the desktop's clipboard.
type clipTool interface {
	name() string
	// watch signals changed for every change (it may also signal when
	// nothing changed) until ctx ends or it fails.
	watch(ctx context.Context, changed chan<- struct{}) error
	read(ctx context.Context) (localClip, bool)
	write(ctx context.Context, c localClip) error
}

// textTypes and imageTypes, most wanted first.
var (
	textTypes  = []string{"text/plain;charset=utf-8", "UTF8_STRING", "text/plain", "STRING", "TEXT"}
	imageTypes = []string{"image/png", "image/jpeg"}
)

// pickType chooses what to take from the types on offer: text first, then
// a picture. A password manager's secret ("x-kde-passwordManagerHint") is
// never taken.
func pickType(types []string) (string, bool) {
	have := map[string]bool{}
	for _, t := range types {
		t = strings.TrimSpace(t)
		if t == "x-kde-passwordManagerHint" {
			return "", false
		}
		have[t] = true
	}
	for _, t := range textTypes {
		if have[t] {
			return t, true
		}
	}
	for _, t := range imageTypes {
		if have[t] {
			return t, true
		}
	}
	return "", false
}

func clipMime(t string) string {
	if strings.HasPrefix(t, "image/") {
		return t
	}
	return "text"
}

// run runs a command and returns its output (at most limit bytes; false if more).
func runOut(ctx context.Context, limit int, name string, args ...string) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false
	}
	if err := cmd.Start(); err != nil {
		return nil, false
	}
	b, rerr := io.ReadAll(io.LimitReader(out, int64(limit)+1))
	werr := cmd.Wait()
	if rerr != nil || werr != nil || len(b) > limit {
		return nil, false
	}
	return b, true
}

// feed starts a command that takes data on stdin and may stay behind to
// serve the clipboard (wl-copy, xclip): it isn't waited for past the copy.
func feed(data []byte, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = bytes.NewReader(data)
	// No pipes on stdout/stderr: the child that stays behind must not hold them.
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		return nil // it's serving the clipboard itself
	}
}

func readWith(ctx context.Context, types []string, get func(t string, limit int) ([]byte, bool)) (localClip, bool) {
	t, ok := pickType(types)
	if !ok {
		return localClip{}, false
	}
	limit := agentMaxText
	if strings.HasPrefix(t, "image/") {
		limit = agentMaxImage
	}
	b, ok := get(t, limit)
	if !ok || len(b) == 0 {
		return localClip{}, false
	}
	return localClip{mime: clipMime(t), data: b}, true
}

// ---------- Wayland (data-control) ----------

type wlTool struct{}

func (wlTool) name() string { return "wl-clipboard" }

func (wlTool) watch(ctx context.Context, changed chan<- struct{}) error {
	cmd := exec.CommandContext(ctx, "wl-paste", "--watch", "echo", "x")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		select {
		case changed <- struct{}{}:
		default:
		}
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("wl-paste --watch stopped: %v %s", err, strings.TrimSpace(stderr.String()))
}

func (wlTool) read(ctx context.Context) (localClip, bool) {
	b, ok := runOut(ctx, 64<<10, "wl-paste", "--list-types")
	if !ok {
		return localClip{}, false
	}
	return readWith(ctx, strings.Split(string(b), "\n"), func(t string, limit int) ([]byte, bool) {
		return runOut(ctx, limit, "wl-paste", "--no-newline", "--type", t)
	})
}

func (wlTool) write(_ context.Context, c localClip) error {
	t := c.mime
	if t == "text" {
		t = "text/plain;charset=utf-8"
	}
	return feed(c.data, "wl-copy", "--type", t)
}

// ---------- X11 (Xorg, or Xwayland under GNOME) ----------

type xTool struct{ every time.Duration }

func (xTool) name() string { return "xclip" }

func xclipOut(ctx context.Context, t string, limit int) ([]byte, bool) {
	return runOut(ctx, limit, "xclip", "-selection", "clipboard", "-o", "-t", t)
}

// watch polls: X11 has no "clipboard changed" for a plain client. Only
// the list of types and its timestamp are read each time; the content
// only when they changed (or always for text, which is small).
func (x xTool) watch(ctx context.Context, changed chan<- struct{}) error {
	var last string
	tick := time.NewTicker(x.every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
		types, _ := xclipOut(ctx, "TARGETS", 64<<10)
		stamp, _ := xclipOut(ctx, "TIMESTAMP", 256)
		key := string(types) + "\x00" + string(stamp)
		if t, ok := pickType(strings.Split(string(types), "\n")); ok && !strings.HasPrefix(t, "image/") && len(stamp) == 0 {
			// No timestamp from this owner: compare the text itself.
			if b, ok := xclipOut(ctx, t, agentMaxText); ok {
				key += string(b)
			}
		}
		if key != last {
			last = key
			select {
			case changed <- struct{}{}:
			default:
			}
		}
	}
}

func (xTool) read(ctx context.Context) (localClip, bool) {
	types, ok := xclipOut(ctx, "TARGETS", 64<<10)
	if !ok {
		return localClip{}, false
	}
	return readWith(ctx, strings.Split(string(types), "\n"), func(t string, limit int) ([]byte, bool) {
		return xclipOut(ctx, t, limit)
	})
}

func (xTool) write(_ context.Context, c localClip) error {
	t := c.mime
	if t == "text" {
		t = "UTF8_STRING"
	}
	return feed(c.data, "xclip", "-selection", "clipboard", "-t", t, "-i")
}

// ---------- alfad ----------

type agentClip struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"`
	Text string `json:"text"`
	Mime string `json:"mime"`
}

type agentEvents struct {
	Cursor  int64      `json:"cursor"`
	Enabled bool       `json:"enabled"`
	Clip    *agentClip `json:"clip"`
}

var errOff = errors.New("switched off")

type agentServer struct {
	c *http.Client
}

func newAgentServer(sock string) agentServer {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
		MaxIdleConns: 2,
	}
	return agentServer{c: &http.Client{Transport: tr}}
}

func (a agentServer) do(ctx context.Context, method, path, ctype string, body []byte) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://nocapos"+path, rd)
	if err != nil {
		return nil, err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := a.c.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusForbidden {
		res.Body.Close()
		return nil, errOff
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("NoCapOS answered %s", res.Status)
	}
	return res, nil
}

func (a agentServer) events(ctx context.Context, after int64) (agentEvents, error) {
	var ev agentEvents
	res, err := a.do(ctx, "GET", "/v1/events?wait=25&after="+strconv.FormatInt(after, 10), "", nil)
	if err != nil {
		return ev, err
	}
	defer res.Body.Close()
	err = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&ev)
	return ev, err
}

func (a agentServer) fetch(ctx context.Context, c agentClip) (localClip, error) {
	if c.Kind != "image" {
		return localClip{mime: "text", data: []byte(c.Text)}, nil
	}
	if c.Mime != "image/png" && c.Mime != "image/jpeg" {
		return localClip{}, errors.New("not a PNG or JPEG")
	}
	res, err := a.do(ctx, "GET", "/v1/clip/"+strconv.FormatInt(c.ID, 10)+"/data", "", nil)
	if err != nil {
		return localClip{}, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, agentMaxImage+1))
	if err != nil || len(b) > agentMaxImage {
		return localClip{}, errors.New("picture too big")
	}
	return localClip{mime: c.Mime, data: b}, nil
}

func (a agentServer) send(ctx context.Context, c localClip) error {
	ctype, body := c.mime, c.data
	if c.mime == "text" {
		ctype = "application/json"
		body, _ = json.Marshal(map[string]string{"text": string(c.data)})
	}
	res, err := a.do(ctx, "POST", "/v1/clip", ctype, body)
	if err != nil {
		return err
	}
	res.Body.Close()
	return nil
}

// ---------- the agent ----------

func have(cmd string) bool { _, err := exec.LookPath(cmd); return err == nil }

// pickTool chooses how to reach the clipboard in this session.
func pickTool(ctx context.Context) (clipTool, error) {
	if os.Getenv("WAYLAND_DISPLAY") != "" && have("wl-paste") && have("wl-copy") {
		// Only compositors with the data-control protocol let a background
		// program watch the clipboard; GNOME doesn't, so check first.
		wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := wlTool{}.watch(wctx, make(chan struct{}, 1))
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			return wlTool{}, nil
		}
	}
	if os.Getenv("DISPLAY") != "" && have("xclip") {
		return xTool{every: 1200 * time.Millisecond}, nil
	}
	return nil, errors.New("no clipboard tool for this session (needs wl-clipboard or xclip)")
}

// clipAgent keeps one desktop clipboard in step with alfad.
type clipAgent struct {
	tool clipTool
	srv  agentServer
	log  *log.Logger

	mu      sync.Mutex
	last    string // hash of what the clipboard held when last seen or set
	enabled bool
}

// seen records c as on the clipboard; true when it's new.
func (g *clipAgent) seen(c localClip) bool {
	h := c.hash()
	g.mu.Lock()
	defer g.mu.Unlock()
	if h == g.last {
		return false
	}
	g.last = h
	return true
}

// follow applies clips from the person's other devices.
func (g *clipAgent) follow(ctx context.Context) {
	var cursor int64
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		ev, err := g.srv.events(ctx, cursor)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, errOff) {
				backoff = time.Minute // not a NoCapOS account (yet)
			}
			sleepCtx(ctx, backoff)
			backoff = min(backoff*2, 5*time.Minute)
			continue
		}
		backoff = 2 * time.Second
		g.mu.Lock()
		g.enabled = ev.Enabled
		g.mu.Unlock()
		if ev.Clip != nil && cursor != 0 {
			c, err := g.srv.fetch(ctx, *ev.Clip)
			if err == nil && g.seen(c) {
				if err := g.tool.write(ctx, c); err != nil {
					g.log.Printf("couldn't set the clipboard: %v", err)
				}
			}
		}
		cursor = ev.Cursor
	}
}

// share sends what's copied on the desktop.
func (g *clipAgent) share(ctx context.Context, changed <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
		sleepCtx(ctx, 150*time.Millisecond) // let the copying app finish
		c, ok := g.tool.read(ctx)
		if !ok || !g.seen(c) {
			continue
		}
		g.mu.Lock()
		on := g.enabled
		g.mu.Unlock()
		if !on {
			continue
		}
		if err := g.srv.send(ctx, c); err != nil && !errors.Is(err, errOff) {
			g.log.Printf("couldn't share the clipboard: %v", err)
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func cmdClipboard(args []string) error {
	if len(args) != 0 && args[0] != "agent" {
		return errors.New("usage: cap clipboard agent")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	lg := log.New(os.Stderr, "", 0)
	tool, err := pickTool(ctx)
	if err != nil {
		// Nothing to do in this session; not a failure worth restarting for.
		lg.Print(err)
		return nil
	}
	lg.Printf("NoCap Desktop clipboard: using %s", tool.name())
	g := &clipAgent{tool: tool, srv: newAgentServer(clipSocket), log: lg}
	if c, ok := tool.read(ctx); ok {
		g.seen(c) // what's there already isn't news
	}
	changed := make(chan struct{}, 1)
	go g.follow(ctx)
	go g.share(ctx, changed)
	for ctx.Err() == nil {
		if err := tool.watch(ctx, changed); err != nil && ctx.Err() == nil {
			lg.Print(err)
			sleepCtx(ctx, 5*time.Second)
		}
	}
	return nil
}
