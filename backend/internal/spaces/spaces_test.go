package spaces

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/store"
)

func TestFolderPerm(t *testing.T) {
	f := &store.SharedFolder{ID: 1, Name: "Media"}
	locked := &store.SharedFolder{ID: 2, Name: "Docs", ReadOnly: true}
	perms := map[string]string{"alex": "rw", "sam": "ro"}
	cases := []struct {
		v      Viewer
		folder *store.SharedFolder
		want   Perm
	}{
		{Viewer{Name: "admin", Admin: true}, f, ReadWrite},
		{Viewer{Name: "Alex"}, f, ReadWrite}, // user names don't care about case
		{Viewer{Name: "sam"}, f, ReadOnly},
		{Viewer{Name: "kim"}, f, None}, // not on the list: no access
		{Viewer{Name: "alex"}, locked, ReadOnly},
		{Viewer{Name: "admin", Admin: true}, locked, ReadOnly},
		{Viewer{Name: "kim"}, locked, None},
		{Viewer{Name: ""}, f, None},
		{Viewer{Name: "alex"}, nil, None},
	}
	for _, c := range cases {
		if got := FolderPerm(c.v, c.folder, perms); got != c.want {
			t.Errorf("FolderPerm(%+v, %v) = %q, want %q", c.v, c.folder, got, c.want)
		}
	}
	if FolderPerm(Viewer{Name: "x"}, f, map[string]string{"x": "bogus"}) != None {
		t.Error("unknown permission granted access")
	}
	for _, s := range []string{"rw", "ro", "none"} {
		if _, err := ParsePerm(s); err != nil {
			t.Errorf("ParsePerm(%q): %v", s, err)
		}
	}
	if _, err := ParsePerm("all"); err == nil {
		t.Error("ParsePerm accepted junk")
	}
}

func TestAppAllowed(t *testing.T) {
	selected := &store.AppAccess{Users: []string{"Alex"}}
	cases := []struct {
		rule *store.AppAccess
		v    Viewer
		want bool
	}{
		{nil, Viewer{Name: "sam"}, true}, // no rules: everyone
		{&store.AppAccess{Everyone: true}, Viewer{Name: "sam"}, true},
		{selected, Viewer{Name: "alex"}, true},
		{selected, Viewer{Name: "sam"}, false},
		{selected, Viewer{Name: "root", Admin: true}, true}, // admins use every app
		{&store.AppAccess{}, Viewer{Name: "sam"}, false},    // limited to nobody
	}
	for _, c := range cases {
		if got := AppAllowed(c.rule, c.v); got != c.want {
			t.Errorf("AppAllowed(%+v, %+v) = %v, want %v", c.rule, c.v, got, c.want)
		}
	}
}

// symlink makes a link or skips the test where links need privileges.
func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks not available: %v", err)
	}
}

func TestInside(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".", "a", "a/b", "/a/b", "a/../a/b", "../../a", `a\b`, "new/file.txt"} {
		if _, err := Inside(base, rel); err != nil {
			t.Errorf("Inside(%q): %v", rel, err)
		}
	}
	// ".." can't climb out: it is cleaned against the folder itself.
	got, err := Inside(base, "../../../etc/passwd")
	if err != nil || !within(got, base) {
		t.Errorf("Inside(..) = %q, %v", got, err)
	}
	if _, err := Inside(base, "a\x00b"); err == nil {
		t.Error("NUL accepted")
	}
	symlink(t, outside, filepath.Join(base, "out"))
	for _, rel := range []string{"out", "out/x", "out/new/deeper"} {
		if _, err := Inside(base, rel); !errors.Is(err, ErrOutside) {
			t.Errorf("Inside(%q) through a link out = %v, want ErrOutside", rel, err)
		}
	}
	symlink(t, filepath.Join(base, "a"), filepath.Join(base, "in"))
	if _, err := Inside(base, "in/b"); err != nil {
		t.Errorf("link inside the folder: %v", err)
	}
	if !IsInside(filepath.Join(base, "in", "b"), base) || IsInside(filepath.Join(base, "out"), base) {
		t.Error("IsInside")
	}
}

type world struct {
	m     *Manager
	fsvc  *files.Service
	drive string
	st    *store.Store
}

func newWorld(t *testing.T) *world {
	t.Helper()
	dir := t.TempDir()
	drive := filepath.Join(dir, "drive")
	for _, p := range []string{"Media/Movies", "Docs", "Secret"} {
		if err := os.MkdirAll(filepath.Join(drive, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fsvc, err := files.New([]files.Root{{ID: "drive", Name: "Drive", Path: drive}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(context.Background(), st, fsvc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	m.SetPeople(func(context.Context) ([]Person, error) {
		return []Person{{Name: "admin", Admin: true}, {Name: "alex"}, {Name: "sam"}}, nil
	})
	return &world{m: m, fsvc: fsvc, drive: drive, st: st}
}

func (w *world) share(t *testing.T, name, path string) Folder {
	t.Helper()
	f, err := w.m.CreateFolder(context.Background(), FolderInput{Name: &name, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestAccessAndLocations(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	m := w.m
	media := w.share(t, "Media", "/Media")
	secret := w.share(t, "Secret", "/Secret")
	if err := m.SetPermission(ctx, media.ID, "alex", ReadOnly); err != nil {
		t.Fatal(err)
	}
	alex, sam, admin := Viewer{Name: "alex"}, Viewer{Name: "sam"}, Viewer{Name: "admin", Admin: true}

	locs := m.Locations(alex)
	if len(locs) != 2 || locs[0].ID != HomeRootID("alex") || locs[1].ID != ShareRootID(media.ID) || !locs[1].ReadOnly {
		t.Fatalf("alex's locations: %+v", locs)
	}
	if _, err := os.Stat(filepath.Join(w.drive, HomesDir, "alex")); err != nil {
		t.Fatalf("home not created: %v", err)
	}
	cases := []struct {
		v         Viewer
		root, rel string
		want      Perm
	}{
		{alex, HomeRootID("alex"), ".", ReadWrite},
		{alex, HomeRootID("sam"), ".", None},
		{alex, ShareRootID(media.ID), "Movies", ReadOnly},
		{alex, ShareRootID(secret.ID), ".", None},
		{alex, "drive", ".", None}, // fixed locations are administrators'
		{alex, HomesRootID, ".", None},
		{alex, "share-x", ".", None},
		{sam, ShareRootID(media.ID), ".", None},
		{admin, "drive", "Secret", ReadWrite},
		{admin, HomeRootID("alex"), ".", ReadWrite},
		{admin, HomesRootID, ".", ReadWrite},
		{Viewer{}, HomeRootID(""), ".", None},
	}
	for _, c := range cases {
		if got := m.Access(c.v, c.root, c.rel); got != c.want {
			t.Errorf("Access(%s, %s, %s) = %q, want %q", c.v.Name, c.root, c.rel, got, c.want)
		}
	}

	// The per-person ids open the right folders, and file operations stay inside.
	if err := os.WriteFile(filepath.Join(w.drive, HomesDir, "alex", "a.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.EnsureHome("sam"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.drive, HomesDir, "sam", "s.txt"), []byte("sam's"), 0o600); err != nil {
		t.Fatal(err)
	}
	if txt, _, err := w.fsvc.ReadText(HomeRootID("alex"), "a.txt", 100); err != nil || txt != "hi" {
		t.Fatalf("read own home: %q %v", txt, err)
	}
	rel, _ := files.Clean("../sam/s.txt")
	if txt, _, err := w.fsvc.ReadText(HomeRootID("alex"), rel, 100); err == nil {
		t.Errorf("read another home through ..: %q", txt)
	}
	if _, err := w.fsvc.List(ShareRootID(media.ID), "."); err != nil {
		t.Errorf("list share: %v", err)
	}
	// A link inside a home pointing elsewhere goes nowhere.
	if err := os.Symlink(filepath.Join(w.drive, HomesDir, "sam"), filepath.Join(w.drive, HomesDir, "alex", "sams")); err == nil {
		if _, _, err := w.fsvc.ReadText(HomeRootID("alex"), "sams/s.txt", 100); err == nil {
			t.Error("followed a link into another home")
		}
	}

	// Admins who don't see homes lose them everywhere.
	if err := m.SetAdminsSeeHomes(ctx, false); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ root, rel string }{{HomeRootID("alex"), "."}, {HomesRootID, "."}, {"drive", "homes/alex"}, {"drive", "homes/alex/a.txt"}} {
		if got := m.Access(admin, c.root, c.rel); got != None {
			t.Errorf("admin without homes: Access(%s, %s) = %q", c.root, c.rel, got)
		}
	}
	if m.Access(admin, "drive", "Media") != ReadWrite || !m.HideHomes(admin, "drive", ".") {
		t.Error("admin without homes lost the rest")
	}
	if len(m.Locations(admin)) != 0 {
		t.Error("Homes location still offered")
	}
	hits := m.MapHits(admin, []files.Hit{{Root: "drive", Path: "/homes/alex/a.txt"}, {Root: "drive", Path: "/Media/x.jpg"}})
	if len(hits) != 1 || hits[0].Path != "/Media/x.jpg" {
		t.Errorf("admin hits: %+v", hits)
	}
}

func TestMapHits(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	media := w.share(t, "Media", "/Media")
	_ = w.share(t, "Docs", "/Docs")
	if err := w.m.SetPermission(ctx, media.ID, "alex", ReadWrite); err != nil {
		t.Fatal(err)
	}
	alex := Viewer{Name: "alex"}
	in := []files.Hit{
		{Root: "drive", Path: "/homes/alex/notes.txt"},
		{Root: "drive", Path: "/homes/sam/diary.txt"},
		{Root: "drive", Path: "/Media/Movies/film.mkv"},
		{Root: "drive", Path: "/Media"},
		{Root: "drive", Path: "/Docs/plan.txt"},
		{Root: "drive", Path: "/Secret/key.txt"},
		{Root: "other", Path: "/homes/alex/x"},
	}
	got := w.m.MapHits(alex, in)
	want := map[string]string{HomeRootID("alex"): "/notes.txt", ShareRootID(media.ID): "/Movies/film.mkv"}
	if len(got) != len(want) {
		t.Fatalf("hits: %+v", got)
	}
	for _, h := range got {
		if want[h.Root] != h.Path {
			t.Errorf("unexpected hit %+v", h)
		}
	}
}

func TestFolders(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	m := w.m
	name := "Projects"
	f, err := m.CreateFolder(ctx, FolderInput{Name: &name}) // a new folder, named after it
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(w.drive, "Projects")); err != nil || !st.IsDir() || f.Path != "/Projects" || !f.Recycle || !f.Network {
		t.Fatalf("created %+v (%v)", f, err)
	}
	for _, bad := range []FolderInput{
		{Name: strp("homes")},
		{Name: strp("Bad/Name")},
		{Name: strp("Whole"), Path: "/"},                    // holds everyone's homes
		{Name: strp("InHome"), Path: "/" + HomesDir + "/x"}, // inside them
		{Name: strp("Sys"), Root: "system", Path: "/"},
		{Name: strp("Nope"), Path: "/does/not/exist"},
		{Name: strp("projects")}, // taken (names ignore case)
	} {
		if bad.Path == "/"+HomesDir+"/x" {
			_ = os.MkdirAll(filepath.Join(w.drive, HomesDir, "x"), 0o755)
		}
		if _, err := m.CreateFolder(ctx, bad); err == nil {
			t.Errorf("CreateFolder(%s, %q) accepted", *bad.Name, bad.Path)
		}
	}
	off := false
	f, err = m.UpdateFolder(ctx, f.ID, FolderInput{Description: strp("Team\nwork"), Recycle: &off})
	if err != nil || f.Description != "Team work" || f.Recycle || m.Recycles(ShareRootID(f.ID)) {
		t.Fatalf("update: %+v %v", f, err)
	}
	if err := m.SetPermission(ctx, f.ID, "alex", ReadWrite); err != nil || !m.PerUserSharing() {
		t.Fatalf("permission: %v", err)
	}
	if got := m.PermissionsOf("ALEX"); got[f.ID] != "rw" {
		t.Errorf("PermissionsOf: %v", got)
	}
	// Deleting keeps the files unless asked.
	if err := os.WriteFile(filepath.Join(w.drive, "Projects", "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteFolder(ctx, f.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(w.drive, "Projects", "keep.txt")); err != nil {
		t.Error("files gone after stop sharing")
	}
	if m.PerUserSharing() {
		t.Error("permissions outlived their folder")
	}
	f, _ = m.CreateFolder(ctx, FolderInput{Name: &name})
	if err := m.DeleteFolder(ctx, f.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(w.drive, "Projects")); !os.IsNotExist(err) {
		t.Error("folder kept after delete data")
	}

	// App access: stopping one person on an everyone-app keeps the others.
	if err := m.SetAppAllowed(ctx, "jellyfin", "sam", false); err != nil {
		t.Fatal(err)
	}
	rules, _ := m.AppRules(ctx)
	if r := rules["jellyfin"]; r == nil || r.Everyone || !AppAllowed(r, Viewer{Name: "alex"}) || AppAllowed(r, Viewer{Name: "sam"}) {
		t.Errorf("rules after stopping sam: %+v", rules["jellyfin"])
	}
}

func strp(s string) *string { return &s }

func TestLinuxHomes(t *testing.T) {
	w := newWorld(t)
	home := t.TempDir() // stands in for /home
	passwd := map[string]string{
		"alex": filepath.Join(home, "alex"),
		"sam":  filepath.Join(home, "sam"),
		"svc":  "/var/lib/svc", // a service account's "home": never a private home
		"root": "/root",
	}
	w.m.SetHomes(LinuxHomes{Dir: home, Lookup: func(n string) (string, bool) {
		p, ok := passwd[n]
		return p, ok
	}})
	var owned []string
	w.m.SetOwner(func(name string) (int, int, bool) {
		owned = append(owned, name)
		return 1000, 1000, true
	})
	if w.m.HomePath("svc") != "" || w.m.HomePath("root") != "" || w.m.HomePath("nobody") != "" {
		t.Error("system folders accepted as homes")
	}
	alex := Viewer{Name: "alex"}
	locs := w.m.Locations(alex)
	if len(locs) != 1 || locs[0].Path != filepath.Join(home, "alex") {
		t.Fatalf("alex's home: %+v", locs)
	}
	if st, err := os.Stat(filepath.Join(home, "alex")); err != nil || !st.IsDir() {
		t.Fatal("missing Linux home not created")
	}
	if len(owned) != 1 || owned[0] != "alex" {
		t.Errorf("new home handed to %v", owned)
	}
	r, err := w.fsvc.Root(HomeRootID("alex"))
	if err != nil || !IsInside(r.Path, filepath.Join(home, "alex")) {
		t.Fatalf("home root: %+v %v", r, err)
	}
	// Files made in a Linux home go to its owner; elsewhere nothing changes.
	if _, err := w.fsvc.Mkdir(HomeRootID("alex"), ".", "Docs"); err != nil {
		t.Fatal(err)
	}
	owned = nil
	w.m.Own(HomeRootID("alex"), "Docs")
	w.m.Own(HomeRootID("alex"), ".")
	w.m.Own("drive", "Media")
	if len(owned) != 1 {
		t.Errorf("ownership lookups: %v", owned)
	}
	// Admins' Homes view is the folder holding the Linux homes.
	if hr, err := w.fsvc.Root(HomesRootID); err != nil || hr.Path != home {
		t.Errorf("Homes: %+v %v", hr, err)
	}
	if w.m.Access(alex, HomeRootID("sam"), ".") != None {
		t.Error("alex can open sam's Linux home")
	}
}
