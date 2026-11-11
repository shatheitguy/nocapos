package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSessionInfo(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().Truncate(time.Second)
	if err := st.CreateUser(ctx, &User{ID: "u1", Username: "alex", PasswordHash: "x", Role: RoleUser, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	token := func(hash, family string, created time.Time) {
		t.Helper()
		if err := st.InsertRefreshToken(ctx, &RefreshToken{TokenHash: hash, FamilyID: family, UserID: "u1", CreatedAt: created,
			ExpiresAt: now.Add(time.Hour), FamilyExpiresAt: now.Add(time.Hour), UserAgent: "ua-" + family, IP: "10.0.0.1"}); err != nil {
			t.Fatal(err)
		}
	}

	// A session from before details were recorded still lists, from its token.
	token("h-old", "old", now.Add(-time.Hour))
	token("h-new", "new", now)
	if err := st.SaveSessionInfo(ctx, &SessionInfo{ID: "new", UserID: "u1", Kind: SessionWeb, DeviceKey: "web|chrome|windows",
		UserAgent: "Chrome", IP: "10.0.0.2", CreatedAt: now, LastSeenAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchSession(ctx, "new", "10.0.0.3", "", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	list, err := st.ActiveSessions(ctx, "u1", now)
	if err != nil || len(list) != 2 {
		t.Fatalf("active: %+v %v", list, err)
	}
	if a := list[0]; a.ID != "new" || a.IP != "10.0.0.3" || a.UserAgent != "Chrome" || a.Kind != SessionWeb || !a.LastSeenAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("recorded session: %+v", a)
	}
	if a := list[1]; a.ID != "old" || a.Kind != "" || a.UserAgent != "ua-old" || a.Username != "alex" || !a.CreatedAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("legacy session: %+v", a)
	}
	if _, err := st.ActiveSession(ctx, "nope", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown session: %v", err)
	}

	known, any, err := st.KnownDevice(ctx, "u1", "web|chrome|windows", "other")
	if err != nil || !known || !any {
		t.Fatalf("known device: %v %v %v", known, any, err)
	}
	if known, any, _ := st.KnownDevice(ctx, "u1", "web|chrome|windows", "new"); known || any {
		t.Fatalf("the session itself counted: %v %v", known, any)
	}

	ids, err := st.RevokeUserSessionsExcept(ctx, "u1", "new", now)
	if err != nil || len(ids) != 1 || ids[0] != "old" {
		t.Fatalf("revoke others: %v %v", ids, err)
	}
	if list, _ := st.ActiveSessions(ctx, "", now); len(list) != 1 || list[0].ID != "new" {
		t.Fatalf("after revoke: %+v", list)
	}

	// Details stay while the session lives, and go once it's ended and old.
	if n, _ := st.PurgeSessionInfo(ctx, now.Add(48*time.Hour), now); n != 0 {
		t.Fatalf("purged a live session's details: %d", n)
	}
	_ = st.RevokeFamily(ctx, "new", now)
	if n, _ := st.PurgeSessionInfo(ctx, now.Add(48*time.Hour), now); n != 1 {
		t.Fatalf("purge: %d", n)
	}
}
