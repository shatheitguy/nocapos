package api

import (
	"strings"
	"testing"

	"alfaos/alfad/internal/syspkg"
)

func TestSecurityNotification(t *testing.T) {
	if securityNotification([]syspkg.Update{{Name: "curl", Available: "1"}}) != nil {
		t.Error("no security updates: no notification")
	}
	if securityNotification([]syspkg.Update{{Name: "openssl", Available: "1", Security: true, Held: true}}) != nil {
		t.Error("held packages don't count")
	}
	a := securityNotification([]syspkg.Update{{Name: "openssl", Available: "3.0.15", Security: true}, {Name: "curl", Available: "1"}})
	if a == nil || a.Title != "1 security update for this system" || !strings.HasPrefix(a.Key, "sysupdate:security:") || a.Level != "warning" {
		t.Fatalf("unexpected notification %+v", a)
	}
	b := securityNotification([]syspkg.Update{{Name: "openssl", Available: "3.0.15", Security: true}})
	if a.Key != b.Key {
		t.Error("the same set of security updates should have the same key")
	}
	c := securityNotification([]syspkg.Update{{Name: "openssl", Available: "3.0.16", Security: true}, {Name: "libc6", Available: "2", Security: true}})
	if c.Key == a.Key || !strings.HasPrefix(c.Title, "2 security updates") {
		t.Errorf("a new set should notify again: %+v", c)
	}
}

func TestSortUpdates(t *testing.T) {
	ups := []syspkg.Update{{Name: "b"}, {Name: "linux-image-x", Kernel: true}, {Name: "z", Security: true}, {Name: "a"}}
	sortUpdates(ups)
	got := []string{}
	for _, u := range ups {
		got = append(got, u.Name)
	}
	if strings.Join(got, ",") != "z,linux-image-x,a,b" {
		t.Errorf("order = %v", got)
	}
}
