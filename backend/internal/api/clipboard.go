package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"alfaos/alfad/internal/store"
)

// NoCap Link clipboard: one shared clipboard across a person's own devices
// (their phones, NoCap OS in the browser and NoCap Desktop on this
// machine's screen). Only its owner ever sees it. It lives in memory and
// keeps only the last item, unless the person turns on a short history.

const (
	linkMaxClip      = 1 << 20  // text, bytes
	linkMaxClipImage = 10 << 20 // a PNG or JPEG picture, bytes
	linkClipHistory  = 10       // items kept with "short history" on

	clipText  = "text"
	clipImage = "image"

	clipFromPhone   = "phone"
	clipFromWeb     = "web"
	clipFromDesktop = "desktop"
)

// linkClip is one clipboard item. DeviceID is the phone it came from
// (source "phone") or the phone it's for (source "web"; "" = all of them).
type linkClip struct {
	ID       int64     `json:"id"`
	Kind     string    `json:"kind"` // text | image
	Text     string    `json:"text"`
	Mime     string    `json:"mime,omitempty"` // image/png | image/jpeg
	Size     int       `json:"size,omitempty"` // a picture's bytes
	From     string    `json:"from"`
	Source   string    `json:"source"` // phone | web | desktop
	DeviceID string    `json:"device_id"`
	At       time.Time `json:"at"`

	data []byte // the picture
}

// clipForPhone: whether deviceID should get c. Everything from the
// person's other devices, never its own clips back.
func clipForPhone(c linkClip, deviceID string) bool {
	switch c.Source {
	case clipFromPhone:
		return c.DeviceID != deviceID
	case clipFromWeb:
		return c.DeviceID == "" || c.DeviceID == deviceID
	default:
		return true
	}
}

// addClip keeps c as uid's newest clip (keep = how many to keep in all) and
// tells their open desktops and waiting phones.
func (h *linkHub) addClip(uid string, c linkClip, keep int) int64 {
	if keep < 1 {
		keep = 1
	}
	if c.Kind == "" {
		c.Kind = clipText
	}
	h.mu.Lock()
	u := h.user(uid)
	c.ID = h.bump()
	c.At = h.now().UTC().Truncate(time.Second)
	u.clips = append(u.clips, c)
	if len(u.clips) > keep {
		u.clips = append([]linkClip(nil), u.clips[len(u.clips)-keep:]...)
	}
	// Only the newest picture keeps its bytes: a history is short text.
	newest := true
	for i := len(u.clips) - 1; i >= 0; i-- {
		if u.clips[i].Kind != clipImage {
			continue
		}
		if !newest {
			u.clips[i].data = nil
		}
		newest = false
	}
	h.mu.Unlock()
	h.tell(uid, linkEvent{Type: "clip", Clip: &c})
	return c.ID
}

// clips lists uid's clipboard, newest first.
func (h *linkHub) clips(uid string) []linkClip {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.user(uid)
	out := make([]linkClip, 0, len(u.clips))
	for i := len(u.clips) - 1; i >= 0; i-- {
		out = append(out, u.clips[i])
	}
	return out
}

// clipData is the picture of uid's clip id (only theirs).
func (h *linkHub) clipData(uid string, id int64) (linkClip, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.user(uid).clips {
		if c.ID == id && c.Kind == clipImage && c.data != nil {
			return c, true
		}
	}
	return linkClip{}, false
}

// latestClip is uid's newest clip with an ID after `after` that pass lets
// through, if any. Call with mu held.
func (h *linkHub) latestClip(uid string, after int64, pass func(linkClip) bool) *linkClip {
	u := h.user(uid)
	for i := len(u.clips) - 1; i >= 0; i-- {
		c := u.clips[i]
		if c.ID <= after {
			break
		}
		if pass(c) {
			return &c
		}
	}
	return nil
}

// clearClips forgets uid's clipboard (history turned off, or by hand).
func (h *linkHub) clearClips(uid string, keepLast bool) {
	h.mu.Lock()
	u := h.user(uid)
	if keepLast && len(u.clips) > 0 {
		u.clips = u.clips[len(u.clips)-1:]
	} else {
		u.clips = nil
	}
	h.bump()
	h.mu.Unlock()
	h.tell(uid, linkEvent{Type: "changed"})
}

// ---------- routes ----------

func (s *Server) clipboardRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/clipboard", s.authed(s.clipboardList))
	mux.Handle("POST /api/v1/clipboard", s.authed(s.clipboardSend))
	mux.Handle("DELETE /api/v1/clipboard", s.authed(s.clipboardClear))
	mux.Handle("GET /api/v1/clipboard/settings", s.authed(s.clipboardSettingsGet))
	mux.Handle("PUT /api/v1/clipboard/settings", s.authed(s.clipboardSettingsPut))
	mux.Handle("GET /api/v1/clipboard/{id}/data", s.authed(s.clipboardData))
}

// clipSettings is uid's choices (the defaults when they can't be read).
func (s *Server) clipSettings(ctx context.Context, uid string) store.ClipboardSettings {
	c, err := s.Store.ClipboardSettings(ctx, uid)
	if err != nil {
		return store.DefaultClipboardSettings
	}
	return c
}

// keepClips is how many clips uid keeps.
func (s *Server) keepClips(ctx context.Context, uid string) int {
	if s.clipSettings(ctx, uid).History {
		return linkClipHistory
	}
	return 1
}

// readClip reads one clipboard item: JSON {"text": …} (plus whatever else
// extra holds), or a PNG or JPEG picture as the body. false: it answered.
func readClip(w http.ResponseWriter, r *http.Request, extra *string) (linkClip, bool) {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt == "image/png" || mt == "image/jpeg" {
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, linkMaxClipImage+1))
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) || len(b) > linkMaxClipImage {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "Pictures can be up to 10 MB.")
			return linkClip{}, false
		}
		if err != nil || len(b) == 0 || !imageIs(b, mt) {
			writeError(w, http.StatusBadRequest, "validation", "Only PNG and JPEG pictures can be shared.")
			return linkClip{}, false
		}
		if extra != nil {
			*extra = r.URL.Query().Get("device_id")
		}
		return linkClip{Kind: clipImage, Mime: mt, Size: len(b), data: b}, true
	}
	var req struct {
		Text     string `json:"text"`
		DeviceID string `json:"device_id"`
	}
	// JSON escapes can make text up to six times longer.
	if !decodeJSONLimit(w, r, &req, 6*linkMaxClip+4096) {
		return linkClip{}, false
	}
	if len(req.Text) > linkMaxClip {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "Text can be up to 1 MB.")
		return linkClip{}, false
	}
	if req.Text == "" || !utf8.ValidString(req.Text) {
		writeError(w, http.StatusBadRequest, "validation", "Send some text.")
		return linkClip{}, false
	}
	if extra != nil {
		*extra = req.DeviceID
	} else if req.DeviceID != "" {
		writeError(w, http.StatusBadRequest, "validation", "device_id isn't used here")
		return linkClip{}, false
	}
	return linkClip{Kind: clipText, Text: req.Text}, true
}

// imageIs checks a picture really is the type it claims.
func imageIs(b []byte, mt string) bool {
	switch mt {
	case "image/png":
		return bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n"))
	case "image/jpeg":
		return bytes.HasPrefix(b, []byte{0xff, 0xd8, 0xff})
	}
	return false
}

// writeClipData sends a clip's picture.
func writeClipData(w http.ResponseWriter, c linkClip) {
	w.Header().Set("Content-Type", c.Mime)
	w.Header().Set("Content-Length", strconv.Itoa(len(c.data)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(c.data)
}

// clipID reads the {id} path value.
func clipID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

// deviceClipboard: the phone shares its clipboard.
func (s *Server) deviceClipboard(w http.ResponseWriter, r *http.Request) {
	d, u := deviceFrom(r.Context()), userFrom(r.Context())
	c, ok := readClip(w, r, nil)
	if !ok {
		return
	}
	c.From, c.Source, c.DeviceID = d.Name, clipFromPhone, d.ID
	id := s.link().addClip(u.ID, c, s.keepClips(r.Context(), u.ID))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

// deviceClipboardData: a picture on the clipboard, for the phone.
func (s *Server) deviceClipboardData(w http.ResponseWriter, r *http.Request) {
	c, ok := s.link().clipData(userFrom(r.Context()).ID, clipID(r))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "That's not on the clipboard any more.")
		return
	}
	writeClipData(w, c)
}

func (s *Server) clipboardList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.link().clips(userFrom(r.Context()).ID)})
}

func (s *Server) clipboardData(w http.ResponseWriter, r *http.Request) {
	c, ok := s.link().clipData(userFrom(r.Context()).ID, clipID(r))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "That's not on the clipboard any more.")
		return
	}
	writeClipData(w, c)
}

// clipboardSend: NoCap OS shares text or a picture with one of the person's
// phones, or all of their devices.
func (s *Server) clipboardSend(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var deviceID string
	c, ok := readClip(w, r, &deviceID)
	if !ok {
		return
	}
	if deviceID != "" {
		list, err := s.Store.ListDevices(r.Context(), u.ID)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		found := false
		for _, d := range list {
			if d.ID == deviceID && d.UserID == u.ID {
				found = true
				break
			}
		}
		if !found {
			writeError(w, http.StatusNotFound, "not_found", "no such device")
			return
		}
	}
	c.From, c.Source, c.DeviceID = "NoCap OS", clipFromWeb, deviceID
	id := s.link().addClip(u.ID, c, s.keepClips(r.Context(), u.ID))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

// clipboardClear forgets the person's clipboard here (their devices keep
// what they already have).
func (s *Server) clipboardClear(w http.ResponseWriter, r *http.Request) {
	s.link().clearClips(userFrom(r.Context()).ID, false)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) clipboardSettingsGet(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	c, err := s.Store.ClipboardSettings(r.Context(), u.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) clipboardSettingsPut(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var req struct {
		History *bool `json:"history"`
		Desktop *bool `json:"desktop"`
	}
	if !decodeJSONLimit(w, r, &req, 4096) {
		return
	}
	c, err := s.Store.ClipboardSettings(r.Context(), u.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	was := c.History
	if req.History != nil {
		c.History = *req.History
	}
	if req.Desktop != nil {
		c.Desktop = *req.Desktop
	}
	if err := s.Store.SetClipboardSettings(r.Context(), u.ID, c); err != nil {
		s.internalError(w, r, err)
		return
	}
	if was && !c.History {
		// History off: forget it, keep only what's on the clipboard now.
		s.link().clearClips(u.ID, true)
	}
	writeJSON(w, http.StatusOK, c)
}
