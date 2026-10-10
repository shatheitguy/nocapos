package vm

import (
	"bufio"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Parsers for virsh and qemu-img output (run with LC_ALL=C).

// parseUUIDs reads `virsh list --all --uuid`.
func parseUUIDs(out string) []string {
	var ids []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			ids = append(ids, l)
		}
	}
	return ids
}

// parseState reads `virsh domstate --reason`, e.g. "shut off (destroyed)".
func parseState(out string) (state, reason string) {
	s := strings.TrimSpace(out)
	if i := strings.Index(s, " ("); i >= 0 && strings.HasSuffix(s, ")") {
		reason = s[i+2 : len(s)-1]
		s = s[:i]
	}
	return mapState(s), reason
}

func mapState(s string) string {
	switch strings.TrimSpace(s) {
	case "running", "idle", "blocked", "no state":
		return StateRunning
	case "paused":
		return StatePaused
	case "in shutdown":
		return StateStopping
	case "crashed":
		return StateCrashed
	case "pmsuspended":
		return StateSuspended
	}
	return StateOff
}

// parseInfo reads `virsh dominfo` ("Key:   value" lines).
func parseInfo(out string) map[string]string {
	m := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(l, ":")
		if ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}

// rawStats is one sample of `virsh domstats`.
type rawStats struct {
	CPUTime  uint64 // ns, all vCPUs
	VCPUs    int
	MemUsed  int64 // bytes
	MemTotal int64
}

// parseDomStats reads `virsh domstats --cpu-total --balloon --vcpu`, keyed by name.
func parseDomStats(out string) map[string]rawStats {
	res := map[string]rawStats{}
	var name string
	var cur rawStats
	var avail, unused, rss, current int64
	flush := func() {
		if name == "" {
			return
		}
		cur.MemTotal = current << 10
		switch {
		case avail > 0 && unused >= 0 && unused <= avail:
			cur.MemUsed = (avail - unused) << 10 // reported by the guest's balloon driver
		case rss > 0:
			cur.MemUsed = min(rss, current) << 10
		}
		res[name] = cur
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(l, "Domain:") {
			flush()
			name = strings.Trim(strings.TrimSpace(strings.TrimPrefix(l, "Domain:")), "'")
			cur, avail, unused, rss, current = rawStats{}, 0, -1, 0, 0
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		n, _ := strconv.ParseInt(v, 10, 64)
		switch k {
		case "cpu.time":
			cur.CPUTime = uint64(n)
		case "vcpu.current":
			cur.VCPUs = int(n)
		case "balloon.current":
			current = n
		case "balloon.available":
			avail = n
		case "balloon.unused":
			unused = n
		case "balloon.rss":
			rss = n
		}
	}
	flush()
	return res
}

// parseVNCDisplay reads `virsh vncdisplay`: ":1", "127.0.0.1:1" or "[::1]:1"
// mean display 1, port 5901.
func parseVNCDisplay(out string) (int, bool) {
	s := strings.TrimSpace(out)
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(s[i+1:])
	if err != nil || n < 0 || n > 59635 {
		return 0, false
	}
	return 5900 + n, true
}

var snapLineRe = regexp.MustCompile(`^\s*(\S+)\s+(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d(?: [+-]\d{4})?)\s+(.+?)\s*$`)

// parseSnapshots reads the `virsh snapshot-list` table.
func parseSnapshots(out string) []Snapshot {
	snaps := []Snapshot{}
	for _, l := range strings.Split(out, "\n") {
		m := snapLineRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		t, err := time.Parse("2006-01-02 15:04:05 -0700", m[2])
		if err != nil {
			t, _ = time.Parse("2006-01-02 15:04:05", m[2])
		}
		snaps = append(snaps, Snapshot{Name: m[1], Created: t.UTC(), State: mapState(m[3])})
	}
	return snaps
}

// parseImgInfo reads `qemu-img info --output=json`.
func parseImgInfo(b []byte) (size, used int64, format string, err error) {
	var info struct {
		VirtualSize int64  `json:"virtual-size"`
		ActualSize  int64  `json:"actual-size"`
		Format      string `json:"format"`
	}
	if err = json.Unmarshal(b, &info); err != nil {
		return 0, 0, "", err
	}
	return info.VirtualSize, info.ActualSize, info.Format, nil
}

// parseMemTotal reads MemTotal from /proc/meminfo, in MiB.
func parseMemTotal(b []byte) int {
	for _, l := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(l, "MemTotal:"); ok {
			f := strings.Fields(rest)
			if len(f) > 0 {
				kb, _ := strconv.ParseInt(f[0], 10, 64)
				return int(kb >> 10)
			}
		}
	}
	return 0
}
