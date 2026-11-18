package appdata

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSafeName(t *testing.T) {
	cases := map[string]string{
		"jellyfin":              "jellyfin",
		"My App":                "my-app",
		"../../etc":             "etc",
		"..":                    "app",
		"":                      "app",
		"Nextcloud_AIO.1":       "nextcloud_aio.1",
		"a/b\\c":                "a-b-c",
		strings.Repeat("x", 80): strings.Repeat("x", 64),
	}
	for in, want := range cases {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPaths(t *testing.T) {
	if got := filepath.ToSlash(VolumeDir("/srv/appdata", "jellyfin", "config")); got != "/srv/appdata/jellyfin/config" {
		t.Errorf("VolumeDir = %s", got)
	}
	if got := filepath.ToSlash(VolumeDir("/srv/appdata", "../x", "../../y")); got != "/srv/appdata/x/y" {
		t.Errorf("names must not climb out: %s", got)
	}
	if got := Suggest("/srv/appdata", "Home Assistant", ""); got != "/srv/appdata/home-assistant/data" {
		t.Errorf("Suggest = %s", got)
	}
	if !Under("/srv/appdata/a", "/srv/appdata") || Under("/srv/appdata", "/srv/appdata") || Under("/srv/appdata2/a", "/srv/appdata") {
		t.Error("Under is wrong")
	}
	if runtime.GOOS == "linux" && DefaultRootFor("", "/var/lib/nocapos") != DefaultRoot {
		t.Error("Linux default should be /srv/appdata")
	}
	if DefaultRootFor("/data/appdata", "/x") != filepath.Clean("/data/appdata") {
		t.Error("the image OS's folder should win")
	}
}

func TestValidateRootAndBinds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("system folders are Linux paths")
	}
	data := "/var/lib/nocapos"
	protected := []string{data}
	docker := "/var/lib/docker"
	okRoots := []string{"/srv/appdata", "/mnt/fast/appdata", "/data/appdata"}
	for _, p := range okRoots {
		if err := ValidateRoot(p, protected, docker); err != nil {
			t.Errorf("ValidateRoot(%s) = %v", p, err)
		}
	}
	badRoots := []string{"", "relative", "/", "/srv", "/etc/appdata", "/usr", "/var/lib/docker/volumes", data, data + "/db", "/var/lib"}
	for _, p := range badRoots {
		if err := ValidateRoot(p, protected, docker); err == nil {
			t.Errorf("ValidateRoot(%q) should fail", p)
		}
	}
	okBinds := []string{"/srv/appdata/x/config", "/srv/media", "/etc/localtime", "/var/run/docker.sock", "/mnt/pool/films"}
	for _, p := range okBinds {
		if err := CheckBind(p, protected, docker); err != nil {
			t.Errorf("CheckBind(%s) = %v", p, err)
		}
	}
	badBinds := []string{"/", "/etc", "/etc/shadow", "/home", "/usr/bin", "/proc", data, data + "/secret.key", "/var/lib", "/boot"}
	for _, p := range badBinds {
		if err := CheckBind(p, protected, docker); err == nil {
			t.Errorf("CheckBind(%q) should fail", p)
		}
	}
}

func TestEnsureDirNeverWorldWritable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "appdata")
	dir := filepath.Join(root, "app", "config")
	if err := EnsureDir(root, dir, -1, -1); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{root, filepath.Dir(dir), dir} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && st.Mode().Perm()&0o002 != 0 {
			t.Errorf("%s is world-writable: %v", p, st.Mode())
		}
	}
	if err := EnsureDir(root, filepath.Join(root, "..", "elsewhere"), -1, -1); err == nil {
		t.Error("folders outside App data must be refused")
	}
}

func TestRewriteComposeVolumes(t *testing.T) {
	in := `services:
  db:
    image: postgres:16
    volumes:
      # the database
      - db:/var/lib/postgresql/data
      - ./init:/docker-entrypoint-initdb.d:ro
  web:
    image: nginx
    volumes:
      - type: volume
        source: cache
        target: /cache
        volume:
          nocopy: true
      - type: volume
        source: keep
        target: /keep
volumes:
  db:
  cache:
  keep:
    external: true
`
	out, err := RewriteComposeVolumes(in, map[string]string{"db": "/srv/appdata/notes/db", "cache": "/srv/appdata/notes/cache"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"- /srv/appdata/notes/db:/var/lib/postgresql/data",
		"# the database",
		"- ./init:/docker-entrypoint-initdb.d:ro",
		"type: bind",
		"source: /srv/appdata/notes/cache",
		"source: keep",
		"keep:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rewritten file lacks %q:\n%s", want, out)
		}
	}
	if top := out[strings.LastIndex(out, "\nvolumes:"):]; strings.Contains(out, "nocopy") || strings.Contains(top, "db:") || strings.Contains(top, "cache:") {
		t.Errorf("moved volumes should leave the volumes list:\n%s", out)
	}
	binds, err := ComposeBinds(out)
	if err != nil || len(binds) != 2 {
		t.Errorf("ComposeBinds = %v, %v", binds, err)
	}
	if got := ReplaceRoot(out, "/srv/appdata", "/mnt/ssd/appdata"); !strings.Contains(got, "/mnt/ssd/appdata/notes/db:") {
		t.Errorf("ReplaceRoot:\n%s", got)
	}
}
