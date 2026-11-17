package api

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"alfaos/alfad/internal/config"
	"alfaos/alfad/internal/docker"
	"alfaos/alfad/internal/localaddr"
)

func TestLocalRoutes(t *testing.T) {
	list := []docker.Container{
		{ID: "a", Name: "shop-api-1", State: "running", Project: "shop", Service: "api",
			Labels:   map[string]string{localaddr.LabelDomain: "api.shop"},
			Ports:    []docker.Port{{Private: 8080, Public: 18080, Protocol: "tcp"}},
			Networks: []docker.NetworkAttachment{{Name: "shop_default", IP: "172.20.0.2"}}},
		{ID: "b", Name: "Media_Box", State: "running", Labels: map[string]string{},
			Ports:    []docker.Port{{Private: 8096, Protocol: "tcp"}},
			Networks: []docker.NetworkAttachment{{Name: "bridge", IP: "172.17.0.3"}}},
		// Takes "api.shop" too, but the label above wins.
		{ID: "c", Name: "api", State: "running", Project: "shop", Service: "api2",
			Labels: map[string]string{localaddr.LabelDomain: "API.SHOP"}, Ports: []docker.Port{{Private: 80, Public: 81}}},
		{ID: "d", Name: "nocap-alfad", State: "running", System: true, Ports: []docker.Port{{Private: 80, Public: 80}}},
		{ID: "e", Name: "stopped", State: "exited", Ports: []docker.Port{{Private: 80, Public: 8081}}},
		{ID: "f", Name: "lan-cam", State: "running", Ports: []docker.Port{{Private: 80}},
			Networks: []docker.NetworkAttachment{{Name: "lan", Driver: "macvlan", IP: "192.168.1.60"}}},
		{ID: "g", Name: "hostnet", State: "running", Labels: map[string]string{localaddr.LabelPort: "8780"},
			Networks: []docker.NetworkAttachment{{Name: "host"}}},
		{ID: "h", Name: "evil", State: "running", Labels: map[string]string{localaddr.LabelDomain: "bank.example.com/x"},
			Ports: []docker.Port{{Private: 80, Public: 82}}},
		{ID: "i", Name: "local-only", State: "running", Ports: []docker.Port{{IP: "192.168.1.5", Private: 80, Public: 83}},
			Networks: []docker.NetworkAttachment{{Name: "bridge", IP: "172.17.0.9"}}},
	}
	tbl := localaddr.NewTable()
	tbl.Set(buildRoutes(list))
	want := map[string]string{
		"api.shop.nocap.local":   "a|127.0.0.1:18080",
		"shop-api-1.nocap.local": "a|127.0.0.1:18080",
		"media-box.nocap.local":  "b|172.17.0.3:8096",
		"api.nocap.local":        "c|127.0.0.1:81",
		"api2.shop.nocap.local":  "c|127.0.0.1:81",
		"evil.nocap.local":       "h|127.0.0.1:82",
		"local-only.nocap.local": "i|172.17.0.9:80",
	}
	got := map[string]string{}
	for _, r := range tbl.All() {
		got[r.Name] = r.ID + "|" + r.Target
	}
	for n, w := range want {
		if got[n] != w {
			t.Errorf("%s → %q, want %q", n, got[n], w)
		}
	}
	for n := range got {
		if _, ok := want[n]; !ok {
			t.Errorf("unexpected local address %s → %s (system, stopped, macvlan-only and host-network containers get none)", n, got[n])
		}
	}
}

func TestLocalURLsAndCert(t *testing.T) {
	s := &Server{Deps: Deps{Config: &config.Config{ListenAddr: ":443", TLSAuto: true, HTTPRedirectAddr: ":80", DataDir: t.TempDir()}}}
	s.localAddr = &localAddrState{table: localaddr.NewTable(), enabled: true, loaded: true, mdns: newMDNSPublisher(), kick: make(chan struct{}, 1)}
	h, hs := s.localURLs("web.nocap.local")
	if h != "http://web.nocap.local" || hs != "https://web.nocap.local" {
		t.Errorf("urls: %q %q", h, hs)
	}
	s.Config.ListenAddr = ":8443"
	if _, hs := s.localURLs("web.nocap.local"); hs != "https://web.nocap.local:8443" {
		t.Errorf("https on another port: %q", hs)
	}

	// Certificates only for names a container uses right now.
	s.localAddr.table.Set([]localaddr.Route{{Name: "web.nocap.local", ID: "x", Container: "web", Target: "127.0.0.1:1"}})
	c, err := s.LocalAddressCert(&tls.ClientHelloInfo{ServerName: "web.nocap.local"})
	if err != nil || c == nil || c.Leaf.DNSNames[0] != "web.nocap.local" {
		t.Fatalf("leaf for a known name: %v %v", c, err)
	}
	for _, n := range []string{"other.nocap.local", "nocap.local", "example.com", ""} {
		if c, _ := s.LocalAddressCert(&tls.ClientHelloInfo{ServerName: n}); c != nil {
			t.Errorf("%q must get the server's usual certificate", n)
		}
	}
	s.localAddr.enabled = false
	if c, _ := s.LocalAddressCert(&tls.ClientHelloInfo{ServerName: "web.nocap.local"}); c != nil {
		t.Error("turned off: no local certificates")
	}
}

// The files endpoints refuse bad paths and names before reaching Docker.
func TestContainerFilePathsChecked(t *testing.T) {
	s := &Server{}
	call := func(h http.HandlerFunc, method, target string) int {
		r := httptest.NewRequest(method, target, nil)
		r.SetPathValue("id", "web")
		w := httptest.NewRecorder()
		h(w, r)
		return w.Code
	}
	for _, p := range []string{"", "etc", "/etc/../root", "/a%00b", "..%2Fetc"} {
		if code := call(s.containerFiles, http.MethodGet, "/x?path="+p); code != http.StatusBadRequest {
			t.Errorf("list %q: %d", p, code)
		}
		if code := call(s.containerFileDownload, http.MethodGet, "/x?path="+p); code != http.StatusBadRequest {
			t.Errorf("download %q: %d", p, code)
		}
	}
	for _, n := range []string{"", "..", "a%2Fb", "x%00"} {
		if code := call(s.containerFileUpload, http.MethodPost, "/x?path=/app&name="+n); code != http.StatusBadRequest {
			t.Errorf("upload name %q: %d", n, code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/x?path=/", nil)
	r.SetPathValue("id", "../../etc")
	w := httptest.NewRecorder()
	s.containerFiles(w, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "container") {
		t.Errorf("bad container id: %d %s", w.Code, w.Body.String())
	}
}

func TestPullProgress(t *testing.T) {
	j := &pullJob{}
	p := func(id, status string, cur, total int64) docker.PullProgress {
		pp := docker.PullProgress{ID: id, Status: status}
		pp.Detail.Current, pp.Detail.Total = cur, total
		return pp
	}
	j.applyProgress(docker.PullProgress{ID: "latest", Status: "Pulling from library/nginx"})
	j.applyProgress(p("a", "Downloading", 50, 100))
	j.applyProgress(p("b", "Downloading", 0, 300))
	if j.Percent < 12 || j.Percent > 13 {
		t.Errorf("50 of 400 bytes: %.1f%%", j.Percent)
	}
	j.applyProgress(p("a", "Pull complete", 0, 0))
	j.applyProgress(p("b", "Download complete", 0, 0))
	if j.Percent != 99 {
		t.Errorf("not done until every layer is: %.1f%%", j.Percent)
	}
	j.applyProgress(p("b", "Pull complete", 0, 0))
	if j.Percent != 100 || len(j.Layers) != 2 {
		t.Errorf("done: %.1f%% %d layers", j.Percent, len(j.Layers))
	}
}
