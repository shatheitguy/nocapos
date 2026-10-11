package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"alfaos/alfad/internal/appstore"
	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/config"
	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/spaces"
	"alfaos/alfad/internal/store"
)

type spacesWorld struct {
	s      *Server
	drive  string
	media  int64
	secret int64
	users  map[string]*store.User
}

func newSpacesWorld(t *testing.T) *spacesWorld {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	drive := filepath.Join(dir, "drive")
	for _, p := range []string{"Media/Photos", "Secret", "Docs"} {
		if err := os.MkdirAll(filepath.Join(drive, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(drive, "Media", "song.txt"), []byte("la la"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(drive, "Secret", "plan.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fsvc, err := files.New([]files.Root{{ID: "drive", Name: "Drive", Path: drive}})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sp, err := spaces.New(ctx, st, fsvc, log)
	if err != nil {
		t.Fatal(err)
	}
	sp.SetPeople(func(context.Context) ([]spaces.Person, error) {
		return []spaces.Person{{Name: "admin", Admin: true}, {Name: "alex"}, {Name: "sam"}}, nil
	})
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	w := &spacesWorld{drive: drive, users: map[string]*store.User{}}
	w.s = &Server{Deps: Deps{Config: &config.Config{MaxUpload: 1 << 20}, Store: st, Log: log, Files: fsvc, FileIndex: files.NewIndex(fsvc, done), FileJobs: files.NewJobs(fsvc), Spaces: sp}}
	for _, name := range []string{"admin", "alex", "sam"} {
		role := store.RoleUser
		if name == "admin" {
			role = store.RoleAdmin
		}
		u := &store.User{ID: store.NewID(), Username: name, PasswordHash: "x", Role: role, CreatedAt: time.Now(), UpdatedAt: time.Now()}
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		w.users[name] = u
	}
	media, secret := "Media", "Secret"
	mf, err := sp.CreateFolder(ctx, spaces.FolderInput{Name: &media, Path: "/Media"})
	if err != nil {
		t.Fatal(err)
	}
	sf, err := sp.CreateFolder(ctx, spaces.FolderInput{Name: &secret, Path: "/Secret"})
	if err != nil {
		t.Fatal(err)
	}
	w.media, w.secret = mf.ID, sf.ID
	if err := sp.SetPermission(ctx, mf.ID, "alex", spaces.ReadOnly); err != nil {
		t.Fatal(err)
	}
	return w
}

// call runs a handler as a signed-in person (the auth middleware is
// tested elsewhere; this exercises each handler's own access checks).
func (w *spacesWorld) call(t *testing.T, who string, h http.HandlerFunc, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, target, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req = req.WithContext(context.WithValue(req.Context(), userKey, w.users[who]))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func list(root, p string) string {
	return "/api/v1/files/list?" + url.Values{"root": {root}, "path": {p}}.Encode()
}

func text(root, p string) string {
	return "/api/v1/files/text?" + url.Values{"root": {root}, "path": {p}}.Encode()
}

func TestFilesAccessForPeople(t *testing.T) {
	w := newSpacesWorld(t)
	s := w.s
	home := spaces.HomeRootID("alex")
	media := spaces.ShareRootID(w.media)
	secret := spaces.ShareRootID(w.secret)

	// Locations: alex gets their Home and Media (read only); nothing else.
	rec := w.call(t, "alex", s.filesRoots, "GET", "/api/v1/files/roots", nil)
	var roots []rootView
	_ = json.Unmarshal(rec.Body.Bytes(), &roots)
	if len(roots) != 2 || roots[0].ID != home || roots[0].Kind != "home" || roots[1].ID != media || !roots[1].ReadOnly {
		t.Fatalf("alex's roots: %s", rec.Body)
	}
	rec = w.call(t, "admin", s.filesRoots, "GET", "/api/v1/files/roots", nil)
	if !strings.Contains(rec.Body.String(), `"id":"drive"`) || !strings.Contains(rec.Body.String(), `"id":"all-homes"`) {
		t.Errorf("admin's roots: %s", rec.Body)
	}

	expect := func(who string, h http.HandlerFunc, method, target string, body any, want int) {
		t.Helper()
		if rec := w.call(t, who, h, method, target, body); rec.Code != want {
			t.Errorf("%s %s %s as %s = %d, want %d (%s)", method, target, body, who, rec.Code, want, strings.TrimSpace(rec.Body.String()))
		}
	}

	// Home: read and write.
	expect("alex", s.filesWriteText, "PUT", "/api/v1/files/text", map[string]any{"root": home, "path": "/notes.txt", "content": "mine"}, http.StatusOK)
	expect("alex", s.filesList, "GET", list(home, "/"), nil, http.StatusOK)
	expect("alex", s.filesReadText, "GET", text(home, "/notes.txt"), nil, http.StatusOK)
	expect("alex", s.filesMkdir, "POST", "/api/v1/files/mkdir", map[string]any{"root": home, "path": "/", "name": "Work"}, http.StatusCreated)

	// A read-only share: look, don't touch.
	expect("alex", s.filesList, "GET", list(media, "/"), nil, http.StatusOK)
	expect("alex", s.filesReadText, "GET", text(media, "/song.txt"), nil, http.StatusOK)
	expect("alex", s.filesWriteText, "PUT", "/api/v1/files/text", map[string]any{"root": media, "path": "/song.txt", "content": "x"}, http.StatusForbidden)
	expect("alex", s.filesMkdir, "POST", "/api/v1/files/mkdir", map[string]any{"root": media, "path": "/", "name": "Mine"}, http.StatusForbidden)
	expect("alex", s.filesDelete, "POST", "/api/v1/files/delete", map[string]any{"root": media, "paths": []string{"/song.txt"}}, http.StatusForbidden)
	expect("alex", s.filesRename, "POST", "/api/v1/files/rename", map[string]any{"root": media, "path": "/song.txt", "name": "x.txt"}, http.StatusForbidden)
	expect("alex", s.filesTransfer, "POST", "/api/v1/files/transfer", map[string]any{"root": media, "paths": []string{"/song.txt"}, "dest": "/Photos"}, http.StatusForbidden)

	// Forbidden places, however the path is written.
	for _, target := range []string{
		list("drive", "/"), list("drive", "/Secret"), list(secret, "/"), list(spaces.HomeRootID("sam"), "/"),
		list(spaces.HomesRootID, "/"), list("system", "/"), list("nope", "/"),
		text("drive", "/Secret/plan.txt"), text(secret, "/plan.txt"), text("drive", "/../Secret/plan.txt"),
	} {
		expect("alex", s.filesList, "GET", target, nil, http.StatusForbidden)
	}
	expect("sam", s.filesList, "GET", list(media, "/"), nil, http.StatusForbidden)
	expect("sam", s.filesReadText, "GET", text(home, "/notes.txt"), nil, http.StatusForbidden)
	// ".." is cleaned within the location: it can't climb out of a home.
	if rec := w.call(t, "alex", s.filesReadText, "GET", text(home, "/../../Secret/plan.txt"), nil); rec.Code == http.StatusOK {
		t.Errorf("climbed out of home: %s", rec.Body)
	}
	// File links (downloads, thumbnails) are only issued for allowed places.
	expect("alex", s.filesTicket, "POST", "/api/v1/files/ticket", map[string]any{"root": secret, "path": "/plan.txt"}, http.StatusForbidden)
	expect("alex", s.filesTicket, "POST", "/api/v1/files/ticket", map[string]any{"root": home, "path": "/notes.txt"}, http.StatusOK)

	// Uploads land only where people may write.
	upload := func(root string) int {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		fw, _ := mw.CreateFormFile("file", "up.txt")
		_, _ = fw.Write([]byte("uploaded"))
		_ = mw.Close()
		req := httptest.NewRequest("POST", "/api/v1/files/upload?"+url.Values{"root": {root}, "path": {"/"}}.Encode(), &b)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req = req.WithContext(context.WithValue(req.Context(), userKey, w.users["alex"]))
		rec := httptest.NewRecorder()
		s.filesUpload(rec, req)
		return rec.Code
	}
	if c := upload(home); c != http.StatusCreated {
		t.Errorf("upload to home: %d", c)
	}
	if c := upload(media); c != http.StatusForbidden {
		t.Errorf("upload to read-only share: %d", c)
	}
	if c := upload("drive"); c != http.StatusForbidden {
		t.Errorf("upload to the drive: %d", c)
	}

	// Trash: deleting in your home goes to your home's own recycle bin.
	expect("alex", s.filesDelete, "POST", "/api/v1/files/delete", map[string]any{"root": home, "paths": []string{"/notes.txt"}}, http.StatusNoContent)
	if ents, _ := os.ReadDir(filepath.Join(w.drive, spaces.HomesDir, "alex", files.RecycleDir)); len(ents) != 1 {
		t.Errorf("home recycle bin has %d items", len(ents))
	}

	// Read/write on the share once granted.
	if err := s.Spaces.SetPermission(context.Background(), w.media, "alex", spaces.ReadWrite); err != nil {
		t.Fatal(err)
	}
	expect("alex", s.filesMkdir, "POST", "/api/v1/files/mkdir", map[string]any{"root": media, "path": "/", "name": "Mine"}, http.StatusCreated)

	// Admins keep the full view.
	expect("admin", s.filesList, "GET", list("drive", "/Secret"), nil, http.StatusOK)
}

func TestSearchAndFavoritesScoped(t *testing.T) {
	w := newSpacesWorld(t)
	s := w.s
	if _, err := s.Spaces.EnsureHome("alex"); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(w.drive, spaces.HomesDir, "alex", "plan-mine.txt"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(w.drive, "Media", "plan-shared.txt"), []byte("x"), 0o644)
	// The index builds in the background.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if n, building, _ := s.FileIndex.Stats(); n > 0 && !building {
			hits := s.FileIndex.Search("plan", 50)
			if len(hits) >= 3 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("index never built")
		}
		s.FileIndex.Touch()
		time.Sleep(200 * time.Millisecond)
	}
	rec := w.call(t, "alex", s.filesSearch, "GET", "/api/v1/files/search?q=plan", nil)
	var res struct {
		Results []files.Hit `json:"results"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	got := map[string]bool{}
	for _, h := range res.Results {
		got[h.Root+":"+h.Path] = true
	}
	if len(res.Results) != 2 || !got[spaces.HomeRootID("alex")+":/plan-mine.txt"] || !got[spaces.ShareRootID(w.media)+":/plan-shared.txt"] {
		t.Errorf("alex's search: %s", rec.Body)
	}
	rec = w.call(t, "sam", s.filesSearch, "GET", "/api/v1/files/search?q=plan", nil)
	if strings.Contains(rec.Body.String(), "plan") {
		t.Errorf("sam found others' files: %s", rec.Body)
	}

	// Favorites pointing somewhere a person can't go are dropped.
	_ = s.Store.SetFileFavorites(context.Background(), w.users["sam"].ID, []store.FileFavorite{{Root: "drive", Path: "/Secret"}, {Root: spaces.HomeRootID("sam"), Path: "/"}})
	rec = w.call(t, "sam", s.filesFavorites, "GET", "/api/v1/files/favorites", nil)
	if strings.Contains(rec.Body.String(), "Secret") || !strings.Contains(rec.Body.String(), "homedir-sam") {
		t.Errorf("sam's favorites: %s", rec.Body)
	}
}

func TestStoreAppsFilteredPerPerson(t *testing.T) {
	catalog := []appstore.App{{ID: "jellyfin", Name: "Jellyfin"}, {ID: "immich", Name: "Immich"}, {ID: "gitea", Name: "Gitea"}}
	status := map[string]*appstore.Installed{
		"jellyfin": {Status: "running", WebPort: 8096, Credentials: &appstore.Credentials{Username: "admin", Password: "s3cret"}},
		"immich":   {Status: "running", WebPort: 2283},
	}
	rules := map[string]*store.AppAccess{"immich": {Users: []string{"alex"}}}
	names := func(v spaces.Viewer) string {
		var out []string
		for _, a := range visibleStoreApps(catalog, status, rules, v) {
			if a.Installed.Credentials != nil || a.Installed.Containers != nil {
				t.Errorf("%s: app credentials leaked to %s", a.ID, v.Name)
			}
			out = append(out, a.ID)
		}
		return strings.Join(out, ",")
	}
	if got := names(spaces.Viewer{Name: "alex"}); got != "jellyfin,immich" {
		t.Errorf("alex sees %q", got)
	}
	if got := names(spaces.Viewer{Name: "sam"}); got != "jellyfin" {
		t.Errorf("sam sees %q", got)
	}
	if status["jellyfin"].Credentials == nil {
		t.Error("filtering changed the shared status")
	}
}

func TestFilesOnlyCantSignIn(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := auth.NewService(st, auth.NewIssuer(bytes.Repeat([]byte("k"), 32), time.Minute), auth.Options{RefreshTTL: time.Hour, SessionMaxAge: 2 * time.Hour})
	dir := newAppDir(st)
	for _, n := range []string{"desk-user", "file-user"} {
		if err := dir(ctx, n, "a-long-password-1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetUserAccess(ctx, "file-user", store.AccessFiles); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(ctx, "desk-user", "a-long-password-1", auth.ClientMeta{}); err != nil {
		t.Errorf("desktop user: %v", err)
	}
	if _, err := svc.Login(ctx, "file-user", "a-long-password-1", auth.ClientMeta{}); !errors.Is(err, auth.ErrFilesOnly) {
		t.Errorf("files-only user: %v", err)
	}
	if _, err := svc.Login(ctx, "file-user", "wrong-password-11", auth.ClientMeta{}); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("files-only, wrong password: %v", err)
	}
	// Still usable for file sharing.
	if name, admin, ok := svc.CheckPassword(ctx, "file-user", "a-long-password-1"); !ok || admin || name != "file-user" {
		t.Errorf("CheckPassword: %q %v %v", name, admin, ok)
	}
	// Switching someone to files-only ends their existing session.
	sess, err := svc.Login(ctx, "desk-user", "a-long-password-1", auth.ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserAccess(ctx, "desk-user", store.AccessFiles); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, sess.AccessToken); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("token after switching to files-only: %v", err)
	}
	if _, err := svc.Refresh(ctx, sess.RefreshToken, auth.ClientMeta{}); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("refresh after switching to files-only: %v", err)
	}
}

// newAppDir returns a helper that adds a NoCapOS account.
func newAppDir(st *store.Store) func(ctx context.Context, name, pw string) error {
	return func(ctx context.Context, name, pw string) error {
		hash, err := auth.HashPassword(pw)
		if err != nil {
			return err
		}
		return st.CreateUser(ctx, &store.User{ID: store.NewID(), Username: name, PasswordHash: hash, Role: store.RoleUser, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	}
}
