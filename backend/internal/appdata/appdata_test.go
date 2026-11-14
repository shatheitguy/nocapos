package appdata

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"alfaos/alfad/internal/docker"
)

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	return p
}

func TestCheck(t *testing.T) {
	base := mkdir(t, t.TempDir())
	data := mkdir(t, base, "nocap-data")
	volumes := mkdir(t, base, "docker", "volumes")
	vol := mkdir(t, volumes, "media_config", "_data")
	bind := mkdir(t, base, "srv", "media")
	file := filepath.Join(base, "one.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	protected := []string{data}
	vols := []string{volumes}

	ok := []struct {
		src    string
		volume bool
	}{
		{bind, false},
		{vol, true},
		{mkdir(t, data, "apps", "x"), false}, // inside NoCapOS's data folder, not holding it
		{mkdir(t, data, "stacks", "media", "config"), false},
	}
	for _, c := range ok {
		if _, err := Check(c.src, c.volume, protected, vols); err != nil {
			t.Errorf("Check(%q) = %v, want ok", c.src, err)
		}
	}
	bad := []struct {
		src    string
		volume bool
	}{
		{"", false},
		{"relative/path", false},
		{filepath.Join(base, "missing"), false},
		{file, false},                            // a single file
		{bind, true},                             // a "volume" outside Docker's volumes folder
		{volumes, true},                          // the volumes folder itself
		{base, false},                            // holds NoCapOS's data
		{data, false},                            // is NoCapOS's data
		{filepath.Dir(base), false},              // holds it too
		{mkdir(t, data, "secrets"), false},       // NoCapOS's own keys
		{filepath.Join(data, "stacks"), false},   // every app's folder at once
		{mkdir(t, data, "tls", "nested"), false}, // anything else inside its data
		{bind + string(filepath.Separator) + ".." + string(filepath.Separator) + "..", false}, // .. leads to base
	}
	for _, c := range bad {
		if p, err := Check(c.src, c.volume, protected, vols); err == nil {
			t.Errorf("Check(%q) = %q, want refused", c.src, p)
		}
	}
	if _, err := Check(string(filepath.Separator), false, nil, nil); err == nil {
		t.Error("the whole disk was allowed")
	}
}

type fakeEngine struct {
	list    []docker.Container
	details map[string]*docker.ContainerDetail
	root    string
}

func (f *fakeEngine) ListContainers(context.Context, bool) ([]docker.Container, error) {
	return f.list, nil
}
func (f *fakeEngine) InspectContainer(_ context.Context, id string) (*docker.ContainerDetail, error) {
	return f.details[id], nil
}
func (f *fakeEngine) Info(context.Context) (*docker.Info, error) {
	return &docker.Info{RootDir: f.root}, nil
}

func TestAppsAndResolve(t *testing.T) {
	base := mkdir(t, t.TempDir())
	root := mkdir(t, base, "docker")
	vol := mkdir(t, root, "volumes", "media_config", "_data")
	movies := mkdir(t, base, "srv", "movies")
	eng := &fakeEngine{
		root: root,
		list: []docker.Container{
			{ID: "1", Name: "media-server", Image: "media:1", State: "running", Labels: map[string]string{LabelApp: "media"}},
			{ID: "2", Name: "nocap-helper", System: true},
			{ID: "3", Name: "plain"},
		},
		details: map[string]*docker.ContainerDetail{
			"1": {Mounts: []docker.Mount{
				{Type: "volume", Name: "media_config", Source: vol, Destination: "/config", RW: true},
				{Type: "bind", Source: movies, Destination: "/movies"},
				{Type: "bind", Source: base, Destination: "/everything"},
				{Type: "tmpfs", Destination: "/tmp"},
			}},
			"3": {Mounts: []docker.Mount{}},
		},
	}
	m := New(eng, func() []string { return []string{mkdir(t, base, "nocap")} })
	apps, err := m.Apps(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].Name != "media" || len(apps[0].Folders) != 3 {
		t.Fatalf("apps = %+v", apps)
	}
	f := apps[0].Folders
	if f[0].Target != "/config" || f[0].Blocked != "" || f[1].Target != "/everything" || f[1].Blocked == "" || f[2].Blocked != "" {
		t.Fatalf("folders = %+v", f)
	}
	r, ok := m.Resolve(f[0].ID)
	if !ok || r.Path != vol || r.Name != "media · /config" {
		t.Fatalf("Resolve(%q) = %+v, %v", f[0].ID, r, ok)
	}
	if _, ok := m.Resolve(f[1].ID); ok {
		t.Error("a blocked folder resolved")
	}
	for _, id := range []string{"app:media-server:9", "app:nope:0", "home:x", "app:media-server:0/../1"} {
		if _, ok := m.Resolve(id); ok {
			t.Errorf("Resolve(%q) succeeded", id)
		}
	}
}
