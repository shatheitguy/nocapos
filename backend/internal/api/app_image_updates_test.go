package api

import (
	"context"
	"testing"

	"alfaos/alfad/internal/imageupdates"
	"alfaos/alfad/internal/store"
)

func TestAppImageUpdates(t *testing.T) {
	ctx := context.Background()
	s, st := notifyServer(t)
	_ = st.SaveInstalledApp(ctx, &store.InstalledApp{ID: "immich", Version: "1.2.0", Secrets: map[string]string{}})

	// The same catalog version, but a newer image: an update in the App Store.
	flagged := map[string]bool{"immich": true}
	s.AppStore.ImageUpdates = func(id string) bool { return flagged[id] }
	status, err := s.AppStore.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if in := status["immich"]; in == nil || !in.UpdateAvailable || !in.ImageUpdate {
		t.Fatalf("immich = %+v", in)
	}

	// One notification per new image, and none once they're turned off.
	ups := []imageupdates.Update{{Ref: "ghcr.io/immich-app/immich-server:release", Digest: "sha256:0123456789abcdef0123"}}
	s.notifyAppImage(ctx, "immich", ups)
	s.notifyAppImage(ctx, "immich", ups)
	if got := titles(t, s); len(got) != 1 || got[0] != "An update for Immich is available" {
		t.Fatalf("notifications = %q", got)
	}
	items, _, _ := s.notify.List(ctx, 0)
	if items[0].Key != "appimage:immich:0123456789ab" || items[0].Action == nil || items[0].Action.App != "appcenter" {
		t.Fatalf("notification %+v", items[0])
	}
	if !s.appImageNotices(ctx) {
		t.Fatal("notices are on by default")
	}
	_ = st.SetSetting(ctx, appImageNoticesKey, "0")
	s.notifyAppImage(ctx, "immich", []imageupdates.Update{{Ref: "x:1", Digest: "sha256:ffffffffffffffff"}})
	if got := titles(t, s); len(got) != 1 {
		t.Fatalf("notified while turned off: %q", got)
	}
}
