package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/store"
)

// plainBox "seals" by prefixing, which is enough to test the plumbing.
type plainBox struct{}

func (plainBox) Seal(s string) []byte { return []byte("sealed:" + s) }
func (plainBox) Open(b []byte) (string, error) {
	s, ok := strings.CutPrefix(string(b), "sealed:")
	if !ok {
		return "", errors.New("not sealed")
	}
	return s, nil
}

func TestNextRun(t *testing.T) {
	loc := time.Local
	last := time.Date(2026, 10, 9, 14, 20, 0, 0, loc) // a Friday
	cases := []struct {
		s    Schedule
		want time.Time
	}{
		{Schedule{Every: "hour"}, time.Date(2026, 10, 9, 15, 0, 0, 0, loc)},
		{Schedule{Every: "day", At: "02:30"}, time.Date(2026, 10, 10, 2, 30, 0, 0, loc)},
		{Schedule{Every: "day", At: "18:00"}, time.Date(2026, 10, 9, 18, 0, 0, 0, loc)},
		{Schedule{Every: "week", At: "03:00", Weekday: 0}, time.Date(2026, 10, 11, 3, 0, 0, 0, loc)},
		{Schedule{Every: "manual"}, time.Time{}},
	}
	for _, c := range cases {
		if got := nextRun(c.s, last, time.Time{}); !got.Equal(c.want) {
			t.Errorf("%+v: next %v, want %v", c.s, got, c.want)
		}
	}
	// Never run: counted from when the plan was made.
	if got := nextRun(Schedule{Every: "day", At: "01:00"}, time.Time{}, last); !got.Equal(time.Date(2026, 10, 10, 1, 0, 0, 0, loc)) {
		t.Errorf("first run %v", got)
	}
}

func TestBesideName(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "report.txt")
	got := besideName(p, "2026-10-09 1504")
	if filepath.Base(got) != "report (restored 2026-10-09 1504).txt" {
		t.Fatalf("got %s", got)
	}
	_ = os.WriteFile(got, nil, 0o644)
	if second := besideName(p, "2026-10-09 1504"); filepath.Base(second) != "report (restored 2026-10-09 1504 2).txt" {
		t.Fatalf("second %s", second)
	}
}

func TestResticError(t *testing.T) {
	if !errors.Is(resticError("Fatal: wrong password or no key found", errors.New("exit 1")), ErrWrongPassword) {
		t.Error("wrong password not recognised")
	}
	if !errors.Is(resticError("Fatal: unable to open config file: stat /x/config: no such file or directory\nIs there a repository at the following location?\n/x", errors.New("exit 1")), ErrNoRepo) {
		t.Error("missing repo not recognised")
	}
	if got := resticError("noise\nFatal: unable to save snapshot: disk full\n", errors.New("x")).Error(); got != "unable to save snapshot: disk full" {
		t.Errorf("got %q", got)
	}
}

func waitJob(t *testing.T, m *Manager, id string) *Job {
	t.Helper()
	for i := 0; i < 600; i++ {
		if j, ok := m.JobByID(id); ok && j.Done {
			return j
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return nil
}

// TestEndToEnd needs restic (on PATH or $ALFA_RESTIC).
func TestEndToEnd(t *testing.T) {
	if os.Getenv("ALFA_RESTIC") == "" {
		if _, err := exec.LookPath("restic"); err != nil {
			t.Skip("restic not installed")
		}
	}
	ctx := context.Background()
	dir := t.TempDir()
	drive := filepath.Join(dir, "drive")
	data := filepath.Join(dir, "data")
	write := func(rel, body string) {
		p := filepath.Join(drive, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Docs/report.txt", "version one")
	write("Docs/sub/notes.md", "notes")
	write("Docs/.recycle-not-really.txt", "kept") // only the real .recycle folder is skipped
	write(".recycle/old.txt", "binned")
	if err := os.MkdirAll(filepath.Join(data, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(data, "secrets", "jwt.key"), []byte("k"), 0o600)

	st, err := store.Open(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fsvc, err := files.New([]files.Root{{ID: "drive", Name: "Drive", Path: drive}})
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(st, plainBox{}, fsvc, data, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if s := m.Status(ctx); !s.Installed {
		t.Fatalf("status: %+v", s)
	}

	repoDir := filepath.Join(dir, "usb", "backups")
	repo, key, err := m.AddRepo(ctx, NewRepo{Name: "USB disk", Kind: "local", Config: RepoConfig{Path: repoDir}})
	if err != nil {
		t.Fatal(err)
	}
	if len(key) < 30 || repo.Location != repoDir {
		t.Fatalf("repo %+v key %q", repo, key)
	}
	// The same place again: refused without the key, wrong key refused, right key connects.
	if _, _, err := m.AddRepo(ctx, NewRepo{Name: "again", Kind: "local", Config: RepoConfig{Path: repoDir}}); err == nil {
		t.Error("adding a destination that already has backups must ask for its key")
	}
	if _, _, err := m.AddRepo(ctx, NewRepo{Name: "again", Kind: "local", Config: RepoConfig{Path: repoDir}, RecoveryKey: "nope"}); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("wrong key: %v", err)
	}
	if _, _, err := m.AddRepo(ctx, NewRepo{Name: "again", Kind: "local", Config: RepoConfig{Path: repoDir}, RecoveryKey: key}); err != nil {
		t.Errorf("connect with key: %v", err)
	}

	planID, err := m.SavePlan(ctx, Plan{RepoID: repo.ID, Name: "Documents", Enabled: true,
		Sources:  []Source{{Root: "drive", Path: "/Docs"}, {Special: SpecialNoCapOS}},
		Schedule: Schedule{Every: "day", At: "02:00"}, Keep: Keep{Daily: 7}})
	if err != nil {
		t.Fatal(err)
	}
	job, err := m.RunPlan(ctx, planID)
	if err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, m, job.ID); j.Error != "" {
		t.Fatalf("backup failed: %s", j.Error)
	}
	plans, _ := m.Plans(ctx)
	if len(plans) != 1 || plans[0].LastStatus != "ok" || plans[0].LastSnapshot == "" || plans[0].NextRun.IsZero() {
		t.Fatalf("plan after run: %+v", plans[0])
	}
	if _, err := os.Stat(filepath.Join(data, "backup-staging", "nocapos")); !os.IsNotExist(err) {
		t.Error("staging folder should be cleaned up")
	}

	snaps, err := m.Snapshots(ctx, planID)
	if err != nil || len(snaps) != 1 {
		t.Fatalf("snapshots %v %v", snaps, err)
	}
	snap := snaps[0].ID
	top, err := m.Browse(ctx, planID, snap, "")
	if err != nil || len(top) != 2 {
		t.Fatalf("top %v %v", top, err)
	}
	labels := map[string]bool{}
	for _, n := range top {
		labels[n.Name] = true
	}
	if !labels["Drive › Docs"] || !labels["NoCapOS settings & accounts"] {
		t.Errorf("top-level names %v", labels)
	}
	docs := filepath.ToSlash(filepath.Join(drive, "Docs"))
	list, err := m.Browse(ctx, planID, snap, docs)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, n := range list {
		names[n.Name] = true
	}
	if !names["report.txt"] || !names["sub"] || len(list) != 3 {
		t.Fatalf("Docs listing: %+v", list)
	}
	if _, err := m.Browse(ctx, planID, snap, "/etc"); err == nil {
		t.Error("browsing outside the backed-up folders must fail")
	}

	var buf bytes.Buffer
	if err := m.Dump(ctx, planID, snap, docs+"/report.txt", &buf); err != nil || buf.String() != "version one" {
		t.Fatalf("dump %q %v", buf.String(), err)
	}

	// Change the file, then restore next to it and in place of it.
	write("Docs/report.txt", "version two")
	rj, err := m.Restore(ctx, RestoreRequest{PlanID: planID, Snapshot: snap, Paths: []string{docs + "/report.txt"}, Mode: "beside"})
	if err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, m, rj.ID); j.Error != "" {
		t.Fatalf("restore beside: %s", j.Error)
	}
	matches, _ := filepath.Glob(filepath.Join(drive, "Docs", "report (restored *).txt"))
	if len(matches) != 1 {
		t.Fatalf("restored copy not found: %v", matches)
	}
	if b, _ := os.ReadFile(matches[0]); string(b) != "version one" {
		t.Errorf("restored copy has %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(drive, "Docs", "report.txt")); string(b) != "version two" {
		t.Errorf("beside must not touch the current file, got %q", b)
	}

	rj, err = m.Restore(ctx, RestoreRequest{PlanID: planID, Snapshot: snap, Paths: []string{docs + "/sub"}, Mode: "replace"})
	if err != nil {
		t.Fatal(err)
	}
	write("Docs/sub/notes.md", "changed")
	if j := waitJob(t, m, rj.ID); j.Error != "" {
		t.Fatalf("restore replace: %s", j.Error)
	}
	if b, _ := os.ReadFile(filepath.Join(drive, "Docs", "sub", "notes.md")); string(b) != "notes" {
		t.Errorf("replace restored %q", b)
	}
	if left, _ := filepath.Glob(filepath.Join(drive, ".nocap-restore-*")); len(left) != 0 {
		t.Errorf("restore left temp folders: %v", left)
	}

	// The destination can't go while a plan uses it.
	if err := m.DeleteRepo(ctx, repo.ID); !errors.Is(err, store.ErrInUse) {
		t.Errorf("delete in-use destination: %v", err)
	}
	if err := m.DeletePlan(ctx, planID); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteRepo(ctx, repo.ID); err != nil {
		t.Errorf("delete destination: %v", err)
	}
}
