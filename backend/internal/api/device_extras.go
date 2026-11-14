package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/hardware"
	"alfaos/alfad/internal/notify"
	"alfaos/alfad/internal/spaces"
	"alfaos/alfad/internal/store"
)

// More for the NoCap OS app (all behind a device token): a server summary
// for home-screen widgets, the owner's look, Files, "Send to NoCap" from
// the share sheet, the home-screen backup, admin notifications, unlinking,
// and NoCap AI chats.

func (s *Server) deviceRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/device/summary", s.device(s.deviceSummary))
	mux.Handle("GET /api/v1/device/prefs", s.device(s.devicePrefs))
	mux.Handle("GET /api/v1/device/files/roots", s.device(s.filesRoots))
	mux.Handle("GET /api/v1/device/files/list", s.device(s.filesList))
	mux.Handle("GET /api/v1/device/files/recent", s.device(s.withIndex(s.filesRecent)))
	mux.Handle("GET /api/v1/device/files/search", s.device(s.withIndex(s.filesSearch)))
	mux.Handle("POST /api/v1/device/files/ticket", s.device(s.filesTicket))
	mux.Handle("POST /api/v1/device/send", s.device(s.deviceSend))
	mux.Handle("GET /api/v1/device/layout", s.device(s.deviceLayoutGet))
	mux.Handle("PUT /api/v1/device/layout", s.device(s.deviceLayoutPut))
	mux.Handle("GET /api/v1/device/notifications", s.device(s.deviceNotifications))
	mux.Handle("DELETE /api/v1/device/me", s.device(s.deviceUnlink))
	mux.Handle("POST /api/v1/device/phone-notifications", s.device(s.devicePhoneNote))
	mux.Handle("DELETE /api/v1/device/phone-notifications/{key}", s.device(s.devicePhoneNoteGone))
	mux.Handle("POST /api/v1/device/clipboard", s.device(s.deviceClipboard))
	mux.Handle("GET /api/v1/device/clipboard/{id}/data", s.device(s.deviceClipboardData))
	mux.Handle("GET /api/v1/device/events", s.device(s.deviceEvents))
	mux.Handle("POST /api/v1/device/state", s.device(s.deviceState))
	mux.Handle("GET /api/v1/device/addresses", s.device(s.deviceAddresses))
	mux.Handle("POST /api/v1/device/open", s.device(s.deviceOpen))
	mux.Handle("GET /api/v1/device/ai/conversations", s.device(s.withAI(s.aiListConversations)))
	mux.Handle("POST /api/v1/device/ai/conversations", s.device(s.withAI(s.aiCreateConversation)))
	mux.Handle("GET /api/v1/device/ai/conversations/{id}", s.device(s.withAI(s.aiGetConversation)))
	mux.Handle("DELETE /api/v1/device/ai/conversations/{id}", s.device(s.withAI(s.aiDeleteConversation)))
	mux.Handle("POST /api/v1/device/ai/conversations/{id}/send", s.device(s.withAI(s.aiSend)))
	mux.Handle("GET /api/v1/device/wifi/saved", s.device(s.deviceWifiSaved))
	mux.Handle("POST /api/v1/device/wifi/share", s.device(s.deviceWifiShare))
}

// withAI answers 503 when this server has no AI Assistant.
func (s *Server) withAI(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.AI == nil {
			writeError(w, http.StatusServiceUnavailable, "no_ai", "NoCap AI isn't available on this server.")
			return
		}
		h(w, r)
	}
}

// withIndex answers an empty result when there's no search index.
func (s *Server) withIndex(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.FileIndex == nil {
			writeJSON(w, http.StatusOK, map[string]any{"results": []files.Hit{}, "indexed": 0, "building": false, "capped": false})
			return
		}
		h(w, r)
	}
}

// ---------- summary ----------

type usageJSON struct {
	Total   uint64  `json:"total"`
	Used    uint64  `json:"used"`
	Percent float64 `json:"percent"`
}

type cpuJSON struct {
	Percent float64 `json:"percent"`
	Cores   int     `json:"cores"`
}

type netJSON struct {
	RxRate float64 `json:"rx_rate"` // bytes/s, all interfaces but loopback
	TxRate float64 `json:"tx_rate"`
}

type deviceSummaryJSON struct {
	Server  pairInfo  `json:"server"`
	Ready   bool      `json:"ready"`
	Uptime  float64   `json:"uptime"`
	CPU     cpuJSON   `json:"cpu"`
	Memory  usageJSON `json:"memory"`
	Storage usageJSON `json:"storage"`
	Network netJSON   `json:"network"`
	Updates int       `json:"updates"` // pending NoCapOS and system package updates, as last checked
	Time    time.Time `json:"time"`
}

// netTotals adds up every interface but loopback.
func netTotals(list []hardware.NetStat) netJSON {
	var out netJSON
	for _, n := range list {
		if n.Interface != "lo" {
			out.RxRate += n.RxRate
			out.TxRate += n.TxRate
		}
	}
	return out
}

// pendingUpdates counts what the last update checks found.
func pendingUpdates() int {
	n := 0
	updateCache.Lock()
	if c := updateCache.info; c != nil && c.Available {
		n++
	}
	updateCache.Unlock()
	sysPkgCache.Lock()
	for _, u := range sysPkgCache.updates {
		if !u.Held {
			n++
		}
	}
	sysPkgCache.Unlock()
	return n
}

// deviceSummary is a small, read-only health card for widgets.
func (s *Server) deviceSummary(w http.ResponseWriter, r *http.Request) {
	id, err := s.nocapID()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := deviceSummaryJSON{
		Server:  pairInfo{NocapID: id, Name: s.serverName(), Version: s.Version},
		CPU:     cpuJSON{Cores: runtime.NumCPU()},
		Time:    time.Now().UTC().Truncate(time.Second),
		Updates: pendingUpdates(),
	}
	if s.Sampler != nil {
		if snap := s.Sampler.Latest(); snap != nil {
			out.Ready = true
			out.Uptime = snap.Uptime
			out.CPU.Percent = snap.CPU.Percent
			if n := len(snap.CPU.PerCore); n > 0 {
				out.CPU.Cores = n
			}
			out.Memory = usageJSON{Total: snap.Memory.Total, Used: snap.Memory.Used, Percent: snap.Memory.Percent}
			if len(snap.Disks) > 0 {
				d := snap.Disks[0]
				for _, x := range snap.Disks {
					if x.Path == "/" {
						d = x
						break
					}
				}
				out.Storage = usageJSON{Total: d.Total, Used: d.Used, Percent: d.Percent}
			}
			out.Network = netTotals(snap.Network)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------- look ----------

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// devicePrefsFrom picks the bits of the desktop's saved look the phone uses.
func devicePrefsFrom(data string) map[string]string {
	out := map[string]string{"wallpaper": "", "lingo": "", "accent": ""}
	var p map[string]any
	if data == "" || json.Unmarshal([]byte(data), &p) != nil {
		return out
	}
	str := func(k string) string { v, _ := p[k].(string); return v }
	switch wp := str("wallpaper"); {
	case wp == "tide" || wp == "nebula" || wp == "midnight":
		out["wallpaper"] = wp
	case wp == "custom" || strings.HasPrefix(wp, "custom-"):
		out["wallpaper"] = "custom"
	}
	if l := str("lingo"); l == "genz" || l == "standard" {
		out["lingo"] = l
	}
	if a := str("accent"); hexColor.MatchString(a) {
		out["accent"] = a
	}
	return out
}

func (s *Server) devicePrefs(w http.ResponseWriter, r *http.Request) {
	data, _, err := s.Store.UserPrefs(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, devicePrefsFrom(data))
}

// ---------- Send to NoCap ----------

// sendFolder is where things sent from a phone land, at the top of its
// owner's home.
const sendFolder = "NoCap Link"

const maxSendText = 1_000_000

// sendHome is the owner's home location id, or "" when they have none.
func (s *Server) sendHome(r *http.Request) string {
	if s.Spaces == nil {
		return ""
	}
	v := s.viewer(r)
	id := spaces.HomeRootID(v.Name)
	if v.Admin {
		// Administrators browse whole drives, but still have a home of their own.
		if _, err := s.Spaces.EnsureHome(v.Name); err != nil {
			return ""
		}
	} else {
		found := false
		for _, l := range s.Spaces.Locations(v) {
			if l.ID == id && l.Kind == "home" {
				found = true
				break
			}
		}
		if !found {
			return ""
		}
	}
	if !s.access(r, id, sendFolder).CanWrite() {
		return ""
	}
	return id
}

// sendName makes a safe file name for a text sent from a phone.
func sendName(title, deviceName string, at time.Time) string {
	clean := func(in string) string {
		var b strings.Builder
		for _, c := range in {
			if unicode.IsControl(c) || strings.ContainsRune(`/\:*?"<>|`, c) {
				continue
			}
			b.WriteRune(c)
		}
		out := strings.TrimSpace(b.String())
		if utf8.RuneCountInString(out) > 80 {
			out = strings.TrimSpace(string([]rune(out)[:80]))
		}
		return strings.TrimRight(out, ". ")
	}
	base := clean(title)
	if base == "" {
		base = clean("From " + deviceName)
	}
	if base == "" {
		base = "From my phone"
	}
	name := base + " " + at.Format("2006-01-02 150405") + ".txt"
	if files.ValidName(name) != nil {
		name = "From my phone " + at.Format("2006-01-02 150405") + ".txt"
	}
	return name
}

type sendTextRequest struct {
	Text  string `json:"text"`
	Title string `json:"title"`
}

// deviceSend saves files or text shared from the phone into
// <home>/NoCap Link (keeping both on a name clash).
func (s *Server) deviceSend(w http.ResponseWriter, r *http.Request) {
	d, u := deviceFrom(r.Context()), userFrom(r.Context())
	root := s.sendHome(r)
	if root == "" {
		writeError(w, http.StatusServiceUnavailable, "no_home", "Your account doesn't have a home folder on this server.")
		return
	}
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "multipart/form-data" && mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "send multipart/form-data files or JSON text")
		return
	}
	// Read a JSON body before making the folder, so a bad request leaves no trace.
	var text sendTextRequest
	if mt == "application/json" {
		if !decodeJSONLimit(w, r, &text, 8*maxSendText) {
			return
		}
		if len(text.Text) == 0 || len(text.Text) > maxSendText {
			writeError(w, http.StatusBadRequest, "validation", "Send some text (up to 1 MB).")
			return
		}
	}
	if _, err := s.Files.Mkdir(root, ".", sendFolder); err == nil {
		s.ownFiles(root, sendFolder)
	} else if !errors.Is(err, fs.ErrExist) {
		s.fileError(w, r, err)
		return
	}

	saved := []string{}
	if mt == "application/json" {
		name := sendName(text.Title, d.Name, time.Now())
		rel, err := s.Files.UploadWith(root, sendFolder, name, strings.NewReader(text.Text), files.ConflictRename)
		if err != nil {
			s.audit(r, u.ID, "devices.send", d.Name, false, "text")
			s.fileError(w, r, err)
			return
		}
		s.ownFiles(root, rel)
		saved = append(saved, display(rel))
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, s.Config.MaxUpload)
		mr, err := r.MultipartReader()
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "expected multipart/form-data")
			return
		}
		tooBig := func(err error) bool {
			var mbe *http.MaxBytesError
			return errors.As(err, &mbe)
		}
		for {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				s.auditSend(r, u.ID, d.Name, saved, false)
				if tooBig(err) {
					writeError(w, http.StatusRequestEntityTooLarge, "too_large", "That's bigger than this server takes in one go.")
					return
				}
				writeError(w, http.StatusBadRequest, "bad_request", "malformed upload")
				return
			}
			name := part.FileName()
			if name == "" {
				part.Close()
				continue
			}
			name = path.Base(strings.ReplaceAll(name, `\`, "/"))
			rel, err := s.Files.UploadWith(root, sendFolder, name, part, files.ConflictRename)
			part.Close()
			if err != nil {
				s.auditSend(r, u.ID, d.Name, saved, false)
				if tooBig(err) {
					writeError(w, http.StatusRequestEntityTooLarge, "too_large", "That's bigger than this server takes in one go.")
					return
				}
				s.fileError(w, r, err)
				return
			}
			s.ownFiles(root, rel)
			saved = append(saved, display(rel))
		}
		if len(saved) == 0 {
			writeError(w, http.StatusBadRequest, "validation", "Nothing to save: attach at least one file.")
			return
		}
	}
	s.auditSend(r, u.ID, d.Name, saved, true)
	if s.FileIndex != nil {
		s.FileIndex.Touch()
	}
	writeJSON(w, http.StatusCreated, map[string]any{"root": root, "folder": "/" + sendFolder, "paths": saved})
}

func (s *Server) auditSend(r *http.Request, userID, deviceName string, saved []string, ok bool) {
	s.audit(r, userID, "devices.send", deviceName, ok, fmt.Sprintf("%d item(s) to %s", len(saved), "/"+sendFolder))
}

// ---------- home-screen backup ----------

const maxLayoutBytes = 256 << 10

var safeFileID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// layoutPath is where a person's phone home-screen backup is kept.
func (s *Server) layoutPath(userID string) (string, error) {
	if !safeFileID.MatchString(userID) {
		return "", fmt.Errorf("device layout: unsafe user id %q", userID)
	}
	return filepath.Join(s.Config.DataDir, "device-layouts", userID+".json"), nil
}

func (s *Server) deviceLayoutGet(w http.ResponseWriter, r *http.Request) {
	p, err := s.layoutPath(userFrom(r.Context()).ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		writeJSON(w, http.StatusOK, map[string]any{"layout": nil})
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := map[string]any{"layout": json.RawMessage(data)}
	if st, err := os.Stat(p); err == nil {
		out["updated_at"] = st.ModTime().UTC().Truncate(time.Second)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deviceLayoutPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Layout json.RawMessage `json:"layout"`
	}
	if !decodeJSONLimit(w, r, &req, maxLayoutBytes+1024) {
		return
	}
	raw := bytes.TrimSpace(req.Layout)
	var obj map[string]any
	if len(raw) == 0 || len(raw) > maxLayoutBytes || raw[0] != '{' || json.Unmarshal(raw, &obj) != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "layout must be a JSON object under 256 KB")
		return
	}
	p, err := s.layoutPath(userFrom(r.Context()).ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := writeFileAtomic(p, raw); err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeFileAtomic writes data to p (0600) through a temp file and a rename.
func writeFileAtomic(p string, data []byte) error {
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".layout-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // a no-op after a successful rename
	if err := f.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// ---------- notifications ----------

// deviceNotifications: the server's notifications (read-only; nothing is
// marked read). Administrators get them all, everyone else the ones meant
// for everyone (see noticeForEveryone).
func (s *Server) deviceNotifications(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, 100)
	after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
	admin := userFrom(r.Context()).Role == store.RoleAdmin
	items := []notify.Notification{}
	if s.notify != nil {
		want := limit
		if after > 0 || !admin {
			want = 500 // filtered below, so look further back
		}
		list, _, err := s.notify.List(r.Context(), want)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		for _, n := range list {
			if n.ID > after && (admin || noticeForEveryone(n)) {
				items = append(items, n)
			}
		}
		sort.SliceStable(items, func(i, j int) bool { return items[i].ID > items[j].ID })
		if len(items) > limit {
			items = items[:limit]
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"admin": admin, "items": items})
}

// ---------- unlink ----------

// deviceUnlink lets the phone remove its own link.
func (s *Server) deviceUnlink(w http.ResponseWriter, r *http.Request) {
	d := deviceFrom(r.Context())
	if _, err := s.Store.RevokeDevice(r.Context(), d.ID, d.UserID, time.Now()); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, r, err)
		return
	}
	s.link().forgetDevice(d.UserID, d.ID)
	s.mirrorForget(d.ID)
	s.audit(r, d.UserID, "devices.revoke", d.Name, true, "unlinked from the phone")
	w.WriteHeader(http.StatusNoContent)
}
