package hardware

import (
	"bytes"
	"context"
	"encoding/csv"
	"log/slog"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Vendors.
const (
	VendorNVIDIA      = "nvidia"
	VendorNVIDIATegra = "nvidia-tegra" // Jetson (integrated, nvgpu driver)
	VendorAMD         = "amd"
	VendorIntel       = "intel"
	VendorARM         = "arm" // Mali
	VendorBroadcom    = "broadcom"
	VendorQualcomm    = "qualcomm"
	VendorVivante     = "vivante"
	VendorImagination = "imagination"
	VendorApple       = "apple"
	VendorGoogle      = "google"
	VendorHailo       = "hailo"
	VendorRockchip    = "rockchip"
	VendorOther       = "other"
)

// Kinds.
const (
	KindGPU     = "gpu"
	KindNPU     = "npu"
	KindTPU     = "tpu"
	KindDisplay = "display" // BMC / virtual display adapter, not usable for compute
)

type GPUMetrics struct {
	Utilization *float64 `json:"utilization,omitempty"` // percent
	MemUsed     *uint64  `json:"mem_used,omitempty"`    // bytes
	MemTotal    *uint64  `json:"mem_total,omitempty"`
	TempC       *float64 `json:"temp_c,omitempty"`
	PowerW      *float64 `json:"power_w,omitempty"`
	FreqMHz     *float64 `json:"freq_mhz,omitempty"`
}

// Passthrough describes how to hand an accelerator to a container. It is
// consumed by the app/agent launcher.
type Passthrough struct {
	Devices    []string       `json:"devices,omitempty"`     // host device nodes to map
	GPURequest *DeviceRequest `json:"gpu_request,omitempty"` // HostConfig.DeviceRequests entry
	Runtime    string         `json:"runtime,omitempty"`     // OCI runtime override
	Groups     []string       `json:"groups,omitempty"`      // supplementary groups for device access
}

type DeviceRequest struct {
	Driver       string     `json:"Driver"`
	DeviceIDs    []string   `json:"DeviceIDs,omitempty"`
	Capabilities [][]string `json:"Capabilities"`
}

// GPU describes any compute accelerator: discrete/integrated GPUs, SoC GPUs,
// NPUs and TPUs.
type GPU struct {
	ID          string      `json:"id"`
	Kind        string      `json:"kind"`
	Vendor      string      `json:"vendor"`
	Name        string      `json:"name"`
	Driver      string      `json:"driver,omitempty"`
	PCIAddress  string      `json:"pci_address,omitempty"`
	DeviceNodes []string    `json:"device_nodes,omitempty"`
	Note        string      `json:"note,omitempty"`
	Access      Passthrough `json:"access"`
	Metrics     GPUMetrics  `json:"metrics"`

	sysPath     string // sysfs device directory
	drmPath     string // sysfs drm/cardN directory
	devfreqPath string // sysfs devfreq directory
	nvIndex     int    // nvidia-smi / CUDA index
}

type GPUProbe struct {
	sys       string
	log       *slog.Logger
	nvidiaSMI string
	devices   []*GPU // immutable after discovery
}

// DiscoverGPUs scans sysfs once. Hot-plug of accelerators is rare enough that a
// daemon restart is an acceptable way to pick up new hardware.
func DiscoverGPUs(sys string, log *slog.Logger) *GPUProbe {
	p := &GPUProbe{sys: sys, log: log}
	p.nvidiaSMI, _ = exec.LookPath("nvidia-smi")
	p.discoverPCI()
	p.discoverSoC()
	p.discoverDevfreq()
	p.discoverAccelerators()
	p.discoverSMIOnly()
	p.finalize()
	for _, g := range p.devices {
		log.Info("accelerator detected", "id", g.ID, "kind", g.Kind, "vendor", g.Vendor, "name", g.Name, "driver", g.Driver)
	}
	return p
}

// Devices returns static descriptions without live metrics.
func (p *GPUProbe) Devices() []GPU {
	out := make([]GPU, len(p.devices))
	for i, g := range p.devices {
		out[i] = *g
	}
	return out
}

// Sample returns every device with current metrics.
func (p *GPUProbe) Sample(ctx context.Context) []GPU {
	var smi map[string]smiRow
	if p.nvidiaSMI != "" && p.has(VendorNVIDIA) {
		smi = p.querySMI(ctx)
	}
	out := make([]GPU, len(p.devices))
	for i, d := range p.devices {
		g := *d
		switch g.Vendor {
		case VendorNVIDIA:
			if r, ok := smi[busKey(g.PCIAddress)]; ok {
				g.Metrics = r.metrics
			}
		case VendorAMD:
			g.Metrics = amdMetrics(g.sysPath)
		case VendorIntel:
			if g.Kind == KindGPU {
				g.Metrics = intelMetrics(g.sysPath, g.drmPath)
			}
		}
		if g.devfreqPath != "" {
			mergeDevfreq(&g.Metrics, g.devfreqPath)
		}
		out[i] = g
	}
	return out
}

func (p *GPUProbe) has(vendor string) bool {
	for _, g := range p.devices {
		if g.Vendor == vendor {
			return true
		}
	}
	return false
}

func (p *GPUProbe) bySysPath(path string) *GPU {
	for _, g := range p.devices {
		if g.sysPath != "" && g.sysPath == path {
			return g
		}
	}
	return nil
}

// ---------- discovery: PCI (x86 dGPU/iGPU, ARM servers with PCIe GPUs) ----------

var pciVendors = map[string]string{
	"0x10de": VendorNVIDIA,
	"0x1002": VendorAMD,
	"0x8086": VendorIntel,
}

// Display adapters that exist on servers/VMs but are useless for compute.
var displayOnlyVendors = map[string]string{
	"0x1a03": "ASPEED BMC graphics",
	"0x102b": "Matrox graphics",
	"0x1af4": "Virtio GPU",
	"0x15ad": "VMware SVGA",
	"0x1234": "QEMU VGA",
	"0x1414": "Hyper-V video",
}

func (p *GPUProbe) discoverPCI() {
	devs, _ := filepath.Glob(filepath.Join(p.sys, "bus", "pci", "devices", "*"))
	nvIndex := 0 // CUDA/nvidia-smi enumerate in PCI bus order, which Glob preserves
	for _, dev := range devs {
		class, _ := readTrimmed(filepath.Join(dev, "class"))
		if !strings.HasPrefix(class, "0x03") { // 0x0300 VGA, 0x0302 3D, 0x0380 other display
			continue
		}
		vendorID, _ := readTrimmed(filepath.Join(dev, "vendor"))
		deviceID, _ := readTrimmed(filepath.Join(dev, "device"))
		addr := filepath.Base(dev)
		g := &GPU{
			ID:         "pci-" + addr,
			Kind:       KindGPU,
			PCIAddress: addr,
			Driver:     linkBase(filepath.Join(dev, "driver")),
			sysPath:    dev,
		}
		if v, ok := pciVendors[vendorID]; ok {
			g.Vendor = v
		} else {
			g.Vendor = VendorOther
			g.Kind = KindDisplay
		}
		var card string
		g.DeviceNodes, card = drmNodes(filepath.Join(dev, "drm"))
		if card != "" {
			g.drmPath = filepath.Join(dev, "drm", card)
		}

		devID := strings.TrimPrefix(deviceID, "0x")
		switch g.Vendor {
		case VendorNVIDIA:
			g.Name = "NVIDIA GPU [" + devID + "]"
			g.nvIndex = nvIndex
			nvIndex++
			if p.nvidiaSMI == "" {
				g.Note = "Live metrics need the NVIDIA Container Toolkit: start with deploy/docker-compose.nvidia.yml"
			}
		case VendorAMD:
			name, _ := readTrimmed(filepath.Join(g.sysPath, "product_name"))
			g.Name = nonEmpty(name, "AMD Radeon ["+devID+"]")
		case VendorIntel:
			g.Name = "Intel Graphics [" + devID + "]"
			g.Note = "Utilization requires i915/xe perf counters; frequency is reported instead"
		default:
			g.Name = nonEmpty(displayOnlyVendors[vendorID], "Display adapter "+strings.TrimPrefix(vendorID, "0x")+":"+devID)
		}
		p.devices = append(p.devices, g)
	}

	// Refine NVIDIA names once via nvidia-smi when available.
	if p.nvidiaSMI != "" && p.has(VendorNVIDIA) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		rows := p.querySMI(ctx)
		for _, g := range p.devices {
			if r, ok := rows[busKey(g.PCIAddress)]; ok && g.Vendor == VendorNVIDIA {
				g.Name = r.name
				g.nvIndex = r.index
			}
		}
	}
}

var cardRe = regexp.MustCompile(`^card\d+$`)
var renderRe = regexp.MustCompile(`^renderD\d+$`)

// drmNodes lists /dev/dri nodes for a device's sysfs drm directory.
func drmNodes(drmDir string) (nodes []string, card string) {
	entries, _ := filepath.Glob(filepath.Join(drmDir, "*"))
	for _, e := range entries {
		name := filepath.Base(e)
		switch {
		case cardRe.MatchString(name):
			card = name
			nodes = append(nodes, "/dev/dri/"+name)
		case renderRe.MatchString(name):
			nodes = append(nodes, "/dev/dri/"+name)
		}
	}
	return nodes, card
}

// ---------- discovery: SoC GPUs via DRM (Raspberry Pi, Rockchip, Amlogic, Snapdragon, ...) ----------

// Only render-capable drivers; display controllers (vc4, rockchip-drm,
// sun4i-drm, meson, ...) are skipped.
var socGPUDrivers = map[string]struct{ vendor, name string }{
	"v3d":      {VendorBroadcom, "Broadcom VideoCore (V3D)"},
	"panfrost": {VendorARM, "Arm Mali (Panfrost)"},
	"panthor":  {VendorARM, "Arm Mali (Panthor)"},
	"lima":     {VendorARM, "Arm Mali Utgard (Lima)"},
	"msm":      {VendorQualcomm, "Qualcomm Adreno"},
	"etnaviv":  {VendorVivante, "Vivante GPU"},
	"powervr":  {VendorImagination, "Imagination PowerVR"},
	"asahi":    {VendorApple, "Apple AGX"},
	"nouveau":  {VendorNVIDIATegra, "NVIDIA Tegra GPU (nouveau)"},
}

func (p *GPUProbe) discoverSoC() {
	cards, _ := filepath.Glob(filepath.Join(p.sys, "class", "drm", "card*"))
	for _, card := range cards {
		if !cardRe.MatchString(filepath.Base(card)) { // skip connectors (card0-HDMI-A-1)
			continue
		}
		real, err := filepath.EvalSymlinks(filepath.Join(card, "device"))
		if err != nil || strings.Contains(real, "/pci") || p.bySysPath(real) != nil {
			continue
		}
		driver := linkBase(filepath.Join(real, "driver"))
		info, ok := socGPUDrivers[driver]
		if !ok {
			continue
		}
		g := &GPU{
			ID:      "soc-" + filepath.Base(real),
			Kind:    KindGPU,
			Vendor:  info.vendor,
			Name:    info.name,
			Driver:  driver,
			sysPath: real,
		}
		g.DeviceNodes, _ = drmNodes(filepath.Join(real, "drm"))
		p.devices = append(p.devices, g)
	}
}

// ---------- discovery: devfreq (Mali vendor kernels, Jetson, SoC NPUs) ----------

var tegraGPUNames = []string{".ga10b", ".gv11b", ".gp10b", ".gm20b", ".gk20a"}

func (p *GPUProbe) discoverDevfreq() {
	entries, _ := filepath.Glob(filepath.Join(p.sys, "class", "devfreq", "*"))
	for _, e := range entries {
		base := filepath.Base(e)
		name := strings.ToLower(base)
		tegra := false
		for _, t := range tegraGPUNames {
			if strings.HasSuffix(name, t) {
				tegra = true
			}
		}
		kind := ""
		switch {
		case strings.Contains(name, "npu"):
			kind = KindNPU
		case tegra || strings.Contains(name, "gpu") || strings.Contains(name, "mali"):
			kind = KindGPU
		default:
			continue
		}
		real, _ := filepath.EvalSymlinks(filepath.Join(e, "device"))
		if g := p.bySysPath(real); g != nil { // same device already found via DRM
			g.devfreqPath = e
			continue
		}
		g := &GPU{
			ID:          "devfreq-" + base,
			Kind:        kind,
			Driver:      linkBase(filepath.Join(e, "device", "driver")),
			sysPath:     real,
			devfreqPath: e,
		}
		switch {
		case tegra:
			g.Vendor, g.Name = VendorNVIDIATegra, "NVIDIA Jetson GPU"
			g.Note = "Use the NVIDIA container runtime (JetPack) for CUDA access"
		case kind == KindNPU:
			g.Vendor, g.Name = VendorRockchip, "SoC NPU ("+base+")"
			// Older BSP kernels register rknpu as a misc device; newer ones use DRM.
			if fileExists(filepath.Join(p.sys, "class", "misc", "rknpu")) {
				g.DeviceNodes = []string{"/dev/rknpu"}
			} else {
				g.DeviceNodes, _ = drmNodes(filepath.Join(real, "drm"))
			}
		default:
			g.Vendor, g.Name = VendorARM, "Arm Mali GPU ("+base+")"
			// Vendor (BSP) Mali drivers expose /dev/mali0 instead of a DRM node.
			if fileExists(filepath.Join(p.sys, "class", "misc", "mali0")) {
				g.DeviceNodes = []string{"/dev/mali0"}
			} else {
				g.DeviceNodes, _ = drmNodes(filepath.Join(real, "drm"))
			}
		}
		p.devices = append(p.devices, g)
	}
}

// ---------- discovery: dedicated AI accelerators ----------

var accelDrivers = map[string]struct{ vendor, name string }{
	"intel_vpu":  {VendorIntel, "Intel NPU"},
	"amdxdna":    {VendorAMD, "AMD XDNA NPU"},
	"habanalabs": {VendorIntel, "Intel Gaudi"},
	"qaic":       {VendorQualcomm, "Qualcomm Cloud AI"},
	"rocket":     {VendorRockchip, "Rockchip NPU"},
	"ethosu":     {VendorARM, "Arm Ethos-U NPU"},
}

func (p *GPUProbe) discoverAccelerators() {
	// DRM accel subsystem (/dev/accel/accelN).
	accels, _ := filepath.Glob(filepath.Join(p.sys, "class", "accel", "accel*"))
	for _, a := range accels {
		base := filepath.Base(a)
		driver := linkBase(filepath.Join(a, "device", "driver"))
		info, ok := accelDrivers[driver]
		if !ok {
			info = struct{ vendor, name string }{VendorOther, "AI accelerator (" + nonEmpty(driver, base) + ")"}
		}
		p.devices = append(p.devices, &GPU{
			ID: "accel-" + base, Kind: KindNPU, Vendor: info.vendor, Name: info.name, Driver: driver,
			DeviceNodes: []string{"/dev/accel/" + base},
		})
	}

	// Google Coral Edge TPU, PCIe / M.2 (gasket-apex driver).
	apex, _ := filepath.Glob(filepath.Join(p.sys, "class", "apex", "apex_*"))
	for _, a := range apex {
		base := filepath.Base(a)
		p.devices = append(p.devices, &GPU{
			ID: "apex-" + base, Kind: KindTPU, Vendor: VendorGoogle, Name: "Google Coral Edge TPU (PCIe)", Driver: "apex",
			DeviceNodes: []string{"/dev/" + base},
		})
	}

	// Hailo-8 / 8L (e.g. Raspberry Pi AI Kit).
	hailo, _ := filepath.Glob(filepath.Join(p.sys, "class", "hailo_chardev", "hailo*"))
	for _, h := range hailo {
		base := filepath.Base(h)
		p.devices = append(p.devices, &GPU{
			ID: "hailo-" + base, Kind: KindNPU, Vendor: VendorHailo, Name: "Hailo AI accelerator", Driver: "hailo_pci",
			DeviceNodes: []string{"/dev/" + base},
		})
	}

	// Google Coral USB (1a6e:089a before firmware load, 18d1:9302 after).
	usb, _ := filepath.Glob(filepath.Join(p.sys, "bus", "usb", "devices", "*"))
	for _, u := range usb {
		v, _ := readTrimmed(filepath.Join(u, "idVendor"))
		pid, _ := readTrimmed(filepath.Join(u, "idProduct"))
		if (v == "1a6e" && pid == "089a") || (v == "18d1" && pid == "9302") {
			p.devices = append(p.devices, &GPU{
				ID: "usb-" + filepath.Base(u), Kind: KindTPU, Vendor: VendorGoogle, Name: "Google Coral Edge TPU (USB)", Driver: "usb",
				DeviceNodes: []string{"/dev/bus/usb"},
			})
		}
	}
}

// discoverSMIOnly finds NVIDIA GPUs through nvidia-smi when sysfs is not
// available (native Windows runs, or Linux without the /sys mount).
func (p *GPUProbe) discoverSMIOnly() {
	if p.nvidiaSMI == "" || p.has(VendorNVIDIA) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows := p.querySMI(ctx)
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return rows[keys[i]].index < rows[keys[j]].index })
	for _, k := range keys {
		r := rows[k]
		p.devices = append(p.devices, &GPU{
			ID:         "nvidia-" + strconv.Itoa(r.index),
			Kind:       KindGPU,
			Vendor:     VendorNVIDIA,
			Name:       r.name,
			Driver:     "nvidia",
			PCIAddress: "0000:" + k,
			nvIndex:    r.index,
		})
	}
}

// finalize computes container passthrough specs.
func (p *GPUProbe) finalize() {
	for _, g := range p.devices {
		g.Access = passthrough(g)
	}
}

func passthrough(g *GPU) Passthrough {
	switch {
	case g.Kind == KindDisplay:
		return Passthrough{}
	case g.Vendor == VendorNVIDIA:
		return Passthrough{GPURequest: &DeviceRequest{
			Driver:       "nvidia",
			DeviceIDs:    []string{strconv.Itoa(g.nvIndex)},
			Capabilities: [][]string{{"gpu", "compute", "utility"}},
		}}
	case g.Vendor == VendorNVIDIATegra && g.Driver != "nouveau":
		return Passthrough{Runtime: "nvidia"}
	case g.Vendor == VendorAMD && g.Kind == KindGPU:
		// ROCm needs the KFD compute node in addition to the render node.
		return Passthrough{Devices: append([]string{"/dev/kfd"}, g.DeviceNodes...), Groups: []string{"video", "render"}}
	case len(g.DeviceNodes) > 0 && strings.HasPrefix(g.DeviceNodes[0], "/dev/dri/"):
		return Passthrough{Devices: g.DeviceNodes, Groups: []string{"video", "render"}}
	default:
		return Passthrough{Devices: g.DeviceNodes}
	}
}

// ---------- metrics ----------

type smiRow struct {
	index   int
	name    string
	metrics GPUMetrics
}

func (p *GPUProbe) querySMI(ctx context.Context) map[string]smiRow {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, p.nvidiaSMI,
		"--query-gpu=index,pci.bus_id,name,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw,clocks.sm",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		p.log.Debug("nvidia-smi failed", "err", err)
		return nil
	}
	r := csv.NewReader(bytes.NewReader(out))
	r.TrimLeadingSpace = true
	recs, err := r.ReadAll()
	if err != nil {
		p.log.Debug("nvidia-smi output unparsable", "err", err)
		return nil
	}
	rows := make(map[string]smiRow, len(recs))
	for _, f := range recs {
		if len(f) < 9 {
			continue
		}
		idx, _ := strconv.Atoi(strings.TrimSpace(f[0]))
		rows[busKey(f[1])] = smiRow{
			index: idx,
			name:  strings.TrimSpace(f[2]),
			metrics: GPUMetrics{
				Utilization: optFloat(f[3], 1),
				MemUsed:     optUint(f[4], 1<<20),
				MemTotal:    optUint(f[5], 1<<20),
				TempC:       optFloat(f[6], 1),
				PowerW:      optFloat(f[7], 1),
				FreqMHz:     optFloat(f[8], 1),
			},
		}
	}
	return rows
}

// busKey normalizes PCI addresses: nvidia-smi uses an 8-digit domain
// ("00000000:01:00.0"), sysfs a 4-digit one ("0000:01:00.0").
func busKey(addr string) string {
	s := strings.ToLower(strings.TrimSpace(addr))
	if strings.Count(s, ":") == 2 {
		_, s, _ = strings.Cut(s, ":")
	}
	return s
}

func amdMetrics(dev string) GPUMetrics {
	m := GPUMetrics{
		Utilization: readOptFloat(filepath.Join(dev, "gpu_busy_percent"), 1),
		MemUsed:     readOptUint(filepath.Join(dev, "mem_info_vram_used")),
		MemTotal:    readOptUint(filepath.Join(dev, "mem_info_vram_total")),
	}
	if hw := firstGlob(filepath.Join(dev, "hwmon", "hwmon*")); hw != "" {
		m.TempC = readOptFloat(filepath.Join(hw, "temp1_input"), 0.001)
		m.PowerW = readOptFloat(filepath.Join(hw, "power1_average"), 1e-6)
		if m.PowerW == nil {
			m.PowerW = readOptFloat(filepath.Join(hw, "power1_input"), 1e-6)
		}
		m.FreqMHz = readOptFloat(filepath.Join(hw, "freq1_input"), 1e-6)
	}
	return m
}

func intelMetrics(dev, drm string) GPUMetrics {
	var m GPUMetrics
	if drm != "" { // i915
		m.FreqMHz = readOptFloat(filepath.Join(drm, "gt_act_freq_mhz"), 1)
		if m.FreqMHz == nil {
			m.FreqMHz = readOptFloat(filepath.Join(drm, "gt_cur_freq_mhz"), 1)
		}
	}
	if m.FreqMHz == nil { // xe
		m.FreqMHz = readOptFloat(filepath.Join(dev, "tile0", "gt0", "freq0", "act_freq"), 1)
	}
	if hw := firstGlob(filepath.Join(dev, "hwmon", "hwmon*")); hw != "" { // Arc dGPUs
		m.TempC = readOptFloat(filepath.Join(hw, "temp1_input"), 0.001)
	}
	return m
}

func mergeDevfreq(m *GPUMetrics, path string) {
	if m.FreqMHz == nil {
		m.FreqMHz = readOptFloat(filepath.Join(path, "cur_freq"), 1e-6)
	}
	if m.Utilization != nil {
		return
	}
	// Jetson: load in tenths of a percent on the GPU device.
	if v := readOptFloat(filepath.Join(path, "device", "load"), 0.1); v != nil {
		m.Utilization = v
		return
	}
	// Rockchip BSP: "37@800000000Hz".
	if raw, err := readTrimmed(filepath.Join(path, "load")); err == nil {
		if pct, _, ok := strings.Cut(raw, "@"); ok {
			m.Utilization = optFloat(pct, 1)
		}
	}
}

// ---------- parsing helpers ----------

func optFloat(s string, scale float64) *float64 {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "[") || strings.EqualFold(s, "N/A") {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	v *= scale
	return &v
}

func optUint(s string, scale uint64) *uint64 {
	f := optFloat(s, 1)
	if f == nil || *f < 0 {
		return nil
	}
	v := uint64(*f) * scale
	return &v
}

func readOptFloat(path string, scale float64) *float64 {
	s, err := readTrimmed(path)
	if err != nil {
		return nil
	}
	return optFloat(s, scale)
}

func readOptUint(path string) *uint64 {
	s, err := readTrimmed(path)
	if err != nil {
		return nil
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return nil
	}
	return &v
}

func firstGlob(pattern string) string {
	m, _ := filepath.Glob(pattern)
	if len(m) == 0 {
		return ""
	}
	return m[0]
}
