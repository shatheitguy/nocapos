package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// StatsSampler takes one-shot usage samples of many containers at once, for
// overview screens that refresh every few seconds. One-shot samples come back
// immediately (no second sample inside the engine), so CPU and the network
// and disk rates are worked out against the previous call's sample instead.
type StatsSampler struct {
	c *Client

	mu   sync.Mutex
	prev map[string]sample
}

type sample struct {
	stats    Stats
	cpuTotal uint64
	sysTotal uint64
	online   uint32
	seen     time.Time
}

func NewStatsSampler(c *Client) *StatsSampler {
	return &StatsSampler{c: c, prev: map[string]sample{}}
}

// statsConcurrency bounds the parallel stats calls to the engine.
const statsConcurrency = 8

// Sample returns current usage for each container id (missing on failure).
// Several windows may ask for different containers; each container's last
// sample is kept for a minute as the next call's baseline.
func (s *StatsSampler) Sample(ctx context.Context, ids []string) map[string]Stats {
	type result struct {
		id string
		sm sample
		ok bool
	}
	results := make(chan result, len(ids))
	sem := make(chan struct{}, statsConcurrency)
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			sm, err := s.c.statsOnce(cctx, id)
			results <- result{id: id, sm: sm, ok: err == nil}
		}(id)
	}
	wg.Wait()
	close(results)

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	out := make(map[string]Stats, len(ids))
	for r := range results {
		if !r.ok {
			continue
		}
		cur := r.sm
		cur.seen = now
		if p, ok := s.prev[r.id]; ok {
			cur.stats = merge(cur, p)
		}
		s.prev[r.id] = cur
		out[r.id] = cur.stats
	}
	// Forget containers nobody has asked about for a while (removed ones).
	for id, p := range s.prev {
		if now.Sub(p.seen) > sampleTTL {
			delete(s.prev, id)
		}
	}
	return out
}

// sampleTTL is how long a container's last sample is kept as the baseline.
const sampleTTL = time.Minute

// merge works out CPU % and rates for cur against the previous sample p. A
// CPU figure the engine already computed (non one-shot) is kept.
func merge(cur, p sample) Stats {
	st := cur.stats
	st.withRates(p.stats)
	if st.CPUPercent == 0 {
		st.CPUPercent = cpuBetween(cur, p)
	}
	return st
}

// cpuBetween is `docker stats`' formula over two samples taken by us.
func cpuBetween(cur, prev sample) float64 {
	if cur.cpuTotal <= prev.cpuTotal || cur.sysTotal <= prev.sysTotal {
		return 0 // no time passed, or the container restarted
	}
	online := float64(cur.online)
	if online == 0 {
		online = 1
	}
	return float64(cur.cpuTotal-prev.cpuTotal) / float64(cur.sysTotal-prev.sysTotal) * online * 100
}

func (c *Client) statsOnce(ctx context.Context, id string) (sample, error) {
	q := url.Values{"stream": {"0"}, "one-shot": {"1"}}
	resp, err := c.send(ctx, c.hc, http.MethodGet, "/containers/"+url.PathEscape(id)+"/stats", q)
	if err != nil {
		return sample{}, err
	}
	defer resp.Body.Close()
	var raw statsJSON
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return sample{}, err
	}
	return sampleFrom(&raw), nil
}

func sampleFrom(raw *statsJSON) sample {
	online := raw.CPUStats.OnlineCPUs
	if online == 0 {
		online = uint32(len(raw.CPUStats.CPUUsage.PercpuUsage))
	}
	return sample{
		stats:    raw.toStats(),
		cpuTotal: raw.CPUStats.CPUUsage.TotalUsage,
		sysTotal: raw.CPUStats.SystemUsage,
		online:   online,
	}
}
