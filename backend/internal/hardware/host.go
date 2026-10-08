package hardware

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type HostInfo struct {
	Hostname string    `json:"hostname"`
	OS       string    `json:"os"`
	Kernel   string    `json:"kernel"`
	Arch     string    `json:"arch"`
	CPUModel string    `json:"cpu_model"`
	CPUCores int       `json:"cpu_cores"`
	Board    string    `json:"board,omitempty"`
	MemTotal uint64    `json:"mem_total"`
	BootTime time.Time `json:"boot_time"`
}

func readHostInfo(proc, sys, etc string) HostInfo {
	h := HostInfo{Arch: runtime.GOARCH}

	h.Hostname, _ = readTrimmed(filepath.Join(etc, "hostname"))
	if h.Hostname == "" {
		h.Hostname, _ = os.Hostname()
	}
	h.OS = osPrettyName(filepath.Join(etc, "os-release"))
	h.Kernel, _ = readTrimmed(filepath.Join(proc, "sys", "kernel", "osrelease"))
	h.CPUModel = cpuModel(filepath.Join(proc, "cpuinfo"))
	if _, cores, err := readCPUTimes(proc); err == nil {
		h.CPUCores = len(cores)
	}
	if m, err := readMeminfo(proc); err == nil {
		h.MemTotal = m["MemTotal"]
	}
	if up := readUptime(proc); up > 0 {
		h.BootTime = time.Now().Add(-time.Duration(up * float64(time.Second))).UTC().Truncate(time.Second)
	}

	// ARM boards describe themselves in the device tree; x86 via DMI.
	if model, err := readTrimmed(filepath.Join(sys, "firmware", "devicetree", "base", "model")); err == nil && model != "" {
		h.Board = model
	} else {
		vendor, _ := readTrimmed(filepath.Join(sys, "class", "dmi", "id", "sys_vendor"))
		product, _ := readTrimmed(filepath.Join(sys, "class", "dmi", "id", "product_name"))
		h.Board = strings.TrimSpace(vendor + " " + product)
	}
	if nativeHostInfo != nil {
		nativeHostInfo(&h)
	}
	return h
}

func osPrettyName(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "PRETTY_NAME="); ok {
			if u, err := strconv.Unquote(v); err == nil {
				return u
			}
			return strings.Trim(v, `"'`)
		}
	}
	return ""
}

// cpuModel handles x86 ("model name"), most ARM kernels ("Hardware"/"Model")
// and falls back to the ARM "CPU part" when nothing better exists.
func cpuModel(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	found := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if _, seen := found[k]; !seen && v != "" {
			found[k] = v
		}
	}
	return nonEmpty(found["model name"], found["Hardware"], found["Model"], found["cpu model"], found["uarch"], found["CPU part"])
}
