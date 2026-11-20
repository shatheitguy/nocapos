package appdata

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"alfaos/alfad/internal/docker"
	"alfaos/alfad/internal/store"
)

// ---------- a fake Docker engine ----------

type fakeCtr struct {
	id, name string
	labels   map[string]string
	project  string
	spec     docker.Spec
	running  bool
	exit     int
}

type moveEngine struct {
	mu      sync.Mutex
	ctrs    map[string]*fakeCtr
	order   []string
	vols    map[string]docker.VolumeInfo
	next    int
	root    string
	crashes func(s docker.Spec) bool // a container with this spec dies on start
	deleted []string
}

func newMoveEngine(dockerRoot string) *moveEngine {
	return &moveEngine{ctrs: map[string]*fakeCtr{}, vols: map[string]docker.VolumeInfo{}, root: dockerRoot}
}

func (e *moveEngine) addVolume(t *testing.T, name string, labels map[string]string, files map[string]string) string {
	t.Helper()
	mp := filepath.Join(e.root, "volumes", name, "_data")
	if err := os.MkdirAll(mp, 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, content := range files {
		p := filepath.Join(mp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	e.vols[name] = docker.VolumeInfo{Name: name, Driver: "local", Mountpoint: mp, Labels: labels}
	return mp
}

func (e *moveEngine) add(name string, labels map[string]string, project string, mounts ...docker.SpecMount) string {
	e.next++
	id := fmt.Sprintf("c%02d", e.next)
	e.ctrs[id] = &fakeCtr{id: id, name: name, labels: labels, project: project, running: true,
		spec: docker.Spec{Name: name, Image: "img:" + name, Mounts: mounts, Restart: "unless-stopped"}}
	e.order = append(e.order, id)
	return id
}

func (e *moveEngine) byName(name string) *fakeCtr {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, c := range e.ctrs {
		if c.name == name {
			return c
		}
	}
	return nil
}

func (e *moveEngine) ListContainers(ctx context.Context, all bool) ([]docker.Container, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := []docker.Container{}
	for _, id := range e.order {
		c, ok := e.ctrs[id]
		if !ok {
			continue
		}
		state := "exited"
		if c.running {
			state = "running"
		}
		out = append(out, docker.Container{ID: c.id, Name: c.name, Labels: c.labels, Project: c.project, State: state})
	}
	return out, nil
}

func (e *moveEngine) InspectContainer(ctx context.Context, id string) (*docker.ContainerDetail, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.ctrs[id]
	if !ok {
		return nil, errors.New("no such container")
	}
	d := &docker.ContainerDetail{ID: c.id, Name: c.name, Running: c.running, ExitCode: c.exit, Labels: c.labels}
	d.State = "exited"
	if c.running {
		d.State = "running"
	}
	for _, m := range c.spec.Mounts {
		mt := docker.Mount{Type: m.Type, Source: m.Source, Destination: m.Target, RW: !m.ReadOnly}
		if m.Type == "volume" {
			mt.Name = m.Source
			mt.Source = e.vols[m.Source].Mountpoint
		}
		d.Mounts = append(d.Mounts, mt)
	}
	return d, nil
}

func (e *moveEngine) Info(ctx context.Context) (*docker.Info, error) {
	return &docker.Info{RootDir: e.root}, nil
}

func (e *moveEngine) GetSpec(ctx context.Context, ref string) (docker.Spec, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.ctrs[ref]
	if !ok {
		return docker.Spec{}, errors.New("no such container")
	}
	s := c.spec
	s.Mounts = append([]docker.SpecMount(nil), c.spec.Mounts...)
	return s, nil
}

func (e *moveEngine) Recreate(ctx context.Context, ref string, s docker.Spec, pull func(string) error) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	old, ok := e.ctrs[ref]
	if !ok {
		return "", errors.New("no such container")
	}
	e.next++
	id := fmt.Sprintf("c%02d", e.next)
	nc := &fakeCtr{id: id, name: s.Name, labels: old.labels, project: old.project, spec: s, running: old.running}
	delete(e.ctrs, ref)
	e.ctrs[id] = nc
	for i, o := range e.order {
		if o == ref {
			e.order[i] = id
		}
	}
	return id, nil
}

func (e *moveEngine) StartContainer(ctx context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.ctrs[id]
	if !ok {
		return errors.New("no such container")
	}
	if e.crashes != nil && e.crashes(c.spec) {
		c.running, c.exit = false, 1 // starts, then dies
		return nil
	}
	c.running, c.exit = true, 0
	return nil
}

func (e *moveEngine) StopContainer(ctx context.Context, id string, timeout time.Duration) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if c, ok := e.ctrs[id]; ok {
		c.running = false
	}
	return nil
}

func (e *moveEngine) VolumeDetails(ctx context.Context) ([]docker.VolumeInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := []docker.VolumeInfo{}
	for _, v := range e.vols {
		out = append(out, v)
	}
	return out, nil
}

func (e *moveEngine) DeleteVolume(ctx context.Context, name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.vols, name)
	e.deleted = append(e.deleted, name)
	return nil
}

// ---------- a fake database ----------

type fakeDB struct {
	mu       sync.Mutex
	settings map[string]string
	old      map[string]store.OldVolume
}

func newFakeDB() *fakeDB {
	return &fakeDB{settings: map[string]string{}, old: map[string]store.OldVolume{}}
}

func (d *fakeDB) Setting(ctx context.Context, key string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.settings[key], nil
}

func (d *fakeDB) SetSetting(ctx context.Context, key, value string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.settings[key] = value
	return nil
}

func (d *fakeDB) OldVolumes(ctx context.Context) ([]store.OldVolume, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []store.OldVolume{}
	for _, v := range d.old {
		out = append(out, v)
	}
	return out, nil
}

func (d *fakeDB) SaveOldVolume(ctx context.Context, v store.OldVolume) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.old[v.Volume] = v
	return nil
}

func (d *fakeDB) DeleteOldVolume(ctx context.Context, volume string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.old, volume)
	return nil
}

type fakeAppStore struct {
	calls []string
	remap func(docker.SpecMount) docker.SpecMount
}

func (f *fakeAppStore) Rebind(ctx context.Context, app string, remap func(docker.SpecMount) docker.SpecMount, folders bool) error {
	f.calls = append(f.calls, fmt.Sprintf("%s:%v", app, folders))
	f.remap = remap
	return nil
}

// ---------- setup ----------

type rig struct {
	engine *moveEngine
	db     *fakeDB
	mover  *Mover
	shop   *fakeAppStore
	root   string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	base := mkdir(t, t.TempDir())
	e := newMoveEngine(mkdir(t, base, "docker"))
	db := newFakeDB()
	root := filepath.Join(base, "srv", "appdata")
	mv := NewMover(e, db, func() []string { return []string{filepath.Join(base, "nocap-data")} }, root)
	mv.Copy, mv.Settle = GoCopy, 0
	shop := &fakeAppStore{}
	mv.AppStore = shop
	return &rig{engine: e, db: db, mover: mv, shop: shop, root: root}
}

func vol(name, target string) docker.SpecMount {
	return docker.SpecMount{Type: "volume", Source: name, Target: target}
}

// ---------- tests ----------

func TestCandidatesNamesFolders(t *testing.T) {
	r := newRig(t)
	e := r.engine
	e.addVolume(t, "nocap-media-config", nil, nil)
	e.addVolume(t, "notes_db", map[string]string{composeVolumeLabel: "db"}, nil)
	anon := strings.Repeat("ab", 32)
	e.addVolume(t, anon, nil, nil)
	e.addVolume(t, "wiki-data", nil, nil)
	e.addVolume(t, "shared", nil, nil)
	e.add("nocap-media-web", map[string]string{LabelApp: "media"}, "", vol("nocap-media-config", "/config"))
	e.add("notes-db-1", map[string]string{}, "notes", vol("notes_db", "/var/lib/postgresql/data"))
	e.add("wiki", map[string]string{}, "", vol("wiki-data", "/data"), vol(anon, "/cache"), vol("shared", "/x"))
	e.add("other", map[string]string{}, "", vol("shared", "/y"))

	cands, err := r.mover.Candidates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Candidate{}
	for _, c := range cands {
		got[c.App] = c
	}
	want := map[string][]string{
		"media": {"media/config"},
		"notes": {"notes/db"},
		"wiki":  {"wiki/data", "wiki/cache", "wiki/shared"},
	}
	for app, dirs := range want {
		c, ok := got[app]
		if !ok {
			t.Fatalf("%s missing from %+v", app, cands)
		}
		for i, d := range dirs {
			if w := filepath.ToSlash(filepath.Join(r.root, filepath.FromSlash(d))); c.Volumes[i].Dir != w {
				t.Errorf("%s volume %d â†’ %s, want %s", app, i, c.Volumes[i].Dir, w)
			}
		}
	}
	if got["media"].Kind != "app" || got["wiki"].Kind != "container" || got["notes"].Kind != "stack" {
		t.Errorf("kinds: %s %s %s", got["media"].Kind, got["wiki"].Kind, got["notes"].Kind)
	}
	if !strings.Contains(got["wiki"].Blocked, "shared") {
		t.Errorf("a volume shared between apps should block the move: %q", got["wiki"].Blocked)
	}
	if got["notes"].Blocked == "" {
		t.Error("a compose project NoCapOS doesn't manage should be blocked")
	}
}

func TestMoveAppIntoFolders(t *testing.T) {
	r := newRig(t)
	e := r.engine
	mp := e.addVolume(t, "nocap-media-config", nil, map[string]string{"settings.xml": "<x/>", "db/library.db": "data"})
	e.add("nocap-media-web", map[string]string{LabelApp: "media"}, "", vol("nocap-media-config", "/config"))
	e.add("unrelated", nil, "", docker.SpecMount{Type: "bind", Source: "/srv/films", Target: "/films"})

	if _, err := r.mover.Move(context.Background(), "media"); err != nil {
		t.Fatal(err)
	}
	j := r.mover.Wait()
	if j.Error != "" {
		t.Fatalf("move failed: %s", j.Error)
	}
	dir := filepath.Join(r.root, "media", "config")
	if b, err := os.ReadFile(filepath.Join(dir, "db", "library.db")); err != nil || string(b) != "data" {
		t.Fatalf("copied file = %q, %v", b, err)
	}
	c := e.byName("nocap-media-web")
	if c == nil || !c.running {
		t.Fatalf("container should be recreated and running: %+v", c)
	}
	if m := c.spec.Mounts[0]; m.Type != "bind" || m.Source != filepath.ToSlash(dir) || m.Target != "/config" {
		t.Errorf("mount = %+v, want a folder at %s", m, dir)
	}
	if c.spec.Image != "img:nocap-media-web" || c.spec.Restart != "unless-stopped" {
		t.Errorf("other settings changed: %+v", c.spec)
	}
	// The old volume is kept (and remembered) until an admin cleans it up.
	if _, err := os.Stat(filepath.Join(mp, "settings.xml")); err != nil {
		t.Error("the old volume's files should be kept")
	}
	if _, ok := r.db.old["nocap-media-config"]; !ok {
		t.Error("the old volume should be remembered for cleanup")
	}
	if len(e.deleted) != 0 {
		t.Errorf("no volume may be deleted by a move: %v", e.deleted)
	}
	if len(r.shop.calls) != 1 || r.shop.calls[0] != "media:true" {
		t.Errorf("App Store not told: %v", r.shop.calls)
	}
	if n := r.shop.remap(vol("nocap-media-config", "/config")); n.Type != "bind" {
		t.Errorf("saved changes remap = %+v", n)
	}
	// Nothing left to move; cleanup removes the remembered volume only.
	if cands, _ := r.mover.Candidates(context.Background()); len(cands) != 0 {
		t.Errorf("still candidates: %+v", cands)
	}
	if _, err := r.mover.RemoveOldVolumes(context.Background(), []string{"nocap-media-config", "someone-elses"}); err == nil {
		t.Error("removing a volume no move left should fail")
	}
	if len(e.deleted) != 1 || e.deleted[0] != "nocap-media-config" {
		t.Errorf("deleted = %v", e.deleted)
	}
}

func TestMoveRollsBack(t *testing.T) {
	r := newRig(t)
	e := r.engine
	e.addVolume(t, "nocap-media-config", nil, map[string]string{"a.txt": "a"})
	e.addVolume(t, "nocap-media-db", nil, map[string]string{"b.txt": "b"})
	e.add("nocap-media-db", map[string]string{LabelApp: "media"}, "", vol("nocap-media-db", "/var/lib/db"))
	e.add("nocap-media-web", map[string]string{LabelApp: "media"}, "", vol("nocap-media-config", "/config"))
	// The web container dies with its new folders.
	e.crashes = func(s docker.Spec) bool {
		return s.Name == "nocap-media-web" && len(s.Mounts) > 0 && s.Mounts[0].Type == "bind"
	}
	if _, err := r.mover.Move(context.Background(), "media"); err != nil {
		t.Fatal(err)
	}
	j := r.mover.Wait()
	if j.Error == "" || !strings.Contains(j.Error, "stopped after the move") {
		t.Fatalf("job error = %q, want the failed start", j.Error)
	}
	for _, name := range []string{"nocap-media-web", "nocap-media-db"} {
		c := e.byName(name)
		if c == nil || !c.running {
			t.Fatalf("%s should be back and running: %+v", name, c)
		}
		if c.spec.Mounts[0].Type != "volume" {
			t.Errorf("%s should use its volume again: %+v", name, c.spec.Mounts)
		}
	}
	if _, err := os.Stat(filepath.Join(r.root, "media")); !os.IsNotExist(err) {
		t.Errorf("the copies should be removed after a rollback: %v", err)
	}
	if len(r.db.old) != 0 || len(r.shop.calls) != 0 || len(e.deleted) != 0 {
		t.Errorf("a failed move must change nothing: old=%v calls=%v deleted=%v", r.db.old, r.shop.calls, e.deleted)
	}
}

func TestMoveRefusesFolderWithFiles(t *testing.T) {
	r := newRig(t)
	r.engine.addVolume(t, "wiki-data", nil, map[string]string{"x": "1"})
	r.engine.add("wiki", nil, "", vol("wiki-data", "/data"))
	mkdir(t, r.root, "wiki", "data", "already")
	if _, err := r.mover.Move(context.Background(), "wiki"); err != nil {
		t.Fatal(err)
	}
	if j := r.mover.Wait(); !strings.Contains(j.Error, "already has files") {
		t.Fatalf("error = %q", j.Error)
	}
	if c := r.engine.byName("wiki"); !c.running || c.spec.Mounts[0].Type != "volume" {
		t.Errorf("wiki should be untouched: %+v", c)
	}
}

func TestOrganizeAllReportsEachApp(t *testing.T) {
	r := newRig(t)
	e := r.engine
	e.addVolume(t, "a-data", nil, map[string]string{"f": "1"})
	e.addVolume(t, "b-data", nil, map[string]string{"f": "2"})
	e.add("a", nil, "", vol("a-data", "/data"))
	e.add("b", nil, "", vol("b-data", "/data"))
	e.crashes = func(s docker.Spec) bool { return s.Name == "b" && s.Mounts[0].Type == "bind" }
	if _, err := r.mover.OrganizeAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	j := r.mover.Wait()
	if len(j.Results) != 2 || !j.Results[0].OK || j.Results[1].OK {
		t.Fatalf("results = %+v", j.Results)
	}
	if e.byName("a").spec.Mounts[0].Type != "bind" || e.byName("b").spec.Mounts[0].Type != "volume" {
		t.Error("a should be moved, b put back")
	}
}

func TestMoveRoot(t *testing.T) {
	r := newRig(t)
	e := r.engine
	old := filepath.Join(r.root, "jellyfin", "config")
	mkdir(t, old)
	if err := os.WriteFile(filepath.Join(old, "s.xml"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	e.add("nocap-jellyfin-web", map[string]string{LabelApp: "jellyfin"}, "", docker.SpecMount{Type: "bind", Source: filepath.ToSlash(old), Target: "/config"})
	dst := filepath.Join(filepath.Dir(filepath.Dir(r.root)), "ssd", "appdata")
	if _, err := r.mover.MoveRoot(context.Background(), r.root); err == nil {
		t.Error("moving into the same folder should fail")
	}
	if _, err := r.mover.MoveRoot(context.Background(), dst); err != nil {
		t.Fatal(err)
	}
	if j := r.mover.Wait(); j.Error != "" {
		t.Fatal(j.Error)
	}
	if got := r.mover.Root(context.Background()); got != dst {
		t.Errorf("root = %s, want %s", got, dst)
	}
	c := e.byName("nocap-jellyfin-web")
	want := filepath.ToSlash(filepath.Join(dst, "jellyfin", "config"))
	if c.spec.Mounts[0].Source != want || !c.running {
		t.Errorf("mount = %+v running=%v, want %s", c.spec.Mounts[0], c.running, want)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "jellyfin", "config", "s.xml")); string(b) != "x" {
		t.Error("files not copied")
	}
	st := r.mover.State(context.Background())
	if st.OldRoot != filepath.ToSlash(r.root) {
		t.Errorf("old root = %q", st.OldRoot)
	}
	if _, err := os.Stat(old); err != nil {
		t.Error("the old copy must be kept until confirmed")
	}
	if err := r.mover.RemoveOldRoot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(r.root); !os.IsNotExist(err) {
		t.Error("old copy should be gone")
	}
}

func TestVerifyFindsDifferences(t *testing.T) {
	base := mkdir(t, t.TempDir())
	a, b := mkdir(t, base, "a"), mkdir(t, base, "b")
	for _, d := range []string{a, b} {
		if err := os.WriteFile(filepath.Join(d, "f.txt"), []byte("same"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Verify(context.Background(), a, b); err != nil {
		t.Fatalf("identical folders: %v", err)
	}
	if err := os.WriteFile(filepath.Join(b, "f.txt"), []byte("diff"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(context.Background(), a, b); err == nil {
		t.Error("changed content should be found")
	}
	if err := os.WriteFile(filepath.Join(b, "extra"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(context.Background(), a, b); err == nil {
		t.Error("an extra file should be found")
	}
}
