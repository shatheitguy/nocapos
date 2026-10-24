package api

import (
	"strings"
	"testing"
	"time"

	"alfaos/alfad/internal/notify"
	"alfaos/alfad/internal/removable"
	"alfaos/alfad/internal/storage"
)

func TestDriveNotifications(t *testing.T) {
	at := time.Unix(1700000000, 0)
	in := driveNotification(removable.Drive{Device: "/dev/sdb1", Name: "HOLIDAY", Mount: "/media/nocapos/HOLIDAY"}, true, at)
	if in.Kind != notify.KindDevice || in.Title != "HOLIDAY connected — Open in Files" || in.Quiet ||
		in.Action == nil || in.Action.App != "files" || in.Action.Props["path"] != "/media/nocapos/HOLIDAY" || in.Action.Props["root"] != "system" {
		t.Errorf("plugged in: %+v", in)
	}
	out := driveNotification(removable.Drive{Device: "/dev/sdb1", Name: "HOLIDAY"}, false, at)
	if !out.Quiet || !strings.Contains(out.Title, "removed") || out.Key == in.Key {
		t.Errorf("removed: %+v", out)
	}
	bad := driveNotification(removable.Drive{Device: "/dev/sdc1", Name: "WIN", Error: "the drive's filesystem looks damaged"}, true, at)
	if bad.Level != "warning" || !strings.Contains(bad.Body, "damaged") {
		t.Errorf("can't open: %+v", bad)
	}
}

func TestSelfTestNotifications(t *testing.T) {
	at := time.Unix(1700000000, 0)
	ok := testNotification(storage.TestResult{Disk: "sdb", Model: "WD Red", Kind: "short", Status: "Completed without error", Passed: true}, at)
	if ok.Level != "success" || !strings.Contains(ok.Title, "Short disk test passed") || ok.Kind != notify.KindStorage {
		t.Errorf("passed: %+v", ok)
	}
	failed := testNotification(storage.TestResult{Disk: "sdb", Model: "WD Red", Kind: "long", Status: "Completed: read failure"}, at)
	if failed.Level != "error" || !strings.Contains(failed.Title, "Long disk test failed") || !strings.Contains(failed.Body, "read failure") {
		t.Errorf("failed: %+v", failed)
	}
	stopped := testNotification(storage.TestResult{Disk: "sdb", Model: "WD Red", Kind: "long", Status: "Aborted by host", Aborted: true}, at)
	if stopped.Level != "warning" {
		t.Errorf("aborted: %+v", stopped)
	}
}
