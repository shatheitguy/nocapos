package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"alfaos/alfad/internal/store"
)

const testUUID = "11111111-1111-4111-8111-111111111111"

func wifiShareReq(t *testing.T, w *authWorld, as *store.User, password string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(map[string]string{"password": password})
	r := httptest.NewRequest("POST", "/api/v1/system/wifi/saved/"+testUUID+"/share", &buf)
	r.Header.Set("Content-Type", "application/json")
	r.SetPathValue("uuid", testUUID)
	r = r.WithContext(context.WithValue(r.Context(), userKey, as))
	rec := httptest.NewRecorder()
	w.s.wifiSavedShare(rec, r)
	return rec
}

// Revealing a Wi-Fi password needs the admin's own password again, and
// guessing it is cut off after a few misses.
func TestWiFiShareNeedsReauth(t *testing.T) {
	w := newAuthWorld(t)
	boss := w.users["boss"]
	t.Cleanup(func() {
		reauthLimit.Lock()
		delete(reauthLimit.misses, boss.ID)
		reauthLimit.Unlock()
	})
	if rec := wifiShareReq(t, w, boss, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("no password: %d %s", rec.Code, rec.Body)
	}
	if rec := wifiShareReq(t, w, boss, "wrong-password"); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong password: %d %s", rec.Code, rec.Body)
	}
	// The right password gets past re-authentication (this test host has no
	// NetworkManager, so the share itself answers "unsupported").
	if rec := wifiShareReq(t, w, boss, pw); rec.Code == http.StatusForbidden {
		t.Fatalf("right password refused: %d %s", rec.Code, rec.Body)
	}
	for i := 0; i < reauthMaxMisses; i++ {
		wifiShareReq(t, w, boss, "guess")
	}
	if rec := wifiShareReq(t, w, boss, pw); rec.Code != http.StatusForbidden {
		t.Fatalf("after %d misses the right password should wait: %d", reauthMaxMisses, rec.Code)
	}
	// A non-admin never passes, even with their own password.
	if w.s.reauth(httptest.NewRequest("GET", "/", nil), w.users["alex"], pw) {
		t.Fatal("non-admin re-authenticated")
	}
}

func TestDeviceWiFiShareAdminOnly(t *testing.T) {
	w := newAuthWorld(t)
	rec := w.do(t, w.s.deviceWifiShare, w.users["alex"], "POST", "/api/v1/device/wifi/share", map[string]string{"uuid": testUUID, "password": pw})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin phone: %d %s", rec.Code, rec.Body)
	}
	rec = w.do(t, w.s.deviceWifiSaved, w.users["alex"], "GET", "/api/v1/device/wifi/saved", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin list: %d %s", rec.Code, rec.Body)
	}
}
