package storage

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// parseSize reads ZFS/human sizes: "0B", "800G", "3.45T", "1.2K", "512".
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" || s == "none" {
		return 0, nil
	}
	mult := 1.0
	last := s[len(s)-1]
	if last == 'B' && len(s) > 1 && (s[len(s)-2] < '0' || s[len(s)-2] > '9') {
		s = s[:len(s)-1] // "1.2KB"-ish
		last = s[len(s)-1]
	}
	switch last {
	case 'B':
		s = s[:len(s)-1]
	case 'K', 'k':
		mult, s = 1<<10, s[:len(s)-1]
	case 'M':
		mult, s = 1<<20, s[:len(s)-1]
	case 'G':
		mult, s = 1<<30, s[:len(s)-1]
	case 'T':
		mult, s = 1<<40, s[:len(s)-1]
	case 'P':
		mult, s = 1<<50, s[:len(s)-1]
	case 'E':
		mult, s = 1<<60, s[:len(s)-1]
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return int64(math.Round(f * mult)), nil
}

func atoi(s string) int64 {
	v, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(s), "%"), 10, 64)
	if err != nil {
		if f, err2 := parseSize(s); err2 == nil {
			return f
		}
		return 0
	}
	return v
}

var groupNames = map[string]string{"logs": "logs", "cache": "cache", "spares": "spares", "special": "special", "dedup": "dedup"}

// vdevType derives the type from a vdev name ("mirror-0" -> mirror).
func vdevType(name string) string {
	base := name
	if i := strings.LastIndex(name, "-"); i > 0 {
		if _, err := strconv.Atoi(name[i+1:]); err == nil {
			base = name[:i]
		}
	}
	switch {
	case base == "mirror", base == "replacing", base == "spare", base == "raidz1", base == "raidz2", base == "raidz3":
		return base
	case base == "raidz":
		return "raidz1"
	case strings.HasPrefix(name, "draid"):
		return "draid"
	case strings.HasPrefix(name, "/") && !strings.HasPrefix(name, "/dev/"):
		return "file"
	}
	return "disk"
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

var keyLineRe = regexp.MustCompile(`^ *(pool|id|state|status|action|see|scan|config|errors): ?(.*)$`)

type textBlock struct {
	fields map[string]string
	config []string
}

// splitBlocks splits `zpool status` / `zpool import` text into per-pool blocks.
func splitBlocks(out string) []textBlock {
	var blocks []textBlock
	var cur *textBlock
	key := ""
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if m := keyLineRe.FindStringSubmatch(line); m != nil && !strings.HasPrefix(line, "\t") {
			if m[1] == "pool" {
				blocks = append(blocks, textBlock{fields: map[string]string{}})
				cur = &blocks[len(blocks)-1]
			}
			if cur == nil {
				continue
			}
			key = m[1]
			cur.fields[key] = strings.TrimSpace(m[2])
			continue
		}
		if cur == nil {
			continue
		}
		if key == "config" {
			if strings.TrimSpace(line) != "" {
				cur.config = append(cur.config, line)
			}
			continue
		}
		if t := strings.TrimSpace(line); t != "" && key != "" {
			cur.fields[key] += " " + t
		}
	}
	return blocks
}

// parseConfig reads the indented NAME STATE READ WRITE CKSUM tree.
// It returns the root (the pool) with its vdevs and group headers as children.
func parseConfig(lines []string) *Vdev {
	type frame struct {
		level int
		v     *Vdev
	}
	var root *Vdev
	var stack []frame
	for _, raw := range lines {
		line := strings.TrimPrefix(raw, "\t")
		trim := strings.TrimLeft(line, " ")
		if strings.HasPrefix(trim, "NAME ") || trim == "NAME" {
			continue
		}
		level := (len(line) - len(trim)) / 2
		f := strings.Fields(trim)
		if len(f) == 0 {
			continue
		}
		v := &Vdev{Name: f[0], Children: []Vdev{}}
		if len(f) > 1 {
			v.Health = f[1]
		}
		rest := f[min(2, len(f)):]
		if len(rest) >= 3 && isCount(rest[0]) && isCount(rest[1]) && isCount(rest[2]) {
			v.ReadErrors, v.WriteErrors, v.ChecksumErrors = atoi(rest[0]), atoi(rest[1]), atoi(rest[2])
			rest = rest[3:]
		}
		note := strings.Join(rest, " ")
		if strings.HasPrefix(note, "was ") {
			v.Was = strings.TrimPrefix(note, "was ")
		} else if note != "" {
			v.Note = strings.Trim(note, "()")
		}
		if root == nil {
			v.Type = "root"
			root = v
			stack = []frame{{level, v}}
			continue
		}
		if g, ok := groupNames[v.Name]; ok && level == stack[0].level && v.Health == "" {
			v.Type = g
			root.Children = append(root.Children, *v)
			stack = []frame{stack[0], {level, &root.Children[len(root.Children)-1]}}
			continue
		}
		v.Type = vdevType(v.Name)
		if isDigits(v.Name) {
			v.GUID = v.Name
		} else if strings.HasPrefix(v.Name, "/") {
			v.Path = v.Name
		}
		for len(stack) > 1 && stack[len(stack)-1].level >= level {
			stack = stack[:len(stack)-1]
		}
		parent := stack[len(stack)-1].v
		parent.Children = append(parent.Children, *v)
		stack = append(stack, frame{level, &parent.Children[len(parent.Children)-1]})
	}
	return root
}

func isCount(s string) bool {
	if isDigits(s) {
		return true
	}
	_, err := parseSize(s) // "1.2K" without -p
	return err == nil && s != ""
}

var (
	pctRe     = regexp.MustCompile(`([0-9.]+)% done`)
	etaDaysRe = regexp.MustCompile(`(\d+) days? (\d+):(\d+):(\d+) to go`)
	etaRe     = regexp.MustCompile(`(\d+):(\d+):(\d+) to go`)
	errsRe    = regexp.MustCompile(`with (\d+) errors`)
	onDateRe  = regexp.MustCompile(` on ([A-Z][a-z]{2} [A-Z][a-z]{2} +\d+ \d+:\d+:\d+ \d{4})`)
)

func parseZDate(s string) string {
	t, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", strings.Join(strings.Fields(s), " "), time.Local)
	if err != nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

// parseScanText reads the "scan:" field of zpool status.
func parseScanText(s string) Scan {
	s = strings.Join(strings.Fields(s), " ")
	sc := Scan{State: "none"}
	if s == "" || strings.HasPrefix(s, "none requested") {
		return sc
	}
	switch {
	case strings.HasPrefix(s, "resilver"):
		sc.Function = "resilver"
	case strings.HasPrefix(s, "scrub"):
		sc.Function = "scrub"
	}
	switch {
	case strings.Contains(s, "in progress"):
		sc.State = "scanning"
		if m := pctRe.FindStringSubmatch(s); m != nil {
			if p, err := strconv.ParseFloat(m[1], 64); err == nil {
				sc.Percent = &p
			}
		}
		if m := etaDaysRe.FindStringSubmatch(s); m != nil {
			e := atoi(m[1])*86400 + atoi(m[2])*3600 + atoi(m[3])*60 + atoi(m[4])
			sc.EtaSeconds = &e
		} else if m := etaRe.FindStringSubmatch(s); m != nil {
			e := atoi(m[1])*3600 + atoi(m[2])*60 + atoi(m[3])
			sc.EtaSeconds = &e
		}
	case strings.Contains(s, "paused"):
		sc.State = "paused"
		if m := pctRe.FindStringSubmatch(s); m != nil {
			if p, err := strconv.ParseFloat(m[1], 64); err == nil {
				sc.Percent = &p
			}
		}
	case strings.Contains(s, "canceled"):
		sc.State = "canceled"
	case strings.HasPrefix(s, "scrub repaired") || strings.HasPrefix(s, "resilvered"):
		sc.State = "finished"
		if m := errsRe.FindStringSubmatch(s); m != nil {
			e := atoi(m[1])
			sc.Errors = &e
		}
	}
	if m := onDateRe.FindStringSubmatch(s); m != nil && sc.State != "scanning" {
		sc.Finished = parseZDate(m[1])
	}
	return sc
}

// ParseStatusText reads `zpool status -P -p` (OpenZFS 2.0+ text).
func ParseStatusText(out string) []Pool {
	var pools []Pool
	for _, b := range splitBlocks(out) {
		p := Pool{Name: b.fields["pool"], Health: firstWord(b.fields["state"]), Vdevs: []Vdev{}}
		p.Scan = parseScanText(b.fields["scan"])
		if e := b.fields["errors"]; e != "" && !strings.HasPrefix(e, "No known data errors") {
			p.Errors = e
		}
		p.Status = b.fields["status"]
		if root := parseConfig(b.config); root != nil {
			p.Vdevs = root.Children
		}
		p.Layout = layoutOf(p.Vdevs)
		pools = append(pools, p)
	}
	return pools
}

func firstWord(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return ""
}

// layoutOf names the pool layout from its data vdevs.
func layoutOf(vdevs []Vdev) string {
	layout := ""
	for _, v := range vdevs {
		if _, group := groupNames[v.Type]; group {
			continue
		}
		t := v.Type
		if t == "disk" || t == "file" {
			t = "stripe"
		}
		if t == "replacing" || t == "spare" {
			continue // a single disk being swapped; doesn't change the layout
		}
		if layout == "" {
			layout = t
		} else if layout != t {
			return "mixed"
		}
	}
	if layout == "" {
		layout = "stripe"
	}
	return layout
}

// ---- OpenZFS 2.3 JSON (zpool status -j --json-int -P) ----

type jVdev struct {
	Name           flexStr          `json:"name"`
	VdevType       string           `json:"vdev_type"`
	GUID           flexStr          `json:"guid"`
	Path           string           `json:"path"`
	Was            string           `json:"was"`
	State          string           `json:"state"`
	ReadErrors     flexStr          `json:"read_errors"`
	WriteErrors    flexStr          `json:"write_errors"`
	ChecksumErrors flexStr          `json:"checksum_errors"`
	Resilvering    flexStr          `json:"resilvering"`
	Vdevs          map[string]jVdev `json:"vdevs"`
}

type jScan struct {
	Function       string  `json:"function"`
	State          string  `json:"state"`
	StartTime      flexStr `json:"start_time"`
	EndTime        flexStr `json:"end_time"`
	ToExamine      flexStr `json:"to_examine"`
	Examined       flexStr `json:"examined"`
	Issued         flexStr `json:"issued"`
	Errors         flexStr `json:"errors"`
	PassStart      flexStr `json:"pass_start"`
	ScrubPause     flexStr `json:"scrub_pause"`
	ScrubSpentPaus flexStr `json:"scrub_spent_paused"`
	PassIssued     flexStr `json:"issued_bytes_per_scan"`
}

type jPool struct {
	Name       string           `json:"name"`
	State      string           `json:"state"`
	Status     string           `json:"status"`
	ScanStats  *jScan           `json:"scan_stats"`
	Vdevs      map[string]jVdev `json:"vdevs"`
	Logs       map[string]jVdev `json:"logs"`
	L2Cache    map[string]jVdev `json:"l2cache"`
	Spares     map[string]jVdev `json:"spares"`
	Special    map[string]jVdev `json:"special"`
	Dedup      map[string]jVdev `json:"dedup"`
	ErrorCount flexStr          `json:"error_count"`
}

// natural sort so mirror-2 < mirror-10.
func natLess(a, b string) bool {
	ai, bi := strings.LastIndex(a, "-"), strings.LastIndex(b, "-")
	if ai > 0 && bi > 0 && a[:ai] == b[:bi] {
		x, e1 := strconv.Atoi(a[ai+1:])
		y, e2 := strconv.Atoi(b[bi+1:])
		if e1 == nil && e2 == nil {
			return x < y
		}
	}
	return a < b
}

func convVdevs(m map[string]jVdev) []Vdev {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return natLess(keys[i], keys[j]) })
	out := make([]Vdev, 0, len(keys))
	for _, k := range keys {
		j := m[k]
		name := string(j.Name)
		if name == "" {
			name = k
		}
		v := Vdev{Name: name, Health: j.State, ReadErrors: j.ReadErrors.int(), WriteErrors: j.WriteErrors.int(),
			ChecksumErrors: j.ChecksumErrors.int(), GUID: string(j.GUID), Was: j.Was, Children: convVdevs(j.Vdevs)}
		v.Type = vdevType(name)
		if j.VdevType == "file" {
			v.Type = "file"
		}
		if v.Type == "disk" || v.Type == "file" {
			v.Path = j.Path
			if v.Path == "" && strings.HasPrefix(name, "/") {
				v.Path = name
			}
		}
		if r := string(j.Resilvering); r == "true" || r == "1" {
			v.Note = "resilvering"
		}
		out = append(out, v)
	}
	return out
}

func convScan(j *jScan, now time.Time) Scan {
	if j == nil {
		return Scan{State: "none"}
	}
	sc := Scan{Function: strings.ToLower(j.Function)}
	switch strings.ToUpper(j.State) {
	case "SCANNING":
		sc.State = "scanning"
		if p := string(j.ScrubPause); p != "" && p != "-" && p != "0" {
			sc.State = "paused"
		}
	case "FINISHED":
		sc.State = "finished"
	case "CANCELED":
		sc.State = "canceled"
	default:
		sc.State = "none"
		sc.Function = ""
	}
	total, issued := j.ToExamine.int(), j.Issued.int()
	if sc.State == "scanning" || sc.State == "paused" {
		if total > 0 {
			p := math.Round(float64(issued)/float64(total)*10000) / 100
			sc.Percent = &p
		}
		if start := j.PassStart.int(); sc.State == "scanning" && start > 1e9 && issued > 0 && total > issued {
			elapsed := now.Unix() - start - j.ScrubSpentPaus.int()
			if elapsed > 0 {
				eta := int64(float64(total-issued) / (float64(issued) / float64(elapsed)))
				sc.EtaSeconds = &eta
			}
		}
	}
	if sc.State == "finished" {
		e := j.Errors.int()
		sc.Errors = &e
		if end := j.EndTime.int(); end > 1e9 {
			sc.Finished = time.Unix(end, 0).Format(time.RFC3339)
		} else {
			sc.Finished = parseZDate(string(j.EndTime))
		}
	}
	return sc
}

// ParseStatusJSON reads `zpool status -j --json-int -P` (OpenZFS 2.3+).
func ParseStatusJSON(out []byte, now time.Time) ([]Pool, error) {
	var v struct {
		Pools map[string]jPool `json:"pools"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, fmt.Errorf("couldn't read zpool status: %w", err)
	}
	names := make([]string, 0, len(v.Pools))
	for n := range v.Pools {
		names = append(names, n)
	}
	sort.Strings(names)
	pools := make([]Pool, 0, len(names))
	for _, n := range names {
		jp := v.Pools[n]
		p := Pool{Name: n, Health: jp.State, Status: jp.Status, Scan: convScan(jp.ScanStats, now), Vdevs: []Vdev{}}
		if jp.Name != "" {
			p.Name = jp.Name
		}
		// The pool root is the single entry of "vdevs".
		for _, root := range jp.Vdevs {
			p.Vdevs = convVdevs(root.Vdevs)
		}
		for _, g := range []struct {
			typ string
			m   map[string]jVdev
		}{{"logs", jp.Logs}, {"cache", jp.L2Cache}, {"spares", jp.Spares}, {"special", jp.Special}, {"dedup", jp.Dedup}} {
			if len(g.m) > 0 {
				p.Vdevs = append(p.Vdevs, Vdev{Name: g.typ, Type: g.typ, Children: convVdevs(g.m)})
			}
		}
		if n := jp.ErrorCount.int(); n > 0 {
			p.Errors = fmt.Sprintf("%d data errors, use 'zpool status -v' for a list", n)
		}
		p.Layout = layoutOf(p.Vdevs)
		pools = append(pools, p)
	}
	return pools, nil
}

// poolSizes is one `zpool list` row.
type poolSizes struct {
	Size, Alloc, Free, Frag, Cap int64
	Health                       string
}

// ParseListText reads `zpool list -Hp -o name,size,allocated,free,fragmentation,capacity,health`.
func ParseListText(out string) map[string]poolSizes {
	res := map[string]poolSizes{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 7 {
			continue
		}
		res[f[0]] = poolSizes{Size: atoi(f[1]), Alloc: atoi(f[2]), Free: atoi(f[3]), Frag: atoi(f[4]), Cap: atoi(f[5]), Health: f[6]}
	}
	return res
}

// ParseListJSON reads `zpool list -j --json-int` (OpenZFS 2.3+).
func ParseListJSON(out []byte) (map[string]poolSizes, error) {
	var v struct {
		Pools map[string]struct {
			Name       string `json:"name"`
			State      string `json:"state"`
			Properties map[string]struct {
				Value flexStr `json:"value"`
			} `json:"properties"`
		} `json:"pools"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, fmt.Errorf("couldn't read zpool list: %w", err)
	}
	res := map[string]poolSizes{}
	for n, p := range v.Pools {
		pr := p.Properties
		h := string(pr["health"].Value)
		if h == "" {
			h = p.State
		}
		res[n] = poolSizes{Size: pr["size"].Value.int(), Alloc: pr["allocated"].Value.int(), Free: pr["free"].Value.int(),
			Frag: pr["fragmentation"].Value.int(), Cap: pr["capacity"].Value.int(), Health: h}
	}
	return res, nil
}

// ParseImport reads `zpool import` (pools that can be imported).
func ParseImport(out string) []ImportablePool {
	var res []ImportablePool
	for _, b := range splitBlocks(out) {
		ip := ImportablePool{Name: b.fields["pool"], ID: firstWord(b.fields["id"]), Health: firstWord(b.fields["state"]), Disks: []string{}}
		if root := parseConfig(b.config); root != nil {
			var walk func(v Vdev)
			walk = func(v Vdev) {
				if (v.Type == "disk" || v.Type == "file") && len(v.Children) == 0 && !isDigits(v.Name) {
					ip.Disks = append(ip.Disks, v.Name)
				}
				for _, c := range v.Children {
					walk(c)
				}
			}
			for _, c := range root.Children {
				walk(c)
			}
		}
		res = append(res, ip)
	}
	return res
}

// ParseDatasets reads `zfs list -Hp -o name,used,avail,refer,mountpoint,compression,compressratio,quota`.
func ParseDatasets(out string) []Dataset {
	res := []Dataset{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 8 {
			continue
		}
		ratio, _ := strconv.ParseFloat(strings.TrimSuffix(f[6], "x"), 64)
		res = append(res, Dataset{Name: f[0], Used: atoi(f[1]), Available: atoi(f[2]), Referenced: atoi(f[3]),
			Mountpoint: f[4], Compression: f[5], CompressRatio: ratio, Quota: atoi(f[7])})
	}
	return res
}

// ParseSnapshots reads `zfs list -Hp -t snapshot -o name,creation,used,refer`.
func ParseSnapshots(out string) []Snapshot {
	res := []Snapshot{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 4 {
			continue
		}
		res = append(res, Snapshot{Name: f[0], Created: time.Unix(atoi(f[1]), 0).UTC(), Used: atoi(f[2]), Referenced: atoi(f[3])})
	}
	return res
}

// leaves lists the device paths/names of every leaf vdev in a pool.
func leaves(vs []Vdev) []*Vdev {
	var out []*Vdev
	for i := range vs {
		v := &vs[i]
		if len(v.Children) == 0 && (v.Type == "disk" || v.Type == "file") {
			out = append(out, v)
		}
		out = append(out, leaves(v.Children)...)
	}
	return out
}
