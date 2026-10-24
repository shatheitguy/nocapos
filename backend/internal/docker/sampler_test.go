package docker

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

const oneShotSample = `{
  "read": "2026-10-17T10:00:02Z",
  "cpu_stats": {"cpu_usage": {"total_usage": 3000000000}, "system_cpu_usage": 40000000000, "online_cpus": 4},
  "precpu_stats": {"cpu_usage": {"total_usage": 0}, "system_cpu_usage": 0},
  "memory_stats": {"usage": 600000000, "limit": 2000000000, "stats": {"inactive_file": 100000000}},
  "networks": {"eth0": {"rx_bytes": 3000, "tx_bytes": 1500}, "eth1": {"rx_bytes": 1000, "tx_bytes": 500}},
  "blkio_stats": {"io_service_bytes_recursive": [{"op": "Read", "value": 8192}, {"op": "Write", "value": 4096}, {"op": "read", "value": 8192}]},
  "pids_stats": {"current": 12}
}`

func TestSampleFromOneShot(t *testing.T) {
	var raw statsJSON
	if err := json.Unmarshal([]byte(oneShotSample), &raw); err != nil {
		t.Fatal(err)
	}
	s := sampleFrom(&raw)
	if s.stats.CPUPercent != 0 {
		t.Errorf("one-shot sample has no CPU baseline, got %v", s.stats.CPUPercent)
	}
	if s.stats.MemUsage != 500000000 || s.stats.MemPercent != 25 {
		t.Errorf("memory: got %d (%v%%), want 500000000 (25%%)", s.stats.MemUsage, s.stats.MemPercent)
	}
	if s.stats.NetRx != 4000 || s.stats.NetTx != 2000 {
		t.Errorf("network totals: %d/%d", s.stats.NetRx, s.stats.NetTx)
	}
	if s.stats.BlockRead != 16384 || s.stats.BlockWrite != 4096 {
		t.Errorf("block totals: %d/%d", s.stats.BlockRead, s.stats.BlockWrite)
	}
	if s.online != 4 || s.stats.Pids != 12 {
		t.Errorf("online %d pids %d", s.online, s.stats.Pids)
	}
}

func TestMergeComputesCPUAndRates(t *testing.T) {
	t0 := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	prev := sample{stats: Stats{Time: t0, NetRx: 1000, NetTx: 500, BlockRead: 0, BlockWrite: 1000}, cpuTotal: 1e9, sysTotal: 20e9, online: 4}
	cur := sample{stats: Stats{Time: t0.Add(2 * time.Second), NetRx: 5000, NetTx: 2500, BlockRead: 4096, BlockWrite: 1000}, cpuTotal: 3e9, sysTotal: 40e9, online: 4}
	st := merge(cur, prev)
	// 2e9 of 20e9 host time on 4 CPUs = 40% (of one core).
	if math.Abs(st.CPUPercent-40) > 1e-9 {
		t.Errorf("cpu = %v, want 40", st.CPUPercent)
	}
	if st.NetRxRate != 2000 || st.NetTxRate != 1000 {
		t.Errorf("net rates %v/%v, want 2000/1000", st.NetRxRate, st.NetTxRate)
	}
	if st.BlockReadRate != 2048 || st.BlockWriteRate != 0 {
		t.Errorf("block rates %v/%v, want 2048/0", st.BlockReadRate, st.BlockWriteRate)
	}
}

func TestMergeHandlesRestartedContainer(t *testing.T) {
	t0 := time.Now()
	prev := sample{stats: Stats{Time: t0, NetRx: 9000}, cpuTotal: 9e9, sysTotal: 20e9, online: 2}
	cur := sample{stats: Stats{Time: t0.Add(time.Second), NetRx: 100}, cpuTotal: 1e8, sysTotal: 21e9, online: 2}
	st := merge(cur, prev)
	if st.CPUPercent != 0 || st.NetRxRate != 0 {
		t.Errorf("counter reset should read as 0, got cpu %v rx %v", st.CPUPercent, st.NetRxRate)
	}
}

func TestMergeKeepsEngineCPU(t *testing.T) {
	t0 := time.Now()
	cur := sample{stats: Stats{Time: t0.Add(time.Second), CPUPercent: 12.5}, cpuTotal: 5, sysTotal: 5}
	if st := merge(cur, sample{stats: Stats{Time: t0}}); st.CPUPercent != 12.5 {
		t.Errorf("engine CPU figure should win, got %v", st.CPUPercent)
	}
}
