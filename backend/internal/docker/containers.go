package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LabelSystem marks Alfa OS's own containers; the API refuses to stop them.
const LabelSystem = "alfa.system"

// --- wire types (Engine API JSON) ---

type containerSummary struct {
	ID      string `json:"Id"`
	Names   []string
	Image   string
	Created int64
	State   string
	Status  string
	Ports   []struct {
		IP          string
		PrivatePort uint16
		PublicPort  uint16
		Type        string
	}
	Labels map[string]string
}

type containerJSON struct {
	ID           string `json:"Id"`
	Name         string
	Created      time.Time
	RestartCount int
	State        struct {
		Status     string
		Running    bool
		OOMKilled  bool
		ExitCode   int
		StartedAt  time.Time
		FinishedAt time.Time
		Health     *struct{ Status string }
	}
	Config struct {
		Image  string
		Tty    bool
		Labels map[string]string
	}
	HostConfig struct {
		Privileged    bool
		NetworkMode   string
		Runtime       string
		RestartPolicy struct{ Name string }
	}
	Mounts []struct {
		Type        string
		Source      string
		Destination string
		RW          bool
	}
	NetworkSettings struct {
		Networks map[string]struct{ IPAddress string }
	}
}

type infoJSON struct {
	ServerVersion     string
	OperatingSystem   string
	OSType            string
	Architecture      string
	KernelVersion     string
	Driver            string
	DockerRootDir     string
	CgroupVersion     string
	NCPU              int
	MemTotal          int64
	Containers        int
	ContainersRunning int
	ContainersPaused  int
	ContainersStopped int
	Images            int
	DefaultRuntime    string
	Runtimes          map[string]json.RawMessage
}

type imageSummary struct {
	ID         string `json:"Id"`
	RepoTags   []string
	Created    int64
	Size       int64
	Containers int64
}

// --- API views (what alfad returns to clients) ---

type Port struct {
	IP       string `json:"ip,omitempty"`
	Private  uint16 `json:"private"`
	Public   uint16 `json:"public,omitempty"`
	Protocol string `json:"protocol"`
}

type Container struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Image   string            `json:"image"`
	State   string            `json:"state"`
	Status  string            `json:"status"`
	Created time.Time         `json:"created"`
	Ports   []Port            `json:"ports"`
	Project string            `json:"project,omitempty"`
	System  bool              `json:"system"`
	Labels  map[string]string `json:"labels"`
}

type Mount struct {
	Type        string `json:"type"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	RW          bool   `json:"rw"`
}

type NetworkAttachment struct {
	Name string `json:"name"`
	IP   string `json:"ip,omitempty"`
}

// ContainerDetail deliberately omits Config.Env: environment variables
// routinely hold credentials and are never sent to the browser.
type ContainerDetail struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Image         string              `json:"image"`
	Created       time.Time           `json:"created"`
	State         string              `json:"state"`
	Running       bool                `json:"running"`
	ExitCode      int                 `json:"exit_code"`
	OOMKilled     bool                `json:"oom_killed"`
	StartedAt     time.Time           `json:"started_at"`
	FinishedAt    time.Time           `json:"finished_at"`
	Health        string              `json:"health,omitempty"`
	RestartCount  int                 `json:"restart_count"`
	RestartPolicy string              `json:"restart_policy"`
	Privileged    bool                `json:"privileged"`
	NetworkMode   string              `json:"network_mode"`
	Runtime       string              `json:"runtime"`
	Tty           bool                `json:"tty"`
	System        bool                `json:"system"`
	Labels        map[string]string   `json:"labels"`
	Mounts        []Mount             `json:"mounts"`
	Networks      []NetworkAttachment `json:"networks"`
}

type Info struct {
	ServerVersion  string   `json:"server_version"`
	APIVersion     string   `json:"api_version"`
	OS             string   `json:"os"`
	OSType         string   `json:"os_type"`
	Arch           string   `json:"arch"`
	KernelVersion  string   `json:"kernel_version"`
	StorageDriver  string   `json:"storage_driver"`
	RootDir        string   `json:"root_dir"`
	CgroupVersion  string   `json:"cgroup_version"`
	CPUs           int      `json:"cpus"`
	MemTotal       int64    `json:"mem_total"`
	Containers     int      `json:"containers"`
	Running        int      `json:"running"`
	Paused         int      `json:"paused"`
	Stopped        int      `json:"stopped"`
	Images         int      `json:"images"`
	DefaultRuntime string   `json:"default_runtime"`
	Runtimes       []string `json:"runtimes"`
}

type Image struct {
	ID         string    `json:"id"`
	Tags       []string  `json:"tags"`
	Created    time.Time `json:"created"`
	Size       int64     `json:"size"`
	Containers int64     `json:"containers"`
}

// --- endpoints ---

func (c *Client) Info(ctx context.Context) (*Info, error) {
	var raw infoJSON
	if err := c.do(ctx, http.MethodGet, "/info", nil, &raw); err != nil {
		return nil, err
	}
	runtimes := make([]string, 0, len(raw.Runtimes))
	for name := range raw.Runtimes {
		runtimes = append(runtimes, name)
	}
	sort.Strings(runtimes)
	return &Info{
		ServerVersion:  raw.ServerVersion,
		APIVersion:     c.version,
		OS:             raw.OperatingSystem,
		OSType:         raw.OSType,
		Arch:           raw.Architecture,
		KernelVersion:  raw.KernelVersion,
		StorageDriver:  raw.Driver,
		RootDir:        raw.DockerRootDir,
		CgroupVersion:  raw.CgroupVersion,
		CPUs:           raw.NCPU,
		MemTotal:       raw.MemTotal,
		Containers:     raw.Containers,
		Running:        raw.ContainersRunning,
		Paused:         raw.ContainersPaused,
		Stopped:        raw.ContainersStopped,
		Images:         raw.Images,
		DefaultRuntime: raw.DefaultRuntime,
		Runtimes:       runtimes,
	}, nil
}

func (c *Client) ListContainers(ctx context.Context, all bool) ([]Container, error) {
	q := url.Values{}
	if all {
		q.Set("all", "1")
	}
	var raw []containerSummary
	if err := c.do(ctx, http.MethodGet, "/containers/json", q, &raw); err != nil {
		return nil, err
	}
	out := make([]Container, 0, len(raw))
	for _, r := range raw {
		ct := Container{
			ID:      r.ID,
			Image:   r.Image,
			State:   r.State,
			Status:  r.Status,
			Created: time.Unix(r.Created, 0).UTC(),
			Ports:   make([]Port, 0, len(r.Ports)),
			Labels:  r.Labels,
			Project: r.Labels["com.docker.compose.project"],
			System:  r.Labels[LabelSystem] == "true",
		}
		if len(r.Names) > 0 {
			ct.Name = strings.TrimPrefix(r.Names[0], "/")
		}
		for _, p := range r.Ports {
			ct.Ports = append(ct.Ports, Port{IP: p.IP, Private: p.PrivatePort, Public: p.PublicPort, Protocol: p.Type})
		}
		out = append(out, ct)
	}
	return out, nil
}

func (c *Client) inspect(ctx context.Context, id string) (*containerJSON, error) {
	var raw containerJSON
	if err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json", nil, &raw); err != nil {
		return nil, err
	}
	return &raw, nil
}

func (c *Client) InspectContainer(ctx context.Context, id string) (*ContainerDetail, error) {
	raw, err := c.inspect(ctx, id)
	if err != nil {
		return nil, err
	}
	d := &ContainerDetail{
		ID:            raw.ID,
		Name:          strings.TrimPrefix(raw.Name, "/"),
		Image:         raw.Config.Image,
		Created:       raw.Created,
		State:         raw.State.Status,
		Running:       raw.State.Running,
		ExitCode:      raw.State.ExitCode,
		OOMKilled:     raw.State.OOMKilled,
		StartedAt:     raw.State.StartedAt,
		FinishedAt:    raw.State.FinishedAt,
		RestartCount:  raw.RestartCount,
		RestartPolicy: raw.HostConfig.RestartPolicy.Name,
		Privileged:    raw.HostConfig.Privileged,
		NetworkMode:   raw.HostConfig.NetworkMode,
		Runtime:       raw.HostConfig.Runtime,
		Tty:           raw.Config.Tty,
		System:        raw.Config.Labels[LabelSystem] == "true",
		Labels:        raw.Config.Labels,
		Mounts:        make([]Mount, 0, len(raw.Mounts)),
		Networks:      make([]NetworkAttachment, 0, len(raw.NetworkSettings.Networks)),
	}
	if raw.State.Health != nil {
		d.Health = raw.State.Health.Status
	}
	for _, m := range raw.Mounts {
		d.Mounts = append(d.Mounts, Mount{Type: m.Type, Source: m.Source, Destination: m.Destination, RW: m.RW})
	}
	for name, n := range raw.NetworkSettings.Networks {
		d.Networks = append(d.Networks, NetworkAttachment{Name: name, IP: n.IPAddress})
	}
	sort.Slice(d.Networks, func(i, j int) bool { return d.Networks[i].Name < d.Networks[j].Name })
	return d, nil
}

// 304 Not Modified (already started/stopped) is treated as success.
func (c *Client) StartContainer(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/start", nil, nil)
}

func (c *Client) StopContainer(ctx context.Context, id string, timeout time.Duration) error {
	q := url.Values{"t": {strconv.Itoa(int(timeout.Seconds()))}}
	return c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/stop", q, nil)
}

func (c *Client) RestartContainer(ctx context.Context, id string, timeout time.Duration) error {
	q := url.Values{"t": {strconv.Itoa(int(timeout.Seconds()))}}
	return c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/restart", q, nil)
}

func (c *Client) ListImages(ctx context.Context) ([]Image, error) {
	var raw []imageSummary
	if err := c.do(ctx, http.MethodGet, "/images/json", nil, &raw); err != nil {
		return nil, err
	}
	out := make([]Image, 0, len(raw))
	for _, r := range raw {
		tags := r.RepoTags
		if tags == nil {
			tags = []string{}
		}
		out = append(out, Image{ID: r.ID, Tags: tags, Created: time.Unix(r.Created, 0).UTC(), Size: r.Size, Containers: r.Containers})
	}
	return out, nil
}
