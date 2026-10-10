package appstore

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/docker"
	"alfaos/alfad/internal/store"
)

const (
	LabelApp     = "nocapos.app"
	LabelService = "nocapos.service"
	portLow      = 18100
	portHigh     = 18999
)

var (
	ErrUnknownApp = errors.New("no such app in the App Store")
	ErrBusy       = errors.New("this app is already being installed, updated or removed")
	ErrInstalled  = errors.New("this app is already installed")
	ErrNotInstall = errors.New("this app is not installed")
)

// Engine is the part of the Docker client the App Store uses (faked in tests).
type Engine interface {
	PullImage(ctx context.Context, image, tag string, onProgress func(docker.PullProgress)) error
	CreateNetwork(ctx context.Context, name string, labels map[string]string) error
	RemoveNetwork(ctx context.Context, name string) error
	RemoveVolume(ctx context.Context, name string) error
	CreateContainer(ctx context.Context, name string, cfg docker.CreateConfig) (string, error)
	StartContainer(ctx context.Context, id string) error
	StopContainer(ctx context.Context, id string, timeout time.Duration) error
	RestartContainer(ctx context.Context, id string, timeout time.Duration) error
	RemoveContainer(ctx context.Context, id string) error
	ContainersByLabel(ctx context.Context, label, value string) ([]docker.Container, error)
	ConnectNetwork(ctx context.Context, n docker.NetLink, container string) error
}

// Store is the part of the database the App Store uses.
type Store interface {
	InstalledApps(ctx context.Context) (map[string]*store.InstalledApp, error)
	InstalledApp(ctx context.Context, id string) (*store.InstalledApp, error)
	SaveInstalledApp(ctx context.Context, a *store.InstalledApp) error
	DeleteInstalledApp(ctx context.Context, id string) error
	AppOverrides(ctx context.Context, appID string) (map[string]string, error)
	SaveAppOverride(ctx context.Context, appID, service, spec string) error
	DeleteAppOverrides(ctx context.Context, appID string) error
}

type Manager struct {
	apps   []App
	byID   map[string]*App
	engine Engine
	db     Store
	// portFree reports whether a host port can be bound (swappable in tests).
	portFree func(port int, proto string) bool

	mu       sync.Mutex
	jobs     map[string]*job
	active   map[string]string // app id → running job id
	last     map[string]string // app id → most recent job id (shown for a while after it ends)
	reserved map[int]string    // web ports picked by installs that haven't saved yet
}

func NewManager(apps []App, engine Engine, db Store) *Manager {
	m := &Manager{apps: apps, byID: map[string]*App{}, engine: engine, db: db, portFree: hostPortFree,
		jobs: map[string]*job{}, active: map[string]string{}, last: map[string]string{}, reserved: map[int]string{}}
	for i := range apps {
		m.byID[apps[i].ID] = &apps[i]
	}
	return m
}

func (m *Manager) Catalog() []App { return m.apps }

func (m *Manager) App(id string) (*App, error) {
	if a, ok := m.byID[id]; ok {
		return a, nil
	}
	return nil, ErrUnknownApp
}

// ---------- status ----------

type ContainerState struct {
	Service string `json:"service"`
	ID      string `json:"id"`
	State   string `json:"state"`
}

// Installed describes an installed app for the UI.
type Installed struct {
	Version         string           `json:"version"`
	WebPort         int              `json:"web_port"`
	Status          string           `json:"status"` // running | stopped | partial | missing
	UpdateAvailable bool             `json:"update_available"`
	Containers      []ContainerState `json:"containers"`
	Credentials     *Credentials     `json:"credentials,omitempty"`
	InstalledAt     time.Time        `json:"installed_at"`
}

// Status returns every installed app's state, keyed by app id.
func (m *Manager) Status(ctx context.Context) (map[string]*Installed, error) {
	rows, err := m.db.InstalledApps(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]*Installed{}
	for id, row := range rows {
		in := &Installed{Version: row.Version, WebPort: row.WebPort, InstalledAt: row.InstalledAt, Status: "missing"}
		if a, ok := m.byID[id]; ok {
			in.UpdateAvailable = a.Version != row.Version
			if a.Credentials != nil {
				in.Credentials = &Credentials{
					Username: expand(a.Credentials.Username, row.Secrets, func() string { return "" }),
					Password: expand(a.Credentials.Password, row.Secrets, func() string { return "" }),
				}
			}
		}
		if cs, err := m.engine.ContainersByLabel(ctx, LabelApp, id); err == nil {
			running := 0
			for _, c := range cs {
				in.Containers = append(in.Containers, ContainerState{Service: c.Labels[LabelService], ID: c.ID, State: c.State})
				if c.State == "running" {
					running++
				}
			}
			sort.Slice(in.Containers, func(i, j int) bool { return in.Containers[i].Service < in.Containers[j].Service })
			switch {
			case len(cs) == 0:
				in.Status = "missing"
			case running == len(cs):
				in.Status = "running"
			case running == 0:
				in.Status = "stopped"
			default:
				in.Status = "partial"
			}
		}
		out[id] = in
	}
	return out, nil
}

// recentJobWindow is how long a finished job (and its error) stays visible.
const recentJobWindow = 5 * time.Minute

// Jobs returns each app's running job, or its last job if it ended recently.
func (m *Manager) Jobs() map[string]Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]Job{}
	for app, jid := range m.last {
		j := m.jobs[jid]
		if j == nil {
			continue
		}
		snap := j.snapshot()
		if snap.Done && time.Since(snap.Ended) > recentJobWindow {
			continue
		}
		out[app] = snap
	}
	return out
}

// Busy reports whether an install, update or uninstall of the app is running.
func (m *Manager) Busy(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.active[id]
	return ok
}

// ---------- jobs ----------

// Job is the progress of an install, update or uninstall.
type Job struct {
	ID      string    `json:"id"`
	App     string    `json:"app"`
	Action  string    `json:"action"` // install | update | uninstall
	Phase   string    `json:"phase"`
	Percent int       `json:"percent"`
	Done    bool      `json:"done"`
	Error   string    `json:"error,omitempty"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended,omitempty"`
}

// job is a running Job guarded by a mutex.
type job struct {
	mu sync.Mutex
	Job
}

func (j *job) set(phase string, pct int) {
	j.mu.Lock()
	j.Phase, j.Percent = phase, pct
	j.mu.Unlock()
}

func (j *job) snapshot() Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.Job
}

func (m *Manager) Job(id string) (Job, bool) {
	m.mu.Lock()
	j := m.jobs[id]
	m.mu.Unlock()
	if j == nil {
		return Job{}, false
	}
	return j.snapshot(), true
}

// start runs fn as a background job for app, one job per app at a time.
func (m *Manager) start(app, action string, fn func(ctx context.Context, j *job) error, onDone func(error)) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, busy := m.active[app]; busy {
		return "", ErrBusy
	}
	j := &job{Job: Job{ID: randomString(16), App: app, Action: action, Phase: "Starting…", Started: time.Now().UTC()}}
	m.jobs[j.ID] = j
	m.active[app] = j.ID
	m.last[app] = j.ID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		err := fn(ctx, j)
		j.mu.Lock()
		j.Done, j.Ended = true, time.Now().UTC()
		if err != nil {
			j.Error = err.Error()
		} else {
			j.Percent, j.Phase = 100, "Done"
		}
		j.mu.Unlock()
		m.mu.Lock()
		delete(m.active, app)
		for p, owner := range m.reserved {
			if owner == app {
				delete(m.reserved, p)
			}
		}
		m.mu.Unlock()
		if onDone != nil {
			onDone(err)
		}
	}()
	return j.ID, nil
}

// ---------- install / update / uninstall ----------

// Install pulls the app's images and starts its containers in the background.
func (m *Manager) Install(ctx context.Context, id string, onDone func(error)) (string, error) {
	a, err := m.App(id)
	if err != nil {
		return "", err
	}
	if _, err := m.db.InstalledApp(ctx, id); err == nil {
		return "", ErrInstalled
	}
	return m.start(id, "install", func(ctx context.Context, j *job) error {
		row := &store.InstalledApp{ID: id, Version: a.Version, Secrets: map[string]string{}}
		if a.Web != nil {
			port, err := m.freeWebPort(ctx, id)
			if err != nil {
				return err
			}
			row.WebPort = port
		}
		if err := m.checkFixedPorts(a); err != nil {
			return err
		}
		if err := m.pullAll(ctx, a, j, 0, 80); err != nil {
			return err
		}
		if err := m.createAll(ctx, a, row, j); err != nil {
			m.removeContainers(context.Background(), id)
			_ = m.engine.RemoveNetwork(context.Background(), netName(id))
			m.removeVolumes(context.Background(), a)
			return err
		}
		return m.db.SaveInstalledApp(ctx, row)
	}, onDone)
}

// Update pulls the catalog's images and recreates the containers, keeping
// the app's data volumes, port and secrets.
func (m *Manager) Update(ctx context.Context, id string, onDone func(error)) (string, error) {
	a, err := m.App(id)
	if err != nil {
		return "", err
	}
	row, err := m.db.InstalledApp(ctx, id)
	if err != nil {
		return "", ErrNotInstall
	}
	return m.start(id, "update", func(ctx context.Context, j *job) error {
		if err := m.pullAll(ctx, a, j, 0, 75); err != nil {
			return err
		}
		j.set("Replacing containers…", 80)
		m.removeContainers(ctx, id)
		row.Version = a.Version
		if err := m.createAll(ctx, a, row, j); err != nil {
			return err
		}
		return m.db.SaveInstalledApp(ctx, row)
	}, onDone)
}

// Uninstall removes the app's containers and network; its data volumes only
// when deleteData is set.
func (m *Manager) Uninstall(ctx context.Context, id string, deleteData bool, onDone func(error)) (string, error) {
	a, err := m.App(id)
	if err != nil {
		return "", err
	}
	if _, err := m.db.InstalledApp(ctx, id); err != nil {
		return "", ErrNotInstall
	}
	return m.start(id, "uninstall", func(ctx context.Context, j *job) error {
		j.set("Stopping and removing containers…", 20)
		m.removeContainers(ctx, id)
		_ = m.engine.RemoveNetwork(ctx, netName(id))
		if deleteData {
			j.set("Deleting app data…", 70)
			m.removeVolumes(ctx, a)
			// Saved changes go with the data (they may point at its folders).
			_ = m.db.DeleteAppOverrides(ctx, id)
		}
		return m.db.DeleteInstalledApp(ctx, id)
	}, onDone)
}

// Control starts, stops or restarts all of an app's containers.
func (m *Manager) Control(ctx context.Context, id, action string) error {
	if _, err := m.db.InstalledApp(ctx, id); err != nil {
		return ErrNotInstall
	}
	cs, err := m.engine.ContainersByLabel(ctx, LabelApp, id)
	if err != nil {
		return err
	}
	a, _ := m.App(id)
	sort.Slice(cs, func(i, j int) bool { return serviceIndex(a, cs[i]) < serviceIndex(a, cs[j]) })
	for _, c := range cs {
		switch action {
		case "start":
			err = m.engine.StartContainer(ctx, c.ID)
		case "stop":
			err = m.engine.StopContainer(ctx, c.ID, 20*time.Second)
		case "restart":
			err = m.engine.RestartContainer(ctx, c.ID, 20*time.Second)
		default:
			return fmt.Errorf("unknown action %q", action)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// ---------- helpers ----------

func netName(app string) string            { return "nocap-" + app }
func containerName(app, svc string) string { return "nocap-" + app + "-" + svc }
func volumeName(app, vol string) string    { return "nocap-" + app + "-" + vol }

func serviceIndex(a *App, c docker.Container) int {
	if a != nil {
		for i, s := range a.Services {
			if s.Name == c.Labels[LabelService] {
				return i
			}
		}
	}
	return 99
}

func (m *Manager) pullAll(ctx context.Context, a *App, j *job, from, to int) error {
	n := len(a.Services)
	for i, s := range a.Services {
		image, tag := splitImage(s.Image)
		base := from + (to-from)*i/n
		span := (to - from) / n
		layers, done := map[string]bool{}, map[string]bool{}
		j.set(fmt.Sprintf("Downloading %s…", s.Image), base)
		err := m.engine.PullImage(ctx, image, tag, func(p docker.PullProgress) {
			if p.ID == "" || strings.HasPrefix(p.Status, "Pulling from") {
				return
			}
			layers[p.ID] = true
			if p.Status == "Pull complete" || p.Status == "Already exists" {
				done[p.ID] = true
			}
			pct := base
			if len(layers) > 0 {
				pct = base + span*len(done)/len(layers)
			}
			j.set(fmt.Sprintf("Downloading %s… %d of %d layers", s.Image, len(done), len(layers)), pct)
		})
		if err != nil {
			return fmt.Errorf("download %s: %w", s.Image, err)
		}
	}
	return nil
}

func (m *Manager) createAll(ctx context.Context, a *App, row *store.InstalledApp, j *job) error {
	multi := len(a.Services) > 1
	if multi {
		if err := m.engine.CreateNetwork(ctx, netName(a.ID), map[string]string{LabelApp: a.ID}); err != nil {
			return fmt.Errorf("create network: %w", err)
		}
	}
	gen := func() string { return randomString(24) }
	overrides, err := m.db.AppOverrides(ctx, a.ID)
	if err != nil {
		return fmt.Errorf("read saved changes: %w", err)
	}
	for i, s := range a.Services {
		j.set(fmt.Sprintf("Starting %s…", s.Name), 85+10*i/len(a.Services))
		cfg := docker.CreateConfig{
			Image:  s.Image,
			Labels: map[string]string{LabelApp: a.ID, LabelService: s.Name, "nocapos.version": a.Version},
			HostConfig: docker.HostConfig{
				RestartPolicy: &docker.RestartPolicy{Name: "unless-stopped"},
				PortBindings:  map[string][]docker.PortBinding{},
			},
			ExposedPorts: map[string]struct{}{},
		}
		for _, c := range s.Cmd {
			cfg.Cmd = append(cfg.Cmd, expand(c, row.Secrets, gen))
		}
		keys := make([]string, 0, len(s.Env))
		for k := range s.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			cfg.Env = append(cfg.Env, k+"="+expand(s.Env[k], row.Secrets, gen))
		}
		for _, p := range s.Ports {
			proto := p.Protocol
			if proto == "" {
				proto = "tcp"
			}
			host := p.Host
			if host == 0 && a.Web != nil && a.Web.Service == s.Name && a.Web.Port == p.Container {
				host = row.WebPort
			}
			if host == 0 {
				continue // internal only
			}
			key := strconv.Itoa(p.Container) + "/" + proto
			cfg.ExposedPorts[key] = struct{}{}
			cfg.HostConfig.PortBindings[key] = append(cfg.HostConfig.PortBindings[key], docker.PortBinding{HostPort: strconv.Itoa(host)})
		}
		for _, v := range s.Volumes {
			cfg.HostConfig.Mounts = append(cfg.HostConfig.Mounts, docker.MountSpec{Type: "volume", Source: volumeName(a.ID, v.Name), Target: v.Path, ReadOnly: v.ReadOnly})
		}
		if multi {
			cfg.HostConfig.NetworkMode = netName(a.ID)
			cfg.NetworkingConfig = &docker.NetworkingConfig{EndpointsConfig: map[string]docker.EndpointConfig{netName(a.ID): {Aliases: []string{s.Name}}}}
		}
		var extra []docker.NetLink
		if raw, ok := overrides[s.Name]; ok {
			var ov docker.Spec
			if err := json.Unmarshal([]byte(raw), &ov); err != nil {
				return fmt.Errorf("saved changes for %s: %w", s.Name, err)
			}
			cfg, extra = applyOverride(cfg, ov)
		}
		id, err := m.engine.CreateContainer(ctx, containerName(a.ID, s.Name), cfg)
		if err != nil {
			return fmt.Errorf("create %s: %w", s.Name, err)
		}
		for _, n := range extra {
			if err := m.engine.ConnectNetwork(ctx, n, id); err != nil {
				return fmt.Errorf("join network %s: %w", n.Name, err)
			}
		}
		if err := m.engine.StartContainer(ctx, id); err != nil {
			return fmt.Errorf("start %s: %w", s.Name, err)
		}
	}
	return nil
}

// applyOverride uses the changes someone saved for a container: their ports,
// storage, networks, restart policy and limits win; the image and labels
// always come from the catalog (so updates still update), and environment
// variables are merged, theirs winning.
func applyOverride(catalog docker.CreateConfig, ov docker.Spec) (docker.CreateConfig, []docker.NetLink) {
	ov.Image = catalog.Image
	cfg := ov.CreateConfig(catalog.Labels)
	if len(ov.Cmd) == 0 {
		cfg.Cmd = catalog.Cmd
	}
	idx := map[string]int{}
	env := append([]string(nil), catalog.Env...)
	for i, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		idx[k] = i
	}
	for _, e := range ov.Env {
		if i, ok := idx[e.Key]; ok {
			env[i] = e.Key + "=" + e.Value
		} else {
			idx[e.Key] = len(env)
			env = append(env, e.Key+"="+e.Value)
		}
	}
	cfg.Env = env
	return cfg, ov.ExtraNetworks()
}

// SaveOverride remembers the changes made to one of an app's containers, so
// they are applied again when the app is updated.
func (m *Manager) SaveOverride(ctx context.Context, appID, service string, spec docker.Spec) error {
	if _, err := m.App(appID); err != nil {
		return err
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	return m.db.SaveAppOverride(ctx, appID, service, string(raw))
}

func (m *Manager) removeContainers(ctx context.Context, app string) {
	cs, err := m.engine.ContainersByLabel(ctx, LabelApp, app)
	if err != nil {
		return
	}
	for _, c := range cs {
		_ = m.engine.RemoveContainer(ctx, c.ID)
	}
}

func (m *Manager) removeVolumes(ctx context.Context, a *App) {
	for _, s := range a.Services {
		for _, v := range s.Volumes {
			_ = m.engine.RemoveVolume(ctx, volumeName(a.ID, v.Name))
		}
	}
}

// freeWebPort picks a host port in [portLow, portHigh] that no installed app
// uses and nothing is listening on.
func (m *Manager) freeWebPort(ctx context.Context, app string) (int, error) {
	rows, err := m.db.InstalledApps(ctx)
	if err != nil {
		return 0, err
	}
	used := map[int]bool{}
	for id, r := range rows {
		if id != app {
			used[r.WebPort] = true
		}
	}
	// Reserve under the lock so simultaneous installs never pick the same port.
	m.mu.Lock()
	defer m.mu.Unlock()
	for p := portLow; p <= portHigh; p++ {
		if owner, taken := m.reserved[p]; taken && owner != app {
			continue
		}
		if !used[p] && m.portFree(p, "tcp") {
			m.reserved[p] = app
			return p, nil
		}
	}
	return 0, errors.New("no free port for the app's web page")
}

func (m *Manager) checkFixedPorts(a *App) error {
	for _, s := range a.Services {
		for _, p := range s.Ports {
			if p.Host == 0 {
				continue
			}
			proto := p.Protocol
			if proto == "" {
				proto = "tcp"
			}
			if !m.portFree(p.Host, proto) {
				return fmt.Errorf("port %d/%s is already in use on this machine", p.Host, proto)
			}
		}
	}
	return nil
}

func hostPortFree(port int, proto string) bool {
	addr := ":" + strconv.Itoa(port)
	if proto == "udp" {
		c, err := net.ListenPacket("udp", addr)
		if err != nil {
			return false
		}
		c.Close()
		return true
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	l.Close()
	return true
}

const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func randomString(n int) string {
	b := make([]byte, n)
	for i := range b {
		k, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		b[i] = alphabet[k.Int64()]
	}
	return string(b)
}
