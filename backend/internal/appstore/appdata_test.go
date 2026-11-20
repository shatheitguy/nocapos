package appstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"alfaos/alfad/internal/docker"
	"alfaos/alfad/internal/store"
)

func withAppData(t *testing.T, m *Manager) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "appdata")
	m.SetAppData(func(context.Context) string { return root })
	return root
}

// A catalog volume becomes <root>/<app>/<volume>; without a root it stays
// the Docker volume it always was.
func TestCatalogMountMapping(t *testing.T) {
	a := &App{ID: "jellyfin"}
	v := Volume{Name: "config", Path: "/config", ReadOnly: true}
	got := catalogMount(a, v, "/srv/appdata")
	if got != (docker.SpecMount{Type: "bind", Source: "/srv/appdata/jellyfin/config", Target: "/config", ReadOnly: true}) {
		t.Errorf("with App data: %+v", got)
	}
	got = catalogMount(a, v, "")
	if got != (docker.SpecMount{Type: "volume", Source: "nocap-jellyfin-config", Target: "/config", ReadOnly: true}) {
		t.Errorf("without App data: %+v", got)
	}
}

func TestInstallUsesAppDataFolders(t *testing.T) {
	m, eng, st := setup(t)
	root := withAppData(t, m)
	j := started(t)(m.Install(context.Background(), "uptime-kuma", nil))
	if r := wait(t, m, j); r.Error != "" {
		t.Fatal(r.Error)
	}
	if st.apps["uptime-kuma"].Storage != store.StorageFolders {
		t.Fatalf("storage = %q", st.apps["uptime-kuma"].Storage)
	}
	cfg := eng.created["nocap-uptime-kuma-app"]
	want := filepath.ToSlash(filepath.Join(root, "uptime-kuma", "data"))
	if len(cfg.HostConfig.Mounts) != 1 || cfg.HostConfig.Mounts[0].Type != "bind" || cfg.HostConfig.Mounts[0].Source != want {
		t.Fatalf("mounts = %+v, want a folder at %s", cfg.HostConfig.Mounts, want)
	}
	if st, err := os.Stat(filepath.FromSlash(want)); err != nil || !st.IsDir() {
		t.Fatalf("the app's folder should be made before it starts: %v", err)
	}
	tpl, err := m.Template(context.Background(), "uptime-kuma", false)
	if err != nil || tpl.Services[0].Spec.Mounts[0].Source != want {
		t.Fatalf("template = %+v, %v", tpl, err)
	}

	// Uninstall keeps the folder unless asked to delete the data.
	j = started(t)(m.Uninstall(context.Background(), "uptime-kuma", false, nil))
	wait(t, m, j)
	if _, err := os.Stat(filepath.FromSlash(want)); err != nil {
		t.Fatal("uninstall without deleting data must keep the folder")
	}
	j = started(t)(m.Install(context.Background(), "uptime-kuma", nil))
	wait(t, m, j)
	j = started(t)(m.Uninstall(context.Background(), "uptime-kuma", true, nil))
	wait(t, m, j)
	if _, err := os.Stat(filepath.Join(root, "uptime-kuma")); !os.IsNotExist(err) {
		t.Fatal("deleting the data should remove the app's folder")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal("the App data folder itself must stay")
	}
}

// Apps installed before keep their volumes through updates until moved.
func TestOlderAppsKeepVolumesUntilMoved(t *testing.T) {
	m, eng, st := setup(t)
	j := started(t)(m.Install(context.Background(), "uptime-kuma", nil))
	wait(t, m, j)
	if st.apps["uptime-kuma"].Storage != store.StorageVolumes {
		t.Fatalf("no App data: storage = %q", st.apps["uptime-kuma"].Storage)
	}
	root := withAppData(t, m)
	j = started(t)(m.Update(context.Background(), "uptime-kuma", nil))
	wait(t, m, j)
	if mt := eng.created["nocap-uptime-kuma-app"].HostConfig.Mounts[0]; mt.Type != "volume" {
		t.Fatalf("an update must keep the volume: %+v", mt)
	}
	dir := filepath.ToSlash(filepath.Join(root, "uptime-kuma", "data"))
	remap := func(mt docker.SpecMount) docker.SpecMount {
		if mt.Type == "volume" && mt.Source == "nocap-uptime-kuma-data" {
			return docker.SpecMount{Type: "bind", Source: dir, Target: mt.Target}
		}
		return mt
	}
	if err := m.Rebind(context.Background(), "uptime-kuma", remap, true); err != nil {
		t.Fatal(err)
	}
	j = started(t)(m.Update(context.Background(), "uptime-kuma", nil))
	wait(t, m, j)
	if mt := eng.created["nocap-uptime-kuma-app"].HostConfig.Mounts[0]; mt.Type != "bind" || mt.Source != dir {
		t.Fatalf("after the move updates use the folder: %+v", mt)
	}
}

func TestParseUser(t *testing.T) {
	cases := []struct {
		in       string
		uid, gid int
	}{
		{"1000", 1000, 1000},
		{"1000:100", 1000, 100},
		{"node", -1, -1},
		{"", -1, -1},
	}
	for _, c := range cases {
		if u, g := parseUser(c.in, -1); u != c.uid || g != c.gid {
			t.Errorf("parseUser(%q) = %d:%d, want %d:%d", c.in, u, g, c.uid, c.gid)
		}
	}
}
