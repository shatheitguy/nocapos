//go:build windows

package hardware

import (
	"fmt"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Native Windows telemetry for running NoCapOS as a normal desktop app:
// total and per-core CPU, memory, uptime, per-adapter network counters.

var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemTimes       = kernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetTickCount64       = kernel32.NewProc("GetTickCount64")

	ntdll                        = windows.NewLazySystemDLL("ntdll.dll")
	procNtQuerySystemInformation = ntdll.NewProc("NtQuerySystemInformation")
)

func init() {
	nativeCPUTimes = winCPUTimes
	nativeMeminfo = winMeminfo
	nativeUptime = winUptime
	nativeHostInfo = winHostInfo
	nativeNetDev = winNetDev
}

func filetime(f windows.Filetime) uint64 {
	return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime)
}

func winCPUTimes() (cpuTimes, []cpuTimes, error) {
	var idle, kernel, user windows.Filetime
	r, _, err := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if r == 0 {
		return cpuTimes{}, nil, err
	}
	// Kernel time already includes idle time.
	return cpuTimes{idle: filetime(idle), total: filetime(kernel) + filetime(user)}, winCoreTimes(), nil
}

// SYSTEM_PROCESSOR_PERFORMANCE_INFORMATION (one per logical processor).
type processorPerf struct {
	IdleTime, KernelTime, UserTime, DpcTime, InterruptTime int64
	InterruptCount                                         uint32
	_                                                      uint32
}

const systemProcessorPerformanceInformation = 8

// winCoreTimes returns per-logical-processor times, or nil if unavailable
// (the per-core view is then simply left out).
func winCoreTimes() []cpuTimes {
	n := runtime.NumCPU()
	buf := make([]processorPerf, n)
	var got uint32
	r, _, _ := procNtQuerySystemInformation.Call(systemProcessorPerformanceInformation,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(n)*unsafe.Sizeof(buf[0]), uintptr(unsafe.Pointer(&got)))
	if r != 0 { // NTSTATUS != STATUS_SUCCESS
		return nil
	}
	cores := make([]cpuTimes, 0, n)
	for _, p := range buf[:int(got)/int(unsafe.Sizeof(buf[0]))] {
		cores = append(cores, cpuTimes{idle: uint64(p.IdleTime), total: uint64(p.KernelTime + p.UserTime)})
	}
	return cores
}

// winNetDev reads byte counters for every connected physical adapter.
func winNetDev() (map[string]netCounter, error) {
	size := uint32(16 << 10)
	var buf []byte
	for i := 0; i < 3; i++ {
		buf = make([]byte, size)
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC,
			windows.GAA_FLAG_SKIP_UNICAST|windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST|windows.GAA_FLAG_SKIP_DNS_SERVER,
			0, (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])), &size)
		if err == nil {
			break
		}
		if err != windows.ERROR_BUFFER_OVERFLOW || i == 2 {
			return nil, err
		}
	}
	out := map[string]netCounter{}
	for a := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); a != nil; a = a.Next {
		if a.IfType == windows.IF_TYPE_SOFTWARE_LOOPBACK || a.OperStatus != windows.IfOperStatusUp {
			continue
		}
		name := windows.UTF16PtrToString(a.FriendlyName)
		// Hyper-V / WSL virtual switches duplicate the real adapter's traffic.
		if strings.HasPrefix(name, "vEthernet") || strings.HasPrefix(name, "Loopback") {
			continue
		}
		row := windows.MibIfRow2{InterfaceIndex: a.IfIndex}
		if windows.GetIfEntry2Ex(windows.MibIfEntryNormal, &row) != nil {
			continue
		}
		out[name] = netCounter{rx: row.InOctets, tx: row.OutOctets}
	}
	return out, nil
}

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func winMeminfo() (map[string]uint64, error) {
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	r, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return nil, err
	}
	// Swap is not reported: Windows' page-file figures are commit charge, not
	// page-file usage, and would read as a near-full swap on a healthy system.
	return map[string]uint64{"MemTotal": m.TotalPhys, "MemAvailable": m.AvailPhys}, nil
}

func winUptime() float64 {
	ms, _, _ := procGetTickCount64.Call()
	return float64(ms) / 1000
}

func winHostInfo(h *HostInfo) {
	h.CPUCores = runtime.NumCPU()
	if m, err := winMeminfo(); err == nil {
		h.MemTotal = m["MemTotal"]
	}

	v := windows.RtlGetVersion()
	h.Kernel = fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
	h.OS = "Windows"
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE); err == nil {
		if name, _, err := k.GetStringValue("ProductName"); err == nil {
			// ProductName still says "Windows 10" on Windows 11 (build 22000+).
			if v.BuildNumber >= 22000 {
				name = strings.Replace(name, "Windows 10", "Windows 11", 1)
			}
			h.OS = name
		}
		if disp, _, err := k.GetStringValue("DisplayVersion"); err == nil && disp != "" {
			h.OS += " " + disp
		}
		k.Close()
	}

	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE); err == nil {
		if name, _, err := k.GetStringValue("ProcessorNameString"); err == nil {
			h.CPUModel = strings.TrimSpace(name)
		}
		k.Close()
	}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\BIOS`, registry.QUERY_VALUE); err == nil {
		vendor, _, _ := k.GetStringValue("SystemManufacturer")
		product, _, _ := k.GetStringValue("SystemProductName")
		if b := strings.TrimSpace(vendor + " " + product); b != "" {
			h.Board = b
		}
		k.Close()
	}
}
