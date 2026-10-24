package auth

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// TicketStore issues single-use, short-lived tickets that let a browser open a
// WebSocket (which cannot carry an Authorization header) without putting the
// access token in the URL.
type TicketStore struct {
	mu      sync.Mutex
	ttl     time.Duration
	tickets map[string]ticket
}

type ticket struct {
	userID  string
	session string
	exp     time.Time
}

func NewTicketStore(ttl time.Duration) *TicketStore {
	return &TicketStore{ttl: ttl, tickets: make(map[string]ticket)}
}

func (t *TicketStore) Issue(userID string) (string, time.Time) {
	return t.IssueSession(userID, "")
}

// IssueSession issues a ticket that also remembers the login session it came
// from, so whatever it opens ends when that session does.
func (t *TicketStore) IssueSession(userID, session string) (string, time.Time) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	now := time.Now()
	exp := now.Add(t.ttl)

	t.mu.Lock()
	defer t.mu.Unlock()
	for k, v := range t.tickets {
		if now.After(v.exp) {
			delete(t.tickets, k)
		}
	}
	t.tickets[tok] = ticket{userID: userID, session: session, exp: exp}
	return tok, exp
}

func (t *TicketStore) Redeem(tok string) (string, bool) {
	userID, _, ok := t.RedeemSession(tok)
	return userID, ok
}

// RedeemSession consumes a ticket and returns its user and session.
func (t *TicketStore) RedeemSession(tok string) (userID, session string, ok bool) {
	if tok == "" {
		return "", "", false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	v, found := t.tickets[tok]
	if !found {
		return "", "", false
	}
	delete(t.tickets, tok)
	if time.Now().After(v.exp) {
		return "", "", false
	}
	return v.userID, v.session, true
}
