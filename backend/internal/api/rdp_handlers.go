package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"alfaos/alfad/internal/rdp"
)

const rdpTicketTTL = 60 * time.Second

type rdpTicket struct {
	userID string
	params rdp.Params
	exp    time.Time
}

// rdpTicketStore holds single-use tickets. A ticket carries the RDP
// credentials server-side so they never appear in a WebSocket URL.
type rdpTicketStore struct {
	mu sync.Mutex
	m  map[string]rdpTicket
}

func (t *rdpTicketStore) issue(tk rdpTicket) string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	id := base64.RawURLEncoding.EncodeToString(b)
	tk.exp = time.Now().Add(rdpTicketTTL)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = make(map[string]rdpTicket)
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

func (t *rdpTicketStore) redeem(id string) (rdpTicket, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tk, ok := t.m[id]
	if !ok {
		return rdpTicket{}, false
	}
	delete(t.m, id)
	return tk, time.Now().Before(tk.exp)
}

// rdpStatus reports (and kicks off provisioning of) the RDP engine.
func (s *Server) rdpStatus(w http.ResponseWriter, r *http.Request) {
	s.Guacd.Ensure()
	writeJSON(w, http.StatusOK, s.Guacd.Status())
}

type rdpTicketRequest struct {
	Hostname   string `json:"hostname"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	Domain     string `json:"domain"`
	Security   string `json:"security"`
	IgnoreCert bool   `json:"ignore_cert"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	DPI        int    `json:"dpi"`
}

func (s *Server) rdpTicket(w http.ResponseWriter, r *http.Request) {
	var req rdpTicketRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Hostname = strings.TrimSpace(req.Hostname)
	if req.Hostname == "" || len(req.Hostname) > 253 || strings.ContainsAny(req.Hostname, " /\\") {
		writeError(w, http.StatusBadRequest, "bad_request", "enter a valid host name or IP address")
		return
	}
	if req.Port < 0 || req.Port > 65535 {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid port")
		return
	}
	switch req.Security {
	case "", "any", "nla", "tls", "rdp":
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "security must be any, nla, tls or rdp")
		return
	}
	req.Width, req.Height = clampRange(req.Width, 640, 7680, 1280), clampRange(req.Height, 480, 4320, 800)
	req.DPI = clampRange(req.DPI, 48, 384, 96)

	uid := userFrom(r.Context()).ID
	id := s.rdpTickets.issue(rdpTicket{userID: uid, params: rdp.Params{
		Hostname: req.Hostname, Port: req.Port, Username: req.Username, Password: req.Password,
		Domain: req.Domain, Security: req.Security, IgnoreCert: req.IgnoreCert,
		Width: req.Width, Height: req.Height, DPI: req.DPI,
	}})
	s.audit(r, uid, "rdp.open", req.Hostname, true, "")
	writeJSON(w, http.StatusOK, map[string]any{"ticket": id, "expires_in": int(rdpTicketTTL.Seconds())})
}

func clampRange(v, lo, hi, def int) int {
	if v == 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// rdpConnect bridges guacamole-common-js (WebSocketTunnel) to guacd.
func (s *Server) rdpConnect(w http.ResponseWriter, r *http.Request) {
	tk, ok := s.rdpTickets.redeem(r.URL.Query().Get("ticket"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid_ticket", "invalid or expired remote desktop ticket")
		return
	}
	u, err := s.Store.UserByID(r.Context(), tk.userID)
	if err != nil || u.Disabled || u.Role != "admin" {
		writeError(w, http.StatusUnauthorized, "invalid_ticket", "invalid or expired remote desktop ticket")
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:   []string{"guacamole"},
		OriginPatterns: s.Config.AllowedOrigins,
	})
	if err != nil {
		return
	}
	conn.SetReadLimit(4 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Guacamole status codes: 0x0201 upstream unavailable, 0x0203 upstream error.
	fail := func(msg, code string) {
		_ = conn.Write(ctx, websocket.MessageText, []byte(rdp.Encode("error", msg, code)))
		conn.Close(websocket.StatusNormalClosure, "")
	}

	addr, ready := s.Guacd.Addr()
	if !ready {
		fail("The remote desktop engine is not ready yet", "513")
		return
	}
	upstream, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		s.Guacd.Ensure() // it went away — start recovering for the next attempt
		fail("Could not reach the remote desktop engine", "513")
		return
	}
	defer upstream.Close()

	rd := rdp.NewReader(upstream)
	_ = upstream.SetDeadline(time.Now().Add(30 * time.Second))
	id, err := rdp.Handshake(upstream, rd, tk.params)
	if err != nil {
		msg := "Could not start the remote desktop session"
		if errors.Is(err, rdp.ErrServer) {
			msg = strings.TrimPrefix(err.Error(), rdp.ErrServer.Error()+": ")
		}
		fail(msg, "515")
		return
	}
	_ = upstream.SetDeadline(time.Time{})

	// Like Guacamole's own servlet, announce the tunnel UUID first.
	if err := conn.Write(ctx, websocket.MessageText, []byte(rdp.Encode("", id))); err != nil {
		return
	}
	s.rdpRelay(ctx, cancel, conn, upstream, rd)
}

func (s *Server) rdpRelay(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, upstream net.Conn, rd *rdp.Reader) {
	// guacd -> browser: batch whole instructions into WebSocket messages.
	go func() {
		defer cancel()
		var batch strings.Builder
		for {
			ins, err := rd.Read()
			if err != nil {
				_ = conn.Write(ctx, websocket.MessageText, []byte(rdp.Encode("error", "The remote desktop session ended", "517")))
				conn.Close(websocket.StatusNormalClosure, "")
				return
			}
			batch.WriteString(ins.Raw)
			if rd.Buffered() > 0 && batch.Len() < 64*1024 {
				continue
			}
			wctx, wcancel := context.WithTimeout(ctx, 15*time.Second)
			err = conn.Write(wctx, websocket.MessageText, []byte(batch.String()))
			wcancel()
			if err != nil {
				return
			}
			batch.Reset()
		}
	}()

	// browser -> guacd. Internal instructions (opcode "") are tunnel-level:
	// answer pings ourselves and never forward them to guacd.
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			_, _ = upstream.Write([]byte(rdp.Encode("disconnect")))
			cancel()
			return
		}
		if strings.HasPrefix(string(data), "0.,") {
			if strings.HasPrefix(string(data), "0.,4.ping,") {
				_ = conn.Write(ctx, websocket.MessageText, data)
			}
			continue
		}
		if _, err := upstream.Write(data); err != nil {
			cancel()
			return
		}
	}
}
