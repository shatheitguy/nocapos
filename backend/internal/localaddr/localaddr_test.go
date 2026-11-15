package localaddr

import (
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNormalizeLabel(t *testing.T) {
	good := map[string]string{
		"api.myapp":             "api.myapp.nocap.local",
		"API.MyApp":             "api.myapp.nocap.local",
		"api.myapp.nocap.local": "api.myapp.nocap.local",
		" media ":               "media.nocap.local",
		"a-b.c1.d2":             "a-b.c1.d2.nocap.local",
		"photos.":               "photos.nocap.local",
	}
	for in, want := range good {
		got, err := NormalizeLabel(in)
		if err != nil || got != want {
			t.Errorf("NormalizeLabel(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"", ".", "nocap.local", "a..b", "-api", "api-", "under_score", "*.myapp", "a.b.c.d",
		"192.168.1.10", "api/../x", "api myapp", "ünicode", "a:8080", "www", "localhost",
		strings.Repeat("a", 64), strings.Repeat("ab.", 40) + "c",
	}
	for _, in := range bad {
		if got, err := NormalizeLabel(in); err == nil {
			t.Errorf("NormalizeLabel(%q) = %q; want an error", in, got)
		}
	}
	// "evil.com" is taken as a name under nocap.local, never as another domain.
	if got, _ := NormalizeLabel("evil.com"); got != "evil.com.nocap.local" {
		t.Errorf("other domains stay under nocap.local: %q", got)
	}
}

func TestSlugAndNames(t *testing.T) {
	cases := map[string]string{"My_App.1": "my-app-1", "__x__": "x", "---": "", "nocap": "", "Ä": ""}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	got := NamesFor("myapp-api-1", "myapp", "api", "API.Shop")
	want := []string{"api.shop.nocap.local", "myapp-api-1.nocap.local", "api.myapp.nocap.local"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("NamesFor = %v, want %v", got, want)
	}
	if got := NamesFor("web", "", "", "bad_label!"); strings.Join(got, ",") != "web.nocap.local" {
		t.Errorf("an invalid label is ignored: %v", got)
	}
	if !IsLocalName("a.nocap.local") || IsLocalName("nocap.local") || IsLocalName("a.nocap.local.evil.com") || IsLocalName("xnocap.local") {
		t.Error("IsLocalName")
	}
}

func TestCAIsNameConstrained(t *testing.T) {
	dir := t.TempDir()
	ca, err := LoadOrCreateCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := ca.Certificate()
	if !c.IsCA || !c.PermittedDNSDomainsCritical || len(c.PermittedDNSDomains) != 1 || c.PermittedDNSDomains[0] != "nocap.local" {
		t.Fatalf("CA constraints: critical=%v permitted=%v", c.PermittedDNSDomainsCritical, c.PermittedDNSDomains)
	}
	if len(c.ExcludedIPRanges) != 2 {
		t.Errorf("IP addresses must be excluded: %v", c.ExcludedIPRanges)
	}
	if !c.MaxPathLenZero {
		t.Error("the CA must not be able to make other CAs")
	}
	roots := x509.NewCertPool()
	roots.AddCert(c)

	// A local name verifies.
	leaf, err := ca.Leaf("api.myapp.nocap.local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := leaf.Leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "api.myapp.nocap.local"}); err != nil {
		t.Errorf("a local name should verify: %v", err)
	}
	if again, _ := ca.Leaf("API.myapp.nocap.local."); again != leaf {
		t.Error("leaves are cached per name")
	}

	// The CA refuses to issue for anything else.
	for _, n := range []string{"example.com", "nocap.local", "nocap.local.evil.com", "192.168.1.5"} {
		if _, err := ca.Leaf(n); err == nil {
			t.Errorf("Leaf(%q) should be refused", n)
		}
	}

	// Even a certificate signed with the CA's own key for another name
	// (as a thief could) doesn't verify, thanks to the constraint.
	for _, forged := range []*x509.Certificate{
		{DNSNames: []string{"bank.example.com"}},
		{DNSNames: []string{"x.nocap.local", "bank.example.com"}},
		{IPAddresses: []net.IP{net.IPv4(192, 168, 1, 1)}},
	} {
		forged.SerialNumber = big.NewInt(42)
		forged.Subject = pkix.Name{CommonName: "forged"}
		forged.NotBefore, forged.NotAfter = time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
		forged.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		der, err := x509.CreateCertificate(rand.Reader, forged, c, &ca.key.PublicKey, ca.key)
		if err != nil {
			t.Fatal(err)
		}
		fc, _ := x509.ParseCertificate(der)
		if _, err := fc.Verify(x509.VerifyOptions{Roots: roots}); err == nil {
			t.Errorf("a forged certificate for %v %v verified", forged.DNSNames, forged.IPAddresses)
		}
	}

	// It's loaded again from disk, not made anew.
	again, err := LoadOrCreateCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(again.CertPEM()) != string(ca.CertPEM()) {
		t.Error("the CA should be kept across restarts")
	}
}

func TestTableAndProxy(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || strings.Contains(r.Header.Get("Cookie"), "alfa_refresh") {
			t.Error("NoCapOS credentials must not reach the app")
		}
		_, _ = w.Write([]byte("hello from " + r.Host + " " + r.Header.Get("Cookie")))
	}))
	defer backend.Close()
	target := strings.TrimPrefix(backend.URL, "http://")

	tbl := NewTable()
	tbl.Set([]Route{
		{Name: "api.shop.nocap.local", Container: "shop", ID: "1", Target: target, Source: "label"},
		{Name: "api.shop.nocap.local", Container: "other", ID: "2", Target: "127.0.0.1:1", Source: "container"},
		{Name: "evil.com", Container: "x", ID: "3", Target: target},
		{Name: "empty.nocap.local", Container: "y", ID: "4"},
	})
	if len(tbl.All()) != 1 {
		t.Fatalf("only valid, first-wins routes: %+v", tbl.All())
	}
	if r, ok := tbl.Lookup("API.Shop.nocap.local:8443"); !ok || r.Container != "shop" {
		t.Errorf("lookup ignores case and port: %+v %v", r, ok)
	}

	lan := true
	p := NewProxy(tbl, func(*http.Request) bool { return lan })
	req := httptest.NewRequest(http.MethodGet, "http://api.shop.nocap.local/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Cookie", "alfa_refresh=abc; app_session=keep")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "hello from api.shop.nocap.local app_session=keep") {
		t.Errorf("proxied: %d %q", rec.Code, rec.Body.String())
	}

	lan = false
	rec = httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://api.shop.nocap.local/", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("outside the LAN: %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://nope.nocap.local/", nil))
	if rec.Code != http.StatusNotFound || p.Handles(httptest.NewRequest(http.MethodGet, "http://nocap.local/", nil)) {
		t.Errorf("unknown names: %d", rec.Code)
	}
}
