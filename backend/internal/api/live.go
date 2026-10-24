package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"alfaos/alfad/internal/store"
)

// liveConns tracks each person's open long-lived connections (desktop
// WebSocket, terminals, remote desktops, the Brave stream), so that taking
// someone's rights away also ends what they already have open. Checking at
// connect time alone would leave a demoted administrator's root terminal
// running.
type liveConns struct {
	mu   sync.Mutex
	next uint64
	m    map[string]map[uint64]liveConn // user ID → connections
}

type liveConn struct {
	session string // login session (refresh token family)
	close   func()
}

// add registers a connection; call the returned func when it ends.
func (l *liveConns) add(userID, session string, closeFn func()) (remove func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = make(map[string]map[uint64]liveConn)
	}
	l.next++
	id := l.next
	if l.m[userID] == nil {
		l.m[userID] = make(map[uint64]liveConn)
	}
	l.m[userID][id] = liveConn{session: session, close: closeFn}
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if conns := l.m[userID]; conns != nil {
			delete(conns, id)
			if len(conns) == 0 {
				delete(l.m, userID)
			}
		}
	}
}

// closeWhere closes (outside the lock) the user's connections that match.
func (l *liveConns) closeWhere(userID string, match func(liveConn) bool) int {
	l.mu.Lock()
	var fns []func()
	for id, c := range l.m[userID] {
		if match(c) {
			fns = append(fns, c.close)
			delete(l.m[userID], id)
		}
	}
	if len(l.m[userID]) == 0 {
		delete(l.m, userID)
	}
	l.mu.Unlock()
	for _, fn := range fns {
		go fn() // a WebSocket close handshake can take seconds
	}
	return len(fns)
}

// closeUser ends every live connection a person has.
func (l *liveConns) closeUser(userID string) int {
	return l.closeWhere(userID, func(liveConn) bool { return true })
}

// closeSession ends the live connections opened from one login session.
func (l *liveConns) closeSession(userID, session string) int {
	if session == "" {
		return 0
	}
	return l.closeWhere(userID, func(c liveConn) bool { return c.session == session })
}

func (l *liveConns) count(userID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.m[userID])
}

// signOutEverywhere revokes all of a person's sessions and closes their live
// connections: used when an administrator changes their role, access or
// password, or disables them.
func (s *Server) signOutEverywhere(ctx context.Context, u *store.User) {
	if err := s.Store.RevokeAllUserSessions(ctx, u.ID, time.Now()); err != nil {
		s.Log.Warn("revoke sessions", "user", u.Username, "err", err)
	}
	s.live.closeUser(u.ID)
}

// closeLive closes a person's live connections without ending their
// sessions (a promotion, say: the desktop reconnects with the new rights).
func (s *Server) closeLive(u *store.User) { s.live.closeUser(u.ID) }

var errNoLongerAllowed = errors.New("no longer allowed")

// currentUser re-reads a person for a connection that outlives the request
// that authorized it, and checks they may still use the desktop from that
// session; with admin set, that they're still an administrator.
func (s *Server) currentUser(ctx context.Context, userID, session string, admin bool) (*store.User, error) {
	u, err := s.Store.UserByID(ctx, userID)
	if err != nil {
		return nil, errNoLongerAllowed
	}
	if err := s.Auth.DesktopAllowed(ctx, u); err != nil {
		return nil, errNoLongerAllowed
	}
	if admin && u.Role != store.RoleAdmin {
		return nil, errNoLongerAllowed
	}
	if session != "" {
		if ok, err := s.Auth.SessionActive(ctx, session); err != nil || !ok {
			return nil, errNoLongerAllowed
		}
	}
	return u, nil
}

// sessionFrom is the login session of an authed request.
func sessionFrom(ctx context.Context) string {
	sid, _ := ctx.Value(sessionKey).(string)
	return sid
}

// refuseTicket answers a WebSocket/file ticket whose holder lost access.
func refuseTicket(w http.ResponseWriter, msg string) {
	writeError(w, http.StatusUnauthorized, "invalid_ticket", msg)
}
