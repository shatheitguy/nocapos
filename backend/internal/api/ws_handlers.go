package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"alfaos/alfad/internal/docker"
	"alfaos/alfad/internal/store"
	"alfaos/alfad/internal/ws"
)

var (
	errUnknownTopic = errors.New("unknown topic")
	errForbidden    = errors.New("forbidden")
)

func (s *Server) wsTicket(w http.ResponseWriter, r *http.Request) {
	tok, exp := s.tickets.Issue(userFrom(r.Context()).ID)
	writeJSON(w, http.StatusOK, map[string]any{"ticket": tok, "expires_at": exp.UTC()})
}

func (s *Server) wsConnect(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.tickets.Redeem(r.URL.Query().Get("ticket"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid_ticket", "invalid or expired ticket")
		return
	}
	u, err := s.Store.UserByID(r.Context(), userID)
	if err != nil || u.Disabled {
		writeError(w, http.StatusUnauthorized, "invalid_ticket", "invalid or expired ticket")
		return
	}
	// Accept rejects cross-origin upgrades unless the origin matches Host or
	// one of the configured patterns (CSWSH protection).
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.Config.AllowedOrigins})
	if err != nil {
		return // Accept has already written the HTTP error
	}
	s.hub.Serve(conn, u)
}

// resolveTopic maps topic names to feeds:
//
//	system.metrics           any user   host + accelerator snapshot every interval
//	docker.events            admin      engine events
//	container.stats/<id>     admin      per-container resource usage (~1/s)
//	container.logs/<id>      admin      last 200 lines, then follow (batched)
//	storage.alerts           admin      disk and pool health alerts, on change
//	notifications            admin      new notifications and the unread count
func (s *Server) resolveTopic(topic string, u *store.User) (ws.Subscription, error) {
	name, arg, _ := strings.Cut(topic, "/")
	if name == "system.metrics" && arg == "" {
		return ws.Subscription{Shared: true, Source: s.metricsSource}, nil
	}
	if u.Role != store.RoleAdmin {
		return ws.Subscription{}, errForbidden
	}
	switch name {
	case "docker.events":
		if arg != "" {
			return ws.Subscription{}, errUnknownTopic
		}
		return ws.Subscription{Shared: true, Source: func(ctx context.Context, emit func(any)) error {
			return s.Docker.Events(ctx, func(e docker.Event) error { emit(e); return nil })
		}}, nil
	case "container.stats":
		if !validContainerRef(arg) {
			return ws.Subscription{}, errUnknownTopic
		}
		return ws.Subscription{Shared: true, Source: func(ctx context.Context, emit func(any)) error {
			return s.Docker.Stats(ctx, arg, func(st docker.Stats) error { emit(st); return nil })
		}}, nil
	case "storage.alerts":
		if arg != "" || s.Storage == nil {
			return ws.Subscription{}, errUnknownTopic
		}
		return ws.Subscription{Shared: true, Source: s.storageAlertsSource}, nil
	case "notifications":
		if arg != "" {
			return ws.Subscription{}, errUnknownTopic
		}
		return ws.Subscription{Shared: true, Source: s.notificationsSource}, nil
	case "container.logs":
		if !validContainerRef(arg) {
			return ws.Subscription{}, errUnknownTopic
		}
		return ws.Subscription{Shared: false, Source: s.logsSource(arg)}, nil
	}
	return ws.Subscription{}, errUnknownTopic
}

func (s *Server) metricsSource(ctx context.Context, emit func(any)) error {
	ch, cancel := s.Sampler.Subscribe()
	defer cancel()
	if snap := s.Sampler.Latest(); snap != nil {
		emit(snap)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case snap := <-ch:
			emit(snap)
		}
	}
}

const (
	logFlushInterval = 100 * time.Millisecond
	logBatchMax      = 500
)

// logsSource batches lines (events carry []docker.LogLine) so a chatty
// container produces ~10 frames/s instead of one frame per line.
func (s *Server) logsSource(id string) ws.Source {
	return func(ctx context.Context, emit func(any)) error {
		var (
			mu    sync.Mutex // held across emit to preserve batch order
			batch []docker.LogLine
		)
		flushLocked := func() {
			if len(batch) > 0 {
				emit(batch)
				batch = nil
			}
		}
		done := make(chan struct{})
		go func() {
			t := time.NewTicker(logFlushInterval)
			defer t.Stop()
			for {
				select {
				case <-done:
					return
				case <-t.C:
					mu.Lock()
					flushLocked()
					mu.Unlock()
				}
			}
		}()

		err := s.Docker.Logs(ctx, id, 200, func(l docker.LogLine) error {
			mu.Lock()
			batch = append(batch, l)
			if len(batch) >= logBatchMax {
				flushLocked()
			}
			mu.Unlock()
			return nil
		})
		close(done)
		mu.Lock()
		flushLocked()
		mu.Unlock()
		return err
	}
}
