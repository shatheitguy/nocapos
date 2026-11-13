package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"alfaos/alfad/internal/store"
)

// mirrorCall runs a mirror handler as a person, with the {id} path value.
func mirrorCall(s *Server, h http.HandlerFunc, as *store.User, method, target, id string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.SetPathValue("id", id)
	r = r.WithContext(context.WithValue(r.Context(), userKey, as))
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

// linkPhone links a phone to as and returns its id and token.
func linkPhone(t *testing.T, w *authWorld, as *store.User, name string) claimResponse {
	t.Helper()
	pc := newCode(t, w, as)
	rec := claim(t, w.s, "10.1.1.9", map[string]string{"code": pc.Code, "device_name": name, "platform": "android"})
	var cr claimResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &cr)
	if rec.Code != http.StatusCreated {
		t.Fatalf("claim: %d %s", rec.Code, rec.Body)
	}
	return cr
}

func TestMirrorOnlyTheOwner(t *testing.T) {
	w := newAuthWorld(t)
	w.s.Config.DataDir = t.TempDir()
	alex, boss := w.users["alex"], w.users["boss"]
	phone := linkPhone(t, w, alex, "Alex's phone")
	bossPhone := linkPhone(t, w, boss, "Boss phone")

	// Each person lists only their own phones; administrators too.
	for _, c := range []struct {
		u    *store.User
		want string
	}{{alex, phone.DeviceID}, {boss, bossPhone.DeviceID}} {
		rec := w.do(t, w.s.mirrorDevices, c.u, "GET", "/api/v1/mirror/phones", nil)
		var list []mirrorDeviceJSON
		_ = json.Unmarshal(rec.Body.Bytes(), &list)
		if rec.Code != http.StatusOK || len(list) != 1 || list[0].ID != c.want {
			t.Fatalf("%s list: %d %s", c.u.Username, rec.Code, rec.Body)
		}
	}

	// An administrator can't open, or ask for, someone else's phone.
	for _, h := range []http.HandlerFunc{w.s.mirrorTicket, w.s.mirrorAsk} {
		if rec := mirrorCall(w.s, h, boss, "POST", "/api/v1/mirror/phones/x/ticket", phone.DeviceID); rec.Code != http.StatusNotFound {
			t.Fatalf("admin on alex's phone: %d %s", rec.Code, rec.Body)
		}
		if rec := mirrorCall(w.s, h, alex, "POST", "/api/v1/mirror/phones/x/ticket", bossPhone.DeviceID); rec.Code != http.StatusNotFound {
			t.Fatalf("alex on boss's phone: %d %s", rec.Code, rec.Body)
		}
		if rec := mirrorCall(w.s, h, alex, "POST", "/api/v1/mirror/phones/x/ticket", "nope"); rec.Code != http.StatusNotFound {
			t.Fatalf("unknown phone: %d %s", rec.Code, rec.Body)
		}
	}
	if w.s.mirrorHub().AskSince(phone.DeviceID, 0) != nil {
		t.Fatal("a refused request still asked the phone")
	}

	// The owner gets a ticket bound to them and the phone, and the phone is asked.
	rec := mirrorCall(w.s, w.s.mirrorTicket, alex, "POST", "/api/v1/mirror/phones/x/ticket", phone.DeviceID)
	var tk struct {
		Ticket string `json:"ticket"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &tk)
	if rec.Code != http.StatusOK || tk.Ticket == "" {
		t.Fatalf("owner ticket: %d %s", rec.Code, rec.Body)
	}
	got, ok := w.s.mirrorTickets().Redeem(tk.Ticket)
	if !ok || got.UserID != alex.ID || got.DeviceID != phone.DeviceID {
		t.Fatalf("ticket: %+v %v", got, ok)
	}
	if _, again := w.s.mirrorTickets().Redeem(tk.Ticket); again {
		t.Fatal("ticket worked twice")
	}
	ask := w.s.mirrorHub().AskSince(phone.DeviceID, 0)
	if ask == nil {
		t.Fatal("the phone wasn't asked")
	}

	// The phone sees the ask on its long-poll, even on a fresh start.
	rec = asDevice(w.s, w.s.deviceEvents, "GET", "/api/v1/device/events?after=0&wait=0", phone.Token, "")
	var ev struct {
		Mirror *struct {
			ID string `json:"id"`
		} `json:"mirror"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &ev)
	if rec.Code != http.StatusOK || ev.Mirror == nil || ev.Mirror.ID != ask.ID {
		t.Fatalf("events: %d %s", rec.Code, rec.Body)
	}
	// Someone else's phone doesn't.
	rec = asDevice(w.s, w.s.deviceEvents, "GET", "/api/v1/device/events?after=0&wait=0", bossPhone.Token, "")
	if strings.Contains(rec.Body.String(), `"mirror"`) {
		t.Fatalf("boss's phone got alex's ask: %s", rec.Body)
	}

	// Declining on the phone clears the ask and tells the windows.
	if rec := asDevice(w.s, w.s.mirrorPhoneState, "POST", "/api/v1/device/mirror/state", phone.Token, `{"state":"denied"}`); rec.Code != http.StatusOK {
		t.Fatalf("state: %d %s", rec.Code, rec.Body)
	}
	if rec := asDevice(w.s, w.s.mirrorPhoneState, "POST", "/api/v1/device/mirror/state", phone.Token, `{"state":"hacked"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad state: %d", rec.Code)
	}
	if w.s.mirrorHub().AskSince(phone.DeviceID, 0) != nil {
		t.Fatal("ask kept after denied")
	}

	// Unlinked phones can't be opened anymore.
	if rec := asDevice(w.s, w.s.deviceUnlink, "DELETE", "/api/v1/device/me", phone.Token, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("unlink: %d", rec.Code)
	}
	if rec := mirrorCall(w.s, w.s.mirrorTicket, alex, "POST", "/api/v1/mirror/phones/x/ticket", phone.DeviceID); rec.Code != http.StatusNotFound {
		t.Fatalf("unlinked phone: %d %s", rec.Code, rec.Body)
	}
}

func TestMirrorLink(t *testing.T) {
	for ip, want := range map[string]string{
		"192.168.1.20": "nearby", "10.0.0.5": "nearby", "127.0.0.1": "nearby", "fd00::1": "nearby",
		"fe80::1": "nearby", "8.8.8.8": "remote", "2001:4860::1": "remote", "": "remote",
	} {
		if got := mirrorLink(ip); got != want {
			t.Errorf("%q: %s, want %s", ip, got, want)
		}
	}
}
