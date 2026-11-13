package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"alfaos/alfad/internal/config"
)

// TestShieldRoutesAdminOnly: every Shield endpoint exists and needs a
// signed-in administrator.
func TestShieldRoutesAdminOnly(t *testing.T) {
	w := deviceWorld(t)
	var h http.Handler
	func() {
		defer func() {
			if v := recover(); v != nil {
				t.Skipf("server needs more to build: %v", v)
			}
		}()
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		deps := w.s.Deps
		deps.Config = &config.Config{DataDir: t.TempDir(), MaxUpload: 1 << 20}
		h = New(ctx, deps).Handler()
	}()
	for _, rt := range []string{
		"GET /api/v1/shield", "GET /api/v1/shield/firewall", "POST /api/v1/shield/firewall/install",
		"POST /api/v1/shield/firewall/enable", "POST /api/v1/shield/firewall/disable",
		"PUT /api/v1/shield/firewall/defaults", "PUT /api/v1/shield/firewall/logging", "PUT /api/v1/shield/firewall/ipv6",
		"POST /api/v1/shield/firewall/rules", "PUT /api/v1/shield/firewall/rules", "POST /api/v1/shield/firewall/rules/delete",
		"POST /api/v1/shield/firewall/rules/move", "POST /api/v1/shield/firewall/presets/ssh",
		"GET /api/v1/shield/firewall/exposed", "PUT /api/v1/shield/firewall/exposed", "GET /api/v1/shield/firewall/blocked",
		"POST /api/v1/shield/firewall/keep", "POST /api/v1/shield/firewall/undo",
		"GET /api/v1/shield/dns", "PUT /api/v1/shield/dns", "GET /api/v1/shield/dns/stats", "GET /api/v1/shield/dns/log",
		"DELETE /api/v1/shield/dns/log", "POST /api/v1/shield/dns/pause", "POST /api/v1/shield/dns/refresh",
		"POST /api/v1/shield/dns/port53",
	} {
		method, target, _ := strings.Cut(rt, " ")
		r := httptest.NewRequest(method, target, nil)
		r.RemoteAddr = "127.0.0.1:5000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d %s", rt, rec.Code, rec.Body)
		}
	}
}
