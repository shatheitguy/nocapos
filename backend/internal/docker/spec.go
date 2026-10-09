package docker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Spec is the part of a container people edit (Portainer-style): image,
// ports, storage, networks, environment, restart policy and resources.
// Everything else about the container (entrypoint, user, health check,
// capabilities, log settings…) is kept as it was when it is recreated.
type Spec struct {
	Name        string      `json:"name"`
	Image       string      `json:"image"`
	Cmd         []string    `json:"cmd,omitempty"`
	Env         []EnvVar    `json:"env"`
	Ports       []PortMap   `json:"ports"`
	Mounts      []SpecMount `json:"mounts"`
	NetworkMode string      `json:"network_mode"` // bridge | host | none | a network's name
	Networks    []NetLink   `json:"networks"`     // user networks (the first one is NetworkMode)
	Restart     string      `json:"restart"`      // no | always | unless-stopped | on-failure
	MemoryMB    int64       `json:"memory_mb"`    // 0 = no limit
	CPUs        float64     `json:"cpus"`         // 0 = no limit
	Privileged  bool        `json:"privileged"`
	Devices     []string    `json:"devices"` // "/dev/dri" or "/dev/ttyUSB0:/dev/ttyACM0"
	Hostname    string      `json:"hostname,omitempty"`
}

type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type PortMap struct {
	HostIP    string `json:"host_ip,omitempty"`
	Host      int    `json:"host"`
	Container int    `json:"container"`
	Protocol  string `json:"protocol"` // tcp | udp
}

// SpecMount is a named volume (Type "volume", Source = volume name) or a folder
// on the server (Type "bind", Source = host path).
type SpecMount struct {
	Type     string `json:"type"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}

// NetLink attaches the container to a network, optionally at a fixed address.
type NetLink struct {
	Name    string   `json:"name"`
	IPv4    string   `json:"ipv4,omitempty"`
	Aliases []string `json:"aliases,omitempty"`
}

// rawContainer is an inspect result kept as maps, so recreating a container
// can keep the settings Spec doesn't cover.
type rawContainer struct {
	ID              string         `json:"Id"`
	Name            string         `json:"Name"`
	Config          map[string]any `json:"Config"`
	HostConfig      map[string]any `json:"HostConfig"`
	Mounts          []rawMount     `json:"Mounts"`
	State           struct{ Running bool }
	NetworkSettings struct {
		Networks map[string]rawEndpoint `json:"Networks"`
	} `json:"NetworkSettings"`
}

type rawMount struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	RW          bool   `json:"RW"`
}

type rawEndpoint struct {
	IPAMConfig *struct {
		IPv4Address string `json:"IPv4Address"`
	} `json:"IPAMConfig"`
	Aliases []string `json:"Aliases"`
}

func (c *Client) inspectRaw(ctx context.Context, ref string) (*rawContainer, error) {
	var raw rawContainer
	if err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(ref)+"/json", nil, &raw); err != nil {
		return nil, err
	}
	return &raw, nil
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func strs(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func num(m map[string]any, k string) float64 {
	f, _ := m[k].(float64)
	return f
}

// specFrom reads the editable settings out of an inspect result.
func specFrom(raw *rawContainer) Spec {
	cfg, hc := raw.Config, raw.HostConfig
	if cfg == nil {
		cfg = map[string]any{}
	}
	if hc == nil {
		hc = map[string]any{}
	}
	s := Spec{
		Name:        strings.TrimPrefix(raw.Name, "/"),
		Image:       str(cfg, "Image"),
		Cmd:         strs(cfg["Cmd"]),
		NetworkMode: str(hc, "NetworkMode"),
		Privileged:  hc["Privileged"] == true,
		Env:         []EnvVar{},
		Ports:       []PortMap{},
		Mounts:      []SpecMount{},
		Networks:    []NetLink{},
		Devices:     []string{},
	}
	if h := str(cfg, "Hostname"); h != "" && !strings.HasPrefix(raw.ID, h) {
		s.Hostname = h // only a hostname someone chose, not the generated one
	}
	for _, kv := range strs(cfg["Env"]) {
		k, v, _ := strings.Cut(kv, "=")
		s.Env = append(s.Env, EnvVar{Key: k, Value: v})
	}
	if pb, ok := hc["PortBindings"].(map[string]any); ok {
		keys := make([]string, 0, len(pb))
		for k := range pb {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			portStr, proto, _ := strings.Cut(k, "/")
			cport, _ := strconv.Atoi(portStr)
			if proto == "" {
				proto = "tcp"
			}
			binds, _ := pb[k].([]any)
			for _, b := range binds {
				bm, _ := b.(map[string]any)
				hp, _ := strconv.Atoi(str(bm, "HostPort"))
				s.Ports = append(s.Ports, PortMap{HostIP: str(bm, "HostIp"), Host: hp, Container: cport, Protocol: proto})
			}
		}
	}
	for _, m := range raw.Mounts {
		switch m.Type {
		case "volume":
			s.Mounts = append(s.Mounts, SpecMount{Type: "volume", Source: m.Name, Target: m.Destination, ReadOnly: !m.RW})
		case "bind":
			s.Mounts = append(s.Mounts, SpecMount{Type: "bind", Source: m.Source, Target: m.Destination, ReadOnly: !m.RW})
		}
	}
	if rp, ok := hc["RestartPolicy"].(map[string]any); ok {
		s.Restart = str(rp, "Name")
	}
	if s.Restart == "" {
		s.Restart = "no"
	}
	s.MemoryMB = int64(num(hc, "Memory")) / (1 << 20)
	s.CPUs = num(hc, "NanoCpus") / 1e9
	if devs, ok := hc["Devices"].([]any); ok {
		for _, d := range devs {
			dm, _ := d.(map[string]any)
			host, ctr := str(dm, "PathOnHost"), str(dm, "PathInContainer")
			if ctr == "" || ctr == host {
				s.Devices = append(s.Devices, host)
			} else {
				s.Devices = append(s.Devices, host+":"+ctr)
			}
		}
	}
	// Networks: the primary (NetworkMode) first, then the others, sorted.
	names := make([]string, 0, len(raw.NetworkSettings.Networks))
	for n := range raw.NetworkSettings.Networks {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if (names[i] == s.NetworkMode) != (names[j] == s.NetworkMode) {
			return names[i] == s.NetworkMode
		}
		return names[i] < names[j]
	})
	labels, _ := cfg["Labels"].(map[string]any)
	for _, n := range names {
		if n == "bridge" || n == "host" || n == "none" || n == "podman" {
			continue
		}
		ep := raw.NetworkSettings.Networks[n]
		link := NetLink{Name: n}
		if ep.IPAMConfig != nil {
			link.IPv4 = ep.IPAMConfig.IPv4Address
		}
		// Engines that don't report a fixed address (Podman) get it from our label.
		if ip, _ := labels[labelIPv4+n].(string); link.IPv4 == "" && ip != "" {
			link.IPv4 = ip
		}
		for _, a := range ep.Aliases {
			if !strings.HasPrefix(raw.ID, a) && a != s.Name { // drop the automatic ones
				link.Aliases = append(link.Aliases, a)
			}
		}
		s.Networks = append(s.Networks, link)
	}
	if s.NetworkMode == "default" || s.NetworkMode == "" {
		s.NetworkMode = "bridge"
	}
	// Some engines say "bridge" for a container that is only on user networks.
	if _, onBridge := raw.NetworkSettings.Networks["bridge"]; s.NetworkMode == "bridge" && !onBridge && len(s.Networks) > 0 {
		if _, onPodman := raw.NetworkSettings.Networks["podman"]; !onPodman {
			s.NetworkMode = s.Networks[0].Name
		}
	}
	return s
}

// labelIPv4 records a fixed address per network ("nocapos.ipv4.<network>").
const labelIPv4 = "nocapos.ipv4."

// withIPLabels returns labels with the fixed-address labels set for s.
func (s Spec) withIPLabels(labels map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range labels {
		if !strings.HasPrefix(k, labelIPv4) {
			out[k] = v
		}
	}
	for _, n := range s.Networks {
		if n.IPv4 != "" {
			out[labelIPv4+n.Name] = n.IPv4
		}
	}
	return out
}

// GetSpec returns a container's editable settings.
func (c *Client) GetSpec(ctx context.Context, ref string) (Spec, error) {
	raw, err := c.inspectRaw(ctx, ref)
	if err != nil {
		return Spec{}, err
	}
	return specFrom(raw), nil
}

var (
	nameRe   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
	envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)
)

// Validate checks a Spec and tidies it (defaults, trimmed values).
func (s *Spec) Validate() error {
	s.Name, s.Image = strings.TrimSpace(s.Name), strings.TrimSpace(s.Image)
	if !nameRe.MatchString(s.Name) {
		return errors.New("the name can use letters, numbers, . _ and - (and must start with a letter or number)")
	}
	if s.Image == "" || strings.ContainsAny(s.Image, " \t\n") {
		return errors.New("enter an image, e.g. nginx:latest")
	}
	seenEnv := map[string]bool{}
	for i, e := range s.Env {
		e.Key = strings.TrimSpace(e.Key)
		if !envKeyRe.MatchString(e.Key) {
			return fmt.Errorf("environment variable name %q isn't valid", e.Key)
		}
		if seenEnv[e.Key] {
			return fmt.Errorf("environment variable %s is set twice", e.Key)
		}
		seenEnv[e.Key] = true
		s.Env[i] = e
	}
	seenPort := map[string]bool{}
	for i, p := range s.Ports {
		if p.Protocol == "" {
			p.Protocol = "tcp"
		}
		if p.Protocol != "tcp" && p.Protocol != "udp" {
			return errors.New("ports are tcp or udp")
		}
		if p.Container < 1 || p.Container > 65535 || p.Host < 0 || p.Host > 65535 {
			return errors.New("ports are numbers from 1 to 65535")
		}
		if p.HostIP != "" {
			if _, err := netip.ParseAddr(p.HostIP); err != nil {
				return fmt.Errorf("%q isn't an IP address", p.HostIP)
			}
		}
		if p.Host != 0 {
			k := fmt.Sprintf("%s:%d/%s", p.HostIP, p.Host, p.Protocol)
			if seenPort[k] {
				return fmt.Errorf("host port %d/%s is used twice", p.Host, p.Protocol)
			}
			seenPort[k] = true
		}
		s.Ports[i] = p
	}
	for i, m := range s.Mounts {
		m.Source, m.Target = strings.TrimSpace(m.Source), strings.TrimSpace(m.Target)
		if !strings.HasPrefix(m.Target, "/") {
			return fmt.Errorf("the folder inside the container (%q) must start with /", m.Target)
		}
		switch m.Type {
		case "bind":
			if !strings.HasPrefix(m.Source, "/") && !(len(m.Source) > 2 && m.Source[1] == ':') {
				return fmt.Errorf("the folder on the server (%q) must be a full path", m.Source)
			}
		case "volume":
			if !nameRe.MatchString(m.Source) {
				return fmt.Errorf("volume name %q isn't valid", m.Source)
			}
		default:
			return errors.New("storage is a volume or a folder on the server")
		}
		s.Mounts[i] = m
	}
	switch s.Restart {
	case "", "no", "always", "unless-stopped", "on-failure":
		if s.Restart == "" {
			s.Restart = "no"
		}
	default:
		return errors.New("restart is no, always, unless-stopped or on-failure")
	}
	if s.MemoryMB < 0 || s.CPUs < 0 {
		return errors.New("limits can't be negative")
	}
	if s.MemoryMB != 0 && s.MemoryMB < 6 {
		return errors.New("a memory limit must be at least 6 MB")
	}
	for _, d := range s.Devices {
		if !strings.HasPrefix(d, "/dev/") {
			return fmt.Errorf("device %q must be under /dev", d)
		}
	}
	if s.NetworkMode == "" {
		s.NetworkMode = "bridge"
	}
	if s.NetworkMode == "host" || s.NetworkMode == "none" {
		if len(s.Ports) > 0 {
			return errors.New("ports can't be published with host or no networking")
		}
		s.Networks = nil
	}
	for i, n := range s.Networks {
		if n.IPv4 != "" {
			a, err := netip.ParseAddr(n.IPv4)
			if err != nil || !a.Is4() {
				return fmt.Errorf("%q isn't an IPv4 address", n.IPv4)
			}
		}
		if n.Name == "" {
			return errors.New("choose a network")
		}
		s.Networks[i] = n
	}
	if s.NetworkMode != "bridge" && s.NetworkMode != "host" && s.NetworkMode != "none" {
		// A user network as the main one must also be in Networks (first).
		found := false
		for _, n := range s.Networks {
			found = found || n.Name == s.NetworkMode
		}
		if !found {
			s.Networks = append([]NetLink{{Name: s.NetworkMode}}, s.Networks...)
		}
	}
	return nil
}

func (s Spec) devices() []map[string]any {
	out := []map[string]any{}
	for _, d := range s.Devices {
		host, ctr, ok := strings.Cut(d, ":")
		if !ok {
			ctr = host
		}
		out = append(out, map[string]any{"PathOnHost": host, "PathInContainer": ctr, "CgroupPermissions": "rwm"})
	}
	return out
}

func (s Spec) portMaps() (exposed map[string]any, bindings map[string]any) {
	exposed, bindings = map[string]any{}, map[string]any{}
	for _, p := range s.Ports {
		key := fmt.Sprintf("%d/%s", p.Container, p.Protocol)
		exposed[key] = map[string]any{}
		if p.Host == 0 {
			continue
		}
		list, _ := bindings[key].([]any)
		bindings[key] = append(list, map[string]any{"HostIp": p.HostIP, "HostPort": strconv.Itoa(p.Host)})
	}
	return exposed, bindings
}

func (s Spec) mountList() []map[string]any {
	out := []map[string]any{}
	for _, m := range s.Mounts {
		out = append(out, map[string]any{"Type": m.Type, "Source": m.Source, "Target": m.Target, "ReadOnly": m.ReadOnly})
	}
	return out
}

func endpoint(n NetLink) map[string]any {
	ep := map[string]any{}
	if n.IPv4 != "" {
		ep["IPAMConfig"] = map[string]any{"IPv4Address": n.IPv4}
	}
	if len(n.Aliases) > 0 {
		ep["Aliases"] = n.Aliases
	}
	return ep
}

// createBody merges a Spec into a container's current settings: the edited
// fields are replaced, everything else is kept.
func createBody(raw *rawContainer, s Spec) map[string]any {
	body := map[string]any{}
	for k, v := range raw.Config {
		body[k] = v
	}
	hc := map[string]any{}
	for k, v := range raw.HostConfig {
		hc[k] = v
	}
	// The old container's image id / generated hostname must not carry over.
	delete(body, "Hostname")
	if s.Hostname != "" && s.NetworkMode != "host" {
		body["Hostname"] = s.Hostname
	}
	body["Image"] = s.Image
	old := map[string]string{}
	if l, ok := raw.Config["Labels"].(map[string]any); ok {
		for k, v := range l {
			if sv, ok := v.(string); ok {
				old[k] = sv
			}
		}
	}
	body["Labels"] = s.withIPLabels(old)
	if len(s.Cmd) > 0 {
		body["Cmd"] = s.Cmd
	}
	env := make([]string, 0, len(s.Env))
	for _, e := range s.Env {
		env = append(env, e.Key+"="+e.Value)
	}
	body["Env"] = env
	exposed, bindings := s.portMaps()
	body["ExposedPorts"] = exposed
	hc["PortBindings"] = bindings
	hc["PublishAllPorts"] = false
	// Storage: everything goes through Mounts (old -v style Binds/Volumes are replaced).
	delete(hc, "Binds")
	delete(hc, "VolumesFrom")
	delete(body, "Volumes")
	hc["Mounts"] = s.mountList()
	hc["RestartPolicy"] = map[string]any{"Name": s.Restart, "MaximumRetryCount": 0}
	hc["Memory"] = s.MemoryMB << 20
	hc["MemorySwap"] = 0
	hc["NanoCpus"] = int64(s.CPUs * 1e9)
	hc["Privileged"] = s.Privileged
	hc["Devices"] = s.devices()
	hc["NetworkMode"] = s.NetworkMode
	body["HostConfig"] = hc
	if len(s.Networks) > 0 && s.NetworkMode == s.Networks[0].Name {
		body["NetworkingConfig"] = map[string]any{"EndpointsConfig": map[string]any{s.Networks[0].Name: endpoint(s.Networks[0])}}
	}
	return body
}

// Recreate applies a Spec to an existing container: the old one is stopped
// and set aside, a new one is created with the changes and started, and only
// then is the old one removed (its volumes are kept). If anything fails the
// old container is put back as it was. It returns the new container's id.
func (c *Client) Recreate(ctx context.Context, ref string, s Spec, pull func(image string) error) (string, error) {
	raw, err := c.inspectRaw(ctx, ref)
	if err != nil {
		return "", err
	}
	if labels, ok := raw.Config["Labels"].(map[string]any); ok && labels[LabelSystem] == "true" {
		return "", errors.New("system containers can't be edited")
	}
	if ok, err := c.ImageExists(ctx, s.Image); err != nil {
		return "", err
	} else if !ok {
		if pull == nil {
			return "", fmt.Errorf("image %s isn't on this server", s.Image)
		}
		if err := pull(s.Image); err != nil {
			return "", fmt.Errorf("download %s: %w", s.Image, err)
		}
	}
	oldName := strings.TrimPrefix(raw.Name, "/")
	wasRunning := raw.State.Running
	if wasRunning {
		if err := c.StopContainer(ctx, raw.ID, 20*time.Second); err != nil {
			return "", fmt.Errorf("stop the container: %w", err)
		}
	}
	aside := fmt.Sprintf("%s-old-%d", oldName, time.Now().Unix())
	if err := c.RenameContainer(ctx, raw.ID, aside); err != nil {
		if wasRunning {
			_ = c.StartContainer(context.WithoutCancel(ctx), raw.ID)
		}
		return "", fmt.Errorf("set the old container aside: %w", err)
	}
	restore := func(newID string) {
		bg := context.WithoutCancel(ctx)
		if newID != "" {
			_ = c.removeContainer(bg, newID, false)
		}
		_ = c.RenameContainer(bg, raw.ID, oldName)
		if wasRunning {
			_ = c.StartContainer(bg, raw.ID)
		}
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := c.doBody(ctx, http.MethodPost, "/containers/create", url.Values{"name": {s.Name}}, createBody(raw, s), &created); err != nil {
		restore("")
		return "", err
	}
	for i, n := range s.Networks {
		if i == 0 && n.Name == s.NetworkMode {
			continue
		}
		if err := c.ConnectNetwork(ctx, n, created.ID); err != nil {
			restore(created.ID)
			return "", fmt.Errorf("join network %s: %w", n.Name, err)
		}
	}
	if wasRunning {
		if err := c.StartContainer(ctx, created.ID); err != nil {
			restore(created.ID)
			return "", fmt.Errorf("start the new container (your old one is back): %w", err)
		}
	}
	if err := c.removeContainer(context.WithoutCancel(ctx), raw.ID, false); err != nil {
		return created.ID, fmt.Errorf("the change worked, but the old container %s couldn't be removed: %w", aside, err)
	}
	return created.ID, nil
}

// RenameContainer gives a container a new name.
func (c *Client) RenameContainer(ctx context.Context, id, name string) error {
	return c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/rename", url.Values{"name": {name}}, nil)
}

// removeContainer force-removes a container, with or without its anonymous volumes.
func (c *Client) removeContainer(ctx context.Context, id string, volumes bool) error {
	v := "0"
	if volumes {
		v = "1"
	}
	return c.do(ctx, http.MethodDelete, "/containers/"+url.PathEscape(id), url.Values{"force": {"1"}, "v": {v}}, nil)
}

// ConnectNetwork attaches a container to a network.
func (c *Client) ConnectNetwork(ctx context.Context, n NetLink, container string) error {
	return c.doBody(ctx, http.MethodPost, "/networks/"+url.PathEscape(n.Name)+"/connect", nil,
		map[string]any{"Container": container, "EndpointConfig": endpoint(n)}, nil)
}

// --- networks ---

// Network is a Docker network as the UI sees it.
type Network struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Driver     string   `json:"driver"`
	Subnet     string   `json:"subnet,omitempty"`
	Gateway    string   `json:"gateway,omitempty"`
	IPRange    string   `json:"ip_range,omitempty"`
	Parent     string   `json:"parent,omitempty"`
	Mode       string   `json:"mode,omitempty"` // ipvlan mode (l2/l3)
	Internal   bool     `json:"internal"`
	Builtin    bool     `json:"builtin"`
	Containers []string `json:"containers"`
}

type rawNetwork struct {
	ID       string `json:"Id"`
	Name     string `json:"Name"`
	Driver   string `json:"Driver"`
	Internal bool   `json:"Internal"`
	IPAM     struct {
		Config []struct {
			Subnet  string `json:"Subnet"`
			Gateway string `json:"Gateway"`
			IPRange string `json:"IPRange"`
		} `json:"Config"`
	} `json:"IPAM"`
	Options    map[string]string `json:"Options"`
	Containers map[string]struct {
		Name string `json:"Name"`
	} `json:"Containers"`
}

// ListNetworks returns the networks, with the containers on each.
func (c *Client) ListNetworks(ctx context.Context) ([]Network, error) {
	var list []rawNetwork
	if err := c.do(ctx, http.MethodGet, "/networks", nil, &list); err != nil {
		return nil, err
	}
	out := make([]Network, 0, len(list))
	for _, n := range list {
		// The list may leave out the containers; ask for each network.
		var full rawNetwork
		if err := c.do(ctx, http.MethodGet, "/networks/"+url.PathEscape(n.ID), nil, &full); err == nil {
			n = full
		}
		v := Network{ID: n.ID, Name: n.Name, Driver: n.Driver, Internal: n.Internal, Parent: n.Options["parent"], Mode: n.Options["ipvlan_mode"], Containers: []string{}}
		v.Builtin = n.Name == "bridge" || n.Name == "host" || n.Name == "none" || n.Name == "podman"
		if len(n.IPAM.Config) > 0 {
			v.Subnet, v.Gateway, v.IPRange = n.IPAM.Config[0].Subnet, n.IPAM.Config[0].Gateway, n.IPAM.Config[0].IPRange
		}
		for _, ct := range n.Containers {
			v.Containers = append(v.Containers, ct.Name)
		}
		sort.Strings(v.Containers)
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Builtin != out[j].Builtin {
			return !out[i].Builtin
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// NewNetwork is a network to create.
type NewNetwork struct {
	Name     string `json:"name"`
	Driver   string `json:"driver"` // bridge | macvlan | ipvlan
	Subnet   string `json:"subnet"`
	Gateway  string `json:"gateway"`
	IPRange  string `json:"ip_range"`
	Parent   string `json:"parent"` // host interface for macvlan/ipvlan, e.g. eth0
	Mode     string `json:"mode"`   // ipvlan: l2 (default) | l3
	Internal bool   `json:"internal"`
}

var ifaceRe = regexp.MustCompile(`^[A-Za-z0-9_.:@-]{1,32}$`)

// Validate checks a network definition.
func (n *NewNetwork) Validate() error {
	n.Name = strings.TrimSpace(n.Name)
	if !nameRe.MatchString(n.Name) {
		return errors.New("the name can use letters, numbers, . _ and -")
	}
	switch n.Driver {
	case "bridge":
	case "macvlan", "ipvlan":
		if !ifaceRe.MatchString(n.Parent) {
			return errors.New("choose the network card it goes out on (e.g. eth0)")
		}
		if n.Subnet == "" {
			return errors.New("macvlan and ipvlan need your LAN's subnet, e.g. 192.168.1.0/24")
		}
	default:
		return errors.New("choose bridge, macvlan or ipvlan")
	}
	if n.Mode != "" && n.Mode != "l2" && n.Mode != "l3" {
		return errors.New("ipvlan mode is l2 or l3")
	}
	var subnet netip.Prefix
	if n.Subnet != "" {
		p, err := netip.ParsePrefix(n.Subnet)
		if err != nil {
			return fmt.Errorf("%q isn't a subnet like 192.168.1.0/24", n.Subnet)
		}
		subnet = p.Masked()
		n.Subnet = subnet.String()
	}
	if n.Gateway != "" {
		g, err := netip.ParseAddr(n.Gateway)
		if err != nil || (subnet.IsValid() && !subnet.Contains(g)) {
			return fmt.Errorf("the gateway %q must be an address inside the subnet", n.Gateway)
		}
	}
	if n.IPRange != "" {
		r, err := netip.ParsePrefix(n.IPRange)
		if err != nil || (subnet.IsValid() && (!subnet.Contains(r.Addr()) || r.Bits() < subnet.Bits())) {
			return fmt.Errorf("the address range %q must be a smaller block inside the subnet", n.IPRange)
		}
	}
	return nil
}

// CreateNetworkFrom creates a bridge, macvlan or ipvlan network.
func (c *Client) CreateNetworkFrom(ctx context.Context, n NewNetwork, labels map[string]string) error {
	body := map[string]any{"Name": n.Name, "Driver": n.Driver, "CheckDuplicate": true, "Internal": n.Internal, "Attachable": true, "Labels": labels}
	if n.Subnet != "" {
		cfg := map[string]any{"Subnet": n.Subnet}
		if n.Gateway != "" {
			cfg["Gateway"] = n.Gateway
		}
		if n.IPRange != "" {
			cfg["IPRange"] = n.IPRange
		}
		body["IPAM"] = map[string]any{"Driver": "default", "Config": []any{cfg}}
	}
	opts := map[string]string{}
	if n.Driver == "macvlan" || n.Driver == "ipvlan" {
		opts["parent"] = n.Parent
	}
	if n.Driver == "ipvlan" && n.Mode != "" {
		opts["ipvlan_mode"] = n.Mode
	}
	body["Options"] = opts
	return c.doBody(ctx, http.MethodPost, "/networks/create", nil, body, nil)
}

// CreateConfig builds a fresh container's settings from a Spec (for new
// containers, and App Store apps with saved changes). Networks after the
// first are joined with ConnectNetwork once the container exists.
func (s Spec) CreateConfig(labels map[string]string) CreateConfig {
	cfg := CreateConfig{
		Image:        s.Image,
		Cmd:          s.Cmd,
		Labels:       s.withIPLabels(labels),
		ExposedPorts: map[string]struct{}{},
		HostConfig: HostConfig{
			PortBindings:  map[string][]PortBinding{},
			RestartPolicy: &RestartPolicy{Name: s.Restart},
			Memory:        s.MemoryMB << 20,
			NanoCpus:      int64(s.CPUs * 1e9),
			Privileged:    s.Privileged,
			NetworkMode:   s.NetworkMode,
		},
	}
	if s.NetworkMode != "host" {
		cfg.Hostname = s.Hostname
	}
	for _, e := range s.Env {
		cfg.Env = append(cfg.Env, e.Key+"="+e.Value)
	}
	for _, p := range s.Ports {
		key := fmt.Sprintf("%d/%s", p.Container, p.Protocol)
		cfg.ExposedPorts[key] = struct{}{}
		if p.Host != 0 {
			cfg.HostConfig.PortBindings[key] = append(cfg.HostConfig.PortBindings[key], PortBinding{HostIP: p.HostIP, HostPort: strconv.Itoa(p.Host)})
		}
	}
	for _, m := range s.Mounts {
		cfg.HostConfig.Mounts = append(cfg.HostConfig.Mounts, MountSpec{Type: m.Type, Source: m.Source, Target: m.Target, ReadOnly: m.ReadOnly})
	}
	for _, d := range s.devices() {
		cfg.HostConfig.Devices = append(cfg.HostConfig.Devices, DeviceMapping{PathOnHost: d["PathOnHost"].(string), PathInContainer: d["PathInContainer"].(string), CgroupPermissions: "rwm"})
	}
	if len(s.Networks) > 0 && s.Networks[0].Name == s.NetworkMode {
		n := s.Networks[0]
		ep := EndpointConfig{Aliases: n.Aliases}
		if n.IPv4 != "" {
			ep.IPAMConfig = &EndpointIPAM{IPv4Address: n.IPv4}
		}
		cfg.NetworkingConfig = &NetworkingConfig{EndpointsConfig: map[string]EndpointConfig{n.Name: ep}}
	}
	return cfg
}

// ExtraNetworks are the networks to join after creating (all but the main one).
func (s Spec) ExtraNetworks() []NetLink {
	var out []NetLink
	for i, n := range s.Networks {
		if i == 0 && n.Name == s.NetworkMode {
			continue
		}
		out = append(out, n)
	}
	return out
}

// Volume is a named volume.
type Volume struct {
	Name   string `json:"name"`
	Driver string `json:"driver"`
}

// ListVolumes returns the named volumes.
func (c *Client) ListVolumes(ctx context.Context) ([]Volume, error) {
	var out struct {
		Volumes []struct {
			Name   string `json:"Name"`
			Driver string `json:"Driver"`
		} `json:"Volumes"`
	}
	if err := c.do(ctx, http.MethodGet, "/volumes", nil, &out); err != nil {
		return nil, err
	}
	vols := make([]Volume, 0, len(out.Volumes))
	for _, v := range out.Volumes {
		vols = append(vols, Volume{Name: v.Name, Driver: v.Driver})
	}
	sort.Slice(vols, func(i, j int) bool { return vols[i].Name < vols[j].Name })
	return vols, nil
}
