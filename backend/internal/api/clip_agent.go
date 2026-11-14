package api

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// NoCap Desktop's clipboard agent (cap clipboard, a user service in the
// desktop session) talks to alfad over a local socket that only this
// machine's own accounts can open. The kernel says which Linux account is
// on the other end (see clip_socket_linux.go); that account's NoCapOS
// clipboard is the only one it ever reaches.

// ClipSocketPath is where alfad listens for the desktop's clipboard agent.
const ClipSocketPath = "/run/nocapos/clipboard.sock"

type peerKey struct{}

// WithClipPeer marks a connection as coming from the Linux account name.
func WithClipPeer(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, peerKey{}, name)
}

func clipPeer(ctx context.Context) string {
	n, _ := ctx.Value(peerKey{}).(string)
	return n
}

// ClipAgentHandler serves the desktop clipboard agent. Every request must
// come with a peer account (WithClipPeer) that is a NoCapOS account.
func (s *Server) ClipAgentHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/events", s.clipAgentEvents)
	mux.HandleFunc("POST /v1/clip", s.clipAgentPost)
	mux.HandleFunc("GET /v1/clip/{id}/data", s.clipboardData)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := clipPeer(r.Context())
		if name == "" {
			writeError(w, http.StatusForbidden, "forbidden", "unknown account")
			return
		}
		u, err := s.Store.UserByUsername(r.Context(), name)
		if err != nil || u == nil || u.Disabled {
			writeError(w, http.StatusForbidden, "forbidden", "this account isn't a NoCapOS account")
			return
		}
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	})
}

type clipAgentEvents struct {
	Cursor int64 `json:"cursor"`
	// Enabled: the person lets NoCap Desktop's clipboard join in.
	Enabled bool      `json:"enabled"`
	Clip    *linkClip `json:"clip,omitempty"`
}

// clipAgentEvents is the agent's long-poll: the newest clip from the
// person's other devices after ?after, as soon as there is one.
func (s *Server) clipAgentEvents(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	q := r.URL.Query()
	after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
	wait := linkWaitDefault
	if v := q.Get("wait"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			wait = min(max(time.Duration(n)*time.Second, 0), linkWaitMax)
		}
	}
	h := s.link()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	fromElsewhere := func(c linkClip) bool { return c.Source != clipFromDesktop }
	for {
		on := s.clipSettings(r.Context(), u.ID).Desktop
		h.mu.Lock()
		out := clipAgentEvents{Cursor: h.seq, Enabled: on}
		fresh := after > 0 && after <= h.seq
		if on && fresh {
			out.Clip = h.latestClip(u.ID, after, fromElsewhere)
		}
		wake := h.wake
		h.mu.Unlock()
		if out.Clip != nil || !fresh {
			writeJSON(w, http.StatusOK, out)
			return
		}
		select {
		case <-wake:
		case <-timer.C:
			writeJSON(w, http.StatusOK, out)
			return
		case <-r.Context().Done():
			return
		}
	}
}

// clipAgentPost: something was copied on NoCap Desktop.
func (s *Server) clipAgentPost(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if !s.clipSettings(r.Context(), u.ID).Desktop {
		writeError(w, http.StatusForbidden, "off", "NoCap Desktop's clipboard is switched off in Settings.")
		return
	}
	c, ok := readClip(w, r, nil)
	if !ok {
		return
	}
	c.From, c.Source = "NoCap Desktop", clipFromDesktop
	id := s.link().addClip(u.ID, c, s.keepClips(r.Context(), u.ID))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}
