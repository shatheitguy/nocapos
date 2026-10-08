package hostctl

import (
	"context"
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

func TestWiFiConnectKeepsPasswordOffCommandLine(t *testing.T) {
	var fileBody string
	f := &fakeNM{}
	withFake(t, f)
	runCmd = func(ctx context.Context, name string, args ...string) (string, error) {
		f.calls = append(f.calls, name+" "+strings.Join(args, " "))
		if i := indexOf(args, "passwd-file"); i >= 0 {
			b, _ := os.ReadFile(args[i+1])
			fileBody = string(b)
		}
		return "", nil
	}
	if err := WiFiConnect(context.Background(), "Office", "s3cret-pass"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(f.calls, " "), "s3cret-pass") {
		t.Fatalf("password leaked onto the command line: %v", f.calls)
	}
	if fileBody != "802-11-wireless-security.psk:s3cret-pass\n" {
		t.Fatalf("password file = %q", fileBody)
	}
}

func indexOf(xs []string, v string) int {
	for i, x := range xs {
		if x == v {
			return i
		}
	}
	return -1
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
