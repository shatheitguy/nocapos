package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"alfaos/alfad/internal/auth"
	"alfaos/alfad/internal/nativeapps"
	"alfaos/alfad/internal/webapps"
)

type apiFakeStreams struct {
	mu     sync.Mutex
	opened []string
}

func (f *apiFakeStreams) Open(userID string, spec webapps.StreamSpec) (webapps.StreamStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, userID+" "+spec.Key)
	return webapps.StreamStatus{ID: "0123456789abcdef01234567", Key: spec.Key, Phase: webapps.PhaseStarting}, nil
}

func (f *apiFakeStreams) StopKey(string) {}

type nativeWorld struct {
	*authWorld
	mux     *http.ServeMux
	tokens  map[string]string
	ran     chan []string
	streams *apiFakeStreams
}

func newNativeWorld(t *testing.T, native bool) *nativeWorld {
	t.Helper()
	w := &nativeWorld{authWorld: newAuthWorld(t), tokens: map[string]string{}, ran: make(chan []string, 10), streams: &apiFakeStreams{}}
	svc := nativeapps.New(t.TempDir(), w.streams)
	svc.Native = func() bool { return native }
	svc.HasFlatpak = func() bool { return true }
	svc.Run = func(_ context.Context, args ...string) (string, error) {
		if len(args) > 1 && args[1] == "list" {
			return "org.gimp.GIMP\tGIMP\t3.0\n", nil
		}
		return "", errors.New("offline")
	}
	svc.ReadFile = func(string) ([]byte, error) { return nil, errors.New("none") }
	svc.Account = func(string) *webapps.RunAs { return nil }
	svc.Jobs.Exec = func(_ context.Context, args []string, out io.Writer) error {
		w.ran <- args
		return nil
	}
	w.s.Native = svc
	w.s.Streams = webapps.NewStreams(slog.New(slog.NewTextHandler(io.Discard, nil)), t.TempDir())
	t.Cleanup(w.s.Streams.Close)
	w.mux = http.NewServeMux()
	w.s.nativeRoutes(w.mux)
	for _, name := range []string{"boss", "alex"} {
		sess, err := w.s.Auth.Login(context.Background(), name, pw, auth.ClientMeta{})
		if err != nil {
			t.Fatal(err)
		}
		w.tokens[name] = sess.AccessToken
	}
	return w
}

func (w *nativeWorld) call(t *testing.T, who, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	if who != "" {
		r.Header.Set("Authorization", "Bearer "+w.tokens[who])
	}
	rec := httptest.NewRecorder()
	w.mux.ServeHTTP(rec, r)
	return rec
}

func TestNativeAppsAdminVsUser(t *testing.T) {
	w := newNativeWorld(t, true)

	// Signed out: nothing.
	if rec := w.call(t, "", "GET", "/api/v1/native-apps/installed"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous installed: %d", rec.Code)
	}
	// People may list and open installed apps…
	rec := w.call(t, "alex", "GET", "/api/v1/native-apps/installed")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "org.gimp.GIMP") || strings.Contains(rec.Body.String(), `"jobs"`) {
		t.Fatalf("user installed: %d %s", rec.Code, rec.Body)
	}
	rec = w.call(t, "alex", "POST", "/api/v1/native-apps/org.gimp.GIMP/launch")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"where":"stream"`) {
		t.Fatalf("user launch: %d %s", rec.Code, rec.Body)
	}
	if c := rec.Result().Cookies(); len(c) != 1 || c[0].Name != nativeCookie || c[0].Path != nativePrefix || !c[0].HttpOnly {
		t.Fatalf("launch cookie = %+v", c)
	}
	// …but not install, remove, browse or watch jobs.
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/v1/native-apps/org.kde.krita/install"},
		{"POST", "/api/v1/native-apps/org.gimp.GIMP/uninstall"},
		{"GET", "/api/v1/native-apps/popular"},
		{"GET", "/api/v1/native-apps/search?q=gimp"},
		{"GET", "/api/v1/native-apps/details/org.gimp.GIMP"},
		{"GET", "/api/v1/native-apps/jobs"},
	} {
		if rec := w.call(t, "alex", c.method, c.path); rec.Code != http.StatusForbidden {
			t.Errorf("user %s %s: %d", c.method, c.path, rec.Code)
		}
	}

	// Admins can, and it's audited.
	rec = w.call(t, "boss", "POST", "/api/v1/native-apps/org.kde.krita/install")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("admin install: %d %s", rec.Code, rec.Body)
	}
	var j nativeapps.Job
	_ = json.Unmarshal(rec.Body.Bytes(), &j)
	if j.App != "org.kde.krita" || j.Action != "install" {
		t.Fatalf("job = %+v", j)
	}
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case args := <-w.ran:
			got[strings.Join(args, " ")] = true
		case <-time.After(5 * time.Second):
			t.Fatal("install never ran")
		}
	}
	if !got["flatpak install -y --noninteractive --system flathub org.kde.krita"] {
		t.Fatalf("ran %v", got)
	}
	// Let the job finish (and audit) before the store closes.
	for i := 0; w.s.Native.Jobs.Busy(); i++ {
		if i > 500 {
			t.Fatal("job never finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rec := w.call(t, "boss", "POST", "/api/v1/native-apps/org.kde.krita;reboot/install"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d", rec.Code)
	}
}

func TestNativeAppsUnsupportedHost(t *testing.T) {
	w := newNativeWorld(t, false)
	rec := w.call(t, "alex", "GET", "/api/v1/native-apps/installed")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"supported":false`) || !strings.Contains(rec.Body.String(), `"apps":[]`) {
		t.Fatalf("installed: %d %s", rec.Code, rec.Body)
	}
	if rec := w.call(t, "boss", "POST", "/api/v1/native-apps/org.kde.krita/install"); rec.Code != http.StatusNotImplemented {
		t.Fatalf("install: %d", rec.Code)
	}
	if rec := w.call(t, "boss", "GET", "/api/v1/native-apps/popular"); rec.Code != http.StatusNotImplemented {
		t.Fatalf("popular: %d", rec.Code)
	}
}

func TestNativeStreamProxyNeedsOwnersCookie(t *testing.T) {
	w := newNativeWorld(t, true)
	id := "0123456789abcdef01234567"
	// No cookie.
	if rec := w.call(t, "", "GET", nativePrefix+id+"/vnc.html"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no cookie: %d", rec.Code)
	}
	// Junk ids never reach the stream table.
	if rec := w.call(t, "", "GET", nativePrefix+"..%2f..%2fetc/vnc.html"); rec.Code != http.StatusNotFound {
		t.Fatalf("junk id: %d", rec.Code)
	}
	// A valid cookie for a stream that isn't this person's.
	r := httptest.NewRequest("GET", nativePrefix+id+"/vnc.html", nil)
	r.AddCookie(&http.Cookie{Name: nativeCookie, Value: w.s.signBrave(w.users["alex"].ID, "", time.Now().Add(time.Minute))})
	rec := httptest.NewRecorder()
	w.mux.ServeHTTP(rec, r)
	if rec.Code == http.StatusOK {
		t.Fatalf("reached a stream that isn't there: %d", rec.Code)
	}
	// Brave's cookie doesn't open app streams.
	r = httptest.NewRequest("GET", nativePrefix+id+"/vnc.html", nil)
	r.AddCookie(&http.Cookie{Name: braveCookie, Value: w.s.signBrave(w.users["alex"].ID, "", time.Now().Add(time.Minute))})
	rec = httptest.NewRecorder()
	w.mux.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("brave cookie: %d", rec.Code)
	}
}
