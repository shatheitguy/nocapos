package nativeapps

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestParseUpdates(t *testing.T) {
	out := "Application ID\tName\tVersion\n" +
		"org.gimp.GIMP\tGNU Image Manipulation Program\t3.0.4\n" +
		"org.videolan.VLC\tVLC\t\n" +
		"bogus line\n" +
		"org.gimp.GIMP\tdup\t9\n"
	installed := []App{{ID: "org.gimp.GIMP", Name: "GIMP", Version: "3.0.2"}, {ID: "org.videolan.VLC", Name: "VLC", Version: "3.0.21"}}
	ups := ParseUpdates(out, installed)
	if len(ups) != 2 {
		t.Fatalf("ups = %+v", ups)
	}
	if u := ups[0]; u.ID != "org.gimp.GIMP" || u.From != "3.0.2" || u.To != "3.0.4" || u.Name != "GNU Image Manipulation Program" {
		t.Errorf("gimp = %+v", u)
	}
	if u := ups[1]; u.From != "3.0.21" || u.To != "" {
		t.Errorf("vlc = %+v", u)
	}
	// A terminal-formatted listing (runs of spaces) parses the same.
	if ups := ParseUpdates("org.gimp.GIMP   GIMP   3.0.4", nil); len(ups) != 1 || ups[0].To != "3.0.4" || ups[0].From != "" {
		t.Errorf("spaces: %+v", ups)
	}
	if ups := ParseUpdates("", nil); ups == nil || len(ups) != 0 {
		t.Error("no updates should be an empty list")
	}
}

func TestUpdateArgs(t *testing.T) {
	if got := strings.Join(UpdateArgs("org.gimp.GIMP"), " "); got != "flatpak update -y --noninteractive --system org.gimp.GIMP" {
		t.Errorf("args = %s", got)
	}
	if got := strings.Join(UpdatesArgs(), " "); !strings.Contains(got, "remote-ls --updates") {
		t.Errorf("args = %s", got)
	}
}

func TestUpdateJobs(t *testing.T) {
	s := &Service{
		Jobs:       NewJobs(),
		Native:     func() bool { return true },
		HasFlatpak: func() bool { return true },
		Run: func(_ context.Context, args ...string) (string, error) {
			if strings.Contains(strings.Join(args, " "), "--updates") {
				return "org.gimp.GIMP\tGIMP\t3.0.4\norg.videolan.VLC\tVLC\t3.1\n", nil
			}
			return "org.gimp.GIMP\tGIMP\t3.0.2\n", nil
		},
	}
	var ran []string
	s.Jobs.Exec = func(_ context.Context, args []string, out io.Writer) error {
		ran = append(ran, strings.Join(args, " "))
		_, _ = io.WriteString(out, "Updating 1/1… 100%\n")
		return nil
	}
	if _, err := s.Update(context.Background(), "bad id; reboot", "", nil); !errors.Is(err, ErrBadID) {
		t.Fatalf("bad id: %v", err)
	}
	done := make(chan Job, 1)
	j, err := s.Update(context.Background(), AllApps, "", func(j Job) { done <- j })
	if err != nil {
		t.Fatal(err)
	}
	if j.Action != "update" || j.Name != "All system apps" {
		t.Errorf("job = %+v", j)
	}
	select {
	case r := <-done:
		if !r.OK {
			t.Fatal(r.Error)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("job never finished")
	}
	if len(ran) != 2 || !strings.HasSuffix(ran[0], "org.gimp.GIMP") || !strings.HasSuffix(ran[1], "org.videolan.VLC") {
		t.Errorf("ran = %v", ran)
	}
}
