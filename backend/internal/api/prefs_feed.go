package api

import (
	"context"
	"regexp"
	"sync"
)

// prefsEvent says a person's preferences changed (one window saved them), so
// their other windows and desktops load them again. Client is the saving
// window's own tag, so it can skip its own change.
type prefsEvent struct {
	Client string `json:"client,omitempty"`
}

var prefsClientRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// prefsFeed fans a person's preference changes out to their open windows:
// on NoCap Desktop every app is a window of its own, each with its own page.
type prefsFeed struct {
	mu   sync.Mutex
	subs map[string]map[chan prefsEvent]struct{}
}

func (f *prefsFeed) subscribe(uid string) (<-chan prefsEvent, func()) {
	ch := make(chan prefsEvent, 8)
	f.mu.Lock()
	if f.subs == nil {
		f.subs = map[string]map[chan prefsEvent]struct{}{}
	}
	if f.subs[uid] == nil {
		f.subs[uid] = map[chan prefsEvent]struct{}{}
	}
	f.subs[uid][ch] = struct{}{}
	f.mu.Unlock()
	return ch, func() {
		f.mu.Lock()
		delete(f.subs[uid], ch)
		if len(f.subs[uid]) == 0 {
			delete(f.subs, uid)
		}
		f.mu.Unlock()
	}
}

func (f *prefsFeed) publish(uid string, e prefsEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for ch := range f.subs[uid] {
		select {
		case ch <- e:
		default: // a stuck window loads them again when it reconnects
		}
	}
}

func (s *Server) prefsSource(uid string) func(ctx context.Context, emit func(any)) error {
	return func(ctx context.Context, emit func(any)) error {
		ch, cancel := s.prefsFeed.subscribe(uid)
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return nil
			case e := <-ch:
				emit(e)
			}
		}
	}
}
