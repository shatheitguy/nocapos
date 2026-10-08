package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"alfaos/alfad/internal/terminal"
)

const terminalTicketTTL = 30 * time.Second

type terminalTicket struct {
	userID string
	target string // "host" or "container:<id>"
	cols   int
	rows   int
	exp    time.Time
}

type terminalTicketStore struct {
	mu sync.Mutex
	m  map[string]terminalTicket
}

func (t *terminalTicketStore) issue(tk terminalTicket) string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	id := base64.RawURLEncoding.EncodeToString(b)
	tk.exp = time.Now().Add(terminalTicketTTL)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = make(map[string]terminalTicket)
	}
	now := time.Now()
	for k, v := range t.m {
		if now.After(v.exp) {
			delete(t.m, k)
		}
	}
	t.m[id] = tk
	return id
}

// redeem consumes a ticket (single use).
func (t *terminalTicketStore) redeem(id string) (terminalTicket, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tk, ok := t.m[id]
	if !ok {
		return terminalTicket{}, false
	}
	delete(t.m, id)
	if time.Now().After(tk.exp) {
		return terminalTicket{}, false
	}
	return tk, true
}

type terminalTicketRequest struct {
	Target string `json:"target"` // "host" or "container:<id>"
	Cols   int    `json:"cols"`
	Rows   int    `json:"rows"`
}

func (s *Server) terminalTicket(w http.ResponseWriter, r *http.Request) {
	var req terminalTicketRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	kind, arg, _ := strings.Cut(req.Target, ":")
	switch kind {
	case "host":
		if !s.Terminal.HostAvailable() {
			writeError(w, http.StatusBadRequest, "host_unsupported", "host terminal is only available on Linux")
			return
		}
	case "container":
		if !validContainerRef(arg) {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid container id")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "target must be host or container:<id>")
		return
	}
	cols, rows := clampSize(req.Cols, req.Rows)
	id := s.terminalTickets.issue(terminalTicket{userID: userFrom(r.Context()).ID, target: req.Target, cols: cols, rows: rows})
	s.audit(r, userFrom(r.Context()).ID, "terminal.open", req.Target, true, "")
	writeJSON(w, http.StatusOK, map[string]any{"ticket": id, "expires_in": int(terminalTicketTTL.Seconds())})
}

// terminalInfo tells the UI whether a host shell is offered on this platform.
func (s *Server) terminalInfo(w http.ResponseWriter, r *http.Request) {
	name, root := s.Terminal.HostUser()
	writeJSON(w, http.StatusOK, map[string]any{"host_available": s.Terminal.HostAvailable(), "host_user": name, "host_root": root})
}

func clampSize(cols, rows int) (int, int) {
	if cols < 1 || cols > 1000 {
		cols = 80
	}
	if rows < 1 || rows > 1000 {
		rows = 24
	}
	return cols, rows
}

func (s *Server) terminalConnect(w http.ResponseWriter, r *http.Request) {
	tk, ok := s.terminalTickets.redeem(r.URL.Query().Get("ticket"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid_ticket", "invalid or expired terminal ticket")
		return
	}
	u, err := s.Store.UserByID(r.Context(), tk.userID)
	if err != nil || u.Disabled || u.Role != "admin" {
		writeError(w, http.StatusUnauthorized, "invalid_ticket", "invalid or expired terminal ticket")
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.Config.AllowedOrigins})
	if err != nil {
		return
	}
	// A terminal moves a lot of data; raise the read limit for pastes.
	conn.SetReadLimit(1 << 20)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	var sess terminal.Session
	kind, arg, _ := strings.Cut(tk.target, ":")
	if kind == "host" {
		sess, err = s.Terminal.OpenHost(ctx, tk.cols, tk.rows)
	} else {
		sess, err = s.Terminal.OpenContainer(ctx, arg, tk.cols, tk.rows)
	}
	if err != nil {
		writeWSClose(conn, "could not open shell: "+friendlyTerminalError(err))
		return
	}
	defer sess.Close()

	s.bridge(ctx, cancel, conn, sess)
}

// bridge pumps bytes both ways: session output -> client (binary frames),
// client input (binary) -> session, client control (text JSON) -> resize.
func (s *Server) bridge(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, sess terminal.Session) {
	// session -> client
	go func() {
		defer cancel()
		buf := make([]byte, 32*1024)
		for {
			n, err := sess.Read(buf)
			if n > 0 {
				wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
				werr := conn.Write(wctx, websocket.MessageBinary, buf[:n])
				wcancel()
				if werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// client -> session
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			cancel()
			conn.Close(websocket.StatusNormalClosure, "")
			return
		}
		if typ == websocket.MessageText {
			var ctrl struct {
				Resize []int `json:"resize"`
			}
			if json.Unmarshal(data, &ctrl) == nil && len(ctrl.Resize) == 2 {
				cols, rows := clampSize(ctrl.Resize[0], ctrl.Resize[1])
				_ = sess.Resize(cols, rows)
			}
			continue
		}
		if _, err := sess.Write(data); err != nil {
			cancel()
			return
		}
	}
}

func writeWSClose(conn *websocket.Conn, msg string) {
	if len(msg) > 120 {
		msg = msg[:120]
	}
	_ = conn.Close(websocket.StatusInternalError, msg)
}

func friendlyTerminalError(err error) string {
	switch {
	case errors.Is(err, terminal.ErrHostUnsupported):
		return "host terminal is only available on Linux"
	case errors.Is(err, io.EOF):
		return "the shell closed immediately"
	default:
		return err.Error()
	}
}
