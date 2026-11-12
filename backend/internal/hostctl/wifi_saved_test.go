package hostctl

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exactNM answers whole nmcli command lines (no prefix guessing) and
// records every call.
type exactNM struct {
	out   map[string]string
	fail  map[string]string
	calls []string
}

func (e *exactNM) run(_ context.Context, name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	e.calls = append(e.calls, call)
	if msg, ok := e.fail[call]; ok {
		return msg, errors.New(msg)
	}
	return e.out[call], nil
}

func (e *exactNM) argv() string { return strings.Join(e.calls, "\n") }

// terse escapes a value the way `nmcli -t` prints it.
func terse(v string) string {
	return strings.NewReplacer(`\`, `\\`, `:`, `\:`).Replace(v)
}

const listCmd = "nmcli -t -f NAME,UUID,TYPE,AUTOCONNECT,AUTOCONNECT-PRIORITY,TIMESTAMP,ACTIVE,DEVICE,FILENAME connection show"

const (
	uHome = "11111111-1111-4111-8111-111111111111"
	uCorp = "22222222-2222-4222-8222-222222222222"
	uCafe = "33333333-3333-4333-8333-333333333333"
	uSpot = "44444444-4444-4444-8444-444444444444"
	uEth  = "55555555-5555-4555-8555-555555555555"
)

// savedFixture sets up a NetworkManager with four Wi-Fi profiles (one is a
// hotspot) and an Ethernet one; Home's keyfile is real, in a temp dir.
func savedFixture(t *testing.T) (*exactNM, string) {
	t.Helper()
	dir := t.TempDir()
	certs := t.TempDir()
	oldDir, oldCerts := nmConnectionsDir, nmCertsDir
	nmConnectionsDir, nmCertsDir = dir, certs
	t.Cleanup(func() { nmConnectionsDir, nmCertsDir = oldDir, oldCerts })

	home := filepath.Join(dir, "Home.nmconnection")
	corp := filepath.Join(dir, "Corp.nmconnection")
	if err := os.WriteFile(home, []byte("[connection]\nid=Home:5G\nuuid="+uHome+"\ntype=wifi\n\n[wifi]\nssid=Home:5G\n\n[wifi-security]\nkey-mgmt=wpa-psk\npsk=old-password\npsk-flags=0\n\n[ipv4]\nmethod=auto\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(corp, []byte("[connection]\nid=Corp\nuuid="+uCorp+"\ntype=wifi\n\n[wifi]\nssid=Corp\n\n[wifi-security]\nkey-mgmt=wpa-eap\n\n[802-1x]\neap=peap;\nidentity=alice\npassword=old-eap-pass\nphase2-auth=mschapv2\nsystem-ca-certs=true\n\n[ipv4]\nmethod=auto\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := &exactNM{out: map[string]string{
		listCmd: strings.Join([]string{
			`Home\:5G:` + uHome + `:802-11-wireless:yes:10:1762900000:yes:wlan0:` + terse(home),
			`Corp:` + uCorp + `:802-11-wireless:no:0:1762800000:no:--:` + terse(corp),
			`Cafe:` + uCafe + `:802-11-wireless:yes:-5:0:no:--:/run/NetworkManager/system-connections/netplan-cafe.nmconnection`,
			`Hotspot:` + uSpot + `:802-11-wireless:no:0:0:no:--:` + terse(filepath.Join(dir, "Hotspot.nmconnection")),
			`Wired connection 1:` + uEth + `:802-3-ethernet:yes:0:1762900000:yes:eth0:/etc/x.nmconnection`,
		}, "\n"),
		"nmcli -t connection show uuid " + uHome:                                 "connection.id:Home\\:5G\n802-11-wireless.ssid:Home\\:5G\n802-11-wireless.mode:infrastructure\n802-11-wireless.hidden:no\n802-11-wireless-security.key-mgmt:wpa-psk\n802-11-wireless-security.psk:<hidden>",
		"nmcli -t connection show uuid " + uCorp:                                 "802-11-wireless.ssid:Corp\n802-11-wireless.hidden:yes\n802-11-wireless-security.key-mgmt:wpa-eap\n802-1x.eap:peap\n802-1x.identity:alice\n802-1x.phase2-auth:mschapv2\n802-1x.ca-cert:--\n802-1x.system-ca-certs:yes\n802-1x.domain-suffix-match:radius.example.com",
		"nmcli -t connection show uuid " + uCafe:                                 "802-11-wireless.ssid:Cafe\n802-11-wireless.mode:infrastructure",
		"nmcli -t connection show uuid " + uSpot:                                 "802-11-wireless.ssid:NoCap Hotspot\n802-11-wireless.mode:ap\n802-11-wireless-security.key-mgmt:wpa-psk",
		"nmcli -s -g 802-11-wireless-security.psk connection show uuid " + uHome: `pa\:ss\\word;"x"`,
	}}
	old, oldCan := runCmd, canControl
	runCmd, canControl = e.run, func() error { return nil }
	t.Cleanup(func() { runCmd, canControl = old, oldCan })
	return e, dir
}

func TestSavedWiFiList(t *testing.T) {
	savedFixture(t)
	list, err := SavedWiFiList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("want 3 networks (no hotspot, no Ethernet), got %+v", list)
	}
	home, corp, cafe := list[0], list[1], list[2]
	if home.SSID != "Home:5G" || !home.Active || home.Device != "wlan0" || home.Priority != 10 || !home.AutoConnect ||
		home.LastConnected != 1762900000 || home.Security != "wpa-psk" || !home.Shareable || !home.Editable || home.Enterprise {
		t.Errorf("home: %+v", home)
	}
	if corp.SSID != "Corp" || corp.Active || corp.AutoConnect || !corp.Hidden || !corp.Enterprise || corp.Shareable || !corp.Editable {
		t.Errorf("corp: %+v", corp)
	}
	if corp.EAP == nil || corp.EAP.Method != "peap" || corp.EAP.Phase2 != "mschapv2" || corp.EAP.Identity != "alice" ||
		corp.EAP.CA != "system" || corp.EAP.DomainSuffixMatch != "radius.example.com" {
		t.Errorf("corp EAP: %+v", corp.EAP)
	}
	if cafe.SSID != "Cafe" || cafe.Security != "open" || !cafe.Shareable || cafe.Editable || cafe.Priority != -5 || cafe.LastConnected != 0 {
		t.Errorf("cafe: %+v", cafe)
	}
}

func TestSavedWiFiShare(t *testing.T) {
	e, _ := savedFixture(t)
	sh, err := SavedWiFiShare(context.Background(), uHome)
	if err != nil {
		t.Fatal(err)
	}
	if sh.SSID != "Home:5G" || sh.Password != `pa:ss\word;"x"` || sh.Type != "WPA" || sh.Hidden {
		t.Fatalf("share: %+v", sh)
	}
	if _, err := SavedWiFiShare(context.Background(), uCorp); err == nil {
		t.Fatal("an enterprise network must not be shareable")
	}
	open, err := SavedWiFiShare(context.Background(), uCafe)
	if err != nil || open.Type != "nopass" || open.Password != "" {
		t.Fatalf("open share: %+v %v", open, err)
	}
	if _, err := SavedWiFiShare(context.Background(), "not-a-uuid; rm -rf"); !errors.Is(err, ErrNoSuchProfile) {
		t.Fatalf("bad uuid: %v", err)
	}
	if strings.Contains(e.argv(), "rm -rf") {
		t.Fatal("an invalid UUID reached nmcli")
	}
}

func TestSavedWiFiSetPasswordRewritesKeyfile(t *testing.T) {
	e, dir := savedFixture(t)
	const pw = `new pass\word#1 `
	if err := SavedWiFiSetPassword(context.Background(), uHome, pw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.argv(), "new pass") {
		t.Fatalf("password reached a command line:\n%s", e.argv())
	}
	file := filepath.Join(dir, "Home.nmconnection")
	if !strings.Contains(e.argv(), "nmcli connection load "+file) {
		t.Fatalf("keyfile not reloaded:\n%s", e.argv())
	}
	data, _ := os.ReadFile(file)
	got := string(data)
	if !strings.Contains(got, `psk=new pass\\word#1\s`+"\n") || strings.Contains(got, "old-password") || strings.Contains(got, "psk-flags") {
		t.Fatalf("keyfile:\n%s", got)
	}
	if !strings.Contains(got, "[ipv4]\nmethod=auto") || !strings.Contains(got, "id=Home:5G") {
		t.Fatalf("other settings must stay:\n%s", got)
	}
	// Bad passwords and profiles NoCapOS can't rewrite are refused.
	if SavedWiFiSetPassword(context.Background(), uHome, "short") == nil {
		t.Error("a 5-character WPA password was accepted")
	}
	if err := SavedWiFiSetPassword(context.Background(), uCafe, "longenough"); err == nil {
		t.Error("an open network's password was changed")
	}
}

func TestSavedWiFiNotEditableOutsideNMFolder(t *testing.T) {
	savedFixture(t)
	r := savedRow{SavedWiFi: SavedWiFi{Security: "wpa-psk"}, file: "/run/NetworkManager/system-connections/netplan-cafe.nmconnection"}
	if err := rewriteKeyfile(context.Background(), r, nil); !errors.Is(err, ErrNotEditable) {
		t.Fatalf("want ErrNotEditable, got %v", err)
	}
}

func TestSavedModifyArgs(t *testing.T) {
	on, pri := true, 25
	args, err := savedModifyArgs(uHome, SavedSettings{AutoConnect: &on, Priority: &pri})
	if err != nil {
		t.Fatal(err)
	}
	want := "connection modify uuid " + uHome + " connection.autoconnect yes connection.autoconnect-priority 25"
	if strings.Join(args, " ") != want {
		t.Fatalf("got %q", args)
	}
	big := 5000
	if _, err := savedModifyArgs(uHome, SavedSettings{Priority: &big}); err == nil {
		t.Error("priority out of range accepted")
	}
	if _, err := savedModifyArgs(uHome, SavedSettings{}); err == nil {
		t.Error("an empty change accepted")
	}
	if got := strings.Join(savedConnectArgs(uHome), " "); got != "--wait 45 connection up uuid "+uHome {
		t.Errorf("connect args %q", got)
	}
}

func TestSavedWiFiForgetRemovesCerts(t *testing.T) {
	e, _ := savedFixture(t)
	certDir := filepath.Join(nmCertsDir, uCorp)
	_ = os.MkdirAll(certDir, 0o700)
	_ = os.WriteFile(filepath.Join(certDir, "ca.pem"), []byte("x"), 0o600)
	if err := SavedWiFiForget(context.Background(), uCorp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.argv(), "nmcli connection delete uuid "+uCorp) {
		t.Fatalf("no delete:\n%s", e.argv())
	}
	if _, err := os.Stat(certDir); !os.IsNotExist(err) {
		t.Fatal("certificates left behind")
	}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

const pemCA = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"

func TestEAPValidate(t *testing.T) {
	ok := []EAPConfig{
		{Method: "PEAP", Identity: "alice", Password: "pw"}, // phase 2 defaults to MSCHAPv2, CA to system
		{Method: "ttls", Phase2: "pap", Identity: "bob", Password: "pw", CA: "file", CACert: b64(pemCA), DomainSuffixMatch: "radius.example.com"},
		{Method: "tls", Identity: "dev", ClientCert: b64(pemCA), PrivateKey: b64("-----BEGIN PRIVATE KEY-----\n"), PrivateKeyPassword: "k"},
		{Method: "tls", Identity: "dev", ClientCert: b64("\x30\x82binary-p12")}, // PKCS#12: key inside
		{Method: "pwd", Identity: "x", Password: "y"},
		{Method: "leap", Identity: "x", Password: "y"},
	}
	for _, c := range ok {
		if err := c.Validate(nil); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}
	bad := []EAPConfig{
		{Method: "md5", Identity: "a", Password: "b"},
		{Method: "peap", Phase2: "pap", Identity: "a", Password: "b"}, // PEAP has no PAP
		{Method: "peap", Password: "b"},
		{Method: "peap", Identity: "a"},
		{Method: "peap", Identity: "a\nb", Password: "b"},
		{Method: "ttls", Identity: "a", Password: "b", CA: "file"},
		{Method: "tls", Identity: "a"},
		{Method: "tls", Identity: "a", ClientCert: b64(pemCA)},
		{Method: "peap", Identity: "a", Password: "b", CA: "file", CACert: "%%%"},
		{Method: "peap", Identity: "a", Password: "b", DomainSuffixMatch: "bad domain"},
	}
	for _, c := range bad {
		if c.Validate(nil) == nil {
			t.Errorf("%+v accepted", c)
		}
	}
	// Editing: a blank password keeps the saved one for the same method.
	keep := EAPConfig{Method: "peap", Identity: "alice"}
	if err := keep.Validate(&EAPInfo{Method: "peap", CA: "system"}); err != nil {
		t.Errorf("blank password on edit: %v", err)
	}
	if err := (&EAPConfig{Method: "ttls", Identity: "alice"}).Validate(&EAPInfo{Method: "peap"}); err == nil {
		t.Error("switching method without a password accepted")
	}
}

// A new enterprise join writes the whole sign-in into the keyfile and the
// certificates into their own root-only folder; no secret reaches argv.
func TestWiFiConnectEnterpriseKeyfile(t *testing.T) {
	nmConnectionsDir, nmCertsDir = t.TempDir(), t.TempDir()
	t.Cleanup(func() {
		nmConnectionsDir, nmCertsDir = "/etc/NetworkManager/system-connections", "/etc/NetworkManager/nocap-certs"
	})
	e := &exactNM{out: map[string]string{"nmcli -t -f DEVICE,TYPE device": "wlan0:wifi"}}
	old, oldCan := runCmd, canControl
	runCmd, canControl = e.run, func() error { return nil }
	t.Cleanup(func() { runCmd, canControl = old, oldCan })

	j := WiFiJoin{SSID: "Corp Wi-Fi", EAP: &EAPConfig{
		Method: "ttls", Phase2: "gtc", Identity: "alice@corp", AnonymousIdentity: "anon@corp",
		Password: "eap-secret-1", CA: "file", CACert: b64(pemCA), DomainSuffixMatch: "radius.corp.example",
	}}
	if err := WiFiConnect(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.argv(), "eap-secret-1") {
		t.Fatalf("EAP password in argv:\n%s", e.argv())
	}
	if !strings.Contains(e.argv(), "nmcli --wait 45 connection up id Corp Wi-Fi") {
		t.Fatalf("no connection up:\n%s", e.argv())
	}
	files, _ := filepath.Glob(filepath.Join(nmConnectionsDir, "*.nmconnection"))
	if len(files) != 1 {
		t.Fatalf("want one keyfile, got %v", files)
	}
	data, _ := os.ReadFile(files[0])
	kf := string(data)
	for _, want := range []string{
		"key-mgmt=wpa-eap", "[802-1x]", "eap=ttls;", "identity=alice@corp", "anonymous-identity=anon@corp",
		"password=eap-secret-1", "phase2-autheap=gtc", "domain-suffix-match=radius.corp.example", "ca-cert=" + keyfileEscape(nmCertsDir),
	} {
		if !strings.Contains(kf, want) {
			t.Errorf("keyfile lacks %q:\n%s", want, kf)
		}
	}
	if strings.Contains(kf, "phase2-auth=") || strings.Contains(kf, "system-ca-certs") || strings.Contains(kf, "psk=") {
		t.Errorf("unexpected keys:\n%s", kf)
	}
	ca, _ := filepath.Glob(filepath.Join(nmCertsDir, "*", "ca.pem"))
	if len(ca) != 1 {
		t.Fatalf("CA certificate not saved: %v", ca)
	}
	if got, _ := os.ReadFile(ca[0]); string(got) != pemCA {
		t.Fatalf("CA content %q", got)
	}
}

func TestSavedWiFiSetEAP(t *testing.T) {
	e, dir := savedFixture(t)
	// Switch Corp from PEAP to EAP-TLS with a PKCS#12 bundle and key password.
	c := EAPConfig{Method: "tls", Identity: "laptop-7", CA: "file", CACert: b64(pemCA), ClientCert: b64("\x30\x82p12-bytes"), PrivateKeyPassword: "p12-pass"}
	if err := SavedWiFiSetEAP(context.Background(), uCorp, c); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.argv(), "p12-pass") {
		t.Fatalf("key password in argv:\n%s", e.argv())
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Corp.nmconnection"))
	kf := string(data)
	p12 := keyfileEscape(filepath.Join(nmCertsDir, uCorp, "client.p12"))
	for _, want := range []string{"eap=tls;", "identity=laptop-7", "client-cert=" + p12, "private-key=" + p12, "private-key-password=p12-pass", "ca-cert="} {
		if !strings.Contains(kf, want) {
			t.Errorf("keyfile lacks %q:\n%s", want, kf)
		}
	}
	for _, gone := range []string{"old-eap-pass", "phase2-auth", "system-ca-certs", "eap=peap"} {
		if strings.Contains(kf, gone) {
			t.Errorf("keyfile still has %q:\n%s", gone, kf)
		}
	}
	if strings.Count(kf, "[802-1x]") != 1 {
		t.Errorf("section duplicated:\n%s", kf)
	}
	// A personal network has no EAP to edit.
	if SavedWiFiSetEAP(context.Background(), uHome, EAPConfig{Method: "peap", Identity: "a", Password: "b"}) == nil {
		t.Error("EAP edit on a WPA-personal network accepted")
	}
}

func TestEditKeyfile(t *testing.T) {
	in := "[connection]\nid=x\n\n[802-11-wireless-security]\nkey-mgmt=wpa-psk\n# note\npsk=old\npsk-flags=1\n\n[ipv4]\nmethod=auto\n"
	got := editKeyfile(in, map[string][]kv{
		"wifi-security": {{"psk", sp("new")}, {"psk-flags", nil}, {"pmf", sp("2")}},
		"802-1x":        {{"eap", sp("peap;")}, {"password", nil}},
	})
	want := "[connection]\nid=x\n\n[802-11-wireless-security]\nkey-mgmt=wpa-psk\n# note\npsk=new\npmf=2\n\n[ipv4]\nmethod=auto\n\n[802-1x]\neap=peap;\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestKeyfileEscape(t *testing.T) {
	cases := map[string]string{
		"plain":       "plain",
		` lead`:       `\slead`,
		`trail  `:     `trail\s\s`,
		`a b`:         `a b`,
		`back\slash`:  `back\\slash`,
		"tab\there":   `tab\there`,
		`#hash"quote`: `#hash"quote`,
	}
	for in, want := range cases {
		if got := keyfileEscape(in); got != want {
			t.Errorf("keyfileEscape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWiFiNetworksMarksEnterprise(t *testing.T) {
	e := &exactNM{out: map[string]string{
		"nmcli -t -f IN-USE,SSID,SIGNAL,SECURITY device wifi list --rescan no": " :Corp:70:WPA2 802.1X\n :Home:60:WPA2",
		"nmcli -t -f NAME,TYPE connection show":                                "Home:802-11-wireless",
	}}
	old, oldCan := runCmd, canControl
	runCmd, canControl = e.run, func() error { return nil }
	t.Cleanup(func() { runCmd, canControl = old, oldCan })
	list, err := WiFiNetworks(context.Background(), false)
	if err != nil || len(list) != 2 {
		t.Fatalf("%+v %v", list, err)
	}
	if !list[0].Enterprise || list[1].Enterprise || !list[1].Saved {
		t.Fatalf("%+v", list)
	}
}

// nmcli -t escapes both `:` and `\` in values; a saved SSID shows unescaped.
func TestFillDetailsUnescapesSSID(t *testing.T) {
	var r savedRow
	fillDetails(&r, "802-11-wireless.ssid:"+terse(`C:\Lab`)+"\n802-11-wireless-security.key-mgmt:wpa-psk")
	if r.SSID != `C:\Lab` || !r.Shareable {
		t.Fatalf("got %+v", r.SavedWiFi)
	}
}
