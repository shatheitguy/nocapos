package api

import (
	"strconv"
	"testing"

	"alfaos/alfad/internal/appstore"
	"alfaos/alfad/internal/docker"
	"alfaos/alfad/internal/webaddr"
)

func TestContainerWebURL(t *testing.T) {
	catalog, err := appstore.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	var app *appstore.App
	for i := range catalog {
		if catalog[i].Web != nil {
			app = &catalog[i]
			break
		}
	}
	if app == nil {
		t.Skip("no catalog app with a web UI")
	}
	s := &Server{Deps: Deps{AppStore: appstore.NewManager(catalog, nil, nil)}}
	labels := map[string]string{appstore.LabelApp: app.ID, appstore.LabelService: app.Web.Service}
	macvlan := []docker.NetworkAttachment{{Name: "lan", Driver: "macvlan", IP: "10.0.0.50"}}
	bridge := []docker.NetworkAttachment{{Name: "nocap-x", Driver: "bridge", IP: "172.20.0.2"}}

	path := app.Web.Path
	if path == "" {
		path = "/"
	}
	want := "http://10.0.0.50:" + strconv.Itoa(app.Web.Port) + path
	if app.Web.Port == 443 || app.Web.Port == 8443 {
		want = "https" + want[4:]
	}
	if got := s.containerWebURL("nocap-x-web", labels, macvlan, []int{1234}, nil); got != want {
		t.Errorf("app on macvlan: got %q, want %q (the catalog's web port, not a guess)", got, want)
	}
	if got := s.containerWebURL("nocap-x-web", labels, bridge, nil, nil); got != "" {
		t.Errorf("app on a bridge should use the host's published port, got %q", got)
	}
	book := map[string]string{webaddr.AppKey(app.ID): "https://media.home.lan"}
	if got := s.containerWebURL("nocap-x-web", labels, macvlan, nil, book); got != "https://media.home.lan" {
		t.Errorf("app override: got %q", got)
	}
	book[webaddr.ContainerKey("nocap-x-web")] = "http://10.0.0.99:81/"
	if got := s.containerWebURL("nocap-x-web", labels, macvlan, nil, book); got != "http://10.0.0.99:81/" {
		t.Errorf("container override wins: got %q", got)
	}
	// A plain container on ipvlan: its likeliest web port.
	if got := s.containerWebURL("web", nil, []docker.NetworkAttachment{{Driver: "ipvlan", IP: "192.168.1.70"}}, []int{9443, 8080}, nil); got != "http://192.168.1.70:8080/" {
		t.Errorf("plain container: got %q", got)
	}
}
