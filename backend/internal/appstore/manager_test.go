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
	connected  []string // "network@container"
	// pullGate, when set, holds pulls of images containing pullGateOn until closed.
	pullGate   chan struct{}
	pullGateOn string
}

func newFake() *fakeEngine {
	return &fakeEngine{created: map[string]docker.CreateConfig{}, containers: map[string]docker.Container{}, networks: map[string]bool{}, volumes: map[string]bool{}}
}

func (f *fakeEngine) PullImage(_ context.Context, image, tag string, fn func(docker.PullProgress)) error {
	f.mu.Lock()
	f.pulls = append(f.pulls, image+":"+tag)
	gate := f.pullGate
	if !strings.Contains(image, f.pullGateOn) {
		gate = nil
	}
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
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
func (f *fakeEngine) ConnectNetwork(_ context.Context, n docker.NetLink, container string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected = append(f.connected, n.Name+"@"+container)
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
	mu        sync.Mutex
	apps      map[string]*store.InstalledApp
	overrides map[string]map[string]string
	// afterRead, when set, runs after InstalledApps has taken its snapshot.
	afterRead func()
}

func (s *fakeStore) AppOverrides(_ context.Context, app string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for k, v := range s.overrides[app] {
		out[k] = v
	}
	return out, nil
}
func (s *fakeStore) SaveAppOverride(_ context.Context, app, svc, spec string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.overrides == nil {
		s.overrides = map[string]map[string]string{}
	}
	if s.overrides[app] == nil {
		s.overrides[app] = map[string]string{}
	}
	s.overrides[app][svc] = spec
	return nil
}
func (s *fakeStore) DeleteAppOverrides(_ context.Context, app string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.overrides, app)
	return nil
}

func (s *fakeStore) InstalledApps(context.Context) (map[string]*store.InstalledApp, error) {
	s.mu.Lock()
	out := map[string]*store.InstalledApp{}
	for k, v := range s.apps {
		c := *v
		c.Secrets = map[string]string{}
		for a, b := range v.Secrets {
			c.Secrets[a] = b
		}
		out[k] = &c
	}
	hook := s.afterRead
	s.mu.Unlock()
	if hook != nil {
		hook()
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

// started fails the test when a job couldn't start (e.g. ErrBusy), instead of
// letting wait poll an empty job id until it gives up. Use it as
// started(t)(m.Install(...)).
func started(t *testing.T) func(string, error) string {
	return func(jobID string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("job didn't start: %v", err)
		}
		return jobID
	}
}

func wait(t *testing.T, m *Manager, jobID string) Job {
	t.Helper()
	if _, ok := m.Job(jobID); !ok {
		t.Fatalf("unknown job %q", jobID)
	}
	// Generous: the fake engine is instant, but CI runners (and -race) can be slow.
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		if j, _ := m.Job(jobID); j.Done {
			return j
		}
		time.Sleep(5 * time.Millisecond)
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
		// Web is nil for a deliberately headless app (e.g. a log watcher or a
		// bouncer with no browser UI) — AppCenter.tsx already renders that
		// case ("Runs in the background"), so it isn't an incomplete entry.
		if a.Name == "" || a.Category == "" || len(a.Releases) == 0 {
			t.Errorf("%s: incomplete entry", a.ID)
		}
	}
}

func TestCatalogRejectsPlainHTTPLinks(t *testing.T) {
	doc := `{"apps":[{"id":"x","name":"X","source":"http://example.com","services":[{"name":"app","image":"x"}]}]}`
	if _, err := parseCatalog([]byte(doc)); err == nil {
		t.Fatal("http source link accepted")
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
	j := started(t)(m.Install(context.Background(), "code-server", nil))
	wait(t, m, j)
	before := *st.apps["code-server"]
	st.apps["code-server"].Version = "old"
	status, _ := m.Status(context.Background())
	if !status["code-server"].UpdateAvailable || status["code-server"].Credentials.Password != before.Secrets["password"] {
		t.Fatalf("status = %+v", status["code-server"])
	}
	j = started(t)(m.Update(context.Background(), "code-server", nil))
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
	j := started(t)(m.Install(context.Background(), "uptime-kuma", nil))
	wait(t, m, j)
	j = started(t)(m.Uninstall(context.Background(), "uptime-kuma", false, nil))
	wait(t, m, j)
	if len(eng.containers) != 0 || st.apps["uptime-kuma"] != nil || len(eng.removedVol) != 0 {
		t.Fatalf("uninstall (keep data): containers=%d row=%v removed=%v", len(eng.containers), st.apps["uptime-kuma"], eng.removedVol)
	}
	j = started(t)(m.Install(context.Background(), "uptime-kuma", nil))
	wait(t, m, j)
	j = started(t)(m.Uninstall(context.Background(), "uptime-kuma", true, nil))
	wait(t, m, j)
	if strings.Join(eng.removedVol, ",") != "nocap-uptime-kuma-data" {
		t.Fatalf("removed volumes = %v", eng.removedVol)
	}
}

func TestFailedInstallCleansUp(t *testing.T) {
	m, eng, st := setup(t)
	eng.pullErr = errors.New("manifest unknown")
	j := started(t)(m.Install(context.Background(), "jellyfin", nil))
	r := wait(t, m, j)
	if !strings.Contains(r.Error, "manifest unknown") || st.apps["jellyfin"] != nil || len(eng.containers) != 0 {
		t.Fatalf("job=%+v rows=%v containers=%d", r, st.apps, len(eng.containers))
	}
}

func TestFixedPortInUse(t *testing.T) {
	m, _, _ := setup(t)
	m.portFree = func(p int, _ string) bool { return p != 22000 }
	j := started(t)(m.Install(context.Background(), "syncthing", nil))
	if r := wait(t, m, j); !strings.Contains(r.Error, "22000") {
		t.Fatalf("want port error, got %+v", r)
	}
}

func TestControlStopStart(t *testing.T) {
	m, _, _ := setup(t)
	j := started(t)(m.Install(context.Background(), "memos", nil))
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

// A second install that read the installed apps just before the first one
// saved its row, and reserves just after the first one finished, must still
// not get the first one's port.
func TestInstallFinishingMidPickKeepsItsPort(t *testing.T) {
	m, eng, st := setup(t)
	gate := make(chan struct{})
	eng.mu.Lock()
	eng.pullGate, eng.pullGateOn = gate, "memos"
	eng.mu.Unlock()
	first := started(t)(m.Install(context.Background(), "memos", nil))
	// memos has reserved its port and is now held in its download.
	for deadline := time.Now().Add(time.Minute); ; {
		if j, _ := m.Job(first); strings.HasPrefix(j.Phase, "Downloading") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first install never reached its download")
		}
		time.Sleep(time.Millisecond)
	}
	var once sync.Once
	reads := 0
	st.mu.Lock()
	st.afterRead = func() {
		st.mu.Lock()
		reads++
		n := reads
		st.mu.Unlock()
		if n != 2 { // 1: Install's "already installed?" check; 2: the port pick
			return
		}
		once.Do(func() {
			// Let memos finish while this pick holds a list without it.
			close(gate)
			for deadline := time.Now().Add(300 * time.Millisecond); time.Now().Before(deadline); {
				if j, _ := m.Job(first); j.Done {
					return
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
	st.mu.Unlock()
	second := started(t)(m.Install(context.Background(), "it-tools", nil))
	for _, j := range []string{first, second} {
		if r := wait(t, m, j); r.Error != "" {
			t.Fatal(r.Error)
		}
	}
	st.mu.Lock()
	a, b := st.apps["memos"].WebPort, st.apps["it-tools"].WebPort
	st.mu.Unlock()
	if a == b {
		t.Fatalf("both installs got port %d", a)
	}
}

// The completion callback runs while the app is still busy, and once a job
// is seen done the next job for the app can start without ErrBusy.
func TestDoneJobFreesApp(t *testing.T) {
	m, _, _ := setup(t)
	cb := make(chan string, 1)
	j := started(t)(m.Install(context.Background(), "memos", func(error) {
		job := m.Jobs()["memos"]
		switch {
		case !m.Busy("memos"):
			cb <- "app freed before the callback ran"
		case job.Done:
			cb <- "job marked done before the callback ran"
		default:
			cb <- ""
		}
	}))
	wait(t, m, j)
	if msg := <-cb; msg != "" {
		t.Fatal(msg)
	}
	if m.Busy("memos") {
		t.Fatal("app still busy after its job was seen done")
	}
	wait(t, m, started(t)(m.Uninstall(context.Background(), "memos", false, nil)))
}

func TestFailedInstallStaysVisible(t *testing.T) {
	m, eng, _ := setup(t)
	eng.pullErr = errors.New("registry unreachable")
	j := started(t)(m.Install(context.Background(), "memos", nil))
	wait(t, m, j)
	if got := m.Jobs()["memos"]; !got.Done || !strings.Contains(got.Error, "registry unreachable") {
		t.Fatalf("job = %+v", got)
	}
}

func TestSavedChangesSurviveUpdates(t *testing.T) {
	m, eng, st := setup(t)
	j := started(t)(m.Install(context.Background(), "code-server", nil))
	wait(t, m, j)
	orig := eng.created["nocap-code-server-app"]

	// Someone moves the data to a folder on a drive, pins the port, adds a
	// variable, joins a macvlan network and limits memory.
	spec := docker.Spec{
		Name: "nocap-code-server-app", Image: "ignored:old", Restart: "always", NetworkMode: "bridge",
		Env:      []docker.EnvVar{{Key: "TZ", Value: "Asia/Dubai"}},
		Ports:    []docker.PortMap{{Host: 8443, Container: 8443, Protocol: "tcp"}},
		Mounts:   []docker.SpecMount{{Type: "bind", Source: "/srv/nocapos/code", Target: "/config"}},
		Networks: []docker.NetLink{{Name: "lan", IPv4: "192.168.1.70"}},
		MemoryMB: 512,
	}
	if err := m.SaveOverride(context.Background(), "code-server", "app", spec); err != nil {
		t.Fatal(err)
	}
	st.apps["code-server"].Version = "old"
	j = started(t)(m.Update(context.Background(), "code-server", nil))
	if r := wait(t, m, j); r.Error != "" {
		t.Fatal(r.Error)
	}
	cfg := eng.created["nocap-code-server-app"]
	if cfg.Image != orig.Image {
		t.Errorf("image should follow the catalog: %s", cfg.Image)
	}
	if cfg.Labels[LabelApp] != "code-server" {
		t.Error("app labels lost")
	}
	env := strings.Join(cfg.Env, " ")
	if !strings.Contains(env, "TZ=Asia/Dubai") || !strings.Contains(env, "PASSWORD=") {
		t.Errorf("env not merged: %v", cfg.Env)
	}
	if len(cfg.HostConfig.Mounts) != 1 || cfg.HostConfig.Mounts[0].Type != "bind" || cfg.HostConfig.Mounts[0].Source != "/srv/nocapos/code" {
		t.Errorf("storage not kept: %+v", cfg.HostConfig.Mounts)
	}
	if b := cfg.HostConfig.PortBindings["8443/tcp"]; len(b) != 1 || b[0].HostPort != "8443" {
		t.Errorf("port not kept: %+v", cfg.HostConfig.PortBindings)
	}
	if cfg.HostConfig.Memory != 512<<20 || cfg.HostConfig.RestartPolicy.Name != "always" {
		t.Errorf("limits/restart not kept: %+v", cfg.HostConfig)
	}
	if len(eng.connected) != 1 || !strings.HasPrefix(eng.connected[0], "lan@") {
		t.Errorf("extra network not joined: %v", eng.connected)
	}

	// Uninstalling and deleting the data forgets the changes too.
	j = started(t)(m.Uninstall(context.Background(), "code-server", true, nil))
	wait(t, m, j)
	if ov, _ := st.AppOverrides(context.Background(), "code-server"); len(ov) != 0 {
		t.Error("saved changes kept after deleting the app's data")
	}
}
