package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ---------- stats ----------

type statsJSON struct {
	Read        time.Time `json:"read"`
	CPUStats    cpuStats  `json:"cpu_stats"`
	PreCPUStats cpuStats  `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
	BlkioStats struct {
		IoServiceBytesRecursive []struct {
			Op    string `json:"op"`
			Value uint64 `json:"value"`
		} `json:"io_service_bytes_recursive"`
	} `json:"blkio_stats"`
	PidsStats struct {
		Current uint64 `json:"current"`
	} `json:"pids_stats"`
}

type cpuStats struct {
	CPUUsage struct {
		TotalUsage  uint64   `json:"total_usage"`
		PercpuUsage []uint64 `json:"percpu_usage"`
	} `json:"cpu_usage"`
	SystemUsage uint64 `json:"system_cpu_usage"`
	OnlineCPUs  uint32 `json:"online_cpus"`
}

type Stats struct {
	Time       time.Time `json:"time"`
	CPUPercent float64   `json:"cpu_percent"` // 100 = one full core
	MemUsage   uint64    `json:"mem_usage"`
	MemLimit   uint64    `json:"mem_limit"`
	MemPercent float64   `json:"mem_percent"`
	NetRx      uint64    `json:"net_rx"`
	NetTx      uint64    `json:"net_tx"`
	NetRxRate  float64   `json:"net_rx_rate"` // bytes/s
	NetTxRate  float64   `json:"net_tx_rate"`
	BlockRead  uint64    `json:"block_read"`
	BlockWrite uint64    `json:"block_write"`
	Pids       uint64    `json:"pids"`
}

func (s *statsJSON) toStats() Stats {
	out := Stats{
		Time:       s.Read,
		CPUPercent: cpuPercent(s),
		MemUsage:   memUsage(s),
		MemLimit:   s.MemoryStats.Limit,
		Pids:       s.PidsStats.Current,
	}
	if out.Time.IsZero() {
		out.Time = time.Now()
	}
	if out.MemLimit > 0 {
		out.MemPercent = float64(out.MemUsage) / float64(out.MemLimit) * 100
	}
	for _, n := range s.Networks {
		out.NetRx += n.RxBytes
		out.NetTx += n.TxBytes
	}
	for _, b := range s.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(b.Op) {
		case "read":
			out.BlockRead += b.Value
		case "write":
			out.BlockWrite += b.Value
		}
	}
	return out
}

// cpuPercent mirrors `docker stats`: share of total host CPU time, scaled by
// the number of online CPUs.
func cpuPercent(s *statsJSON) float64 {
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(s.CPUStats.SystemUsage) - float64(s.PreCPUStats.SystemUsage)
	online := float64(s.CPUStats.OnlineCPUs)
	if online == 0 {
		online = float64(len(s.CPUStats.CPUUsage.PercpuUsage))
	}
	if cpuDelta <= 0 || sysDelta <= 0 {
		return 0
	}
	return cpuDelta / sysDelta * online * 100
}

// memUsage excludes reclaimable page cache, matching the Docker CLI.
func memUsage(s *statsJSON) uint64 {
	u := s.MemoryStats.Usage
	for _, key := range []string{"total_inactive_file", "inactive_file"} { // cgroup v1, v2
		if v, ok := s.MemoryStats.Stats[key]; ok && v < u {
			return u - v
		}
	}
	return u
}

// Stats streams resource usage for a container (about one sample per second)
// until ctx is cancelled or the stream ends.
func (c *Client) Stats(ctx context.Context, id string, fn func(Stats) error) error {
	resp, err := c.send(ctx, c.stream, http.MethodGet, "/containers/"+url.PathEscape(id)+"/stats", url.Values{"stream": {"1"}})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	var prev *Stats
	for {
		var raw statsJSON
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return nil
			}
			return err
		}
		s := raw.toStats()
		if prev != nil {
			if dt := s.Time.Sub(prev.Time).Seconds(); dt > 0 {
				s.NetRxRate = rate(s.NetRx, prev.NetRx, dt)
				s.NetTxRate = rate(s.NetTx, prev.NetTx, dt)
			}
		}
		if err := fn(s); err != nil {
			return err
		}
		prev = &s
	}
}

func rate(cur, prev uint64, dt float64) float64 {
	if cur < prev { // counter reset (container restarted)
		return 0
	}
	return float64(cur-prev) / dt
}

// ---------- logs ----------

type LogLine struct {
	Stream string    `json:"stream"`
	Time   time.Time `json:"time"`
	Text   string    `json:"text"`
}

const maxLogLine = 64 * 1024

// Logs follows a container's output, starting with the last `tail` lines.
func (c *Client) Logs(ctx context.Context, id string, tail int, fn func(LogLine) error) error {
	info, err := c.inspect(ctx, id)
	if err != nil {
		return err
	}
	q := url.Values{
		"follow": {"1"}, "stdout": {"1"}, "stderr": {"1"},
		"timestamps": {"1"}, "tail": {strconv.Itoa(tail)},
	}
	resp, err := c.send(ctx, c.stream, http.MethodGet, "/containers/"+url.PathEscape(id)+"/logs", q)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	stdout := &lineWriter{fn: func(b []byte) error { return fn(parseLogLine("stdout", b)) }}
	stderr := &lineWriter{fn: func(b []byte) error { return fn(parseLogLine("stderr", b)) }}
	if info.Config.Tty {
		// TTY containers have a single raw stream with no multiplexing header.
		_, err = io.Copy(stdout, resp.Body)
	} else {
		err = demux(resp.Body, stdout, stderr)
	}
	if ferr := stdout.Close(); err == nil {
		err = ferr
	}
	if ferr := stderr.Close(); err == nil {
		err = ferr
	}
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// demux splits Docker's multiplexed stream: each frame is an 8-byte header
// [stream, 0, 0, 0, size(uint32 BE)] followed by size bytes.
func demux(r io.Reader, stdout, stderr io.Writer) error {
	var hdr [8]byte
	buf := make([]byte, 32*1024)
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		var w io.Writer
		switch hdr[0] {
		case 1:
			w = stdout
		case 2, 3: // 3 = daemon-side error
			w = stderr
		default:
			w = io.Discard
		}
		size := int64(binary.BigEndian.Uint32(hdr[4:]))
		if _, err := io.CopyBuffer(w, io.LimitReader(r, size), buf); err != nil {
			return err
		}
	}
}

// lineWriter reassembles lines that may span frames and truncates lines
// longer than maxLogLine.
type lineWriter struct {
	buf []byte
	fn  func([]byte) error
}

func (w *lineWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			w.append(p)
			break
		}
		w.append(p[:i])
		if err := w.flush(); err != nil {
			return 0, err
		}
		p = p[i+1:]
	}
	return n, nil
}

func (w *lineWriter) append(p []byte) {
	if room := maxLogLine - len(w.buf); room > 0 {
		if len(p) > room {
			p = p[:room]
		}
		w.buf = append(w.buf, p...)
	}
}

func (w *lineWriter) flush() error {
	line := bytes.TrimSuffix(w.buf, []byte{'\r'})
	err := w.fn(line)
	w.buf = w.buf[:0]
	return err
}

func (w *lineWriter) Close() error {
	if len(w.buf) == 0 {
		return nil
	}
	return w.flush()
}

// parseLogLine splits the RFC3339Nano prefix added by timestamps=1.
func parseLogLine(stream string, line []byte) LogLine {
	if i := bytes.IndexByte(line, ' '); i > 0 {
		if t, err := time.Parse(time.RFC3339Nano, string(line[:i])); err == nil {
			return LogLine{Stream: stream, Time: t, Text: string(line[i+1:])}
		}
	}
	return LogLine{Stream: stream, Text: string(line)}
}

// ---------- events ----------

type Event struct {
	Type     string    `json:"type"`
	Action   string    `json:"action"`
	ID       string    `json:"id"`
	Name     string    `json:"name,omitempty"`
	Image    string    `json:"image,omitempty"`
	ExitCode string    `json:"exit_code,omitempty"`
	App      string    `json:"app,omitempty"` // App Store app id (nocapos.app label)
	Time     time.Time `json:"time"`
}

type eventJSON struct {
	Type   string
	Action string
	Actor  struct {
		ID         string
		Attributes map[string]string
	}
	TimeNano int64 `json:"timeNano"`
}

// Events streams engine events for containers, images, networks and volumes.
func (c *Client) Events(ctx context.Context, fn func(Event) error) error {
	q := url.Values{"filters": {`{"type":["container","image","network","volume"]}`}}
	resp, err := c.send(ctx, c.stream, http.MethodGet, "/events", q)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	for {
		var raw eventJSON
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return nil
			}
			return err
		}
		// exec_* actions embed the executed command line, which can contain
		// secrets; they are not forwarded. Attributes also carry every container
		// label, so only a whitelist is exposed.
		if strings.HasPrefix(raw.Action, "exec_") {
			continue
		}
		a := raw.Actor.Attributes
		ev := Event{
			Type:     raw.Type,
			Action:   raw.Action,
			ID:       raw.Actor.ID,
			Name:     a["name"],
			Image:    a["image"],
			ExitCode: a["exitCode"],
			App:      a["nocapos.app"],
			Time:     time.Unix(0, raw.TimeNano).UTC(),
		}
		if err := fn(ev); err != nil {
			return err
		}
	}
}
