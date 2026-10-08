// Package hostctl controls the machine NoCapOS runs on: network radios and
// power. Linux uses NetworkManager (nmcli) and systemd; Windows supports
// power actions and reports adapters read-only.
package hostctl

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"
)

// ErrUnsupported means this host can't do that (no NetworkManager, inside a
// container, Windows radio control, …). The message says why.
var ErrUnsupported = errors.New("not supported on this host")

type Toggle struct {
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
	Reason    string `json:"reason,omitempty"` // why it isn't supported
}

type Interface struct {
	Name  string   `json:"name"`
	Kind  string   `json:"kind"` // wifi | ethernet | other
	Up    bool     `json:"up"`
	State string   `json:"state,omitempty"` // NetworkManager state, e.g. "connected"
	Conn  string   `json:"connection,omitempty"`
	MAC   string   `json:"mac,omitempty"`
	Addrs []string `json:"addresses"`
	IPv4  *IPv4    `json:"ipv4,omitempty"` // how the address is assigned (DHCP / static)
}

type NetworkState struct {
	Manager    string      `json:"manager"` // networkmanager | windows | none
	WiFi       Toggle      `json:"wifi"`
	Networking Toggle      `json:"networking"`
	Interfaces []Interface `json:"interfaces"`
}

// InContainer reports whether alfad itself runs in a container (then "host"
// controls would only affect the container).
func InContainer() bool {
	for _, p := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// runCmd runs a command and returns its trimmed output (swappable in tests).
var runCmd = run

func run(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		if s == "" {
			s = err.Error()
		}
		return s, errors.New(s)
	}
	return s, nil
}

func hasNmcli() bool {
	_, err := exec.LookPath("nmcli")
	return err == nil
}

// Network reports radio switches and interfaces.
func Network(ctx context.Context) NetworkState {
	st := NetworkState{Manager: "none", Interfaces: goInterfaces()}
	reason := "Wi-Fi and network switches need NetworkManager on a Linux host"
	switch {
	case runtime.GOOS == "windows":
		st.Manager = "windows"
		reason = "Switching adapters on Windows needs an elevated (administrator) process"
	case InContainer():
		reason = "NoCapOS is running in a container, so it can't switch the host's network"
	case hasNmcli():
		st.Manager = "networkmanager"
		if out, err := runCmd(ctx, "nmcli", "-t", "-f", "WIFI,NETWORKING", "general"); err == nil {
			f := strings.Split(out, ":")
			if len(f) >= 2 {
				st.WiFi = Toggle{Supported: true, Enabled: f[0] == "enabled"}
				st.Networking = Toggle{Supported: true, Enabled: f[1] == "enabled"}
			}
		}
		mergeNM(ctx, &st)
		fillIPv4NM(ctx, &st)
		if !hasKind(st.Interfaces, "wifi") {
			st.WiFi = Toggle{Supported: false, Enabled: false, Reason: "No Wi-Fi adapter found"}
		}
		return st
	}
	if runtime.GOOS == "windows" {
		fillIPv4Windows(ctx, &st)
	}
	st.WiFi = Toggle{Supported: false, Enabled: hasUp(st.Interfaces, "wifi"), Reason: reason}
	st.Networking = Toggle{Supported: false, Enabled: hasUp(st.Interfaces, ""), Reason: reason}
	return st
}

// SetWiFi turns the Wi-Fi radio on or off.
func SetWiFi(ctx context.Context, on bool) error {
	if err := canControl(); err != nil {
		return err
	}
	_, err := runCmd(ctx, "nmcli", "radio", "wifi", onOff(on))
	return err
}

// SetNetworking turns all networking on or off.
func SetNetworking(ctx context.Context, on bool) error {
	if err := canControl(); err != nil {
		return err
	}
	_, err := runCmd(ctx, "nmcli", "networking", onOff(on))
	return err
}

// canControl is canControlNetwork, swappable in tests.
var canControl = canControlNetwork

func canControlNetwork() error {
	if runtime.GOOS != "linux" || InContainer() || !hasNmcli() {
		return ErrUnsupported
	}
	return nil
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// mergeNM adds NetworkManager's device type/state/connection to interfaces.
func mergeNM(ctx context.Context, st *NetworkState) {
	out, err := runCmd(ctx, "nmcli", "-t", "-f", "DEVICE,TYPE,STATE,CONNECTION", "device")
	if err != nil {
		return
	}
	idx := map[string]int{}
	for i, it := range st.Interfaces {
		idx[it.Name] = i
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(line, ":", 4)
		if len(f) < 4 {
			continue
		}
		kind := "other"
		switch f[1] {
		case "wifi":
			kind = "wifi"
		case "ethernet":
			kind = "ethernet"
		case "loopback", "bridge", "tun", "veth", "wifi-p2p":
			continue
		}
		if i, ok := idx[f[0]]; ok {
			st.Interfaces[i].Kind, st.Interfaces[i].State, st.Interfaces[i].Conn = kind, f[2], f[3]
		} else {
			st.Interfaces = append(st.Interfaces, Interface{Name: f[0], Kind: kind, State: f[2], Conn: f[3], Addrs: []string{}})
		}
	}
}

// goInterfaces lists physical-looking interfaces with their addresses.
func goInterfaces() []Interface {
	ifs, err := net.Interfaces()
	if err != nil {
		return []Interface{}
	}
	out := []Interface{}
	for _, i := range ifs {
		if i.Flags&net.FlagLoopback != 0 || virtual(i.Name) {
			continue
		}
		it := Interface{Name: i.Name, Kind: guessKind(i.Name), Up: i.Flags&net.FlagUp != 0, MAC: i.HardwareAddr.String(), Addrs: []string{}}
		if addrs, err := i.Addrs(); err == nil {
			for _, a := range addrs {
				if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLinkLocalUnicast() {
					it.Addrs = append(it.Addrs, ipn.String())
				}
			}
		}
		out = append(out, it)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

func virtual(name string) bool {
	n := strings.ToLower(name)
	for _, p := range []string{"docker", "veth", "br-", "virbr", "vethernet", "lo", "tun", "tap", "podman", "cni", "flannel", "isatap", "teredo", "bluetooth", "local area connection*"} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

func guessKind(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.HasPrefix(n, "wl") || strings.Contains(n, "wi-fi") || strings.Contains(n, "wifi") || strings.Contains(n, "wireless"):
		return "wifi"
	case strings.HasPrefix(n, "en") || strings.HasPrefix(n, "eth") || strings.Contains(n, "ethernet"):
		return "ethernet"
	}
	return "other"
}

func hasKind(ifs []Interface, kind string) bool {
	for _, i := range ifs {
		if i.Kind == kind {
			return true
		}
	}
	return false
}

func hasUp(ifs []Interface, kind string) bool {
	for _, i := range ifs {
		if (kind == "" || i.Kind == kind) && i.Up && len(i.Addrs) > 0 {
			return true
		}
	}
	return false
}

// ---------- power ----------

type PowerInfo struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
}

func Power() PowerInfo {
	switch {
	case InContainer():
		return PowerInfo{Reason: "NoCapOS is running in a container; restart or shut down the host from its own console"}
	case runtime.GOOS == "windows":
		return PowerInfo{Supported: true}
	case runtime.GOOS == "linux":
		if _, err := exec.LookPath("systemctl"); err == nil {
			return PowerInfo{Supported: true}
		}
		if _, err := exec.LookPath("shutdown"); err == nil {
			return PowerInfo{Supported: true}
		}
		return PowerInfo{Reason: "Neither systemctl nor shutdown is available"}
	}
	return PowerInfo{Reason: "Power control isn't available on this platform"}
}

// Schedule restarts or shuts down the host after a short delay, so the HTTP
// response (and the user's confirmation screen) gets out first.
func Schedule(action string, delay time.Duration) error {
	if !Power().Supported {
		return ErrUnsupported
	}
	var name string
	var args []string
	switch {
	case runtime.GOOS == "windows" && action == "reboot":
		name, args = "shutdown", []string{"/r", "/t", "5", "/c", "Restart requested from NoCapOS"}
	case runtime.GOOS == "windows" && action == "shutdown":
		name, args = "shutdown", []string{"/s", "/t", "5", "/c", "Shut down requested from NoCapOS"}
	case action == "reboot":
		name, args = linuxPower("reboot")
	case action == "shutdown":
		name, args = linuxPower("poweroff")
	default:
		return errors.New("action must be reboot or shutdown")
	}
	go func() {
		time.Sleep(delay)
		_ = exec.Command(name, args...).Run()
	}()
	return nil
}

func linuxPower(verb string) (string, []string) {
	if _, err := exec.LookPath("systemctl"); err == nil {
		return "systemctl", []string{verb}
	}
	if verb == "reboot" {
		return "shutdown", []string{"-r", "now"}
	}
	return "shutdown", []string{"-h", "now"}
}
