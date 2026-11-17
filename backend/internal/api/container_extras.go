package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"alfaos/alfad/internal/appstore"
	"alfaos/alfad/internal/docker"
	"alfaos/alfad/internal/usbdev"
)

// The Containers app beyond the container list: disk usage, whole-project
// actions, running a command, files inside a container, images (pull with
// progress, tag, remove, prune), volumes, network connect/disconnect and
// the host's USB/serial devices. Admin only (routes); destructive actions
// are audited.

func (s *Server) containerExtraRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/docker/usage", s.admin(s.dockerUsage))
	mux.Handle("POST /api/v1/docker/projects/{name}/{action}", s.admin(s.projectAction))
	mux.Handle("POST /api/v1/containers/{id}/exec", s.admin(s.containerExec))
	mux.Handle("GET /api/v1/containers/{id}/files", s.admin(s.containerFiles))
	mux.Handle("GET /api/v1/containers/{id}/files/content", s.admin(s.containerFileContent))
	mux.Handle("GET /api/v1/containers/{id}/files/download", s.admin(s.containerFileDownload))
	mux.Handle("POST /api/v1/containers/{id}/files/upload", s.admin(s.containerFileUpload))
	mux.Handle("GET /api/v1/docker/images", s.admin(s.dockerImages))
	mux.Handle("POST /api/v1/docker/images/pull", s.admin(s.imagePull))
	mux.Handle("GET /api/v1/docker/images/pull/{job}", s.admin(s.imagePullJob))
	mux.Handle("POST /api/v1/docker/images/tag", s.admin(s.imageTag))
	mux.Handle("POST /api/v1/docker/images/remove", s.admin(s.imageRemove))
	mux.Handle("POST /api/v1/docker/images/prune", s.admin(s.imagePrune))
	mux.Handle("GET /api/v1/docker/volumes/details", s.admin(s.volumeDetails))
	mux.Handle("DELETE /api/v1/docker/volumes/{name}", s.admin(s.volumeRemove))
	mux.Handle("POST /api/v1/docker/volumes/prune", s.admin(s.volumePrune))
	mux.Handle("POST /api/v1/docker/networks/{name}/connect", s.admin(s.networkConnect))
	mux.Handle("POST /api/v1/docker/networks/{name}/disconnect", s.admin(s.networkDisconnect))
	mux.Handle("GET /api/v1/docker/devices", s.admin(s.hostDevices))
	mux.Handle("GET /api/v1/docker/local-addresses", s.admin(s.localAddrGet))
	mux.Handle("PUT /api/v1/docker/local-addresses", s.admin(s.localAddrSet))
	mux.Handle("GET /api/v1/docker/local-addresses/ca.crt", s.admin(s.localAddrCA))
}

// ---------- disk usage ----------

type usageCache struct {
	mu   sync.Mutex
	at   time.Time
	data *docker.Usage
	busy chan struct{}
}

const usageFor = 30 * time.Second

// usage returns Docker's disk usage, asked at most every 30 s (it can be slow).
func (s *Server) usage(ctx context.Context, fresh bool) (*docker.Usage, error) {
	c := &s.dockerUsageCache
	c.mu.Lock()
	if c.data != nil && !fresh && time.Since(c.at) < usageFor {
		d := c.data
		c.mu.Unlock()
		return d, nil
	}
	if c.busy != nil { // someone is already asking: wait for that answer
		ch := c.busy
		c.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		c.mu.Lock()
		d := c.data
		c.mu.Unlock()
		if d == nil {
			return nil, errors.New("disk usage isn't available")
		}
		return d, nil
	}
	ch := make(chan struct{})
	c.busy = ch
	c.mu.Unlock()
	uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	d, err := s.Docker.DiskUsage(uctx)
	cancel()
	c.mu.Lock()
	if err == nil {
		c.data, c.at = d, time.Now()
	}
	c.busy = nil
	close(ch)
	c.mu.Unlock()
	return d, err
}

// GET /api/v1/docker/usage[?fresh=1]
func (s *Server) dockerUsage(w http.ResponseWriter, r *http.Request) {
	u, err := s.usage(r.Context(), r.URL.Query().Get("fresh") == "1")
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// ---------- projects ----------

var projectActions = map[string]bool{"start": true, "stop": true, "restart": true, "remove": true}

// POST /api/v1/docker/projects/{name}/{action}: the same action on every
// container of a Compose project. Projects saved as stacks go through the
// stack (so "remove" takes it down but keeps its compose file).
func (s *Server) projectAction(w http.ResponseWriter, r *http.Request) {
	name, action := r.PathValue("name"), r.PathValue("action")
	if !validContainerRef(name) || !projectActions[action] {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid project or action")
		return
	}
	uid := userFrom(r.Context()).ID
	all, err := s.Docker.ListContainers(r.Context(), true)
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	var members []docker.Container
	for _, c := range all {
		if c.Project == name {
			members = append(members, c)
		}
	}
	if len(members) == 0 {
		writeError(w, http.StatusNotFound, "not_found", "no containers belong to that project")
		return
	}
	for _, c := range members {
		if c.System && action != "start" {
			s.audit(r, uid, "project."+action, name, false, "system containers are protected")
			writeError(w, http.StatusForbidden, "protected", "NoCapOS's own containers can't be stopped or removed")
			return
		}
		if action == "remove" && c.Labels[appstore.LabelApp] != "" {
			writeError(w, http.StatusConflict, "app", "this is an App Store app; uninstall it from the App Store")
			return
		}
	}
	if s.Stacks != nil && s.Stacks.Exists(name) {
		stackAction := action
		if action == "remove" {
			stackAction = "down"
		}
		err := s.Stacks.Run(r.Context(), name, stackAction)
		s.audit(r, uid, "project."+action, name, err == nil, "stack: "+stackAction+errText(err))
		if err != nil {
			s.stackError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"stack": true})
		return
	}
	var failed []string
	for _, c := range members {
		s.crashes.Expect(c.ID, c.Name)
		var err error
		switch action {
		case "start":
			err = s.Docker.StartContainer(r.Context(), c.ID)
		case "stop":
			err = s.Docker.StopContainer(r.Context(), c.ID, stopTimeout)
		case "restart":
			err = s.Docker.RestartContainer(r.Context(), c.ID, stopTimeout)
		case "remove":
			err = s.Docker.RemoveContainer(r.Context(), c.ID)
		}
		if err != nil {
			failed = append(failed, c.Name+": "+err.Error())
		}
	}
	detail := strconv.Itoa(len(members)) + " containers"
	if len(failed) > 0 {
		detail += "; failed: " + strings.Join(failed, "; ")
	}
	s.audit(r, uid, "project."+action, name, len(failed) == 0, detail)
	if len(failed) > 0 {
		writeError(w, http.StatusBadGateway, "partial", strings.Join(failed, "; "))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"containers": len(members)})
}

// ---------- one command ----------

const (
	execTimeout = 60 * time.Second
	execOutput  = 256 << 10
)

// POST /api/v1/containers/{id}/exec {command}: runs one command with sh -c
// and returns what it printed. Audited (the command is kept, shortened).
func (s *Server) containerExec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validContainerRef(id) {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid container id")
		return
	}
	var req struct {
		Command string `json:"command"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	cmd := strings.TrimSpace(req.Command)
	if cmd == "" || len(cmd) > 4096 || strings.ContainsRune(cmd, 0) {
		writeError(w, http.StatusBadRequest, "bad_request", "enter a command (up to 4096 characters)")
		return
	}
	d, err := s.Docker.InspectContainer(r.Context(), id)
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	if !d.Running {
		writeError(w, http.StatusConflict, "not_running", "start the container to run commands in it")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), execTimeout)
	defer cancel()
	res, err := s.Docker.ExecRun(ctx, d.ID, []string{"sh", "-c", cmd}, execOutput)
	short := cmd
	if len(short) > 200 {
		short = short[:200] + "…"
	}
	s.audit(r, userFrom(r.Context()).ID, "container.exec", d.Name, err == nil, short)
	if errors.Is(err, context.DeadlineExceeded) {
		writeError(w, http.StatusGatewayTimeout, "timeout", "the command took longer than a minute and was left running; use the terminal for long jobs")
		return
	}
	if err != nil {
		s.editError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ---------- files inside a container ----------

func (s *Server) fileTarget(w http.ResponseWriter, r *http.Request) (*docker.ContainerDetail, string, bool) {
	id := r.PathValue("id")
	if !validContainerRef(id) {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid container id")
		return nil, "", false
	}
	p, err := docker.CleanPath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
		return nil, "", false
	}
	d, err := s.Docker.InspectContainer(r.Context(), id)
	if err != nil {
		s.dockerError(w, r, err)
		return nil, "", false
	}
	return d, p, true
}

func (s *Server) fileErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case docker.IsNotFound(err):
		writeError(w, http.StatusNotFound, "not_found", "nothing at that path")
	case errors.Is(err, docker.ErrIsDir):
		writeError(w, http.StatusBadRequest, "is_dir", err.Error())
	case errors.Is(err, docker.ErrBadPath), errors.Is(err, docker.ErrReadOnly):
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
	default:
		s.editError(w, r, err)
	}
}

// GET /api/v1/containers/{id}/files?path=/etc
func (s *Server) containerFiles(w http.ResponseWriter, r *http.Request) {
	d, p, ok := s.fileTarget(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	l, err := s.Docker.ListDir(ctx, d.ID, p, d.Running)
	if err != nil {
		s.fileErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

const previewMax = 1 << 20

// GET /api/v1/containers/{id}/files/content?path=: the start of a file for
// a preview (text only; binary files just say so).
func (s *Server) containerFileContent(w http.ResponseWriter, r *http.Request) {
	d, p, ok := s.fileTarget(w, r)
	if !ok {
		return
	}
	f, err := s.Docker.OpenFile(r.Context(), d.ID, p)
	if err != nil {
		s.fileErr(w, r, err)
		return
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, previewMax))
	if err != nil {
		s.fileErr(w, r, err)
		return
	}
	binary := isBinary(buf)
	out := map[string]any{"path": p, "size": f.Size, "binary": binary, "truncated": f.Size > int64(len(buf))}
	if !binary {
		out["content"] = string(buf)
	}
	writeJSON(w, http.StatusOK, out)
}

// isBinary: NUL bytes or text that isn't UTF-8 (a cut-off last rune is fine).
func isBinary(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size <= 1 {
			return len(b) > 3 // a rune cut by the preview limit
		}
		b = b[size:]
	}
	return false
}

// GET /api/v1/containers/{id}/files/download?path=: a file as itself, a
// folder as a .tar archive.
func (s *Server) containerFileDownload(w http.ResponseWriter, r *http.Request) {
	d, p, ok := s.fileTarget(w, r)
	if !ok {
		return
	}
	st, err := s.Docker.StatPath(r.Context(), d.ID, p)
	if err != nil {
		s.fileErr(w, r, err)
		return
	}
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	name := safeFileName(path.Base(p))
	if st.Dir {
		rc, err := s.Docker.OpenArchive(r.Context(), d.ID, p)
		if err != nil {
			s.fileErr(w, r, err)
			return
		}
		defer rc.Close()
		h.Set("Content-Type", "application/x-tar")
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + ".tar"}))
		_, _ = io.Copy(w, rc)
		return
	}
	f, err := s.Docker.OpenFile(r.Context(), d.ID, p)
	if err != nil {
		s.fileErr(w, r, err)
		return
	}
	defer f.Close()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Length", strconv.FormatInt(f.Size, 10))
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	_, _ = io.Copy(w, f)
}

func safeFileName(n string) string {
	n = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\\' || r == '/' {
			return '_'
		}
		return r
	}, n)
	if n == "" || n == "/" || n == "." {
		return "container-files"
	}
	return n
}

const maxUpload = 2 << 30

// POST /api/v1/containers/{id}/files/upload?path=<folder>&name=<file>: the
// body is the file. Not into NoCapOS's own containers or /proc, /sys, /dev.
func (s *Server) containerFileUpload(w http.ResponseWriter, r *http.Request) {
	name, err := docker.CleanName(r.URL.Query().Get("name"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", "that file name can't be used")
		return
	}
	d, dir, ok := s.fileTarget(w, r)
	if !ok {
		return
	}
	if d.System {
		writeError(w, http.StatusForbidden, "protected", "files can't be put into NoCapOS's own containers")
		return
	}
	if !docker.UploadAllowed(dir) {
		writeError(w, http.StatusBadRequest, "bad_path", docker.ErrReadOnly.Error())
		return
	}
	if r.ContentLength < 0 || r.ContentLength > maxUpload {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "files up to 2 GB can be uploaded")
		return
	}
	st, err := s.Docker.StatPath(r.Context(), d.ID, dir)
	if err != nil {
		s.fileErr(w, r, err)
		return
	}
	if !st.Dir && !st.Link {
		writeError(w, http.StatusBadRequest, "bad_path", "upload into a folder")
		return
	}
	body := http.MaxBytesReader(w, r.Body, r.ContentLength)
	err = s.Docker.PutFile(r.Context(), d.ID, dir, name, body, r.ContentLength)
	s.audit(r, userFrom(r.Context()).ID, "container.upload", d.Name, err == nil, path.Join(dir, name)+errText(err))
	if err != nil {
		s.fileErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"path": path.Join(dir, name)})
}

// ---------- images ----------

type imageView struct {
	ID         string    `json:"id"`
	Tags       []string  `json:"tags"`
	Created    time.Time `json:"created"`
	Size       int64     `json:"size"`
	Containers []string  `json:"containers"` // names of the containers made from it
	Dangling   bool      `json:"dangling"`
}

// GET /api/v1/docker/images
func (s *Server) dockerImages(w http.ResponseWriter, r *http.Request) {
	imgs, err := s.Docker.ListImages(r.Context())
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	users := map[string][]string{}
	if list, err := s.Docker.ListContainers(r.Context(), true); err == nil {
		for _, c := range list {
			users[c.ImageID] = append(users[c.ImageID], c.Name)
		}
	}
	out := make([]imageView, 0, len(imgs))
	for _, im := range imgs {
		tags := []string{}
		for _, t := range im.Tags {
			if t != "<none>:<none>" {
				tags = append(tags, t)
			}
		}
		u := users[im.ID]
		if u == nil {
			u = []string{}
		}
		sort.Strings(u)
		out = append(out, imageView{ID: im.ID, Tags: tags, Created: im.Created, Size: im.Size, Containers: u, Dangling: len(tags) == 0})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	writeJSON(w, http.StatusOK, map[string]any{"images": out})
}

type pullLayer struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Current int64  `json:"current"`
	Total   int64  `json:"total"`
}

type pullJob struct {
	ID       string      `json:"id"`
	Image    string      `json:"image"`
	Status   string      `json:"status"`
	Done     bool        `json:"done"`
	Error    string      `json:"error,omitempty"`
	Percent  float64     `json:"percent"`
	Layers   []pullLayer `json:"layers"`
	Started  time.Time   `json:"started"`
	Finished time.Time   `json:"finished,omitzero"`
}

type pullJobs struct {
	mu   sync.Mutex
	jobs map[string]*pullJob
}

func (p *pullJobs) get(id string) (pullJob, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	j, ok := p.jobs[id]
	if !ok {
		return pullJob{}, false
	}
	c := *j
	c.Layers = append([]pullLayer(nil), j.Layers...)
	return c, true
}

func (p *pullJobs) update(id string, fn func(*pullJob)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if j, ok := p.jobs[id]; ok {
		fn(j)
	}
}

func (p *pullJobs) add(j *pullJob) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.jobs == nil {
		p.jobs = map[string]*pullJob{}
	}
	for id, old := range p.jobs { // forget finished ones after a while
		if old.Done && time.Since(old.Finished) > 15*time.Minute {
			delete(p.jobs, id)
		}
	}
	p.jobs[j.ID] = j
}

// applyProgress folds one progress line into a job's layers and percent.
func (j *pullJob) applyProgress(pp docker.PullProgress) {
	if pp.ID == "" || strings.Contains(pp.Status, "Pulling from") {
		if pp.Status != "" {
			j.Status = pp.Status
		}
		return
	}
	i := -1
	for k := range j.Layers {
		if j.Layers[k].ID == pp.ID {
			i = k
			break
		}
	}
	if i < 0 {
		if len(j.Layers) >= 200 {
			return
		}
		j.Layers = append(j.Layers, pullLayer{ID: pp.ID})
		i = len(j.Layers) - 1
	}
	l := &j.Layers[i]
	l.Status = pp.Status
	if strings.HasPrefix(pp.Status, "Downloading") && pp.Detail.Total > 0 {
		l.Current, l.Total = pp.Detail.Current, pp.Detail.Total
	}
	switch pp.Status {
	case "Download complete", "Pull complete", "Already exists", "Verifying Checksum", "Extracting":
		if l.Total > 0 {
			l.Current = l.Total
		}
	}
	var cur, total float64
	done := 0
	for _, x := range j.Layers {
		if x.Status == "Pull complete" || x.Status == "Already exists" {
			done++
		}
		if x.Total > 0 {
			cur += float64(x.Current)
			total += float64(x.Total)
		}
	}
	switch {
	case total > 0:
		j.Percent = 100 * cur / total
	case len(j.Layers) > 0:
		j.Percent = 100 * float64(done) / float64(len(j.Layers))
	}
	if j.Percent > 99 && done < len(j.Layers) {
		j.Percent = 99
	}
}

// POST /api/v1/docker/images/pull {image} → {job}; progress at /pull/{job}.
func (s *Server) imagePull(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Image string `json:"image"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ref := strings.TrimSpace(req.Image)
	if !docker.ValidImageRef(ref) {
		writeError(w, http.StatusBadRequest, "bad_request", "enter an image like nginx:latest or ghcr.io/owner/app:1.2")
		return
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	job := &pullJob{ID: hex.EncodeToString(b), Image: ref, Status: "Starting", Layers: []pullLayer{}, Started: time.Now().UTC()}
	s.pulls.add(job)
	uid, ip := userFrom(r.Context()).ID, clientIP(r, s.Config.TrustedProxies)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		name, tag := splitRef(ref)
		if strings.Contains(ref, "@") { // by digest: the name carries it
			name, tag = ref, ""
		}
		err := s.Docker.PullImage(ctx, name, tag, func(p docker.PullProgress) {
			s.pulls.update(job.ID, func(j *pullJob) { j.applyProgress(p) })
		})
		s.pulls.update(job.ID, func(j *pullJob) {
			j.Done, j.Finished = true, time.Now().UTC()
			if err != nil {
				j.Error = err.Error()
			} else {
				j.Percent, j.Status = 100, "Downloaded"
			}
		})
		s.auditCtx(context.Background(), uid, "image.pull", ref, ip, err == nil, errText(err))
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"job": job.ID})
}

func (s *Server) imagePullJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.pulls.get(r.PathValue("job"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such download")
		return
	}
	writeJSON(w, http.StatusOK, j)
}

// POST /api/v1/docker/images/tag {source, target}
func (s *Server) imageTag(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source string `json:"source"`
		Target string `json:"target"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	src, dst := strings.TrimSpace(req.Source), strings.TrimSpace(req.Target)
	if !docker.ValidImageRef(src) || !docker.ValidImageRef(dst) || strings.Contains(dst, "@") || strings.HasPrefix(dst, "sha256:") {
		writeError(w, http.StatusBadRequest, "bad_request", "enter a new name like myapp:stable")
		return
	}
	repo, tag := splitRef(dst)
	err := s.Docker.TagImage(r.Context(), src, strings.ToLower(repo), tag)
	s.audit(r, userFrom(r.Context()).ID, "image.tag", src+" → "+dst, err == nil, errText(err))
	if err != nil {
		s.editError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/docker/images/remove {ref}: one name, or the whole image by id.
func (s *Server) imageRemove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Ref string `json:"ref"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !docker.ValidImageRef(req.Ref) {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid image")
		return
	}
	gone, err := s.Docker.RemoveImage(r.Context(), req.Ref)
	s.audit(r, userFrom(r.Context()).ID, "image.remove", req.Ref, err == nil, errText(err))
	if err != nil {
		var apiErr *docker.APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict {
			writeError(w, http.StatusConflict, "in_use", "a container still uses this image; remove the container first")
			return
		}
		s.editError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": gone})
}

// POST /api/v1/docker/images/prune {all}
func (s *Server) imagePrune(w http.ResponseWriter, r *http.Request) {
	var req struct {
		All bool `json:"all"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := s.Docker.PruneImages(r.Context(), req.All)
	detail := errText(err)
	if res != nil {
		detail = strconv.Itoa(len(res.Removed)) + " removed, " + strconv.FormatInt(res.Reclaimed, 10) + " bytes"
	}
	s.audit(r, userFrom(r.Context()).ID, "image.prune", map[bool]string{true: "unused", false: "dangling"}[req.All], err == nil, detail)
	if err != nil {
		s.editError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ---------- volumes ----------

type volumeView struct {
	docker.VolumeInfo
	Size       int64    `json:"size"` // -1 = unknown
	Containers []string `json:"containers"`
	FilesID    string   `json:"files_id,omitempty"` // opens in Files (App data)
	System     bool     `json:"system"`
}

// GET /api/v1/docker/volumes/details[?usage=1]
func (s *Server) volumeDetails(w http.ResponseWriter, r *http.Request) {
	vols, err := s.Docker.VolumeDetails(r.Context())
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	users := map[string][]string{}
	system := map[string]bool{}
	if list, err := s.Docker.ListContainers(r.Context(), true); err == nil {
		for _, c := range list {
			for _, v := range c.Volumes {
				users[v] = append(users[v], c.Name)
				system[v] = system[v] || c.System
			}
		}
	}
	var sizes map[string]docker.VolumeSize
	if r.URL.Query().Get("usage") == "1" {
		if u, err := s.usage(r.Context(), false); err == nil {
			sizes = u.Volumes
		}
	}
	files := map[string]string{}
	if s.appData != nil {
		if apps, err := s.appData.Apps(r.Context()); err == nil {
			for _, a := range apps {
				for _, f := range a.Folders {
					if f.Type == "volume" && f.Volume != "" && f.Blocked == "" {
						files[f.Volume] = f.ID
					}
				}
			}
		}
	}
	out := make([]volumeView, 0, len(vols))
	for _, v := range vols {
		vv := volumeView{VolumeInfo: v, Size: -1, Containers: users[v.Name], FilesID: files[v.Name], System: system[v.Name]}
		if vv.Containers == nil {
			vv.Containers = []string{}
		}
		sort.Strings(vv.Containers)
		if sz, ok := sizes[v.Name]; ok {
			vv.Size = sz.Size
		}
		out = append(out, vv)
	}
	writeJSON(w, http.StatusOK, map[string]any{"volumes": out})
}

// DELETE /api/v1/docker/volumes/{name}: only a volume no container uses.
func (s *Server) volumeRemove(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validContainerRef(name) {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid volume name")
		return
	}
	err := s.Docker.DeleteVolume(r.Context(), name)
	s.audit(r, userFrom(r.Context()).ID, "volume.remove", name, err == nil, errText(err))
	if err != nil {
		var apiErr *docker.APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict {
			writeError(w, http.StatusConflict, "in_use", "a container still uses this volume")
			return
		}
		s.editError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/docker/volumes/prune {all}: volumes no container uses
// (anonymous ones only, unless all — then named ones too).
func (s *Server) volumePrune(w http.ResponseWriter, r *http.Request) {
	var req struct {
		All bool `json:"all"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := s.Docker.PruneVolumes(r.Context(), req.All)
	detail := errText(err)
	if res != nil {
		detail = strings.Join(res.Removed, ", ")
	}
	s.audit(r, userFrom(r.Context()).ID, "volume.prune", map[bool]string{true: "all unused", false: "anonymous"}[req.All], err == nil, detail)
	if err != nil {
		s.editError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ---------- networks ----------

func (s *Server) networkMember(w http.ResponseWriter, r *http.Request) (network string, d *docker.ContainerDetail, ok bool) {
	network = r.PathValue("name")
	var req struct {
		Container string `json:"container"`
	}
	if !decodeJSON(w, r, &req) {
		return "", nil, false
	}
	if !validContainerRef(network) || !validContainerRef(req.Container) || network == "host" || network == "none" {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid network or container")
		return "", nil, false
	}
	d, err := s.Docker.InspectContainer(r.Context(), req.Container)
	if err != nil {
		s.dockerError(w, r, err)
		return "", nil, false
	}
	if d.System {
		writeError(w, http.StatusForbidden, "protected", "NoCapOS's own containers keep their networks")
		return "", nil, false
	}
	if d.NetworkMode == "host" || d.NetworkMode == "none" || strings.HasPrefix(d.NetworkMode, "container:") {
		writeError(w, http.StatusConflict, "network_mode", "this container uses "+d.NetworkMode+" networking, so it can't join networks")
		return "", nil, false
	}
	return network, d, true
}

func (s *Server) networkConnect(w http.ResponseWriter, r *http.Request) {
	network, d, ok := s.networkMember(w, r)
	if !ok {
		return
	}
	err := s.Docker.ConnectNetwork(r.Context(), docker.NetLink{Name: network}, d.ID)
	s.audit(r, userFrom(r.Context()).ID, "network.connect", network+" ← "+d.Name, err == nil, errText(err))
	if err != nil {
		s.editError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) networkDisconnect(w http.ResponseWriter, r *http.Request) {
	network, d, ok := s.networkMember(w, r)
	if !ok {
		return
	}
	if len(d.Networks) <= 1 {
		writeError(w, http.StatusConflict, "last_network", "that's the container's only network; connect it to another one first")
		return
	}
	err := s.Docker.DisconnectNetwork(r.Context(), network, d.ID)
	s.audit(r, userFrom(r.Context()).ID, "network.disconnect", network+" → "+d.Name, err == nil, errText(err))
	if err != nil {
		s.editError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- USB and serial devices ----------

// hostRoot is where devices are looked for ("/"; tests point it elsewhere).
var hostRoot = "/"

// GET /api/v1/docker/devices: the host's USB and serial devices, with the
// containers that have each one.
func (s *Server) hostDevices(w http.ResponseWriter, r *http.Request) {
	devs := usbdev.List(hostRoot)
	byPath := map[string]int{}
	for i, d := range devs {
		byPath[d.Path] = i
		if d.ByID != "" {
			byPath[d.ByID] = i
		}
	}
	if len(devs) > 0 {
		if list, err := s.Docker.ListContainers(r.Context(), true); err == nil {
			var mu sync.Mutex
			sem := make(chan struct{}, inspectConcurrency)
			var wg sync.WaitGroup
			for _, c := range list {
				wg.Add(1)
				go func() {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					det, err := s.Docker.InspectContainer(r.Context(), c.ID)
					if err != nil {
						return
					}
					mu.Lock()
					defer mu.Unlock()
					for _, dv := range det.Devices {
						host, _, _ := strings.Cut(dv, ":")
						if i, ok := byPath[host]; ok {
							devs[i].UsedBy = append(devs[i].UsedBy, c.Name)
						}
					}
				}()
			}
			wg.Wait()
		}
	}
	type view struct {
		usbdev.Device
		Flag string `json:"device_flag"`
	}
	out := make([]view, 0, len(devs))
	for _, d := range devs {
		sort.Strings(d.UsedBy)
		out = append(out, view{Device: d, Flag: d.DeviceFlag()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}
