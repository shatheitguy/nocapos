package docker

import (
	"archive/tar"
	"strings"
	"testing"
)

func TestCleanPath(t *testing.T) {
	good := map[string]string{"/": "/", "/etc": "/etc", "/etc/": "/etc", "/var//log/./x": "/var/log/x", "/a b/ü.txt": "/a b/ü.txt"}
	for in, want := range good {
		if got, err := CleanPath(in); err != nil || got != want {
			t.Errorf("CleanPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "etc", "./etc", "/etc/../root", "/..", "../x", "/a\x00b", "/a\nb", "/a\x7fb", "/" + strings.Repeat("a", 5000)} {
		if got, err := CleanPath(in); err == nil {
			t.Errorf("CleanPath(%q) = %q; want an error", in, got)
		}
	}
}

func TestCleanNameAndUploads(t *testing.T) {
	for _, n := range []string{"notes.txt", "a b.conf", ".env", "ü"} {
		if _, err := CleanName(n); err != nil {
			t.Errorf("CleanName(%q): %v", n, err)
		}
	}
	for _, n := range []string{"", ".", "..", "a/b", `a\b`, "x\x00", "x\ny", strings.Repeat("a", 256)} {
		if _, err := CleanName(n); err == nil {
			t.Errorf("CleanName(%q) should fail", n)
		}
	}
	for dir, ok := range map[string]bool{"/": true, "/app": true, "/devices": true, "/proc": false, "/proc/1": false, "/sys/kernel": false, "/dev": false, "/dev/shm": false} {
		if UploadAllowed(dir) != ok {
			t.Errorf("UploadAllowed(%q) = %v", dir, !ok)
		}
	}
}

func TestChildEntry(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"./", ""}, {"etc/", ""}, {"./hosts", "hosts"}, {"etc/hosts", "hosts"}, {"./ssl/", "ssl"}, {"./ssl/certs/x", ""}, {"etc/ssl/certs", ""},
	}
	for _, c := range cases {
		e, ok := childEntry("/etc", &tar.Header{Name: c.name, Typeflag: tar.TypeReg, Mode: 0o644})
		if (c.want == "") == ok || (ok && (e.Name != c.want || e.Path != "/etc/"+c.want)) {
			t.Errorf("childEntry(%q) = %+v, %v; want %q", c.name, e, ok, c.want)
		}
	}
	// The root folder's archive has entries without a prefix.
	if e, ok := childEntry("/", &tar.Header{Name: "./bin", Typeflag: tar.TypeDir, Mode: 0o755}); !ok || e.Path != "/bin" {
		t.Errorf("root: %+v %v", e, ok)
	}
}

func TestMaskEnv(t *testing.T) {
	got := MaskEnv([]string{"TZ=Europe/London", "DB_PASSWORD=hunter2", "API_KEY=abc", "DATABASE_URL=postgres://app:s3cret@db/app",
		"HOME_URL=https://example.com/x", "GITHUB_TOKEN=t", "PUID=1000", "=bad", "EMPTY="})
	want := map[string]string{"TZ": "Europe/London", "HOME_URL": "https://example.com/x", "PUID": "1000", "EMPTY": ""}
	secret := map[string]bool{"DB_PASSWORD": true, "API_KEY": true, "DATABASE_URL": true, "GITHUB_TOKEN": true}
	if len(got) != 8 {
		t.Fatalf("entries: %+v", got)
	}
	for _, e := range got {
		if secret[e.Key] {
			if !e.Secret || e.Value != "" {
				t.Errorf("%s must be masked: %+v", e.Key, e)
			}
		} else if e.Secret || e.Value != want[e.Key] {
			t.Errorf("%s shown as is: %+v", e.Key, e)
		}
	}
	if got[0].Key > got[len(got)-1].Key {
		t.Error("sorted by name")
	}
}

func TestValidImageRef(t *testing.T) {
	for _, r := range []string{"nginx", "nginx:1.27", "ghcr.io/owner/app:v1", "localhost:5000/x", "sha256:abc123", "app@sha256:0123abcd"} {
		if !ValidImageRef(r) {
			t.Errorf("%q should be valid", r)
		}
	}
	for _, r := range []string{"", "../containers/x", "a/../../b", "x?y", "a b", "x/", "a//b", "-rm", "x#y"} {
		if ValidImageRef(r) {
			t.Errorf("%q should be refused", r)
		}
	}
}

func TestSpecLimits(t *testing.T) {
	s := Spec{Name: "web", Image: "nginx", PidsLimit: 4}
	if err := s.Validate(); err == nil {
		t.Error("a process limit under 8 is refused")
	}
	s = Spec{Name: "web", Image: "nginx", PidsLimit: 256, ReadOnly: true, MemoryMB: 512, CPUs: 1.5}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg := s.CreateConfig(nil)
	if cfg.HostConfig.PidsLimit != 256 || !cfg.HostConfig.ReadOnly {
		t.Errorf("limits reach the engine: %+v", cfg.HostConfig)
	}
	body := createBody(&rawContainer{Config: map[string]any{}, HostConfig: map[string]any{}}, s)
	hc := body["HostConfig"].(map[string]any)
	if hc["PidsLimit"] != int64(256) || hc["ReadonlyRootfs"] != true {
		t.Errorf("recreate keeps limits: %v %v", hc["PidsLimit"], hc["ReadonlyRootfs"])
	}
	back := specFrom(&rawContainer{Config: map[string]any{"Image": "nginx"}, HostConfig: map[string]any{"PidsLimit": float64(256), "ReadonlyRootfs": true}})
	if back.PidsLimit != 256 || !back.ReadOnly {
		t.Errorf("read back: %+v", back)
	}
}
