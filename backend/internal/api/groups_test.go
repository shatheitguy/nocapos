package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"

	"alfaos/alfad/internal/store"
)

// withPath calls h as u with the {name}/{id} path values set.
func withPath(t *testing.T, h http.HandlerFunc, u *store.User, method, target string, values map[string]string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	r := httptest.NewRequest(method, target, &buf)
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(context.WithValue(r.Context(), userKey, u))
	for k, v := range values {
		r.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

func TestGroupsAPI(t *testing.T) {
	w := newAuthWorld(t)
	boss := w.users["boss"]

	rec := w.do(t, w.s.groupsCreate, boss, "POST", "/api/v1/groups", map[string]any{"name": " Crew ", "color": "#ff3b30", "members": []string{"ALEX", "boss"}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var g store.Group
	_ = json.Unmarshal(rec.Body.Bytes(), &g)
	if g.Name != "Crew" || !reflect.DeepEqual(g.Members, []string{"alex", "boss"}) {
		t.Fatalf("created %+v", g)
	}

	for name, body := range map[string]map[string]any{
		"taken":        {"name": "crew"},
		"empty":        {"name": "  "},
		"bad color":    {"name": "x", "color": "red"},
		"unknown user": {"name": "y", "members": []string{"ghost"}},
	} {
		if rec := w.do(t, w.s.groupsCreate, boss, "POST", "/api/v1/groups", body); rec.Code < 400 {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}

	id := strconv.FormatInt(g.ID, 10)
	rec = withPath(t, w.s.groupsUpdate, boss, "PUT", "/api/v1/groups/"+id, map[string]string{"id": id}, map[string]any{"name": "Crew", "description": "the squad", "members": []string{"admin2"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}

	// The people list shows each person's groups and activity.
	signInAs(t, w, w.users["admin2"], uaFirefox, "10.0.0.7")
	rec = w.do(t, w.s.usersList, boss, "GET", "/api/v1/users", nil)
	var out struct {
		Users []userView `json:"users"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	by := map[string]userView{}
	for _, u := range out.Users {
		by[u.Username] = u
	}
	if !reflect.DeepEqual(by["admin2"].Groups, []string{"Crew"}) || len(by["alex"].Groups) != 0 {
		t.Fatalf("groups: %v / %v", by["admin2"].Groups, by["alex"].Groups)
	}
	if by["admin2"].Sessions != 1 || by["admin2"].LastSeen == nil || by["alex"].Sessions != 0 || by["alex"].LastSeen != nil {
		t.Fatalf("activity: %+v / %+v", by["admin2"], by["alex"])
	}
	if by["boss"].CreatedAt == nil || !by["boss"].You {
		t.Fatalf("boss: %+v", by["boss"])
	}

	// Deleting a person drops them from their groups.
	if rec := withPath(t, w.s.usersDelete, boss, "DELETE", "/api/v1/users/admin2", map[string]string{"name": "admin2"}, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete user: %d %s", rec.Code, rec.Body)
	}
	if got, _ := w.st.Group(context.Background(), g.ID); len(got.Members) != 0 {
		t.Fatalf("still members: %v", got.Members)
	}

	// An account removed outside NoCapOS (userdel) can linger in a group:
	// saving the group with it still listed drops it instead of failing.
	gone := &store.Group{ID: g.ID, Name: "Crew", Members: []string{"gone", "alex"}}
	if err := w.st.UpdateGroup(context.Background(), gone); err != nil {
		t.Fatal(err)
	}
	rec = withPath(t, w.s.groupsUpdate, boss, "PUT", "/api/v1/groups/"+id, map[string]string{"id": id}, map[string]any{"name": "Crew", "members": []string{"gone", "alex", "boss"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("update with stale member: %d %s", rec.Code, rec.Body)
	}
	if got, _ := w.st.Group(context.Background(), g.ID); !reflect.DeepEqual(got.Members, []string{"alex", "boss"}) {
		t.Fatalf("stale member kept: %v", got.Members)
	}
	rec = withPath(t, w.s.groupsUpdate, boss, "PUT", "/api/v1/groups/"+id, map[string]string{"id": id}, map[string]any{"name": "Crew", "members": []string{"ghost"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown new member: %d", rec.Code)
	}

	if rec := withPath(t, w.s.groupsDelete, boss, "DELETE", "/api/v1/groups/"+id, map[string]string{"id": id}, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := withPath(t, w.s.groupsDelete, boss, "DELETE", "/api/v1/groups/"+id, map[string]string{"id": id}, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("delete twice: %d", rec.Code)
	}
}

func TestUserAvatarForAdmins(t *testing.T) {
	w := newAuthWorld(t)
	png := []byte("\x89PNG\r\n\x1a\n0000")
	if err := w.st.SetAvatar(context.Background(), w.users["alex"].ID, "image/png", png); err != nil {
		t.Fatal(err)
	}
	rec := withPath(t, w.s.userAvatarGet, w.users["boss"], "GET", "/api/v1/users/alex/avatar", map[string]string{"name": "alex"}, nil)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), png) || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("avatar: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec := withPath(t, w.s.userAvatarGet, w.users["boss"], "GET", "/api/v1/users/boss/avatar", map[string]string{"name": "boss"}, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no photo: %d", rec.Code)
	}
}
