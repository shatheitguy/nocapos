package osimage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDetect(t *testing.T) {
	dir := t.TempDir()
	old := MarkerPath
	t.Cleanup(func() { MarkerPath = old })
	MarkerPath = filepath.Join(dir, "image-os")
	if _, ok := Detect(); ok {
		t.Fatal("no marker must mean a script install")
	}
	os.WriteFile(MarkerPath, []byte("# NoCapOS\nTARGET=pc\nVERSION=0.14.0\nDEBIAN_SNAPSHOT=20261115T000000Z\n"), 0o644)
	in, ok := Detect()
	if !ok || in.Target != "pc" || in.Version != "0.14.0" || in.Channel != ChannelStable || in.Snapshot != "20261115T000000Z" {
		t.Fatalf("got %+v %v", in, ok)
	}
}

func TestVersionOrder(t *testing.T) {
	order := []string{"0.13.0", "0.14.0-beta.1", "0.14.0-beta.2", "0.14.0-rc.1", "0.14.0", "0.14.0-s1", "v0.14.0-s2", "0.14.1", "1.0.0"}
	for i := 0; i+1 < len(order); i++ {
		if !Newer(order[i+1], order[i]) || Newer(order[i], order[i+1]) {
			t.Errorf("%s should be newer than %s", order[i+1], order[i])
		}
	}
	if Newer("x", "0.1.0") || Newer("0.1.0", "x") {
		t.Error("unparsable versions are never newer")
	}
	if !SameRelease("0.14.0", "0.14.0-s3") || SameRelease("0.14.0", "0.14.1") || SameRelease("0.14.0-beta.1", "0.14.0") {
		t.Error("SameRelease")
	}
}

func TestParseProgressAndSlots(t *testing.T) {
	in := strings.NewReader("noise\n{\"event\":\"UpdateProgress\",\"progress\":12.5}\n{\"event\":\"CompatibilityCheckSkipped\",\"scope\":\"system\",\"reason\":\"x\"}\n{\"event\":\"UpdateProgress\",\"progress\":100.0}\n")
	var got []float64
	ParseProgress(in, func(p float64) { got = append(got, p) })
	if len(got) != 2 || got[0] != 12.5 || got[1] != 100 {
		t.Fatalf("progress %v", got)
	}
	sl, err := ParseSlots([]byte(`{"slots":{},"boot":{"bootFlow":"grub-efi","activeGroup":"b","defaultGroup":"a","groups":{}},"state":{"status":"Active"}}`))
	if err != nil || sl.Active != "b" || sl.Default != "a" || !sl.Trial() {
		t.Fatalf("slots %+v %v", sl, err)
	}
}

func TestOutcome(t *testing.T) {
	st := &State{Phase: PhaseRestarting, From: "0.14.0", To: "0.14.0-s1"}
	cases := []struct {
		running string
		slots   Slots
		check   *BootCheck
		want    string
		reason  string
	}{
		{"0.14.0-s1", Slots{"b", "a"}, nil, OutcomeChecking, ""},
		{"0.14.0-s1", Slots{"b", "b"}, &BootCheck{OK: true, Version: "0.14.0-s1"}, OutcomeUpdated, ""},
		{"0.14.0", Slots{"a", "a"}, &BootCheck{OK: false, Version: "0.14.0-s1", Reason: "nocapos_not_responding"}, OutcomeRolledBack, "nocapos_not_responding"},
		{"0.14.0", Slots{"a", "a"}, nil, OutcomeRolledBack, "did_not_start"},
	}
	for _, c := range cases {
		got, reason := Outcome(st, c.running, c.slots, c.check)
		if got != c.want || reason != c.reason {
			t.Errorf("running %s slots %+v: got %s/%s, want %s/%s", c.running, c.slots, got, reason, c.want, c.reason)
		}
	}
	if o, _ := Outcome(&State{Phase: PhaseReady}, "0.14.0", Slots{}, nil); o != "" {
		t.Error("no outcome before the restart")
	}
}

func TestSettingsWindow(t *testing.T) {
	s := Settings{AutoTime: "03:30"}
	loc := time.UTC
	in := []time.Time{time.Date(2026, 1, 2, 3, 30, 0, 0, loc), time.Date(2026, 1, 2, 4, 29, 0, 0, loc)}
	out := []time.Time{time.Date(2026, 1, 2, 3, 29, 0, 0, loc), time.Date(2026, 1, 2, 4, 30, 0, 0, loc), time.Date(2026, 1, 2, 15, 0, 0, 0, loc)}
	for _, n := range in {
		if !s.InWindow(n) {
			t.Errorf("%v should be in the window", n)
		}
	}
	for _, n := range out {
		if s.InWindow(n) {
			t.Errorf("%v should be outside the window", n)
		}
	}
	dir := t.TempDir()
	if err := SaveSettings(dir, Settings{Channel: "nightly", AutoTime: "03:30"}); err == nil {
		t.Error("unknown channel accepted")
	}
	if err := SaveSettings(dir, Settings{Channel: ChannelBeta, AutoSecurity: true, AutoTime: "02:15"}); err != nil {
		t.Fatal(err)
	}
	if got := LoadSettings(dir, Info{Channel: ChannelStable}); got.Channel != ChannelBeta || !got.AutoSecurity || got.AutoTime != "02:15" {
		t.Fatalf("settings %+v", got)
	}
	if got := LoadSettings(t.TempDir(), Info{Channel: ChannelBeta}); got.Channel != ChannelBeta || got.AutoSecurity || got.AutoTime != DefaultAutoTime {
		t.Fatalf("defaults %+v", got)
	}
}

const testHash = "sha512-256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func manifestServer(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mf := func(v string, sec bool, targets ...string) Manifest {
		m := Manifest{Version: v, Security: sec, Targets: map[string]Target{}}
		for _, tg := range targets {
			m.Targets[tg] = Target{Bundle: "nocapos-" + tg + ".rugixb", URL: "https://github.com/shatheitguy/nocapos/releases/download/v" + v + "/nocapos-" + tg + ".rugixb", BundleHash: testHash, Size: 1 << 30}
		}
		return m
	}
	manifests := map[string]Manifest{
		"/m/0.14.0":        mf("0.14.0", false, "pc", "pi"),
		"/m/0.14.0-s1":     mf("0.14.0-s1", true, "pc"),
		"/m/0.15.0-beta.1": mf("0.15.0-beta.1", false, "pc", "pi"),
	}
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases" {
			rel := func(tag string, pre bool, withManifest bool) map[string]any {
				assets := []map[string]string{{"name": "install.sh", "browser_download_url": "https://github.com/x/install.sh"}}
				if withManifest {
					assets = append(assets, map[string]string{"name": ManifestAsset, "browser_download_url": "https://github.com/m/" + strings.TrimPrefix(tag, "v")})
				}
				return map[string]any{"tag_name": tag, "prerelease": pre, "html_url": "https://github.com/shatheitguy/nocapos/releases/tag/" + tag, "assets": assets}
			}
			json.NewEncoder(w).Encode([]any{
				rel("v0.15.0-beta.1", true, true),
				rel("v0.14.0-s1", false, true),
				rel("v0.14.0", false, true),
				rel("v0.13.9", false, false),
			})
			return
		}
		m, ok := manifests[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(m)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// rewriteTransport sends https://github.com/m/... to the test server.
type rewriteTransport struct{ base string }

func (rt rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	u := *r.URL
	if u.Host == "github.com" {
		r2 := r.Clone(r.Context())
		nu, _ := http.NewRequest(r.Method, rt.base+u.Path, nil)
		r2.URL = nu.URL
		r2.Host = nu.URL.Host
		return http.DefaultTransport.RoundTrip(r2)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func TestLatest(t *testing.T) {
	srv := manifestServer(t)
	oldAPI, oldClient := ReleasesAPI, HTTPClient
	t.Cleanup(func() { ReleasesAPI, HTTPClient = oldAPI, oldClient })
	ReleasesAPI = srv.URL + "/releases"
	HTTPClient = &http.Client{Transport: rewriteTransport{srv.URL}}
	ctx := context.Background()

	m, err := Latest(ctx, ChannelStable, "pc", "test")
	if err != nil || m.Version != "0.14.0-s1" || !m.Security {
		t.Fatalf("stable pc: %+v %v", m, err)
	}
	// The security refresh only has the PC: the Pi gets the release.
	m, err = Latest(ctx, ChannelStable, "pi", "test")
	if err != nil || m.Version != "0.14.0" {
		t.Fatalf("stable pi: %+v %v", m, err)
	}
	m, err = Latest(ctx, ChannelBeta, "pi", "test")
	if err != nil || m.Version != "0.15.0-beta.1" {
		t.Fatalf("beta pi: %+v %v", m, err)
	}
	if _, err := Latest(ctx, ChannelStable, "riscv", "test"); !errors.Is(err, ErrNoRelease) {
		t.Fatalf("unknown target: %v", err)
	}
}

func TestManifestCheck(t *testing.T) {
	good := Target{URL: "https://github.com/shatheitguy/nocapos/releases/download/v1/x.rugixb", BundleHash: testHash}
	m := &Manifest{Version: "0.15.0", MinFrom: "0.14.0", Targets: map[string]Target{"pc": good}}
	if _, err := m.Check("pc", "0.14.0-s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Check("pi", "0.14.0"); err == nil {
		t.Error("missing target accepted")
	}
	if _, err := m.Check("pc", "0.13.0"); err == nil {
		t.Error("too old to update straight accepted")
	}
	bad := *m
	bad.Targets = map[string]Target{"pc": {URL: "https://example.com/x.rugixb", BundleHash: testHash}}
	if _, err := bad.Check("pc", "0.14.0"); err == nil {
		t.Error("foreign URL accepted")
	}
	bad.Targets = map[string]Target{"pc": {URL: good.URL, BundleHash: "sha256:xyz"}}
	if _, err := bad.Check("pc", "0.14.0"); err == nil {
		t.Error("bad hash accepted")
	}
}

type fakeCtrl struct {
	installErr error
	gotHash    string
	restarted  bool
	slots      Slots
}

func (f *fakeCtrl) Install(ctx context.Context, url, hash string, progress func(float64)) error {
	f.gotHash = hash
	progress(50)
	return f.installErr
}
func (f *fakeCtrl) RestartIntoSpare(context.Context) error { f.restarted = true; return nil }
func (f *fakeCtrl) Slots(context.Context) (Slots, error)   { return f.slots, nil }

func waitIdle(t *testing.T, u *Updater) {
	t.Helper()
	for i := 0; i < 200 && u.Running(); i++ {
		time.Sleep(5 * time.Millisecond)
	}
}

func TestUpdaterFlow(t *testing.T) {
	dir := t.TempDir()
	ctrl := &fakeCtrl{}
	u := &Updater{DataDir: dir, Info: Info{Target: "pc", Version: "0.14.0"}, Ctrl: ctrl}
	m := &Manifest{Version: "0.14.0-s1", Security: true, Targets: map[string]Target{"pc": {URL: "https://github.com/a/b.rugixb", BundleHash: testHash, Signed: true}}}
	if err := u.Start(m); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, u)
	st := u.State()
	if st == nil || st.Phase != PhaseReady || st.Progress != 100 || !st.Security || st.To != "0.14.0-s1" {
		t.Fatalf("state %+v", st)
	}
	if ctrl.gotHash != testHash {
		t.Errorf("hash not passed: %q", ctrl.gotHash)
	}
	if err := u.Restart(context.Background()); err != nil || !ctrl.restarted {
		t.Fatalf("restart: %v %v", err, ctrl.restarted)
	}
	if u.State().Phase != PhaseRestarting {
		t.Fatal("phase after restart")
	}

	// A signing root on the device: the signature decides, no hash.
	ctrl2 := &fakeCtrl{installErr: errors.New("bundle verification failed")}
	u2 := &Updater{DataDir: t.TempDir(), Info: Info{Target: "pc", Version: "0.14.0"}, Ctrl: ctrl2, SignedOnly: true}
	if err := u2.Start(m); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, u2)
	if ctrl2.gotHash != "" {
		t.Errorf("hash passed with a signing root: %q", ctrl2.gotHash)
	}
	if st := u2.State(); st.Phase != PhaseFailed || !strings.Contains(st.Error, "verification") {
		t.Fatalf("failed install state %+v", st)
	}
	if err := u2.Restart(context.Background()); err == nil {
		t.Error("restart without a written update accepted")
	}
}
