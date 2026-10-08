package files

import (
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Index is an in-memory list of file names across the storage locations, for
// universal search. It is rebuilt in the background on a timer and shortly
// after any change made through Files. The whole-disk "system" location is
// skipped (too large to index; browse it instead).

const (
	maxIndexed      = 300_000
	rebuildEvery    = 10 * time.Minute
	rebuildDebounce = 4 * time.Second
)

type indexEntry struct {
	root    string
	rel     string // slash path inside the root, without leading slash
	name    string
	lower   string
	dir     bool
	size    int64
	modTime time.Time
}

type Index struct {
	svc *Service

	mu       sync.RWMutex
	entries  []indexEntry
	built    time.Time
	building bool
	capped   bool

	kick chan struct{}
}

// Hit is one search result.
type Hit struct {
	Root    string    `json:"root"`
	Path    string    `json:"path"` // "/a/b.txt"
	Name    string    `json:"name"`
	Dir     bool      `json:"dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// NewIndex starts background indexing; stop it by closing done.
func NewIndex(svc *Service, done <-chan struct{}) *Index {
	ix := &Index{svc: svc, kick: make(chan struct{}, 1)}
	go ix.loop(done)
	return ix
}

// Touch schedules a rebuild soon (call after files change).
func (ix *Index) Touch() {
	select {
	case ix.kick <- struct{}{}:
	default:
	}
}

func (ix *Index) loop(done <-chan struct{}) {
	ix.rebuild()
	tick := time.NewTicker(rebuildEvery)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-tick.C:
			ix.rebuild()
		case <-ix.kick:
			// Coalesce bursts of changes (a folder upload) into one rebuild.
			select {
			case <-done:
				return
			case <-time.After(rebuildDebounce):
			}
			for len(ix.kick) > 0 {
				<-ix.kick
			}
			ix.rebuild()
		}
	}
}

var skipDirs = map[string]bool{".git": true, "node_modules": true, "__pycache__": true, ".cache": true}

func (ix *Index) rebuild() {
	ix.mu.Lock()
	ix.building = true
	ix.mu.Unlock()

	var out []indexEntry
	capped := false
	for _, r := range ix.svc.Roots() {
		if r.ID == "system" || capped {
			continue
		}
		base := filepath.Clean(r.Path)
		_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if p == base {
				return nil
			}
			name := d.Name()
			rel := filepath.ToSlash(strings.TrimPrefix(p, base+string(filepath.Separator)))
			if d.IsDir() && (skipDirs[name] || rel == RecycleDir) {
				return fs.SkipDir
			}
			if strings.HasPrefix(name, uploadPrefix) && strings.HasSuffix(name, uploadSuffix) {
				return nil
			}
			e := indexEntry{root: r.ID, rel: rel, name: name, lower: strings.ToLower(name), dir: d.IsDir()}
			if info, err := d.Info(); err == nil {
				e.modTime = info.ModTime().UTC()
				if !e.dir {
					e.size = info.Size()
				}
			}
			out = append(out, e)
			if len(out) >= maxIndexed {
				capped = true
				return fs.SkipAll
			}
			return nil
		})
	}

	ix.mu.Lock()
	ix.entries, ix.built, ix.building, ix.capped = out, time.Now(), false, capped
	ix.mu.Unlock()
}

// Stats reports how many entries are indexed and whether a build is running.
func (ix *Index) Stats() (count int, building bool, capped bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.entries), ix.building, ix.capped
}

// Search returns up to limit entries whose name contains every word of q,
// best matches first.
func (ix *Index) Search(q string, limit int) []Hit {
	words := strings.Fields(strings.ToLower(q))
	if len(words) == 0 || limit <= 0 {
		return []Hit{}
	}
	type scored struct {
		e     *indexEntry
		score float64
	}
	var found []scored
	ix.mu.RLock()
	for i := range ix.entries {
		e := &ix.entries[i]
		ok := true
		for _, w := range words {
			if !strings.Contains(e.lower, w) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		found = append(found, scored{e, score(e, words, q)})
	}
	ix.mu.RUnlock()

	sort.Slice(found, func(i, j int) bool {
		if found[i].score != found[j].score {
			return found[i].score > found[j].score
		}
		return found[i].e.rel < found[j].e.rel
	})
	if len(found) > limit {
		found = found[:limit]
	}
	hits := make([]Hit, len(found))
	for i, f := range found {
		hits[i] = Hit{Root: f.e.root, Path: "/" + f.e.rel, Name: f.e.name, Dir: f.e.dir, Size: f.e.size, ModTime: f.e.modTime}
	}
	return hits
}

func score(e *indexEntry, words []string, q string) float64 {
	full := strings.ToLower(strings.TrimSpace(q))
	stem := strings.TrimSuffix(e.lower, path.Ext(e.lower))
	s := 0.0
	switch {
	case e.lower == full || stem == full:
		s += 100
	case strings.HasPrefix(e.lower, full):
		s += 60
	}
	for _, w := range words {
		// Matches at the start of a word ("report" in "Q3 report.pdf") rank higher.
		if i := strings.Index(e.lower, w); i == 0 || (i > 0 && strings.ContainsRune(" -_.(", rune(e.lower[i-1]))) {
			s += 15
		}
	}
	if e.dir {
		s += 5
	}
	s -= float64(strings.Count(e.rel, "/")) * 2 // shallower first
	s -= float64(len(e.name)) * 0.1             // shorter names first
	if age := time.Since(e.modTime); age < 30*24*time.Hour {
		s += 8 * (1 - age.Hours()/(30*24)) // recently changed files a little higher
	}
	return s
}
