package auth

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// ErrLocked: too many wrong passwords or codes for this account in a row.
var ErrLocked = errors.New("too many failed attempts for this account; try again in 15 minutes")

const (
	lockoutThreshold = 10
	lockoutDuration  = 15 * time.Minute
	maxLockoutKeys   = 10000
)

// Lockout counts failed sign-in steps per account, independent of the
// address they come from, so spreading guesses over many IPs doesn't help.
// After lockoutThreshold failures in a row the account waits lockoutDuration.
type Lockout struct {
	mu  sync.Mutex
	m   map[string]*lockState
	now func() time.Time
}

type lockState struct {
	fails int
	until time.Time
	last  time.Time
}

func NewLockout() *Lockout {
	return &Lockout{m: make(map[string]*lockState), now: time.Now}
}

func lockKey(username string) string { return strings.ToLower(strings.TrimSpace(username)) }

// Locked reports whether the account is waiting out a lockout.
func (l *Lockout) Locked(username string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.m[lockKey(username)]
	return st != nil && l.now().Before(st.until)
}

// Fail records a failed attempt.
func (l *Lockout) Fail(username string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	k, now := lockKey(username), l.now()
	st := l.m[k]
	if st == nil {
		if len(l.m) >= maxLockoutKeys {
			l.evictLocked(now)
		}
		st = &lockState{}
		l.m[k] = st
	}
	if !st.until.IsZero() && !now.Before(st.until) {
		st.fails, st.until = 0, time.Time{} // the last lockout is over: start counting again
	}
	st.fails++
	st.last = now
	if st.fails >= lockoutThreshold {
		st.until = now.Add(lockoutDuration)
	}
}

// Succeed clears the account's failures.
func (l *Lockout) Succeed(username string) {
	l.mu.Lock()
	delete(l.m, lockKey(username))
	l.mu.Unlock()
}

// evictLocked drops expired entries, then the oldest ones, so the map stays
// bounded without forgetting accounts that are locked right now.
func (l *Lockout) evictLocked(now time.Time) {
	for k, st := range l.m {
		if now.Sub(st.last) > lockoutDuration && !now.Before(st.until) {
			delete(l.m, k)
		}
	}
	for len(l.m) >= maxLockoutKeys {
		var oldest string
		var at time.Time
		for k, st := range l.m {
			if oldest == "" || st.last.Before(at) {
				oldest, at = k, st.last
			}
		}
		delete(l.m, oldest)
	}
}
