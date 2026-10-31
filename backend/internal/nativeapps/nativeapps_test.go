package nativeapps

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"alfaos/alfad/internal/webapps"
)

func TestValidID(t *testing.T) {
	good := []string{"org.gimp.GIMP", "com.visualstudio.code", "org.mozilla.firefox", "io.github.some_user.App-Name.Tool", "md.obsidian.Obsidian", "org.kde.krita"}
	for _, id := range good {
		if !ValidID(id) {
			t.Errorf("%q rejected", id)
		}
	}
	bad := []string{
		"", "gimp", "org.gimp", "org.gimp.GIMP;rm -rf /", "org.gimp.GIMP --user", "-org.gimp.GIMP", "--system",
		"org..gimp", "org.gimp.", ".org.gimp.GIMP", "org.gimp.1GIMP", "org.gimp.GI-MP", "org/gimp/GIMP",
		"org.gimp.GIMP\n", "org.gimp.$(id)", "../../etc.passwd.x", strings.Repeat("a.", 130) + "b",
	}
	for _, id := range bad {
		if ValidID(id) {
			t.Errorf("%q accepted", id)
		}
	}
}

func TestCommandArgv(t *testing.T) {
	cases := map[string][2][]string{
		"install":   {InstallArgs("org.gimp.GIMP"), {"flatpak", "install", "-y", "--noninteractive", "--system", "flathub", "org.gimp.GIMP"}},
		"uninstall": {UninstallArgs("org.gimp.GIMP"), {"flatpak", "uninstall", "-y", "--noninteractive", "--system", "org.gimp.GIMP"}},
		"list":      {ListArgs(), {"flatpak", "list", "--app", "--system", "--columns=application,name,version"}},
		"remote-ls": {RemoteLsArgs(), {"flatpak", "remote-ls", "--system", "--app", "--columns=application,name,description", "flathub"}},
		"remote":    {RemoteAddArgs(), {"flatpak", "remote-add", "--if-not-exists", "--system", "flathub", "https://dl.flathub.org/repo/flathub.flatpakrepo"}},
		"run":       {RunArgs("org.gimp.GIMP"), {"flatpak", "run", "org.gimp.GIMP"}},
	}
	for name, c := range cases {
		if !reflect.DeepEqual(c[0], c[1]) {
			t.Errorf("%s: %q, want %q", name, c[0], c[1])
		}
	}
	steps := InstallSteps("org.gimp.GIMP")
	if len(steps) != 2 || !steps[0].Optional || steps[1].Optional || steps[1].Args[1] != "install" {
		t.Fatalf("install steps = %+v", steps)
	}
}

func TestParseList(t *testing.T) {
	out := "org.gimp.GIMP\tGNU Image Manipulation Program\t3.0.4\n" +
		"org.mozilla.firefox\tFirefox\t140.0\n" +
		"\n" +
		"org.mozilla.firefox\tFirefox\t140.0\n" // listed twice (two branches)
	got := ParseList(out)
	want := []App{
		{ID: "org.gimp.GIMP", Name: "GNU Image Manipulation Program", Version: "3.0.4", Installed: true},
		{ID: "org.mozilla.firefox", Name: "Firefox", Version: "140.0", Installed: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	// A terminal-formatted table with a header.
	tty := "Application ID          Name       Version\norg.videolan.VLC        VLC        3.0.21\n"
	if got := ParseList(tty); len(got) != 1 || got[0].Name != "VLC" || got[0].Version != "3.0.21" {
		t.Fatalf("tty: %+v", got)
	}
	if got := ParseList(""); got == nil || len(got) != 0 {
		t.Fatalf("empty: %#v", got)
	}
}

func TestParseRemoteLs(t *testing.T) {
	out := "org.gimp.GIMP\tGNU Image Manipulation Program\tHigh-end image creation and manipulation\r\n" +
		"bad id\tNope\tNope\n" +
		"org.kde.krita\tKrita\t\n"
	got := ParseRemoteLs(out)
	if len(got) != 2 || got[0].Summary != "High-end image creation and manipulation" || got[1].Name != "Krita" || got[1].Summary != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestProgress(t *testing.T) {
	var p Progress
	for _, l := range []string{
		"Looking for matches…",
		"Installing 1/2… ████     20%  1.2 MB/s  00:10",
		"Installing 1/2… ████████ 100%  1.2 MB/s  00:00",
		"Installing 2/2… ██       40%",
	} {
		p.Line(l)
	}
	if p.Percent != 70 {
		t.Fatalf("percent = %d, want 70", p.Percent)
	}
	if p.Phase != "Downloading and installing (2 of 2)" {
		t.Fatalf("phase = %q", p.Phase)
	}
	p.Line("Installing 1/2… 10%") // never backwards
	if p.Percent != 70 {
		t.Fatalf("went backwards to %d", p.Percent)
	}
	var q Progress
	q.Line("Uninstalling 1/1… 100%")
	if q.Percent != 99 || q.Phase != "Removing" {
		t.Fatalf("uninstall: %+v", q)
	}
}

func TestJobWriterFollowsCarriageReturns(t *testing.T) {
	x := &job{}
	_, _ = x.Write([]byte("Installing 1/1… 10%\rInstalling 1/1… 55%\r"))
	if x.j.Percent != 55 {
		t.Fatalf("percent = %d", x.j.Percent)
	}
}

func TestParseDesktopEnv(t *testing.T) {
	env := ParseDesktopEnv("# NoCap Desktop\nexport WAYLAND_DISPLAY=wayland-0\nXDG_RUNTIME_DIR=\"/run/user/1001\"\nUSER=nocap-desktop\r\nDISPLAY=:0\nJUNK\nOTHER=1\n")
	want := DesktopEnv{User: "nocap-desktop", WaylandDisplay: "wayland-0", XDGRuntimeDir: "/run/user/1001", Display: ":0"}
	if env != want {
		t.Fatalf("got %+v", env)
	}
	if !env.Usable() {
		t.Fatal("should be usable")
	}
	if got := env.Sockets(); !reflect.DeepEqual(got, []string{"/run/user/1001/wayland-0", "/tmp/.X11-unix/X0"}) {
		t.Fatalf("sockets = %v", got)
	}
	e := env.Env()
	for _, w := range []string{"XDG_RUNTIME_DIR=/run/user/1001", "WAYLAND_DISPLAY=wayland-0", "DISPLAY=:0", "XDG_SESSION_TYPE=wayland"} {
		if !contains(e, w) {
			t.Errorf("env lacks %s: %v", w, e)
		}
	}
	for _, bad := range []DesktopEnv{
		{User: "root", WaylandDisplay: "wayland-0", XDGRuntimeDir: "/run/user/0"},
		{User: "x y", WaylandDisplay: "wayland-0", XDGRuntimeDir: "/run/user/1"},
		{User: "nocap", WaylandDisplay: "wayland-0", XDGRuntimeDir: "relative"},
		{User: "nocap", XDGRuntimeDir: "/run/user/1"},
		{User: "nocap", XDGRuntimeDir: "/run/user/1", Display: "remote:0"},
	} {
		if bad.Usable() {
			t.Errorf("%+v should not be usable", bad)
		}
	}
	if got := (DesktopEnv{WaylandDisplay: "../../etc/x", XDGRuntimeDir: "/run/user/1"}).Sockets(); len(got) != 0 {
		t.Fatalf("traversal socket: %v", got)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestPopularIDsValid(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range Popular {
		if !ValidID(a.ID) || seen[a.ID] || a.Name == "" {
			t.Errorf("bad popular entry %+v", a)
		}
		seen[a.ID] = true
	}
}

// --- the service, with a fake host ---

type fakeStreams struct {
	mu    sync.Mutex
	specs []webapps.StreamSpec
}

func (f *fakeStreams) Open(userID string, spec webapps.StreamSpec) (webapps.StreamStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.specs = append(f.specs, spec)
	return webapps.StreamStatus{ID: "0123456789abcdef01234567", Key: spec.Key, Title: spec.Title, Phase: webapps.PhaseStarting}, nil
}

func (f *fakeStreams) StopKey(string) {}

type fakeHost struct {
	mu       sync.Mutex
	list     string
	ran      [][]string
	envFile  string
	sockets  map[string]bool
	onScreen [][]string
}

func newFakeService(t *testing.T, native bool) (*Service, *fakeHost) {
	t.Helper()
	h := &fakeHost{list: "org.gimp.GIMP\tGIMP\t3.0\n", sockets: map[string]bool{}}
	s := New(t.TempDir(), &fakeStreams{})
	s.Native = func() bool { return native }
	s.HasFlatpak = func() bool { return true }
	s.Run = func(_ context.Context, args ...string) (string, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.ran = append(h.ran, args)
		if args[1] == "list" {
			return h.list, nil
		}
		return "", errors.New("offline")
	}
	s.ReadFile = func(p string) ([]byte, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if p == s.EnvPath && h.envFile != "" {
			return []byte(h.envFile), nil
		}
		return nil, errors.New("no such file")
	}
	s.Exists = func(p string) bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.sockets[p]
	}
	s.OnScreen = func(_ DesktopEnv, argv []string) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.onScreen = append(h.onScreen, argv)
		return nil
	}
	s.Account = func(string) *webapps.RunAs { return nil }
	s.Jobs.Exec = func(_ context.Context, args []string, out io.Writer) error {
		h.mu.Lock()
		h.ran = append(h.ran, args)
		h.mu.Unlock()
		_, _ = io.WriteString(out, "Installing 1/1… 100%\n")
		return nil
	}
	return s, h
}

func TestUnsupportedEverywhereElse(t *testing.T) {
	s, _ := newFakeService(t, false)
	if st := s.Status(); st.Supported || !strings.Contains(st.Reason, "directly on a Linux server") {
		t.Fatalf("status = %+v", st)
	}
	var unsup ErrUnsupported
	if _, err := s.Install("org.gimp.GIMP", "", nil); !errors.As(err, &unsup) {
		t.Fatalf("install err = %v", err)
	}
	if _, err := s.Launch(context.Background(), Person{ID: "u1"}, "org.gimp.GIMP"); !errors.As(err, &unsup) {
		t.Fatalf("launch err = %v", err)
	}
	s.Native = func() bool { return true }
	s.HasFlatpak = func() bool { return false }
	if st := s.Status(); st.Supported || !strings.Contains(st.Reason, "Packages") {
		t.Fatalf("no flatpak: %+v", st)
	}
}

func TestInstallRunsArgvAsJob(t *testing.T) {
	s, h := newFakeService(t, true)
	if _, err := s.Install("org.gimp.GIMP; reboot", "", nil); !errors.Is(err, ErrBadID) {
		t.Fatalf("bad id err = %v", err)
	}
	done := make(chan Job, 1)
	if _, err := s.Install("org.kde.krita", "Krita", func(j Job) { done <- j }); err != nil {
		t.Fatal(err)
	}
	select {
	case j := <-done:
		if !j.OK || j.Percent != 100 || j.Action != "install" || j.Name != "Krita" {
			t.Fatalf("job = %+v", j)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("job never finished")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !reflect.DeepEqual(h.ran[len(h.ran)-2], RemoteAddArgs()) || !reflect.DeepEqual(h.ran[len(h.ran)-1], InstallArgs("org.kde.krita")) {
		t.Fatalf("ran %q", h.ran)
	}
}

func TestJobsOneAtATime(t *testing.T) {
	js := NewJobs()
	release := make(chan struct{})
	js.Exec = func(context.Context, []string, io.Writer) error { <-release; return nil }
	if _, err := js.Start("a.b.c", "A", "install", []Step{{Args: []string{"x"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := js.Start("a.b.d", "D", "install", []Step{{Args: []string{"x"}}}, nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v", err)
	}
	close(release)
}

func TestFriendlyError(t *testing.T) {
	err := friendlyError("Looking for matches…\nerror: Unable to connect to dl.flathub.org\n", errors.New("exit status 1"))
	if !strings.Contains(err.Error(), "Flathub's being weird") {
		t.Fatalf("err = %v", err)
	}
	err = friendlyError("error: Nothing matches org.nope.App in remote flathub\n", errors.New("exit status 1"))
	if err.Error() != "Nothing matches org.nope.App in remote flathub" {
		t.Fatalf("err = %v", err)
	}
}

func TestLaunchPrefersServerScreen(t *testing.T) {
	s, h := newFakeService(t, true)
	ctx := context.Background()
	if _, err := s.Launch(ctx, Person{ID: "u1"}, "org.kde.krita"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("not installed: %v", err)
	}
	// No screen: streamed.
	got, err := s.Launch(ctx, Person{ID: "u1"}, "org.gimp.GIMP")
	if err != nil || got.Where != "stream" || got.Stream == nil || got.Stream.Key != "flatpak:org.gimp.GIMP" {
		t.Fatalf("stream launch = %+v %v", got, err)
	}
	fs := s.Streams.(*fakeStreams)
	if sp := fs.specs[0]; !reflect.DeepEqual(sp.Command, []string{"flatpak", "run", "org.gimp.GIMP"}) || sp.Title != "GIMP" || sp.RunAs != nil || !strings.Contains(sp.Home, "u1") {
		t.Fatalf("spec = %+v", sp)
	}
	// The file exists but the session is gone: still streamed.
	h.mu.Lock()
	h.envFile = "USER=nocap-desktop\nWAYLAND_DISPLAY=wayland-0\nXDG_RUNTIME_DIR=/run/user/1001\n"
	h.mu.Unlock()
	if got, _ := s.Launch(ctx, Person{ID: "u1"}, "org.gimp.GIMP"); got.Where != "stream" {
		t.Fatalf("dead session: %+v", got)
	}
	// Session alive: on the server's screen.
	h.mu.Lock()
	h.sockets["/run/user/1001/wayland-0"] = true
	h.mu.Unlock()
	if !s.Status().Screen {
		t.Fatal("status should report the screen")
	}
	got, err = s.Launch(ctx, Person{ID: "u1"}, "org.gimp.GIMP")
	if err != nil || got.Where != "screen" {
		t.Fatalf("screen launch = %+v %v", got, err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.onScreen) != 1 || !reflect.DeepEqual(h.onScreen[0], []string{"flatpak", "run", "org.gimp.GIMP"}) {
		t.Fatalf("on screen = %q", h.onScreen)
	}
}

func TestCatalogSearchAndFallback(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/search" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"hits":[
			{"app_id":"org.gimp.GIMP","name":"GIMP","summary":"Images","icon":"https://dl.flathub.org/media/gimp.png","developer_name":"The GIMP team","type":"desktop-application"},
			{"app_id":"bad id","name":"Bad"},
			{"app_id":"org.evil.App","name":"Evil","icon":"https://evil.example/x.png","type":"desktop-application"},
			{"app_id":"org.gnome.Platform","name":"Runtime","type":"runtime"}]}`)
	}))
	defer srv.Close()
	c := NewCatalog(func(context.Context) (string, error) {
		return "org.kde.krita\tKrita\tDigital painting\norg.gimp.GIMP\tGIMP\tImages\n", nil
	})
	c.Base = srv.URL
	apps, err := c.Search(context.Background(), "gimp")
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 2 || apps[0].Icon != "https://dl.flathub.org/media/gimp.png" || apps[1].Icon != "" {
		t.Fatalf("apps = %+v", apps)
	}
	if _, _ = c.Search(context.Background(), "GIMP"); calls != 1 {
		t.Fatalf("cache missed: %d calls", calls)
	}

	// Flathub down: the local catalogue answers.
	srv.Close()
	apps, err = c.Search(context.Background(), "krita")
	if err != nil || len(apps) != 1 || apps[0].ID != "org.kde.krita" {
		t.Fatalf("fallback = %+v %v", apps, err)
	}
	c.RemoteLs = func(context.Context) (string, error) { return "", errors.New("no") }
	c.remote = cached[[]App]{}
	if _, err := c.Search(context.Background(), "inkscape"); !errors.Is(err, ErrOffline) {
		t.Fatalf("offline err = %v", err)
	}
}

func TestPlainText(t *testing.T) {
	got := plainText("<p>GIMP is &amp; great.</p>\n<ul><li>Paint</li><li>Retouch <em>photos</em></li></ul><script>x</script>")
	want := "GIMP is & great.\n• Paint\n• Retouch photos\n\nx"
	if got != want {
		t.Fatalf("got %q", got)
	}
}
