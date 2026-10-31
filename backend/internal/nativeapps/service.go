package nativeapps

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"alfaos/alfad/internal/syspkg"
	"alfaos/alfad/internal/webapps"
)

// Status says whether Linux apps work here.
type Status struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
	// Screen: the server's own screen is on, so apps open there.
	Screen bool `json:"screen"`
}

// Unsupported reasons, shown as they are.
const (
	reasonNotNative = "Linux apps need NoCapOS installed directly on a Linux server. This one runs in Docker or on another OS, so there's nothing to install them on."
	reasonNoFlatpak = "Flatpak isn't on this server yet. Open Packages and install “Linux apps (Flatpak, apps from Flathub)”, then come back."
)

// ErrUnsupported wraps the reason Linux apps don't work here.
type ErrUnsupported struct{ Reason string }

func (e ErrUnsupported) Error() string { return e.Reason }

// ErrNotInstalled: launching an app that isn't installed.
var ErrNotInstalled = errors.New("That app isn't installed (anymore). Grab it from the App Store")

// ErrBadID: an app id that isn't one.
var ErrBadID = errors.New("That's not a valid app id")

// Launched says where an app opened.
type Launched struct {
	// Where is "screen" (the server's own monitor) or "stream" (a window here).
	Where  string                `json:"where"`
	Stream *webapps.StreamStatus `json:"stream,omitempty"`
}

// Person is who is launching an app.
type Person struct {
	ID       string // NoCapOS user id (stream ownership)
	Username string // their Linux account, when they have one
}

// Streamer runs apps streamed into NoCapOS windows (webapps.Streams).
type Streamer interface {
	Open(userID string, spec webapps.StreamSpec) (webapps.StreamStatus, error)
	StopKey(key string)
}

// Service is the Linux apps backend.
type Service struct {
	Catalog *Catalog
	Jobs    *Jobs
	Streams Streamer

	// Swappable in tests.
	Native     func() bool
	HasFlatpak func() bool
	Run        func(ctx context.Context, args ...string) (string, error)
	ReadFile   func(path string) ([]byte, error)
	Exists     func(path string) bool
	// OnScreen starts argv on the local session's screen as its user.
	OnScreen func(env DesktopEnv, argv []string) error
	// Account finds a person's Linux account for streamed apps (nil = run as
	// alfad's own account).
	Account func(username string) *webapps.RunAs

	EnvPath string
	DataDir string

	mu          sync.Mutex
	installed   []App
	installedAt time.Time
}

// New wires the real host.
func New(dataDir string, streams Streamer) *Service {
	s := &Service{
		Jobs: NewJobs(), Streams: streams,
		Native:     syspkg.Native,
		HasFlatpak: func() bool { _, err := exec.LookPath("flatpak"); return err == nil },
		Run:        runOutput,
		ReadFile:   os.ReadFile,
		Exists:     func(p string) bool { _, err := os.Stat(p); return err == nil },
		OnScreen:   startOnScreen,
		Account:    lookupAccount,
		EnvPath:    DesktopEnvPath,
		DataDir:    dataDir,
	}
	s.Catalog = NewCatalog(func(ctx context.Context) (string, error) {
		if !s.supported() {
			return "", errors.New("unsupported")
		}
		return s.Run(ctx, RemoteLsArgs()...)
	})
	return s
}

func (s *Service) supported() bool { return s.Native() && s.HasFlatpak() }

// Check returns ErrUnsupported when Linux apps can't work here.
func (s *Service) Check() error {
	if !s.Native() {
		return ErrUnsupported{reasonNotNative}
	}
	if !s.HasFlatpak() {
		return ErrUnsupported{reasonNoFlatpak}
	}
	return nil
}

// Status reports support and whether the server's screen is on.
func (s *Service) Status() Status {
	if err := s.Check(); err != nil {
		return Status{Reason: err.Error()}
	}
	_, screen := s.screen()
	return Status{Supported: true, Screen: screen}
}

// screen reads the local session's file and checks it's alive.
func (s *Service) screen() (DesktopEnv, bool) {
	b, err := s.ReadFile(s.EnvPath)
	if err != nil {
		return DesktopEnv{}, false
	}
	env := ParseDesktopEnv(string(b))
	if !env.Usable() {
		return env, false
	}
	for _, sock := range env.Sockets() {
		if s.Exists(sock) {
			return env, true
		}
	}
	return env, false
}

// Installed lists installed apps (cached briefly).
func (s *Service) Installed(ctx context.Context) ([]App, error) {
	if err := s.Check(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.installed != nil && time.Since(s.installedAt) < 15*time.Second {
		defer s.mu.Unlock()
		return append([]App(nil), s.installed...), nil
	}
	s.mu.Unlock()
	out, err := s.Run(ctx, ListArgs()...)
	if err != nil {
		return nil, err
	}
	apps := ParseList(out)
	s.mu.Lock()
	s.installed, s.installedAt = apps, time.Now()
	s.mu.Unlock()
	return append([]App(nil), apps...), nil
}

func (s *Service) invalidate() {
	s.mu.Lock()
	s.installed = nil
	s.mu.Unlock()
}

// installedSet maps installed ids to their entry.
func (s *Service) installedSet(ctx context.Context) map[string]App {
	m := map[string]App{}
	apps, _ := s.Installed(ctx)
	for _, a := range apps {
		m[a.ID] = a
	}
	return m
}

func (s *Service) mark(ctx context.Context, apps []App) []App {
	inst := s.installedSet(ctx)
	for i := range apps {
		if a, ok := inst[apps[i].ID]; ok {
			apps[i].Installed, apps[i].Version = true, a.Version
		}
	}
	return apps
}

// Search finds apps on Flathub.
func (s *Service) Search(ctx context.Context, q string) ([]App, error) {
	if err := s.Check(); err != nil {
		return nil, err
	}
	apps, err := s.Catalog.Search(ctx, q)
	if err != nil {
		return nil, err
	}
	return s.mark(ctx, append([]App(nil), apps...)), nil
}

// Popular is the landing page.
func (s *Service) Popular(ctx context.Context) ([]App, error) {
	if err := s.Check(); err != nil {
		return nil, err
	}
	return s.mark(ctx, s.Catalog.Enrich(ctx, Popular)), nil
}

// Details is an app's page.
func (s *Service) Details(ctx context.Context, id string) (*Details, error) {
	if err := s.Check(); err != nil {
		return nil, err
	}
	if !ValidID(id) {
		return nil, ErrBadID
	}
	d, err := s.Catalog.Details(ctx, id)
	if err != nil {
		// Installed apps still get a page.
		if a, ok := s.installedSet(ctx)[id]; ok {
			return &Details{App: a}, nil
		}
		return nil, err
	}
	if a, ok := s.installedSet(ctx)[id]; ok {
		d.Installed, d.Version = true, a.Version
	}
	return d, nil
}

// InstallSteps adds Flathub when missing, then installs the app.
func InstallSteps(id string) []Step {
	return []Step{{Args: RemoteAddArgs(), Optional: true}, {Args: InstallArgs(id)}}
}

// Install starts installing an app; onDone gets the result.
func (s *Service) Install(id, name string, onDone func(Job)) (Job, error) {
	if err := s.Check(); err != nil {
		return Job{}, err
	}
	if !ValidID(id) {
		return Job{}, ErrBadID
	}
	if name == "" {
		name = id
	}
	return s.Jobs.Start(id, name, "install", InstallSteps(id), func(j Job) {
		s.invalidate()
		if onDone != nil {
			onDone(j)
		}
	})
}

// Uninstall starts removing an app and closes its streams.
func (s *Service) Uninstall(id, name string, onDone func(Job)) (Job, error) {
	if err := s.Check(); err != nil {
		return Job{}, err
	}
	if !ValidID(id) {
		return Job{}, ErrBadID
	}
	if name == "" {
		name = id
	}
	if s.Streams != nil {
		s.Streams.StopKey(streamKey(id))
	}
	return s.Jobs.Start(id, name, "uninstall", []Step{{Args: UninstallArgs(id)}}, func(j Job) {
		s.invalidate()
		if onDone != nil {
			onDone(j)
		}
	})
}

func streamKey(id string) string { return "flatpak:" + id }

// Launch opens an installed app: on the server's own screen when its local
// session is on, else streamed into a NoCapOS window.
func (s *Service) Launch(ctx context.Context, who Person, id string) (Launched, error) {
	if err := s.Check(); err != nil {
		return Launched{}, err
	}
	if !ValidID(id) {
		return Launched{}, ErrBadID
	}
	app, ok := s.installedSet(ctx)[id]
	if !ok {
		s.invalidate() // maybe installed a moment ago
		if app, ok = s.installedSet(ctx)[id]; !ok {
			return Launched{}, ErrNotInstalled
		}
	}
	if env, on := s.screen(); on {
		if err := s.OnScreen(env, RunArgs(id)); err == nil {
			return Launched{Where: "screen"}, nil
		}
		// The screen didn't take it: stream it instead.
	}
	if s.Streams == nil {
		return Launched{}, ErrUnsupported{reasonNotNative}
	}
	spec := webapps.StreamSpec{Key: streamKey(id), Title: app.Name, Command: RunArgs(id)}
	if a := s.Account(who.Username); a != nil && a.UID != 0 {
		spec.RunAs = a
	} else {
		// alfad's own account, with a home of its own per person.
		spec.Home = filepath.Join(s.DataDir, "app-homes", safeDir(who.ID))
	}
	st, err := s.Streams.Open(who.ID, spec)
	if err != nil {
		return Launched{}, err
	}
	return Launched{Where: "stream", Stream: &st}, nil
}

func safeDir(id string) string {
	out := make([]rune, 0, len(id))
	for _, c := range id {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return "unknown"
	}
	return string(out)
}

// iconDirs are where installed apps export their icons.
var iconDirs = []string{
	"/var/lib/flatpak/exports/share/icons/hicolor/128x128/apps",
	"/var/lib/flatpak/exports/share/icons/hicolor/256x256/apps",
	"/var/lib/flatpak/exports/share/icons/hicolor/64x64/apps",
	"/var/lib/flatpak/exports/share/icons/hicolor/512x512/apps",
	"/var/lib/flatpak/exports/share/icons/hicolor/scalable/apps",
}

// Icon is an app's icon: the installed one, else Flathub's.
func (s *Service) Icon(ctx context.Context, id string) (Icon, error) {
	if !ValidID(id) {
		return Icon{}, ErrBadID
	}
	if s.Native() {
		for _, dir := range iconDirs {
			for _, ext := range []string{".png", ".svg"} {
				p := filepath.Join(dir, id+ext)
				if b, err := s.ReadFile(p); err == nil && len(b) <= iconMax {
					if t := iconType(p, b); t != "" {
						return Icon{Type: t, Data: b}, nil
					}
				}
			}
		}
	}
	return s.Catalog.RemoteIcon(ctx, id)
}
