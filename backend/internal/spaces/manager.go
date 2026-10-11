package spaces

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/store"
)

const (
	// HomesDir is the folder in the main storage location that holds one
	// private home per person.
	HomesDir = "homes"
	// HomesRootID is the administrators' view of every home.
	HomesRootID = "all-homes"

	homePrefix    = "homedir-"
	sharePrefix   = "share-"
	keyAdminHomes = "spaces.admin_homes" // "off" hides people's homes from admins
)

// HomeRootID is the storage location id of a person's home.
func HomeRootID(name string) string { return homePrefix + name }

// ShareRootID is the storage location id of a shared folder.
func ShareRootID(id int64) string { return sharePrefix + strconv.FormatInt(id, 10) }

// Person is someone with an account, as sharing sees them.
type Person struct {
	Name     string `json:"username"`
	Admin    bool   `json:"admin"`
	Disabled bool   `json:"disabled"`
	Access   string `json:"access"` // desktop | files
}

// Location is a place a person can open in Files beyond the fixed storage
// locations: their home, a shared folder, or (admins) everyone's homes.
type Location struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"` // home | share | homes
	ReadOnly bool   `json:"read_only,omitempty"`
	Path     string `json:"-"`
}

// Folder is a shared folder with its permissions, for administrators.
type Folder struct {
	store.SharedFolder
	Missing     bool              `json:"missing,omitempty"`     // its folder or location is gone
	HoldsHomes  bool              `json:"holds_homes,omitempty"` // contains people's homes: not shared over the network
	Permissions map[string]string `json:"permissions"`           // user name → rw | ro
	RootName    string            `json:"root_name,omitempty"`   // the storage location's name
	FullPath    string            `json:"full_path,omitempty"`   // on disk, for the network address hints
}

type Manager struct {
	st    *store.Store
	files *files.Service
	log   *slog.Logger

	people   func(ctx context.Context) ([]Person, error)
	onChange []func(ctx context.Context)
	// homes says where people's homes are (see homes.go).
	homes Homes
	// owner finds a person's Linux uid/gid, so files made for them in their
	// home belong to them (native installs as root).
	owner func(username string) (uid, gid int, ok bool)

	mu         sync.RWMutex
	folders    []*store.SharedFolder
	perms      map[int64]map[string]string
	adminHomes bool
}

// New loads shared folders and permissions and registers the per-person
// locations with the file service.
func New(ctx context.Context, st *store.Store, fsvc *files.Service, log *slog.Logger) (*Manager, error) {
	m := &Manager{st: st, files: fsvc, log: log, owner: linuxOwner}
	m.homes = storageHomes{m}
	if err := m.reload(ctx); err != nil {
		return nil, err
	}
	v, err := st.Setting(ctx, keyAdminHomes)
	if err != nil {
		return nil, err
	}
	m.adminHomes = v != "off"
	fsvc.SetResolver(m.resolve)
	m.protectHomes()
	return m, nil
}

func (m *Manager) protectHomes() {
	if dir := m.HomesPath(); dir != "" {
		m.files.ProtectPoints(dir) // the homes folder itself stays put
	}
}

// SetPeople provides the list of accounts (Linux users or NoCapOS's own).
func (m *Manager) SetPeople(fn func(ctx context.Context) ([]Person, error)) { m.people = fn }

// OnChange registers a function to run after folders, permissions or people
// change (it regenerates the Samba configuration).
func (m *Manager) OnChange(fn func(ctx context.Context)) { m.onChange = append(m.onChange, fn) }

// Changed runs the change hooks (call after account changes too).
func (m *Manager) Changed(ctx context.Context) {
	for _, fn := range m.onChange {
		fn(ctx)
	}
}

// Reload re-reads shared folders and permissions (after people are deleted).
func (m *Manager) Reload(ctx context.Context) error { return m.reload(ctx) }

func (m *Manager) reload(ctx context.Context) error {
	folders, err := m.st.SharedFolders(ctx)
	if err != nil {
		return err
	}
	perms, err := m.st.SharePermissions(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.folders, m.perms = folders, perms
	m.mu.Unlock()
	return nil
}

// People lists every account with its access type.
func (m *Manager) People(ctx context.Context) ([]Person, error) {
	if m.people == nil {
		return nil, nil
	}
	list, err := m.people(ctx)
	if err != nil {
		return nil, err
	}
	access, err := m.st.UserAccessAll(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i].Access = store.AccessDesktop
		if !list[i].Admin && access[strings.ToLower(list[i].Name)] == store.AccessFiles {
			list[i].Access = store.AccessFiles
		}
	}
	return list, nil
}

// ---------- storage, homes ----------

// Storage is the main storage location: where homes and new shared folders
// go. It's the first local location (NoCap Drive unless configured).
func (m *Manager) Storage() (*files.Root, bool) {
	for _, r := range m.files.Roots() {
		if r.ID != "system" && !strings.HasPrefix(r.ID, "net:") {
			return &r, true
		}
	}
	return nil, false
}

// AdminsSeeHomes reports whether administrators can open people's homes.
func (m *Manager) AdminsSeeHomes() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.adminHomes
}

func (m *Manager) SetAdminsSeeHomes(ctx context.Context, on bool) error {
	v := "on"
	if !on {
		v = "off"
	}
	if err := m.st.SetSetting(ctx, keyAdminHomes, v); err != nil {
		return err
	}
	m.mu.Lock()
	m.adminHomes = on
	m.mu.Unlock()
	return nil
}

// ---------- locations ----------

func (m *Manager) folder(id int64) (*store.SharedFolder, map[string]string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, f := range m.folders {
		if f.ID == id {
			c := *f
			return &c, m.perms[id]
		}
	}
	return nil, nil
}

// folderPath is a shared folder's real path on disk, which must still be
// inside its storage location ("" if the location or folder is gone, or a
// link now leads elsewhere).
func (m *Manager) folderPath(f *store.SharedFolder) string {
	var base string
	for _, r := range m.files.Roots() {
		if r.ID == f.Root {
			base = r.Path
			break
		}
	}
	if base == "" {
		return ""
	}
	p, err := Inside(base, f.Path)
	if err != nil {
		return ""
	}
	if st, err := os.Stat(p); err != nil || !st.IsDir() {
		return ""
	}
	return p
}

// holdsHomes reports whether a folder is, contains or sits inside the homes.
func (m *Manager) holdsHomes(p string) bool {
	homes := m.HomesPath()
	return p != "" && homes != "" && (IsInside(homes, p) || IsInside(p, homes))
}

// resolve maps per-person location ids to folders for the file service.
func (m *Manager) resolve(id string) (*files.Root, bool) {
	switch {
	case id == HomesRootID:
		p := m.HomesPath()
		if p == "" {
			return nil, false
		}
		_ = os.MkdirAll(p, 0o755)
		return &files.Root{ID: id, Name: "Homes", Path: p}, true
	case strings.HasPrefix(id, homePrefix):
		p := m.HomePath(strings.TrimPrefix(id, homePrefix))
		if p == "" || (!m.LinuxHomes() && !IsInside(p, m.HomesPath())) {
			return nil, false
		}
		if real, err := realPath(p); err == nil {
			p = real // pin the folder the home's path leads to now
		}
		return &files.Root{ID: id, Name: "Home", Path: p}, true
	case strings.HasPrefix(id, sharePrefix):
		n, err := strconv.ParseInt(strings.TrimPrefix(id, sharePrefix), 10, 64)
		if err != nil {
			return nil, false
		}
		f, _ := m.folder(n)
		if f == nil {
			return nil, false
		}
		p := m.folderPath(f)
		if p == "" {
			return nil, false
		}
		return &files.Root{ID: id, Name: f.Name, Path: p}, true
	}
	return nil, false
}

// Locations lists what a person can open in Files besides the fixed
// storage locations (which only administrators see): their home and the
// shared folders they may use; administrators get everyone's homes instead.
func (m *Manager) Locations(v Viewer) []Location {
	var out []Location
	if v.Admin {
		if m.AdminsSeeHomes() && m.HomesPath() != "" {
			out = append(out, Location{ID: HomesRootID, Name: "Homes", Kind: "homes", Path: m.HomesPath()})
		}
		return out
	}
	if p, err := m.EnsureHome(v.Name); err == nil {
		out = append(out, Location{ID: HomeRootID(v.Name), Name: "Home", Kind: "home", Path: p})
	} else {
		m.log.Warn("spaces: home folder", "user", v.Name, "err", err)
	}
	m.mu.RLock()
	folders := append([]*store.SharedFolder(nil), m.folders...)
	perms := m.perms
	m.mu.RUnlock()
	for _, f := range folders {
		p := FolderPerm(v, f, perms[f.ID])
		if p == None {
			continue
		}
		fp := m.folderPath(f)
		if fp == "" || m.holdsHomes(fp) {
			continue
		}
		out = append(out, Location{ID: ShareRootID(f.ID), Name: f.Name, Kind: "share", ReadOnly: p != ReadWrite, Path: fp})
	}
	return out
}

// Access is what a person may do at rel inside a storage location. It is
// the one check every Files request goes through.
func (m *Manager) Access(v Viewer, rootID, rel string) Perm {
	if v.Name == "" {
		return None
	}
	switch {
	case rootID == HomesRootID:
		if v.Admin && m.AdminsSeeHomes() {
			return ReadWrite
		}
		return None
	case strings.HasPrefix(rootID, homePrefix):
		owner := strings.TrimPrefix(rootID, homePrefix)
		if strings.EqualFold(owner, v.Name) || (v.Admin && m.AdminsSeeHomes()) {
			return ReadWrite
		}
		return None
	case strings.HasPrefix(rootID, sharePrefix):
		n, err := strconv.ParseInt(strings.TrimPrefix(rootID, sharePrefix), 10, 64)
		if err != nil {
			return None
		}
		f, perms := m.folder(n)
		p := FolderPerm(v, f, perms)
		if p == None {
			return None
		}
		if !v.Admin || !m.AdminsSeeHomes() {
			// A folder that holds people's homes never leads into them.
			if fp := m.folderPath(f); fp == "" || (m.holdsHomes(fp) && m.underHomes(fp, rel, v)) {
				return None
			}
		}
		return p
	}
	if !v.Admin {
		return None
	}
	if !m.AdminsSeeHomes() {
		if st, ok := m.Storage(); ok && st.ID == rootID && m.underHomes(st.Path, rel, v) {
			return None
		}
	}
	return ReadWrite
}

// underHomes reports whether base/rel (symlinks resolved) is inside someone
// else's home. The homes folder itself can be listed.
func (m *Manager) underHomes(base, rel string, v Viewer) bool {
	homes := m.HomesPath()
	if homes == "" {
		return false
	}
	full, err := Inside(base, rel)
	if err != nil {
		return true // leads outside the location: never allowed
	}
	rh, err := realPath(homes)
	if err != nil || !within(full, rh) || full == rh {
		return false
	}
	own := m.HomePath(v.Name)
	return own == "" || !IsInside(full, own)
}

// Recycles reports whether deletes in a location go to its recycle bin
// (false for shared folders with the recycle bin turned off).
func (m *Manager) Recycles(rootID string) bool {
	if !strings.HasPrefix(rootID, sharePrefix) {
		return true
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(rootID, sharePrefix), 10, 64)
	if err != nil {
		return true
	}
	if f, _ := m.folder(n); f != nil {
		return f.Recycle
	}
	return true
}

// HideHomes reports whether a listing of base/rel should leave out the
// homes folder for this person (administrators who don't see homes).
func (m *Manager) HideHomes(v Viewer, rootID, rel string) bool {
	if !v.Admin || m.AdminsSeeHomes() || rel != "." {
		return false
	}
	st, ok := m.Storage()
	return ok && st.ID == rootID
}

// MapHits turns search hits (made against the fixed locations) into ones a
// person can open: hits inside their home or a shared folder they may use
// are re-rooted there; everything else is dropped. Administrators keep all
// hits, minus people's homes when those are hidden from them.
func (m *Manager) MapHits(v Viewer, hits []files.Hit) []files.Hit {
	out := make([]files.Hit, 0, len(hits))
	if v.Admin {
		if m.AdminsSeeHomes() {
			return hits
		}
		homesRoot, homesRel, hasHomes := m.IndexPlace(m.HomesPath())
		for _, h := range hits {
			if hasHomes && h.Root == homesRoot && (homesRel == "" || h.Path == homesRel || strings.HasPrefix(h.Path, homesRel+"/")) {
				continue
			}
			out = append(out, h)
		}
		return out
	}
	type place struct{ root, prefix, id string }
	var places []place
	for _, l := range m.Locations(v) {
		if root, rel, ok := m.IndexPlace(l.Path); ok {
			places = append(places, place{root, rel, l.ID})
		}
	}
	for _, h := range hits {
		for _, p := range places {
			if h.Root != p.root {
				continue
			}
			if p.prefix == "" || h.Path == p.prefix || strings.HasPrefix(h.Path, p.prefix+"/") {
				h.Root = p.id
				h.Path = "/" + strings.TrimPrefix(strings.TrimPrefix(h.Path, p.prefix), "/")
				if h.Path == "/" {
					break // the location itself isn't a result
				}
				out = append(out, h)
				break
			}
		}
	}
	return out
}

// ---------- shared folders ----------

var folderNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.-]{0,39}$`)

// reservedNames are share names Samba (or common setups) use for themselves.
var reservedNames = map[string]bool{"global": true, "homes": true, "printers": true, "print$": true, "ipc$": true}

// ValidFolderName checks a shared folder's name (it's also its network name).
func ValidFolderName(name string) error {
	if !folderNameRe.MatchString(name) || reservedNames[strings.ToLower(name)] {
		return errors.New("name the shared folder with letters, numbers, spaces, - _ or . (up to 40)")
	}
	return nil
}

// Folders lists shared folders with their permissions.
func (m *Manager) Folders() []Folder {
	m.mu.RLock()
	folders := append([]*store.SharedFolder(nil), m.folders...)
	perms := m.perms
	m.mu.RUnlock()
	names := map[string]string{}
	for _, r := range m.files.Roots() {
		names[r.ID] = r.Name
	}
	out := make([]Folder, 0, len(folders))
	for _, f := range folders {
		p := m.folderPath(f)
		fv := Folder{SharedFolder: *f, Missing: p == "", HoldsHomes: m.holdsHomes(p), Permissions: map[string]string{}, RootName: names[f.Root], FullPath: p}
		for u, perm := range perms[f.ID] {
			fv.Permissions[u] = perm
		}
		out = append(out, fv)
	}
	return out
}

// Folder returns one shared folder with its permissions.
func (m *Manager) Folder(id int64) (Folder, error) {
	for _, f := range m.Folders() {
		if f.ID == id {
			return f, nil
		}
	}
	return Folder{}, store.ErrNotFound
}

// FolderInput creates or edits a shared folder. Nil fields keep their value.
type FolderInput struct {
	Name        *string `json:"name"`
	Root        string  `json:"root"` // create only: storage location (default: the main one)
	Path        string  `json:"path"` // create only: existing folder (default: a new one named after the share)
	Description *string `json:"description"`
	Recycle     *bool   `json:"recycle"`
	Network     *bool   `json:"network"`
	ReadOnly    *bool   `json:"read_only"`
}

func cleanDescription(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len([]rune(s)) > 200 {
		s = string([]rune(s)[:200])
	}
	return s
}

// CreateFolder shares a folder. Without a path, a new folder named after
// the share is made at the top of the location (an existing one is reused).
func (m *Manager) CreateFolder(ctx context.Context, in FolderInput) (Folder, error) {
	if in.Name == nil {
		return Folder{}, errors.New("give the shared folder a name")
	}
	name := strings.TrimSpace(*in.Name)
	if err := ValidFolderName(name); err != nil {
		return Folder{}, err
	}
	rootID := in.Root
	if rootID == "" {
		st, ok := m.Storage()
		if !ok {
			return Folder{}, errors.New("there's no storage location to put it in")
		}
		rootID = st.ID
	}
	if rootID == "system" {
		return Folder{}, errors.New("share folders from your drives; the whole-server System view can't be shared")
	}
	var base string
	for _, r := range m.files.Roots() {
		if r.ID == rootID {
			base = r.Path
		}
	}
	if base == "" {
		return Folder{}, errors.New("choose a storage location")
	}
	rel := in.Path
	if strings.TrimSpace(rel) == "" {
		rel = "/" + name
		full, err := Inside(base, rel)
		if err != nil {
			return Folder{}, err
		}
		if err := os.MkdirAll(full, 0o755); err != nil {
			return Folder{}, fmt.Errorf("create the folder: %w", err)
		}
	}
	clean, err := files.Clean(rel)
	if err != nil {
		return Folder{}, err
	}
	full, err := Inside(base, clean)
	if err != nil {
		return Folder{}, err
	}
	if st, err := os.Stat(full); err != nil || !st.IsDir() {
		return Folder{}, errors.New("that folder doesn't exist")
	}
	if m.holdsHomes(full) {
		return Folder{}, errors.New("people's homes are private, so a folder that holds them (or sits inside them) can't be shared")
	}
	if strings.ContainsAny(full, "%\n") {
		return Folder{}, errors.New("folders with % in their path can't be shared")
	}
	f := &store.SharedFolder{Name: name, Root: rootID, Path: "/" + strings.TrimPrefix(clean, "."), Recycle: true, Network: true}
	if f.Path == "/." || f.Path == "" {
		f.Path = "/"
	}
	if in.Description != nil {
		f.Description = cleanDescription(*in.Description)
	}
	if in.Recycle != nil {
		f.Recycle = *in.Recycle
	}
	if in.Network != nil {
		f.Network = *in.Network
	}
	if in.ReadOnly != nil {
		f.ReadOnly = *in.ReadOnly
	}
	err = m.st.CreateSharedFolder(ctx, f)
	if errors.Is(err, store.ErrExists) {
		return Folder{}, fmt.Errorf("there's already a shared folder called %q", name)
	}
	if err != nil {
		return Folder{}, err
	}
	if err := m.reload(ctx); err != nil {
		return Folder{}, err
	}
	m.Changed(ctx)
	return m.Folder(f.ID)
}

// UpdateFolder changes a folder's name, description or switches.
func (m *Manager) UpdateFolder(ctx context.Context, id int64, in FolderInput) (Folder, error) {
	f, _ := m.folder(id)
	if f == nil {
		return Folder{}, store.ErrNotFound
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if err := ValidFolderName(name); err != nil {
			return Folder{}, err
		}
		f.Name = name
	}
	if in.Description != nil {
		f.Description = cleanDescription(*in.Description)
	}
	if in.Recycle != nil {
		f.Recycle = *in.Recycle
	}
	if in.Network != nil {
		f.Network = *in.Network
	}
	if in.ReadOnly != nil {
		f.ReadOnly = *in.ReadOnly
	}
	err := m.st.UpdateSharedFolder(ctx, f)
	if errors.Is(err, store.ErrExists) {
		return Folder{}, fmt.Errorf("there's already a shared folder called %q", f.Name)
	}
	if err != nil {
		return Folder{}, err
	}
	if err := m.reload(ctx); err != nil {
		return Folder{}, err
	}
	m.Changed(ctx)
	return m.Folder(id)
}

// DeleteFolder stops sharing a folder. Its files stay unless deleteData is
// set, in which case the folder and everything in it is erased.
func (m *Manager) DeleteFolder(ctx context.Context, id int64, deleteData bool) error {
	f, _ := m.folder(id)
	if f == nil {
		return store.ErrNotFound
	}
	if deleteData {
		rel, err := files.Clean(f.Path)
		if err != nil {
			return err
		}
		p := m.folderPath(f)
		switch {
		case rel == "." || p == "":
			return errors.New("this shared folder is a whole storage location (or is missing), so its files can't be deleted from here")
		case m.holdsHomes(p):
			return errors.New("this folder holds people's homes, so its files can't be deleted from here")
		}
		if err := m.files.CheckChange(f.Root, rel); err != nil {
			return err
		}
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	if err := m.st.DeleteSharedFolder(ctx, id); err != nil {
		return err
	}
	if err := m.reload(ctx); err != nil {
		return err
	}
	m.Changed(ctx)
	return nil
}

// SetPermission sets what one person may do in a folder.
func (m *Manager) SetPermission(ctx context.Context, folderID int64, username string, p Perm) error {
	if f, _ := m.folder(folderID); f == nil {
		return store.ErrNotFound
	}
	if err := m.st.SetSharePermission(ctx, folderID, username, string(p)); err != nil {
		return err
	}
	if err := m.reload(ctx); err != nil {
		return err
	}
	m.Changed(ctx)
	return nil
}

// PermissionsOf maps folder id → permission for one person (rows only).
func (m *Manager) PermissionsOf(username string) map[int64]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := map[int64]string{}
	for id, perms := range m.perms {
		if p, ok := perms[strings.ToLower(username)]; ok {
			out[id] = p
		}
	}
	return out
}

// PerUserSharing reports whether anyone has been given access to a shared
// folder. Until then, the single shared network account keeps working.
func (m *Manager) PerUserSharing() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.perms {
		if len(p) > 0 {
			return true
		}
	}
	return false
}

// NetFolder is a shared folder as the network services see it.
type NetFolder struct {
	store.SharedFolder
	FullPath string
	// What each person may do (administrators included, disabled people left out).
	Perms map[string]Perm
}

// NetFolders lists the shared folders that exist on disk, can go on the
// network (not holding homes), with each person's resolved permission.
func (m *Manager) NetFolders(people []Person) []NetFolder {
	m.mu.RLock()
	folders := append([]*store.SharedFolder(nil), m.folders...)
	perms := m.perms
	m.mu.RUnlock()
	var out []NetFolder
	for _, f := range folders {
		p := m.folderPath(f)
		if p == "" || m.holdsHomes(p) {
			continue
		}
		nf := NetFolder{SharedFolder: *f, FullPath: p, Perms: map[string]Perm{}}
		for _, who := range people {
			if who.Disabled {
				continue
			}
			if got := FolderPerm(Viewer{Name: who.Name, Admin: who.Admin}, f, perms[f.ID]); got != None {
				nf.Perms[who.Name] = got
			}
		}
		out = append(out, nf)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// ---------- apps ----------

// AppRules returns every app's access rules (apps without any: everyone).
func (m *Manager) AppRules(ctx context.Context) (map[string]*store.AppAccess, error) {
	return m.st.AppAccessAll(ctx)
}

// SetAppAccess replaces who may use an app.
func (m *Manager) SetAppAccess(ctx context.Context, appID string, a store.AppAccess) error {
	return m.st.SetAppAccess(ctx, appID, a)
}

// SetAppAllowed lets one person use an app, or stops them. Stopping someone
// on an app that's open to everyone limits it to everyone else.
func (m *Manager) SetAppAllowed(ctx context.Context, appID, username string, allowed bool) error {
	rules, err := m.st.AppAccessAll(ctx)
	if err != nil {
		return err
	}
	rule := rules[appID]
	if rule == nil || rule.Everyone {
		if allowed {
			return nil
		}
		people, err := m.People(ctx)
		if err != nil {
			return err
		}
		next := store.AppAccess{}
		for _, p := range people {
			if !p.Admin && !strings.EqualFold(p.Name, username) {
				next.Users = append(next.Users, p.Name)
			}
		}
		return m.st.SetAppAccess(ctx, appID, next)
	}
	return m.st.SetAppAllowed(ctx, appID, username, allowed)
}
