package appstore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"alfaos/alfad/internal/docker"
	"alfaos/alfad/internal/store"
)

// fakeEngine records what the manager asks Docker to do.
type fakeEngine struct {
	mu         sync.Mutex
	pulls      []string
	created    map[string]docker.CreateConfig // name → config
	containers map[string]docker.Container    // id → container
	networks   map[string]bool
	volumes    map[string]bool // volumes "in use" (created by mounts)
	removedVol []string
	pullErr    error
}

func newFake() *fakeEngine {
	return &fakeEngine{created: map[string]docker.CreateConfig{}, containers: map[string]docker.Container{}, networks: map[string]bool{}, volumes: map[string]bool{}}
}

func (f *fakeEngine) PullImage(_ context.Context, image, tag string, fn func(docker.PullProgress)) error {
	f.mu.Lock()
	f.pulls = append(f.pulls, image+":"+tag)
	f.mu.Unlock()
	fn(docker.PullProgress{ID: "l1", Status: "Downloading"})
	fn(docker.PullProgress{ID: "l1", Status: "Pull complete"})
	return f.pullErr
}
func (f *fakeEngine) CreateNetwork(_ context.Context, n string, _ map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.networks[n] = true
	return nil
}
func (f *fakeEngine) RemoveNetwork(_ context.Context, n string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.networks, n)
	return nil
}
func (f *fakeEngine) RemoveVolume(_ context.Context, n string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removedVol = append(f.removedVol, n)
	delete(f.volumes, n)
	return nil
}
func (f *fakeEngine) CreateContainer(_ context.Context, name string, cfg docker.CreateConfig) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, dup := f.created[name]; dup {
		return "", errors.New("name in use")
	}
	f.created[name] = cfg
	id := "id-" + name
	f.containers[id] = docker.Container{ID: id, Name: name, State: "created", Labels: cfg.Labels}
	for _, m := range cfg.HostConfig.Mounts {
		f.volumes[m.Source] = true
	}
	return id, nil
}
func (f *fakeEngine) setState(id, st string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.containers[id]
	c.State = st
	f.containers[id] = c
	return nil
}
func (f *fakeEngine) StartContainer(_ context.Context, id string) error {
	return f.setState(id, "running")
}
func (f *fakeEngine) StopContainer(_ context.Context, id string, _ time.Duration) error {
	return f.setState(id, "exited")
}
func (f *fakeEngine) RestartContainer(_ context.Context, id string, _ time.Duration) error {
	return f.setState(id, "running")
}
func (f *fakeEngine) RemoveContainer(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.containers[id]
	delete(f.containers, id)
	delete(f.created, c.Name)
	return nil
}
func (f *fakeEngine) ContainersByLabel(_ context.Context, label, value string) ([]docker.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []docker.Container
	for _, c := range f.containers {
		if c.Labels[label] == value {
			out = append(out, c)
		}
	}
	return out, nil
}

type fakeStore struct {
	mu   sync.Mutex
	apps map[string]*store.InstalledApp
}

func (s *fakeStore) InstalledApps(context.Context) (map[string]*store.InstalledApp, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]*store.InstalledApp{}
	for k, v := range s.apps {
		c := *v
		c.Secrets = map[string]string{}
		for a, b := range v.Secrets {
			c.Secrets[a] = b
		}
		out[k] = &c
	}
	return out, nil
}
func (s *fakeStore) InstalledApp(ctx context.Context, id string) (*store.InstalledApp, error) {
	all, _ := s.InstalledApps(ctx)
	if a, ok := all[id]; ok {
		return a, nil
	}
	return nil, store.ErrNotFound
}
func (s *fakeStore) SaveInstalledApp(_ context.Context, a *store.InstalledApp) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := *a
	s.apps[a.ID] = &c
	return nil
}
func (s *fakeStore) DeleteInstalledApp(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.apps, id)
	return nil
}

func setup(t *testing.T) (*Manager, *fakeEngine, *fakeStore) {
	t.Helper()
	apps, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	eng, st := newFake(), &fakeStore{apps: map[string]*store.InstalledApp{}}
	m := NewManager(apps, eng, st)
	m.portFree = func(int, string) bool { return true }
	return m, eng, st
}

func wait(t *testing.T, m *Manager, jobID string) Job {
	t.Helper()
	for i := 0; i < 1000; i++ { // up to 10 s on a busy machine
		if j, _ := m.Job(jobID); j.Done {
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job never finished")
	return Job{}
}

func TestCatalogLoads(t *testing.T) {
	apps, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) < 10 {
		t.Fatalf("only %d apps", len(apps))
	}
	for _, a := range apps {
		if a.Web == nil || a.Name == "" || a.Category == "" || len(a.Releases) == 0 {
			t.Errorf("%s: incomplete entry", a.ID)
		}
	}
}

func TestSplitImage(t *testing.T) {
	cases := map[string][2]string{
		"louislam/uptime-kuma:1":             {"louislam/uptime-kuma", "1"},
		"ghcr.io/open-webui/open-webui:main": {"ghcr.io/open-webui/open-webui", "main"},
		"nextcloud":                          {"nextcloud", "latest"},
		"registry:5000/team/app":             {"registry:5000/team/app", "latest"},
	}
	for in, want := range cases {
		if i, tg := splitImage(in); i != want[0] || tg != want[1] {
			t.Errorf("splitImage(%q) = %q, %q", in, i, tg)
		}
	}
}

func TestInstallMultiServiceApp(t *testing.T) {
	m, eng, st := setup(t)
	jid, err := m.Install(context.Background(), "open-webui", nil)
	if err != nil {
		t.Fatal(err)
	}
	if j := wait(t, m, jid); j.Error != "" {
		t.Fatal(j.Error)
	}
	if !eng.networks["nocap-open-webui"] {
		t.Fatal("private network not created")
	}
	web := eng.created["nocap-open-webui-webui"]
	row := st.apps["open-webui"]
	if row == nil || row.WebPort != portLow {
		t.Fatalf("row = %+v", row)
	}
	if b := web.HostConfig.PortBindings["8080/tcp"]; len(b) != 1 || b[0].HostPort != "18100" {
		t.Fatalf("web port binding = %+v", web.HostConfig.PortBindings)
	}
	env := strings.Join(web.Env, " ")
	if !strings.Contains(env, "OLLAMA_BASE_URL=http://ollama:11434") || !strings.Contains(env, "WEBUI_SECRET_KEY="+row.Secrets["webui_key"]) || len(row.Secrets["webui_key"]) != 24 {
		t.Fatalf("env/secrets wrong: %s / %+v", env, row.Secrets)
	}
	ollama := eng.created["nocap-open-webui-ollama"]
	if len(ollama.HostConfig.PortBindings) != 0 {
		t.Fatal("ollama should not be published on the host")
	}
	if ollama.NetworkingConfig.EndpointsConfig["nocap-open-webui"].Aliases[0] != "ollama" {
		t.Fatal("ollama alias missing")
	}
	status, _ := m.Status(context.Background())
	if s := status["open-webui"]; s == nil || s.Status != "running" || len(s.Containers) != 2 {
		t.Fatalf("status = %+v", s)
	}
	if _, err := m.Install(context.Background(), "open-webui", nil); !errors.Is(err, ErrInstalled) {
		t.Fatalf("second install: %v", err)
	}
}

func TestUpdateKeepsPortSecretsAndData(t *testing.T) {
	m, eng, st := setup(t)
	j, _ := m.Install(context.Background(), "code-server", nil)
	wait(t, m, j)
	before := *st.apps["code-server"]
	st.apps["code-server"].Version = "old"
	status, _ := m.Status(context.Background())
	if !status["code-server"].UpdateAvailable || status["code-server"].Credentials.Password != before.Secrets["password"] {
		t.Fatalf("status = %+v", status["code-server"])
	}
	j, _ = m.Update(context.Background(), "code-server", nil)
	if r := wait(t, m, j); r.Error != "" {
		t.Fatal(r.Error)
	}
	after := st.apps["code-server"]
	if after.WebPort != before.WebPort || after.Secrets["password"] != before.Secrets["password"] || after.Version != before.Version {
		t.Fatalf("update changed port/secret/version: %+v vs %+v", after, before)
	}
	if len(eng.removedVol) != 0 {
		t.Fatal("update must not delete data")
	}
	if !strings.Contains(strings.Join(eng.created["nocap-code-server-app"].Env, " "), "PASSWORD="+before.Secrets["password"]) {
		t.Fatal("password env changed after update")
	}
}

func TestUninstallKeepsOrDeletesData(t *testing.T) {
	m, eng, st := setup(t)
	j, _ := m.Install(context.Background(), "uptime-kuma", nil)
	wait(t, m, j)
	j, _ = m.Uninstall(context.Background(), "uptime-kuma", false, nil)
	wait(t, m, j)
	if len(eng.containers) != 0 || st.apps["uptime-kuma"] != nil || len(eng.removedVol) != 0 {
		t.Fatalf("uninstall (keep data): containers=%d row=%v removed=%v", len(eng.containers), st.apps["uptime-kuma"], eng.removedVol)
	}
	j, _ = m.Install(context.Background(), "uptime-kuma", nil)
	wait(t, m, j)
	j, _ = m.Uninstall(context.Background(), "uptime-kuma", true, nil)
	wait(t, m, j)
	if strings.Join(eng.removedVol, ",") != "nocap-uptime-kuma-data" {
		t.Fatalf("removed volumes = %v", eng.removedVol)
	}
}

func TestFailedInstallCleansUp(t *testing.T) {
	m, eng, st := setup(t)
	eng.pullErr = errors.New("manifest unknown")
	j, _ := m.Install(context.Background(), "jellyfin", nil)
	r := wait(t, m, j)
	if !strings.Contains(r.Error, "manifest unknown") || st.apps["jellyfin"] != nil || len(eng.containers) != 0 {
		t.Fatalf("job=%+v rows=%v containers=%d", r, st.apps, len(eng.containers))
	}
}

func TestFixedPortInUse(t *testing.T) {
	m, _, _ := setup(t)
	m.portFree = func(p int, _ string) bool { return p != 22000 }
	j, _ := m.Install(context.Background(), "syncthing", nil)
	if r := wait(t, m, j); !strings.Contains(r.Error, "22000") {
		t.Fatalf("want port error, got %+v", r)
	}
}

func TestControlStopStart(t *testing.T) {
	m, _, _ := setup(t)
	j, _ := m.Install(context.Background(), "memos", nil)
	wait(t, m, j)
	if err := m.Control(context.Background(), "memos", "stop"); err != nil {
		t.Fatal(err)
	}
	s, _ := m.Status(context.Background())
	if s["memos"].Status != "stopped" {
		t.Fatalf("status = %s", s["memos"].Status)
	}
	if err := m.Control(context.Background(), "nope", "start"); !errors.Is(err, ErrNotInstall) {
		t.Fatal(err)
	}
}

func TestSimultaneousInstallsGetDifferentPorts(t *testing.T) {
	m, _, st := setup(t)
	ids := []string{"it-tools", "memos", "excalidraw"}
	var jobs []string
	for _, id := range ids {
		j, err := m.Install(context.Background(), id, nil)
		if err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, j)
	}
	for _, j := range jobs {
		if r := wait(t, m, j); r.Error != "" {
			t.Fatal(r.Error)
		}
	}
	seen := map[int]string{}
	for _, id := range ids {
		p := st.apps[id].WebPort
		if other, dup := seen[p]; dup {
			t.Fatalf("%s and %s both got port %d", id, other, p)
		}
		seen[p] = id
	}
	// Finished jobs stay visible (with their outcome) for a while.
	if js := m.Jobs(); len(js) != 3 || !js["memos"].Done {
		t.Fatalf("recent jobs = %+v", js)
	}
}

func TestFailedInstallStaysVisible(t *testing.T) {
	m, eng, _ := setup(t)
	eng.pullErr = errors.New("registry unreachable")
	j, _ := m.Install(context.Background(), "memos", nil)
	wait(t, m, j)
	if got := m.Jobs()["memos"]; !got.Done || !strings.Contains(got.Error, "registry unreachable") {
		t.Fatalf("job = %+v", got)
	}
}
