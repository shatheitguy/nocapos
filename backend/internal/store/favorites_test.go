package store

import (
	"context"
	"testing"
	"time"
)

func TestFileFavorites(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	if err := st.CreateUser(ctx, &User{ID: "u1", Username: "admin", PasswordHash: "x", Role: "admin", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	if favs, set, err := st.FileFavorites(ctx, "u1"); err != nil || set || len(favs) != 0 {
		t.Fatalf("fresh user: %v %v %v, want nothing saved", favs, set, err)
	}
	want := []FileFavorite{{Root: "drive", Path: "/Photos"}, {Root: "drive", Path: "/Documents"}}
	if err := st.SetFileFavorites(ctx, "u1", want); err != nil {
		t.Fatal(err)
	}
	favs, set, err := st.FileFavorites(ctx, "u1")
	if err != nil || !set || len(favs) != 2 || favs[0] != want[0] || favs[1] != want[1] {
		t.Fatalf("got %v %v %v, want %v", favs, set, err, want)
	}
	// An empty list is remembered as empty, not reset to the defaults.
	if err := st.SetFileFavorites(ctx, "u1", nil); err != nil {
		t.Fatal(err)
	}
	if favs, set, _ := st.FileFavorites(ctx, "u1"); !set || len(favs) != 0 {
		t.Fatalf("after clearing: %v %v", favs, set)
	}
	// Favorites go with the user.
	if err := st.DeleteUser(ctx, "u1"); err == nil {
		if _, set, _ := st.FileFavorites(ctx, "u1"); set {
			t.Fatal("favorites outlived their user")
		}
	}
}
