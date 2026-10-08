// Package hardware reads host telemetry straight from procfs/sysfs.
//
// Parsing /proc and /sys directly (instead of a portability library) keeps
// alfad dependency-free and behaves identically on x86 servers, ARM SBCs and
// Jetson boards. Paths are rooted at configurable mount points so the daemon
// can observe the host from inside its container.
package hardware

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Platform hooks for hosts without procfs (native Windows builds). When set,
// they replace the procfs readers.
var (
	nativeCPUTimes func() (cpuTimes, []cpuTimes, error)
	nativeMeminfo  func() (map[string]uint64, error)
	nativeUptime   func() float64
	nativeHostInfo func(*HostInfo)
	nativeNetDev   func() (map[string]netCounter, error)
)

func readTrimmed(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.TrimRight(string(b), "\x00")), nil
}

// ---------- CPU ----------

type cpuTimes struct{ idle, total uint64 }

func (c cpuTimes) usage(prev cpuTimes) float64 {
	if c.total <= prev.total || c.idle < prev.idle {
		return 0
	}
	dt := float64(c.total - prev.total)
	di := float64(c.idle - prev.idle)
	return clamp((1-di/dt)*100, 0, 100)
}

func readCPUTimes(proc string) (cpuTimes, []cpuTimes, error) {
	if nativeCPUTimes != nil {
		return nativeCPUTimes()
	}
	f, err := os.Open(filepath.Join(proc, "stat"))
	if err != nil {
		return cpuTimes{}, nil, err
	}
	defer f.Close()
	return parseCPUTimes(f)
}

func parseCPUTimes(r io.Reader) (total cpuTimes, cores []cpuTimes, err error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 || !strings.HasPrefix(fields[0], "cpu") {
			continue
		}
		var t cpuTimes
		// user nice system idle iowait irq softirq steal [guest guest_nice];
		// guest time is already counted in user/nice.
		for i, v := range fields[1:] {
			if i >= 8 {
				break
			}
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				continue
			}
			t.total += n
			if i == 3 || i == 4 {
				t.idle += n
			}
		}
		if fields[0] == "cpu" {
			total = t
		} else {
			cores = append(cores, t)
		}
	}
	return total, cores, sc.Err()
}

// ---------- memory / load / uptime ----------

// readMeminfo returns /proc/meminfo values in bytes.
func readMeminfo(proc string) (map[string]uint64, error) {
	if nativeMeminfo != nil {
		return nativeMeminfo()
	}
	b, err := os.ReadFile(filepath.Join(proc, "meminfo"))
	if err != nil {
		return nil, err
	}
	out := make(map[string]uint64, 64)
	for _, line := range strings.Split(string(b), "\n") {
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		v, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		if len(f) > 1 && f[1] == "kB" {
			v *= 1024
		}
		out[key] = v
	}
	return out, nil
}

func readLoadavg(proc string) (l1, l5, l15 float64) {
	s, err := readTrimmed(filepath.Join(proc, "loadavg"))
	if err != nil {
		return
	}
	f := strings.Fields(s)
	if len(f) >= 3 {
		l1, _ = strconv.ParseFloat(f[0], 64)
		l5, _ = strconv.ParseFloat(f[1], 64)
		l15, _ = strconv.ParseFloat(f[2], 64)
	}
	return
}

func readUptime(proc string) float64 {
	if nativeUptime != nil {
		return nativeUptime()
	}
	s, err := readTrimmed(filepath.Join(proc, "uptime"))
	if err != nil {
		return 0
	}
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}

// ---------- network ----------

type netCounter struct{ rx, tx uint64 }

// readNetDev prefers PID 1's view (the host network namespace when /proc is
// the host's) over our own container namespace.
func readNetDev(proc string) (map[string]netCounter, error) {
	if nativeNetDev != nil {
		return nativeNetDev()
	}
	var lastErr error
	for _, p := range []string{filepath.Join(proc, "1", "net", "dev"), filepath.Join(proc, "net", "dev")} {
		b, err := os.ReadFile(p)
		if err == nil {
			return parseNetDev(b), nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func parseNetDev(b []byte) map[string]netCounter {
	out := map[string]netCounter{}
	for _, line := range bytes.Split(b, []byte{'\n'}) {
		name, rest, ok := strings.Cut(string(line), ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		rx, err1 := strconv.ParseUint(f[0], 10, 64)
		tx, err2 := strconv.ParseUint(f[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		out[strings.TrimSpace(name)] = netCounter{rx: rx, tx: tx}
	}
	return out
}

var virtualIfPrefixes = []string{"veth", "docker", "br-", "virbr", "cni", "flannel", "cali", "vxlan", "tunl", "kube-ipvs"}

func isVirtualInterface(name string) bool {
	if name == "lo" {
		return true
	}
	for _, p := range virtualIfPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// ---------- temperatures ----------

type TempStat struct {
	Sensor  string  `json:"sensor"`
	Celsius float64 `json:"celsius"`
}

// readTemps collects thermal zones (ARM SoCs, laptops) and hwmon sensors
// (x86 coretemp/k10temp, NVMe). GPU sensors are reported per GPU instead.
func readTemps(sys string) []TempStat {
	var out []TempStat
	zones, _ := filepath.Glob(filepath.Join(sys, "class", "thermal", "thermal_zone*"))
	for _, z := range zones {
		typ, _ := readTrimmed(filepath.Join(z, "type"))
		if c, ok := readMilliC(filepath.Join(z, "temp")); ok {
			out = append(out, TempStat{Sensor: nonEmpty(typ, filepath.Base(z)), Celsius: c})
		}
	}
	mons, _ := filepath.Glob(filepath.Join(sys, "class", "hwmon", "hwmon*"))
	for _, m := range mons {
		name, _ := readTrimmed(filepath.Join(m, "name"))
		switch name {
		case "amdgpu", "nouveau", "i915", "xe", "": // GPUs are handled by the GPU probe
			continue
		}
		if c, ok := readMilliC(filepath.Join(m, "temp1_input")); ok {
			out = append(out, TempStat{Sensor: name, Celsius: c})
		}
	}
	return out
}

func readMilliC(path string) (float64, bool) {
	s, err := readTrimmed(path)
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	c := float64(v) / 1000
	if c <= -40 || c >= 150 { // disconnected or bogus sensor
		return 0, false
	}
	return c, true
}

// ---------- helpers ----------

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func nonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func linkBase(path string) string {
	target, err := os.Readlink(path)
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}
