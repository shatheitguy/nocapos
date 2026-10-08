package hardware

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"
)

type Options struct {
	Proc, Sys, Etc string
	DiskPaths      []string
	Interval       time.Duration
}

type CPUStat struct {
	Percent float64   `json:"percent"`
	PerCore []float64 `json:"per_core"`
	Load1   float64   `json:"load1"`
	Load5   float64   `json:"load5"`
	Load15  float64   `json:"load15"`
}

type MemStat struct {
	Total     uint64  `json:"total"`
	Used      uint64  `json:"used"`
	Available uint64  `json:"available"`
	Percent   float64 `json:"percent"`
	SwapTotal uint64  `json:"swap_total"`
	SwapUsed  uint64  `json:"swap_used"`
}

type DiskStat struct {
	Path    string  `json:"path"`
	Total   uint64  `json:"total"`
	Used    uint64  `json:"used"`
	Free    uint64  `json:"free"`
	Percent float64 `json:"percent"`
}

type NetStat struct {
	Interface string  `json:"interface"`
	RxBytes   uint64  `json:"rx_bytes"`
	TxBytes   uint64  `json:"tx_bytes"`
	RxRate    float64 `json:"rx_rate"` // bytes/s
	TxRate    float64 `json:"tx_rate"`
}

type Snapshot struct {
	Time         time.Time  `json:"time"`
	Uptime       float64    `json:"uptime"`
	CPU          CPUStat    `json:"cpu"`
	Memory       MemStat    `json:"memory"`
	Disks        []DiskStat `json:"disks"`
	Network      []NetStat  `json:"network"`
	Temperatures []TempStat `json:"temperatures"`
	Accelerators []GPU      `json:"accelerators"`
}

// idleInterval is used while nobody is watching live metrics, so REST
// snapshots stay reasonably fresh without burning CPU on small boards.
const idleInterval = 30 * time.Second

// Sampler periodically collects a Snapshot and fans it out to subscribers.
type Sampler struct {
	opt  Options
	log  *slog.Logger
	gpus *GPUProbe

	mu     sync.Mutex
	latest *Snapshot
	subs   map[chan *Snapshot]struct{}
	wake   chan struct{}

	// Delta state, touched only by the Run goroutine.
	prevCPU   cpuTimes
	prevCores []cpuTimes
	prevNet   map[string]netCounter
	prevAt    time.Time
}

func NewSampler(opt Options, log *slog.Logger) *Sampler {
	return &Sampler{
		opt:  opt,
		log:  log,
		gpus: DiscoverGPUs(opt.Sys, log),
		subs: make(map[chan *Snapshot]struct{}),
		wake: make(chan struct{}, 1),
	}
}

// DiskUsage reports capacity for the filesystem containing path.
func DiskUsage(path string) (total, used, free uint64, err error) { return diskUsage(path) }

func (s *Sampler) HostInfo() HostInfo  { return readHostInfo(s.opt.Proc, s.opt.Sys, s.opt.Etc) }
func (s *Sampler) Accelerators() []GPU { return s.gpus.Devices() }

func (s *Sampler) Latest() *Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latest
}

// Subscribe returns a channel that always holds the most recent snapshot
// (older undelivered ones are dropped) and a cancel func.
func (s *Sampler) Subscribe() (<-chan *Snapshot, func()) {
	ch := make(chan *Snapshot, 1)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	first := len(s.subs) == 1
	s.mu.Unlock()
	if first {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.subs, ch)
			s.mu.Unlock()
		})
	}
}

func (s *Sampler) Run(ctx context.Context) {
	s.publish(s.sample(ctx))
	t := time.NewTimer(s.opt.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-t.C:
		}
		s.publish(s.sample(ctx))

		s.mu.Lock()
		next := idleInterval
		if len(s.subs) > 0 {
			next = s.opt.Interval
		}
		s.mu.Unlock()
		t.Reset(next)
	}
}

func (s *Sampler) publish(snap *Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest = snap
	for ch := range s.subs {
		select {
		case ch <- snap:
		default: // replace the stale snapshot the subscriber has not read yet
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- snap:
			default:
			}
		}
	}
}

func (s *Sampler) sample(ctx context.Context) *Snapshot {
	now := time.Now()
	snap := &Snapshot{Time: now.UTC(), Uptime: readUptime(s.opt.Proc)}

	if total, cores, err := readCPUTimes(s.opt.Proc); err == nil {
		snap.CPU.Percent = total.usage(s.prevCPU)
		snap.CPU.PerCore = make([]float64, len(cores))
		for i, c := range cores {
			if i < len(s.prevCores) {
				snap.CPU.PerCore[i] = c.usage(s.prevCores[i])
			}
		}
		s.prevCPU, s.prevCores = total, cores
	} else {
		s.log.Debug("read cpu", "err", err)
	}
	snap.CPU.Load1, snap.CPU.Load5, snap.CPU.Load15 = readLoadavg(s.opt.Proc)

	if m, err := readMeminfo(s.opt.Proc); err == nil {
		mem := MemStat{Total: m["MemTotal"], Available: m["MemAvailable"], SwapTotal: m["SwapTotal"]}
		if mem.Total > mem.Available {
			mem.Used = mem.Total - mem.Available
		}
		if mem.Total > 0 {
			mem.Percent = float64(mem.Used) / float64(mem.Total) * 100
		}
		if mem.SwapTotal > m["SwapFree"] {
			mem.SwapUsed = mem.SwapTotal - m["SwapFree"]
		}
		snap.Memory = mem
	}

	for _, p := range s.opt.DiskPaths {
		total, used, free, err := diskUsage(p)
		if err != nil || total == 0 || used+free == 0 {
			continue
		}
		snap.Disks = append(snap.Disks, DiskStat{
			Path: p, Total: total, Used: used, Free: free,
			Percent: float64(used) / float64(used+free) * 100, // same basis as df
		})
	}

	if counters, err := readNetDev(s.opt.Proc); err == nil {
		dt := now.Sub(s.prevAt).Seconds()
		names := make([]string, 0, len(counters))
		for n := range counters {
			if !isVirtualInterface(n) {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		for _, n := range names {
			c := counters[n]
			ns := NetStat{Interface: n, RxBytes: c.rx, TxBytes: c.tx}
			if prev, ok := s.prevNet[n]; ok && dt > 0 {
				if c.rx >= prev.rx {
					ns.RxRate = float64(c.rx-prev.rx) / dt
				}
				if c.tx >= prev.tx {
					ns.TxRate = float64(c.tx-prev.tx) / dt
				}
			}
			snap.Network = append(snap.Network, ns)
		}
		s.prevNet = counters
	}

	snap.Temperatures = readTemps(s.opt.Sys)
	snap.Accelerators = s.gpus.Sample(ctx)
	s.prevAt = now
	return snap
}
