package api

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/hardware"
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
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	tok := base64.RawURLEncoding.EncodeToString(b)
	now := time.Now()
	exp := now.Add(fileTicketTTL)
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
		errors.Is(err, files.ErrNotDir), errors.Is(err, files.ErrIsDir), errors.Is(err, files.ErrIntoSelf):
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
	w.WriteHeader(http.StatusNoContent)
}

type transferRequest struct {
	Root  string   `json:"root"`
	Paths []string `json:"paths"`
	Dest  string   `json:"dest"`
	Move  bool     `json:"move"`
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
	out, err := s.Files.Transfer(req.Root, rels, dest, req.Move)
	action := "files.copy"
	if req.Move {
		action = "files.move"
	}
	s.audit(r, userFrom(r.Context()).ID, action, req.Root+":"+strings.Join(rels, ", ")+" → "+display(dest), err == nil, "")
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	for i := range out {
		out[i] = display(out[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"paths": out})
}

// filesUpload streams multipart parts straight to disk (no buffering in memory).
func (s *Server) filesUpload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dir, err := files.Clean(q.Get("path"))
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.Config.MaxUpload)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "expected multipart/form-data")
		return
	}
	var saved []string
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
		rel, err := s.Files.Upload(q.Get("root"), dir, name, part)
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
		saved = append(saved, display(rel))
	}
	writeJSON(w, http.StatusCreated, map[string]any{"paths": saved})
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
