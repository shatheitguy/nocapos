package api

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/store"
)

// Received files: whatever a linked device sends this server lands in the
// person's own Received folder ("Drops" in the Gen Z voice) at the top of
// their home, in a subfolder per device, keeping the original file name
// ("name (1).ext" on a clash). Files sent from here to a device are kept in
// that device's "Sent" subfolder. The folder can be renamed in Settings; it
// is created (and anything in the old "NoCap Link" folder moved into it)
// the first time it's needed.

const (
	// receivedLegacy is where sent files landed before the Received folder.
	receivedLegacy = "NoCap Link"
	// receivedSent is the per-device subfolder for files sent to that device.
	receivedSent = "Sent"
	// receivedScan caps how many entries the unread count looks at.
	receivedScan = 5000

	receivedKeyFolder = "received.folder."
	receivedKeySeen   = "received.seen."
	receivedKeyMoved  = "received.moved."
)

var errNoHome = errors.New("no home folder")

// receivedDefault is the folder's name before the person picks one: in
// the voice they use.
func receivedDefault(lingo string) string {
	if lingo == "standard" {
		return "Received"
	}
	return "Drops"
}

// cleanFileName makes a name safe for one path element: no separators,
// reserved or control characters, no "." or "..", at most 120 characters
// (keeping the extension). Empty when nothing usable is left.
func cleanFileName(in string) string {
	in = strings.ReplaceAll(in, `\`, "/")
	in = path.Base("/" + in) // last element only: no traversal
	var b strings.Builder
	for _, c := range in {
		if unicode.IsControl(c) || strings.ContainsRune(`/\:*?"<>|`, c) || c == utf8.RuneError {
			b.WriteRune('_')
			continue
		}
		b.WriteRune(c)
	}
	out := strings.Trim(strings.TrimSpace(b.String()), ".")
	out = strings.TrimSpace(out)
	if r := []rune(out); len(r) > 120 {
		ext := path.Ext(out)
		if utf8.RuneCountInString(ext) > 16 {
			ext = ""
		}
		keep := 120 - utf8.RuneCountInString(ext)
		out = strings.TrimSpace(string(r[:keep])) + ext
	}
	if out == "" || out == "/" || files.ValidName(out) != nil {
		return ""
	}
	return out
}

// receivedDeviceDir is the subfolder for a device's files.
func receivedDeviceDir(deviceName string) string {
	if n := cleanFileName(deviceName); n != "" {
		return n
	}
	return "My device"
}

// receivedFolderName checks a folder name someone picked.
func receivedFolderName(in string) (string, bool) {
	n := strings.TrimSpace(in)
	if n == "" || utf8.RuneCountInString(n) > 64 || cleanFileName(n) != n || strings.HasPrefix(n, ".") {
		return "", false
	}
	return n, true
}

// receivedSetting reads a per-person setting.
func (s *Server) receivedSetting(ctx context.Context, prefix, userID string) string {
	v, _ := s.Store.Setting(ctx, prefix+userID)
	return v
}

// receivedName is the person's Received folder name (picked now, in their
// voice, if they have none yet).
func (s *Server) receivedName(ctx context.Context, u *store.User) string {
	if n, ok := receivedFolderName(s.receivedSetting(ctx, receivedKeyFolder, u.ID)); ok {
		return n
	}
	data, _, _ := s.Store.UserPrefs(ctx, u.ID)
	n := receivedDefault(devicePrefsFrom(data)["lingo"])
	_ = s.Store.SetSetting(ctx, receivedKeyFolder+u.ID, n)
	return n
}

// receivedRoot is the person's home and their Received folder in it,
// made (and the old "NoCap Link" folder moved in) when missing.
func (s *Server) receivedRoot(r *http.Request) (root, folder string, err error) {
	u := userFrom(r.Context())
	folder = s.receivedName(r.Context(), u)
	root = s.sendHome(r, folder)
	if root == "" {
		return "", "", errNoHome
	}
	moved := s.receivedSetting(r.Context(), receivedKeyMoved, u.ID) != ""
	if !moved {
		// First time: what's there already isn't new.
		if s.receivedSetting(r.Context(), receivedKeySeen, u.ID) == "" {
			_ = s.Store.SetSetting(r.Context(), receivedKeySeen+u.ID, strconv.FormatInt(time.Now().UnixMilli(), 10))
		}
		s.receivedMoveLegacy(r.Context(), u, root, folder)
	}
	if _, err := s.Files.Mkdir(root, ".", folder); err == nil {
		s.ownFiles(root, folder)
	} else if !errors.Is(err, fs.ErrExist) {
		return "", "", err
	}
	return root, folder, nil
}

// receivedMoveLegacy brings the old "NoCap Link" folder into Received,
// once: renamed when Received doesn't exist yet, else moved inside it.
func (s *Server) receivedMoveLegacy(ctx context.Context, u *store.User, root, folder string) {
	defer func() { _ = s.Store.SetSetting(ctx, receivedKeyMoved+u.ID, "1") }()
	if folder == receivedLegacy {
		return
	}
	if _, err := s.Files.List(root, receivedLegacy); err != nil {
		return // nothing to move
	}
	if _, err := s.Files.List(root, folder); err != nil {
		if _, err := s.Files.Rename(root, receivedLegacy, folder); err == nil {
			return
		}
		if _, err := s.Files.Mkdir(root, ".", folder); err != nil && !errors.Is(err, fs.ErrExist) {
			return
		}
		s.ownFiles(root, folder)
	}
	if res, err := s.Files.TransferWith(ctx, root, []string{receivedLegacy}, folder, files.TransferOptions{Move: true, Conflict: files.ConflictRename}); err == nil {
		s.ownFiles(root, res.Paths...)
	}
}

// receivedDeviceFolder makes (if needed) the device's subfolder, or its
// "Sent" subfolder, and returns its path in root.
func (s *Server) receivedDeviceFolder(root, folder, deviceName string, sent bool) (string, error) {
	rel := folder
	parts := []string{receivedDeviceDir(deviceName)}
	if sent {
		parts = append(parts, receivedSent)
	}
	for _, p := range parts {
		next, err := s.Files.Mkdir(root, rel, p)
		if err == nil {
			s.ownFiles(root, next)
		} else if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
		rel = path.Join(rel, p)
	}
	return rel, nil
}

// receivedUnread counts files that arrived after seen (the files sent from
// here, in each device's "Sent" folder, don't count).
func (s *Server) receivedUnread(root, folder string, seen time.Time) int {
	n, looked := 0, 0
	var walk func(rel string, depth int)
	walk = func(rel string, depth int) {
		list, err := s.Files.List(root, rel)
		if err != nil {
			return
		}
		for _, e := range list {
			if looked++; looked > receivedScan {
				return
			}
			if e.Dir {
				if depth < 2 && !(depth == 1 && e.Name == receivedSent) {
					walk(path.Join(rel, e.Name), depth+1)
				}
				continue
			}
			// "seen" is stored to the millisecond, so compare at that precision:
			// a file written in the same millisecond was already seen.
			if e.ModTime.UnixMilli() > seen.UnixMilli() {
				n++
			}
		}
	}
	walk(folder, 0)
	return n
}

type receivedJSON struct {
	Available bool   `json:"available"`
	Root      string `json:"root,omitempty"`
	Folder    string `json:"folder"`
	Path      string `json:"path,omitempty"`
	Default   string `json:"default"`
	Unread    int    `json:"unread"`
	// Devices: each of the person's devices' subfolder (it may not exist yet).
	Devices map[string]string `json:"devices"`
}

func (s *Server) receivedInfo(r *http.Request) receivedJSON {
	u := userFrom(r.Context())
	data, _, _ := s.Store.UserPrefs(r.Context(), u.ID)
	out := receivedJSON{Default: receivedDefault(devicePrefsFrom(data)["lingo"]), Devices: map[string]string{}}
	root, folder, err := s.receivedRoot(r)
	out.Folder = folder
	if err != nil {
		out.Folder = s.receivedName(r.Context(), u)
		return out
	}
	out.Available, out.Root, out.Path = true, root, display(folder)
	seen := time.Time{}
	if v, err := strconv.ParseInt(s.receivedSetting(r.Context(), receivedKeySeen, u.ID), 10, 64); err == nil {
		seen = time.UnixMilli(v)
	}
	out.Unread = s.receivedUnread(root, folder, seen)
	if list, err := s.Store.ListDevices(r.Context(), u.ID); err == nil {
		for _, d := range list {
			if d.UserID == u.ID && d.RevokedAt.IsZero() {
				out.Devices[d.ID] = display(path.Join(folder, receivedDeviceDir(d.Name)))
			}
		}
	}
	return out
}

func (s *Server) receivedRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/received", s.authed(s.receivedGet))
	mux.Handle("PUT /api/v1/received", s.authed(s.receivedPut))
	mux.Handle("POST /api/v1/received/seen", s.authed(s.receivedSeen))
	mux.Handle("POST /api/v1/devices/{id}/outbox", s.authed(s.receivedOutbox))
}

func (s *Server) receivedGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.receivedInfo(r))
}

func (s *Server) receivedSeen(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if err := s.Store.SetSetting(r.Context(), receivedKeySeen+u.ID, strconv.FormatInt(time.Now().UnixMilli(), 10)); err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// receivedPut renames the person's Received folder (the folder itself is
// renamed too when it exists).
func (s *Server) receivedPut(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var req struct {
		Folder string `json:"folder"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name, ok := receivedFolderName(req.Folder)
	if !ok {
		writeError(w, http.StatusBadRequest, "validation", "Use a folder name of up to 64 characters without / \\ : * ? \" < > |.")
		return
	}
	old := s.receivedName(r.Context(), u)
	if name != old {
		if root := s.sendHome(r, name); root != "" {
			if _, err := s.Files.List(root, old); err == nil {
				if _, err := s.Files.Rename(root, old, name); errors.Is(err, fs.ErrExist) {
					writeError(w, http.StatusConflict, "exists", "There's already a folder with that name in your home.")
					return
				} else if err != nil {
					s.fileError(w, r, err)
					return
				}
			}
		}
		if err := s.Store.SetSetting(r.Context(), receivedKeyFolder+u.ID, name); err != nil {
			s.internalError(w, r, err)
			return
		}
		s.audit(r, u.ID, "received.folder", name, true, "")
	}
	writeJSON(w, http.StatusOK, s.receivedInfo(r))
}

// receivedOutbox is where a file sent from here to one of the person's own
// devices is uploaded first: <Received>/<device>/Sent.
func (s *Server) receivedOutbox(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	d, err := s.Store.DeviceByID(r.Context(), r.PathValue("id"))
	if err != nil || !mirrorOwns(u, d) {
		writeError(w, http.StatusNotFound, "not_found", "That device isn't linked to your account.")
		return
	}
	root, folder, err := s.receivedRoot(r)
	if errors.Is(err, errNoHome) {
		writeError(w, http.StatusServiceUnavailable, "no_home", "Your account doesn't have a home folder on this server.")
		return
	}
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	rel, err := s.receivedDeviceFolder(root, folder, d.Name, true)
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"root": root, "path": display(rel)})
}

// linkReceived tells the person's desktops that files arrived from a device.
type linkReceived struct {
	DeviceID   string   `json:"device_id"`
	DeviceName string   `json:"device_name"`
	Root       string   `json:"root"`
	Folder     string   `json:"folder"`
	Paths      []string `json:"paths"`
	At         string   `json:"at"`
}
