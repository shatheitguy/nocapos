package appdata

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/docker"
)

// Moving the whole App data folder (e.g. to a fast SSD pool): the apps
// using it stop, everything is copied and checked, the apps are switched to
// the new folder and started again. The old copy stays until the admin
// confirms the move.

// RootState is where App data is and whether an old copy is waiting.
type RootState struct {
	Root    string `json:"root"`
	Default string `json:"default"`
	OldRoot string `json:"old_root,omitempty"`
}

// State reports the App data folder.
func (mv *Mover) State(ctx context.Context) RootState {
	st := RootState{Root: filepath.ToSlash(mv.Root(ctx)), Default: filepath.ToSlash(mv.fallback)}
	if mv.db != nil {
		if v, _ := mv.db.Setting(ctx, KeyOldRoot); v != "" {
			st.OldRoot = filepath.ToSlash(v)
		}
	}
	return st
}

// rootUser is a container (or stack) with folders inside the App data folder.
type rootUser struct {
	id      string
	name    string
	app     string // App Store app id
	project string // NoCapOS stack
}

// MoveRoot moves the App data folder to dst in the background.
func (mv *Mover) MoveRoot(ctx context.Context, dst string) (*Job, error) {
	old := filepath.Clean(mv.Root(ctx))
	clean, err := absClean(dst)
	if err != nil {
		return nil, err
	}
	if err := ValidateRoot(clean, mv.protectedDirs(), mv.DockerRoot(ctx)); err != nil {
		return nil, err
	}
	if files := filepath.Clean(clean); files == old || Under(files, old) || Under(old, files) {
		return nil, errors.New("pick a folder outside the current App data folder")
	}
	if v, _ := mv.db.Setting(ctx, KeyOldRoot); v != "" {
		return nil, errors.New("finish the last move first: remove or keep the old copy at " + v)
	}
	if ok, err := emptyOrMissing(clean); err != nil {
		return nil, err
	} else if !ok {
		return nil, errors.New(dst + " already has files in it; pick an empty folder")
	}
	return mv.start("root", "", func(ctx context.Context) error { return mv.moveRoot(ctx, old, clean) })
}

func (mv *Mover) moveRoot(ctx context.Context, old, dst string) error {
	users, err := mv.rootUsers(ctx, old)
	if err != nil {
		return err
	}
	specs := map[string]docker.Spec{}
	running := map[string]bool{}
	ids := []string{}
	projects := map[string]bool{}
	apps := map[string]bool{}
	for _, u := range users {
		if d, err := mv.engine.InspectContainer(ctx, u.id); err == nil && d.Running {
			running[u.id] = true
		}
		if u.project != "" {
			projects[u.project] = projects[u.project] || running[u.id]
			continue
		}
		s, err := mv.engine.GetSpec(ctx, u.id)
		if err != nil {
			return fmt.Errorf("read %s's settings: %w", u.name, err)
		}
		specs[u.id] = s
		ids = append(ids, u.id)
		if u.app != "" {
			apps[u.app] = true
		}
	}
	mv.set("Stopping apps…", 3)
	for p := range projects {
		if err := mv.Stacks.RunWait(ctx, p, "stop"); err != nil {
			mv.restartStacks(ctx, projects)
			return fmt.Errorf("stop %s: %w", p, err)
		}
	}
	if err := mv.stopAll(ctx, ids, running); err != nil {
		mv.startAll(context.WithoutCancel(ctx), ids, running)
		mv.restartStacks(ctx, projects)
		return err
	}
	undoCopy := func() {
		// The new folder was empty (or missing): empty it again.
		if entries, err := os.ReadDir(dst); err == nil {
			for _, e := range entries {
				_ = os.RemoveAll(filepath.Join(dst, e.Name()))
			}
		}
	}
	mv.set("Copying App data to "+filepath.ToSlash(dst)+"…", 10)
	if _, err := os.Stat(old); err == nil {
		err = mv.Copy(ctx, old, dst)
		if err == nil {
			mv.set("Checking the copy…", 60)
			_, err = Verify(ctx, old, dst)
		}
		if err != nil {
			undoCopy()
			mv.startAll(context.WithoutCancel(ctx), ids, running)
			mv.restartStacks(ctx, projects)
			return err
		}
	} else if err := os.MkdirAll(dst, 0o755); err != nil {
		mv.startAll(context.WithoutCancel(ctx), ids, running)
		mv.restartStacks(ctx, projects)
		return err
	}
	oldSlash, dstSlash := filepath.ToSlash(old), filepath.ToSlash(dst)
	remap := func(m docker.SpecMount) docker.SpecMount {
		if m.Type == "bind" && (m.Source == oldSlash || strings.HasPrefix(m.Source, oldSlash+"/")) {
			m.Source = dstSlash + strings.TrimPrefix(m.Source, oldSlash)
		}
		return m
	}
	mv.set("Switching apps to the new folder…", 75)
	backups := map[string][2]string{} // stack → compose, env before
	fail := func(err error, newIDs map[string]string) error {
		mv.set("Something went wrong; putting the apps back…", 85)
		bg := context.WithoutCancel(ctx)
		mv.putBack(bg, ids, newIDs, specs, running)
		for p, b := range backups {
			_ = mv.Stacks.Save(bg, p, b[0], b[1], false)
			_ = mv.Stacks.RunWait(bg, p, "up")
			if !projects[p] {
				_ = mv.Stacks.RunWait(bg, p, "stop")
			}
		}
		for p := range projects {
			if _, rewritten := backups[p]; !rewritten && projects[p] {
				_ = mv.Stacks.RunWait(bg, p, "start")
			}
		}
		undoCopy()
		return err
	}
	newIDs, err := mv.recreateAll(ctx, ids, specs, remap)
	if err != nil {
		return fail(err, newIDs)
	}
	for p := range projects {
		compose, env, err := mv.Stacks.Files(p)
		if err != nil {
			return fail(err, newIDs)
		}
		if _, err := mv.Stacks.Backup(p, "before-appdata-move-"+time.Now().Format("20060102-150405")); err != nil {
			return fail(err, newIDs)
		}
		backups[p] = [2]string{compose, env}
		if err := mv.Stacks.Save(ctx, p, ReplaceRoot(compose, oldSlash, dstSlash), env, false); err != nil {
			return fail(fmt.Errorf("%s: %w", p, err), newIDs)
		}
	}
	mv.set("Starting apps…", 88)
	if err := mv.startChecked(ctx, newIDs, ids, running); err != nil {
		return fail(err, newIDs)
	}
	for p, wasRunning := range projects {
		if err := mv.Stacks.RunWait(ctx, p, "up"); err != nil {
			return fail(fmt.Errorf("%s: %w", p, err), newIDs)
		}
		if !wasRunning {
			_ = mv.Stacks.RunWait(ctx, p, "stop")
		}
	}
	if err := mv.db.SetSetting(ctx, KeyRoot, dst); err != nil {
		return fail(err, newIDs)
	}
	_ = mv.db.SetSetting(ctx, KeyOldRoot, old)
	if mv.AppStore != nil {
		for app := range apps {
			_ = mv.AppStore.Rebind(ctx, app, remap, true)
		}
	}
	return nil
}

func (mv *Mover) restartStacks(ctx context.Context, projects map[string]bool) {
	for p, wasRunning := range projects {
		if wasRunning {
			_ = mv.Stacks.RunWait(context.WithoutCancel(ctx), p, "start")
		}
	}
}

// rootUsers lists the containers with folders inside root.
func (mv *Mover) rootUsers(ctx context.Context, root string) ([]rootUser, error) {
	list, err := mv.engine.ListContainers(ctx, true)
	if err != nil {
		return nil, err
	}
	rootSlash := filepath.ToSlash(root)
	out := []rootUser{}
	for _, c := range list {
		if c.System {
			continue
		}
		d, err := mv.engine.InspectContainer(ctx, c.ID)
		if err != nil {
			continue
		}
		uses := false
		for _, m := range d.Mounts {
			src := filepath.ToSlash(m.Source)
			if m.Type == "bind" && (src == rootSlash || strings.HasPrefix(src, rootSlash+"/")) {
				uses = true
			}
		}
		if !uses {
			continue
		}
		u := rootUser{id: c.ID, name: c.Name, app: c.Labels[LabelApp]}
		if c.Project != "" && u.app == "" {
			if mv.Stacks == nil || !mv.Stacks.Exists(c.Project) {
				return nil, fmt.Errorf("%s was started outside NoCapOS and uses the App data folder; stop it first", c.Project)
			}
			u.project = c.Project
		}
		out = append(out, u)
	}
	return out, nil
}

// StopApps stops the running apps that use the App data folder (for a
// consistent backup); restart starts them again.
func (mv *Mover) StopApps(ctx context.Context) (func(), error) {
	users, err := mv.rootUsers(ctx, filepath.Clean(mv.Root(ctx)))
	if err != nil {
		return func() {}, err
	}
	stopped := []string{}
	stacks := map[string]bool{}
	restart := func() {
		bg := context.WithoutCancel(ctx)
		for p := range stacks {
			_ = mv.Stacks.RunWait(bg, p, "start")
		}
		for _, id := range stopped {
			_ = mv.engine.StartContainer(bg, id)
		}
	}
	for _, u := range users {
		d, err := mv.engine.InspectContainer(ctx, u.id)
		if err != nil || !d.Running {
			continue
		}
		if u.project != "" {
			if !stacks[u.project] {
				if err := mv.Stacks.RunWait(ctx, u.project, "stop"); err != nil {
					restart()
					return func() {}, err
				}
				stacks[u.project] = true
			}
			continue
		}
		if err := mv.engine.StopContainer(ctx, u.id, 30*time.Second); err != nil {
			restart()
			return func() {}, err
		}
		stopped = append(stopped, u.id)
	}
	return restart, nil
}

// RemoveOldRoot deletes the old copy left by a move of the App data folder.
func (mv *Mover) RemoveOldRoot(ctx context.Context) error {
	if j := mv.Job(); j != nil && !j.Done {
		return ErrBusy
	}
	old, _ := mv.db.Setting(ctx, KeyOldRoot)
	if old == "" {
		return errors.New("there's no old copy to remove")
	}
	cur := filepath.Clean(mv.Root(ctx))
	clean := filepath.Clean(old)
	if clean == cur || Under(cur, clean) || Under(clean, cur) {
		return errors.New("the old copy overlaps the App data folder; remove it by hand")
	}
	if err := ValidateRoot(clean, mv.protectedDirs(), mv.DockerRoot(ctx)); err != nil {
		return fmt.Errorf("won't remove %s: %w", old, err)
	}
	if users, err := mv.rootUsers(ctx, clean); err == nil && len(users) > 0 {
		return fmt.Errorf("%s still uses the old copy", users[0].name)
	}
	if err := os.RemoveAll(clean); err != nil {
		return err
	}
	return mv.db.SetSetting(ctx, KeyOldRoot, "")
}

// KeepOldRoot forgets the old copy without deleting it.
func (mv *Mover) KeepOldRoot(ctx context.Context) error {
	return mv.db.SetSetting(ctx, KeyOldRoot, "")
}

// ---------- the folder itself ----------

// AppFolder is one app's folder in App data.
type AppFolder struct {
	Name string `json:"name"` // folder name (the app's id)
	Path string `json:"path"`
	Size int64  `json:"size"` // -1 = still being added up
}

// sizes caches folder sizes, refreshed in the background.
type sizes struct {
	mu      sync.Mutex
	values  map[string]int64
	at      time.Time
	running bool
}

var folderSizes = &sizes{values: map[string]int64{}}

// Folders lists the app folders in App data, sorted by name.
func (mv *Mover) Folders(ctx context.Context) ([]AppFolder, error) {
	root := mv.Root(ctx)
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return []AppFolder{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []AppFolder{}
	dirs := []string{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(root, e.Name())
		dirs = append(dirs, p)
		out = append(out, AppFolder{Name: e.Name(), Path: filepath.ToSlash(p), Size: -1})
	}
	folderSizes.mu.Lock()
	for i := range out {
		if v, ok := folderSizes.values[filepath.FromSlash(out[i].Path)]; ok {
			out[i].Size = v
		}
	}
	stale := time.Since(folderSizes.at) > 2*time.Minute
	if stale && !folderSizes.running {
		folderSizes.running = true
		go func() {
			vals := map[string]int64{}
			for _, d := range dirs {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				vals[d] = DirSize(ctx, d)
				cancel()
			}
			folderSizes.mu.Lock()
			folderSizes.values, folderSizes.at, folderSizes.running = vals, time.Now(), false
			folderSizes.mu.Unlock()
		}()
	}
	folderSizes.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// RemoveAppDir deletes an app's folder in App data (uninstall with "also
// delete its data").
func RemoveAppDir(root, app string) error {
	dir := AppDir(root, app)
	if !Under(dir, root) {
		return errors.New("not inside the App data folder")
	}
	return os.RemoveAll(dir)
}
