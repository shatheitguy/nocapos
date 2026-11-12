package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestGroups(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	g := &Group{Name: "Family", Color: "#ff3b30", Members: []string{"mia", "alex"}}
	if err := st.CreateGroup(ctx, g); err != nil {
		t.Fatal(err)
	}
	if g.ID == 0 {
		t.Fatal("no id")
	}
	if err := st.CreateGroup(ctx, &Group{Name: "family"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("same name, other case: %v", err)
	}
	if err := st.CreateGroup(ctx, &Group{Name: "Work", Members: []string{"alex"}}); err != nil {
		t.Fatal(err)
	}

	list, err := st.Groups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "Family" || !reflect.DeepEqual(list[0].Members, []string{"alex", "mia"}) || list[1].Name != "Work" {
		t.Fatalf("groups: %+v %+v", list[0], list[1])
	}

	by, err := st.GroupsByMember(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(by["alex"], []string{"Family", "Work"}) || !reflect.DeepEqual(by["mia"], []string{"Family"}) {
		t.Fatalf("by member: %v", by)
	}

	g.Name, g.Description, g.Members = "Fam", "home crew", []string{"mia"}
	if err := st.UpdateGroup(ctx, g); err != nil {
		t.Fatal(err)
	}
	got, err := st.Group(ctx, g.ID)
	if err != nil || got.Name != "Fam" || got.Description != "home crew" || !reflect.DeepEqual(got.Members, []string{"mia"}) {
		t.Fatalf("updated: %+v %v", got, err)
	}
	if err := st.UpdateGroup(ctx, &Group{ID: 999, Name: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}

	// A deleted person leaves every group.
	if err := st.ForgetUser(ctx, "MIA"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Group(ctx, g.ID); len(got.Members) != 0 {
		t.Fatalf("mia still in %v", got.Members)
	}

	if err := st.DeleteGroup(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteGroup(ctx, g.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestSortedMembers(t *testing.T) {
	got := SortedMembers([]string{" Zed", "alex", "ALEX", "", "mia"})
	if !reflect.DeepEqual(got, []string{"alex", "mia", "Zed"}) {
		t.Fatalf("%v", got)
	}
}

func TestLastSeenAndAvatars(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().Truncate(time.Second).UTC()
	for _, id := range []string{"u1", "u2"} {
		if err := st.CreateUser(ctx, &User{ID: id, Username: "n" + id, PasswordHash: "x", Role: RoleUser, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SaveSessionInfo(ctx, &SessionInfo{ID: "s1", UserID: "u1", Kind: SessionWeb, CreatedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	seen, err := st.LastSeenAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !seen["u1"].Equal(now.Add(-time.Minute)) {
		t.Fatalf("u1 last seen %v", seen["u1"])
	}
	if _, ok := seen["u2"]; ok {
		t.Fatal("u2 was never seen")
	}

	if err := st.SetAvatar(ctx, "u2", "image/png", []byte{1}); err != nil {
		t.Fatal(err)
	}
	photos, err := st.AvatarOwners(ctx)
	if err != nil || !photos["u2"] || photos["u1"] {
		t.Fatalf("avatars %v %v", photos, err)
	}
}
