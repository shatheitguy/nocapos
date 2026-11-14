package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestLinkTXT(t *testing.T) {
	got := linkTXT("NC-AB12", "https", "0.13.0", true)
	want := []string{"nocap_id=NC-AB12", "scheme=https", "setup=new", "version=0.13.0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("new: %v", got)
	}
	if got := linkTXT("NC-AB12", "http", "", false); !reflect.DeepEqual(got, []string{"nocap_id=NC-AB12", "scheme=http", "setup=done"}) {
		t.Errorf("done, no version: %v", got)
	}
}

func TestBLEWanted(t *testing.T) {
	for _, c := range []struct {
		mode  string
		isNew bool
		want  bool
	}{
		{"", true, true}, {"", false, false},
		{"on", false, true}, {"on", true, true},
		{"off", true, false}, {"off", false, false},
	} {
		if got := bleWanted(c.mode, c.isNew); got != c.want {
			t.Errorf("bleWanted(%q, %v) = %v", c.mode, c.isNew, got)
		}
	}
}

func TestParseNoCapBrowse(t *testing.T) {
	text := strings.Join([]string{
		`+;eth0;IPv4;NoCap\032media-box;_nocap._tcp;local`,
		`=;eth0;IPv4;NoCap\032media-box;_nocap._tcp;local;media-box.local;192.168.1.20;8443;"version=0.13.0" "setup=done" "scheme=https" "nocap_id=NC-OTHER1"`,
		`=;eth0;IPv6;NoCap\032media-box;_nocap._tcp;local;media-box.local;fe80::1;8443;"setup=done" "scheme=https" "nocap_id=NC-OTHER1"`,
		`=;eth0;IPv6;NoCap\032media-box;_nocap._tcp;local;media-box.local;fd00::20;8443;"setup=done" "scheme=https" "nocap_id=NC-OTHER1"`,
		`=;wlan0;IPv4;NoCap\032media-box;_nocap._tcp;local;media-box.local;192.168.1.20;8443;"setup=done" "scheme=https" "nocap_id=NC-OTHER1"`,
		// a brand-new one on plain http, from an older version without version=
		`=;eth0;IPv4;NoCap\032fresh;_nocap._tcp;local;fresh.local;192.168.1.30;8080;"setup=new" "scheme=http" "nocap_id=NC-NEW2"`,
		// this server itself
		`=;eth0;IPv4;NoCap\032me;_nocap._tcp;local;me.local;192.168.1.5;8443;"scheme=https" "nocap_id=NC-SELF"`,
		// junk: no or bad ID, bad port, other service
		`=;eth0;IPv4;x;_nocap._tcp;local;x.local;192.168.1.40;8443;"scheme=https"`,
		`=;eth0;IPv4;x;_nocap._tcp;local;x.local;192.168.1.41;8443;"nocap_id=<script>"`,
		`=;eth0;IPv4;x;_nocap._tcp;local;x.local;192.168.1.42;99999;"nocap_id=NC-BAD3"`,
		`=;eth0;IPv4;x;_smb._tcp;local;x.local;192.168.1.43;445;"nocap_id=NC-SMB4"`,
	}, "\n")
	got := parseNoCapBrowse(text, "NC-SELF")
	want := []foundNoCap{
		{NocapID: "NC-NEW2", Name: "fresh", Hostname: "fresh.local", Addresses: []string{"192.168.1.30"}, URL: "http://192.168.1.30:8080", Setup: "new"},
		{NocapID: "NC-OTHER1", Name: "media-box", Hostname: "media-box.local", Addresses: []string{"192.168.1.20", "[fd00::20]"}, URL: "https://192.168.1.20:8443", Version: "0.13.0", Setup: "done"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if got := parseNoCapBrowse("", "x"); got == nil || len(got) != 0 {
		t.Errorf("empty: %#v", got)
	}
}

func TestParseTXTEscapes(t *testing.T) {
	got := parseTXT(`"a=x\"y" "b=c\032d" "a=second"`)
	if got["a"] != `x"y` || got["b"] != "c d" || len(got) != 2 {
		t.Errorf("%v", got)
	}
}

// A brand-new server: the phone app creates the administrator, then links
// itself in the same session, with no pairing code.
func TestSetupThenLinkSelf(t *testing.T) {
	s := newSetupWorld(t, "tok-123")
	s.Config.DataDir = t.TempDir()

	info := func() pairInfo {
		rec := httptest.NewRecorder()
		s.pairInfo(rec, httptest.NewRequest("GET", "/api/v1/pair/info", nil))
		var p pairInfo
		_ = json.Unmarshal(rec.Body.Bytes(), &p)
		return p
	}
	if p := info(); p.Setup != "new" || !strings.HasPrefix(p.NocapID, "NC-") {
		t.Fatalf("before setup: %+v", p)
	}
	changed := s.discovery.wait()

	rec := setupCall(s, "192.168.1.10:4000", "", map[string]string{"username": "owner", "password": pw})
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body)
	}
	select {
	case <-changed:
	default:
		t.Fatal("setup didn't tell the announcers")
	}
	if p := info(); p.Setup != "done" {
		t.Fatalf("after setup: %+v", p)
	}
	var sess sessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)

	link := func(bearer string, body map[string]string) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		_ = json.NewEncoder(&buf).Encode(body)
		r := httptest.NewRequest("POST", "/api/v1/devices/link-self", &buf)
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = "192.168.1.10:4000"
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		s.authed(s.devicesLinkSelf).ServeHTTP(rec, r)
		return rec
	}
	if rec := link("", map[string]string{"device_name": "Pixel", "platform": "android"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("without a session: %d", rec.Code)
	}
	if rec := link(sess.AccessToken, map[string]string{"device_name": "", "platform": "android"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("no name: %d", rec.Code)
	}
	rec = link(sess.AccessToken, map[string]string{"device_name": "Pixel", "platform": "android", "model": "Pixel 9", "os_version": "Android 16"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("link-self: %d %s", rec.Code, rec.Body)
	}
	var c claimResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &c)
	if c.Token == "" || c.User["username"] != "owner" || c.NocapID != info().NocapID {
		t.Fatalf("claim: %+v", c)
	}
	list, err := s.Store.ListDevices(t.Context(), "")
	if err != nil || len(list) != 1 || list[0].Name != "Pixel" || list[0].Model != "Pixel 9" {
		t.Fatalf("devices: %+v %v", list, err)
	}
}
