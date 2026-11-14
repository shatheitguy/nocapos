package firewall

import (
	"context"
	"strings"
	"testing"
)

// A reverse proxy on another machine: the firewall sees the proxy, so both
// the browser's and the proxy's address get a guard rule.
func TestEnableKeepsProxyPeerIn(t *testing.T) {
	e, f := newFake(t)
	k := KeepFor("203.0.113.9", 8443).WithPeer("192.168.1.2")
	if _, err := e.Enable(context.Background(), k); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range f.rules {
		g := strings.Join(r, " ")
		for _, a := range []string{"203.0.113.9", "192.168.1.2"} {
			if strings.Contains(g, "from "+a+" ") {
				got[a] = true
			}
		}
	}
	if !got["203.0.113.9"] || !got["192.168.1.2"] {
		t.Fatalf("guards %q", f.rules)
	}
	// The same address twice is one guard per port.
	if n := len(KeepFor("192.168.1.2", 8443).WithPeer("::ffff:192.168.1.2").addrs()); n != 1 {
		t.Fatalf("%d addresses", n)
	}
}

// A rate limit on the admin's own port could cut a busy browser off: it
// is a risky change with an undo window and a guard rule.
func TestAddLimitIsRisky(t *testing.T) {
	e, f := newFake(t)
	f.active = true
	f.rules = [][]string{{"allow", "in", "proto", "tcp", "from", "any", "to", "any", "port", "8443"}}
	st, err := e.Add(context.Background(), Rule{Action: "limit", Direction: "in", Proto: "tcp", Port: "8443"}, 0, KeepFor("192.168.1.50", 8443))
	if err != nil {
		t.Fatal(err)
	}
	if st.Pending == nil {
		t.Fatal("a limit rule must start the keep-or-undo window")
	}
	guard := false
	for _, r := range f.rules {
		if strings.Contains(strings.Join(r, " "), "from 192.168.1.50 ") {
			guard = true
		}
	}
	if !guard {
		t.Fatalf("no guard rule: %q", f.rules)
	}
}

// Undo finishes even when the browser that asked has gone away.
func TestUndoOutlivesRequest(t *testing.T) {
	_, f := newFake(t)
	// Like exec.CommandContext: a cancelled context stops the command.
	e := NewWith(func(ctx context.Context, name string, args ...string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return f.run(ctx, name, args...)
	}, f.etc)
	st, err := e.Enable(context.Background(), KeepFor("192.168.1.50", 8443))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Undo(ctx, st.Pending.ID); err != nil {
		t.Fatal(err)
	}
	if f.active {
		t.Fatal("still active after undo")
	}
}
