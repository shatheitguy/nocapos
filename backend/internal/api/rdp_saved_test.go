package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"alfaos/alfad/internal/ai"
)

type savedConnJSON struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Protocol    string     `json:"protocol"`
	Hostname    string     `json:"hostname"`
	Options     rdpOptions `json:"options"`
	HasPassword bool       `json:"has_password"`
	LastUsed    *int64     `json:"last_used"`
}

func rdpWorld(t *testing.T) *spacesWorld {
	t.Helper()
	w := deviceWorld(t)
	box, err := ai.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	w.s.Secrets = box
	return w
}

func TestRemoteConnectionsSavedPerPersonWithSealedPasswords(t *testing.T) {
	w := rdpWorld(t)
	ctx := context.Background()
	body := `{"name":" Office PC ","protocol":"rdp","hostname":"10.0.0.5","username":"me","remember_password":true,"password":"s3cret!",
		"options":{"size_mode":"fixed","fixed_width":1920,"fixed_height":1080,"scale":150,"color_depth":16,"clipboard":"to-remote","audio":true,"speed":"lan","wallpaper":true}}`
	rec := webCall(w, "alex", w.s.remoteConnCreate, "POST", "/api/v1/rdp/connections", body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "s3cret") || strings.Contains(rec.Body.String(), "password_enc") {
		t.Fatalf("password leaked: %s", rec.Body)
	}
	var c savedConnJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil || c.Name != "Office PC" || !c.HasPassword || c.Options.Scale != 150 || c.Options.FixedWidth != 1920 {
		t.Fatalf("saved: %+v %v", c, err)
	}
	// Sealed at rest.
	raw, err := w.s.Store.RemoteConn(ctx, w.users["alex"].ID, c.ID)
	if err != nil || len(raw.PasswordEnc) == 0 || strings.Contains(string(raw.PasswordEnc), "s3cret") {
		t.Fatalf("stored: %+v %v", raw, err)
	}

	// Someone else can't see, use or change it.
	rec = webCall(w, "sam", w.s.remoteConnList, "GET", "/api/v1/rdp/connections", "", nil)
	if strings.Contains(rec.Body.String(), c.ID) {
		t.Fatalf("sam sees alex's computers: %s", rec.Body)
	}
	if rec := webCall(w, "sam", w.s.remoteConnDelete, "DELETE", "/", "", map[string]string{"id": c.ID}); rec.Code != http.StatusNotFound {
		t.Fatalf("sam deleted: %d", rec.Code)
	}
	if rec := webCall(w, "sam", w.s.rdpTicket, "POST", "/", `{"hostname":"10.0.0.5","connection_id":"`+c.ID+`"}`, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("sam used alex's password: %d %s", rec.Code, rec.Body)
	}

	// Connecting uses the remembered password and the saved options.
	rec = webCall(w, "alex", w.s.rdpTicket, "POST", "/", `{"hostname":"10.0.0.5","username":"me","connection_id":"`+c.ID+`",
		"size_mode":"fixed","fixed_width":1920,"fixed_height":1080,"scale":150,"color_depth":16,"clipboard":"to-remote","audio":true,"width":1000,"height":700}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("ticket: %d %s", rec.Code, rec.Body)
	}
	var tk struct{ Ticket string }
	_ = json.Unmarshal(rec.Body.Bytes(), &tk)
	got, ok := w.s.rdpTickets.redeem(tk.Ticket)
	p := got.params
	if !ok || p.Password != "s3cret!" || p.DPI != 144 || !p.FixedSize || p.Width != 1920 || p.ColorDepth != 16 ||
		!p.DisableCopy || p.DisablePaste || !p.Audio {
		t.Fatalf("params: %+v", p)
	}
	if raw, _ := w.s.Store.RemoteConn(ctx, w.users["alex"].ID, c.ID); raw.LastUsed == nil {
		t.Fatal("last used not recorded")
	}

	// Duplicate keeps the password; unticking Remember forgets it.
	rec = webCall(w, "alex", w.s.remoteConnDuplicate, "POST", "/", "", map[string]string{"id": c.ID})
	var dup savedConnJSON
	if json.Unmarshal(rec.Body.Bytes(), &dup); rec.Code != http.StatusCreated || dup.Name != "Office PC (copy)" || !dup.HasPassword {
		t.Fatalf("duplicate: %d %s", rec.Code, rec.Body)
	}
	rec = webCall(w, "alex", w.s.remoteConnUpdate, "PUT", "/", `{"name":"Office","hostname":"10.0.0.5","remember_password":false}`, map[string]string{"id": c.ID})
	if json.Unmarshal(rec.Body.Bytes(), &c); rec.Code != http.StatusOK || c.HasPassword || c.Name != "Office" {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	// Remembering again with no new password keeps nothing to keep.
	rec = webCall(w, "alex", w.s.remoteConnUpdate, "PUT", "/", `{"hostname":"10.0.0.5","protocol":"vnc","remember_password":true}`, map[string]string{"id": dup.ID})
	if json.Unmarshal(rec.Body.Bytes(), &dup); rec.Code != http.StatusOK || !dup.HasPassword || dup.Protocol != "vnc" || dup.Name != "10.0.0.5" {
		t.Fatalf("update dup: %d %s", rec.Code, rec.Body)
	}
	if rec := webCall(w, "alex", w.s.remoteConnDelete, "DELETE", "/", "", map[string]string{"id": dup.ID}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
}

func TestRemoteConnectionInputChecks(t *testing.T) {
	w := rdpWorld(t)
	for _, body := range []string{
		`{"hostname":""}`,
		`{"hostname":"a b"}`,
		`{"hostname":"x","protocol":"ssh"}`,
		`{"hostname":"x","options":{"security":"weird"}}`,
		`{"hostname":"x","options":{"color_depth":12}}`,
		`{"hostname":"x","options":{"clipboard":"sideways"}}`,
		`{"hostname":"x","options":{"gateway_host":"bad host"}}`,
	} {
		if rec := webCall(w, "alex", w.s.remoteConnCreate, "POST", "/", body, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, rec.Code)
		}
	}
	// No secret box: saving works, remembering a password doesn't.
	w.s.Secrets = nil
	if rec := webCall(w, "alex", w.s.remoteConnCreate, "POST", "/", `{"hostname":"x","remember_password":true,"password":"p"}`, nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("remember without a box: %d", rec.Code)
	}
	if rec := webCall(w, "alex", w.s.remoteConnCreate, "POST", "/", `{"hostname":"x"}`, nil); rec.Code != http.StatusCreated {
		t.Errorf("plain save: %d %s", rec.Code, rec.Body)
	}
}

func TestRDPOptionsParams(t *testing.T) {
	o := rdpOptions{SizeMode: "fit", Clipboard: "off", GatewayHost: "gw.example", GatewayUser: "u", FontSmoothing: true}
	if msg := o.check(); msg != "" {
		t.Fatal(msg)
	}
	p := o.params("rdp", "pc", 0, "me", "pw", "", "gwpw", 1280, 800)
	if p.FixedSize || !p.DisableCopy || !p.DisablePaste || p.GatewayHost != "gw.example" || p.GatewayPassword != "gwpw" || p.NoFontSmoothing || p.DPI != 96 {
		t.Fatalf("rdp params: %+v", p)
	}
	// VNC: no gateway, no audio.
	o.Audio = true
	p = o.params("vnc", "pc", 5901, "", "pw", "", "gwpw", 1280, 800)
	if p.GatewayHost != "" || p.Audio || p.Protocol != "vnc" {
		t.Fatalf("vnc params: %+v", p)
	}
}
