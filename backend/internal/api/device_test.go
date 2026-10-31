package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"alfaos/alfad/internal/appstore"
	"alfaos/alfad/internal/spaces"
	"alfaos/alfad/internal/store"
)

// claim posts to /pair/claim from a client address.
func claim(t *testing.T, s *Server, ip string, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "/api/v1/pair/claim", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = ip + ":40000"
	rec := httptest.NewRecorder()
	s.pairClaim(rec, r)
	return rec
}

// asDevice calls h through the device middleware with a token.
func asDevice(s *Server, h http.HandlerFunc, method, target, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Device "+token)
	}
	r.Host = "nocap.local:8443"
	rec := httptest.NewRecorder()
	s.device(h).ServeHTTP(rec, r)
	return rec
}

func newCode(t *testing.T, w *authWorld, as *store.User) pairCodeResponse {
	t.Helper()
	rec := w.do(t, w.s.devicesPairCode, as, "POST", "/api/v1/devices/pair-code", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("pair-code: %d %s", rec.Code, rec.Body)
	}
	var pc pairCodeResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &pc)
	return pc
}

func TestDevicePairingFlow(t *testing.T) {
	w := newAuthWorld(t)
	w.s.Config.DataDir = t.TempDir()
	alex := w.users["alex"]

	// Public info: a stable NoCap ID.
	rec := w.do(t, w.s.pairInfo, nil, "GET", "/api/v1/pair/info", nil)
	var info pairInfo
	_ = json.Unmarshal(rec.Body.Bytes(), &info)
	if rec.Code != http.StatusOK || !strings.HasPrefix(info.NocapID, "NC-") || len(info.NocapID) != 12 {
		t.Fatalf("info: %d %s", rec.Code, rec.Body)
	}

	pc := newCode(t, w, alex)
	if len(pc.Code) != 6 || pc.NocapID != info.NocapID || time.Until(pc.ExpiresAt) > 5*time.Minute+time.Second {
		t.Fatalf("pair code: %+v", pc)
	}
	u, err := url.Parse(pc.PairURL)
	if err != nil || u.Scheme != "nocap" || u.Host != "pair" || u.Query().Get("server") != "http://example.com" || u.Query().Get("code") != pc.Code || u.Query().Get("id") != info.NocapID {
		t.Fatalf("pair url %q", pc.PairURL)
	}

	// Validation.
	if rec := claim(t, w.s, "10.1.1.1", map[string]string{"code": pc.Code, "device_name": "", "platform": "android"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty name: %d", rec.Code)
	}
	if rec := claim(t, w.s, "10.1.1.1", map[string]string{"code": pc.Code, "device_name": "Pixel", "platform": "toaster"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad platform: %d", rec.Code)
	}

	// Happy path.
	rec = claim(t, w.s, "10.1.1.1", map[string]string{"code": pc.Code, "device_name": "Pixel 8", "platform": "android"})
	var cr claimResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &cr)
	if rec.Code != http.StatusCreated || cr.Token == "" || cr.DeviceID == "" || cr.NocapID != info.NocapID || cr.User["username"] != "alex" {
		t.Fatalf("claim: %d %s", rec.Code, rec.Body)
	}
	// Single use.
	if rec := claim(t, w.s, "10.1.1.1", map[string]string{"code": pc.Code, "device_name": "Pixel 8", "platform": "android"}); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "code_expired") {
		t.Fatalf("reused code: %d %s", rec.Code, rec.Body)
	}

	// The token works.
	rec = asDevice(w.s, w.s.deviceMe, "GET", "/api/v1/device/me", cr.Token, "")
	var me struct {
		Device map[string]string `json:"device"`
		User   map[string]string `json:"user"`
		Server pairInfo          `json:"server"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &me)
	if rec.Code != http.StatusOK || me.Device["id"] != cr.DeviceID || me.Device["platform"] != "android" || me.User["username"] != "alex" || me.Server.NocapID != info.NocapID {
		t.Fatalf("me: %d %s", rec.Code, rec.Body)
	}

	// Heartbeat.
	if rec := asDevice(w.s, w.s.deviceHeartbeat, "POST", "/api/v1/device/heartbeat", cr.Token, `{"battery":87,"app_version":"1.0.0"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("heartbeat: %d %s", rec.Code, rec.Body)
	}
	if rec := asDevice(w.s, w.s.deviceHeartbeat, "POST", "/api/v1/device/heartbeat", cr.Token, `{"battery":187}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad battery: %d", rec.Code)
	}
	if rec := asDevice(w.s, w.s.deviceHeartbeat, "POST", "/api/v1/device/heartbeat", cr.Token, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("empty heartbeat: %d %s", rec.Code, rec.Body)
	}

	// Listing: alex sees theirs, another user nothing, admins everyone's.
	rec = w.do(t, w.s.devicesList, alex, "GET", "/api/v1/devices", nil)
	var list []deviceJSON
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].Battery == nil || *list[0].Battery != 87 || list[0].AppVersion != "1.0.0" || list[0].LastSeenAt == nil || list[0].Username != "" {
		t.Fatalf("list: %s", rec.Body)
	}
	rec = w.do(t, w.s.devicesList, w.users["boss"], "GET", "/api/v1/devices", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].Username != "alex" {
		t.Fatalf("admin list: %s", rec.Body)
	}

	// Wrong and missing tokens.
	if rec := asDevice(w.s, w.s.deviceMe, "GET", "/api/v1/device/me", "nope", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", rec.Code)
	}
	if rec := asDevice(w.s, w.s.deviceMe, "GET", "/api/v1/device/me", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}

	// A disabled owner locks the device out; re-enabling lets it back in.
	ctx := context.Background()
	_ = w.st.SetDisabled(ctx, alex.ID, true)
	if rec := asDevice(w.s, w.s.deviceMe, "GET", "/api/v1/device/me", cr.Token, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled owner: %d", rec.Code)
	}
	_ = w.st.SetDisabled(ctx, alex.ID, false)

	// Someone else can't unlink it; its owner can, and it stops at once.
	req := func(as *store.User) int {
		r := httptest.NewRequest("DELETE", "/api/v1/devices/"+cr.DeviceID, nil)
		r.SetPathValue("id", cr.DeviceID)
		r = r.WithContext(context.WithValue(r.Context(), userKey, as))
		rec := httptest.NewRecorder()
		w.s.devicesRevoke(rec, r)
		return rec.Code
	}
	other := &store.User{ID: store.NewID(), Username: "sam", PasswordHash: "x", Role: store.RoleUser, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	_ = w.st.CreateUser(ctx, other)
	if code := req(other); code != http.StatusNotFound {
		t.Fatalf("other user's revoke: %d", code)
	}
	if code := req(alex); code != http.StatusNoContent {
		t.Fatalf("revoke: %d", code)
	}
	if rec := asDevice(w.s, w.s.deviceMe, "GET", "/api/v1/device/me", cr.Token, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked device: %d", rec.Code)
	}
}

func TestPairClaimWrongGuesses(t *testing.T) {
	w := newAuthWorld(t)
	w.s.Config.DataDir = t.TempDir()
	pc := newCode(t, w, w.users["alex"])
	wrong := "000000"
	if wrong == pc.Code {
		wrong = "000001"
	}
	for i := range 5 {
		rec := claim(t, w.s, "10.2.2.2", map[string]string{"code": wrong, "device_name": "Pixel", "platform": "android"})
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "wrong_code") {
			t.Fatalf("guess %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	// Blocked, even with the right code.
	if rec := claim(t, w.s, "10.2.2.2", map[string]string{"code": pc.Code, "device_name": "Pixel", "platform": "android"}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("blocked client: %d %s", rec.Code, rec.Body)
	}
	// The code still works from elsewhere.
	if rec := claim(t, w.s, "10.3.3.3", map[string]string{"code": pc.Code, "device_name": "Pixel", "platform": "android"}); rec.Code != http.StatusCreated {
		t.Fatalf("other client: %d %s", rec.Code, rec.Body)
	}
}

func TestDeviceAppList(t *testing.T) {
	catalog := []appstore.App{
		{ID: "jelly", Name: "Jelly", Logo: "/app-icons/jelly.svg", Web: &appstore.Web{Service: "web", Port: 8096}},
		{ID: "dash", Name: "Dash", Web: &appstore.Web{Service: "web", Port: 80, Path: "/admin/"}},
		{ID: "own", Name: "Own Address", Web: &appstore.Web{Service: "web", Port: 80}},
		{ID: "secret", Name: "Secret", Web: &appstore.Web{Service: "web", Port: 80}},
		{ID: "headless", Name: "No UI"},
		{ID: "absent", Name: "Not installed", Web: &appstore.Web{Service: "web", Port: 80}},
	}
	status := map[string]*appstore.Installed{
		"jelly":    {WebPort: 30001},
		"dash":     {WebPort: 30002},
		"own":      {WebPort: 30003, WebURL: "https://own.example.com/"},
		"secret":   {WebPort: 30004},
		"headless": {},
	}
	rules := map[string]*store.AppAccess{"secret": {Users: []string{"boss"}}}
	got := deviceAppList("https://nocap.local:8443", "nocap.local", catalog, status, rules, spaces.Viewer{Name: "alex"})
	want := []deviceApp{
		{ID: "nocapos", Name: "NoCapOS", IconURL: "https://nocap.local:8443/brand/mark-512.png", URL: "https://nocap.local:8443/"},
		{ID: "jelly", Name: "Jelly", IconURL: "https://nocap.local:8443/app-icons/jelly.svg", URL: "http://nocap.local:30001/"},
		{ID: "dash", Name: "Dash", IconURL: "", URL: "http://nocap.local:30002/admin/"},
		{ID: "own", Name: "Own Address", IconURL: "", URL: "https://own.example.com/"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("app %d: got %+v want %+v", i, got[i], want[i])
		}
	}
	// An allowed person sees the restricted app too.
	if all := deviceAppList("http://h", "h", catalog, status, rules, spaces.Viewer{Name: "boss"}); len(all) != 5 {
		t.Fatalf("boss: %+v", all)
	}

	// Through the handler (no App Store): just NoCapOS, as JSON.
	w := newAuthWorld(t)
	w.s.Config.DataDir = t.TempDir()
	pc := newCode(t, w, w.users["alex"])
	rec := claim(t, w.s, "10.4.4.4", map[string]string{"code": pc.Code, "device_name": "Pixel", "platform": "android"})
	var cr claimResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &cr)
	rec = asDevice(w.s, w.s.deviceApps, "GET", "/api/v1/device/apps", cr.Token, "")
	var apps []map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &apps)
	if rec.Code != http.StatusOK || len(apps) != 1 || apps[0]["id"] != "nocapos" || apps[0]["url"] != "http://nocap.local:8443/" || apps[0]["icon_url"] != "http://nocap.local:8443/brand/mark-512.png" {
		t.Fatalf("apps: %d %s", rec.Code, rec.Body)
	}
}

func TestRequestHostname(t *testing.T) {
	for host, want := range map[string]string{"nocap.local:8443": "nocap.local", "10.0.0.5": "10.0.0.5", "[fe80::1]:8443": "[fe80::1]", "[::1]": "[::1]"} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Host = host
		if got := requestHostname(r); got != want {
			t.Errorf("%s: %s, want %s", host, got, want)
		}
	}
}
