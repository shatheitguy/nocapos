package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/hardware"
	"alfaos/alfad/internal/store"
)

const (
	maxTextBytes   = 2 << 20 // text viewer/editor limit
	fileTicketTTL  = 10 * time.Minute
	maxTicketCount = 4096
)

// ---------- file tickets ----------
//
// <img>, <video> and download links cannot send an Authorization header, so
// the UI requests a ticket scoped to one file or folder and embeds it in the
// URL. Tickets are multi-use (video seeking issues many Range requests) but
// expire after 10 minutes and never grant access outside their prefix.

type fileTicket struct {
	userID string
	root   string
	prefix string
	exp    time.Time
}

type fileTicketStore struct {
	mu sync.Mutex
	m  map[string]fileTicket
}

func (t *fileTicketStore) issue(userID, root, prefix string) (string, time.Time) {
	return t.issueTTL(userID, root, prefix, fileTicketTTL)
}

func (t *fileTicketStore) issueTTL(userID, root, prefix string, ttl time.Duration) (string, time.Time) {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	tok := base64.RawURLEncoding.EncodeToString(b)
	now := time.Now()
	exp := now.Add(ttl)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = make(map[string]fileTicket)
	}
	for k, v := range t.m {
		if now.After(v.exp) || len(t.m) > maxTicketCount {
			delete(t.m, k)
		}
	}
	t.m[tok] = fileTicket{userID: userID, root: root, prefix: prefix, exp: exp}
	return tok, exp
}

func (t *fileTicketStore) check(tok, root, rel string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	v, ok := t.m[tok]
	if !ok || time.Now().After(v.exp) || v.root != root {
		return "", false
	}
	if v.prefix != "." && rel != v.prefix && !strings.HasPrefix(rel, v.prefix+"/") {
		return "", false
	}
	return v.userID, true
}

// ---------- helpers ----------

func (s *Server) fileError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, files.ErrUnknownRoot):
		writeError(w, http.StatusNotFound, "not_found", "no such file or folder")
	case errors.Is(err, fs.ErrExist):
		writeError(w, http.StatusConflict, "exists", "an item with that name already exists")
	case errors.Is(err, fs.ErrPermission):
		writeError(w, http.StatusForbidden, "permission_denied", "permission denied by the operating system")
	case errors.Is(err, files.ErrProtected):
		writeError(w, http.StatusForbidden, "protected", err.Error())
	case errors.Is(err, files.ErrChanged):
		writeError(w, http.StatusConflict, "changed", err.Error())
	case errors.Is(err, files.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", err.Error())
	case errors.Is(err, files.ErrNotText):
		writeError(w, http.StatusUnsupportedMediaType, "not_text", err.Error())
	case errors.Is(err, files.ErrBadPath), errors.Is(err, files.ErrBadName), errors.Is(err, files.ErrIsRoot),
		errors.Is(err, files.ErrNotDir), errors.Is(err, files.ErrIsDir), errors.Is(err, files.ErrIntoSelf),
		errors.Is(err, files.ErrNotArchive), errors.Is(err, files.ErrUnsafeArchive):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	case strings.Contains(err.Error(), "escapes from parent"):
		// os.Root refused a path (e.g. a symlink pointing outside the root).
		writeError(w, http.StatusBadRequest, "bad_request", "path is outside the storage location")
	default:
		s.internalError(w, r, err)
	}
}

func cleanAll(ps []string) ([]string, error) {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		c, err := files.Clean(p)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func display(rel string) string {
	if rel == "." {
		return "/"
	}
	return "/" + rel
}

// ---------- handlers ----------

type rootView struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Total uint64 `json:"total"`
	Free  uint64 `json:"free"`
}

func (s *Server) filesRoots(w http.ResponseWriter, r *http.Request) {
	out := []rootView{}
	for _, rt := range s.Files.Roots() {
		v := rootView{ID: rt.ID, Name: rt.Name}
		if total, _, free, err := hardware.DiskUsage(rt.Path); err == nil {
			v.Total, v.Free = total, free
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) filesList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rel, err := files.Clean(q.Get("path"))
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	entries, err := s.Files.List(q.Get("root"), rel)
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"root": q.Get("root"), "path": display(rel), "entries": entries, "unix_perms": files.UnixPerms})
}

func (s *Server) filesReadText(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rel, err := files.Clean(q.Get("path"))
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	text, st, err := s.Files.ReadText(q.Get("root"), rel, maxTextBytes)
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"content": text, "size": st.Size(), "mod_time": st.ModTime().UTC()})
}

type saveTextRequest struct {
	Root    string     `json:"root"`
	Path    string     `json:"path"`
	Content string     `json:"content"`
	ModTime *time.Time `json:"mod_time,omitempty"` // optimistic concurrency check
}

func (s *Server) filesWriteText(w http.ResponseWriter, r *http.Request) {
	var req saveTextRequest
	if !decodeJSONLimit(w, r, &req, maxTextBytes*2) {
		return
	}
	if len(req.Content) > maxTextBytes {
		s.fileError(w, r, files.ErrTooLarge)
		return
	}
	rel, err := files.Clean(req.Path)
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	st, err := s.Files.WriteText(req.Root, rel, req.Content, req.ModTime)
	s.audit(r, userFrom(r.Context()).ID, "files.save", req.Root+":"+display(rel), err == nil, "")
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"size": st.Size(), "mod_time": st.ModTime().UTC()})
}

type mkdirRequest struct {
	Root string `json:"root"`
	Path string `json:"path"` // parent folder
	Name string `json:"name"`
}

func (s *Server) filesMkdir(w http.ResponseWriter, r *http.Request) {
	var req mkdirRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	dir, err := files.Clean(req.Path)
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	rel, err := s.Files.Mkdir(req.Root, dir, strings.TrimSpace(req.Name))
	s.audit(r, userFrom(r.Context()).ID, "files.mkdir", req.Root+":"+display(path.Join(dir, req.Name)), err == nil, "")
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"path": display(rel)})
}

type renameRequest struct {
	Root string `json:"root"`
	Path string `json:"path"`
	Name string `json:"name"`
}

func (s *Server) filesRename(w http.ResponseWriter, r *http.Request) {
	var req renameRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	rel, err := files.Clean(req.Path)
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	dst, err := s.Files.Rename(req.Root, rel, strings.TrimSpace(req.Name))
	s.audit(r, userFrom(r.Context()).ID, "files.rename", req.Root+":"+display(rel), err == nil, req.Name)
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": display(dst)})
}

type deleteRequest struct {
	Root      string   `json:"root"`
	Paths     []string `json:"paths"`
	Permanent bool     `json:"permanent"`
}

func (s *Server) filesDelete(w http.ResponseWriter, r *http.Request) {
	var req deleteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	rels, err := cleanAll(req.Paths)
	if err != nil || len(rels) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "no paths given")
		return
	}
	err = s.Files.Delete(req.Root, rels, req.Permanent)
	detail := "to recycle bin"
	if req.Permanent {
		detail = "permanent"
	}
	s.audit(r, userFrom(r.Context()).ID, "files.delete", req.Root+":"+strings.Join(rels, ", "), err == nil, detail)
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	s.FileIndex.Remove(req.Root, rels)
	w.WriteHeader(http.StatusNoContent)
}

type transferRequest struct {
	Root     string   `json:"root"`
	Paths    []string `json:"paths"`
	Dest     string   `json:"dest"`
	Move     bool     `json:"move"`
	Conflict string   `json:"conflict"`   // rename (default) | replace | skip
	Async    bool     `json:"background"` // run as a job with progress
}

func (s *Server) filesTransfer(w http.ResponseWriter, r *http.Request) {
	var req transferRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	rels, err := cleanAll(req.Paths)
	if err != nil || len(rels) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "no paths given")
		return
	}
	dest, err := files.Clean(req.Dest)
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	conflict, err := files.ParseConflict(req.Conflict)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	action := "files.copy"
	if req.Move {
		action = "files.move"
	}
	what := req.Root + ":" + strings.Join(rels, ", ") + " → " + display(dest)
	uid := userFrom(r.Context()).ID

	if req.Async {
		// The job outlives this request: audit with a detached copy of it.
		ar := r.Clone(context.WithoutCancel(r.Context()))
		j := s.FileJobs.Start(uid, req.Root, rels, dest, req.Move, conflict, display, func(j files.Job) {
			s.audit(ar, uid, action, what, j.Status == "done", j.Status+" "+j.Error)
			if s.FileIndex != nil {
				s.FileIndex.Touch()
			}
		})
		writeJSON(w, http.StatusAccepted, j)
		return
	}

	res, err := s.Files.TransferWith(r.Context(), req.Root, rels, dest, files.TransferOptions{Move: req.Move, Conflict: conflict})
	s.audit(r, uid, action, what, err == nil, "")
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	for i := range res.Paths {
		res.Paths[i] = display(res.Paths[i])
	}
	for i := range res.Skipped {
		res.Skipped[i] = display(res.Skipped[i])
	}
	writeJSON(w, http.StatusOK, res)
}

// filesConflicts lists which of the items already exist in the destination,
// so the UI can ask "keep both / replace / skip" before starting.
func (s *Server) filesConflicts(w http.ResponseWriter, r *http.Request) {
	var req transferRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	rels, err := cleanAll(req.Paths)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid paths")
		return
	}
	dest, err := files.Clean(req.Dest)
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	names, err := s.Files.Conflicts(req.Root, rels, dest)
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"names": names})
}

func (s *Server) filesJobs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.FileJobs.List(userFrom(r.Context()).ID))
}

func (s *Server) filesJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.FileJobs.Get(r.PathValue("id"), userFrom(r.Context()).ID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such transfer")
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) filesJobCancel(w http.ResponseWriter, r *http.Request) {
	if !s.FileJobs.Cancel(r.PathValue("id"), userFrom(r.Context()).ID) {
		writeError(w, http.StatusNotFound, "not_found", "no such transfer")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "canceling"})
}

func (s *Server) filesUpload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dir, err := files.Clean(q.Get("path"))
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	conflict, err := files.ParseConflict(q.Get("conflict"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.Config.MaxUpload)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "expected multipart/form-data")
		return
	}
	saved, skipped := []string{}, []string{}
	for {
		part, err := mr.NextPart()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				writeError(w, http.StatusRequestEntityTooLarge, "too_large", "upload exceeds the size limit")
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
		// Browsers send a bare name; strip any path a client might add.
		name = path.Base(strings.ReplaceAll(name, `\`, "/"))
		rel, err := s.Files.UploadWith(q.Get("root"), dir, name, part, conflict)
		part.Close()
		s.audit(r, userFrom(r.Context()).ID, "files.upload", q.Get("root")+":"+display(path.Join(dir, name)), err == nil, "")
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				writeError(w, http.StatusRequestEntityTooLarge, "too_large", "upload exceeds the size limit")
				return
			}
			s.fileError(w, r, err)
			return
		}
		if rel == "" {
			skipped = append(skipped, name)
			continue
		}
		saved = append(saved, display(rel))
	}
	writeJSON(w, http.StatusCreated, map[string]any{"paths": saved, "skipped": skipped})
}

type ticketRequest struct {
	Root string `json:"root"`
	Path string `json:"path"`
}

func (s *Server) filesTicket(w http.ResponseWriter, r *http.Request) {
	var req ticketRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	rel, err := files.Clean(req.Path)
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	if _, err := s.Files.Root(req.Root); err != nil {
		s.fileError(w, r, err)
		return
	}
	tok, exp := s.fileTickets.issue(userFrom(r.Context()).ID, req.Root, rel)
	writeJSON(w, http.StatusOK, map[string]any{"ticket": tok, "expires_at": exp.UTC()})
}

// Types rendered inline by the browser. Everything else — notably HTML, SVG
// and XML, which could run script in the Alfa OS origin — is served as an
// opaque download.
var inlineTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".avif": "image/avif", ".bmp": "image/bmp", ".ico": "image/x-icon",
	".mp4": "video/mp4", ".m4v": "video/mp4", ".webm": "video/webm", ".ogv": "video/ogg", ".mov": "video/quicktime",
	".mp3": "audio/mpeg", ".m4a": "audio/mp4", ".aac": "audio/aac", ".wav": "audio/wav",
	".flac": "audio/flac", ".ogg": "audio/ogg", ".opus": "audio/ogg",
	".pdf": "application/pdf",
	".txt": "text/plain; charset=utf-8", ".md": "text/plain; charset=utf-8", ".log": "text/plain; charset=utf-8",
	".csv": "text/plain; charset=utf-8", ".json": "text/plain; charset=utf-8",
}

func (s *Server) filesRaw(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	root := q.Get("root")
	rel, err := files.Clean(q.Get("path"))
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	userID, ok := s.fileTickets.check(q.Get("t"), root, rel)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid_ticket", "invalid or expired file link")
		return
	}
	if u, err := s.Store.UserByID(r.Context(), userID); err != nil || u.Disabled {
		writeError(w, http.StatusUnauthorized, "invalid_ticket", "invalid or expired file link")
		return
	}
	f, st, err := s.Files.Open(root, rel)
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	defer f.Close()

	name := path.Base(rel)
	ctype, inline := inlineTypes[strings.ToLower(path.Ext(name))]
	if !inline || q.Get("download") == "1" {
		inline = false
		if ctype == "" {
			ctype = "application/octet-stream"
		}
	}
	disp := "attachment"
	if inline {
		disp = "inline"
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": name}))
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Cache-Control", "private, max-age=300")
	// Even an inline file gets no script, no plugins and no network access.
	csp := "default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'; sandbox"
	if ctype == "application/pdf" {
		csp = "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; object-src 'self'" // PDF viewers break under sandbox
	}
	h.Set("Content-Security-Policy", csp)
	http.ServeContent(w, r, name, st.ModTime(), f)
}

// ---------- recents, favorites, external storage ----------

// filesRecent lists recently changed files across the locations (from the
// search index, so it costs no disk walk).
func (s *Server) filesRecent(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	count, building, _ := s.FileIndex.Stats()
	writeJSON(w, http.StatusOK, map[string]any{"results": s.FileIndex.Recent(limit), "indexed": count, "building": building})
}

const maxFavorites = 50

// defaultFavoriteNames are pinned for a user who never changed favorites,
// when those folders exist at the top of the first location.
var defaultFavoriteNames = []string{"Documents", "Downloads", "Photos", "Videos", "Music"}

func (s *Server) defaultFavorites() []store.FileFavorite {
	out := []store.FileFavorite{}
	for _, rt := range s.Files.Roots() {
		if rt.ID == "system" || strings.HasPrefix(rt.ID, "net:") {
			continue
		}
		entries, err := s.Files.List(rt.ID, ".")
		if err != nil {
			return out
		}
		for _, want := range defaultFavoriteNames {
			for _, e := range entries {
				if e.Dir && strings.EqualFold(e.Name, want) {
					out = append(out, store.FileFavorite{Root: rt.ID, Path: "/" + e.Name})
					break
				}
			}
		}
		return out
	}
	return out
}

func (s *Server) filesFavorites(w http.ResponseWriter, r *http.Request) {
	favs, set, err := s.Store.FileFavorites(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !set {
		favs = s.defaultFavorites()
	}
	if favs == nil {
		favs = []store.FileFavorite{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"favorites": favs})
}

func (s *Server) filesSetFavorites(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Favorites []store.FileFavorite `json:"favorites"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Favorites) > maxFavorites {
		writeError(w, http.StatusBadRequest, "bad_request", "too many favorites")
		return
	}
	out := []store.FileFavorite{}
	seen := map[string]bool{}
	for _, f := range req.Favorites {
		rel, err := files.Clean(f.Path)
		if err != nil || f.Root == "" || len(f.Root) > 64 {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid favorite")
			return
		}
		key := f.Root + ":" + rel
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, store.FileFavorite{Root: f.Root, Path: display(rel)})
	}
	if err := s.Store.SetFileFavorites(r.Context(), userFrom(r.Context()).ID, out); err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"favorites": out})
}

type externalView struct {
	Name   string `json:"name"`
	Root   string `json:"root"`
	Path   string `json:"path"`
	Device string `json:"device"`
	Total  uint64 `json:"total"`
	Free   uint64 `json:"free"`
}

// filesExternal lists USB and other disks the OS has mounted. They open
// through the whole-disk System location, so they only show when it exists.
func (s *Server) filesExternal(w http.ResponseWriter, r *http.Request) {
	out := []externalView{}
	if _, err := s.Files.Root("system"); err != nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	skip := os.Getenv("ALFA_MOUNT_DIR")
	if skip == "" {
		skip = "/mnt/nocapos"
	}
	for _, m := range files.ExternalMounts(skip) {
		v := externalView{Name: m.Name, Root: "system", Path: m.Path, Device: m.Device}
		if total, _, free, err := hardware.DiskUsage(m.Path); err == nil {
			v.Total, v.Free = total, free
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------- compress / extract ----------

type archiveRequest struct {
	Root  string   `json:"root"`
	Paths []string `json:"paths"` // compress: the items; extract: the one archive
	Dest  string   `json:"dest"`  // folder the result goes into
	Name  string   `json:"name"`  // compress: zip file name
}

func (s *Server) filesCompress(w http.ResponseWriter, r *http.Request) {
	var req archiveRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	rels, err := cleanAll(req.Paths)
	if err != nil || len(rels) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "no paths given")
		return
	}
	dest, err := files.Clean(req.Dest)
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Archive.zip"
	}
	// Check the cheap things now so mistakes show right away, not as a failed job.
	for _, rel := range rels {
		if rel == "." {
			s.fileError(w, r, files.ErrIsRoot)
			return
		}
		if err := s.Files.CheckChange(req.Root, rel); err != nil {
			s.fileError(w, r, err)
			return
		}
	}
	s.startArchiveJob(w, r, "compress", req.Root, rels, dest, func(ctx context.Context, p func(files.Progress)) (files.TransferResult, error) {
		out, err := s.Files.Compress(ctx, req.Root, rels, dest, name, p)
		return files.TransferResult{Paths: nonEmpty(out)}, err
	})
}

func (s *Server) filesExtract(w http.ResponseWriter, r *http.Request) {
	var req archiveRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	rels, err := cleanAll(req.Paths)
	if err != nil || len(rels) != 1 {
		writeError(w, http.StatusBadRequest, "bad_request", "choose one archive")
		return
	}
	dest, err := files.Clean(req.Dest)
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	if files.ArchiveStem(path.Base(rels[0])) == "" {
		s.fileError(w, r, files.ErrNotArchive)
		return
	}
	s.startArchiveJob(w, r, "extract", req.Root, rels, dest, func(ctx context.Context, p func(files.Progress)) (files.TransferResult, error) {
		out, err := s.Files.Extract(ctx, req.Root, rels[0], dest, p)
		return files.TransferResult{Paths: nonEmpty(out)}, err
	})
}

func nonEmpty(p string) []string {
	if p == "" {
		return []string{}
	}
	return []string{display(p)}
}

func (s *Server) startArchiveJob(w http.ResponseWriter, r *http.Request, kind, root string, rels []string, dest string,
	work func(ctx context.Context, p func(files.Progress)) (files.TransferResult, error)) {
	if _, err := s.Files.Root(root); err != nil {
		s.fileError(w, r, err)
		return
	}
	uid := userFrom(r.Context()).ID
	ar := r.Clone(context.WithoutCancel(r.Context()))
	what := root + ":" + strings.Join(rels, ", ") + " → " + display(dest)
	j := s.FileJobs.Run(uid, kind, root, rels, dest, files.ConflictRename, display, work, func(j files.Job) {
		s.audit(ar, uid, "files."+kind, what, j.Status == "done", j.Status+" "+j.Error)
		if s.FileIndex != nil {
			s.FileIndex.Touch()
		}
	})
	writeJSON(w, http.StatusAccepted, j)
}
