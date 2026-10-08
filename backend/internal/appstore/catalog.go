// Package appstore installs, updates and removes self-hosted apps from a
// curated catalog, as Docker containers with named volumes.
package appstore

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

//go:embed catalog.json
var catalogJSON []byte

type App struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Tagline     string       `json:"tagline"`
	Description string       `json:"description"`
	Category    string       `json:"category"`
	Developer   string       `json:"developer"`
	Website     string       `json:"website"`
	Icon        string       `json:"icon"`
	Tile        [2]string    `json:"tile"`
	Featured    bool         `json:"featured,omitempty"`
	Version     string       `json:"version"`
	Web         *Web         `json:"web,omitempty"`
	FirstRun    string       `json:"first_run,omitempty"`
	Credentials *Credentials `json:"credentials,omitempty"`
	Services    []Service    `json:"services"`
	Releases    []Release    `json:"releases"`
}

// Web is the service and container port the app's web UI listens on.
type Web struct {
	Service string `json:"service"`
	Port    int    `json:"port"`
	Path    string `json:"path,omitempty"`
}

// Credentials shown to the admin after install ({{secret:x}} placeholders allowed).
type Credentials struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type Service struct {
	Name    string            `json:"name"`
	Image   string            `json:"image"`
	Cmd     []string          `json:"cmd,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Ports   []Port            `json:"ports,omitempty"`
	Volumes []Volume          `json:"volumes,omitempty"`
}

// Port publishes a container port. Host 0 = pick a free port (web UI);
// a fixed host port is used as-is (e.g. Syncthing's 22000).
type Port struct {
	Container int    `json:"container"`
	Host      int    `json:"host,omitempty"`
	Protocol  string `json:"protocol,omitempty"` // tcp (default) | udp
}

type Volume struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

type Release struct {
	Version string `json:"version"`
	Date    string `json:"date"`
	Notes   string `json:"notes"`
}

var (
	idRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)
	secretRe = regexp.MustCompile(`\{\{secret:([a-z0-9_]{1,32})\}\}`)
)

// LoadCatalog parses and validates the embedded catalog.
func LoadCatalog() ([]App, error) { return parseCatalog(catalogJSON) }

func parseCatalog(b []byte) ([]App, error) {
	var doc struct {
		Apps []App `json:"apps"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	seen := map[string]bool{}
	for _, a := range doc.Apps {
		if !idRe.MatchString(a.ID) || seen[a.ID] {
			return nil, fmt.Errorf("catalog: bad or duplicate id %q", a.ID)
		}
		seen[a.ID] = true
		if len(a.Services) == 0 {
			return nil, fmt.Errorf("catalog: %s has no services", a.ID)
		}
		names := map[string]bool{}
		for _, s := range a.Services {
			if !idRe.MatchString(s.Name) || names[s.Name] || s.Image == "" {
				return nil, fmt.Errorf("catalog: %s: bad service %q", a.ID, s.Name)
			}
			names[s.Name] = true
			for _, v := range s.Volumes {
				if !idRe.MatchString(v.Name) || !strings.HasPrefix(v.Path, "/") {
					return nil, fmt.Errorf("catalog: %s/%s: bad volume %q", a.ID, s.Name, v.Name)
				}
			}
		}
		if a.Web != nil && !names[a.Web.Service] {
			return nil, fmt.Errorf("catalog: %s: web service %q not defined", a.ID, a.Web.Service)
		}
	}
	return doc.Apps, nil
}

// splitImage turns "ghcr.io/a/b:tag" into ("ghcr.io/a/b", "tag"); no tag = latest.
func splitImage(ref string) (string, string) {
	slash := strings.LastIndex(ref, "/")
	if i := strings.LastIndex(ref, ":"); i > slash {
		return ref[:i], ref[i+1:]
	}
	return ref, "latest"
}

// expand fills {{secret:name}} placeholders, creating secrets on first use.
func expand(s string, secrets map[string]string, gen func() string) string {
	return secretRe.ReplaceAllStringFunc(s, func(m string) string {
		name := secretRe.FindStringSubmatch(m)[1]
		v, ok := secrets[name]
		if !ok {
			v = gen()
			secrets[name] = v
		}
		return v
	})
}
