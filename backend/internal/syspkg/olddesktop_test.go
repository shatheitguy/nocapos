package syspkg

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const installerHistory = `
Start-Date: 2026-10-01  10:00:00
Commandline: apt-get install -y -q --no-install-recommends -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold gdm3 gnome-session gnome-shell nautilus loupe
Install: gdm3:amd64 (48.0-2), gnome-session:amd64 (48.0-1), gnome-shell:amd64 (48.7-0), mutter-common:amd64 (48.4-1, automatic), loupe:amd64 (48.1-1)
End-Date: 2026-10-01  10:05:00

Start-Date: 2026-10-02  10:00:00
Commandline: apt install gnome-text-editor
Install: gnome-text-editor:amd64 (48.3-1)
End-Date: 2026-10-02  10:01:00
`

func TestDecideOldDesktop(t *testing.T) {
	inst := map[string]bool{"gdm3": true, "gnome-session": true, "gnome-shell": true, "nautilus": true, "loupe": true, "gnome-text-editor": true, "x11vnc": true}
	manual := map[string]bool{"gdm3": true, "gnome-session": true, "gnome-shell": true, "nautilus": true, "loupe": true, "gnome-text-editor": true}
	// Installed by hand before NoCapOS (nautilus was already there: not in
	// the installer's Install line), by the person later (gnome-text-editor),
	// or only as a dependency (x11vnc, automatic): all stay.
	p := DecideOldDesktop(OldDesktopFacts{Installed: inst, Manual: manual, History: installerHistory})
	if !reflect.DeepEqual(p.Remove, []string{"gdm3", "gnome-session", "gnome-shell", "loupe"}) {
		t.Errorf("remove = %v", p.Remove)
	}
	if !reflect.DeepEqual(p.Keep, []string{"nautilus", "gnome-text-editor", "x11vnc"}) {
		t.Errorf("keep = %v", p.Keep)
	}
	// From an earlier NoCapOS image: everything of the list marked manual is NoCapOS's.
	p = DecideOldDesktop(OldDesktopFacts{Installed: inst, Manual: manual, FromImage: true})
	if len(p.Remove) != 6 || !reflect.DeepEqual(p.Keep, []string{"x11vnc"}) {
		t.Errorf("image: %+v", p)
	}
	// No proof at all: keep everything.
	p = DecideOldDesktop(OldDesktopFacts{Installed: inst, Manual: manual})
	if len(p.Remove) != 0 {
		t.Errorf("unsure, yet removing %v", p.Remove)
	}
}

func TestSplitHistoryList(t *testing.T) {
	got := splitHistoryList(" a:amd64 (1.0), b:amd64 (2.0, automatic), c (3)")
	want := []historyItem{{"a", false}, {"b", true}, {"c", false}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

// fakeApt answers the commands OldDesktopSteps runs.
type fakeApt struct {
	auto    map[string]bool
	orphans func(auto map[string]bool) []string
	extra   string // a package the removal simulation would also take
	calls   []string
}

func (f *fakeApt) run(_ context.Context, args ...string) (string, error) {
	line := strings.Join(args, " ")
	f.calls = append(f.calls, line)
	switch {
	case strings.HasPrefix(line, "apt-get -s autoremove"):
		var b strings.Builder
		for _, n := range f.orphans(f.auto) {
			b.WriteString("Remv " + n + " [1.0]\n")
		}
		return b.String(), nil
	case strings.HasPrefix(line, "apt-mark auto"):
		for _, n := range args[2:] {
			f.auto[n] = true
		}
	case strings.HasPrefix(line, "apt-mark manual"):
		for _, n := range args[2:] {
			delete(f.auto, n)
		}
	case strings.HasPrefix(line, "apt-get -s remove"):
		var b strings.Builder
		for _, n := range args[3:] {
			b.WriteString("Remv " + n + " [1.0]\n")
		}
		if f.extra != "" {
			b.WriteString("Remv " + f.extra + " [1.0]\n")
		}
		return b.String(), nil
	case strings.HasPrefix(line, "dpkg-query"):
		// After a removal only what's still needed is installed.
		if strings.Contains(strings.Join(f.calls, "\n"), " -y -q remove ") {
			return "gnome-shell\tii \n", nil
		}
		return "gdm3\tii \ngnome-shell\tii \n", nil
	}
	return "", nil
}

func runSteps(t *testing.T, steps []Step) error {
	t.Helper()
	var out strings.Builder
	for _, s := range steps {
		if err := s.Fn(context.Background(), &out); err != nil {
			return err
		}
	}
	return nil
}

func TestOldDesktopStepsRemoveOnlyNewOrphans(t *testing.T) {
	f := &fakeApt{auto: map[string]bool{}, orphans: func(auto map[string]bool) []string {
		o := []string{"libold1"} // unneeded before: stays
		if auto["gdm3"] {
			o = append(o, "gdm3", "libgdm1")
		}
		// gnome-shell is still needed by something the person installed.
		return o
	}}
	var removed []string
	steps, err := OldDesktopSteps([]string{"gdm3", "gnome-shell"}, f.run, func(r []string) { removed = r })
	if err != nil {
		t.Fatal(err)
	}
	if err := runSteps(t, steps); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(removed, []string{"gdm3", "libgdm1"}) {
		t.Fatalf("removed %v; calls %v", removed, f.calls)
	}
	last := ""
	for _, c := range f.calls {
		if strings.Contains(c, " -y -q remove ") {
			last = c
		}
	}
	if !strings.HasSuffix(last, "remove gdm3 libgdm1") || strings.Contains(last, "libold1") {
		t.Fatalf("remove call = %q", last)
	}
	// What stays is marked as it was.
	if f.auto["gnome-shell"] {
		t.Fatal("gnome-shell left marked automatic")
	}
}

func TestOldDesktopStepsStopOnSurprises(t *testing.T) {
	f := &fakeApt{auto: map[string]bool{}, extra: "someones-app", orphans: func(auto map[string]bool) []string {
		if auto["gdm3"] {
			return []string{"gdm3"}
		}
		return nil
	}}
	steps, _ := OldDesktopSteps([]string{"gdm3"}, f.run, func([]string) { t.Fatal("done with a removal") })
	err := runSteps(t, steps)
	if err == nil || !strings.Contains(err.Error(), "someones-app") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(strings.Join(f.calls, "\n"), " -y -q remove ") || f.auto["gdm3"] {
		t.Fatalf("removed or left marked: %v", f.calls)
	}
	// Something NoCap Desktop needs going unneeded: stop.
	f = &fakeApt{auto: map[string]bool{}, orphans: func(auto map[string]bool) []string {
		if auto["gdm3"] {
			return []string{"gdm3", "labwc"}
		}
		return nil
	}}
	steps, _ = OldDesktopSteps([]string{"gdm3"}, f.run, func([]string) {})
	if err := runSteps(t, steps); err == nil || !strings.Contains(err.Error(), "labwc") {
		t.Fatalf("err = %v", err)
	}
	if _, err := OldDesktopSteps([]string{"labwc"}, f.run, nil); err == nil {
		t.Fatal("asked to remove labwc")
	}
	if _, err := OldDesktopSteps([]string{"bad name;"}, f.run, nil); err == nil {
		t.Fatal(errors.New("bad name accepted"))
	}
}
