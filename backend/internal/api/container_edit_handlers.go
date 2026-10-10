package api

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"alfaos/alfad/internal/appstore"
	"alfaos/alfad/internal/docker"
	"alfaos/alfad/internal/files"
)

// Editing containers like Portainer, in NoCapOS: change a container's image,
// ports, storage, networks (incl. macvlan/ipvlan), environment and limits;
// add new containers; manage networks. Admin only; changes are audited.

const composeProject = "com.docker.compose.project"

// editError shows Docker's own message for request errors (a port in use,
// an address outside the network) instead of a generic failure.
func (s *Server) editError(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *docker.APIError
	if errors.As(err, &apiErr) && apiErr.Status != http.StatusNotFound && !errors.Is(err, docker.ErrUnavailable) {
		writeError(w, http.StatusBadRequest, "docker_error", apiErr.Message)
		return
	}
	if errors.As(err, &apiErr) || errors.Is(err, docker.ErrUnavailable) {
		s.dockerError(w, r, err)
		return
	}
	writeError(w, http.StatusBadRequest, "bad_request", err.Error())
}

func splitRef(ref string) (string, string) {
	slash := strings.LastIndex(ref, "/")
	if i := strings.LastIndex(ref, ":"); i > slash {
		return ref[:i], ref[i+1:]
	}
	return ref, "latest"
}

// pullFor downloads an image during an edit (bounded, so a slow registry can't hang forever).
func (s *Server) pullFor(ctx context.Context) func(image string) error {
	return func(image string) error {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Minute)
		defer cancel()
		name, tag := splitRef(image)
		return s.Docker.PullImage(ctx, name, tag, nil)
	}
}

type specInfo struct {
	Spec    docker.Spec `json:"spec"`
	App     string      `json:"app,omitempty"`     // App Store app id
	Service string      `json:"service,omitempty"` // its service
	Stack   string      `json:"stack,omitempty"`   // compose project
	System  bool        `json:"system"`
}

func (s *Server) containerSpec(w http.ResponseWriter, r *http.Request) {
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
	spec, err := s.Docker.GetSpec(r.Context(), d.ID)
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, specInfo{Spec: spec, App: d.Labels[appstore.LabelApp], Service: d.Labels[appstore.LabelService], Stack: d.Labels[composeProject], System: d.System})
}

// containerEdit applies changes by recreating the container (rolled back if anything fails).
func (s *Server) containerEdit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validContainerRef(id) {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid container id")
		return
	}
	var spec docker.Spec
	if !decodeJSON(w, r, &spec) {
		return
	}
	if err := spec.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	d, err := s.Docker.InspectContainer(r.Context(), id)
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	uid := userFrom(r.Context()).ID
	if d.System {
		writeError(w, http.StatusForbidden, "protected", "NoCapOS's own containers can't be edited")
		return
	}
	if stack := d.Labels[composeProject]; stack != "" {
		writeError(w, http.StatusConflict, "stack", "this container belongs to the stack “"+stack+"”; edit its compose file instead")
		return
	}
	app, svc := d.Labels[appstore.LabelApp], d.Labels[appstore.LabelService]
	if app != "" {
		spec.Name = d.Name // App Store containers keep their names
	}
	s.crashes.Expect(d.ID, d.Name) // the old container is stopped on purpose
	newID, err := s.Docker.Recreate(r.Context(), d.ID, spec, s.pullFor(r.Context()))
	s.audit(r, uid, "container.edit", d.Name, err == nil || newID != "", errText(err))
	if err != nil && newID == "" {
		s.editError(w, r, err)
		return
	}
	if app != "" && svc != "" {
		if e := s.AppStore.SaveOverride(context.WithoutCancel(r.Context()), app, svc, spec); e != nil {
			s.Log.Warn("container edit: save app override", "app", app, "err", e)
		}
	}
	resp := map[string]any{"id": newID}
	if err != nil {
		resp["warning"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// containerCreate adds a new container from a spec and starts it.
func (s *Server) containerCreate(w http.ResponseWriter, r *http.Request) {
	var spec docker.Spec
	if !decodeJSON(w, r, &spec) {
		return
	}
	if err := spec.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ctx := r.Context()
	uid := userFrom(ctx).ID
	if ok, err := s.Docker.ImageExists(ctx, spec.Image); err != nil {
		s.dockerError(w, r, err)
		return
	} else if !ok {
		if err := s.pullFor(ctx)(spec.Image); err != nil {
			s.audit(r, uid, "container.create", spec.Name, false, err.Error())
			s.editError(w, r, err)
			return
		}
	}
	id, err := s.Docker.CreateContainer(ctx, spec.Name, spec.CreateConfig(map[string]string{"nocapos.custom": "true"}))
	if err == nil {
		for _, n := range spec.ExtraNetworks() {
			if err = s.Docker.ConnectNetwork(ctx, n, id); err != nil {
				break
			}
		}
		if err == nil {
			err = s.Docker.StartContainer(ctx, id)
		}
		if err != nil {
			_ = s.Docker.RemoveContainer(context.WithoutCancel(ctx), id)
		}
	}
	s.audit(r, uid, "container.create", spec.Name+" ("+spec.Image+")", err == nil, errText(err))
	if err != nil {
		s.editError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// containerRemove deletes a container (not App Store apps or NoCapOS's own).
func (s *Server) containerRemove(w http.ResponseWriter, r *http.Request) {
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
	switch {
	case d.System:
		writeError(w, http.StatusForbidden, "protected", "NoCapOS's own containers can't be removed")
		return
	case d.Labels[appstore.LabelApp] != "":
		writeError(w, http.StatusConflict, "app", "this is an App Store app; uninstall it from the App Store")
		return
	case d.Labels[composeProject] != "":
		writeError(w, http.StatusConflict, "stack", "this container belongs to a stack; remove the stack instead")
		return
	}
	s.crashes.Expect(d.ID, d.Name)
	err = s.Docker.RemoveContainer(r.Context(), d.ID)
	s.audit(r, userFrom(r.Context()).ID, "container.remove", d.Name, err == nil, errText(err))
	if err != nil {
		s.editError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- networks ----------

func (s *Server) dockerNetworks(w http.ResponseWriter, r *http.Request) {
	nets, err := s.Docker.ListNetworks(r.Context())
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"networks": nets, "interfaces": hostInterfaces()})
}

func (s *Server) dockerNetworkCreate(w http.ResponseWriter, r *http.Request) {
	var n docker.NewNetwork
	if !decodeJSON(w, r, &n) {
		return
	}
	if err := n.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	err := s.Docker.CreateNetworkFrom(r.Context(), n, map[string]string{"nocapos.custom": "true"})
	s.audit(r, userFrom(r.Context()).ID, "network.create", n.Driver+":"+n.Name, err == nil, errText(err))
	if err != nil {
		s.editError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) dockerNetworkDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validContainerRef(name) || name == "bridge" || name == "host" || name == "none" || name == "podman" {
		writeError(w, http.StatusBadRequest, "bad_request", "that network can't be removed")
		return
	}
	nets, err := s.Docker.ListNetworks(r.Context())
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	for _, n := range nets {
		if n.Name == name && len(n.Containers) > 0 {
			writeError(w, http.StatusConflict, "in_use", "containers still use this network: "+strings.Join(n.Containers, ", "))
			return
		}
	}
	err = s.Docker.RemoveNetwork(r.Context(), name)
	s.audit(r, userFrom(r.Context()).ID, "network.delete", name, err == nil, errText(err))
	if err != nil {
		s.editError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) dockerVolumes(w http.ResponseWriter, r *http.Request) {
	vols, err := s.Docker.ListVolumes(r.Context())
	if err != nil {
		s.dockerError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"volumes": vols})
}

// HostInterface is a network card macvlan/ipvlan networks can go out on.
type HostInterface struct {
	Name    string `json:"name"`
	Address string `json:"address,omitempty"` // e.g. 192.168.1.20
	Subnet  string `json:"subnet,omitempty"`  // e.g. 192.168.1.0/24
	Gateway string `json:"gateway,omitempty"` // the default gateway, when it's on this card
}

var virtualIface = []string{"lo", "docker", "br-", "veth", "virbr", "cni", "podman", "flannel", "cali", "vxlan", "tun", "tap", "wg", "tailscale", "zt"}

// hostInterfaces lists the physical-looking network cards with their IPv4 subnet.
func hostInterfaces() []HostInterface {
	gw := defaultGateways()
	ifs, err := net.Interfaces()
	if err != nil {
		return []HostInterface{}
	}
	out := []HostInterface{}
	for _, it := range ifs {
		if it.Flags&net.FlagLoopback != 0 || it.Flags&net.FlagUp == 0 {
			continue
		}
		skip := false
		for _, p := range virtualIface {
			skip = skip || strings.HasPrefix(it.Name, p)
		}
		if skip {
			continue
		}
		hi := HostInterface{Name: it.Name, Gateway: gw[it.Name]}
		addrs, _ := it.Addrs()
		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil && p.Addr().Is4() {
				hi.Address, hi.Subnet = p.Addr().String(), p.Masked().String()
				break
			}
		}
		out = append(out, hi)
	}
	// The card with the default route first, then by name.
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Gateway != "") != (out[j].Gateway != "") {
			return out[i].Gateway != ""
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// defaultGateways reads the default route per interface from /proc/net/route (Linux).
func defaultGateways() map[string]string {
	out := map[string]string{}
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 || fields[1] != "00000000" {
			continue
		}
		b, err := hex.DecodeString(fields[2])
		if err != nil || len(b) != 4 {
			continue
		}
		out[fields[0]] = netip.AddrFrom4([4]byte{b[3], b[2], b[1], b[0]}).String()
	}
	return out
}

// dockerHostPath turns a folder picked in a storage location into its path on
// the server (for binding it into a container).
func (s *Server) dockerHostPath(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	root, err := s.Files.Root(q.Get("root"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "no such storage location")
		return
	}
	rel, err := files.Clean(q.Get("path"))
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": filepath.Join(root.Path, filepath.FromSlash(rel))})
}
