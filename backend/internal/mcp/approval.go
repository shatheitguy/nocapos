package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"
)

// ApprovalTimeout is how long an agent's action waits for the owner.
const ApprovalTimeout = 2 * time.Minute

// Decision is the owner's answer to an approval card.
type Decision string

const (
	Once   Decision = "once"   // run it this time
	Always Decision = "always" // run it, and don't ask again for this tool and agent
	Deny   Decision = "deny"
)

// ValidDecision reports whether d is one of the three answers.
func ValidDecision(d Decision) bool { return d == Once || d == Always || d == Deny }

// Approval is one waiting action, as the approval card shows it.
type Approval struct {
	ID        string         `json:"id"`
	UserID    string         `json:"-"`
	TokenID   string         `json:"token_id"`
	Agent     string         `json:"agent"`
	Tool      string         `json:"tool"`
	ToolTitle string         `json:"tool_title"`
	Cap       string         `json:"capability"`
	Risk      Risk           `json:"risk"`
	Summary   string         `json:"summary"`
	Arguments map[string]any `json:"arguments"`
	CreatedAt time.Time      `json:"created_at"`
	ExpiresAt time.Time      `json:"expires_at"`

	answer chan Decision
}

// Outcome is how an approval ended.
type Outcome struct {
	Decision Decision
	TimedOut bool // nobody answered in time (counts as denied)
}

// ErrNoApproval: no waiting approval with that id belongs to that person.
var ErrNoApproval = errors.New("that request isn't waiting anymore")

// Broker holds the actions waiting for their owners' approval.
type Broker struct {
	Timeout time.Duration // 0 = ApprovalTimeout
	// OnAsk, when set, hears about each new approval (in its own goroutine).
	OnAsk func(Approval)

	mu      sync.Mutex
	pending map[string]*Approval
	watch   map[chan struct{}]string // → user id
}

func (b *Broker) timeout() time.Duration {
	if b.Timeout > 0 {
		return b.Timeout
	}
	return ApprovalTimeout
}

// Ask shows an approval card to a.UserID and waits for the answer, the
// timeout or ctx (the agent hung up). Anything but an answer is a deny.
func (b *Broker) Ask(ctx context.Context, a Approval) Outcome {
	idb := make([]byte, 12)
	_, _ = rand.Read(idb)
	a.ID = hex.EncodeToString(idb)
	a.CreatedAt = time.Now().UTC()
	a.ExpiresAt = a.CreatedAt.Add(b.timeout())
	a.answer = make(chan Decision, 1)
	ap := &a

	b.mu.Lock()
	if b.pending == nil {
		b.pending = map[string]*Approval{}
	}
	b.pending[a.ID] = ap
	b.mu.Unlock()
	b.changed(a.UserID)
	if b.OnAsk != nil {
		c := a
		c.answer = nil
		go b.OnAsk(c)
	}
	defer func() {
		b.mu.Lock()
		delete(b.pending, a.ID)
		b.mu.Unlock()
		b.changed(a.UserID)
	}()

	t := time.NewTimer(b.timeout())
	defer t.Stop()
	select {
	case d := <-ap.answer:
		return Outcome{Decision: d}
	case <-t.C:
		return Outcome{Decision: Deny, TimedOut: true}
	case <-ctx.Done():
		return Outcome{Decision: Deny}
	}
}

// Decide answers a waiting approval; only its owner may.
func (b *Broker) Decide(id, userID string, d Decision) error {
	if !ValidDecision(d) {
		return errors.New("decision must be once, always or deny")
	}
	b.mu.Lock()
	a, ok := b.pending[id]
	if !ok || a.UserID != userID {
		b.mu.Unlock()
		return ErrNoApproval
	}
	delete(b.pending, id) // answered: nobody can answer it twice
	b.mu.Unlock()
	a.answer <- d
	b.changed(userID)
	return nil
}

// DenyAll answers deny to every waiting approval of a person ("" = of
// everyone): Stop all agents. It returns how many it denied.
func (b *Broker) DenyAll(userID string) int {
	b.mu.Lock()
	var hit []*Approval
	users := map[string]bool{}
	for id, a := range b.pending {
		if userID == "" || a.UserID == userID {
			hit = append(hit, a)
			users[a.UserID] = true
			delete(b.pending, id)
		}
	}
	b.mu.Unlock()
	for _, a := range hit {
		a.answer <- Deny
	}
	for u := range users {
		b.changed(u)
	}
	return len(hit)
}

// Pending lists a person's waiting approvals, oldest first.
func (b *Broker) Pending(userID string) []Approval {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []Approval{}
	for _, a := range b.pending {
		if a.UserID == userID {
			c := *a
			c.answer = nil
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Watch tells the caller (without blocking) whenever a person's waiting
// approvals change. Call cancel when done.
func (b *Broker) Watch(userID string) (ch <-chan struct{}, cancel func()) {
	c := make(chan struct{}, 1)
	b.mu.Lock()
	if b.watch == nil {
		b.watch = map[chan struct{}]string{}
	}
	b.watch[c] = userID
	b.mu.Unlock()
	return c, func() {
		b.mu.Lock()
		delete(b.watch, c)
		b.mu.Unlock()
	}
}

func (b *Broker) changed(userID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for c, u := range b.watch {
		if u == userID {
			select {
			case c <- struct{}{}:
			default:
			}
		}
	}
}
