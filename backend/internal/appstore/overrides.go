package appstore

import (
	"context"
	"encoding/json"
	"fmt"

	"alfaos/alfad/internal/docker"
)

// Override is the saved changes to one of an app's containers: made before
// installing it, or later from its settings. It is stored as JSON; older
// rows are a bare docker.Spec, which reads the same with the extra fields off.
type Override struct {
	docker.Spec
	// KeepImage: someone picked their own image or tag; updates keep it
	// instead of following the catalog.
	KeepImage bool `json:"keep_image,omitempty"`
	// EnvRemoved are catalog variables they deleted (merging would bring
	// them back otherwise).
	EnvRemoved []string `json:"env_removed,omitempty"`
}

// EditError is a change that can't be used (a bad port, path or name).
type EditError struct{ Msg string }

func (e *EditError) Error() string { return e.Msg }

func editErr(format string, a ...any) error { return &EditError{Msg: fmt.Sprintf(format, a...)} }

// overrides reads an app's saved changes, by service.
func (m *Manager) overrides(ctx context.Context, appID string) (map[string]Override, error) {
	raw, err := m.db.AppOverrides(ctx, appID)
	if err != nil {
		return nil, fmt.Errorf("read saved changes: %w", err)
	}
	out := map[string]Override{}
	for svc, js := range raw {
		var ov Override
		if err := json.Unmarshal([]byte(js), &ov); err != nil {
			return nil, fmt.Errorf("saved changes for %s: %w", svc, err)
		}
		out[svc] = ov
	}
	return out, nil
}

// imageFor is the image a service runs: the catalog's, or one someone chose.
func imageFor(s Service, overrides map[string]Override) string {
	if ov, ok := overrides[s.Name]; ok && ov.KeepImage && ov.Image != "" {
		return ov.Image
	}
	return s.Image
}

// expanded fills {{secret:x}} placeholders left in variables and the command
// (changes made before installing keep the catalog's placeholders).
func (ov Override) expanded(secrets map[string]string, gen func() string) Override {
	out := ov
	out.Env = make([]docker.EnvVar, len(ov.Env))
	for i, e := range ov.Env {
		out.Env[i] = docker.EnvVar{Key: e.Key, Value: expand(e.Value, secrets, gen)}
	}
	out.Cmd = nil
	for _, c := range ov.Cmd {
		out.Cmd = append(out.Cmd, expand(c, secrets, gen))
	}
	out.Ports = append([]docker.PortMap(nil), ov.Ports...)
	return out
}

// saveOverride stores a container's changes. prevImage is the image it had
// before; a new one is kept through updates. With fullEnv, spec lists every
// variable, so catalog ones missing from it were deleted on purpose.
func (m *Manager) saveOverride(ctx context.Context, a *App, service string, spec docker.Spec, prevImage string, fullEnv bool) error {
	var svc *Service
	for i := range a.Services {
		if a.Services[i].Name == service {
			svc = &a.Services[i]
		}
	}
	if svc == nil {
		return editErr("%s has no part called %q", a.Name, service)
	}
	ov := Override{Spec: spec}
	old, _ := m.overrides(ctx, a.ID)
	switch {
	case spec.Image == "" || spec.Image == svc.Image:
		ov.KeepImage = false
	case spec.Image != prevImage:
		ov.KeepImage = true
	default:
		prev, had := old[service]
		ov.KeepImage = had && prev.KeepImage && prev.Image == spec.Image
	}
	have := map[string]bool{}
	for _, e := range spec.Env {
		have[e.Key] = true
	}
	for _, k := range sortedKeys(svc.Env) {
		if fullEnv && !have[k] {
			ov.EnvRemoved = append(ov.EnvRemoved, k)
		}
	}
	raw, err := json.Marshal(ov)
	if err != nil {
		return err
	}
	return m.db.SaveAppOverride(ctx, a.ID, service, string(raw))
}

// checkEdits validates changes made before installing: each container's
// settings, names, and host ports (free, not used twice, not another app's).
func (m *Manager) checkEdits(ctx context.Context, a *App, edits map[string]docker.Spec) error {
	if len(edits) == 0 {
		return nil
	}
	known := map[string]bool{}
	for _, s := range a.Services {
		known[s.Name] = true
	}
	names := map[string]string{}
	type hp struct {
		port  int
		proto string
	}
	ports := map[hp]string{}
	rows, err := m.db.InstalledApps(ctx)
	if err != nil {
		return err
	}
	webPorts := map[int]string{}
	for id, r := range rows {
		if r.WebPort != 0 {
			webPorts[r.WebPort] = id
		}
	}
	for _, s := range a.Services {
		if _, edited := edits[s.Name]; edited {
			continue
		}
		names[containerName(a.ID, s.Name)] = s.Name
		for _, p := range s.Ports {
			if p.Host != 0 {
				proto := p.Protocol
				if proto == "" {
					proto = "tcp"
				}
				ports[hp{p.Host, proto}] = s.Name
			}
		}
	}
	for svc := range edits {
		if !known[svc] {
			return editErr("%s has no part called %q", a.Name, svc)
		}
	}
	for _, s := range a.Services {
		spec, edited := edits[s.Name]
		if !edited {
			continue
		}
		if spec.Name == "" {
			spec.Name = containerName(a.ID, s.Name)
		}
		if err := spec.Validate(); err != nil {
			return editErr("%s: %v", s.Name, err)
		}
		edits[s.Name] = spec
		if other, dup := names[spec.Name]; dup {
			return editErr("%s and %s can't both be called %s", other, s.Name, spec.Name)
		}
		names[spec.Name] = s.Name
		for _, p := range spec.Ports {
			if p.Host == 0 {
				continue
			}
			k := hp{p.Host, p.Protocol}
			if other, dup := ports[k]; dup {
				return editErr("port %d/%s is used by both %s and %s", p.Host, p.Protocol, other, s.Name)
			}
			ports[k] = s.Name
			if owner, taken := webPorts[p.Host]; taken && p.Protocol == "tcp" {
				return editErr("port %d is already %s's web page", p.Host, owner)
			}
			if !m.portFree(p.Host, p.Protocol) {
				return editErr("port %d/%s is already in use on this server", p.Host, p.Protocol)
			}
		}
	}
	return nil
}

// TemplateService is one container of an app, as it will be (or is) set up.
type TemplateService struct {
	Service      string      `json:"service"`
	Spec         docker.Spec `json:"spec"`
	CatalogImage string      `json:"catalog_image"`
	// Secrets are the variables holding generated secrets (shown masked).
	Secrets []string `json:"secrets"`
	// WebPort is the container port of the web UI (its host port 0 = picked
	// automatically on install).
	WebPort int  `json:"web_port,omitempty"`
	Saved   bool `json:"saved"` // has saved changes
}

// Template is an app's containers as a form: the catalog's settings with
// any saved changes on top (defaults = the catalog's alone).
type Template struct {
	Services []TemplateService `json:"services"`
	// Network is the app's own network (multi-container apps), created on install.
	Network string `json:"network,omitempty"`
}

// Template returns the editable settings of an app's containers.
func (m *Manager) Template(ctx context.Context, id string, defaults bool) (*Template, error) {
	a, err := m.App(id)
	if err != nil {
		return nil, err
	}
	saved := map[string]Override{}
	if !defaults {
		if saved, err = m.overrides(ctx, id); err != nil {
			return nil, err
		}
	}
	t := &Template{Services: []TemplateService{}}
	if len(a.Services) > 1 {
		t.Network = netName(id)
	}
	for _, s := range a.Services {
		ts := TemplateService{Service: s.Name, CatalogImage: s.Image, Secrets: []string{}, Spec: baseSpec(a, s)}
		if a.Web != nil && a.Web.Service == s.Name {
			ts.WebPort = a.Web.Port
		}
		for _, k := range sortedKeys(s.Env) {
			if secretRe.MatchString(s.Env[k]) {
				ts.Secrets = append(ts.Secrets, k)
			}
		}
		if ov, ok := saved[s.Name]; ok {
			ts.Saved = true
			spec := ov.Spec
			if !ov.KeepImage || spec.Image == "" {
				spec.Image = s.Image
			}
			if spec.Name == "" {
				spec.Name = containerName(id, s.Name)
			}
			// Catalog variables they kept but never saw (added since) show too.
			have, removed := map[string]bool{}, map[string]bool{}
			for _, e := range spec.Env {
				have[e.Key] = true
			}
			for _, k := range ov.EnvRemoved {
				removed[k] = true
			}
			spec.Env = append([]docker.EnvVar(nil), spec.Env...)
			for _, k := range sortedKeys(s.Env) {
				if !have[k] && !removed[k] {
					spec.Env = append(spec.Env, docker.EnvVar{Key: k, Value: s.Env[k]})
				}
			}
			ts.Spec = normalized(spec)
		}
		t.Services = append(t.Services, ts)
	}
	return t, nil
}

// baseSpec is a catalog service as a docker.Spec (secrets stay placeholders;
// the web UI's host port is 0 = automatic).
func baseSpec(a *App, s Service) docker.Spec {
	spec := docker.Spec{
		Name:        containerName(a.ID, s.Name),
		Image:       s.Image,
		Cmd:         append([]string(nil), s.Cmd...),
		NetworkMode: "bridge",
		Restart:     "unless-stopped",
	}
	for _, k := range sortedKeys(s.Env) {
		spec.Env = append(spec.Env, docker.EnvVar{Key: k, Value: s.Env[k]})
	}
	for _, p := range s.Ports {
		proto := p.Protocol
		if proto == "" {
			proto = "tcp"
		}
		web := a.Web != nil && a.Web.Service == s.Name && a.Web.Port == p.Container
		if p.Host == 0 && !web {
			continue // internal only: reachable by the app's other containers
		}
		spec.Ports = append(spec.Ports, docker.PortMap{Host: p.Host, Container: p.Container, Protocol: proto})
	}
	for _, v := range s.Volumes {
		spec.Mounts = append(spec.Mounts, docker.SpecMount{Type: "volume", Source: volumeName(a.ID, v.Name), Target: v.Path, ReadOnly: v.ReadOnly})
	}
	if len(a.Services) > 1 {
		spec.NetworkMode = netName(a.ID)
		spec.Networks = []docker.NetLink{{Name: netName(a.ID), Aliases: []string{s.Name}}}
	}
	return normalized(spec)
}

// normalized gives every list a value (the UI expects arrays, not null).
func normalized(s docker.Spec) docker.Spec {
	if s.Env == nil {
		s.Env = []docker.EnvVar{}
	}
	if s.Ports == nil {
		s.Ports = []docker.PortMap{}
	}
	if s.Mounts == nil {
		s.Mounts = []docker.SpecMount{}
	}
	if s.Networks == nil {
		s.Networks = []docker.NetLink{}
	}
	if s.Devices == nil {
		s.Devices = []string{}
	}
	if s.CapAdd == nil {
		s.CapAdd = []string{}
	}
	if s.Restart == "" {
		s.Restart = "unless-stopped"
	}
	return s
}
