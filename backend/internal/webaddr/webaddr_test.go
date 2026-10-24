package webaddr

import (
	"context"
	"testing"
)

func TestResolve(t *testing.T) {
	macvlan := []Attachment{{Driver: "bridge", IP: "172.18.0.5"}, {Driver: "macvlan", IP: "10.0.0.50"}}
	cases := []struct {
		name     string
		override string
		nets     []Attachment
		port     int
		path     string
		want     string
	}{
		{"bridge only uses host + published port", "", []Attachment{{Driver: "bridge", IP: "172.18.0.5"}}, 8096, "/", ""},
		{"macvlan uses the container's IP and internal port", "", macvlan, 8096, "/web", "http://10.0.0.50:8096/web"},
		{"ipvlan works the same", "", []Attachment{{Driver: "ipvlan", IP: "192.168.1.60"}}, 80, "", "http://192.168.1.60:80/"},
		{"path without slash", "", []Attachment{{Driver: "ipvlan", IP: "192.168.1.60"}}, 3000, "admin", "http://192.168.1.60:3000/admin"},
		{"macvlan without an address yet", "", []Attachment{{Driver: "macvlan"}}, 8096, "/", ""},
		{"no port", "", macvlan, 0, "/", ""},
		{"override wins over macvlan", "https://jellyfin.home.lan", macvlan, 8096, "/", "https://jellyfin.home.lan"},
		{"override wins over host port", "http://10.0.0.9:8080/", nil, 8096, "/", "http://10.0.0.9:8080/"},
		{"ipv6 container address", "", []Attachment{{Driver: "macvlan", IP: "fd00::5"}}, 8080, "/", "http://[fd00::5]:8080/"},
		{"https port", "", []Attachment{{Driver: "macvlan", IP: "10.0.0.51"}}, 443, "/", "https://10.0.0.51:443/"},
	}
	for _, c := range cases {
		if got := Resolve(c.override, c.nets, c.port, c.path); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestPickPort(t *testing.T) {
	cases := []struct {
		in   []int
		want int
	}{
		{nil, 0},
		{[]int{5432}, 5432},
		{[]int{9443, 8096}, 8096},
		{[]int{1900, 8096, 8080}, 8080},
		{[]int{443, 80}, 80},
		{[]int{0, 70000, 22}, 22},
	}
	for _, c := range cases {
		if got := PickPort(c.in); got != c.want {
			t.Errorf("PickPort(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestNormalize(t *testing.T) {
	ok := map[string]string{
		"":                              "",
		"  https://jellyfin.home.lan  ": "https://jellyfin.home.lan",
		"http://10.0.0.50:8096":         "http://10.0.0.50:8096",
		"https://nas.lan/app/#frag":     "https://nas.lan/app/",
		"http://[fd00::1]:8080/":        "http://[fd00::1]:8080/",
	}
	for in, want := range ok {
		got, err := Normalize(in)
		if err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"javascript:alert(1)", "ftp://nas.lan", "jellyfin.home.lan", "http://", "https://user:pw@nas.lan",
		"http://nas.lan:99999", "http://nas lan", "http://nas.lan/\"onmouseover", "data:text/html,hi", "//nas.lan",
	} {
		if _, err := Normalize(in); err == nil {
			t.Errorf("Normalize(%q) should fail", in)
		}
	}
}

type memSettings map[string]string

func (m memSettings) Setting(_ context.Context, k string) (string, error) { return m[k], nil }
func (m memSettings) SetSetting(_ context.Context, k, v string) error     { m[k] = v; return nil }

func TestBook(t *testing.T) {
	ctx := context.Background()
	b := NewBook(memSettings{})
	if err := b.Set(ctx, AppKey("jellyfin"), "https://jellyfin.home.lan"); err != nil {
		t.Fatal(err)
	}
	if err := b.Set(ctx, ContainerKey("web"), "http://10.0.0.7/"); err != nil {
		t.Fatal(err)
	}
	all, _ := b.All(ctx)
	if all["app:jellyfin"] != "https://jellyfin.home.lan" || all["container:web"] != "http://10.0.0.7/" {
		t.Fatalf("saved %v", all)
	}
	if err := b.Set(ctx, AppKey("jellyfin"), ""); err != nil {
		t.Fatal(err)
	}
	if all, _ = b.All(ctx); len(all) != 1 {
		t.Fatalf("clearing should remove the entry: %v", all)
	}
	broken := NewBook(memSettings{settingKey: "{not json"})
	if all, err := broken.All(ctx); err != nil || len(all) != 0 {
		t.Fatalf("damaged value: %v %v", all, err)
	}
}
