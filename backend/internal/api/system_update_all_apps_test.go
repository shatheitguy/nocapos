package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"alfaos/alfad/internal/config"
	"alfaos/alfad/internal/store"
)

// Update everything with App Store apps: they update first, then the run
// ends (no system packages or NoCapOS here); a failed app ends it as failed.
func TestUpdateAllAppsFirst(t *testing.T) {
	ctx := context.Background()
	s, st := notifyServer(t)
	s.Config = &config.Config{DataDir: t.TempDir()}
	s.Version = "0.14.7"
	_ = st.SaveInstalledApp(ctx, &store.InstalledApp{ID: "jellyfin", Version: "10.10.0", Secrets: map[string]string{}})
	_ = st.SaveInstalledApp(ctx, &store.InstalledApp{ID: "immich", Version: "1.2.0", Secrets: map[string]string{}})

	// Only installed apps that have an update are kept.
	if got := s.appsToUpdate(ctx, []string{"immich", "jellyfin", "jellyfin", "nope"}); len(got) != 1 || got[0] != "jellyfin" {
		t.Fatalf("apps to update = %v", got)
	}

	r := httptest.NewRequest("POST", "/api/v1/system/update/all", nil)
	run := &updateAll{Started: time.Now().UTC(), Phase: updateAllApps, Apps: []string{"jellyfin"}}
	s.writeUpdateAll(run)
	updateAllAppsRunning.Store(true)
	if st := s.updateAllStatus(); !st.Running || st.Run.Phase != updateAllApps {
		t.Fatalf("while the apps update: %+v", st.Run)
	}
	s.updateAllAppsThenRest(run.Apps, nil, "", r, "")
	got := s.readUpdateAll()
	if got == nil || got.Phase != updateAllDone || !got.OK || got.AppsDone != 1 {
		t.Fatalf("after the apps: %+v", got)
	}
	if row, _ := st.InstalledApp(ctx, "jellyfin"); row == nil || row.Version != "10.11.0" {
		t.Fatalf("jellyfin wasn't updated: %+v", row)
	}
}

func TestUpdateAllAppsLostOnRestart(t *testing.T) {
	s := newUpdateServer(t, "0.14.7")
	updateAllAppsRunning.Store(false)
	s.writeUpdateAll(&updateAll{Started: time.Now().UTC(), Phase: updateAllApps, Apps: []string{"jellyfin"}})
	st := s.updateAllStatus()
	if st.Running || st.Run.Phase != updateAllDone || st.Run.OK {
		t.Fatalf("an apps part lost to a restart should end as failed: %+v", st.Run)
	}
}

func TestUpdateAllFailedAppsNamed(t *testing.T) {
	u := &updateAll{AppsFailed: []string{"Immich"}}
	u.finish(true, "")
	if u.OK || !strings.Contains(u.Error, "Immich") {
		t.Fatalf("finish = %+v", u)
	}
}
