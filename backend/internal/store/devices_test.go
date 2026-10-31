package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDevices(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().Truncate(time.Second)
	for _, id := range []string{"u1", "u2"} {
		if err := st.CreateUser(ctx, &User{ID: id, Username: "name-" + id, PasswordHash: "x", Role: RoleUser, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	d1 := &Device{ID: "d1", UserID: "u1", Name: "Pixel 8", Platform: "android", Battery: -1, CreatedAt: now, LastSeenAt: now}
	d2 := &Device{ID: "d2", UserID: "u2", Name: "Tablet", Platform: "other", Battery: -1, CreatedAt: now.Add(time.Second)}
	if err := st.CreateDevice(ctx, d1, "hash1"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDevice(ctx, d2, "hash2"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDevice(ctx, &Device{ID: "d3", UserID: "u1", Name: "x", Platform: "ios", CreatedAt: now}, "hash1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate token hash: %v", err)
	}

	got, err := st.DeviceByTokenHash(ctx, "hash1")
	if err != nil || got.ID != "d1" || got.Username != "name-u1" || got.Battery != -1 || !got.LastSeenAt.Equal(now) {
		t.Fatalf("by hash: %+v %v", got, err)
	}
	if _, err := st.DeviceByTokenHash(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown hash: %v", err)
	}

	mine, _ := st.ListDevices(ctx, "u1")
	all, _ := st.ListDevices(ctx, "")
	if len(mine) != 1 || mine[0].ID != "d1" || len(all) != 2 || all[0].ID != "d2" {
		t.Fatalf("list: mine %+v all %+v", mine, all)
	}

	b, v := 87, "1.0.0"
	if err := st.TouchDevice(ctx, "d1", now.Add(time.Minute), &b, &v); err != nil {
		t.Fatal(err)
	}
	got, _ = st.DeviceByTokenHash(ctx, "hash1")
	if got.Battery != 87 || got.AppVersion != "1.0.0" || !got.LastSeenAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("after touch: %+v", got)
	}

	// Someone else can't revoke your device; you (or an admin, userID "") can.
	if _, err := st.RevokeDevice(ctx, "d1", "u2", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoke as another user: %v", err)
	}
	if d, err := st.RevokeDevice(ctx, "d1", "u1", now); err != nil || d.Name != "Pixel 8" {
		t.Fatalf("revoke own: %+v %v", d, err)
	}
	if _, err := st.DeviceByTokenHash(ctx, "hash1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked device still authenticates: %v", err)
	}
	if _, err := st.RevokeDevice(ctx, "d1", "", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoke twice: %v", err)
	}
	if _, err := st.RevokeDevice(ctx, "d2", "", now); err != nil {
		t.Fatalf("admin revoke: %v", err)
	}
	if all, _ := st.ListDevices(ctx, ""); len(all) != 0 {
		t.Fatalf("revoked devices still listed: %+v", all)
	}

	// Devices go with their owner.
	d4 := &Device{ID: "d4", UserID: "u2", Name: "Phone", Platform: "ios", Battery: -1, CreatedAt: now}
	if err := st.CreateDevice(ctx, d4, "hash4"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteUser(ctx, "u2"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeviceByTokenHash(ctx, "hash4"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("device outlived its owner: %v", err)
	}
}
