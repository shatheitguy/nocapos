package cloudimport

import (
	"strings"
	"testing"
)

func TestMountArgs(t *testing.T) {
	got := strings.Join(mountArgs("/usr/bin/rclone", "/data/secrets/rclone/mount-7.conf", "/data/secrets/rclone", 7, true), " ")
	want := "systemd-run --unit=nocapos-cloud-7 --collect --property=Type=notify --setenv=HOME=/data/secrets/rclone " +
		"/usr/bin/rclone --config /data/secrets/rclone/mount-7.conf mount src: /srv/nocapos/cloud/7 " +
		"--vfs-cache-mode writes --allow-other --dir-cache-time 5m --umask 002 --read-only"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if strings.Join(unmountArgs(7), " ") != "systemctl stop nocapos-cloud-7.service" {
		t.Fatal(unmountArgs(7))
	}
	if mountRoot(12) != "cloud:12" || mountPath(12) != "/srv/nocapos/cloud/12" {
		t.Fatal("paths")
	}
}
