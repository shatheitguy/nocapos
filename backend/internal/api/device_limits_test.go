package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"alfaos/alfad/internal/devices"
	"alfaos/alfad/internal/store"
)

func limitsOf(t *testing.T, w *authWorld, as *store.User) deviceLimitsJSON {
	t.Helper()
	rec := w.do(t, w.s.devicesLimits, as, "GET", "/api/v1/devices/limits", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("limits: %d %s", rec.Code, rec.Body)
	}
	var out deviceLimitsJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// withPath runs h with a path value set, as the mux would.
func withPath(t *testing.T, w *authWorld, h http.HandlerFunc, as *store.User, method, key, val string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return w.do(t, func(rw http.ResponseWriter, r *http.Request) {
		r.SetPathValue(key, val)
		h(rw, r)
	}, as, method, "/api/v1/devices/x", body)
}

func pairOne(t *testing.T, w *authWorld, as *store.User, name string) *httptest.ResponseRecorder {
	t.Helper()
	pc := newCode(t, w, as)
	return claim(t, w.s, "10.2.2.2", map[string]string{"code": pc.Code, "device_name": name, "platform": "android", "model": "Pixel 8", "os_version": "Android 15"})
}

func TestDeviceLimitDefaultAndOverride(t *testing.T) {
	w := newAuthWorld(t)
	w.s.Config.DataDir = t.TempDir()
	alex, boss := w.users["alex"], w.users["boss"]

	l := limitsOf(t, w, alex)
	if l.Used != 0 || l.Limit != devices.DefaultLimit || l.Default != devices.DefaultLimit || l.Users != nil || l.Total != nil {
		t.Fatalf("fresh limits: %+v", l)
	}

	for _, bad := range []any{map[string]int{"default": -1}, map[string]int{"default": devices.MaxLimit + 1}, map[string]any{}} {
		if rec := w.do(t, w.s.devicesSetDefaultLimit, boss, "PUT", "/api/v1/devices/limits", bad); rec.Code != http.StatusBadRequest {
			t.Fatalf("bad default %v: %d", bad, rec.Code)
		}
	}
	if rec := w.do(t, w.s.devicesSetDefaultLimit, boss, "PUT", "/api/v1/devices/limits", map[string]int{"default": 2}); rec.Code != http.StatusNoContent {
		t.Fatalf("set default: %d %s", rec.Code, rec.Body)
	}

	// alex may link two.
	for _, n := range []string{"Phone A", "Phone B"} {
		if rec := pairOne(t, w, alex, n); rec.Code != http.StatusCreated {
			t.Fatalf("pair %s: %d %s", n, rec.Code, rec.Body)
		}
	}
	if l := limitsOf(t, w, alex); l.Used != 2 || l.Limit != 2 {
		t.Fatalf("after two: %+v", l)
	}

	// A third: no pairing code at all.
	rec := w.do(t, w.s.devicesPairCode, alex, "POST", "/api/v1/devices/pair-code", nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "device_limit") || !strings.Contains(rec.Body.String(), `"limit":2`) {
		t.Fatalf("pair-code at limit: %d %s", rec.Code, rec.Body)
	}

	// A per-person override beats the default.
	if rec := withPath(t, w, w.s.devicesSetUserLimit, boss, "PUT", "user", alex.ID, map[string]int{"limit": 3}); rec.Code != http.StatusNoContent {
		t.Fatalf("set override: %d %s", rec.Code, rec.Body)
	}
	if rec := withPath(t, w, w.s.devicesSetUserLimit, boss, "PUT", "user", "nobody", map[string]int{"limit": 3}); rec.Code != http.StatusNotFound {
		t.Fatalf("override unknown user: %d", rec.Code)
	}
	if rec := withPath(t, w, w.s.devicesSetUserLimit, boss, "PUT", "user", alex.ID, map[string]int{"limit": 1000}); rec.Code != http.StatusBadRequest {
		t.Fatalf("override too big: %d", rec.Code)
	}
	if rec := pairOne(t, w, alex, "Tablet"); rec.Code != http.StatusCreated {
		t.Fatalf("pair under override: %d %s", rec.Code, rec.Body)
	}

	// The admin view: per-person counts and the total.
	if rec := pairOne(t, w, boss, "Boss phone"); rec.Code != http.StatusCreated {
		t.Fatalf("boss pair: %d %s", rec.Code, rec.Body)
	}
	al := limitsOf(t, w, boss)
	if al.Total == nil || *al.Total != 4 || len(al.Users) != 3 {
		t.Fatalf("admin limits: %+v", al)
	}
	for _, u := range al.Users {
		switch u.Username {
		case "alex":
			if u.Used != 3 || u.Limit != 3 || u.Override == nil || *u.Override != 3 {
				t.Fatalf("alex row: %+v", u)
			}
		case "boss":
			if u.Used != 1 || u.Limit != 2 || u.Override != nil {
				t.Fatalf("boss row: %+v", u)
			}
		}
	}

	// Back to the default (2): alex is over it, nothing is unlinked, but no
	// more codes.
	if rec := withPath(t, w, w.s.devicesSetUserLimit, boss, "PUT", "user", alex.ID, map[string]any{"limit": nil}); rec.Code != http.StatusNoContent {
		t.Fatalf("clear override: %d", rec.Code)
	}
	if l := limitsOf(t, w, alex); l.Used != 3 || l.Limit != 2 {
		t.Fatalf("after clearing: %+v", l)
	}
	if rec := w.do(t, w.s.devicesPairCode, alex, "POST", "/api/v1/devices/pair-code", nil); rec.Code != http.StatusConflict {
		t.Fatalf("over the limit: %d", rec.Code)
	}

	// 0 = no limit.
	if rec := withPath(t, w, w.s.devicesSetUserLimit, boss, "PUT", "user", alex.ID, map[string]int{"limit": 0}); rec.Code != http.StatusNoContent {
		t.Fatalf("unlimited: %d", rec.Code)
	}
	if rec := pairOne(t, w, alex, "Watch"); rec.Code != http.StatusCreated {
		t.Fatalf("pair unlimited: %d %s", rec.Code, rec.Body)
	}
}

// A code made while there was room can't be redeemed once the limit is hit.
func TestDeviceLimitEnforcedOnClaim(t *testing.T) {
	w := newAuthWorld(t)
	w.s.Config.DataDir = t.TempDir()
	alex, boss := w.users["alex"], w.users["boss"]
	if rec := withPath(t, w, w.s.devicesSetUserLimit, boss, "PUT", "user", alex.ID, map[string]int{"limit": 1}); rec.Code != http.StatusNoContent {
		t.Fatalf("set: %d", rec.Code)
	}
	pc := newCode(t, w, alex)
	// Another device gets linked meanwhile (making a new code would cancel
	// pc, so straight into the store).
	_, hash := devices.NewToken()
	if err := w.st.CreateDevice(context.Background(), &store.Device{ID: store.NewID(), UserID: alex.ID, Name: "First", Platform: "android", Battery: -1}, hash); err != nil {
		t.Fatal(err)
	}
	rec := claim(t, w.s, "10.3.3.3", map[string]string{"code": pc.Code, "device_name": "Second", "platform": "android"})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "device_limit") || !strings.Contains(rec.Body.String(), "Linked Devices") {
		t.Fatalf("claim over limit: %d %s", rec.Code, rec.Body)
	}
	if n, _ := w.st.CountDevices(context.Background(), alex.ID); n != 1 {
		t.Fatalf("devices: %d", n)
	}

	// Unlinking makes room again.
	list, _ := w.st.ListDevices(context.Background(), alex.ID)
	if rec := withPath(t, w, w.s.devicesRevoke, alex, "DELETE", "id", list[0].ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
	}
	if rec := pairOne(t, w, alex, "Second"); rec.Code != http.StatusCreated {
		t.Fatalf("after unlink: %d %s", rec.Code, rec.Body)
	}
}

// Many phones redeeming codes at once still stop at the limit.
func TestDeviceLimitConcurrentClaims(t *testing.T) {
	w := newAuthWorld(t)
	w.s.Config.DataDir = t.TempDir()
	alex, boss := w.users["alex"], w.users["boss"]
	if rec := w.do(t, w.s.devicesSetDefaultLimit, boss, "PUT", "/api/v1/devices/limits", map[string]int{"default": 2}); rec.Code != http.StatusNoContent {
		t.Fatalf("set default: %d", rec.Code)
	}
	// One person has one live code at a time, so race linkDevice itself:
	// it's what every pairing path goes through.
	var wg sync.WaitGroup
	var mu sync.Mutex
	created := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, hash := devices.NewToken()
			d := &store.Device{ID: store.NewID(), UserID: alex.ID, Name: "P", Platform: "android", Battery: -1}
			r := httptest.NewRequest("POST", "/api/v1/pair/claim", nil)
			full, err := w.s.linkDevice(r, alex, d, hash)
			if err != nil {
				t.Error(err)
				return
			}
			if full == nil {
				mu.Lock()
				created++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if created != 2 {
		t.Fatalf("created %d devices, want 2", created)
	}
}

func TestDeviceDetailsRenameAndCaps(t *testing.T) {
	w := newAuthWorld(t)
	w.s.Config.DataDir = t.TempDir()
	alex := w.users["alex"]
	rec := pairOne(t, w, alex, "Pixel")
	var cr claimResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &cr)
	if rec.Code != http.StatusCreated {
		t.Fatalf("pair: %d %s", rec.Code, rec.Body)
	}

	if rec := asDevice(w.s, w.s.deviceHeartbeat, "POST", "/api/v1/device/heartbeat", cr.Token,
		`{"battery":55,"model":"Google Pixel 9","os_version":"Android 16","capabilities":{"notifications":true,"clipboard":false,"files":true,"bogus":true}}`); rec.Code != http.StatusNoContent {
		t.Fatalf("heartbeat: %d %s", rec.Code, rec.Body)
	}
	if rec := asDevice(w.s, w.s.deviceHeartbeat, "POST", "/api/v1/device/heartbeat", cr.Token, `{"model":"`+strings.Repeat("x", 65)+`"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("long model: %d", rec.Code)
	}
	rec = w.do(t, w.s.devicesList, alex, "GET", "/api/v1/devices", nil)
	var list []deviceJSON
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 {
		t.Fatalf("list: %s", rec.Body)
	}
	d := list[0]
	if d.Model != "Google Pixel 9" || d.OSVersion != "Android 16" || strings.Join(d.Caps, ",") != "notifications,files" || !d.Online || d.LastIP == "" || d.UserID != "" {
		t.Fatalf("device: %+v", d)
	}

	// Rename: own device yes, someone else's no (unless admin).
	if rec := withPath(t, w, w.s.devicesRename, alex, "PATCH", "id", d.ID, map[string]string{"name": "  Work phone "}); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"Work phone"`) {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}
	if rec := withPath(t, w, w.s.devicesRename, alex, "PATCH", "id", d.ID, map[string]string{"name": ""}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty name: %d", rec.Code)
	}
	bossPhone := pairOne(t, w, w.users["boss"], "Boss")
	var bc claimResponse
	_ = json.Unmarshal(bossPhone.Body.Bytes(), &bc)
	if rec := withPath(t, w, w.s.devicesRename, alex, "PATCH", "id", bc.DeviceID, map[string]string{"name": "Mine now"}); rec.Code != http.StatusNotFound {
		t.Fatalf("rename someone else's: %d", rec.Code)
	}
	if rec := withPath(t, w, w.s.devicesRename, w.users["boss"], "PATCH", "id", d.ID, map[string]string{"name": "Alex phone"}); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"username":"alex"`) {
		t.Fatalf("admin rename: %d %s", rec.Code, rec.Body)
	}
}
