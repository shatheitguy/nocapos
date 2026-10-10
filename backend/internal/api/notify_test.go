package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"alfaos/alfad/internal/appstore"
	"alfaos/alfad/internal/backup"
	"alfaos/alfad/internal/docker"
	"alfaos/alfad/internal/notify"
	"alfaos/alfad/internal/storage"
	"alfaos/alfad/internal/store"
)

// nullEngine is an App Store engine with no containers (Docker isn't needed
// to see that an installed app has a newer catalog version).
type nullEngine struct{}

func (nullEngine) PullImage(context.Context, string, string, func(docker.PullProgress)) error {
	return nil
}
func (nullEngine) CreateNetwork(context.Context, string, map[string]string) error { return nil }
func (nullEngine) RemoveNetwork(context.Context, string) error                    { return nil }
func (nullEngine) RemoveVolume(context.Context, string) error                     { return nil }
func (nullEngine) CreateContainer(context.Context, string, docker.CreateConfig) (string, error) {
	return "", nil
}
func (nullEngine) StartContainer(context.Context, string) error                  { return nil }
func (nullEngine) StopContainer(context.Context, string, time.Duration) error    { return nil }
func (nullEngine) RestartContainer(context.Context, string, time.Duration) error { return nil }
func (nullEngine) RemoveContainer(context.Context, string) error                 { return nil }
func (nullEngine) ConnectNetwork(context.Context, docker.NetLink, string) error  { return nil }
func (nullEngine) ContainersByLabel(context.Context, string, string) ([]docker.Container, error) {
	return nil, errors.New("docker is not running")
}

func notifyServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	catalog := []appstore.App{{ID: "jellyfin", Name: "Jellyfin", Version: "10.11.0"}, {ID: "immich", Name: "Immich", Version: "1.2.0"}}
	s := &Server{Deps: Deps{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), AppStore: appstore.NewManager(catalog, nullEngine{}, st)}}
	s.notify = notify.New(st, s.Log)
	s.crashes = notify.NewCrashWatch()
	return s, st
}

func titles(t *testing.T, s *Server) []string {
	t.Helper()
	items, _, err := s.notify.List(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, n := range items {
		out = append(out, n.Title)
	}
	return out
}

func TestAppUpdateNotifiesOncePerVersion(t *testing.T) {
	ctx := context.Background()
	s, st := notifyServer(t)
	_ = st.SaveInstalledApp(ctx, &store.InstalledApp{ID: "jellyfin", Version: "10.10.0", Secrets: map[string]string{}})
	_ = st.SaveInstalledApp(ctx, &store.InstalledApp{ID: "immich", Version: "1.2.0", Secrets: map[string]string{}})
	s.checkAppUpdates(ctx)
	s.checkAppUpdates(ctx) // the 6-hourly check again, or after a restart
	got := titles(t, s)
	if len(got) != 1 || got[0] != "Jellyfin 10.11.0 is available" {
		t.Fatalf("notifications = %q", got)
	}
	items, _, _ := s.notify.List(ctx, 0)
	if a := items[0].Action; a == nil || a.App != "appcenter" || a.Props["app"] != "jellyfin" || items[0].Icon != "store:jellyfin" {
		t.Fatalf("action = %+v icon %q", a, items[0].Icon)
	}
}

func TestReleaseStorageBackupAndJobNotifications(t *testing.T) {
	ctx := context.Background()
	s, _ := notifyServer(t)

	info := updateInfo{Current: "0.3.0", Latest: "v0.3.1", Available: true}
	s.add(ctx, releaseNotification(info))
	s.add(ctx, releaseNotification(info))

	alert := storage.Alert{Key: "pool:tank:DEGRADED", Level: "warning", Title: "Pool tank is degraded", Body: "…"}
	s.add(ctx, storageNotification(alert))
	s.add(ctx, storageNotification(alert))

	started := time.Now()
	s.notifyBackup(ctx, backup.Result{PlanID: 3, Plan: "Photos", Started: started, Status: "failed", Error: "destination is full"})
	s.notifyBackup(ctx, backup.Result{PlanID: 3, Plan: "Photos", Started: started, Status: "failed", Error: "destination is full"})
	s.notifyBackup(ctx, backup.Result{PlanID: 3, Plan: "Photos", Started: started.Add(time.Hour), Status: "ok"})
	s.notifyBackup(ctx, backup.Result{PlanID: 3, Plan: "Photos", Started: started.Add(2 * time.Hour), Status: "canceled"})

	s.notifyAppJob("immich", "install", errors.New("port 2283 is in use"))
	s.notifyAppJob("immich", "install", nil)
	s.notifyAppJob("immich", "start", errors.New("nope"))

	want := []string{"Couldn't install Immich", "Backup “Photos” failed", "Pool tank is degraded", "NoCapOS 0.3.1 is available"}
	got := titles(t, s)
	if len(got) != len(want) {
		t.Fatalf("notifications = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("notifications = %q, want %q", got, want)
		}
	}
	items, _, _ := s.notify.List(ctx, 0)
	if items[2].Level != "warning" || items[2].Action.App != "storage" || items[1].Level != "error" || items[1].Action.App != "backups" {
		t.Fatalf("levels/actions: %+v %+v", items[2], items[1])
	}
	if a := items[3].Action; a.App != "settings" || a.Props["section"] != "update" {
		t.Fatalf("update action = %+v", a)
	}
}

func TestCrashNotificationFlow(t *testing.T) {
	ctx := context.Background()
	s, _ := notifyServer(t)
	ev := notify.ContainerExit{Action: "die", ID: "0123456789abcdef", Name: "/nocap-jellyfin-server", App: "jellyfin", ExitCode: "1", Time: time.Now()}
	if c, ok := s.crashes.Observe(ev); ok {
		s.add(ctx, crashNotification(c, s.appName(c.App), ev.Name))
	}
	if c, ok := s.crashes.Observe(ev); ok { // crash loop: rate-limited
		s.add(ctx, crashNotification(c, s.appName(c.App), ev.Name))
	}
	got := titles(t, s)
	if len(got) != 1 || got[0] != "Jellyfin stopped unexpectedly" {
		t.Fatalf("notifications = %q", got)
	}
}
