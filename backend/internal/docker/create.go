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

type HostConfig struct {
	PortBindings  map[string][]PortBinding `json:"PortBindings,omitempty"`
	ShmSize       int64                    `json:"ShmSize,omitempty"`
	RestartPolicy *RestartPolicy           `json:"RestartPolicy,omitempty"`
	SecurityOpt   []string                 `json:"SecurityOpt,omitempty"`
}

type CreateConfig struct {
	Image        string              `json:"Image"`
	Env          []string            `json:"Env,omitempty"`
	Labels       map[string]string   `json:"Labels,omitempty"`
	ExposedPorts map[string]struct{} `json:"ExposedPorts,omitempty"`
	HostConfig   HostConfig          `json:"HostConfig"`
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
