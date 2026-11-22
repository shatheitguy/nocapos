package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/appdata"
	"alfaos/alfad/internal/filesync"
	"alfaos/alfad/internal/store"
)

// Failover pairs: files. The administrators choose which locations sync
// (the NoCap Drive and other Files locations, people's home folders on
// native Linux installs, the App data folder); container stacks and the
// Shield's DNS settings ("config") always sync with the settings. Each
// server keeps an index of its files (filesync), rescans a location when
// its watcher sees a change (inotify on Linux) and every few minutes
// anyway, and reads the other's index every few seconds, downloading what
// changed over the link (HTTPS to the pinned certificate, the link's
// token), resuming partial downloads, within the bandwidth limit and
// schedule the administrators set.
//
// App data and consistency: an app's folder is only sent while the app is
// stopped on the sending server (a running database's files aren't
// consistent on their own), so the other server always holds the app's
// data as it was when it last stopped. Snapshot-based copies of running
// apps (ZFS send/receive) aren't part of this yet.

const (
	failoverFilesEvery    = 15 * time.Second // reading the other server's file index
	failoverFullScanEvery = 10 * time.Minute // rescanning even without a watcher event
	failoverFilePage      = 2000
	failoverSizesKeep     = 5 * time.Minute
	failoverConfigLoc     = "config"
)

// failoverOptions are a pair's choices, the same on both servers.
type failoverOptions struct {
	Files      []string `json:"files"`       // location ids that sync
	LimitKBps  int      `json:"limit_kbps"`  // download limit, 0 = none
	WindowFrom string   `json:"window_from"` // "HH:MM": files only sync from…
	WindowTo   string   `json:"window_to"`   // …to (empty = any time)
}

func parseFailoverOptions(s string) failoverOptions {
	var o failoverOptions
	_ = json.Unmarshal([]byte(s), &o)
	return o
}

func (o failoverOptions) has(loc string) bool {
	if loc == failoverConfigLoc {
		return true
	}
	for _, l := range o.Files {
		if l == loc {
			return true
		}
	}
	return false
}

// clockMinutes parses "HH:MM" (-1 when it isn't one).
func clockMinutes(s string) int {
	h, m, ok := strings.Cut(s, ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return -1
	}
	return hh*60 + mm
}

// inWindow: may files sync at t (no window = always; from > to wraps midnight).
func (o failoverOptions) inWindow(t time.Time) bool {
	from, to := clockMinutes(o.WindowFrom), clockMinutes(o.WindowTo)
	if from < 0 || to < 0 || from == to {
		return true
	}
	now := t.Hour()*60 + t.Minute()
	if from < to {
		return now >= from && now < to
	}
	return now >= from || now < to
}

// failoverLoc is a folder that can sync.
type failoverLoc struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"` // drive | homes | appdata | config
	Path string `json:"-"`
	loc  filesync.Location
}

// failoverLocations are this server's folders that can sync.
func (s *Server) failoverLocations(ctx context.Context) []failoverLoc {
	var out []failoverLoc
	if s.Config != nil {
		for _, r := range s.Config.FileRoots {
			if r.ID == "system" || r.Path == "/" {
				continue
			}
			out = append(out, failoverLoc{ID: r.ID, Name: r.Name, Kind: "drive", Path: r.Path})
		}
		if runtime.GOOS == "linux" && s.systemSync() != nil {
			out = append(out, failoverLoc{ID: "homes", Name: "Home folders", Kind: "homes", Path: "/home"})
		}
	}
	if s.appMover != nil {
		if root := s.appMover.Root(ctx); root != "" {
			out = append(out, failoverLoc{ID: appdata.RootID, Name: "App data", Kind: "appdata", Path: root})
		}
	}
	if s.Config != nil && s.Config.DataDir != "" {
		out = append(out, failoverLoc{ID: failoverConfigLoc, Name: "Containers and Shield", Kind: "config", Path: s.Config.DataDir})
	}
	for i := range out {
		out[i].loc = filesync.Location{ID: out[i].ID, Path: out[i].Path, Skip: s.failoverSkip(ctx, out[i])}
		if out[i].Kind == "homes" {
			out[i].loc.Owner = homeOwner(out[i].Path)
		}
	}
	return out
}

// configSkip: of the data folder, only stacks and the Shield's DNS settings sync.
func configSkip(rel string) bool {
	switch {
	case rel == "stacks", strings.HasPrefix(rel, "stacks/"), rel == "dns",
		rel == "dns/settings.json", rel == "dns/personal.json":
		return false
	}
	return true
}

// homesSkip: caches and the desktop's own trash don't sync.
func homesSkip(rel string) bool {
	_, rest, _ := strings.Cut(rel, "/")
	for _, p := range []string{".cache", ".local/share/Trash", ".local/share/containers", ".local/share/docker"} {
		if rest == p || strings.HasPrefix(rest, p+"/") {
			return true
		}
	}
	return false
}

func (s *Server) failoverSkip(ctx context.Context, l failoverLoc) func(string) bool {
	switch l.Kind {
	case "config":
		return configSkip
	case "homes":
		return homesSkip
	case "appdata":
		running := s.runningApps(ctx)
		return func(rel string) bool {
			top, _, _ := strings.Cut(rel, "/")
			return running[top]
		}
	}
	return nil
}

// runningApps: app ids (their App data folder names) with a running container.
func (s *Server) runningApps(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	if s.Docker == nil {
		return out
	}
	list, err := s.Docker.ListContainers(ctx, false)
	if err != nil {
		// Unsure what runs: treat every app as running (send nothing).
		return map[string]bool{"": true}
	}
	for _, c := range list {
		if c.State != "running" {
			continue
		}
		if id := c.Labels[appdata.LabelApp]; id != "" {
			out[id] = true
		}
		if c.Project != "" {
			out[c.Project] = true
		}
		out[c.Name] = true
	}
	return out
}

// ---------- the loop ----------

type fileLocStatus struct {
	Files    int       `json:"files"`
	Bytes    int64     `json:"bytes"`
	Scanned  time.Time `json:"scanned,omitzero"`
	Synced   time.Time `json:"synced,omitzero"`
	Error    string    `json:"error,omitempty"`
	Received int64     `json:"received"` // bytes downloaded since start
}

type failoverFilesState struct {
	once    sync.Once
	run     sync.Mutex
	kick    chan struct{}
	mu      sync.Mutex
	status  map[string]*fileLocStatus
	dirty   map[string]bool // a watcher saw a change
	full    time.Time       // last full scan
	sizes   map[string]int64
	sizesAt time.Time
	sizing  bool
	limiter *filesync.Limiter
	limit   int
	watched string // the folders the watcher follows, joined
	stopW   context.CancelFunc
}

func (s *Server) filesInit() *failoverFilesState {
	f := &s.failoverFiles
	f.once.Do(func() {
		f.kick = make(chan struct{}, 1)
		f.status, f.dirty, f.sizes = map[string]*fileLocStatus{}, map[string]bool{}, map[string]int64{}
	})
	return f
}

func (s *Server) failoverFilesKick() {
	select {
	case s.filesInit().kick <- struct{}{}:
	default:
	}
}

func (s *Server) failoverFilesLoop(ctx context.Context) {
	f := s.filesInit()
	t := time.NewTicker(failoverFilesEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-f.kick:
		}
		if err := s.failoverFilesOnce(ctx, false); err != nil {
			s.Log.Debug("failover files", "err", err)
		}
	}
}

func (f *failoverFilesState) loc(id string) *fileLocStatus {
	st := f.status[id]
	if st == nil {
		st = &fileLocStatus{}
		f.status[id] = st
	}
	return st
}

// failoverFilesOnce scans what changed here and takes what changed on the
// other server. force: "Sync now" (even while paused or outside the schedule).
func (s *Server) failoverFilesOnce(ctx context.Context, force bool) error {
	f := s.filesInit()
	f.run.Lock()
	defer f.run.Unlock()
	p, err := s.theFailover(ctx)
	if err != nil || p == nil || p.State != store.FailoverActive {
		s.watchFailoverFolders(ctx, nil)
		return err
	}
	o := parseFailoverOptions(p.Options)
	now := s.failoverInit().now()
	if !force && (p.Paused || !o.inWindow(now)) {
		return nil
	}
	var locs []failoverLoc
	for _, l := range s.failoverLocations(ctx) {
		if o.has(l.ID) {
			locs = append(locs, l)
		}
	}
	s.watchFailoverFolders(ctx, locs)
	f.mu.Lock()
	full := force || now.Sub(f.full) > failoverFullScanEvery
	if full {
		f.full = now
	}
	dirty := f.dirty
	f.dirty = map[string]bool{}
	f.mu.Unlock()
	idx := s.Store.FileIndex()
	for _, l := range locs {
		if !full && !dirty[l.ID] {
			continue
		}
		res, err := filesync.Scan(ctx, idx, l.loc)
		f.mu.Lock()
		st := f.loc(l.ID)
		if err != nil {
			st.Error = err.Error()
		} else {
			st.Files, st.Bytes, st.Scanned, st.Error = res.Files, res.Bytes, now, ""
		}
		f.mu.Unlock()
	}
	return s.failoverFilesPull(ctx, p, o, locs)
}

type fileFeed struct {
	Changes []filesync.Change `json:"changes"`
	Next    int64             `json:"next"`
	More    bool              `json:"more"`
	Seen    int64             `json:"seen"` // how far the sender has read the reader's index
}

func (s *Server) failoverFilesPull(ctx context.Context, p *store.FailoverPair, o failoverOptions, locs []failoverLoc) error {
	l, err := s.Store.ServerLink(ctx, p.NocapID)
	if err != nil {
		return err
	}
	self, err := s.nocapID()
	if err != nil {
		return err
	}
	cursor, _, err := s.Store.FailoverFileCursors(ctx, p.NocapID)
	if err != nil {
		return err
	}
	ids := make([]string, len(locs))
	byID := map[string]failoverLoc{}
	for i, x := range locs {
		ids[i], byID[x.ID] = x.ID, x
	}
	f := s.filesInit()
	f.mu.Lock()
	if o.LimitKBps != f.limit {
		f.limiter, f.limit = filesync.NewLimiter(int64(o.LimitKBps)<<10), o.LimitKBps
	}
	lim := f.limiter
	f.mu.Unlock()
	client, tok, err := s.failoverClient(ctx, l)
	if err != nil {
		return err
	}
	client.Timeout = 0 // big files take long; the context ends it
	fetch := func(ctx context.Context, loc, rel string, offset int64) (io.ReadCloser, error) {
		q := url.Values{"loc": {loc}, "path": {rel}, "offset": {strconv.FormatInt(offset, 10)}}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.URL+"/api/v1/failover/peer/files/file?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", peerAuthPrefix+tok)
		res, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if res.StatusCode != http.StatusOK {
			res.Body.Close()
			return nil, fmt.Errorf("the other server answered %d for %s", res.StatusCode, rel)
		}
		return res.Body, nil
	}
	for page := 0; page < 50; page++ {
		var feed fileFeed
		q := url.Values{"since": {strconv.FormatInt(cursor, 10)}, "locs": {strings.Join(ids, ",")}}
		if err := s.failoverCall(ctx, l, http.MethodGet, "/api/v1/failover/peer/files/changes?"+q.Encode(), nil, &feed); err != nil {
			return err
		}
		if feed.Seen > 0 {
			_ = s.Store.SetFailoverFileAcked(ctx, p.NocapID, feed.Seen)
		}
		_, acked, err := s.Store.FailoverFileCursors(ctx, p.NocapID)
		if err != nil {
			return err
		}
		perLoc := map[string][]filesync.Entry{}
		var order []string
		for _, c := range feed.Changes {
			if _, ok := byID[c.Loc]; !ok {
				continue
			}
			if perLoc[c.Loc] == nil {
				order = append(order, c.Loc)
			}
			perLoc[c.Loc] = append(perLoc[c.Loc], c.Entry)
		}
		var firstErr error
		for _, id := range order {
			x := byID[id]
			res, err := filesync.ApplyChanges(ctx, s.Store.FileIndex(), x.loc, perLoc[id], filesync.Apply{
				Self: self, SelfName: s.serverName(), Peer: p.NocapID, Acked: acked, Fetch: fetch, Wrap: lim.Reader,
			})
			f.mu.Lock()
			st := f.loc(id)
			st.Received += res.Bytes
			if err != nil {
				st.Error = err.Error()
			} else {
				st.Synced, st.Error = s.failoverInit().now(), ""
			}
			f.mu.Unlock()
			if err != nil && firstErr == nil {
				firstErr = err
			}
			if res.Conflicts > 0 {
				s.auditCtx(ctx, "", "failover.file_conflict", l.Name+" ("+l.NocapID+")", "", true, fmt.Sprintf("%d files in %s changed on both servers; conflict copies kept", res.Conflicts, x.Name))
			}
			if id == failoverConfigLoc && res.Applied > 0 {
				s.failoverConfigApplied(perLoc[id])
			}
		}
		if firstErr != nil {
			return firstErr // the cursor stays: the next pass tries again
		}
		cursor = feed.Next
		if err := s.Store.SetFailoverFileCursor(ctx, p.NocapID, cursor); err != nil {
			return err
		}
		if !feed.More {
			break
		}
	}
	now := s.failoverInit().now()
	f.mu.Lock()
	for _, x := range locs {
		if st := f.loc(x.ID); st.Error == "" {
			st.Synced = now
		}
	}
	f.mu.Unlock()
	return nil
}

// failoverConfigApplied puts synced Shield settings to work.
func (s *Server) failoverConfigApplied(changes []filesync.Entry) {
	for _, c := range changes {
		if strings.HasPrefix(c.Path, "dns/") && s.shield != nil && s.shield.dns != nil {
			if err := s.shield.dns.Reload(); err != nil {
				s.Log.Warn("failover: Shield settings", "err", err)
			}
			return
		}
	}
}

// ---------- the watcher ----------

// watchFailoverFolders follows the syncing folders for changes (Linux:
// inotify), marking them for a rescan; nil stops it.
func (s *Server) watchFailoverFolders(ctx context.Context, locs []failoverLoc) {
	f := s.filesInit()
	var paths []string
	for _, l := range locs {
		paths = append(paths, l.ID+"="+l.Path)
	}
	key := strings.Join(paths, "\n")
	f.mu.Lock()
	defer f.mu.Unlock()
	if key == f.watched {
		return
	}
	if f.stopW != nil {
		f.stopW()
		f.stopW = nil
	}
	f.watched = key
	if len(locs) == 0 {
		return
	}
	wctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	f.stopW = cancel
	for _, l := range locs {
		id := l.ID
		if err := filesync.Watch(wctx, l.Path, func(rel string) {
			if l.loc.Skipped(rel) {
				return
			}
			f.mu.Lock()
			f.dirty[id] = true
			f.mu.Unlock()
			s.failoverFilesKick()
		}); err != nil {
			s.Log.Debug("failover: watch", "path", l.Path, "err", err)
		}
	}
}

// ---------- sizes ----------

// failoverSizes: each location's size (cached; -1 while it's added up).
func (s *Server) failoverSizes(locs []failoverLoc) map[string]int64 {
	f := s.filesInit()
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.sizing && time.Since(f.sizesAt) > failoverSizesKeep {
		f.sizing = true
		go func() {
			out := map[string]int64{}
			for _, l := range locs {
				out[l.ID] = folderSize(l.loc)
			}
			f.mu.Lock()
			f.sizes, f.sizesAt, f.sizing = out, time.Now(), false
			f.mu.Unlock()
		}()
	}
	out := map[string]int64{}
	for _, l := range locs {
		if v, ok := f.sizes[l.ID]; ok {
			out[l.ID] = v
		} else {
			out[l.ID] = -1
		}
	}
	return out
}

func folderSize(l filesync.Location) int64 {
	var n int64
	_ = filepath.WalkDir(l.Path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && p != l.Path {
				return fs.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(l.Path, p)
		rel = filepath.ToSlash(rel)
		if rel != "." && l.Skip != nil && l.Skip(rel) && d.IsDir() {
			return fs.SkipDir
		}
		if d.Type().IsRegular() && (l.Skip == nil || !l.Skip(rel)) {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// ---------- administrators ----------

type fileLocJSON struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Kind    string     `json:"kind"`
	On      bool       `json:"on"`
	Always  bool       `json:"always,omitempty"` // config: syncs with the settings
	Size    int64      `json:"size"`             // -1 while it's added up
	Files   int        `json:"files"`
	Queued  int64      `json:"queued"`       // this server's changes the other hasn't read
	QBytes  int64      `json:"queued_bytes"` // and their size
	Scanned *time.Time `json:"scanned,omitempty"`
	Synced  *time.Time `json:"synced,omitempty"`
	Error   string     `json:"error,omitempty"`
	Got     int64      `json:"received"`
}

type failoverFilesJSON struct {
	Locations  []fileLocJSON `json:"locations"`
	LimitKBps  int           `json:"limit_kbps"`
	WindowFrom string        `json:"window_from"`
	WindowTo   string        `json:"window_to"`
	InWindow   bool          `json:"in_window"`
}

func (s *Server) failoverFilesJSON(ctx context.Context, p *store.FailoverPair) (*failoverFilesJSON, error) {
	o := parseFailoverOptions(p.Options)
	locs := s.failoverLocations(ctx)
	sizes := s.failoverSizes(locs)
	queue := map[string][2]int64{}
	if p.State == store.FailoverActive {
		_, acked, err := s.Store.FailoverFileCursors(ctx, p.NocapID)
		if err != nil {
			return nil, err
		}
		if queue, err = s.Store.FileIndex().FileQueue(ctx, acked); err != nil {
			return nil, err
		}
	}
	out := &failoverFilesJSON{Locations: []fileLocJSON{}, LimitKBps: o.LimitKBps, WindowFrom: o.WindowFrom, WindowTo: o.WindowTo, InWindow: o.inWindow(s.failoverInit().now())}
	f := s.filesInit()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range locs {
		j := fileLocJSON{ID: l.ID, Name: l.Name, Kind: l.Kind, On: o.has(l.ID), Always: l.ID == failoverConfigLoc, Size: sizes[l.ID],
			Queued: queue[l.ID][0], QBytes: queue[l.ID][1]}
		if st := f.status[l.ID]; st != nil {
			j.Files, j.Scanned, j.Synced, j.Error, j.Got = st.Files, timePtr(st.Scanned), timePtr(st.Synced), st.Error, st.Received
		}
		out.Locations = append(out.Locations, j)
	}
	return out, nil
}

// failoverSetFiles: an administrator picks the locations that sync, the
// bandwidth limit and the schedule. The other server is told (both keep
// the same choices).
func (s *Server) failoverSetFiles(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var req failoverOptions
	if !decodeJSONLimit(w, r, &req, 8<<10) {
		return
	}
	if req.LimitKBps < 0 || req.LimitKBps > 10<<20 {
		writeError(w, http.StatusBadRequest, "validation", "The bandwidth limit is out of range.")
		return
	}
	if (req.WindowFrom != "" && clockMinutes(req.WindowFrom) < 0) || (req.WindowTo != "" && clockMinutes(req.WindowTo) < 0) {
		writeError(w, http.StatusBadRequest, "validation", "Times are HH:MM.")
		return
	}
	p, err := s.theFailover(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if p == nil || p.State != store.FailoverActive {
		writeError(w, http.StatusNotFound, "not_found", "Failover isn't on.")
		return
	}
	known := map[string]bool{}
	for _, l := range s.failoverLocations(r.Context()) {
		known[l.ID] = true
	}
	files := []string{}
	for _, id := range req.Files {
		if known[id] && id != failoverConfigLoc && !slices.Contains(files, id) {
			files = append(files, id)
		}
	}
	sort.Strings(files)
	o := parseFailoverOptions(p.Options)
	o.Files, o.LimitKBps, o.WindowFrom, o.WindowTo = files, req.LimitKBps, req.WindowFrom, req.WindowTo
	if err := s.saveFailoverOptions(r.Context(), p, o, true); err != nil {
		s.audit(r, u.ID, "failover.files", p.NocapID, false, err.Error())
		writeError(w, http.StatusBadGateway, "unreachable", "Couldn't tell the other server: "+err.Error())
		return
	}
	s.audit(r, u.ID, "failover.files", p.NocapID, true, "locations: "+strings.Join(files, ", ")+"; limit "+strconv.Itoa(o.LimitKBps)+" KB/s")
	s.failoverFilesKick()
	s.failoverGet(w, r)
}

// saveFailoverOptions saves the pair's options here and (tell) on the other server.
func (s *Server) saveFailoverOptions(ctx context.Context, p *store.FailoverPair, o failoverOptions, tell bool) error {
	b, err := json.Marshal(o)
	if err != nil {
		return err
	}
	if tell {
		l, err := s.Store.ServerLink(ctx, p.NocapID)
		if err != nil {
			return err
		}
		if err := s.failoverCall(ctx, l, http.MethodPost, "/api/v1/failover/peer/options", json.RawMessage(b), nil); err != nil {
			return err
		}
	}
	return s.Store.SetFailoverOptions(ctx, p.NocapID, string(b))
}

// ---------- the other server calling ----------

func (s *Server) activePeerPair(w http.ResponseWriter, r *http.Request) (*store.ServerLink, *store.FailoverPair, bool) {
	l := peerFrom(r.Context())
	p, err := s.Store.FailoverPair(r.Context(), l.NocapID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && p.State != store.FailoverActive) {
		writeError(w, http.StatusForbidden, "not_paired", "These servers aren't a failover pair.")
		return nil, nil, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return nil, nil, false
	}
	return l, p, true
}

// failoverPeerOptions: the other server changed the pair's choices.
func (s *Server) failoverPeerOptions(w http.ResponseWriter, r *http.Request) {
	l, p, ok := s.activePeerPair(w, r)
	if !ok {
		return
	}
	var o failoverOptions
	if !decodeJSONLimit(w, r, &o, 8<<10) {
		return
	}
	cur := parseFailoverOptions(p.Options)
	cur.Files, cur.LimitKBps, cur.WindowFrom, cur.WindowTo = o.Files, o.LimitKBps, o.WindowFrom, o.WindowTo
	if err := s.saveFailoverOptions(r.Context(), p, cur, false); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "", "failover.files", l.Name+" ("+l.NocapID+")", true, "changed on the other server: "+strings.Join(o.Files, ", "))
	s.failoverFilesKick()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// failoverPeerFileChanges: the other server reads this one's file index.
func (s *Server) failoverPeerFileChanges(w http.ResponseWriter, r *http.Request) {
	l, p, ok := s.activePeerPair(w, r)
	if !ok {
		return
	}
	since, err := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	if err != nil || since < 0 {
		writeError(w, http.StatusBadRequest, "validation", "since must be a number")
		return
	}
	o := parseFailoverOptions(p.Options)
	var locs []string
	for _, id := range strings.Split(r.URL.Query().Get("locs"), ",") {
		if id != "" && o.has(id) {
			locs = append(locs, id)
		}
	}
	if since > 0 {
		_ = s.Store.SetFailoverFileAcked(r.Context(), l.NocapID, since)
	}
	self, err := s.nocapID()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	changes, next, more, err := s.Store.FileIndex().Changes(r.Context(), since, locs, self, l.NocapID, failoverFilePage)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	cursor, _, _ := s.Store.FailoverFileCursors(r.Context(), l.NocapID)
	writeJSON(w, http.StatusOK, fileFeed{Changes: changes, Next: next, More: more, Seen: cursor})
}

// failoverPeerFile: the other server downloads one file (from offset on).
func (s *Server) failoverPeerFile(w http.ResponseWriter, r *http.Request) {
	_, p, ok := s.activePeerPair(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	id, rel := q.Get("loc"), q.Get("path")
	offset, err := strconv.ParseInt(q.Get("offset"), 10, 64)
	if err != nil || offset < 0 || !filesync.ValidPath(rel) || !parseFailoverOptions(p.Options).has(id) {
		writeError(w, http.StatusBadRequest, "validation", "bad file request")
		return
	}
	var loc *failoverLoc
	for _, l := range s.failoverLocations(r.Context()) {
		if l.ID == id {
			loc = &l
			break
		}
	}
	if loc == nil || (loc.loc.Skip != nil && loc.loc.Skip(rel)) {
		writeError(w, http.StatusNotFound, "not_found", "no such file")
		return
	}
	full, err := filesync.Join(loc.Path, rel)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "no such file")
		return
	}
	fh, err := os.Open(full)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "no such file")
		return
	}
	defer fh.Close()
	fi, err := fh.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, "not_found", "no such file")
		return
	}
	if _, err := fh.Seek(offset, io.SeekStart); err != nil {
		writeError(w, http.StatusBadRequest, "validation", "bad offset")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(max(0, fi.Size()-offset), 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, fh)
}
