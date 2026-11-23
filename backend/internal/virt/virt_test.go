package virt

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetect(t *testing.T) {
	t.Setenv(EnvOverride, "")
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"bare machine", nil, ""},
		{"lxc via systemd", map[string]string{"run/systemd/container": "lxc\n"}, "lxc"},
		{"nspawn via systemd", map[string]string{"run/systemd/container": "systemd-nspawn"}, "systemd-nspawn"},
		{"lxc via pid 1 environment", map[string]string{"proc/1/environ": "PATH=/bin\x00container=lxc\x00TERM=linux"}, "lxc"},
		{"docker is an app container", map[string]string{".dockerenv": "", "run/systemd/container": "docker"}, ""},
		{"podman is an app container", map[string]string{"run/.containerenv": "", "proc/1/environ": "container=podman"}, ""},
		{"docker name without marker", map[string]string{"run/systemd/container": "docker"}, ""},
		{"vm or bare metal environ", map[string]string{"proc/1/environ": "HOME=/\x00TERM=linux"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			for rel, body := range c.files {
				write(t, root, rel, body)
			}
			if got := Detect(root); got != c.want {
				t.Fatalf("Detect = %q, want %q", got, c.want)
			}
		})
	}
}

func TestDetectOverride(t *testing.T) {
	root := t.TempDir()
	write(t, root, "run/systemd/container", "lxc")
	t.Setenv(EnvOverride, "none")
	if got := Detect(root); got != "" {
		t.Fatalf("override none: got %q", got)
	}
	t.Setenv(EnvOverride, "lxc")
	if got := Detect(t.TempDir()); got != "lxc" {
		t.Fatalf("override lxc: got %q", got)
	}
}
