package tlsutil

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRedirectHandler(t *testing.T) {
	cases := []struct {
		port, host, path, want string
		code                   int
	}{
		{"443", "nocap.local", "/", "https://nocap.local/", http.StatusMovedPermanently},
		{"443", "nocap.local:80", "/files?x=1", "https://nocap.local/files?x=1", http.StatusMovedPermanently},
		{"8443", "192.168.1.20", "/", "https://192.168.1.20:8443/", http.StatusMovedPermanently},
		{"443", "[fd00::2]:80", "/", "https://[fd00::2]/", http.StatusMovedPermanently},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, c.path, nil)
		req.Host = c.host
		rec := httptest.NewRecorder()
		RedirectHandler(c.port).ServeHTTP(rec, req)
		if rec.Code != c.code || rec.Header().Get("Location") != c.want {
			t.Errorf("%s%s → %d %q, want %d %q", c.host, c.path, rec.Code, rec.Header().Get("Location"), c.code, c.want)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/x", nil)
	req.Host = "nocap.local"
	rec := httptest.NewRecorder()
	RedirectHandler("443").ServeHTTP(rec, req)
	if rec.Code != http.StatusPermanentRedirect {
		t.Errorf("POST should keep its method: got %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "evil.com@x"
	rec = httptest.NewRecorder()
	RedirectHandler("443").ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("odd host should be refused, got %d", rec.Code)
	}
}
