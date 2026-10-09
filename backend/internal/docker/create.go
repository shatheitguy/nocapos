package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

// --- image pull ---

// PullProgress is one line of a pull's progress stream.
type PullProgress struct {
	Status   string `json:"status"`
	ID       string `json:"id"`
	Progress string `json:"progress"`
	Error    string `json:"error"`
}

// ImageExists reports whether an image reference is present locally.
func (c *Client) ImageExists(ctx context.Context, ref string) (bool, error) {
	resp, err := c.send(ctx, c.hc, http.MethodGet, "/images/"+url.PathEscape(ref)+"/json", nil)
	if err != nil {
		if IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	resp.Body.Close()
	return true, nil
}

// PullImage pulls fromImage:tag, invoking onProgress for each status line. It
// blocks until the pull completes; callers should run it off the request path.
func (c *Client) PullImage(ctx context.Context, image, tag string, onProgress func(PullProgress)) error {
	q := url.Values{"fromImage": {image}, "tag": {tag}}
	resp, err := c.sendBody(ctx, c.stream, http.MethodPost, "/images/create", q, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var p PullProgress
		if json.Unmarshal(line, &p) != nil {
			continue
		}
		if p.Error != "" {
			return &APIError{Status: http.StatusBadGateway, Message: p.Error}
		}
		if onProgress != nil {
			onProgress(p)
		}
	}
	return sc.Err()
}

// --- container create ---

type PortBinding struct {
	HostIP   string `json:"HostIp,omitempty"`
	HostPort string `json:"HostPort"`
}

type RestartPolicy struct {
	Name string `json:"Name"`
}

// MountSpec attaches a named volume (Type "volume") or host path (Type "bind").
type MountSpec struct {
	Type     string `json:"Type"`
	Source   string `json:"Source"`
	Target   string `json:"Target"`
	ReadOnly bool   `json:"ReadOnly,omitempty"`
}

// DeviceMapping passes a host device (e.g. /dev/dri) into a container.
type DeviceMapping struct {
	PathOnHost        string `json:"PathOnHost"`
	PathInContainer   string `json:"PathInContainer"`
	CgroupPermissions string `json:"CgroupPermissions"`
}

type HostConfig struct {
	PortBindings  map[string][]PortBinding `json:"PortBindings,omitempty"`
	Memory        int64                    `json:"Memory,omitempty"`
	NanoCpus      int64                    `json:"NanoCpus,omitempty"`
	Privileged    bool                     `json:"Privileged,omitempty"`
	Devices       []DeviceMapping          `json:"Devices,omitempty"`
	ShmSize       int64                    `json:"ShmSize,omitempty"`
	RestartPolicy *RestartPolicy           `json:"RestartPolicy,omitempty"`
	SecurityOpt   []string                 `json:"SecurityOpt,omitempty"`
	Mounts        []MountSpec              `json:"Mounts,omitempty"`
	NetworkMode   string                   `json:"NetworkMode,omitempty"`
}

// EndpointConfig gives a container DNS aliases on a user network.
type EndpointConfig struct {
	Aliases    []string      `json:"Aliases,omitempty"`
	IPAMConfig *EndpointIPAM `json:"IPAMConfig,omitempty"`
}

// EndpointIPAM pins a container's address on a network.
type EndpointIPAM struct {
	IPv4Address string `json:"IPv4Address,omitempty"`
}

type NetworkingConfig struct {
	EndpointsConfig map[string]EndpointConfig `json:"EndpointsConfig,omitempty"`
}

type CreateConfig struct {
	Image            string              `json:"Image"`
	Hostname         string              `json:"Hostname,omitempty"`
	Cmd              []string            `json:"Cmd,omitempty"`
	Env              []string            `json:"Env,omitempty"`
	Labels           map[string]string   `json:"Labels,omitempty"`
	ExposedPorts     map[string]struct{} `json:"ExposedPorts,omitempty"`
	HostConfig       HostConfig          `json:"HostConfig"`
	NetworkingConfig *NetworkingConfig   `json:"NetworkingConfig,omitempty"`
}

// CreateContainer creates a container and returns its id.
func (c *Client) CreateContainer(ctx context.Context, name string, cfg CreateConfig) (string, error) {
	var out struct {
		ID string `json:"Id"`
	}
	if err := c.doBody(ctx, http.MethodPost, "/containers/create", url.Values{"name": {name}}, cfg, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// RemoveContainer force-removes a container (and its anonymous volumes).
func (c *Client) RemoveContainer(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/containers/"+id, url.Values{"force": {"1"}, "v": {"1"}}, nil)
}

// HostPort returns the host port a container's private port is published on
// (e.g. privatePort "6901/tcp"). Returns 0 if it is not published.
func (c *Client) HostPort(ctx context.Context, id, privatePort string) (int, error) {
	var out struct {
		NetworkSettings struct {
			Ports map[string][]struct {
				HostIP   string `json:"HostIp"`
				HostPort string `json:"HostPort"`
			}
		}
	}
	if err := c.do(ctx, http.MethodGet, "/containers/"+id+"/json", nil, &out); err != nil {
		return 0, err
	}
	for _, b := range out.NetworkSettings.Ports[privatePort] {
		if b.HostPort == "" {
			continue
		}
		if p, err := strconv.Atoi(b.HostPort); err == nil {
			return p, nil
		}
	}
	return 0, nil
}

// FindContainer returns the id and running state of a container by name, or
// ok=false if there is no such container.
func (c *Client) FindContainer(ctx context.Context, name string) (id string, running bool, ok bool, err error) {
	var out struct {
		ID    string `json:"Id"`
		State struct{ Running bool }
	}
	if err = c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(name)+"/json", nil, &out); err != nil {
		if IsNotFound(err) {
			return "", false, false, nil
		}
		return "", false, false, err
	}
	return out.ID, out.State.Running, true, nil
}

// --- networks & volumes (App Store apps) ---

// CreateNetwork creates a bridge network unless one with that name exists.
func (c *Client) CreateNetwork(ctx context.Context, name string, labels map[string]string) error {
	err := c.doBody(ctx, http.MethodPost, "/networks/create", nil,
		map[string]any{"Name": name, "Driver": "bridge", "CheckDuplicate": true, "Labels": labels}, nil)
	if apiErr, ok := err.(*APIError); ok && apiErr.Status == http.StatusConflict {
		return nil
	}
	return err
}

// RemoveNetwork deletes a network; a missing one is not an error.
func (c *Client) RemoveNetwork(ctx context.Context, name string) error {
	err := c.do(ctx, http.MethodDelete, "/networks/"+url.PathEscape(name), nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// RemoveVolume deletes a named volume; a missing one is not an error.
func (c *Client) RemoveVolume(ctx context.Context, name string) error {
	err := c.do(ctx, http.MethodDelete, "/volumes/"+url.PathEscape(name), url.Values{"force": {"1"}}, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// ContainersByLabel lists containers (running or not) carrying label=value.
func (c *Client) ContainersByLabel(ctx context.Context, label, value string) ([]Container, error) {
	all, err := c.ListContainers(ctx, true)
	if err != nil {
		return nil, err
	}
	var out []Container
	for _, ct := range all {
		if ct.Labels[label] == value {
			out = append(out, ct)
		}
	}
	return out, nil
}
