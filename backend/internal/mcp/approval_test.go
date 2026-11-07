package mcp

import (
	"context"
	"testing"
	"time"
)

// waitPending waits until userID has n waiting approvals.
func waitPending(t *testing.T, b *Broker, userID string, n int) []Approval {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p := b.Pending(userID); len(p) == n {
			return p
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("never saw %d pending", n)
	return nil
}

func ask(ctx context.Context, b *Broker, userID string) <-chan Outcome {
	ch := make(chan Outcome, 1)
	go func() { ch <- b.Ask(ctx, Approval{UserID: userID, Agent: "bot", Tool: "write_file"}) }()
	return ch
}

func TestApproveDenyAlways(t *testing.T) {
	b := &Broker{Timeout: time.Minute}
	for _, d := range []Decision{Once, Always, Deny} {
		res := ask(context.Background(), b, "u1")
		p := waitPending(t, b, "u1", 1)
		if p[0].ID == "" || p[0].Agent != "bot" || p[0].ExpiresAt.Sub(p[0].CreatedAt) != time.Minute {
			t.Fatalf("pending: %+v", p[0])
		}
		// Someone else can't answer it.
		if err := b.Decide(p[0].ID, "u2", Once); err != ErrNoApproval {
			t.Fatalf("other user: %v", err)
		}
		if err := b.Decide(p[0].ID, "u1", d); err != nil {
			t.Fatal(err)
		}
		if out := <-res; out.Decision != d || out.TimedOut {
			t.Fatalf("%s: got %+v", d, out)
		}
		// Answered once only.
		if err := b.Decide(p[0].ID, "u1", Once); err != ErrNoApproval {
			t.Fatalf("second answer: %v", err)
		}
		waitPending(t, b, "u1", 0)
	}
	if err := b.Decide("x", "u1", "maybe"); err == nil {
		t.Fatal("bad decision accepted")
	}
}

func TestApprovalTimeoutDenies(t *testing.T) {
	b := &Broker{Timeout: 30 * time.Millisecond}
	out := b.Ask(context.Background(), Approval{UserID: "u1"})
	if out.Decision != Deny || !out.TimedOut {
		t.Fatalf("timeout: %+v", out)
	}
	if len(b.Pending("u1")) != 0 {
		t.Fatal("still pending after timeout")
	}
}

func TestApprovalCancelledDenies(t *testing.T) {
	b := &Broker{Timeout: time.Minute}
	ctx, cancel := context.WithCancel(context.Background())
	res := ask(ctx, b, "u1")
	waitPending(t, b, "u1", 1)
	cancel()
	if out := <-res; out.Decision != Deny || out.TimedOut {
		t.Fatalf("cancelled: %+v", out)
	}
}

func TestWatchAndOnAsk(t *testing.T) {
	asked := make(chan Approval, 1)
	b := &Broker{Timeout: time.Minute, OnAsk: func(a Approval) { asked <- a }}
	ch, cancel := b.Watch("u1")
	defer cancel()
	other, cancelOther := b.Watch("u2")
	defer cancelOther()
	res := ask(context.Background(), b, "u1")
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher not told")
	}
	a := <-asked
	if a.UserID != "u1" || a.ID == "" {
		t.Fatalf("OnAsk got %+v", a)
	}
	select {
	case <-other:
		t.Fatal("another person's watcher was told")
	default:
	}
	_ = b.Decide(a.ID, "u1", Deny)
	<-res
}
