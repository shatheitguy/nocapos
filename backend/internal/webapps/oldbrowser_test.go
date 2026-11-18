package webapps

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeHost struct {
	calls     []string
	installed string // "apt", "dnf" or ""
	failApt   bool
	removed   []string
}

func (f *fakeHost) run(_ context.Context, name string, args ...string) bool {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	switch {
	case name == "sh":
		return f.installed == "apt"
	case name == "rpm":
		return f.installed == "dnf"
	case name == "apt-get":
		return !f.failApt
	}
	return true
}

func (f *fakeHost) ran(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func newOldBrowserHost(t *testing.T, f *fakeHost) (oldBrowserHost, string) {
	t.Helper()
	root := t.TempDir()
	data := filepath.Join(root, "var", "lib", "nocapos")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	return oldBrowserHost{
		dataDir: data, root: root, run: f.run,
		removeContainer: func(_ context.Context, name string) error {
			f.removed = append(f.removed, name)
			return nil
		},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, root
}

func put(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOldBrowserCleanupRemovesWhatNoCapOSMade(t *testing.T) {
	f := &fakeHost{installed: "apt"}
	h, root := newOldBrowserHost(t, f)
	put(t, filepath.Join(h.dataDir, "brave-run.sh"), "#!/bin/sh\n")
	put(t, filepath.Join(h.dataDir, "brave.log"), "log\n")
	put(t, filepath.Join(h.dataDir, "brave-profile", "Default", "Bookmarks"), "{}")
	put(t, h.path(oldBrowserAptList), "deb [signed-by="+oldBrowserKeyring+"] https://"+oldBrowserAptURL+"/ stable main\n")
	put(t, h.path(oldBrowserKeyring), "key")
	put(t, h.path(oldBrowserPolicy), "{}")
	put(t, h.path(oldBrowserLauncher), "[Desktop Entry]\n")
	mine := filepath.Join(root, "home", "alex", "Documents", "notes.txt")
	put(t, mine, "mine")

	if !h.cleanup(context.Background()) {
		t.Fatal("cleanup not finished")
	}
	if !f.ran("apt-get remove -y -q brave-browser") {
		t.Fatalf("package not removed: %v", f.calls)
	}
	if !f.ran("pkill -f -- " + filepath.Join(h.dataDir, "brave-run.sh")) {
		t.Fatalf("stream not stopped: %v", f.calls)
	}
	for _, p := range []string{h.path(oldBrowserAptList), h.path(oldBrowserKeyring), h.path(oldBrowserPolicy), h.path(oldBrowserLauncher),
		filepath.Join(h.dataDir, "brave-run.sh"), filepath.Join(h.dataDir, "brave.log")} {
		if exists(p) {
			t.Errorf("%s still there", p)
		}
	}
	// Profiles and people's files stay.
	for _, p := range []string{mine, filepath.Join(h.dataDir, "brave-profile", "Default", "Bookmarks")} {
		if !exists(p) {
			t.Errorf("%s was deleted", p)
		}
	}
	if len(f.removed) != len(oldBrowserContainers) {
		t.Errorf("containers: %v", f.removed)
	}

	// Once only.
	f.calls = nil
	h.cleanup(context.Background())
	if len(f.calls) != 0 {
		t.Fatalf("ran again: %v", f.calls)
	}
}

func TestOldBrowserCleanupLeavesSomeoneElsesBrowser(t *testing.T) {
	// Nothing in the data folder: NoCapOS never set it up.
	f := &fakeHost{installed: "apt"}
	h, _ := newOldBrowserHost(t, f)
	put(t, h.path(oldBrowserAptList), "deb https://"+oldBrowserAptURL+"/ stable main\n")
	h.cleanup(context.Background())
	if f.ran("apt-get") || f.ran("pkill") || !exists(h.path(oldBrowserAptList)) {
		t.Fatalf("touched a browser NoCapOS didn't install: %v", f.calls)
	}

	// NoCapOS set it up, but a person has a profile of their own in it.
	f = &fakeHost{installed: "dnf"}
	h, root := newOldBrowserHost(t, f)
	put(t, filepath.Join(h.dataDir, "brave.log"), "log\n")
	put(t, filepath.Join(root, "home", "sam", ".config", "BraveSoftware", "Brave-Browser", "Local State"), "{}")
	put(t, h.path(oldBrowserRpmRepo), "baseurl=https://"+oldBrowserRpmURL+"\n")
	h.cleanup(context.Background())
	if f.ran("dnf remove") || !exists(h.path(oldBrowserRpmRepo)) {
		t.Fatalf("removed a browser in use: %v", f.calls)
	}
	if exists(filepath.Join(h.dataDir, "brave.log")) {
		t.Fatal("NoCapOS's own log should go")
	}
}

func TestOldBrowserCleanupRetriesWhenRemovalFails(t *testing.T) {
	f := &fakeHost{installed: "apt", failApt: true}
	h, _ := newOldBrowserHost(t, f)
	put(t, filepath.Join(h.dataDir, "brave-run.sh"), "x")
	if h.cleanup(context.Background()) {
		t.Fatal("should report unfinished")
	}
	if exists(filepath.Join(h.dataDir, oldBrowserDone)) {
		t.Fatal("marked done after a failure")
	}
}
