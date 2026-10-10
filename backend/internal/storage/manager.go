package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/store"
	"alfaos/alfad/internal/syspkg"
)

// LocationStore persists Files locations and settings (the server store).
type LocationStore interface {
	StorageLocations(ctx context.Context) ([]store.StorageLocation, error)
	SaveStorageLocation(ctx context.Context, l store.StorageLocation) error
	DeleteStorageLocation(ctx context.Context, id string) error
	Setting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
}

// PathUse is a host folder an app keeps data in.
type PathUse struct {
	Path string
	App  string
}

type Options struct {
	Backend Backend
	Store   LocationStore
	Files   *files.Service
	DataDir string
	Log     *slog.Logger
	Demo    bool
	// Host checks, replaceable in tests.
	Native     func() bool
	CanInstall func() bool
	PkgManager func() string
}

type Manager struct {
	be      Backend
	st      LocationStore
	files   *files.Service
	dataDir string
	log     *slog.Logger
	demo    bool
	native  func() bool
	canInst func() bool
	pkgMgr  func() string

	// InUse lists folders apps keep data in (set by the API from Docker).
	InUse func(ctx context.Context) []PathUse

	mu         sync.Mutex
	busy       map[string]string // lock key -> what's running
	jobs       []*Job
	smart      map[string]smartEntry
	refreshing bool
	alerts     []Alert
	subs       map[chan []Alert]struct{}
}

type smartEntry struct {
	s  *SmartSummary
	at time.Time
}

const (
	smartMaxAge     = 10 * time.Minute
	healthInterval  = 10 * time.Minute
	scrubEvery      = 30 * 24 * time.Hour
	keyAutoScrub    = "storage.auto_scrub"
	keyLastScrubPfx = "storage.last_scrub."
)

func NewManager(o Options) *Manager {
	if o.Native == nil {
		o.Native = syspkg.Native
	}
	if o.CanInstall == nil {
		o.CanInstall = syspkg.CanInstall
	}
	if o.PkgManager == nil {
		o.PkgManager = syspkg.Manager
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	return &Manager{be: o.Backend, st: o.Store, files: o.Files, dataDir: filepath.ToSlash(o.DataDir), log: o.Log, demo: o.Demo,
		native: o.Native, canInst: o.CanInstall, pkgMgr: o.PkgManager,
		busy: map[string]string{}, smart: map[string]smartEntry{}, subs: map[chan []Alert]struct{}{}}
}

// ---- status ----

type ZFSStatus struct {
	Installed  bool   `json:"installed"`
	Module     bool   `json:"module"`
	Version    string `json:"version,omitempty"`
	CanInstall bool   `json:"can_install"`
}

type SmartStatus struct {
	Installed  bool `json:"installed"`
	CanInstall bool `json:"can_install"`
}

type Status struct {
	Supported bool        `json:"supported"`
	Reason    string      `json:"reason,omitempty"`
	Native    bool        `json:"native"`
	Demo      bool        `json:"demo"`
	ZFS       ZFSStatus   `json:"zfs"`
	Smart     SmartStatus `json:"smart"`
}

func (m *Manager) supported() bool { return m.demo || m.native() }

func unsupportedReason() string {
	switch {
	case runtime.GOOS != "linux":
		return "Managing disks and RAID needs NoCapOS installed directly on a Linux server. This copy runs on " + osName() + ", so the Storage app can only explain what it does."
	case os.Geteuid() != 0:
		return "NoCapOS needs to run as root to manage disks. Start the NoCapOS service as root to use this app."
	}
	return "NoCapOS is running inside a container, so it can't see or change the server's disks. Install NoCapOS directly on the server to manage disks and RAID."
}

func osName() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	}
	return runtime.GOOS
}

func (m *Manager) Status(ctx context.Context) Status {
	st := Status{Supported: m.supported(), Native: m.native(), Demo: m.demo}
	if !st.Supported {
		st.Reason = unsupportedReason()
		return st
	}
	t := m.be.Tools(ctx)
	st.ZFS = ZFSStatus{Installed: t.ZFSInstalled, Module: t.ZFSModule, Version: t.ZFSVersion,
		CanInstall: m.demo || (m.native() && m.pkgMgr() == "apt")}
	st.Smart = SmartStatus{Installed: t.SmartInstalled, CanInstall: m.demo || m.canInst()}
	return st
}

func (m *Manager) ready() error {
	if !m.supported() {
		return &Error{503, unsupportedReason()}
	}
	return nil
}

func (m *Manager) needZFS(ctx context.Context) error {
	if err := m.ready(); err != nil {
		return err
	}
	t := m.be.Tools(ctx)
	if !t.ZFSInstalled {
		return bad("install ZFS first")
	}
	if !t.ZFSModule {
		return bad("the ZFS kernel module isn't loaded; try installing ZFS again or restart the server")
	}
	return nil
}

// Install installs "zfs" or "smart".
func (m *Manager) Install(ctx context.Context, tool string) error {
	if err := m.ready(); err != nil {
		return err
	}
	if tool != "zfs" && tool != "smart" {
		return bad("unknown tool %q", tool)
	}
	return m.be.Install(ctx, tool)
}

// ---- locks and jobs ----

func (m *Manager) lock(what string, keys ...string) (func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		if cur, ok := m.busy[k]; ok {
			return nil, busy("%s is busy (%s); wait for it to finish", strings.SplitN(k, ":", 2)[1], cur)
		}
	}
	for _, k := range keys {
		m.busy[k] = what
	}
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, k := range keys {
			delete(m.busy, k)
		}
	}, nil
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// startJob runs fn in the background; unlock is called when it ends.
func (m *Manager) startJob(kind, target string, unlock func(), fn func(ctx context.Context) error, done func(error)) *Job {
	j := &Job{ID: newID(), Kind: kind, Target: target, State: "running", Started: time.Now().UTC()}
	m.mu.Lock()
	m.jobs = append(m.jobs, j)
	if len(m.jobs) > 50 {
		m.jobs = m.jobs[len(m.jobs)-50:]
	}
	m.mu.Unlock()
	go func() {
		defer unlock()
		ctx, cancel := context.WithTimeout(context.Background(), longTimeout+5*time.Minute)
		defer cancel()
		err := fn(ctx)
		now := time.Now().UTC()
		m.mu.Lock()
		j.Finished = &now
		if err != nil {
			j.State, j.Error = "failed", err.Error()
		} else {
			j.State = "done"
		}
		m.mu.Unlock()
		if err != nil {
			m.log.Warn("storage job failed", "kind", kind, "target", target, "err", err)
		} else {
			m.log.Info("storage job done", "kind", kind, "target", target)
		}
		if done != nil {
			done(err)
		}
	}()
	c := *j
	return &c
}

func (m *Manager) Job(id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.ID == id {
			return *j, nil
		}
	}
	return Job{}, notFound("no such job")
}

func (m *Manager) Jobs() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, 0, len(m.jobs))
	for i := len(m.jobs) - 1; i >= 0; i-- {
		out = append(out, *m.jobs[i])
	}
	return out
}

// ---- disks ----

type scan struct {
	facts *Facts
	pools []Pool
	disks []Disk // listed (non-virtual) disks
}

// scanAll takes a fresh look at disks and pools.
func (m *Manager) scanAll(ctx context.Context) (*scan, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}
	f, err := m.be.Facts(ctx)
	if err != nil {
		return nil, err
	}
	var pools []Pool
	if t := m.be.Tools(ctx); t.ZFSInstalled && t.ZFSModule {
		if pools, err = m.be.Pools(ctx); err != nil {
			return nil, err
		}
	}
	in := classifyInput{Facts: f, PoolLeaves: map[string]string{}, ActivePools: map[string]bool{}, DataDir: m.dataDir}
	for i := range pools {
		in.ActivePools[pools[i].Name] = true
		for _, l := range leaves(pools[i].Vdevs) {
			for _, p := range []string{l.Path, l.Name, l.Was} {
				if p != "" && !isDigits(p) {
					in.PoolLeaves[p] = pools[i].Name
				}
			}
			if k := resolveDev(firstNonEmpty(l.Path, l.Name), f.Links); k != "" {
				l.Disk = diskOf(f, k)
			}
		}
	}
	var disks []Disk
	for _, d := range classify(in) {
		if !d.hidden {
			disks = append(disks, d)
		}
	}
	if disks == nil {
		disks = []Disk{}
	}
	return &scan{facts: f, pools: pools, disks: disks}, nil
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

// Disks lists the disks with availability and cached SMART health.
func (m *Manager) Disks(ctx context.Context) ([]Disk, error) {
	sc, err := m.scanAll(ctx)
	if err != nil {
		return nil, err
	}
	m.attachSmart(ctx, sc.disks)
	return sc.disks, nil
}

func smartKey(d Disk) string { return d.Name + "|" + d.Serial }

func (m *Manager) attachSmart(ctx context.Context, disks []Disk) {
	if !m.be.Tools(ctx).SmartInstalled {
		return
	}
	var missing []int
	stale := false
	m.mu.Lock()
	for i := range disks {
		e, ok := m.smart[smartKey(disks[i])]
		if !ok {
			missing = append(missing, i)
			continue
		}
		disks[i].Smart = e.s
		if time.Since(e.at) > smartMaxAge {
			stale = true
		}
	}
	m.mu.Unlock()
	if len(missing) > 0 {
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		for _, i := range missing {
			disks[i].Smart = m.readSmart(cctx, disks[i])
		}
		cancel()
	}
	if stale {
		go m.refreshSmart(append([]Disk(nil), disks...))
	}
}

func (m *Manager) readSmart(ctx context.Context, d Disk) *SmartSummary {
	r, err := m.be.Smart(ctx, d)
	var s *SmartSummary
	if err != nil {
		s = &SmartSummary{Available: false}
	} else {
		s = &r.Summary
	}
	m.mu.Lock()
	if old, ok := m.smart[smartKey(d)]; ok && s.Asleep && old.s.Available && !old.s.Asleep {
		cp := *old.s // keep the last real reading for a sleeping disk
		cp.Asleep = true
		s = &cp
	}
	m.smart[smartKey(d)] = smartEntry{s: s, at: time.Now()}
	m.mu.Unlock()
	return s
}

func (m *Manager) refreshSmart(disks []Disk) {
	m.mu.Lock()
	if m.refreshing {
		m.mu.Unlock()
		return
	}
	m.refreshing = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.refreshing = false; m.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, d := range disks {
		m.readSmart(ctx, d)
	}
}

func findDisk(disks []Disk, name string) (Disk, bool) {
	for _, d := range disks {
		if d.Name == name {
			return d, true
		}
	}
	return Disk{}, false
}

// requireAvailable re-checks disks at action time against a fresh scan.
func (m *Manager) requireAvailable(ctx context.Context, names []string) ([]Disk, error) {
	sc, err := m.scanAll(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]Disk, 0, len(names))
	for _, n := range names {
		if err := validKernelName(n); err != nil {
			return nil, err
		}
		if seen[n] {
			return nil, bad("%s is listed twice", n)
		}
		seen[n] = true
		d, ok := findDisk(sc.disks, n)
		if !ok {
			return nil, bad("disk %s isn't connected any more", n)
		}
		if !d.Available {
			return nil, bad("%s can't be used: %s", n, d.Reason)
		}
		out = append(out, d)
	}
	return out, nil
}

// stablePath is how we hand a disk to zpool: /dev/disk/by-id when known.
func stablePath(d Disk) string {
	if d.ByID != "" {
		return d.ByID
	}
	return d.Path
}

func (m *Manager) SmartDetail(ctx context.Context, name string) (*SmartReport, error) {
	if err := validKernelName(name); err != nil {
		return nil, err
	}
	sc, err := m.scanAll(ctx)
	if err != nil {
		return nil, err
	}
	d, ok := findDisk(sc.disks, name)
	if !ok {
		return nil, notFound("no disk named %s", name)
	}
	if !m.be.Tools(ctx).SmartInstalled {
		return nil, bad("install SMART tools (smartmontools) first")
	}
	r, err := m.be.Smart(ctx, d)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.smart[smartKey(d)] = smartEntry{s: &r.Summary, at: time.Now()}
	m.mu.Unlock()
	return r, nil
}

func (m *Manager) SmartTest(ctx context.Context, name, kind string, done func(error)) (*Job, error) {
	if kind != "short" && kind != "long" {
		return nil, bad("test type must be short or long")
	}
	if err := validKernelName(name); err != nil {
		return nil, err
	}
	sc, err := m.scanAll(ctx)
	if err != nil {
		return nil, err
	}
	d, ok := findDisk(sc.disks, name)
	if !ok {
		return nil, notFound("no disk named %s", name)
	}
	if !m.be.Tools(ctx).SmartInstalled {
		return nil, bad("install SMART tools (smartmontools) first")
	}
	unlock, err := m.lock("SMART test", "disk:"+name)
	if err != nil {
		return nil, err
	}
	return m.startJob("smart-test", name, unlock, func(ctx context.Context) error {
		err := m.be.SmartTest(ctx, d, kind)
		m.readSmart(ctx, d)
		return err
	}, done), nil
}

// FormatRequest makes a single disk into NoCapOS storage (ext4).
type FormatRequest struct {
	Label      string `json:"label"`
	AddToFiles bool   `json:"add_to_files"`
	Confirm    string `json:"confirm"`
}

func (m *Manager) Format(ctx context.Context, name string, req FormatRequest, done func(error)) (*Job, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}
	if err := checkConfirm(req.Confirm, "ERASE"); err != nil {
		return nil, err
	}
	if err := ValidLabel(req.Label); err != nil {
		return nil, err
	}
	if err := validKernelName(name); err != nil {
		return nil, err
	}
	mp := DiskBase + "/" + req.Label
	if m.be.Mounted(mp) {
		return nil, bad("something is already mounted at %s; pick another label", mp)
	}
	unlock, err := m.lock("formatting", "disk:"+name, "label:"+req.Label)
	if err != nil {
		return nil, err
	}
	if _, err := m.requireAvailable(ctx, []string{name}); err != nil {
		unlock()
		return nil, err
	}
	return m.startJob("format", name, unlock, func(ctx context.Context) error {
		disks, err := m.requireAvailable(ctx, []string{name})
		if err != nil {
			return err
		}
		if err := m.be.Format(ctx, disks[0].Path, req.Label, mp); err != nil {
			return err
		}
		if req.AddToFiles {
			return m.addLocation(ctx, "disk:"+req.Label, "disk", req.Label, req.Label, m.be.FilesPath(mp))
		}
		return nil
	}, done), nil
}

// SetDiskFiles shows or hides a NoCapOS-formatted disk in Files.
func (m *Manager) SetDiskFiles(ctx context.Context, name string, enabled bool) error {
	if err := validKernelName(name); err != nil {
		return err
	}
	sc, err := m.scanAll(ctx)
	if err != nil {
		return err
	}
	d, ok := findDisk(sc.disks, name)
	if !ok {
		return notFound("no disk named %s", name)
	}
	mp := ""
	for _, p := range d.Partitions {
		for _, x := range p.Mountpoints {
			if within(x, DiskBase) && x != DiskBase {
				mp = x
			}
		}
	}
	if mp == "" {
		return bad("only disks set up as storage by NoCapOS can be shown in Files")
	}
	label := filepath.Base(mp)
	if enabled {
		return m.addLocation(ctx, "disk:"+label, "disk", label, label, m.be.FilesPath(mp))
	}
	return m.removeLocation(ctx, "disk:"+label)
}

// ---- pools ----

// PoolsResult is GET /pools.
type PoolsResult struct {
	Pools      []Pool           `json:"pools"`
	Importable []ImportablePool `json:"importable"`
}

func (m *Manager) Pools(ctx context.Context) (*PoolsResult, error) {
	if err := m.needZFS(ctx); err != nil {
		return nil, err
	}
	sc, err := m.scanAll(ctx)
	if err != nil {
		return nil, err
	}
	locs := m.locationIDs(ctx)
	res := &PoolsResult{Pools: sc.pools, Importable: []ImportablePool{}}
	if res.Pools == nil {
		res.Pools = []Pool{}
	}
	for i := range res.Pools {
		res.Pools[i].InFiles = locs["pool:"+res.Pools[i].Name]
	}
	imp, err := m.be.Importable(ctx)
	if err == nil {
		for _, ip := range imp {
			for j, d := range ip.Disks {
				if k := resolveDev(d, sc.facts.Links); k != "" {
					if top := diskOf(sc.facts, k); top != "" {
						ip.Disks[j] = top
					}
				}
			}
			res.Importable = append(res.Importable, ip)
		}
	}
	return res, nil
}

func (m *Manager) pool(ctx context.Context, name string) (*Pool, *scan, error) {
	if err := ValidPoolName(name); err != nil {
		return nil, nil, err
	}
	if err := m.needZFS(ctx); err != nil {
		return nil, nil, err
	}
	sc, err := m.scanAll(ctx)
	if err != nil {
		return nil, nil, err
	}
	for i := range sc.pools {
		if sc.pools[i].Name == name {
			return &sc.pools[i], sc, nil
		}
	}
	return nil, nil, notFound("no pool named %s", name)
}

type CreatePoolRequest struct {
	Name        string   `json:"name"`
	Layout      string   `json:"layout"`
	Disks       []string `json:"disks"`
	Compression string   `json:"compression"`
	AddToFiles  bool     `json:"add_to_files"`
	Confirm     string   `json:"confirm"`
	Mountpoint  string   `json:"mountpoint,omitempty"`
}

func (m *Manager) CreatePool(ctx context.Context, req CreatePoolRequest, done func(error)) (*Job, error) {
	if err := m.needZFS(ctx); err != nil {
		return nil, err
	}
	if err := checkConfirm(req.Confirm, "ERASE"); err != nil {
		return nil, err
	}
	if err := ValidPoolName(req.Name); err != nil {
		return nil, err
	}
	if req.Compression == "" {
		req.Compression = "lz4"
	}
	if err := validCompression(req.Compression, false); err != nil {
		return nil, err
	}
	if err := validLayout(req.Layout, len(req.Disks)); err != nil {
		return nil, err
	}
	mp, err := poolMountpoint(req.Name, req.Mountpoint)
	if err != nil {
		return nil, err
	}
	keys := []string{"pool:" + req.Name}
	for _, d := range req.Disks {
		if err := validKernelName(d); err != nil {
			return nil, err
		}
		keys = append(keys, "disk:"+d)
	}
	unlock, err := m.lock("creating pool "+req.Name, keys...)
	if err != nil {
		return nil, err
	}
	check := func(ctx context.Context) ([]string, error) {
		sc, err := m.scanAll(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range sc.pools {
			if p.Name == req.Name {
				return nil, bad("a pool named %s already exists", req.Name)
			}
		}
		if imp, err := m.be.Importable(ctx); err == nil {
			for _, ip := range imp {
				if ip.Name == req.Name {
					return nil, bad("a pool named %s is connected and can be imported; pick another name", req.Name)
				}
			}
		}
		disks, err := m.requireAvailable(ctx, req.Disks)
		if err != nil {
			return nil, err
		}
		devs := make([]string, len(disks))
		for i, d := range disks {
			devs[i] = stablePath(d)
		}
		return devs, nil
	}
	if _, err := check(ctx); err != nil {
		unlock()
		return nil, err
	}
	return m.startJob("create-pool", req.Name, unlock, func(ctx context.Context) error {
		devs, err := check(ctx)
		if err != nil {
			return err
		}
		if err := m.be.CreatePool(ctx, req.Name, req.Layout, devs, req.Compression, mp); err != nil {
			return err
		}
		if req.AddToFiles {
			return m.addLocation(ctx, "pool:"+req.Name, "pool", req.Name, req.Name, m.be.FilesPath(mp))
		}
		return nil
	}, done), nil
}

type AddVdevRequest struct {
	Layout        string   `json:"layout"`
	Disks         []string `json:"disks"`
	Confirm       string   `json:"confirm"`
	AllowMismatch bool     `json:"allow_mismatch,omitempty"`
}

func (m *Manager) AddVdev(ctx context.Context, pool string, req AddVdevRequest, done func(error)) (*Job, error) {
	if err := checkConfirm(req.Confirm, "ERASE"); err != nil {
		return nil, err
	}
	p, _, err := m.pool(ctx, pool)
	if err != nil {
		return nil, err
	}
	if err := validLayout(req.Layout, len(req.Disks)); err != nil {
		return nil, err
	}
	if p.Layout != req.Layout && !req.AllowMismatch {
		return nil, bad("%s is a %s pool; adding a %s group changes how many disks can fail. Confirm the mismatch to do it anyway", pool, layoutTitle(p.Layout), layoutTitle(req.Layout))
	}
	keys := []string{"pool:" + pool}
	for _, d := range req.Disks {
		if err := validKernelName(d); err != nil {
			return nil, err
		}
		keys = append(keys, "disk:"+d)
	}
	unlock, err := m.lock("adding disks", keys...)
	if err != nil {
		return nil, err
	}
	if _, err := m.requireAvailable(ctx, req.Disks); err != nil {
		unlock()
		return nil, err
	}
	return m.startJob("add-vdev", pool, unlock, func(ctx context.Context) error {
		disks, err := m.requireAvailable(ctx, req.Disks)
		if err != nil {
			return err
		}
		devs := make([]string, len(disks))
		for i, d := range disks {
			devs[i] = stablePath(d)
		}
		return m.be.AddVdev(ctx, pool, req.Layout, devs)
	}, done), nil
}

type ReplaceRequest struct {
	Old     string `json:"old"`
	Disk    string `json:"disk"`
	Confirm string `json:"confirm"`
}

// findLeaf matches a leaf vdev by name, path or GUID.
func findLeaf(vs []Vdev, old string) *Vdev {
	for _, l := range leaves(vs) {
		if old != "" && (l.Name == old || l.Path == old || l.GUID == old) {
			return l
		}
	}
	return nil
}

func (m *Manager) Replace(ctx context.Context, pool string, req ReplaceRequest, done func(error)) (*Job, error) {
	if err := checkConfirm(req.Confirm, "ERASE"); err != nil {
		return nil, err
	}
	p, _, err := m.pool(ctx, pool)
	if err != nil {
		return nil, err
	}
	if findLeaf(p.Vdevs, req.Old) == nil {
		return nil, bad("%s isn't a disk in pool %s", req.Old, pool)
	}
	if err := validKernelName(req.Disk); err != nil {
		return nil, err
	}
	unlock, err := m.lock("replacing a disk", "pool:"+pool, "disk:"+req.Disk)
	if err != nil {
		return nil, err
	}
	if _, err := m.requireAvailable(ctx, []string{req.Disk}); err != nil {
		unlock()
		return nil, err
	}
	return m.startJob("replace", pool, unlock, func(ctx context.Context) error {
		disks, err := m.requireAvailable(ctx, []string{req.Disk})
		if err != nil {
			return err
		}
		return m.be.Replace(ctx, pool, req.Old, stablePath(disks[0]))
	}, done), nil
}

// protect refuses changes to a dataset tree that holds NoCapOS's data or an app's.
func (m *Manager) protect(ctx context.Context, root string, datasets []Dataset) error {
	src := m.be.DataSource(ctx, m.dataDir)
	if src == root || strings.HasPrefix(src, root+"/") {
		return bad("NoCapOS keeps its own data on %s, so it can't be removed", root)
	}
	var uses []PathUse
	if m.InUse != nil {
		uses = m.InUse(ctx)
	}
	for _, ds := range datasets {
		if ds.Name != root && !strings.HasPrefix(ds.Name, root+"/") {
			continue
		}
		mp := ds.Mountpoint
		if !strings.HasPrefix(mp, "/") {
			continue // none, legacy, -
		}
		if within(m.dataDir, mp) {
			return bad("NoCapOS keeps its own data in %s, so it can't be removed", mp)
		}
		for _, u := range uses {
			if within(u.Path, mp) {
				return bad("the app %s keeps data in %s; remove the app or move its data first", u.App, u.Path)
			}
		}
	}
	return nil
}

func (m *Manager) protectPool(ctx context.Context, name string) error {
	ds, err := m.be.Datasets(ctx, name)
	if err != nil {
		return fmt.Errorf("couldn't check what's on %s: %w", name, err)
	}
	return m.protect(ctx, name, ds)
}

func (m *Manager) Destroy(ctx context.Context, name, confirm string) error {
	if err := ValidPoolName(name); err != nil {
		return err
	}
	if err := checkConfirm(confirm, name); err != nil {
		return err
	}
	if _, _, err := m.pool(ctx, name); err != nil {
		return err
	}
	if err := m.protectPool(ctx, name); err != nil {
		return err
	}
	unlock, err := m.lock("destroying", "pool:"+name)
	if err != nil {
		return err
	}
	defer unlock()
	return m.withoutLocation(ctx, "pool:"+name, true, func() error { return m.be.Destroy(ctx, name) })
}

func (m *Manager) Export(ctx context.Context, name string) error {
	if _, _, err := m.pool(ctx, name); err != nil {
		return err
	}
	if err := m.protectPool(ctx, name); err != nil {
		return err
	}
	unlock, err := m.lock("exporting", "pool:"+name)
	if err != nil {
		return err
	}
	defer unlock()
	// The saved location stays, so Files shows it again after an import.
	return m.withoutLocation(ctx, "pool:"+name, false, func() error { return m.be.Export(ctx, name) })
}

func (m *Manager) Import(ctx context.Context, nameOrID string) error {
	if ValidPoolName(nameOrID) != nil && !poolIDRe.MatchString(nameOrID) {
		return bad("pick a pool to import")
	}
	if err := m.needZFS(ctx); err != nil {
		return err
	}
	imp, err := m.be.Importable(ctx)
	if err != nil {
		return err
	}
	name := ""
	for _, ip := range imp {
		if ip.Name == nameOrID || ip.ID == nameOrID {
			name = ip.Name
		}
	}
	if name == "" {
		return notFound("no pool %s is waiting to be imported", nameOrID)
	}
	unlock, err := m.lock("importing", "pool:"+name)
	if err != nil {
		return err
	}
	defer unlock()
	if err := m.be.Import(ctx, nameOrID); err != nil {
		return err
	}
	m.restoreLocations(ctx)
	return nil
}

func (m *Manager) Scrub(ctx context.Context, name, action string) error {
	if action != "start" && action != "stop" {
		return bad("action must be start or stop")
	}
	p, _, err := m.pool(ctx, name)
	if err != nil {
		return err
	}
	if action == "stop" && p.Scan.State != "scanning" && p.Scan.State != "paused" {
		return bad("no scrub is running on %s", name)
	}
	return m.be.Scrub(ctx, name, action == "stop")
}

// SetPoolFiles shows or hides a pool in Files.
func (m *Manager) SetPoolFiles(ctx context.Context, name string, enabled bool) error {
	if !enabled {
		if err := ValidPoolName(name); err != nil {
			return err
		}
		return m.removeLocation(ctx, "pool:"+name)
	}
	p, _, err := m.pool(ctx, name)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(p.Mountpoint, "/") {
		return bad("%s isn't mounted, so Files can't show it", name)
	}
	return m.addLocation(ctx, "pool:"+name, "pool", name, name, m.be.FilesPath(p.Mountpoint))
}

// ---- datasets and snapshots ----

func (m *Manager) Datasets(ctx context.Context, pool string) ([]Dataset, error) {
	if err := ValidPoolName(pool); err != nil {
		return nil, err
	}
	if err := m.needZFS(ctx); err != nil {
		return nil, err
	}
	return m.be.Datasets(ctx, pool)
}

type DatasetRequest struct {
	Name        string `json:"name"`
	Quota       *int64 `json:"quota,omitempty"`
	Compression string `json:"compression,omitempty"`
}

func (m *Manager) checkDatasetReq(req DatasetRequest) error {
	if req.Quota != nil && *req.Quota < 0 {
		return bad("the quota can't be negative")
	}
	if req.Compression != "" {
		return validCompression(req.Compression, true)
	}
	return nil
}

func (m *Manager) CreateDataset(ctx context.Context, req DatasetRequest) error {
	if err := ValidChildDataset(req.Name); err != nil {
		return err
	}
	if err := m.checkDatasetReq(req); err != nil {
		return err
	}
	if err := m.needZFS(ctx); err != nil {
		return err
	}
	return m.be.CreateDataset(ctx, req.Name, req.Quota, req.Compression)
}

func (m *Manager) UpdateDataset(ctx context.Context, req DatasetRequest) error {
	if err := ValidDatasetName(req.Name); err != nil {
		return err
	}
	if err := m.checkDatasetReq(req); err != nil {
		return err
	}
	if req.Compression == "inherit" && !strings.Contains(req.Name, "/") {
		return bad("the pool itself has nothing to inherit from; pick lz4, zstd or off")
	}
	if err := m.needZFS(ctx); err != nil {
		return err
	}
	return m.be.UpdateDataset(ctx, req.Name, req.Quota, req.Compression)
}

func poolOfDataset(name string) string {
	return strings.SplitN(strings.SplitN(name, "@", 2)[0], "/", 2)[0]
}

func (m *Manager) DeleteDataset(ctx context.Context, name, confirm string) error {
	if err := ValidChildDataset(name); err != nil {
		return err
	}
	if err := checkConfirm(confirm, name); err != nil {
		return err
	}
	if err := m.needZFS(ctx); err != nil {
		return err
	}
	all, err := m.be.Datasets(ctx, poolOfDataset(name))
	if err != nil {
		return err
	}
	found := false
	for _, d := range all {
		found = found || d.Name == name
	}
	if !found {
		return notFound("no dataset named %s", name)
	}
	if err := m.protect(ctx, name, all); err != nil {
		return err
	}
	unlock, err := m.lock("deleting a dataset", "pool:"+poolOfDataset(name))
	if err != nil {
		return err
	}
	defer unlock()
	return m.be.DestroyDataset(ctx, name)
}

func (m *Manager) Snapshots(ctx context.Context, dataset string) ([]Snapshot, error) {
	if err := ValidDatasetName(dataset); err != nil {
		return nil, err
	}
	if err := m.needZFS(ctx); err != nil {
		return nil, err
	}
	return m.be.Snapshots(ctx, dataset)
}

func (m *Manager) CreateSnapshot(ctx context.Context, dataset, name string) error {
	if err := ValidDatasetName(dataset); err != nil {
		return err
	}
	if err := validSnapLabel(name); err != nil {
		return err
	}
	if err := m.needZFS(ctx); err != nil {
		return err
	}
	return m.be.Snapshot(ctx, dataset+"@"+name)
}

func (m *Manager) Rollback(ctx context.Context, full, confirm string) error {
	if err := ValidSnapshotName(full); err != nil {
		return err
	}
	if err := checkConfirm(confirm, full); err != nil {
		return err
	}
	if err := m.needZFS(ctx); err != nil {
		return err
	}
	ds := strings.SplitN(full, "@", 2)[0]
	all, err := m.be.Datasets(ctx, poolOfDataset(ds))
	if err != nil {
		return err
	}
	// Only the dataset itself rolls back (not its children).
	var own []Dataset
	for _, d := range all {
		if d.Name == ds {
			own = append(own, d)
		}
	}
	if src := m.be.DataSource(ctx, m.dataDir); src == ds {
		return bad("NoCapOS keeps its own data on %s, so it can't be rolled back while running", ds)
	}
	for _, d := range own {
		if strings.HasPrefix(d.Mountpoint, "/") && within(m.dataDir, d.Mountpoint) {
			return bad("NoCapOS keeps its own data on %s, so it can't be rolled back while running", ds)
		}
	}
	unlock, err := m.lock("rolling back", "pool:"+poolOfDataset(ds))
	if err != nil {
		return err
	}
	defer unlock()
	return m.be.Rollback(ctx, full)
}

func (m *Manager) DeleteSnapshot(ctx context.Context, full string) error {
	if err := ValidSnapshotName(full); err != nil {
		return err
	}
	if err := m.needZFS(ctx); err != nil {
		return err
	}
	return m.be.DestroySnapshot(ctx, full)
}

// ---- Files locations ----

func (m *Manager) locationIDs(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	if m.st == nil {
		return out
	}
	locs, err := m.st.StorageLocations(ctx)
	if err != nil {
		return out
	}
	for _, l := range locs {
		out[l.ID] = true
	}
	return out
}

func (m *Manager) addLocation(ctx context.Context, id, kind, target, name, path string) error {
	if err := m.st.SaveStorageLocation(ctx, store.StorageLocation{ID: id, Kind: kind, Target: target, Name: name, Path: path}); err != nil {
		return err
	}
	if m.files != nil && m.be.Mounted(path) {
		m.files.AddRoot(files.Root{ID: id, Name: name, Path: path})
	}
	return nil
}

func (m *Manager) removeLocation(ctx context.Context, id string) error {
	if m.files != nil {
		m.files.RemoveRoot(id)
	}
	err := m.st.DeleteStorageLocation(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	return err
}

// withoutLocation hides a location from Files while fn runs; forget drops
// the saved location when fn succeeds, otherwise it comes back.
func (m *Manager) withoutLocation(ctx context.Context, id string, forget bool, fn func() error) error {
	if m.files != nil {
		m.files.RemoveRoot(id)
	}
	err := fn()
	if err == nil && forget {
		if derr := m.st.DeleteStorageLocation(ctx, id); derr != nil && !errors.Is(derr, store.ErrNotFound) {
			m.log.Warn("storage: forget location", "id", id, "err", derr)
		}
	}
	m.restoreLocations(ctx)
	return err
}

// restoreLocations offers saved locations in Files when they're mounted.
func (m *Manager) restoreLocations(ctx context.Context) {
	if m.st == nil || m.files == nil {
		return
	}
	locs, err := m.st.StorageLocations(ctx)
	if err != nil {
		return
	}
	for _, l := range locs {
		if m.be.Mounted(l.Path) {
			m.files.AddRoot(files.Root{ID: l.ID, Name: l.Name, Path: l.Path})
		} else {
			m.files.RemoveRoot(l.ID)
		}
	}
}

// ---- settings ----

type Settings struct {
	AutoScrub bool `json:"auto_scrub"`
}

func (m *Manager) Settings(ctx context.Context) (Settings, error) {
	v, err := m.st.Setting(ctx, keyAutoScrub)
	return Settings{AutoScrub: v != "0"}, err
}

func (m *Manager) SetSettings(ctx context.Context, s Settings) error {
	v := "1"
	if !s.AutoScrub {
		v = "0"
	}
	return m.st.SetSetting(ctx, keyAutoScrub, v)
}

// ---- health ----

func (m *Manager) Alerts() []Alert {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Alert{}, m.alerts...)
}

// SubscribeAlerts streams the alert list whenever it changes.
func (m *Manager) SubscribeAlerts() (<-chan []Alert, func()) {
	ch := make(chan []Alert, 4)
	m.mu.Lock()
	m.subs[ch] = struct{}{}
	m.mu.Unlock()
	return ch, func() {
		m.mu.Lock()
		delete(m.subs, ch)
		m.mu.Unlock()
	}
}

func (m *Manager) setAlerts(list []Alert) {
	sort.Slice(list, func(i, j int) bool { return list[i].Key < list[j].Key })
	m.mu.Lock()
	old := map[string]bool{}
	for _, a := range m.alerts {
		old[a.Key] = true
	}
	changed := len(list) != len(m.alerts)
	for _, a := range list {
		if !old[a.Key] {
			changed = true
			m.log.Warn("storage alert", "title", a.Title, "detail", a.Body)
		}
	}
	m.alerts = list
	if changed {
		for ch := range m.subs {
			select {
			case ch <- append([]Alert{}, list...):
			default:
			}
		}
	}
	m.mu.Unlock()
}

// computeAlerts turns pool and disk health into alerts.
func computeAlerts(pools []Pool, disks []Disk) []Alert {
	list := []Alert{}
	for _, p := range pools {
		switch p.Health {
		case "ONLINE", "":
		case "DEGRADED":
			list = append(list, Alert{Key: "pool:" + p.Name + ":" + p.Health, Level: "warning", Title: "Pool " + p.Name + " is degraded",
				Body: "A disk in " + p.Name + " has failed or is missing. Your data is still there, but replace the disk soon."})
		default:
			list = append(list, Alert{Key: "pool:" + p.Name + ":" + p.Health, Level: "error", Title: "Pool " + p.Name + " is " + strings.ToLower(p.Health),
				Body: "ZFS can't use " + p.Name + " right now. Open Storage to see which disks are affected."})
		}
	}
	for _, d := range disks {
		level, why := smartProblem(d.Smart)
		if level == "" {
			continue
		}
		name := firstNonEmpty(d.Model, d.Name)
		title := "Disk " + name + " needs attention"
		if level == "error" {
			title = "Disk " + name + " is failing"
		}
		list = append(list, Alert{Key: "smart:" + d.Name + ":" + d.Serial + ":" + level, Level: level, Title: title,
			Body: strings.ToUpper(why[:1]) + why[1:] + " (" + d.Name + "). Back up what's on it and plan to replace it."})
	}
	return list
}

func (m *Manager) healthCheck(ctx context.Context) {
	t := m.be.Tools(ctx)
	if !t.ZFSInstalled && !t.SmartInstalled {
		m.setAlerts([]Alert{})
		return
	}
	sc, err := m.scanAll(ctx)
	if err != nil {
		m.log.Warn("storage health check", "err", err)
		return
	}
	if t.SmartInstalled {
		for i := range sc.disks {
			sc.disks[i].Smart = m.readSmart(ctx, sc.disks[i])
		}
	}
	m.setAlerts(computeAlerts(sc.pools, sc.disks))
	m.autoScrub(ctx, sc.pools, time.Now())
	m.restoreLocations(ctx)
}

// autoScrub starts a monthly scrub on healthy pools that haven't had one.
func (m *Manager) autoScrub(ctx context.Context, pools []Pool, now time.Time) {
	if s, err := m.Settings(ctx); err != nil || !s.AutoScrub {
		return
	}
	for _, p := range pools {
		if p.Health != "ONLINE" || p.Scan.State == "scanning" || p.Scan.State == "paused" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, p.Scan.Finished); err == nil && now.Sub(t) < scrubEvery {
			continue
		}
		key := keyLastScrubPfx + p.Name
		if v, _ := m.st.Setting(ctx, key); v != "" {
			if last, err := strconv.ParseInt(v, 10, 64); err == nil && now.Sub(time.Unix(last, 0)) < scrubEvery {
				continue
			}
		}
		if err := m.be.Scrub(ctx, p.Name, false); err != nil {
			m.log.Warn("storage: monthly scrub", "pool", p.Name, "err", err)
			continue
		}
		_ = m.st.SetSetting(ctx, key, strconv.FormatInt(now.Unix(), 10))
		m.log.Info("storage: started monthly scrub", "pool", p.Name)
	}
}

// Run restores Files locations and checks health every 10 minutes.
func (m *Manager) Run(ctx context.Context) {
	if !m.supported() {
		return
	}
	m.restoreLocations(ctx)
	first := 30 * time.Second
	if m.demo {
		first = 3 * time.Second
	}
	timer := time.NewTimer(first)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		m.healthCheck(cctx)
		cancel()
		timer.Reset(healthInterval)
	}
}
