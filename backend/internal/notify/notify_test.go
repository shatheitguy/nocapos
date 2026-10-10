package notify

import (
	"context"
	"fmt"
	"testing"
	"time"

	"alfaos/alfad/internal/store"
)

func newService(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st, nil)
}

func TestAddDedupesByKey(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	ch, cancel := s.Subscribe()
	defer cancel()

	n := Notification{Key: "update:v0.3.1", Kind: KindUpdate, Title: "NoCapOS 0.3.1 is available",
		Icon: "settings", Action: &Action{App: "settings", Props: map[string]any{"section": "update"}}}
	if added, err := s.Add(ctx, n); err != nil || !added {
		t.Fatalf("first add: %v %v", added, err)
	}
	if added, err := s.Add(ctx, n); err != nil || added {
		t.Fatalf("second add with the same key: added=%v err=%v, want skipped", added, err)
	}
	e := <-ch
	if e.Type != "new" || e.Item == nil || e.Item.Title != n.Title || e.Unread != 1 {
		t.Fatalf("event = %+v", e)
	}
	select {
	case e := <-ch:
		t.Fatalf("duplicate published: %+v", e)
	default:
	}

	items, unread, err := s.List(ctx, 0)
	if err != nil || len(items) != 1 || unread != 1 {
		t.Fatalf("list: %d items, %d unread, %v", len(items), unread, err)
	}
	got := items[0]
	if got.Level != "info" || got.Icon != "settings" || got.Action == nil || got.Action.Props["section"] != "update" || got.ReadAt != nil {
		t.Fatalf("stored notification = %+v", got)
	}

	// Deleting hides it, but the key is remembered: a later check doesn't bring it back.
	if err := s.Delete(ctx, got.ID); err != nil {
		t.Fatal(err)
	}
	if added, _ := s.Add(ctx, n); added {
		t.Fatal("a deleted notification came back")
	}
	if err := s.Delete(ctx, got.ID); err != store.ErrNotFound {
		t.Fatalf("deleting twice: %v, want ErrNotFound", err)
	}
	if items, unread, _ := s.List(ctx, 0); len(items) != 0 || unread != 0 {
		t.Fatalf("after delete: %d items, %d unread", len(items), unread)
	}
}

func TestReadAndClear(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	for i := range 3 {
		if _, err := s.Add(ctx, Notification{Key: fmt.Sprint("k", i), Kind: KindTest, Title: "t"}); err != nil {
			t.Fatal(err)
		}
	}
	items, _, _ := s.List(ctx, 0)
	if err := s.MarkRead(ctx, []int64{items[0].ID}, false); err != nil {
		t.Fatal(err)
	}
	if _, unread, _ := s.List(ctx, 0); unread != 2 {
		t.Fatalf("unread after one read = %d, want 2", unread)
	}
	if err := s.MarkRead(ctx, nil, true); err != nil {
		t.Fatal(err)
	}
	if _, unread, _ := s.List(ctx, 0); unread != 0 {
		t.Fatalf("unread after read all = %d", unread)
	}
	if err := s.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if items, _, _ := s.List(ctx, 0); len(items) != 0 {
		t.Fatalf("%d items after clear", len(items))
	}
	if added, _ := s.Add(ctx, Notification{Key: "k1", Kind: KindTest, Title: "t"}); added {
		t.Fatal("a cleared notification came back")
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	for i := range keepNewest + 20 {
		if _, err := s.Add(ctx, Notification{Key: fmt.Sprint("n", i), Kind: KindTest, Title: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	items, unread, err := s.List(ctx, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != keepNewest || unread != keepNewest {
		t.Fatalf("kept %d (unread %d), want %d", len(items), unread, keepNewest)
	}
	if items[0].Title != fmt.Sprint(keepNewest+19) || items[len(items)-1].Title != "20" {
		t.Fatalf("kept %s … %s, want the newest", items[0].Title, items[len(items)-1].Title)
	}
}

func TestSettingsSkipKinds(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	if set, err := s.Settings(ctx); err != nil || set != DefaultSettings() {
		t.Fatalf("default settings = %+v %v", set, err)
	}
	set := DefaultSettings()
	set.AppUpdates, set.Storage = false, false
	if err := s.SetSettings(ctx, set); err != nil {
		t.Fatal(err)
	}
	if added, _ := s.Add(ctx, Notification{Key: "appupdate:jellyfin:10.11.0", Kind: KindAppUpdate, Title: "x"}); added {
		t.Fatal("a switched-off kind was stored")
	}
	if added, _ := s.Add(ctx, Notification{Key: "storage:pool:tank:DEGRADED", Kind: KindStorage, Title: "x"}); added {
		t.Fatal("a switched-off kind was stored")
	}
	for _, k := range []string{KindUpdate, KindAppError, KindBackup, KindTest} {
		if added, err := s.Add(ctx, Notification{Key: "on:" + k, Kind: k, Title: "x"}); !added || err != nil {
			t.Fatalf("kind %s: added=%v err=%v", k, added, err)
		}
	}
	// Skipped kinds aren't stored at all, so turning them back on notifies later checks.
	if err := s.SetSettings(ctx, DefaultSettings()); err != nil {
		t.Fatal(err)
	}
	if added, _ := s.Add(ctx, Notification{Key: "appupdate:jellyfin:10.11.0", Kind: KindAppUpdate, Title: "x"}); !added {
		t.Fatal("re-enabled kind not stored")
	}
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }
func watch(c *clock) *CrashWatch     { w := NewCrashWatch(); w.now = c.now; return w }
func die(id, app, code string) ContainerExit {
	return ContainerExit{Action: "die", ID: id, Name: "/nocap-" + app + "-web", App: app, ExitCode: code}
}

const cid = "0123456789abcdef0123456789abcdef"

func TestCrashRateLimit(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	w := watch(c)
	cr, ok := w.Observe(die(cid, "jellyfin", "1"))
	if !ok || cr.App != "jellyfin" || cr.ExitCode != "1" || cr.Key == "" {
		t.Fatalf("first crash: %+v %v", cr, ok)
	}
	// A crash loop: restarts every 30s for 9 minutes stay quiet.
	for range 18 {
		c.add(30 * time.Second)
		if _, ok := w.Observe(die(cid, "jellyfin", "1")); ok {
			t.Fatalf("crash loop notified again after %v", c.t.Sub(time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)))
		}
	}
	// Another app is limited separately.
	if _, ok := w.Observe(die("fedcba9876543210", "immich", "2")); !ok {
		t.Fatal("other app's crash was rate-limited")
	}
	c.add(2 * time.Minute) // > 10 minutes since the first
	cr2, ok := w.Observe(die(cid, "jellyfin", "1"))
	if !ok || cr2.Key == cr.Key {
		t.Fatalf("after 10 minutes: %+v %v (first key %s)", cr2, ok, cr.Key)
	}
}

func TestCrashIgnoresIntendedExits(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	w := watch(c)
	for _, code := range []string{"0", "143", ""} {
		if _, ok := w.Observe(die(cid, "jellyfin", code)); ok {
			t.Fatalf("exit code %q notified", code)
		}
	}
	if _, ok := w.Observe(ContainerExit{Action: "die", ID: cid, Name: "/random", ExitCode: "1"}); ok {
		t.Fatal("a container outside the App Store notified")
	}
	if _, ok := w.Observe(ContainerExit{Action: "stop", ID: cid, App: "jellyfin", ExitCode: "1"}); ok {
		t.Fatal("a non-die event notified")
	}

	// Stopped, restarted or recreated by NoCapOS (by id, by name or the whole app).
	w.Expect(cid[:12])
	if _, ok := w.Observe(die(cid, "jellyfin", "137")); ok {
		t.Fatal("expected container (by id) notified")
	}
	w.Expect("/nocap-immich-web")
	if _, ok := w.Observe(die("aaaaaaaaaaaaaaaa", "immich", "137")); ok {
		t.Fatal("expected container (by name) notified")
	}
	w.Expect("app:nextcloud")
	if _, ok := w.Observe(die("bbbbbbbbbbbbbbbb", "nextcloud", "1")); ok {
		t.Fatal("expected app notified")
	}
	// docker stop from anywhere: kill comes first.
	w.Observe(ContainerExit{Action: "kill", ID: "cccccccccccccccc", App: "gitea"})
	if _, ok := w.Observe(die("cccccccccccccccc", "gitea", "137")); ok {
		t.Fatal("killed container notified")
	}
	// An app being installed or updated by the App Store.
	w.Busy = func(app string) bool { return app == "vaultwarden" }
	if _, ok := w.Observe(die("dddddddddddddddd", "vaultwarden", "1")); ok {
		t.Fatal("busy app notified")
	}

	// The marks wear off after ~2 minutes; then a real crash notifies.
	c.add(ExpectFor + time.Second)
	if _, ok := w.Observe(die(cid, "jellyfin", "137")); !ok {
		t.Fatal("crash after the expectation expired was ignored")
	}
	if _, ok := w.Observe(die("bbbbbbbbbbbbbbbb", "nextcloud", "1")); !ok {
		t.Fatal("app crash after the expectation expired was ignored")
	}
	if _, ok := w.Observe(die("cccccccccccccccc", "gitea", "139")); !ok {
		t.Fatal("crash long after a kill was ignored")
	}
}
