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
	userID string
	exp    time.Time
}

func NewTicketStore(ttl time.Duration) *TicketStore {
	return &TicketStore{ttl: ttl, tickets: make(map[string]ticket)}
}

func (t *TicketStore) Issue(userID string) (string, time.Time) {
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
	t.tickets[tok] = ticket{userID: userID, exp: exp}
	return tok, exp
}

func (t *TicketStore) Redeem(tok string) (string, bool) {
	if tok == "" {
		return "", false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	v, ok := t.tickets[tok]
	if !ok {
		return "", false
	}
	delete(t.tickets, tok)
	if time.Now().After(v.exp) {
		return "", false
	}
	return v.userID, true
}
