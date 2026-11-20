package appdata

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/docker"
	"alfaos/alfad/internal/store"
)

// Moving apps into the App data folder. Nothing here runs on its own: every
// move starts from an admin's click.
//
// Moving an app's volumes: its containers are stopped, each volume's files
// are copied into <root>/<app>/<volume> (owners, permissions, extended
// attributes, ACLs and hard links kept) and checked against the original,
// the containers are recreated with the folders in place of the volumes
// (nothing else changes), started, and watched for a moment. If any step
// fails the app goes back to its volumes, as it was. The volumes themselves
// are kept until an admin cleans them up.

// MoveEngine is the part of the Docker client moves use (faked in tests).
type MoveEngine interface {
	Engine
	GetSpec(ctx context.Context, ref string) (docker.Spec, error)
	Recreate(ctx context.Context, ref string, s docker.Spec, pull func(image string) error) (string, error)
	StartContainer(ctx context.Context, id string) error
	StopContainer(ctx context.Context, id string, timeout time.Duration) error
	VolumeDetails(ctx context.Context) ([]docker.VolumeInfo, error)
	DeleteVolume(ctx context.Context, name string) error
}

// Store keeps the settings and the moved volumes.
type Store interface {
	Setting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
	OldVolumes(ctx context.Context) ([]store.OldVolume, error)
	SaveOldVolume(ctx context.Context, v store.OldVolume) error
	DeleteOldVolume(ctx context.Context, volume string) error
}

// Stacks is the part of the stacks manager moves use.
type Stacks interface {
	Exists(name string) bool
	Files(name string) (compose, env string, err error)
	Save(ctx context.Context, name, compose, env string, create bool) error
	Backup(name, tag string) (string, error)
	RunWait(ctx context.Context, name, action string) error
}

// AppStore is told when an App Store app's storage changed, so its saved
// changes (and future updates) use the new folders. remap turns an old
// mount into the new one; folders says the app now keeps its data in
// App data folders.
type AppStore interface {
	Rebind(ctx context.Context, app string, remap func(docker.SpecMount) docker.SpecMount, folders bool) error
}

// Setting keys.
const (
	KeyRoot    = "appdata.root"
	KeyOldRoot = "appdata.old_root"
	KeyShare   = "appdata.share"
)

// Mover moves apps' data into the App data folder and moves that folder.
type Mover struct {
	engine    MoveEngine
	db        Store
	protected func() []string
	fallback  string // the App data folder when none was chosen

	Copy     Copier
	Settle   time.Duration // how long apps run before they are checked
	Stacks   Stacks
	AppStore AppStore

	mu  sync.Mutex
	job *Job
}

// NewMover makes a mover. fallback is the App data folder to use until an
// admin picks another.
func NewMover(engine MoveEngine, db Store, protected func() []string, fallback string) *Mover {
	return &Mover{engine: engine, db: db, protected: protected, fallback: fallback, Copy: SystemCopy, Settle: 15 * time.Second}
}

// Root is the App data folder.
func (mv *Mover) Root(ctx context.Context) string {
	if mv.db != nil {
		if v, err := mv.db.Setting(ctx, KeyRoot); err == nil && v != "" {
			return v
		}
	}
	return mv.fallback
}

func (mv *Mover) protectedDirs() []string {
	if mv.protected == nil {
		return nil
	}
	return mv.protected()
}

// DockerRoot is Docker's own folder ("" when Docker isn't reachable).
func (mv *Mover) DockerRoot(ctx context.Context) string {
	if mv.engine == nil {
		return ""
	}
	if info, err := mv.engine.Info(ctx); err == nil && info.RootDir != "" {
		return info.RootDir
	}
	return "/var/lib/docker"
}

// CheckBind checks a folder someone wants to give a container.
func (mv *Mover) CheckBind(ctx context.Context, src string) error {
	return CheckBind(src, mv.protectedDirs(), mv.DockerRoot(ctx))
}

// ---------- jobs ----------

// Job is the progress of a move.
type Job struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"` // move | organize | root | cleanup
	App     string    `json:"app,omitempty"`
	Phase   string    `json:"phase"`
	Percent int       `json:"percent"`
	Done    bool      `json:"done"`
	Error   string    `json:"error,omitempty"`
	Results []Result  `json:"results,omitempty"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended,omitempty"`
}

// Result is how one app's move ended.
type Result struct {
	App   string `json:"app"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// ErrBusy means a move is already running.
var ErrBusy = errors.New("App data is already being moved; wait for it to finish")

// Job returns the running or last move.
func (mv *Mover) Job() *Job {
	mv.mu.Lock()
	defer mv.mu.Unlock()
	if mv.job == nil {
		return nil
	}
	j := *mv.job
	j.Results = append([]Result(nil), mv.job.Results...)
	return &j
}

func (mv *Mover) set(phase string, pct int) {
	mv.mu.Lock()
	defer mv.mu.Unlock()
	if mv.job != nil {
		mv.job.Phase = phase
		if pct >= 0 {
			mv.job.Percent = pct
		}
	}
}

func (mv *Mover) result(r Result) {
	mv.mu.Lock()
	defer mv.mu.Unlock()
	if mv.job != nil {
		mv.job.Results = append(mv.job.Results, r)
	}
}

// start runs work in the background, one move at a time.
func (mv *Mover) start(kind, app string, work func(ctx context.Context) error) (*Job, error) {
	mv.mu.Lock()
	if mv.job != nil && !mv.job.Done {
		mv.mu.Unlock()
		return nil, ErrBusy
	}
	mv.job = &Job{ID: fmt.Sprintf("%d", time.Now().UnixNano()), Kind: kind, App: app, Phase: "Starting…", Started: time.Now().UTC()}
	j := *mv.job
	mv.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Hour)
		defer cancel()
		err := work(ctx)
		mv.mu.Lock()
		mv.job.Done, mv.job.Ended = true, time.Now().UTC()
		if err != nil {
			mv.job.Error = err.Error()
		} else {
			mv.job.Phase, mv.job.Percent = "Done", 100
		}
		mv.mu.Unlock()
	}()
	return &j, nil
}

// Wait blocks until the running move ends (tests).
func (mv *Mover) Wait() *Job {
	for {
		if j := mv.Job(); j == nil || j.Done {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ---------- what can be moved ----------

// VolMove is one volume of an app and the folder it moves to.
type VolMove struct {
	Volume string `json:"volume"`
	Target string `json:"target"` // where the app sees it
	Dir    string `json:"dir"`    // the folder it moves to
	Key    string `json:"-"`      // the compose file's name for it

	mountpoint string
}

// Candidate is an app that still keeps data in Docker volumes.
type Candidate struct {
	App        string    `json:"app"`
	Kind       string    `json:"kind"` // app (App Store) | stack | container
	Containers []string  `json:"containers"`
	Volumes    []VolMove `json:"volumes"`
	// Why it can't be moved here ("" when it can).
	Blocked string `json:"blocked,omitempty"`

	ids []string
}

const composeVolumeLabel = "com.docker.compose.volume"

var anonVolume = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Candidates lists the apps that still keep data in Docker volumes.
func (mv *Mover) Candidates(ctx context.Context) ([]Candidate, error) {
	if mv.engine == nil {
		return nil, errors.New("docker is not available")
	}
	root := mv.Root(ctx)
	list, err := mv.engine.ListContainers(ctx, true)
	if err != nil {
		return nil, err
	}
	vols, err := mv.engine.VolumeDetails(ctx)
	if err != nil {
		return nil, err
	}
	volInfo := map[string]docker.VolumeInfo{}
	for _, v := range vols {
		volInfo[v.Name] = v
	}
	type group struct {
		c       *Candidate
		project string
		app     bool
	}
	groups := map[string]*group{}
	owners := map[string]map[string]bool{} // volume → apps using it
	order := []string{}
	for _, c := range list {
		if c.System || c.Name == "" {
			continue
		}
		name := appName(c)
		g := groups[name]
		if g == nil {
			g = &group{c: &Candidate{App: name, Containers: []string{}, Volumes: []VolMove{}}, project: c.Project, app: c.Labels[LabelApp] != ""}
			groups[name] = g
			order = append(order, name)
		}
		g.c.Containers = append(g.c.Containers, c.Name)
		g.c.ids = append(g.c.ids, c.ID)
		d, err := mv.engine.InspectContainer(ctx, c.ID)
		if err != nil {
			continue
		}
		for _, mt := range d.Mounts {
			if mt.Type != "volume" || mt.Name == "" {
				continue
			}
			if owners[mt.Name] == nil {
				owners[mt.Name] = map[string]bool{}
			}
			owners[mt.Name][name] = true
			dup := false
			for _, v := range g.c.Volumes {
				if v.Volume == mt.Name {
					dup = true
				}
			}
			if dup {
				continue
			}
			info := volInfo[mt.Name]
			vm := VolMove{Volume: mt.Name, Target: mt.Destination, mountpoint: info.Mountpoint, Key: info.Labels[composeVolumeLabel]}
			if vm.mountpoint == "" {
				vm.mountpoint = mt.Source
			}
			if info.Driver != "" && info.Driver != "local" {
				g.c.Blocked = "the volume " + mt.Name + " is kept by the " + info.Driver + " driver, not on this server's disk"
			}
			g.c.Volumes = append(g.c.Volumes, vm)
		}
	}
	out := []Candidate{}
	for _, name := range order {
		g := groups[name]
		c := g.c
		if len(c.Volumes) == 0 {
			continue
		}
		switch {
		case g.app:
			c.Kind = "app"
		case g.project != "":
			c.Kind = "stack"
			if mv.Stacks == nil || !mv.Stacks.Exists(g.project) {
				c.Blocked = "started outside NoCapOS; change its compose file to use folders in " + filepath.ToSlash(root)
			}
		default:
			c.Kind = "container"
		}
		used := map[string]bool{}
		for i := range c.Volumes {
			v := &c.Volumes[i]
			for other := range owners[v.Volume] {
				if other != name && c.Blocked == "" {
					c.Blocked = "the volume " + v.Volume + " is shared with " + other
				}
			}
			if c.Kind == "stack" && v.Key == "" {
				v.Key = strings.TrimPrefix(v.Volume, g.project+"_")
			}
			sub := subfolder(c.App, c.Kind, *v)
			for n := 2; used[sub]; n++ {
				sub = fmt.Sprintf("%s-%d", subfolder(c.App, c.Kind, *v), n)
			}
			used[sub] = true
			v.Dir = filepath.ToSlash(VolumeDir(root, c.App, sub))
		}
		sort.Strings(c.Containers)
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].App) < strings.ToLower(out[j].App) })
	return out, nil
}

// subfolder names a volume's folder inside its app's folder: the catalog's
// or compose file's name for it, else the volume's name without the app's.
func subfolder(app, kind string, v VolMove) string {
	name := v.Volume
	switch {
	case anonVolume.MatchString(name):
		name = filepath.Base(v.Target)
	case kind == "app":
		name = strings.TrimPrefix(name, "nocap-"+app+"-")
	case kind == "stack" && v.Key != "":
		name = v.Key
	default:
		for _, sep := range []string{"-", "_"} {
			if strings.HasPrefix(name, app+sep) && len(name) > len(app)+1 {
				name = strings.TrimPrefix(name, app+sep)
				break
			}
		}
	}
	return SafeName(name)
}

// ---------- moving apps ----------

// Move moves one app's volumes into the App data folder in the background.
func (mv *Mover) Move(ctx context.Context, app string) (*Job, error) {
	cands, err := mv.Candidates(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range cands {
		if c.App == app {
			if c.Blocked != "" {
				return nil, errors.New(c.Blocked)
			}
			return mv.start("move", app, func(ctx context.Context) error {
				err := mv.moveOne(ctx, c, 0, 100)
				mv.result(resultOf(c.App, err))
				return err
			})
		}
	}
	return nil, errors.New(app + " doesn't keep data in Docker volumes")
}

// OrganizeAll moves every app that can be moved, one after another.
func (mv *Mover) OrganizeAll(ctx context.Context) (*Job, error) {
	cands, err := mv.Candidates(ctx)
	if err != nil {
		return nil, err
	}
	todo := []Candidate{}
	for _, c := range cands {
		if c.Blocked == "" {
			todo = append(todo, c)
		}
	}
	if len(todo) == 0 {
		return nil, errors.New("no apps to move")
	}
	return mv.start("organize", "", func(ctx context.Context) error {
		failed := 0
		for i, c := range todo {
			from, to := 100*i/len(todo), 100*(i+1)/len(todo)
			err := mv.moveOne(ctx, c, from, to)
			mv.result(resultOf(c.App, err))
			if err != nil {
				failed++
			}
		}
		if failed > 0 {
			return fmt.Errorf("%d of %d apps couldn't be moved and are back on their volumes", failed, len(todo))
		}
		return nil
	})
}

func resultOf(app string, err error) Result {
	if err != nil {
		return Result{App: app, Error: err.Error()}
	}
	return Result{App: app, OK: true}
}

// span maps a step's own 0–100 onto the job's from–to.
func span(from, to, pct int) int { return from + (to-from)*pct/100 }

// moveOne moves one app; on failure everything is put back.
func (mv *Mover) moveOne(ctx context.Context, c Candidate, from, to int) error {
	root := mv.Root(ctx)
	if err := ValidateRoot(root, mv.protectedDirs(), mv.DockerRoot(ctx)); err != nil {
		return fmt.Errorf("the App data folder %s can't be used: %w", root, err)
	}
	dirs := map[string]string{} // volume → folder
	for _, v := range c.Volumes {
		if !Under(filepath.FromSlash(v.Dir), root) {
			return fmt.Errorf("%s: not inside the App data folder", v.Dir)
		}
		if ok, err := emptyOrMissing(filepath.FromSlash(v.Dir)); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("%s already has files in it; move them away first", v.Dir)
		}
		dirs[v.Volume] = v.Dir
	}
	if c.Kind == "stack" {
		return mv.moveStack(ctx, c, root, dirs, from, to)
	}

	// How the containers are now, to put them back if anything fails.
	specs := map[string]docker.Spec{}
	running := map[string]bool{}
	for _, id := range c.ids {
		s, err := mv.engine.GetSpec(ctx, id)
		if err != nil {
			return fmt.Errorf("read %s's settings: %w", id, err)
		}
		specs[id] = s
		if d, err := mv.engine.InspectContainer(ctx, id); err == nil && d.Running {
			running[id] = true
		}
	}
	mv.set("Stopping "+c.App+"…", span(from, to, 5))
	if err := mv.stopAll(ctx, c.ids, running); err != nil {
		mv.startAll(context.WithoutCancel(ctx), c.ids, running)
		return err
	}
	created, err := mv.copyVolumes(ctx, c, root, from, to)
	if err != nil {
		removeCreated(root, created)
		mv.startAll(context.WithoutCancel(ctx), c.ids, running)
		return err
	}
	remap := func(m docker.SpecMount) docker.SpecMount {
		if dir, ok := dirs[m.Source]; ok && m.Type == "volume" {
			return docker.SpecMount{Type: "bind", Source: dir, Target: m.Target, ReadOnly: m.ReadOnly}
		}
		return m
	}
	mv.set("Switching "+c.App+" to its folders…", span(from, to, 80))
	newIDs, err := mv.recreateAll(ctx, c.ids, specs, remap)
	if err == nil {
		mv.set("Starting "+c.App+"…", span(from, to, 88))
		err = mv.startChecked(ctx, newIDs, c.ids, running)
	}
	if err != nil {
		mv.set("Something went wrong; putting "+c.App+" back on its volumes…", span(from, to, 90))
		mv.putBack(context.WithoutCancel(ctx), c.ids, newIDs, specs, running)
		removeCreated(root, created)
		return err
	}
	if c.Kind == "app" && mv.AppStore != nil {
		if err := mv.AppStore.Rebind(ctx, c.App, remap, true); err != nil {
			return fmt.Errorf("%s moved, but its App Store settings couldn't be updated: %w", c.App, err)
		}
	}
	mv.keepOld(ctx, c)
	return nil
}

func (mv *Mover) keepOld(ctx context.Context, c Candidate) {
	for _, v := range c.Volumes {
		_ = mv.db.SaveOldVolume(context.WithoutCancel(ctx), store.OldVolume{Volume: v.Volume, App: c.App, Dir: v.Dir, MovedAt: time.Now()})
	}
}

func (mv *Mover) stopAll(ctx context.Context, ids []string, running map[string]bool) error {
	for i := len(ids) - 1; i >= 0; i-- {
		if running[ids[i]] {
			if err := mv.engine.StopContainer(ctx, ids[i], 30*time.Second); err != nil {
				return fmt.Errorf("stop the app: %w", err)
			}
		}
	}
	return nil
}

func (mv *Mover) startAll(ctx context.Context, ids []string, running map[string]bool) {
	for _, id := range ids {
		if running[id] {
			_ = mv.engine.StartContainer(ctx, id)
		}
	}
}

// copyVolumes copies each volume into its folder and checks the copy. It
// returns the folders it made (removed again if anything fails).
func (mv *Mover) copyVolumes(ctx context.Context, c Candidate, root string, from, to int) ([]string, error) {
	created := []string{}
	if _, err := os.Stat(AppDir(root, c.App)); os.IsNotExist(err) {
		created = append(created, AppDir(root, c.App))
	}
	for i, v := range c.Volumes {
		dir := filepath.FromSlash(v.Dir)
		pct := 10 + 65*i/len(c.Volumes)
		mv.set(fmt.Sprintf("Copying %s (%d of %d)…", v.Volume, i+1, len(c.Volumes)), span(from, to, pct))
		if v.mountpoint == "" {
			return created, fmt.Errorf("can't find where the volume %s keeps its files", v.Volume)
		}
		if st, err := os.Stat(v.mountpoint); err != nil || !st.IsDir() {
			return created, fmt.Errorf("can't reach the volume %s's files at %s", v.Volume, v.mountpoint)
		}
		if err := EnsureDir(root, filepath.Dir(dir), -1, -1); err != nil {
			return created, err
		}
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			created = append(created, dir)
		}
		if err := mv.Copy(ctx, v.mountpoint, dir); err != nil {
			return created, err
		}
		mv.set(fmt.Sprintf("Checking the copy of %s…", v.Volume), span(from, to, pct+3))
		if _, err := Verify(ctx, v.mountpoint, dir); err != nil {
			return created, fmt.Errorf("the copy of %s doesn't match: %w", v.Volume, err)
		}
	}
	return created, nil
}

// removeCreated deletes folders a failed move made (only inside root).
func removeCreated(root string, dirs []string) {
	for i := len(dirs) - 1; i >= 0; i-- {
		if Under(dirs[i], root) {
			_ = os.RemoveAll(dirs[i])
		}
	}
}

// recreateAll recreates each container with its mounts remapped; it
// returns old id → new id for the ones it recreated, even on failure.
func (mv *Mover) recreateAll(ctx context.Context, ids []string, specs map[string]docker.Spec, remap func(docker.SpecMount) docker.SpecMount) (map[string]string, error) {
	newIDs := map[string]string{}
	for _, id := range ids {
		s := specs[id]
		ns := s
		ns.Mounts = make([]docker.SpecMount, len(s.Mounts))
		for i, m := range s.Mounts {
			ns.Mounts[i] = remap(m)
		}
		nid, err := mv.engine.Recreate(ctx, id, ns, nil)
		if err != nil {
			return newIDs, fmt.Errorf("recreate %s: %w", s.Name, err)
		}
		newIDs[id] = nid
	}
	return newIDs, nil
}

// startChecked starts the containers that were running and checks they
// stay up.
func (mv *Mover) startChecked(ctx context.Context, newIDs map[string]string, ids []string, running map[string]bool) error {
	started := []string{}
	for _, id := range ids {
		if !running[id] {
			continue
		}
		nid := newIDs[id]
		if err := mv.engine.StartContainer(ctx, nid); err != nil {
			return fmt.Errorf("start: %w", err)
		}
		started = append(started, nid)
	}
	return mv.healthy(ctx, started)
}

// healthy waits a moment, then checks every container is running, not
// restarting and not unhealthy, twice (a crash loop shows as restarts).
func (mv *Mover) healthy(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	wait := func(d time.Duration) error {
		if d <= 0 {
			return nil
		}
		select {
		case <-time.After(d):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	mv.set("Checking the app runs…", -1)
	restarts := map[string]int{}
	for round := 0; round < 2; round++ {
		if err := wait(mv.Settle / time.Duration(1+2*round)); err != nil {
			return err
		}
		for _, id := range ids {
			d, err := mv.engine.InspectContainer(ctx, id)
			if err != nil {
				return err
			}
			switch {
			case d.State == "restarting" || (round > 0 && d.RestartCount > restarts[id]):
				return fmt.Errorf("%s keeps restarting with its new folders", d.Name)
			case !d.Running:
				return fmt.Errorf("%s stopped after the move (exit code %d)", d.Name, d.ExitCode)
			case d.Health == "unhealthy":
				return fmt.Errorf("%s reports it's unhealthy after the move", d.Name)
			}
			restarts[id] = d.RestartCount
		}
	}
	return nil
}

// putBack recreates moved containers with their old settings and starts
// the ones that were running.
func (mv *Mover) putBack(ctx context.Context, ids []string, newIDs map[string]string, specs map[string]docker.Spec, running map[string]bool) {
	for _, id := range ids {
		ref := id
		if nid, ok := newIDs[id]; ok {
			_ = mv.engine.StopContainer(ctx, nid, 30*time.Second)
			if back, err := mv.engine.Recreate(ctx, nid, specs[id], nil); err == nil {
				ref = back
			} else {
				ref = nid
			}
		}
		if running[id] {
			_ = mv.engine.StartContainer(ctx, ref)
		}
	}
}

// moveStack moves a NoCapOS stack: its compose file's volumes become
// folders (the old file is kept next to it) and Compose recreates it.
func (mv *Mover) moveStack(ctx context.Context, c Candidate, root string, dirs map[string]string, from, to int) error {
	project := c.App
	compose, env, err := mv.Stacks.Files(project)
	if err != nil {
		return err
	}
	keys := map[string]string{}
	for _, v := range c.Volumes {
		keys[v.Key] = dirs[v.Volume]
	}
	rewritten, err := RewriteComposeVolumes(compose, keys)
	if err != nil {
		return fmt.Errorf("read the compose file: %w", err)
	}
	running := false
	for _, id := range c.ids {
		if d, err := mv.engine.InspectContainer(ctx, id); err == nil && d.Running {
			running = true
		}
	}
	if _, err := mv.Stacks.Backup(project, "before-appdata-"+time.Now().Format("20060102-150405")); err != nil {
		return fmt.Errorf("keep a copy of the compose file: %w", err)
	}
	mv.set("Stopping "+project+"…", span(from, to, 5))
	if err := mv.Stacks.RunWait(ctx, project, "stop"); err != nil {
		return fmt.Errorf("stop the stack: %w", err)
	}
	restart := func() {
		if running {
			_ = mv.Stacks.RunWait(context.WithoutCancel(ctx), project, "start")
		}
	}
	created, err := mv.copyVolumes(ctx, c, root, from, to)
	if err != nil {
		removeCreated(root, created)
		restart()
		return err
	}
	mv.set("Switching "+project+" to its folders…", span(from, to, 80))
	fail := func(err error) error {
		mv.set("Something went wrong; putting "+project+" back on its volumes…", span(from, to, 90))
		bg := context.WithoutCancel(ctx)
		_ = mv.Stacks.Save(bg, project, compose, env, false)
		_ = mv.Stacks.RunWait(bg, project, "up")
		if !running {
			_ = mv.Stacks.RunWait(bg, project, "stop")
		}
		removeCreated(root, created)
		return err
	}
	if err := mv.Stacks.Save(ctx, project, rewritten, env, false); err != nil {
		return fail(err)
	}
	if err := mv.Stacks.RunWait(ctx, project, "up"); err != nil {
		return fail(err)
	}
	if running {
		ids, err := mv.projectContainers(ctx, project)
		if err == nil {
			err = mv.healthy(ctx, ids)
		}
		if err != nil {
			return fail(err)
		}
	} else {
		_ = mv.Stacks.RunWait(ctx, project, "stop")
	}
	mv.keepOld(ctx, c)
	return nil
}

func (mv *Mover) projectContainers(ctx context.Context, project string) ([]string, error) {
	list, err := mv.engine.ListContainers(ctx, true)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, c := range list {
		if c.Project == project && c.Labels[LabelApp] == "" {
			ids = append(ids, c.ID)
		}
	}
	return ids, nil
}

// ---------- old volumes ----------

// OldVolume is a moved volume still kept, with its size.
type OldVolume struct {
	store.OldVolume
	Size  int64 `json:"size"`
	InUse bool  `json:"in_use"`
}

// OldVolumes lists the volumes kept after moves (ones already gone are
// forgotten).
func (mv *Mover) OldVolumes(ctx context.Context) ([]OldVolume, error) {
	rows, err := mv.db.OldVolumes(ctx)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 || mv.engine == nil {
		return []OldVolume{}, nil
	}
	vols, err := mv.engine.VolumeDetails(ctx)
	if err != nil {
		return nil, err
	}
	have := map[string]docker.VolumeInfo{}
	for _, v := range vols {
		have[v.Name] = v
	}
	used := mv.volumesInUse(ctx)
	out := []OldVolume{}
	for _, r := range rows {
		v, ok := have[r.Volume]
		if !ok {
			_ = mv.db.DeleteOldVolume(ctx, r.Volume)
			continue
		}
		o := OldVolume{OldVolume: r, InUse: used[r.Volume]}
		if v.Mountpoint != "" {
			sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			o.Size = DirSize(sctx, v.Mountpoint)
			cancel()
		}
		out = append(out, o)
	}
	return out, nil
}

func (mv *Mover) volumesInUse(ctx context.Context) map[string]bool {
	used := map[string]bool{}
	list, err := mv.engine.ListContainers(ctx, true)
	if err != nil {
		return used
	}
	for _, c := range list {
		if d, err := mv.engine.InspectContainer(ctx, c.ID); err == nil {
			for _, m := range d.Mounts {
				if m.Type == "volume" {
					used[m.Name] = true
				}
			}
		}
	}
	return used
}

// RemoveOldVolumes deletes kept volumes (only ones moves left behind, and
// never one a container still uses).
func (mv *Mover) RemoveOldVolumes(ctx context.Context, names []string) ([]string, error) {
	rows, err := mv.db.OldVolumes(ctx)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, r := range rows {
		known[r.Volume] = true
	}
	used := mv.volumesInUse(ctx)
	removed := []string{}
	var errs []string
	for _, n := range names {
		switch {
		case !known[n]:
			errs = append(errs, n+": not a volume left by a move")
		case used[n]:
			errs = append(errs, n+": a container still uses it")
		default:
			if err := mv.engine.DeleteVolume(ctx, n); err != nil && !docker.IsNotFound(err) {
				errs = append(errs, n+": "+err.Error())
				continue
			}
			_ = mv.db.DeleteOldVolume(ctx, n)
			removed = append(removed, n)
		}
	}
	if len(errs) > 0 {
		return removed, errors.New(strings.Join(errs, "; "))
	}
	return removed, nil
}
