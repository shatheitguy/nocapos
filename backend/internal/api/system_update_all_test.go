package api

import (
	"testing"
	"time"
)

func TestUpdateAllStatusNoRun(t *testing.T) {
	s := newUpdateServer(t, "0.11.0")
	st := s.updateAllStatus()
	if st.Run != nil || st.Running {
		t.Fatalf("no run yet: %+v", st)
	}
}

func TestUpdateAllPackagesLostOnRestart(t *testing.T) {
	s := newUpdateServer(t, "0.11.0")
	s.writeUpdateAll(&updateAll{Started: time.Now().UTC(), Phase: updateAllPackages, PkgJob: "gone", Packages: 3})
	st := s.updateAllStatus()
	if st.Running || st.Run == nil || st.Run.Phase != updateAllDone || st.Run.OK || st.Run.Error == "" {
		t.Fatalf("a package part lost to a restart should end as failed: %+v", st.Run)
	}
	if again := s.readUpdateAll(); again == nil || again.Phase != updateAllDone {
		t.Fatalf("the ending should be saved: %+v", again)
	}
}

func TestUpdateAllNoCapOSFinished(t *testing.T) {
	stubUnitAlive(t, false)
	// alfad came back on the new version: the run succeeded.
	s := newUpdateServer(t, "0.12.0")
	s.writeUpdateAll(&updateAll{Started: time.Now().UTC().Add(-time.Minute), Phase: updateAllNoCapOS, From: "0.11.0", Target: "0.12.0", Packages: 2})
	st := s.updateAllStatus()
	if st.Running || st.Run.Phase != updateAllDone || !st.Run.OK {
		t.Fatalf("on the target version the run is done and ok: %+v", st.Run)
	}
}

func TestUpdateAllNoCapOSRunning(t *testing.T) {
	stubUnitAlive(t, true)
	s := newUpdateServer(t, "0.11.0")
	writeLock(t, s, updateLock{Started: time.Now().UTC(), From: "0.11.0", To: "0.12.0", Unit: "nocapos-update-1"})
	s.writeUpdateAll(&updateAll{Started: time.Now().UTC(), Phase: updateAllNoCapOS, From: "0.11.0", Target: "0.12.0"})
	st := s.updateAllStatus()
	if !st.Running || st.NoCapOS == nil || !st.NoCapOS.Running {
		t.Fatalf("still updating NoCapOS: %+v", st)
	}
}
