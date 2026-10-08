package files

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Jobs runs copies and moves in the background so large transfers show
// progress, can be canceled, and don't hold an HTTP request open.

const maxJobs = 50

type Job struct {
	ID       string          `json:"id"`
	Kind     string          `json:"kind"` // copy | move
	Root     string          `json:"root"`
	Sources  []string        `json:"sources"`
	Dest     string          `json:"dest"`
	Conflict Conflict        `json:"conflict"`
	Status   string          `json:"status"` // running | done | failed | canceled
	Progress Progress        `json:"progress"`
	Result   *TransferResult `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
	Started  time.Time       `json:"started"`
	Ended    *time.Time      `json:"ended,omitempty"`
	UserID   string          `json:"-"`
}

type job struct {
	mu     sync.Mutex
	j      Job
	cancel context.CancelFunc
}

func (x *job) snapshot() Job {
	x.mu.Lock()
	defer x.mu.Unlock()
	out := x.j
	out.Sources = append([]string(nil), x.j.Sources...)
	return out
}

type Jobs struct {
	svc   *Service
	mu    sync.Mutex
	jobs  map[string]*job
	order []string
}

func NewJobs(svc *Service) *Jobs { return &Jobs{svc: svc, jobs: map[string]*job{}} }

// Start launches a copy or move. onDone runs when it finishes (any outcome).
func (js *Jobs) Start(userID, root string, rels []string, dest string, move bool, conflict Conflict, display func(string) string, onDone func(Job)) Job {
	ctx, cancel := context.WithCancel(context.Background())
	kind := "copy"
	if move {
		kind = "move"
	}
	srcs := make([]string, len(rels))
	for i, r := range rels {
		srcs[i] = display(r)
	}
	x := &job{cancel: cancel, j: Job{ID: randHex(), Kind: kind, Root: root, Sources: srcs, Dest: display(dest), Conflict: conflict,
		Status: "running", Started: time.Now().UTC(), UserID: userID}}

	js.mu.Lock()
	js.jobs[x.j.ID] = x
	js.order = append(js.order, x.j.ID)
	for len(js.order) > maxJobs {
		old := js.jobs[js.order[0]]
		if old != nil && old.snapshot().Status == "running" {
			break
		}
		delete(js.jobs, js.order[0])
		js.order = js.order[1:]
	}
	js.mu.Unlock()

	go func() {
		defer cancel()
		res, err := js.svc.TransferWith(ctx, root, rels, dest, TransferOptions{
			Move: move, Conflict: conflict,
			OnProgress: func(p Progress) {
				x.mu.Lock()
				x.j.Progress = p
				x.mu.Unlock()
			},
		})
		for i := range res.Paths {
			res.Paths[i] = display(res.Paths[i])
		}
		for i := range res.Skipped {
			res.Skipped[i] = display(res.Skipped[i])
		}
		now := time.Now().UTC()
		x.mu.Lock()
		x.j.Ended, x.j.Result = &now, &res
		switch {
		case errors.Is(err, context.Canceled):
			x.j.Status = "canceled"
		case err != nil:
			x.j.Status, x.j.Error = "failed", err.Error()
		default:
			x.j.Status = "done"
		}
		x.mu.Unlock()
		if onDone != nil {
			onDone(x.snapshot())
		}
	}()
	return x.snapshot()
}

// Get returns a job if userID owns it (admins see their own transfers).
func (js *Jobs) Get(id, userID string) (Job, bool) {
	js.mu.Lock()
	x := js.jobs[id]
	js.mu.Unlock()
	if x == nil {
		return Job{}, false
	}
	j := x.snapshot()
	return j, j.UserID == userID
}

// List returns a user's recent jobs, newest first.
func (js *Jobs) List(userID string) []Job {
	js.mu.Lock()
	ids := append([]string(nil), js.order...)
	js.mu.Unlock()
	out := []Job{}
	for i := len(ids) - 1; i >= 0; i-- {
		if j, ok := js.Get(ids[i], userID); ok {
			out = append(out, j)
		}
	}
	return out
}

// Cancel stops a running job owned by userID.
func (js *Jobs) Cancel(id, userID string) bool {
	js.mu.Lock()
	x := js.jobs[id]
	js.mu.Unlock()
	if x == nil || x.snapshot().UserID != userID {
		return false
	}
	x.cancel()
	return true
}
