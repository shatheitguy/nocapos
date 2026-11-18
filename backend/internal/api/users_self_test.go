package api

import (
	"net/http"
	"testing"

	"alfaos/alfad/internal/accounts"
	"alfaos/alfad/internal/store"
)

func TestUsersUpdateSelfRules(t *testing.T) {
	w := newAuthWorld(t)
	boss := w.users["boss"]
	patch := func(u *store.User, name string, body map[string]any) int {
		return groupReq(t, w.s.usersUpdate, u, "PATCH", "/api/v1/users/"+name, map[string]string{"name": name}, body).Code
	}

	// Disabling yourself is never allowed.
	if c := patch(boss, "boss", map[string]any{"disabled": true}); c != http.StatusBadRequest {
		t.Fatalf("self disable: %d", c)
	}
	// Names can't be changed in app accounts mode; the error is a clean 400.
	if c := patch(boss, "alex", map[string]any{"full_name": "Alex Smith"}); c != http.StatusBadRequest {
		t.Fatalf("full name in app mode: %d", c)
	}
	// Stepping down yourself works while another administrator remains.
	if c := patch(boss, "boss", map[string]any{"role": "user"}); c != http.StatusOK {
		t.Fatalf("self demote with another admin: %d", c)
	}
	// The remaining administrator can't step down: they are the last one.
	admin2 := w.users["admin2"]
	if c := patch(admin2, "admin2", map[string]any{"role": "user"}); c != http.StatusBadRequest {
		t.Fatalf("last admin self demote: %d", c)
	}
}

func TestValidFullName(t *testing.T) {
	for _, ok := range []string{"", "Alex Smith", "Zoë Ñúñez"} {
		if err := accounts.ValidFullName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"a:b", "a,b", "a=b", "a\nb", string(make([]byte, 129))} {
		if accounts.ValidFullName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
