// Package appdata lists the data folders of every Docker app (its bind
// mounts and named volumes) and opens them in Files as their own locations.
//
// Each folder is checked before it is offered: it must be a real folder,
// never the whole disk, a core OS folder or anything holding NoCapOS's own
// data. Named volumes are allowed inside Docker's own volumes folder only.
// Once opened, every file operation goes through the file service's os.Root,
// so nothing inside can lead outside the folder.
package appdata

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/docker"
	"alfaos/alfad/internal/files"
)

// Prefix starts the id of every app data location ("app:<container>:<n>").
const Prefix = "app:"

// LabelApp is the label the app store puts on its containers (the app's id).
const LabelApp = "nocapos.app"

const cacheFor = 15 * time.Second

// Engine is the part of the Docker client this package uses.
type Engine interface {
	ListContainers(ctx context.Context, all bool) ([]docker.Container, error)
	InspectContainer(ctx context.Context, id string) (*docker.ContainerDetail, error)
	Info(ctx context.Context) (*docker.Info, error)
}

// Folder is one data folder of an app.
type Folder struct {
	ID     string `json:"id"`               // the Files location id
	Target string `json:"target"`           // where the app sees it
	Source string `json:"source"`           // the folder on this server
	Type   string `json:"type"`             // bind | volume
	Volume string `json:"volume,omitempty"` // the volume's name
	RW     bool   `json:"rw"`               // the app may change it
	// Why it can't be opened ("" when it can).
	Blocked string `json:"blocked,omitempty"`
}

// App is a container with its data folders.
type App struct {
	Name      string   `json:"name"` // app id, compose project or container name
	Container string   `json:"container"`
	Image     string   `json:"image"`
	State     string   `json:"state"`
	Folders   []Folder `json:"folders"`
}

// Manager lists the apps (cached briefly) and resolves their locations.
type Manager struct {
	engine    Engine
	protected func() []string

	mu      sync.Mutex
	apps    []App
	at      time.Time
	volumes []string // Docker's volumes folders
}

// New makes a manager. protected returns the folders NoCapOS protects.
func New(engine Engine, protected func() []string) *Manager {
	return &Manager{engine: engine, protected: protected}
}

// Apps lists every app container (not NoCapOS's own) with its data folders.
func (m *Manager) Apps(ctx context.Context) ([]App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.apps != nil && time.Since(m.at) < cacheFor {
		return m.apps, nil
	}
	if m.engine == nil {
		return nil, errors.New("docker is not available")
	}
	list, err := m.engine.ListContainers(ctx, true)
	if err != nil {
		return nil, err
	}
	if m.volumes == nil {
		dir := "/var/lib/docker"
		if info, err := m.engine.Info(ctx); err == nil && info.RootDir != "" {
			dir = info.RootDir
		}
		m.volumes = []string{filepath.Join(dir, "volumes")}
	}
	protected := []string(nil)
	if m.protected != nil {
		protected = m.protected()
	}
	out := []App{}
	for _, c := range list {
		if c.System || c.Name == "" {
			continue
		}
		d, err := m.engine.InspectContainer(ctx, c.ID)
		if err != nil {
			continue
		}
		app := App{Name: appName(c), Container: c.Name, Image: c.Image, State: c.State, Folders: []Folder{}}
		mounts := make([]docker.Mount, 0, len(d.Mounts))
		for _, mt := range d.Mounts {
			if mt.Type == "bind" || mt.Type == "volume" {
				mounts = append(mounts, mt)
			}
		}
		sort.Slice(mounts, func(i, j int) bool { return mounts[i].Destination < mounts[j].Destination })
		for i, mt := range mounts {
			f := Folder{ID: Prefix + c.Name + ":" + strconv.Itoa(i), Target: mt.Destination, Source: mt.Source, Type: mt.Type, RW: mt.RW}
			if mt.Type == "volume" {
				f.Volume = mt.Name
			}
			if real, err := Check(mt.Source, mt.Type == "volume", protected, m.volumes); err != nil {
				f.Blocked = err.Error()
			} else {
				f.Source = real
			}
			app.Folders = append(app.Folders, f)
		}
		if len(app.Folders) > 0 {
			out = append(out, app)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if a != b {
			return a < b
		}
		return out[i].Container < out[j].Container
	})
	m.apps, m.at = out, time.Now()
	return out, nil
}

func appName(c docker.Container) string {
	if v := c.Labels[LabelApp]; v != "" {
		return v
	}
	if c.Project != "" {
		return c.Project
	}
	return c.Name
}

// Find returns the folder with this location id, and its app.
func (m *Manager) Find(ctx context.Context, id string) (*App, *Folder, bool) {
	if !strings.HasPrefix(id, Prefix) {
		return nil, nil, false
	}
	apps, err := m.Apps(ctx)
	if err != nil {
		return nil, nil, false
	}
	for i := range apps {
		for j := range apps[i].Folders {
			if apps[i].Folders[j].ID == id {
				a, f := apps[i], apps[i].Folders[j]
				return &a, &f, true
			}
		}
	}
	return nil, nil, false
}

// Resolve is the file service's lookup for app data locations.
func (m *Manager) Resolve(id string) (*files.Root, bool) {
	if !strings.HasPrefix(id, Prefix) {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, f, ok := m.Find(ctx, id)
	if !ok || f.Blocked != "" {
		return nil, false
	}
	// Check again: the folder may have been swapped for a link since it was listed.
	protected := []string(nil)
	if m.protected != nil {
		protected = m.protected()
	}
	m.mu.Lock()
	volumes := m.volumes
	m.mu.Unlock()
	real, err := Check(f.Source, f.Type == "volume", protected, volumes)
	if err != nil {
		return nil, false
	}
	return &files.Root{ID: id, Name: Label(a.Name, f.Target), Path: real}, true
}

// Label names a location: the app, then the folder it sees ("media · /config").
func Label(app, target string) string { return app + " · " + target }

// Check reports whether a data folder may be opened in Files, returning
// its real path (links followed). volume says it is a named volume, which
// must live inside one of Docker's volumes folders.
func Check(src string, volume bool, protected, volumes []string) (string, error) {
	if src == "" || !filepath.IsAbs(src) {
		return "", errors.New("not a folder on this server")
	}
	p := filepath.Clean(src)
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	} else {
		return "", errors.New("not on this server")
	}
	st, err := os.Stat(p)
	if err != nil {
		return "", errors.New("not on this server")
	}
	if !st.IsDir() {
		return "", errors.New("a single file, not a folder")
	}
	if isTop(p) {
		return "", errors.New("the whole disk")
	}
	inVolumes := false
	for _, v := range volumes {
		if v == "" {
			continue
		}
		vr := filepath.Clean(v)
		if real, err := filepath.EvalSymlinks(vr); err == nil {
			vr = real
		}
		if files.Within(p, vr) && !files.Within(vr, p) {
			inVolumes = true
		}
	}
	if volume && !inVolumes {
		return "", errors.New("outside Docker's volumes folder")
	}
	for _, t := range files.SystemTrees {
		if files.Within(t, p) {
			return "", errors.New("holds core system folders")
		}
		if files.Within(p, t) && !inVolumes {
			return "", errors.New("a core system folder")
		}
	}
	for _, t := range files.SystemPoints {
		if files.Within(t, p) && t != "/" {
			return "", errors.New("holds a top-level system folder")
		}
	}
	for _, d := range protected {
		if files.Within(d, p) {
			return "", errors.New("holds NoCapOS's own files")
		}
		// Inside a protected folder only an app's own folder may be opened
		// (deploys keep their data under stacks/ or apps/); never NoCapOS's
		// database, keys or program files next to them.
		if files.Within(p, d) && !inVolumes && !appOwned(p, d) {
			return "", errors.New("inside NoCapOS's own files")
		}
	}
	return p, nil
}

// appOwned: p is inside d's stacks/ or apps/ folder (not that folder itself).
func appOwned(p, d string) bool {
	for _, sub := range []string{"stacks", "apps"} {
		dir := filepath.Join(d, sub)
		if files.Within(p, dir) && !files.Within(dir, p) {
			return true
		}
	}
	return false
}

// isTop reports whether p is a filesystem's top ("/", "C:\").
func isTop(p string) bool {
	return filepath.Dir(p) == p
}
