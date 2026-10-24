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

	"alfaos/alfad/internal/store"
	"alfaos/alfad/internal/terminal"
)

const terminalTicketTTL = 30 * time.Second

type terminalTicket struct {
	userID  string
	session string
	target  string // "host" or "container:<id>"
	cols    int
	rows    int
	exp     time.Time
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
	// "host" (a shell as your own Linux account), "host-root" (a root shell:
	// needs your password) or "container:<id>".
	Target   string `json:"target"`
	Cols     int    `json:"cols"`
	Rows     int    `json:"rows"`
	Password string `json:"password,omitempty"` // host-root only
}

func (s *Server) terminalTicket(w http.ResponseWriter, r *http.Request) {
	var req terminalTicketRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	u := userFrom(r.Context())
	kind, arg, _ := strings.Cut(req.Target, ":")
	switch kind {
	case "host", "host-root":
		if !s.Terminal.HostAvailable() {
			writeError(w, http.StatusBadRequest, "host_unsupported", "host terminal is only available on Linux")
			return
		}
		if arg != "" {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid target")
			return
		}
		if kind == "host-root" {
			if s.sensitiveLimited(w, r) {
				return
			}
			// A root shell is the keys to the machine: confirm it's really them.
			if err := s.Auth.ConfirmPassword(r.Context(), u.ID, req.Password); err != nil {
				s.audit(r, u.ID, "terminal.open", req.Target, false, "password not confirmed")
				s.totpAuthError(w, r, err)
				return
			}
		} else if _, err := s.hostAccount(u); err != nil {
			writeError(w, http.StatusBadRequest, "no_account", err.Error())
			return
		}
	case "container":
		if !validContainerRef(arg) {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid container id")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "target must be host, host-root or container:<id>")
		return
	}
	cols, rows := clampSize(req.Cols, req.Rows)
	id := s.terminalTickets.issue(terminalTicket{userID: u.ID, session: sessionFrom(r.Context()), target: req.Target, cols: cols, rows: rows})
	s.audit(r, u.ID, "terminal.open", req.Target, true, "")
	writeJSON(w, http.StatusOK, map[string]any{"ticket": id, "expires_in": int(terminalTicketTTL.Seconds())})
}

// hostAccount is the Linux account a person's host shell runs as. When
// NoCapOS isn't root it can't switch accounts, so (nil) the shell is
// NoCapOS's own unprivileged one. As root it is always the person's own
// account, never root unless they signed in as root; NoCapOS's own
// accounts (not Linux users) get no host shell this way.
func (s *Server) hostAccount(u *store.User) (*terminal.Account, error) {
	if !s.Terminal.RunsAsRoot() {
		return nil, nil
	}
	if !s.Auth.SystemAccountsEnabled() {
		return nil, terminal.ErrNoLinuxAccount
	}
	return terminal.LookupAccount(u.Username)
}

// terminalInfo tells the UI whether a host shell is offered, who it runs as,
// and whether a root shell can be opened (with the password).
func (s *Server) terminalInfo(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	own, ownRoot := s.Terminal.HostUser()
	out := map[string]any{
		"host_available": s.Terminal.HostAvailable(),
		"host_user":      own,
		"host_root":      ownRoot,
		// A root shell only means something when NoCapOS itself is root.
		"root_available": s.Terminal.HostAvailable() && ownRoot,
	}
	if acct, err := s.hostAccount(u); err != nil {
		out["host_user"], out["host_root"], out["host_error"] = "", false, err.Error()
	} else if acct != nil {
		out["host_user"], out["host_root"] = acct.Username, acct.Root()
	}
	writeJSON(w, http.StatusOK, out)
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
	u, err := s.currentUser(r.Context(), tk.userID, tk.session, true)
	if err != nil {
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
	// A shell is root-equivalent: it ends the moment its owner signs out or
	// stops being an administrator.
	defer s.live.add(u.ID, tk.session, func() {
		cancel()
		conn.Close(websocket.StatusPolicyViolation, "session changed")
	})()

	var sess terminal.Session
	kind, arg, _ := strings.Cut(tk.target, ":")
	switch kind {
	case "host-root": // password confirmed when the ticket was issued
		sess, err = s.Terminal.OpenHost(ctx, nil, tk.cols, tk.rows)
	case "host":
		var acct *terminal.Account
		if acct, err = s.hostAccount(u); err == nil {
			sess, err = s.Terminal.OpenHost(ctx, acct, tk.cols, tk.rows)
		}
	default:
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
