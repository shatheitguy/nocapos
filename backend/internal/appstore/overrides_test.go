package appstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"alfaos/alfad/internal/docker"
)

func templateSpecs(t *testing.T, m *Manager, id string) map[string]docker.Spec {
	t.Helper()
	tpl, err := m.Template(context.Background(), id, false)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]docker.Spec{}
	for _, s := range tpl.Services {
		out[s.Service] = s.Spec
	}
	return out
}

func TestTemplateMirrorsCatalog(t *testing.T) {
	m, _, _ := setup(t)
	tpl, err := m.Template(context.Background(), "open-webui", false)
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Network != "nocap-open-webui" || len(tpl.Services) != 2 {
		t.Fatalf("template = %+v", tpl)
	}
	web := tpl.Services[0]
	if web.Service != "webui" {
		web = tpl.Services[1]
	}
	if web.WebPort != 8080 || len(web.Spec.Ports) != 1 || web.Spec.Ports[0].Host != 0 || web.Spec.Ports[0].Container != 8080 {
		t.Errorf("web port should be on auto: %+v", web.Spec.Ports)
	}
	if web.Spec.NetworkMode != "nocap-open-webui" || web.Spec.Name != "nocap-open-webui-webui" {
		t.Errorf("network/name = %s / %s", web.Spec.NetworkMode, web.Spec.Name)
	}
	found := false
	for _, k := range web.Secrets {
		found = found || k == "WEBUI_SECRET_KEY"
	}
	if !found {
		t.Errorf("secret variables not flagged: %v", web.Secrets)
	}
	for _, e := range web.Spec.Env {
		if e.Key == "WEBUI_SECRET_KEY" && !strings.Contains(e.Value, "{{secret:") {
			t.Errorf("template must keep the placeholder, got %q", e.Value)
		}
	}
}

func TestInstallWithEdits(t *testing.T) {
	m, eng, st := setup(t)
	specs := templateSpecs(t, m, "code-server")
	s := specs["app"]
	s.Name = "my-code"
	s.Image = "lscr.io/linuxserver/code-server:4.0.0"
	s.Ports = append(s.Ports, docker.PortMap{Host: 9443, Container: 9443, Protocol: "udp"})
	s.Mounts = append(s.Mounts, docker.SpecMount{Type: "bind", Source: "/srv/projects", Target: "/projects", ReadOnly: true})
	env := []docker.EnvVar{{Key: "EXTRA", Value: "1"}}
	for _, e := range s.Env {
		switch e.Key {
		case "PGID": // remove one catalog variable
		case "TZ":
			env = append(env, docker.EnvVar{Key: "TZ", Value: "Asia/Dubai"})
		default:
			env = append(env, e)
		}
	}
	s.Env = env
	s.CapAdd = []string{"cap_net_admin"}
	s.MemoryMB = 256
	specs["app"] = s

	j := started(t)(m.InstallWith(context.Background(), "code-server", specs, nil))
	if r := wait(t, m, j); r.Error != "" {
		t.Fatal(r.Error)
	}
	cfg, ok := eng.created["my-code"]
	if !ok {
		t.Fatalf("container not created under its new name: %v", eng.created)
	}
	if cfg.Image != "lscr.io/linuxserver/code-server:4.0.0" {
		t.Errorf("chosen image not used: %s", cfg.Image)
	}
	row := st.apps["code-server"]
	if b := cfg.HostConfig.PortBindings["8443/tcp"]; len(b) != 1 || b[0].HostPort != "18100" || row.WebPort != 18100 {
		t.Errorf("auto web port: %+v, row %d", cfg.HostConfig.PortBindings, row.WebPort)
	}
	if b := cfg.HostConfig.PortBindings["9443/udp"]; len(b) != 1 {
		t.Errorf("extra port missing: %+v", cfg.HostConfig.PortBindings)
	}
	joined := strings.Join(cfg.Env, " ")
	if !strings.Contains(joined, "TZ=Asia/Dubai") || strings.Contains(joined, "Etc/UTC") || !strings.Contains(joined, "EXTRA=1") || strings.Contains(joined, "PGID") || !strings.Contains(joined, "PASSWORD="+row.Secrets["password"]) || strings.Contains(joined, "{{secret") {
		t.Errorf("env = %v", cfg.Env)
	}
	if len(cfg.HostConfig.CapAdd) != 1 || cfg.HostConfig.CapAdd[0] != "NET_ADMIN" || cfg.HostConfig.Memory != 256<<20 {
		t.Errorf("caps/limits = %+v", cfg.HostConfig)
	}
	if len(cfg.HostConfig.Mounts) != 2 || !cfg.HostConfig.Mounts[1].ReadOnly {
		t.Errorf("mounts = %+v", cfg.HostConfig.Mounts)
	}

	// An update keeps every change, including the pinned image and the removal.
	st.apps["code-server"].Version = "old"
	j = started(t)(m.Update(context.Background(), "code-server", nil))
	if r := wait(t, m, j); r.Error != "" {
		t.Fatal(r.Error)
	}
	after := eng.created["my-code"]
	if after.Image != cfg.Image || strings.Join(after.Env, " ") != joined || after.HostConfig.PortBindings["8443/tcp"][0].HostPort != "18100" {
		t.Errorf("update lost changes: %+v", after)
	}
	if last := eng.pulls[len(eng.pulls)-1]; last != "lscr.io/linuxserver/code-server:4.0.0" {
		t.Errorf("update pulled %s", last)
	}
}

func TestInstallEditValidation(t *testing.T) {
	m, _, st := setup(t)
	bad := func(name string, change func(map[string]docker.Spec)) {
		t.Helper()
		specs := templateSpecs(t, m, "open-webui")
		change(specs)
		_, err := m.InstallWith(context.Background(), "open-webui", specs, nil)
		var ee *EditError
		if !errors.As(err, &ee) {
			t.Errorf("%s: want an EditError, got %v", name, err)
		}
	}
	bad("relative path", func(s map[string]docker.Spec) {
		w := s["webui"]
		w.Mounts = append(w.Mounts, docker.SpecMount{Type: "bind", Source: "data", Target: "/data"})
		s["webui"] = w
	})
	bad("same port twice", func(s map[string]docker.Spec) {
		w, o := s["webui"], s["ollama"]
		w.Ports = append(w.Ports, docker.PortMap{Host: 7000, Container: 1, Protocol: "tcp"})
		o.Ports = append(o.Ports, docker.PortMap{Host: 7000, Container: 2, Protocol: "tcp"})
		s["webui"], s["ollama"] = w, o
	})
	bad("same name", func(s map[string]docker.Spec) {
		o := s["ollama"]
		o.Name = "nocap-open-webui-webui"
		s["ollama"] = o
	})
	bad("unknown service", func(s map[string]docker.Spec) { s["nope"] = s["webui"] })
	bad("bad capability", func(s map[string]docker.Spec) {
		w := s["webui"]
		w.CapAdd = []string{"net admin; rm"}
		s["webui"] = w
	})
	bad("port in use", func(s map[string]docker.Spec) {
		m.portFree = func(p int, _ string) bool { return p != 7100 }
		w := s["webui"]
		w.Ports = append(w.Ports, docker.PortMap{Host: 7100, Container: 1, Protocol: "tcp"})
		s["webui"] = w
	})
	if len(st.apps) != 0 || len(st.overrides) != 0 {
		t.Fatal("a rejected install left something behind")
	}
}

func TestEditKeepsImagePin(t *testing.T) {
	m, _, st := setup(t)
	j := started(t)(m.Install(context.Background(), "code-server", nil))
	wait(t, m, j)
	catalog := m.byID["code-server"].Services[0].Image
	spec := templateSpecs(t, m, "code-server")["app"]
	read := func() Override {
		var ov Override
		_ = json.Unmarshal([]byte(st.overrides["code-server"]["app"]), &ov)
		return ov
	}
	// An env-only edit follows the catalog.
	if err := m.SaveEdit(context.Background(), "code-server", "app", spec, catalog); err != nil {
		t.Fatal(err)
	}
	if read().KeepImage {
		t.Fatal("unchanged image pinned")
	}
	// A new tag is pinned, and stays pinned through later edits.
	spec.Image = "lscr.io/linuxserver/code-server:9"
	_ = m.SaveEdit(context.Background(), "code-server", "app", spec, catalog)
	if !read().KeepImage {
		t.Fatal("new tag not pinned")
	}
	_ = m.SaveEdit(context.Background(), "code-server", "app", spec, spec.Image)
	if !read().KeepImage {
		t.Fatal("pin lost on a later edit")
	}
	// Moving the web UI's port moves "Open" too.
	spec.Ports = []docker.PortMap{{Host: 8999, Container: 8443, Protocol: "tcp"}}
	_ = m.SaveEdit(context.Background(), "code-server", "app", spec, spec.Image)
	if st.apps["code-server"].WebPort != 8999 {
		t.Fatalf("web port = %d", st.apps["code-server"].WebPort)
	}
}

func TestOldOverrideRowsStillRead(t *testing.T) {
	// Rows saved before Override existed are a bare docker.Spec.
	raw, _ := json.Marshal(docker.Spec{Name: "x", Image: "other:1", Env: []docker.EnvVar{{Key: "A", Value: "b"}}})
	var ov Override
	if err := json.Unmarshal(raw, &ov); err != nil || ov.KeepImage || ov.Image != "other:1" || len(ov.Env) != 1 {
		t.Fatalf("ov = %+v, %v", ov, err)
	}
	cfg, _ := applyOverride(docker.CreateConfig{Image: "cat:2", Env: []string{"A=a", "B=c"}}, ov)
	if cfg.Image != "cat:2" || strings.Join(cfg.Env, ",") != "A=b,B=c" {
		t.Fatalf("cfg = %+v", cfg)
	}
}
