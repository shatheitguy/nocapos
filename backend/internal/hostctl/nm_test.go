package hostctl

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeNM answers nmcli/netsh invocations from a table and records calls.
type fakeNM struct {
	answers map[string]string // joined args prefix → output
	calls   []string
}

func (f *fakeNM) run(_ context.Context, name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	for prefix, out := range f.answers {
		if strings.HasPrefix(call, prefix) {
			return out, nil
		}
	}
	return "", nil
}

func withFake(t *testing.T, f *fakeNM) {
	t.Helper()
	oldRun, oldCan := runCmd, canControl
	runCmd = f.run
	canControl = func() error { return nil }
	t.Cleanup(func() { runCmd, canControl = oldRun, oldCan })
}

func TestSplitTerse(t *testing.T) {
	got := splitTerse(`*:Home\:5G:80:WPA2:AA\:BB\:CC`)
	want := []string{"*", "Home:5G", "80", "WPA2", "AA:BB:CC"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("splitTerse = %q, want %q", got, want)
	}
}

func TestWiFiNetworks(t *testing.T) {
	f := &fakeNM{answers: map[string]string{
		"nmcli -t -f IN-USE,SSID,SIGNAL,SECURITY device wifi list": strings.Join([]string{
			` :Cafe Free:40:--`,
			`*:Office:72:WPA2`,
			` :Office:90:WPA2`, // second access point of the network we're on
			` ::30:WPA2`,       // hidden network
			` :Home\:5G:64:WPA1 WPA2`,
		}, "\n"),
		"nmcli -t -f NAME,TYPE connection show": "Office:802-11-wireless\nWired connection 1:802-3-ethernet",
	}}
	withFake(t, f)
	list, err := WiFiNetworks(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("want 3 networks, got %+v", list)
	}
	if list[0].SSID != "Office" || !list[0].InUse || !list[0].Saved || !list[0].Secure {
		t.Fatalf("connected network should come first and be saved/secure: %+v", list[0])
	}
	if list[1].SSID != "Home:5G" || list[2].SSID != "Cafe Free" || list[2].Secure {
		t.Fatalf("order/security wrong: %+v", list)
	}
}

func TestWiFiConnectArgs(t *testing.T) {
	// wifiConnectArgs only builds the quick-connect form, for an open network
	// or rejoining one NetworkManager already has secrets for; a new password
	// goes through writeWifiProfile + connection reload/up instead.
	cases := []struct {
		j    WiFiJoin
		want []string
	}{
		{WiFiJoin{SSID: "Cafe Free"}, []string{"--wait", "45", "device", "wifi", "connect", "Cafe Free"}},
		{WiFiJoin{SSID: "Office", Device: "wlan0"}, []string{"--wait", "45", "device", "wifi", "connect", "Office", "ifname", "wlan0"}},
		// Spaces and quotes stay inside one argument: no shell, no escaping.
		{WiFiJoin{SSID: `Joe's "Fast" Wi-Fi`}, []string{"--wait", "45", "device", "wifi", "connect", `Joe's "Fast" Wi-Fi`}},
		{WiFiJoin{SSID: "Hidden Lab", Hidden: true, Device: "wlp2s0"},
			[]string{"--wait", "45", "device", "wifi", "connect", "Hidden Lab", "ifname", "wlp2s0", "hidden", "yes"}},
	}
	for _, c := range cases {
		if got := wifiConnectArgs(c.j); strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%+v:\n got %q\nwant %q", c.j, got, c.want)
		}
	}
	for _, bad := range []WiFiJoin{{}, {SSID: strings.Repeat("x", 33)}, {SSID: "a\nb"}, {SSID: "ok", Password: "x\ny"}, {SSID: "ok", Device: "wl an0"}} {
		if bad.Validate() == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

// The password must never show up as a command argument, where `ps` or
// /proc/<pid>/cmdline would expose it to any other local user — it only
// ever lives in the NetworkManager keyfile this writes.
func TestWiFiConnectKeepsPasswordOffCommandLine(t *testing.T) {
	nmConnectionsDir = t.TempDir()
	t.Cleanup(func() { nmConnectionsDir = "/etc/NetworkManager/system-connections" })

	s := &scriptNM{out: map[string]string{
		"nmcli -t -f DEVICE,TYPE device":             "wlan0:wifi",
		"nmcli -t -f NAME,UUID,TYPE connection show": "",
	}}
	old, oldCan := runCmd, canControl
	runCmd, canControl = s.run, func() error { return nil }
	t.Cleanup(func() { runCmd, canControl = old, oldCan })

	const secret = "s3cret-pass-off-argv"
	if err := WiFiConnect(context.Background(), WiFiJoin{SSID: "Office", Password: secret}); err != nil {
		t.Fatal(err)
	}
	for _, call := range s.calls {
		if strings.Contains(call, secret) {
			t.Fatalf("password appeared in a command line: %q", call)
		}
	}
	joined := strings.Join(s.calls, "\n")
	if !strings.Contains(joined, "nmcli connection reload "+nmConnectionsDir) {
		t.Fatalf("expected a connection reload of the written profile:\n%s", joined)
	}
	if !strings.Contains(joined, "nmcli --wait 45 connection up id Office") {
		t.Fatalf("expected connection up by id:\n%s", joined)
	}

	// On success the profile is kept (so NetworkManager can reconnect on its
	// own later), and it carries the password, never in a command line.
	entries, err := os.ReadDir(nmConnectionsDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one saved profile, got %v (%v)", entries, err)
	}
	data, err := os.ReadFile(nmConnectionsDir + "/" + entries[0].Name())
	if err != nil || !strings.Contains(string(data), secret) {
		t.Fatalf("saved profile should hold the password: %v (%v)", string(data), err)
	}
}

// A failed join (e.g. a wrong password) must not leave the profile behind.
func TestWiFiConnectCleansUpProfileOnFailure(t *testing.T) {
	nmConnectionsDir = t.TempDir()
	t.Cleanup(func() { nmConnectionsDir = "/etc/NetworkManager/system-connections" })

	s := &scriptNM{
		out:  map[string]string{"nmcli -t -f DEVICE,TYPE device": "wlan0:wifi", "nmcli -t -f NAME,UUID,TYPE connection show": ""},
		fail: map[string]string{"nmcli --wait": "Error: Connection activation failed: (7) Secrets were required, but not provided."},
	}
	old, oldCan := runCmd, canControl
	runCmd, canControl = s.run, func() error { return nil }
	t.Cleanup(func() { runCmd, canControl = old, oldCan })

	if err := WiFiConnect(context.Background(), WiFiJoin{SSID: "Office", Password: "typo-pass"}); err == nil {
		t.Fatal("expected an error")
	}
	entries, err := os.ReadDir(nmConnectionsDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("profile from a failed join should be removed, got %v", entries)
	}
}

// scriptNM answers by prefix and can fail a call.
type scriptNM struct {
	calls []string
	out   map[string]string
	fail  map[string]string // prefix → error output
}

func (s *scriptNM) run(_ context.Context, name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	s.calls = append(s.calls, call)
	for p, out := range s.fail {
		if strings.HasPrefix(call, p) {
			return out, errors.New(out)
		}
	}
	for p, out := range s.out {
		if strings.HasPrefix(call, p) {
			return out, nil
		}
	}
	return "", nil
}

func TestWiFiConnectReplacesStaleProfile(t *testing.T) {
	nmConnectionsDir = t.TempDir()
	t.Cleanup(func() { nmConnectionsDir = "/etc/NetworkManager/system-connections" })

	s := &scriptNM{out: map[string]string{
		"nmcli -t -f DEVICE,TYPE device":             "wlan0:wifi\neth0:ethernet",
		"nmcli -t -f NAME,UUID,TYPE connection show": "Office:u-1:802-11-wireless\nOffice 1:u-2:802-11-wireless\nOffice Guest:u-3:802-11-wireless\nOffice:u-4:802-3-ethernet",
	}}
	old, oldCan := runCmd, canControl
	runCmd, canControl = s.run, func() error { return nil }
	t.Cleanup(func() { runCmd, canControl = old, oldCan })

	if err := WiFiConnect(context.Background(), WiFiJoin{SSID: "Office", Password: "new-pass-1"}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(s.calls, "\n")
	for _, want := range []string{"nmcli connection delete uuid u-1", "nmcli connection delete uuid u-2",
		"nmcli connection reload " + nmConnectionsDir, "nmcli --wait 45 connection up id Office"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "new-pass-1") {
		t.Errorf("password appeared in a command line:\n%s", joined)
	}
	if strings.Contains(joined, "u-3") || strings.Contains(joined, "u-4") {
		t.Errorf("deleted another network's profile:\n%s", joined)
	}

	// A wrong password: a plain "password's not it" (no raw nmcli dump),
	// and the profile it made is cleaned up.
	s.calls = nil
	s.fail = map[string]string{"nmcli --wait": "Error: Connection activation failed: (7) Secrets were required, but not provided."}
	err := WiFiConnect(context.Background(), WiFiJoin{SSID: "Office", Password: "typo-pass"})
	if err == nil || !strings.Contains(err.Error(), "password's not it") {
		t.Fatalf("error = %v", err)
	}
	if n := strings.Count(strings.Join(s.calls, "\n"), "connection delete uuid u-1"); n != 2 {
		t.Errorf("stale/new profile deletes = %d:\n%s", n, strings.Join(s.calls, "\n"))
	}

	// A saved network without a new password is joined as is.
	s.calls, s.fail = nil, nil
	if err := WiFiConnect(context.Background(), WiFiJoin{SSID: "Office"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(s.calls, "\n"), "delete") {
		t.Errorf("deleted a saved profile without a new password: %v", s.calls)
	}
}

func TestWiFiErrorMessages(t *testing.T) {
	j := WiFiJoin{SSID: "Home", Password: "x"}
	for out, want := range map[string]string{
		"Error: No network with SSID 'Home' found.":                                        "out of range",
		"Error: Connection activation failed: IP configuration could not be reserved":      "address",
		"Error: Timeout expired (45 seconds)":                                              "taking forever",
		"Error: Failed to add/activate new connection: weird thing":                        "couldn't join Home: Failed to add/activate",
		"Error: Connection activation failed: (7) Secrets were required, but not provided": "password's not it",
	} {
		if got := wifiError(j, out, errors.New("exit status 4")).Error(); !strings.Contains(got, want) {
			t.Errorf("%q → %q, want %q", out, got, want)
		}
	}
}

func TestValidHostname(t *testing.T) {
	for in, want := range map[string]string{"nas": "nas", "Living-Room-NAS": "living-room-nas", " box1 ": "box1", "nas.home.arpa": "nas.home.arpa"} {
		if got, err := ValidHostname(in); err != nil || got != want {
			t.Errorf("%q → %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "-nas", "nas-", "my_nas", "nas box", "nas..lan", strings.Repeat("a", 64), "123", "héllo", "a;rm -rf"} {
		if _, err := ValidHostname(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestHostsWithName(t *testing.T) {
	deb := "127.0.0.1\tlocalhost\n127.0.1.1\told-name\n\n# IPv6\n::1 localhost ip6-localhost\n"
	if got := hostsWithName(deb, "nas"); got != "127.0.0.1\tlocalhost\n127.0.1.1\tnas\n\n# IPv6\n::1 localhost ip6-localhost\n" {
		t.Errorf("replace:\n%s", got)
	}
	if got := hostsWithName("127.0.0.1 localhost\n::1 localhost\n", "nas.lan"); got != "127.0.0.1 localhost\n127.0.1.1\tnas.lan nas\n::1 localhost\n" {
		t.Errorf("add:\n%s", got)
	}
	if got := hostsWithName("", "nas"); got != "127.0.1.1\tnas\n" {
		t.Errorf("empty:\n%q", got)
	}
}

func TestSetHostname(t *testing.T) {
	dir := t.TempDir()
	oldHosts, oldName, oldLook, oldCan, oldRun := hostsFile, hostnameFile, lookPath, canRename, runCmd
	t.Cleanup(func() {
		hostsFile, hostnameFile, lookPath, canRename, runCmd = oldHosts, oldName, oldLook, oldCan, oldRun
	})
	hostsFile, hostnameFile = dir+"/hosts", dir+"/hostname"
	_ = os.WriteFile(hostsFile, []byte("127.0.0.1 localhost\n127.0.1.1 old\n"), 0o644)
	canRename = func() error { return nil }
	s := &scriptNM{fail: map[string]string{"hostnamectl": "Failed to connect to bus"}}
	runCmd = s.run
	lookPath = func(n string) (string, error) {
		if n == "hostnamectl" || n == "avahi-daemon" {
			return "/usr/bin/" + n, nil
		}
		return "", errors.New("not found")
	}
	got, err := SetHostname(context.Background(), "Media-Box")
	if err != nil || got != "media-box" {
		t.Fatalf("SetHostname = %q, %v", got, err)
	}
	b, _ := os.ReadFile(hostnameFile)
	h, _ := os.ReadFile(hostsFile)
	if string(b) != "media-box\n" || !strings.Contains(string(h), "127.0.1.1\tmedia-box") {
		t.Errorf("files: hostname=%q hosts=%q", b, h)
	}
	joined := strings.Join(s.calls, "\n")
	for _, want := range []string{"hostnamectl set-hostname media-box", "\nhostname media-box", "systemctl try-restart avahi-daemon"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q:\n%s", want, joined)
		}
	}
	if _, err := SetHostname(context.Background(), "bad name"); err == nil {
		t.Error("invalid name accepted")
	}
	canRename = func() error { return ErrUnsupported }
	if _, err := SetHostname(context.Background(), "ok"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("unsupported host: %v", err)
	}
}

func TestFillIPv4NM(t *testing.T) {
	f := &fakeNM{answers: map[string]string{
		"nmcli -t -f IP4.ADDRESS,IP4.GATEWAY,IP4.DNS device show eth0": "IP4.ADDRESS[1]:192.168.1.20/24\nIP4.GATEWAY:192.168.1.1\nIP4.DNS[1]:1.1.1.1\nIP4.DNS[2]:8.8.8.8",
		"nmcli -t -f ipv4.method connection show Wired":                "ipv4.method:manual",
	}}
	withFake(t, f)
	st := &NetworkState{Interfaces: []Interface{{Name: "eth0", Conn: "Wired"}}}
	fillIPv4NM(context.Background(), st)
	ip := st.Interfaces[0].IPv4
	if ip == nil || ip.Method != "manual" || !ip.Editable || ip.Gateway != "192.168.1.1" ||
		strings.Join(ip.Addresses, ",") != "192.168.1.20/24" || strings.Join(ip.DNS, ",") != "1.1.1.1,8.8.8.8" {
		t.Fatalf("ipv4 = %+v", ip)
	}
}

func TestIPv4Validate(t *testing.T) {
	ok := []IPv4Config{
		{Method: "auto"},
		{Method: "auto", DNS: []string{"1.1.1.1"}},
		{Method: "manual", Address: "192.168.1.50/24", Gateway: "192.168.1.1", DNS: []string{"9.9.9.9"}},
	}
	bad := []IPv4Config{
		{Method: "static"},
		{Method: "manual", Address: "192.168.1.50"},                         // no prefix
		{Method: "manual", Address: "192.168.1.50/24", Gateway: "10.0.0.1"}, // gateway off-subnet
		{Method: "auto", DNS: []string{"not-an-ip"}},
	}
	for _, c := range ok {
		if err := c.Validate(); err != nil {
			t.Errorf("%+v should be valid: %v", c, err)
		}
	}
	for _, c := range bad {
		if c.Validate() == nil {
			t.Errorf("%+v should be rejected", c)
		}
	}
}

func TestModifyArgs(t *testing.T) {
	got := strings.Join(modifyArgs("Wired", IPv4Config{Method: "manual", Address: "10.0.0.5/8", Gateway: "10.0.0.1", DNS: []string{"1.1.1.1", "8.8.8.8"}}), "|")
	want := "connection|modify|Wired|ipv4.method|manual|ipv4.addresses|10.0.0.5/8|ipv4.gateway|10.0.0.1|ipv4.dns|1.1.1.1 8.8.8.8|ipv4.ignore-auto-dns|no"
	if got != want {
		t.Fatalf("manual:\n got %s\nwant %s", got, want)
	}
	got = strings.Join(modifyArgs("Wired", IPv4Config{Method: "auto"}), "|")
	want = "connection|modify|Wired|ipv4.method|auto|ipv4.addresses||ipv4.gateway||ipv4.dns||ipv4.ignore-auto-dns|no"
	if got != want {
		t.Fatalf("auto:\n got %s\nwant %s", got, want)
	}
}

func TestChangeIPv4RevertsUnlessKept(t *testing.T) {
	f := &fakeNM{answers: map[string]string{
		"nmcli -t -f ipv4.method,ipv4.addresses,ipv4.gateway,ipv4.dns connection show Wired": "ipv4.method:auto\nipv4.addresses:\nipv4.gateway:--\nipv4.dns:",
	}}
	withFake(t, f)
	newCfg := IPv4Config{Method: "manual", Address: "192.168.1.50/24", Gateway: "192.168.1.1"}

	// Not kept → applied, then reverted to DHCP.
	if _, err := ChangeIPv4(context.Background(), "Wired", newCfg, 1500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2500 * time.Millisecond)
	joined := strings.Join(f.calls, "\n")
	if !strings.Contains(joined, "ipv4.method manual ipv4.addresses 192.168.1.50/24") {
		t.Fatalf("change never applied:\n%s", joined)
	}
	if !strings.Contains(joined, "ipv4.method auto ipv4.addresses  ipv4.gateway ") {
		t.Fatalf("change was not reverted:\n%s", joined)
	}

	// Kept → no revert.
	f.calls = nil
	tok, err := ChangeIPv4(context.Background(), "Wired", newCfg, 1500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1000 * time.Millisecond)
	if !KeepIPv4(tok) {
		t.Fatal("keep failed")
	}
	time.Sleep(1200 * time.Millisecond)
	if strings.Contains(strings.Join(f.calls, "\n"), "ipv4.method auto") {
		t.Fatalf("kept change was reverted anyway:\n%s", strings.Join(f.calls, "\n"))
	}
}

func TestFillIPv4Windows(t *testing.T) {
	f := &fakeNM{answers: map[string]string{"netsh interface ipv4 show config": `
Configuration for interface "Ethernet 5"
    DHCP enabled:                         Yes
    IP Address:                           192.168.1.170
    Subnet Prefix:                        192.168.0.0/23 (mask 255.255.254.0)
    Default Gateway:                      192.168.0.1
    Gateway Metric:                       0
    InterfaceMetric:                      25
    DNS servers configured through DHCP:  192.168.0.1
                                          8.8.8.8
    Register with which suffix:           Primary only

Configuration for interface "Loopback Pseudo-Interface 1"
    DHCP enabled:                         No
    IP Address:                           127.0.0.1
`}}
	withFake(t, f)
	st := &NetworkState{Interfaces: []Interface{{Name: "Ethernet 5"}}}
	fillIPv4Windows(context.Background(), st)
	ip := st.Interfaces[0].IPv4
	if ip == nil || ip.Method != "auto" || ip.Gateway != "192.168.0.1" || ip.Editable ||
		strings.Join(ip.Addresses, ",") != "192.168.1.170/23" || strings.Join(ip.DNS, ",") != "192.168.0.1,8.8.8.8" {
		t.Fatalf("windows ipv4 = %+v", ip)
	}
}
