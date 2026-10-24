package api

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"alfaos/alfad/internal/appstore"
	"alfaos/alfad/internal/docker"
	"alfaos/alfad/internal/webaddr"
)

// Container IDs (hex) and names ([a-zA-Z0-9][a-zA-Z0-9_.-]+).
var containerRefRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

func validContainerRef(ref string) bool { return containerRefRe.MatchString(ref) }

func (s *Server) listContainers(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("all") != "false"
	list, err := s.Docker.ListContainers(r.Context(), all)
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	s.enrichContainers(r.Context(), list)
	writeJSON(w, http.StatusOK, list)
}

// inspectConcurrency bounds the parallel inspect calls behind one list.
const inspectConcurrency = 8

// enrichContainers adds what the list call leaves out (uptime, restarts,
// health) and where each container's web UI is.
func (s *Server) enrichContainers(ctx context.Context, list []docker.Container) {
	drivers, _ := s.Docker.NetworkDrivers(ctx)
	book, _ := s.webBook.All(ctx)
	sem := make(chan struct{}, inspectConcurrency)
	var wg sync.WaitGroup
	for i := range list {
		c := &list[i]
		for j := range c.Networks {
			c.Networks[j].Driver = drivers[c.Networks[j].Name]
		}
		c.WebAddress = book[webaddr.ContainerKey(c.Name)]
		c.WebURL = s.containerWebURL(c.Name, c.Labels, c.Networks, tcpPorts(c.Ports), book)
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			d, err := s.Docker.InspectContainer(ctx, c.ID)
			if err != nil {
				return
			}
			c.RestartCount, c.Health = d.RestartCount, d.Health
			if d.Running && !d.StartedAt.IsZero() {
				t := d.StartedAt
				c.StartedAt = &t
			}
		}()
	}
	wg.Wait()
}

func tcpPorts(ports []docker.Port) []int {
	var out []int
	for _, p := range ports {
		if p.Protocol == "" || p.Protocol == "tcp" {
			out = append(out, int(p.Private))
		}
	}
	return out
}

// containerWebURL is where "Open" goes for a container when that isn't this
// server's address + a published port: the admin's own address (for the
// container, or for the App Store app it serves), or its macvlan/ipvlan IP.
func (s *Server) containerWebURL(name string, labels map[string]string, nets []docker.NetworkAttachment, ports []int, book map[string]string) string {
	override := book[webaddr.ContainerKey(name)]
	port, path := webaddr.PickPort(ports), "/"
	if app := labels[appstore.LabelApp]; app != "" && s.AppStore != nil {
		if a, err := s.AppStore.App(app); err == nil && a.Web != nil && a.Web.Service == labels[appstore.LabelService] {
			port, path = a.Web.Port, a.Web.Path
			if override == "" {
				override = book[webaddr.AppKey(app)]
			}
		}
	}
	return webaddr.Resolve(override, attachments(nets), port, path)
}

func attachments(nets []docker.NetworkAttachment) []webaddr.Attachment {
	out := make([]webaddr.Attachment, 0, len(nets))
	for _, n := range nets {
		out = append(out, webaddr.Attachment{Driver: n.Driver, IP: n.IP})
	}
	return out
}

func (s *Server) inspectContainer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validContainerRef(id) {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid container id")
		return
	}
	d, err := s.Docker.InspectContainer(r.Context(), id)
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	drivers, _ := s.Docker.NetworkDrivers(r.Context())
	for i := range d.Networks {
		d.Networks[i].Driver = drivers[d.Networks[i].Name]
	}
	book, _ := s.webBook.All(r.Context())
	var ports []int
	for _, p := range d.ExposedPorts {
		if num, proto, _ := strings.Cut(p, "/"); proto == "tcp" || proto == "" {
			if n := atoiSafe(num); n > 0 {
				ports = append(ports, n)
			}
		}
	}
	d.WebAddress = book[webaddr.ContainerKey(d.Name)]
	d.WebURL = s.containerWebURL(d.Name, d.Labels, d.Networks, ports, book)
	writeJSON(w, http.StatusOK, d)
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' || n > 1<<20 {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// containerStats samples usage of running containers in one go:
// ?ids=a,b,c (default: every running container).
func (s *Server) containerStats(w http.ResponseWriter, r *http.Request) {
	var ids []string
	if q := r.URL.Query().Get("ids"); q != "" {
		for _, id := range strings.Split(q, ",") {
			if validContainerRef(id) && len(ids) < 200 {
				ids = append(ids, id)
			}
		}
	} else {
		list, err := s.Docker.ListContainers(r.Context(), false)
		if err != nil {
			s.dockerError(w, r, err)
			return
		}
		for _, c := range list {
			if c.State == "running" {
				ids = append(ids, c.ID)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"stats": s.ctStats.Sample(r.Context(), ids)})
}

const stopTimeout = 10 * time.Second

// What each action may do to NoCapOS's own containers: only start them.
var containerActions = map[string]bool{"start": true, "stop": true, "restart": true, "pause": true, "unpause": true, "kill": true, "update": true}

func (s *Server) containerAction(w http.ResponseWriter, r *http.Request) {
	id, action := r.PathValue("id"), r.PathValue("action")
	if !validContainerRef(id) {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid container id")
		return
	}
	if !containerActions[action] {
		writeError(w, http.StatusNotFound, "not_found", "unknown action")
		return
	}
	u := userFrom(r.Context())

	d, err := s.Docker.InspectContainer(r.Context(), id)
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	// Stopping alfad/Traefik from the UI would lock the user out of the OS.
	if d.System && action != "start" && action != "unpause" {
		s.audit(r, u.ID, "container."+action, d.Name, false, "system container is protected")
		writeError(w, http.StatusForbidden, "protected", "system containers cannot be stopped from the API")
		return
	}
	if action == "update" {
		s.containerUpdate(w, r, d)
		return
	}

	s.crashes.Expect(d.ID, d.Name) // stopped on purpose: not a crash
	switch action {
	case "start":
		err = s.Docker.StartContainer(r.Context(), d.ID)
	case "stop":
		err = s.Docker.StopContainer(r.Context(), d.ID, stopTimeout)
	case "restart":
		err = s.Docker.RestartContainer(r.Context(), d.ID, stopTimeout)
	case "pause":
		err = s.Docker.PauseContainer(r.Context(), d.ID)
	case "unpause":
		err = s.Docker.UnpauseContainer(r.Context(), d.ID)
	case "kill":
		err = s.Docker.KillContainer(r.Context(), d.ID)
	}
	s.audit(r, u.ID, "container."+action, d.Name, err == nil, errText(err))
	if err != nil {
		s.editError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// containerUpdate downloads the container's image again and, if a newer one
// came down, recreates the container with it (same settings, volumes kept).
func (s *Server) containerUpdate(w http.ResponseWriter, r *http.Request, d *docker.ContainerDetail) {
	uid := userFrom(r.Context()).ID
	if stack := d.Labels[composeProject]; stack != "" {
		writeError(w, http.StatusConflict, "stack", "this container belongs to the stack “"+stack+"”; update the stack instead")
		return
	}
	if strings.HasPrefix(d.Image, "sha256:") || d.Image == "" {
		writeError(w, http.StatusConflict, "no_tag", "this container was made from an image id, not a name, so there's nothing to download")
		return
	}
	if err := s.pullFor(r.Context())(d.Image); err != nil {
		s.audit(r, uid, "container.update", d.Name, false, err.Error())
		s.editError(w, r, err)
		return
	}
	latest, err := s.Docker.ImageID(r.Context(), d.Image)
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	if latest == d.ImageID {
		s.audit(r, uid, "container.update", d.Name, true, "already up to date")
		writeJSON(w, http.StatusOK, map[string]any{"id": d.ID, "updated": false})
		return
	}
	spec, err := s.Docker.GetSpec(r.Context(), d.ID)
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	s.crashes.Expect(d.ID, d.Name)
	newID, err := s.Docker.Recreate(r.Context(), d.ID, spec, nil)
	s.audit(r, uid, "container.update", d.Name, err == nil || newID != "", errText(err))
	if err != nil && newID == "" {
		s.editError(w, r, err)
		return
	}
	resp := map[string]any{"id": newID, "updated": true}
	if err != nil {
		resp["warning"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) listImages(w http.ResponseWriter, r *http.Request) {
	imgs, err := s.Docker.ListImages(r.Context())
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, imgs)
}

// ---------- web addresses ----------

type webAddressReq struct {
	URL string `json:"url"`
}

func (s *Server) saveWebAddress(w http.ResponseWriter, r *http.Request, key, target string) {
	var req webAddressReq
	if !decodeJSON(w, r, &req) {
		return
	}
	addr, err := webaddr.Normalize(req.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	err = s.webBook.Set(r.Context(), key, addr)
	s.audit(r, userFrom(r.Context()).ID, "webaddress.set", target, err == nil, addr)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": addr})
}

// containerWebAddress saves the address "Open" uses for a container (kept by
// name, so it survives edits that recreate the container).
func (s *Server) containerWebAddress(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validContainerRef(id) {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid container id")
		return
	}
	d, err := s.Docker.InspectContainer(r.Context(), id)
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	s.saveWebAddress(w, r, webaddr.ContainerKey(d.Name), d.Name)
}

// appWebAddress saves the address "Open" uses for an App Store app.
func (s *Server) appWebAddress(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.AppStore == nil {
		writeError(w, http.StatusNotFound, "not_found", "no such app")
		return
	}
	if _, err := s.AppStore.App(id); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "no such app")
		return
	}
	s.saveWebAddress(w, r, webaddr.AppKey(id), id)
}

// fillAppWebURLs sets each installed app's web address for "Open": the
// admin's own, or the web container's macvlan/ipvlan IP.
func (s *Server) fillAppWebURLs(ctx context.Context, status map[string]*appstore.Installed) {
	book, _ := s.webBook.All(ctx)
	var (
		byID    map[string]docker.Container
		drivers map[string]string
		loaded  bool
	)
	for _, a := range s.AppStore.Catalog() {
		in := status[a.ID]
		if in == nil || a.Web == nil {
			continue
		}
		in.WebAddress = book[webaddr.AppKey(a.ID)]
		if in.WebAddress != "" {
			in.WebURL = in.WebAddress
			continue
		}
		if !loaded {
			loaded = true
			drivers, _ = s.Docker.NetworkDrivers(ctx)
			byID = map[string]docker.Container{}
			if list, err := s.Docker.ListContainers(ctx, true); err == nil {
				for _, c := range list {
					byID[c.ID] = c
				}
			}
		}
		for _, cs := range in.Containers {
			c, ok := byID[cs.ID]
			if !ok || cs.Service != a.Web.Service {
				continue
			}
			for j := range c.Networks {
				c.Networks[j].Driver = drivers[c.Networks[j].Name]
			}
			in.WebURL = s.containerWebURL(c.Name, c.Labels, c.Networks, nil, book)
		}
	}
}
