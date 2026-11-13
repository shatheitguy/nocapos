package storage

import (
	"context"
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Manager side of the Extras: every rule is checked here before the backend
// runs anything.

type extState struct {
	mu         sync.Mutex
	unlockedAt map[string]time.Time
}

func (m *Manager) ex() (Extras, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}
	x, ok := m.be.(Extras)
	if !ok {
		return nil, ErrUnsupported
	}
	return x, nil
}

func (m *Manager) getJSON(ctx context.Context, key string, v any) {
	if m.st == nil {
		return
	}
	if s, err := m.st.Setting(ctx, key); err == nil && s != "" {
		_ = json.Unmarshal([]byte(s), v)
	}
}

func (m *Manager) setJSON(ctx context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return m.st.SetSetting(ctx, key, string(b))
}

const (
	keySnapSchedules = "storage.snap_schedules"
	keySpindown      = "storage.spindown"
	keyAutoLock      = "storage.auto_lock"
	keyScrubEvery    = "storage.scrub_every"
	keyScrubHistPfx  = "storage.scrub_history."
)

// ================= boot drive =================

// BootDrive is what the Storage app shows about the system drive.
type BootDrive struct {
	Source     string           `json:"source"`
	FSType     string           `json:"fstype"`
	Size       int64            `json:"size"`
	Used       int64            `json:"used"`
	Avail      int64            `json:"avail"`
	Disks      []Disk           `json:"disks"`
	Pool       *Pool            `json:"pool,omitempty"`
	Mirrored   bool             `json:"mirrored"`
	Degraded   bool             `json:"degraded"`
	EFI        bool             `json:"efi"`
	Platform   string           `json:"platform"` // pc | pi
	CanMirror  bool             `json:"can_mirror"`
	Mode       string           `json:"mode,omitempty"` // attach | replace
	Why        string           `json:"why,omitempty"`  // not-zfs | pi | sd | arch | already | unknown
	WhyText    string           `json:"why_text,omitempty"`
	MinSize    int64            `json:"min_size,omitempty"`
	Candidates []string         `json:"candidates"`
	Members    []BootPoolMember `json:"members,omitempty"`

	source  Disk
	espPart int
	arch    string
}

var whyText = map[string]string{
	"not-zfs": "The system drive uses a regular filesystem. A live system can't be turned into a mirror safely without reinstalling: " +
		"adding a software RAID layer under a running filesystem risks losing it. To get a mirrored system drive, reinstall NoCapOS " +
		"and pick \"ZFS mirror\" in the installer. Until then, keep backups and watch the drive's health here.",
	"pi": "This board's firmware only starts from one card or drive, so a second drive can't take over by itself if the first fails. " +
		"Keep a backup of the system card instead.",
	"sd":      "The system runs from an SD or eMMC card, which can't be mirrored to a second drive. Keep a backup of the card instead.",
	"arch":    "Mirroring the system drive is supported on 64-bit PCs and 64-bit ARM boards that start with UEFI.",
	"already": "The system drive is already mirrored. If one drive fails, the other keeps NoCapOS running.",
	"unknown": "NoCapOS couldn't work out which drive the system starts from, so it won't touch it.",
}

var partNumRe = regexp.MustCompile(`-part([0-9]+)$`)

// leafPart is the kernel partition name and number of a pool leaf.
func leafPart(l *Vdev, links map[string]string, disk string) int {
	p := firstNonEmpty(l.Path, l.Name)
	if mm := partNumRe.FindStringSubmatch(p); mm != nil {
		n, _ := strconv.Atoi(mm[1])
		return n
	}
	if k := resolveDev(p, links); k != "" {
		return partNumber(k, disk)
	}
	return 0
}

func dataLeaves(vs []Vdev) []*Vdev {
	var out []*Vdev
	for i := range vs {
		if _, group := groupNames[vs[i].Type]; group {
			continue
		}
		out = append(out, leaves(vs[i:i+1])...)
	}
	return out
}

func (m *Manager) Boot(ctx context.Context) (*BootDrive, error) {
	x, err := m.ex()
	if err != nil {
		return nil, err
	}
	bf, err := x.BootFacts(ctx)
	if err != nil {
		return nil, err
	}
	sc, err := m.scanAll(ctx)
	if err != nil {
		return nil, err
	}
	m.attachSmart(ctx, sc.disks)
	return bootFrom(bf, sc), nil
}

// bootFrom works out the boot drive and whether (and how) it can be mirrored.
func bootFrom(bf *BootFacts, sc *scan) *BootDrive {
	b := &BootDrive{Source: bf.Source, FSType: bf.FSType, Size: bf.Size, Used: bf.Used, Avail: bf.Avail, EFI: bf.EFI,
		Platform: "pc", Disks: []Disk{}, Candidates: []string{}, arch: bf.Arch}
	if strings.Contains(bf.Model, "Raspberry Pi") {
		b.Platform = "pi"
	}
	names := map[string]bool{}
	var failed []*Vdev
	if bf.FSType == "zfs" {
		root := poolOfDataset(bf.Source)
		for i := range sc.pools {
			if sc.pools[i].Name == root {
				b.Pool = &sc.pools[i]
			}
		}
		if b.Pool != nil {
			b.Mirrored = b.Pool.Layout == "mirror"
			b.Degraded = b.Pool.Health != "ONLINE"
			for _, l := range dataLeaves(b.Pool.Vdevs) {
				if l.Disk != "" && l.Health == "ONLINE" {
					names[l.Disk] = true
				} else if l.Health != "ONLINE" {
					failed = append(failed, l)
				}
			}
		}
	} else if k := resolveDev(bf.Source, sc.facts.Links); k != "" {
		if d := diskOf(sc.facts, k); d != "" {
			names[d] = true
		}
	}
	for _, d := range sc.disks {
		if names[d.Name] {
			b.Disks = append(b.Disks, d)
		}
	}
	set := func(why string) *BootDrive {
		b.Why, b.WhyText = why, whyText[why]
		return b
	}
	switch {
	case len(b.Disks) == 0:
		return set("unknown")
	case b.Platform == "pi":
		return set("pi")
	case strings.HasPrefix(b.Disks[0].Name, "mmcblk"):
		return set("sd")
	case bf.FSType != "zfs" || b.Pool == nil:
		return set("not-zfs")
	case bf.Arch != "amd64" && bf.Arch != "arm64", bf.Arch == "arm64" && !bf.EFI:
		return set("arch")
	case b.Mirrored && len(failed) == 0:
		return set("already")
	case b.Pool.Layout != "mirror" && b.Pool.Layout != "stripe":
		return set("unknown")
	}
	b.source = b.Disks[0]
	b.Mode = "attach"
	if len(failed) > 0 {
		b.Mode = "replace"
	}
	// Every pool with a partition on the boot disk joins (root and boot pools).
	for i := range sc.pools {
		p := &sc.pools[i]
		var on, broken *Vdev
		for _, l := range dataLeaves(p.Vdevs) {
			if l.Disk == b.source.Name && on == nil {
				on = l
			} else if l.Health != "ONLINE" && broken == nil {
				broken = l
			}
		}
		if on == nil {
			continue
		}
		mem := BootPoolMember{Pool: p.Name, Part: leafPart(on, sc.facts.Links, b.source.Name), Old: firstNonEmpty(on.Path, on.Name)}
		if b.Mode == "replace" {
			if broken == nil {
				continue // this pool still has both halves
			}
			mem.Old = firstNonEmpty(broken.GUID, broken.Path, broken.Name)
		}
		if mem.Part == 0 {
			return set("unknown")
		}
		b.Members = append(b.Members, mem)
	}
	if len(b.Members) == 0 {
		return set("unknown")
	}
	if bf.ESP != "" {
		if k := resolveDev(bf.ESP, sc.facts.Links); k != "" && diskOf(sc.facts, k) == b.source.Name {
			b.espPart = partNumber(k, b.source.Name)
		}
	}
	b.CanMirror = true
	b.MinSize = b.source.Size
	for _, d := range sc.disks {
		if d.Available && d.Size >= b.MinSize {
			b.Candidates = append(b.Candidates, d.Name)
		}
	}
	return b
}

// BootMirrorRequest picks the new disk; Confirm is its name.
type BootMirrorRequest struct {
	Disk    string `json:"disk"`
	Confirm string `json:"confirm,omitempty"`
}

// BootMirrorDryRun is what will happen, shown before anything runs.
type BootMirrorDryRun struct {
	Plan     BootMirrorPlan `json:"plan"`
	Disk     Disk           `json:"disk"`
	Commands [][]string     `json:"commands"`
	Confirm  string         `json:"confirm"`
}

func (m *Manager) bootPlan(ctx context.Context, disk string) (*BootMirrorDryRun, error) {
	if err := validKernelName(disk); err != nil {
		return nil, err
	}
	b, err := m.Boot(ctx)
	if err != nil {
		return nil, err
	}
	if !b.CanMirror {
		return nil, bad("%s", b.WhyText)
	}
	ds, err := m.requireAvailable(ctx, []string{disk})
	if err != nil {
		return nil, err
	}
	d := ds[0]
	if d.Size < b.MinSize {
		return nil, bad("%s is smaller than the system drive; pick one at least %d bytes big", disk, b.MinSize)
	}
	p := BootMirrorPlan{Mode: b.Mode, Source: b.source.Path, Target: d.Path, TargetByID: d.ByID, Members: b.Members,
		ESPPart: b.espPart, EFI: b.EFI, Arch: b.arch}
	return &BootMirrorDryRun{Plan: p, Disk: d, Commands: p.Steps(), Confirm: disk}, nil
}

func (m *Manager) PlanBootMirror(ctx context.Context, req BootMirrorRequest) (*BootMirrorDryRun, error) {
	return m.bootPlan(ctx, req.Disk)
}

func (m *Manager) MirrorBoot(ctx context.Context, req BootMirrorRequest, done func(error)) (*Job, error) {
	x, err := m.ex()
	if err != nil {
		return nil, err
	}
	if err := checkConfirm(req.Confirm, req.Disk); err != nil {
		return nil, err
	}
	if _, err := m.bootPlan(ctx, req.Disk); err != nil {
		return nil, err
	}
	unlock, err := m.lock("mirroring the system drive", "boot:mirror", "disk:"+req.Disk)
	if err != nil {
		return nil, err
	}
	return m.startJob("boot-mirror", req.Disk, unlock, func(ctx context.Context) error {
		dr, err := m.bootPlan(ctx, req.Disk) // re-checked at run time
		if err != nil {
			return err
		}
		return x.MirrorBoot(ctx, dr.Plan)
	}, done), nil
}

// ================= pool plans (dry run) =================

type PlanRequest struct {
	Action      string   `json:"action"` // create | add | support | replace
	Pool        string   `json:"pool,omitempty"`
	Name        string   `json:"name,omitempty"`
	Layout      string   `json:"layout,omitempty"`
	Kind        string   `json:"kind,omitempty"` // support: cache | log | special
	Disks       []string `json:"disks"`
	Old         string   `json:"old,omitempty"`
	Compression string   `json:"compression,omitempty"`
}

type PoolPlan struct {
	Action    string     `json:"action"`
	Layout    string     `json:"layout,omitempty"`
	Disks     []Disk     `json:"disks"`
	Raw       int64      `json:"raw"`
	Usable    int64      `json:"usable"`
	Tolerance int        `json:"tolerance"`
	Commands  [][]string `json:"commands"`
	Warnings  []string   `json:"warnings"`
	Confirm   string     `json:"confirm"`
}

// layoutTolerance is how many disks of one group can fail.
func layoutTolerance(layout string, n int) int {
	switch layout {
	case "mirror":
		return n - 1
	case "raidz1":
		return 1
	case "raidz2":
		return 2
	case "raidz3":
		return 3
	}
	return 0
}

// normLayout maps "single" (one disk on its own) to a one-disk stripe.
func normLayout(layout string, n int) (string, error) {
	if layout == "single" {
		if n != 1 {
			return "", bad("a single-disk pool uses exactly one disk")
		}
		return "stripe", nil
	}
	return layout, nil
}

func (m *Manager) PlanPool(ctx context.Context, req PlanRequest) (*PoolPlan, error) {
	if err := m.needZFS(ctx); err != nil {
		return nil, err
	}
	disks, err := m.requireAvailable(ctx, req.Disks)
	if err != nil {
		return nil, err
	}
	devs := make([]string, len(disks))
	sizes := make([]int64, len(disks))
	pl := &PoolPlan{Action: req.Action, Disks: disks, Warnings: []string{}, Confirm: "ERASE"}
	for i, d := range disks {
		devs[i], sizes[i] = stablePath(d), d.Size
		pl.Raw += d.Size
	}
	if layout, err := normLayout(req.Layout, len(disks)); err == nil {
		req.Layout = layout
	} else {
		return nil, err
	}
	switch req.Action {
	case "create":
		if err := ValidPoolName(req.Name); err != nil {
			return nil, err
		}
		if err := validLayout(req.Layout, len(disks)); err != nil {
			return nil, err
		}
		comp := firstNonEmpty(req.Compression, "lz4")
		if err := validCompression(comp, false); err != nil {
			return nil, err
		}
		pl.Layout, pl.Usable, pl.Tolerance = req.Layout, usable(req.Layout, sizes), layoutTolerance(req.Layout, len(disks))
		pl.Commands = [][]string{createPoolArgs(req.Name, req.Layout, devs, comp, PoolBase+"/"+req.Name)}
	case "add":
		p, _, err := m.pool(ctx, req.Pool)
		if err != nil {
			return nil, err
		}
		if err := validLayout(req.Layout, len(disks)); err != nil {
			return nil, err
		}
		pl.Layout, pl.Usable, pl.Tolerance = req.Layout, usable(req.Layout, sizes), layoutTolerance(req.Layout, len(disks))
		if p.Layout != req.Layout {
			pl.Warnings = append(pl.Warnings, "layout-mismatch")
		}
		if req.Layout == "stripe" && p.Layout != "stripe" {
			pl.Warnings = append(pl.Warnings, "stripe-weakens")
		}
		pl.Commands = [][]string{addVdevArgs(req.Pool, req.Layout, devs)}
	case "support":
		p, _, err := m.pool(ctx, req.Pool)
		if err != nil {
			return nil, err
		}
		w, err := checkSupport(p, req.Kind, disks)
		if err != nil {
			return nil, err
		}
		pl.Layout, pl.Warnings = req.Kind, w
		pl.Commands = [][]string{supportArgs(req.Pool, req.Kind, devs)}
	case "replace":
		p, _, err := m.pool(ctx, req.Pool)
		if err != nil {
			return nil, err
		}
		if findLeaf(p.Vdevs, req.Old) == nil {
			return nil, bad("%s isn't a disk in pool %s", req.Old, req.Pool)
		}
		if len(devs) != 1 {
			return nil, bad("pick one new disk")
		}
		pl.Commands = [][]string{replaceArgs(req.Pool, req.Old, devs[0])}
	default:
		return nil, bad("unknown action %q", req.Action)
	}
	return pl, nil
}

// ================= speed-boost drives, expand, RAM cache =================

// checkSupport checks a cache/log/special request and returns warnings.
func checkSupport(p *Pool, kind string, disks []Disk) ([]string, error) {
	if !supportKinds[kind] {
		return nil, bad("pick a cache, log or special drive")
	}
	if len(disks) == 0 || len(disks) > 4 {
		return nil, bad("pick 1 to 4 drives")
	}
	w := []string{}
	for _, d := range disks {
		if d.Rotational {
			w = append(w, "not-ssd")
			break
		}
	}
	switch kind {
	case "special":
		if len(disks) < 2 && p.Layout != "stripe" {
			return nil, bad("a metadata drive holds part of the pool, so it needs two drives (mirrored) to be as safe as the pool")
		}
		w = append(w, "special-permanent")
	case "log":
		if len(disks) == 1 {
			w = append(w, "log-single")
		}
	}
	return w, nil
}

type SupportRequest struct {
	Kind    string   `json:"kind"`
	Disks   []string `json:"disks"`
	Confirm string   `json:"confirm"`
}

func (m *Manager) AddSupport(ctx context.Context, pool string, req SupportRequest, done func(error)) (*Job, error) {
	x, err := m.ex()
	if err != nil {
		return nil, err
	}
	if err := checkConfirm(req.Confirm, "ERASE"); err != nil {
		return nil, err
	}
	p, _, err := m.pool(ctx, pool)
	if err != nil {
		return nil, err
	}
	keys := []string{"pool:" + pool}
	for _, d := range req.Disks {
		if err := validKernelName(d); err != nil {
			return nil, err
		}
		keys = append(keys, "disk:"+d)
	}
	disks, err := m.requireAvailable(ctx, req.Disks)
	if err != nil {
		return nil, err
	}
	if _, err := checkSupport(p, req.Kind, disks); err != nil {
		return nil, err
	}
	unlock, err := m.lock("adding a "+req.Kind+" drive", keys...)
	if err != nil {
		return nil, err
	}
	return m.startJob("add-"+req.Kind, pool, unlock, func(ctx context.Context) error {
		disks, err := m.requireAvailable(ctx, req.Disks)
		if err != nil {
			return err
		}
		devs := make([]string, len(disks))
		for i, d := range disks {
			devs[i] = stablePath(d)
		}
		return x.AddSupport(ctx, pool, req.Kind, devs)
	}, done), nil
}

// RemoveSupport takes a cache or log drive out (special drives can't leave).
func (m *Manager) RemoveSupport(ctx context.Context, pool, dev string) error {
	x, err := m.ex()
	if err != nil {
		return err
	}
	p, _, err := m.pool(ctx, pool)
	if err != nil {
		return err
	}
	var leaf *Vdev
	for i := range p.Vdevs {
		if t := p.Vdevs[i].Type; t == "cache" || t == "logs" {
			if l := findLeaf(p.Vdevs[i:i+1], dev); l != nil {
				leaf = l
			}
		}
	}
	if leaf == nil {
		return bad("%s isn't a cache or log drive of %s", dev, pool)
	}
	unlock, err := m.lock("removing a drive", "pool:"+pool)
	if err != nil {
		return err
	}
	defer unlock()
	return x.RemoveSupport(ctx, pool, firstNonEmpty(leaf.Path, leaf.Name))
}

// Expand grows a pool after its disks were replaced by bigger ones.
func (m *Manager) Expand(ctx context.Context, pool string) error {
	x, err := m.ex()
	if err != nil {
		return err
	}
	p, _, err := m.pool(ctx, pool)
	if err != nil {
		return err
	}
	var ls []string
	for _, l := range dataLeaves(p.Vdevs) {
		if l.Health == "ONLINE" {
			ls = append(ls, firstNonEmpty(l.Path, l.Name))
		}
	}
	unlock, err := m.lock("expanding", "pool:"+pool)
	if err != nil {
		return err
	}
	defer unlock()
	return x.Expand(ctx, pool, ls)
}

func (m *Manager) ARC(ctx context.Context) (*ARCStats, error) {
	x, err := m.ex()
	if err != nil {
		return nil, err
	}
	if err := m.needZFS(ctx); err != nil {
		return nil, err
	}
	return x.ARC(ctx)
}

// ================= scrub schedule and history =================

var scrubDays = map[string]int{"weekly": 7, "monthly": 30, "quarterly": 91}

func (m *Manager) scrubInterval(ctx context.Context) time.Duration {
	v, _ := m.st.Setting(ctx, keyScrubEvery)
	if d, ok := scrubDays[v]; ok {
		return time.Duration(d) * 24 * time.Hour
	}
	return scrubEvery
}

// ScrubResult is one finished scrub.
type ScrubResult struct {
	Finished string `json:"finished"`
	Errors   int64  `json:"errors"`
	Function string `json:"function"`
}

// recordScans keeps the last 12 finished scrubs and resilvers per pool.
func (m *Manager) recordScans(ctx context.Context, pools []Pool) {
	if m.st == nil {
		return
	}
	for _, p := range pools {
		if p.Scan.State != "finished" || p.Scan.Finished == "" {
			continue
		}
		var hist []ScrubResult
		m.getJSON(ctx, keyScrubHistPfx+p.Name, &hist)
		if len(hist) > 0 && hist[len(hist)-1].Finished == p.Scan.Finished {
			continue
		}
		r := ScrubResult{Finished: p.Scan.Finished, Function: p.Scan.Function}
		if p.Scan.Errors != nil {
			r.Errors = *p.Scan.Errors
		}
		hist = append(hist, r)
		if len(hist) > 12 {
			hist = hist[len(hist)-12:]
		}
		_ = m.setJSON(ctx, keyScrubHistPfx+p.Name, hist)
	}
}

func (m *Manager) ScrubHistory(ctx context.Context, pool string) ([]ScrubResult, error) {
	if err := ValidPoolName(pool); err != nil {
		return nil, err
	}
	hist := []ScrubResult{}
	m.getJSON(ctx, keyScrubHistPfx+pool, &hist)
	return hist, nil
}

// ================= spin-down =================

type SpindownDisk struct {
	Name    string `json:"name"`
	ByID    string `json:"by_id"`
	Model   string `json:"model,omitempty"`
	Minutes int    `json:"minutes"`
}

func (m *Manager) Spindown(ctx context.Context) ([]SpindownDisk, error) {
	if _, err := m.ex(); err != nil {
		return nil, err
	}
	sc, err := m.scanAll(ctx)
	if err != nil {
		return nil, err
	}
	saved := map[string]int{}
	m.getJSON(ctx, keySpindown, &saved)
	out := []SpindownDisk{}
	for _, d := range sc.disks {
		if d.Rotational && d.ByID != "" {
			out = append(out, SpindownDisk{Name: d.Name, ByID: d.ByID, Model: d.Model, Minutes: saved[d.ByID]})
		}
	}
	return out, nil
}

func (m *Manager) SetSpindown(ctx context.Context, name string, minutes int) error {
	x, err := m.ex()
	if err != nil {
		return err
	}
	if _, err := hdparmValue(minutes); err != nil {
		return err
	}
	list, err := m.Spindown(ctx)
	if err != nil {
		return err
	}
	byID := ""
	for _, d := range list {
		if d.Name == name {
			byID = d.ByID
		}
	}
	if byID == "" {
		return bad("only hard drives can spin down")
	}
	saved := map[string]int{}
	m.getJSON(ctx, keySpindown, &saved)
	saved[byID] = minutes
	rules := []SpindownRule{}
	for _, d := range list { // only drives still connected are written
		if v, ok := saved[d.ByID]; ok {
			rules = append(rules, SpindownRule{ByID: d.ByID, Minutes: v})
		}
	}
	unlock, err := m.lock("setting spin-down", "spindown:all")
	if err != nil {
		return err
	}
	defer unlock()
	if err := m.setJSON(ctx, keySpindown, saved); err != nil {
		return err
	}
	return x.ApplySpindown(ctx, rules)
}

// ================= snapshots: schedules, clone, browse =================

// SnapSchedule keeps automatic snapshots of a dataset (counts to keep; 0 = off).
type SnapSchedule struct {
	Dataset string `json:"dataset"`
	Hourly  int    `json:"hourly"`
	Daily   int    `json:"daily"`
	Weekly  int    `json:"weekly"`
	Monthly int    `json:"monthly"`
}

var periods = []string{"hourly", "daily", "weekly", "monthly"}

func (s SnapSchedule) keep(period string) int {
	switch period {
	case "hourly":
		return s.Hourly
	case "daily":
		return s.Daily
	case "weekly":
		return s.Weekly
	}
	return s.Monthly
}

var autoSnapRe = regexp.MustCompile(`^auto-(hourly|daily|weekly|monthly)-[0-9]{8}-[0-9]{4}$`)

// periodStart is the start of the hour, day, ISO week or month holding t (UTC).
func periodStart(period string, t time.Time) time.Time {
	t = t.UTC()
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	switch period {
	case "hourly":
		return t.Truncate(time.Hour)
	case "daily":
		return day
	case "weekly":
		wd := (int(day.Weekday()) + 6) % 7 // Monday = 0
		return day.AddDate(0, 0, -wd)
	}
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func autoSnapName(period string, t time.Time) string {
	return "auto-" + period + "-" + t.UTC().Format("20060102-1504")
}

// snapPlan says which automatic snapshots to take and which to delete. Only
// snapshots named by the schedule are ever deleted, never hand-made ones.
func snapPlan(s SnapSchedule, existing []Snapshot, now time.Time) (take, prune []string) {
	for _, period := range periods {
		keep := s.keep(period)
		var mine []Snapshot
		for _, sn := range existing {
			_, label, _ := strings.Cut(sn.Name, "@")
			if mm := autoSnapRe.FindStringSubmatch(label); mm != nil && mm[1] == period {
				mine = append(mine, sn)
			}
		}
		sort.Slice(mine, func(i, j int) bool { return mine[i].Created.Before(mine[j].Created) })
		if keep > 0 {
			start := periodStart(period, now)
			due := true
			for _, sn := range mine {
				if !sn.Created.Before(start) {
					due = false
				}
			}
			if due {
				take = append(take, s.Dataset+"@"+autoSnapName(period, now))
				mine = append(mine, Snapshot{}) // the new one counts toward keep
			}
		}
		if extra := len(mine) - keep; extra > 0 && keep > 0 {
			for _, sn := range mine[:extra] {
				prune = append(prune, sn.Name)
			}
		}
	}
	return take, prune
}

func (m *Manager) SnapSchedules(ctx context.Context) []SnapSchedule {
	list := []SnapSchedule{}
	m.getJSON(ctx, keySnapSchedules, &list)
	return list
}

func (m *Manager) SetSnapSchedule(ctx context.Context, s SnapSchedule) error {
	if err := ValidDatasetName(s.Dataset); err != nil {
		return err
	}
	for _, n := range []int{s.Hourly, s.Daily, s.Weekly, s.Monthly} {
		if n < 0 || n > 500 {
			return bad("keep between 0 and 500 snapshots of each kind")
		}
	}
	if err := m.needZFS(ctx); err != nil {
		return err
	}
	ds, err := m.be.Datasets(ctx, poolOfDataset(s.Dataset))
	if err != nil {
		return err
	}
	found := false
	for _, d := range ds {
		found = found || d.Name == s.Dataset
	}
	if !found {
		return notFound("no dataset named %s", s.Dataset)
	}
	list := m.SnapSchedules(ctx)
	out := []SnapSchedule{}
	for _, x := range list {
		if x.Dataset != s.Dataset {
			out = append(out, x)
		}
	}
	if s.Hourly+s.Daily+s.Weekly+s.Monthly > 0 {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dataset < out[j].Dataset })
	return m.setJSON(ctx, keySnapSchedules, out)
}

// runSchedules takes and prunes automatic snapshots.
func (m *Manager) runSchedules(ctx context.Context, now time.Time) {
	t := m.be.Tools(ctx)
	if !t.ZFSInstalled || !t.ZFSModule {
		return
	}
	for _, s := range m.SnapSchedules(ctx) {
		existing, err := m.be.Snapshots(ctx, s.Dataset)
		if err != nil {
			continue // dataset gone or pool exported
		}
		take, prune := snapPlan(s, existing, now)
		for _, full := range take {
			if err := m.be.Snapshot(ctx, full); err != nil {
				m.log.Warn("storage: scheduled snapshot", "snapshot", full, "err", err)
			}
		}
		for _, full := range prune {
			if ValidSnapshotName(full) != nil {
				continue
			}
			if err := m.be.DestroySnapshot(ctx, full); err != nil {
				m.log.Warn("storage: prune snapshot", "snapshot", full, "err", err)
			}
		}
	}
}

// SnapOverview is one dataset on the Snapshots page.
type SnapOverview struct {
	Dataset    string        `json:"dataset"`
	Mountpoint string        `json:"mountpoint"`
	Count      int           `json:"count"`
	Used       int64         `json:"used"`
	Latest     *time.Time    `json:"latest,omitempty"`
	Schedule   *SnapSchedule `json:"schedule,omitempty"`
}

func (m *Manager) SnapshotOverview(ctx context.Context) ([]SnapOverview, error) {
	if err := m.needZFS(ctx); err != nil {
		return nil, err
	}
	pools, err := m.be.Pools(ctx)
	if err != nil {
		return nil, err
	}
	sched := map[string]SnapSchedule{}
	for _, s := range m.SnapSchedules(ctx) {
		sched[s.Dataset] = s
	}
	out := []SnapOverview{}
	for _, p := range pools {
		ds, err := m.be.Datasets(ctx, p.Name)
		if err != nil {
			continue
		}
		for _, d := range ds {
			o := SnapOverview{Dataset: d.Name, Mountpoint: d.Mountpoint}
			if s, ok := sched[d.Name]; ok {
				o.Schedule = &s
			}
			if snaps, err := m.be.Snapshots(ctx, d.Name); err == nil {
				o.Count = len(snaps)
				for i := range snaps {
					o.Used += snaps[i].Used
					if o.Latest == nil || snaps[i].Created.After(*o.Latest) {
						c := snaps[i].Created
						o.Latest = &c
					}
				}
			}
			out = append(out, o)
		}
	}
	return out, nil
}

func (m *Manager) Clone(ctx context.Context, snap, target string) error {
	x, err := m.ex()
	if err != nil {
		return err
	}
	if err := ValidSnapshotName(snap); err != nil {
		return err
	}
	if err := ValidChildDataset(target); err != nil {
		return err
	}
	if poolOfDataset(target) != poolOfDataset(snap) {
		return bad("a copy has to live in the same pool as its snapshot")
	}
	if err := m.needZFS(ctx); err != nil {
		return err
	}
	return x.Clone(ctx, snap, target)
}

// snapDir is where a snapshot's files are readable, plus a folder inside it.
func snapDir(mountpoint, label, rel string) (string, error) {
	if !strings.HasPrefix(mountpoint, "/") {
		return "", bad("this dataset isn't mounted, so its snapshots can't be browsed")
	}
	if strings.ContainsRune(rel, 0) || len(rel) > 1024 {
		return "", bad("bad folder")
	}
	clean := path.Clean("/" + rel)
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return "", bad("bad folder")
		}
	}
	root := path.Clean(mountpoint) + "/.zfs/snapshot/" + label
	if clean == "/" {
		return root, nil
	}
	return root + clean, nil
}

func (m *Manager) BrowseSnapshot(ctx context.Context, full, rel string) ([]SnapEntry, error) {
	x, err := m.ex()
	if err != nil {
		return nil, err
	}
	if err := ValidSnapshotName(full); err != nil {
		return nil, err
	}
	if err := m.needZFS(ctx); err != nil {
		return nil, err
	}
	ds, label, _ := strings.Cut(full, "@")
	all, err := m.be.Datasets(ctx, poolOfDataset(ds))
	if err != nil {
		return nil, err
	}
	mp := ""
	for _, d := range all {
		if d.Name == ds {
			mp = d.Mountpoint
		}
	}
	dir, err := snapDir(mp, label, rel)
	if err != nil {
		return nil, err
	}
	return x.BrowseSnapshot(ctx, dir)
}

// ================= encrypted folders =================

type EncryptedRequest struct {
	Name       string `json:"name"`
	Passphrase string `json:"passphrase"`
	Repeat     string `json:"repeat"`
	Quota      *int64 `json:"quota,omitempty"`
	AutoLock   int    `json:"auto_lock_minutes,omitempty"`
}

func validAutoLock(n int) error {
	if n < 0 || n > 7*24*60 {
		return bad("auto-lock is between 0 minutes (off) and a week")
	}
	return nil
}

func (m *Manager) Encryption(ctx context.Context, pool string) ([]CryptInfo, error) {
	x, err := m.ex()
	if err != nil {
		return nil, err
	}
	if err := ValidPoolName(pool); err != nil {
		return nil, err
	}
	if err := m.needZFS(ctx); err != nil {
		return nil, err
	}
	list, err := x.Encryption(ctx, pool)
	if err != nil {
		return nil, err
	}
	auto := map[string]int{}
	m.getJSON(ctx, keyAutoLock, &auto)
	m.ext.mu.Lock()
	for i := range list {
		list[i].AutoLock = auto[list[i].Name]
		if t, ok := m.ext.unlockedAt[list[i].Name]; ok && list[i].Unlocked {
			list[i].UnlockedAt = t.UTC().Format(time.RFC3339)
		}
	}
	m.ext.mu.Unlock()
	return list, nil
}

func (m *Manager) CreateEncrypted(ctx context.Context, req EncryptedRequest) error {
	x, err := m.ex()
	if err != nil {
		return err
	}
	if err := ValidChildDataset(req.Name); err != nil {
		return err
	}
	if err := validPassphrase(req.Passphrase); err != nil {
		return err
	}
	if req.Passphrase != req.Repeat {
		return bad("the two passphrases don't match")
	}
	if req.Quota != nil && *req.Quota < 0 {
		return bad("the quota can't be negative")
	}
	if err := validAutoLock(req.AutoLock); err != nil {
		return err
	}
	if err := m.needZFS(ctx); err != nil {
		return err
	}
	if err := x.CreateEncrypted(ctx, req.Name, req.Passphrase, req.Quota); err != nil {
		return err
	}
	m.markUnlocked(req.Name)
	return m.setAutoLock(ctx, req.Name, req.AutoLock)
}

func (m *Manager) setAutoLock(ctx context.Context, name string, minutes int) error {
	auto := map[string]int{}
	m.getJSON(ctx, keyAutoLock, &auto)
	if minutes == 0 {
		delete(auto, name)
	} else {
		auto[name] = minutes
	}
	return m.setJSON(ctx, keyAutoLock, auto)
}

func (m *Manager) SetAutoLock(ctx context.Context, name string, minutes int) error {
	if err := ValidChildDataset(name); err != nil {
		return err
	}
	if err := validAutoLock(minutes); err != nil {
		return err
	}
	return m.setAutoLock(ctx, name, minutes)
}

func (m *Manager) markUnlocked(name string) {
	m.ext.mu.Lock()
	if m.ext.unlockedAt == nil {
		m.ext.unlockedAt = map[string]time.Time{}
	}
	m.ext.unlockedAt[name] = time.Now()
	m.ext.mu.Unlock()
}

// encryptionRoot checks name is an encrypted dataset that holds its own key.
func (m *Manager) encryptionRoot(ctx context.Context, x Extras, name string) error {
	list, err := x.Encryption(ctx, poolOfDataset(name))
	if err != nil {
		return err
	}
	for _, c := range list {
		if c.Name == name {
			if !c.Encrypted {
				return bad("%s isn't encrypted", name)
			}
			if c.Root != "" && c.Root != name {
				return bad("%s is locked and unlocked with %s", name, c.Root)
			}
			return nil
		}
	}
	return notFound("no dataset named %s", name)
}

func (m *Manager) Unlock(ctx context.Context, name, passphrase string) error {
	x, err := m.ex()
	if err != nil {
		return err
	}
	if err := ValidChildDataset(name); err != nil {
		return err
	}
	if err := validPassphrase(passphrase); err != nil {
		return bad("that passphrase isn't right")
	}
	if err := m.needZFS(ctx); err != nil {
		return err
	}
	if err := m.encryptionRoot(ctx, x, name); err != nil {
		return err
	}
	if err := x.Unlock(ctx, name, passphrase); err != nil {
		return err
	}
	m.markUnlocked(name)
	m.restoreLocations(ctx)
	return nil
}

func (m *Manager) Lock(ctx context.Context, name string) error {
	x, err := m.ex()
	if err != nil {
		return err
	}
	if err := ValidChildDataset(name); err != nil {
		return err
	}
	if err := m.needZFS(ctx); err != nil {
		return err
	}
	if err := m.encryptionRoot(ctx, x, name); err != nil {
		return err
	}
	all, err := m.be.Datasets(ctx, poolOfDataset(name))
	if err != nil {
		return err
	}
	if err := m.protect(ctx, name, all); err != nil {
		return err
	}
	unlock, err := m.lock("locking", "crypt:"+name)
	if err != nil {
		return err
	}
	defer unlock()
	if err := x.Lock(ctx, name); err != nil {
		return err
	}
	m.ext.mu.Lock()
	delete(m.ext.unlockedAt, name)
	m.ext.mu.Unlock()
	return nil
}

// autoLock locks folders that have been open longer than their limit.
func (m *Manager) autoLock(ctx context.Context, now time.Time) {
	auto := map[string]int{}
	m.getJSON(ctx, keyAutoLock, &auto)
	if len(auto) == 0 {
		return
	}
	var due []string
	m.ext.mu.Lock()
	for name, t := range m.ext.unlockedAt {
		if mins, ok := auto[name]; ok && mins > 0 && now.Sub(t) >= time.Duration(mins)*time.Minute {
			due = append(due, name)
		}
	}
	m.ext.mu.Unlock()
	for _, name := range due {
		if err := m.Lock(ctx, name); err != nil {
			m.log.Info("storage: auto-lock postponed", "dataset", name, "err", err)
		} else {
			m.log.Info("storage: auto-locked", "dataset", name)
		}
	}
}

// ================= flexible array (preview) =================

func (m *Manager) flexDisks(ctx context.Context, req FlexRequest) ([]Disk, []Disk, error) {
	all := append(append([]string{}, req.Data...), req.Parity...)
	disks, err := m.requireAvailable(ctx, all)
	if err != nil {
		return nil, nil, err
	}
	return disks[:len(req.Data)], disks[len(req.Data):], nil
}

func (m *Manager) PlanFlex(ctx context.Context, req FlexRequest) (*FlexPlan, error) {
	if _, err := m.ex(); err != nil {
		return nil, err
	}
	data, parity, err := m.flexDisks(ctx, req)
	if err != nil {
		return nil, err
	}
	p, err := planFlex(data, parity)
	if err != nil {
		return nil, err
	}
	p.Enabled = m.demo || FlexEnabled()
	return p, nil
}

func (m *Manager) CreateFlex(ctx context.Context, req FlexRequest, done func(error)) (*Job, error) {
	x, err := m.ex()
	if err != nil {
		return nil, err
	}
	if !m.demo && !FlexEnabled() {
		return nil, bad("the flexible array is a preview; it's off on this server")
	}
	if err := checkConfirm(req.Confirm, "ERASE"); err != nil {
		return nil, err
	}
	if _, err := m.PlanFlex(ctx, req); err != nil {
		return nil, err
	}
	keys := []string{"flex:array"}
	for _, d := range append(append([]string{}, req.Data...), req.Parity...) {
		keys = append(keys, "disk:"+d)
	}
	unlock, err := m.lock("building the flexible array", keys...)
	if err != nil {
		return nil, err
	}
	return m.startJob("flex-create", "array", unlock, func(ctx context.Context) error {
		data, parity, err := m.flexDisks(ctx, req)
		if err != nil {
			return err
		}
		p, err := planFlex(data, parity)
		if err != nil {
			return err
		}
		var devs []string
		for _, d := range append(data, parity...) {
			devs = append(devs, d.Path)
		}
		return x.CreateFlexArray(ctx, *p, devs)
	}, done), nil
}

// ================= background =================

// runExtras takes scheduled snapshots and auto-locks folders every minute.
func (m *Manager) runExtras(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			m.runSchedules(cctx, now)
			m.autoLock(cctx, now)
			cancel()
		}
	}
}
