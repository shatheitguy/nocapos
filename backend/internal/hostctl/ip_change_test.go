package hostctl

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// lockedNM is fakeNM safe for the background apply/revert goroutines.
type lockedNM struct {
	mu      sync.Mutex
	answers map[string]string
	calls   []string
}

func (f *lockedNM) run(_ context.Context, name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	for prefix, out := range f.answers {
		if strings.HasPrefix(call, prefix) {
			return out, nil
		}
	}
	return "", nil
}

func (f *lockedNM) joined() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.calls, "\n")
}

func withLockedFake(t *testing.T, f *lockedNM) {
	t.Helper()
	oldRun, oldCan, oldDelay := runCmd, canControl, applyDelay
	runCmd, canControl, applyDelay = f.run, func() error { return nil }, 50*time.Millisecond
	t.Cleanup(func() { runCmd, canControl, applyDelay = oldRun, oldCan, oldDelay })
}

func TestIPv6Validate(t *testing.T) {
	good := []IPv6Config{
		{Method: "auto"},
		{Method: "disabled"},
		{Method: "auto", DNS: []string{"2606:4700:4700::1111"}},
		{Method: "manual", Address: "fd00::50/64", Gateway: "fd00::1", DNS: []string{"fd00::1"}},
		{Method: "manual", Address: "2001:db8:1::5/64", Gateway: "fe80::1"}, // link-local router
		{Method: "manual", Address: "2001:db8:1::5/64"},
	}
	for _, c := range good {
		if err := c.Validate(); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}
	bad := []IPv6Config{
		{Method: ""},
		{Method: "dhcp"},
		{Method: "manual", Address: "fd00::50"},
		{Method: "manual", Address: "192.168.1.5/24"},
		{Method: "manual", Address: "::ffff:192.168.1.5/120"},
		{Method: "manual", Address: "fd00::50/64", Gateway: "fd01::1"},
		{Method: "manual", Address: "fd00::50/64", Gateway: "192.168.1.1"},
		{Method: "auto", DNS: []string{"1.1.1.1"}},
		{Method: "auto", DNS: []string{"fd00::1; rm -rf /"}},
	}
	for _, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("%+v: accepted", c)
		}
	}
}

func TestModifyArgs6(t *testing.T) {
	got := strings.Join(modifyArgs6(IPv6Config{Method: "manual", Address: "fd00::50/64", Gateway: "fd00::1", DNS: []string{"fd00::1"}}), " ")
	if got != "ipv6.method manual ipv6.addresses fd00::50/64 ipv6.gateway fd00::1 ipv6.dns fd00::1 ipv6.ignore-auto-dns no" {
		t.Fatalf("manual: %q", got)
	}
	got = strings.Join(modifyArgs6(IPv6Config{Method: "auto", DNS: []string{"fd00::1"}}), " ")
	if got != "ipv6.method auto ipv6.addresses  ipv6.gateway  ipv6.dns fd00::1 ipv6.ignore-auto-dns yes" {
		t.Fatalf("auto: %q", got)
	}
}

func TestChangeIPConfirmOnlyOnceLive(t *testing.T) {
	f := &lockedNM{answers: map[string]string{
		"nmcli -t -f ipv4.method,ipv4.addresses,ipv4.gateway,ipv4.dns connection show Wired": "ipv4.method:auto\nipv4.addresses:\nipv4.gateway:--\nipv4.dns:",
		"nmcli -t -f ipv6.method,ipv6.addresses,ipv6.gateway,ipv6.dns connection show Wired": "ipv6.method:link-local\nipv6.addresses:\nipv6.gateway:--\nipv6.dns:",
	}}
	withLockedFake(t, f)
	change := IPChange{
		IPv4: IPv4Config{Method: "manual", Address: "192.168.1.50/24", Gateway: "192.168.1.1", DNS: []string{"192.168.1.1"}},
		IPv6: &IPv6Config{Method: "manual", Address: "fd00::50/64", Gateway: "fd00::1"},
	}

	// Confirmed after it's live: stays.
	tok, err := ChangeIP(context.Background(), "Wired", change, 600*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if got := ConfirmIP(tok); got != ConfirmPending {
		t.Fatalf("confirm before the change is live = %q, want pending", got)
	}
	time.Sleep(200 * time.Millisecond)
	if got := ConfirmIP(tok); got != ConfirmKept {
		t.Fatalf("confirm once live = %q", got)
	}
	if got := ConfirmIP(tok); got != ConfirmGone {
		t.Fatalf("second confirm = %q", got)
	}
	time.Sleep(700 * time.Millisecond)
	j := f.joined()
	if !strings.Contains(j, "ipv4.method manual ipv4.addresses 192.168.1.50/24") || !strings.Contains(j, "ipv6.method manual ipv6.addresses fd00::50/64") {
		t.Fatalf("change never applied:\n%s", j)
	}
	if strings.Contains(j, "ipv6.method link-local") {
		t.Fatalf("confirmed change was reverted:\n%s", j)
	}

	// Never confirmed (the browser couldn't reach the new address): both
	// families go back to exactly what the profile had.
	tok, err = ChangeIP(context.Background(), "Wired", change, 400*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(900 * time.Millisecond)
	j = f.joined()
	if !strings.Contains(j, "ipv4.method auto ipv4.addresses  ipv4.gateway ") || !strings.Contains(j, "ipv6.method link-local ipv6.addresses  ipv6.gateway ") {
		t.Fatalf("change was not reverted:\n%s", j)
	}
	if got := ConfirmIP(tok); got != ConfirmGone {
		t.Fatalf("confirm after revert = %q", got)
	}
}

func TestChangeIPRejectsBadInput(t *testing.T) {
	withLockedFake(t, &lockedNM{})
	if _, err := ChangeIP(context.Background(), "Wired", IPChange{IPv4: IPv4Config{Method: "auto"}, IPv6: &IPv6Config{Method: "manual", Address: "nope"}}, time.Second); err == nil {
		t.Fatal("bad IPv6 accepted")
	}
	if _, err := ChangeIP(context.Background(), "", IPChange{IPv4: IPv4Config{Method: "auto"}}, time.Second); err == nil {
		t.Fatal("empty connection accepted")
	}
}
