package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"time"

	"alfaos/alfad/internal/notes"
	"alfaos/alfad/internal/store"
)

// Sticky Notes: each person's own notes. Every handler works on the signed-in
// person's notes only (a phone works on its owner's); administrators can't
// read anyone else's. Open desktops follow changes on the "notes" topic and
// phones on their long-poll; reminders that come due go to both.

const (
	maxNotesBody      = 512 << 10 // a request: one note (100 KB of text can be more as JSON)
	notesTrashKeep    = 30 * 24 * time.Hour
	notesTickInterval = 15 * time.Second
	notesClientHeader = "X-Notes-Client"
)

var noteIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

func (s *Server) notesRoutes(mux *http.ServeMux) {
	for _, side := range []struct {
		prefix string
		wrap   func(http.HandlerFunc) http.Handler
	}{
		{"/api/v1/notes", s.authed},
		{"/api/v1/device/notes", s.device},
	} {
		p := side.prefix
		mux.Handle("GET "+p, side.wrap(s.notesList))
		mux.Handle("POST "+p, side.wrap(s.notesCreate))
		mux.Handle("PUT "+p+"/desk", side.wrap(s.notesDesk))
		mux.Handle("DELETE "+p+"/trash", side.wrap(s.notesEmptyTrash))
		mux.Handle("GET "+p+"/{id}", side.wrap(s.notesGet))
		mux.Handle("PATCH "+p+"/{id}", side.wrap(s.notesPatch))
		mux.Handle("DELETE "+p+"/{id}", side.wrap(s.notesDelete))
	}
}

// ---------- live updates ----------

// noteEvent goes to a person's open desktops. Client is the X-Notes-Client of
// the window that made the change, so it can skip its own echo.
type noteEvent struct {
	Type   string      `json:"type"` // upsert | delete | reminder
	Note   *store.Note `json:"note,omitempty"`
	IDs    []string    `json:"ids,omitempty"`
	Client string      `json:"client,omitempty"`
}

// noteReminder is a reminder that came due, for the person's phones.
type noteReminder struct {
	ID     int64     `json:"id"` // place in the event sequence
	NoteID string    `json:"note_id"`
	Title  string    `json:"title"`
	Text   string    `json:"text"`
	At     time.Time `json:"at"`
}

type noteFeed struct {
	mu   sync.Mutex
	subs map[string]map[chan noteEvent]struct{}
}

func (f *noteFeed) subscribe(uid string) (<-chan noteEvent, func()) {
	ch := make(chan noteEvent, 32)
	f.mu.Lock()
	if f.subs == nil {
		f.subs = map[string]map[chan noteEvent]struct{}{}
	}
	if f.subs[uid] == nil {
		f.subs[uid] = map[chan noteEvent]struct{}{}
	}
	f.subs[uid][ch] = struct{}{}
	f.mu.Unlock()
	return ch, func() {
		f.mu.Lock()
		delete(f.subs[uid], ch)
		if len(f.subs[uid]) == 0 {
			delete(f.subs, uid)
		}
		f.mu.Unlock()
	}
}

func (f *noteFeed) publish(uid string, e noteEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for ch := range f.subs[uid] {
		select {
		case ch <- e:
		default: // a stuck desktop reloads its notes when it reconnects
		}
	}
}

func (s *Server) notesSource(uid string) func(ctx context.Context, emit func(any)) error {
	return func(ctx context.Context, emit func(any)) error {
		ch, cancel := s.noteFeed.subscribe(uid)
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return nil
			case e := <-ch:
				emit(e)
			}
		}
	}
}

// notesChanged tells uid's desktops and phones.
func (s *Server) notesChanged(uid string, e noteEvent) {
	s.noteFeed.publish(uid, e)
	h := s.link()
	h.mu.Lock()
	h.user(uid).notesSeq = h.bump()
	h.mu.Unlock()
}

func clientTag(r *http.Request) string {
	c := r.Header.Get(notesClientHeader)
	if len(c) > 64 || !noteIDRe.MatchString(c) {
		return ""
	}
	return c
}

// ---------- reminders and trash clean-up ----------

// notesLoop fires reminders as they come due and empties trash older than
// 30 days, until ctx ends.
func (s *Server) notesLoop(ctx context.Context) {
	t := time.NewTicker(notesTickInterval)
	defer t.Stop()
	purged := time.Time{}
	for {
		s.fireNoteReminders(ctx, time.Now())
		if time.Since(purged) > time.Hour {
			s.purgeNotesTrash(ctx, time.Now())
			purged = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) fireNoteReminders(ctx context.Context, now time.Time) int {
	due, err := s.Store.ClaimDueReminders(ctx, now.UnixMilli())
	if err != nil {
		if ctx.Err() == nil {
			s.Log.Warn("note reminders", "err", err)
		}
		return 0
	}
	for _, d := range due {
		s.noteFeed.publish(d.UserID, noteEvent{Type: "reminder", Note: d.Note})
		blocks, _ := notes.Parse([]byte(d.Note.Body))
		h := s.link()
		h.mu.Lock()
		u := h.user(d.UserID)
		u.notesSeq = h.bump()
		u.reminders = append(u.reminders, noteReminder{
			ID: h.bump(), NoteID: d.Note.ID, Title: d.Note.Title,
			Text: notes.Snippet("", blocks, 200), At: h.now().UTC(),
		})
		if len(u.reminders) > 20 {
			u.reminders = u.reminders[len(u.reminders)-20:]
		}
		h.mu.Unlock()
	}
	return len(due)
}

func (s *Server) purgeNotesTrash(ctx context.Context, now time.Time) {
	gone, err := s.Store.PurgeNotesTrash(ctx, now.Add(-notesTrashKeep).UnixMilli())
	if err != nil {
		if ctx.Err() == nil {
			s.Log.Warn("notes trash clean-up", "err", err)
		}
		return
	}
	for uid, ids := range gone {
		s.auditCtx(ctx, uid, "notes.purge", "", "", true, fmt.Sprintf("%d note(s) older than 30 days in the trash", len(ids)))
		s.notesChanged(uid, noteEvent{Type: "delete", IDs: ids})
	}
}

// ---------- handlers ----------

func (s *Server) notesList(w http.ResponseWriter, r *http.Request) {
	f := store.NoteFilter(r.URL.Query().Get("state"))
	switch f {
	case store.NotesAll, store.NotesActive, store.NotesArchived, store.NotesTrash:
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "state must be active, archived or trash")
		return
	}
	list, err := s.Store.ListNotes(r.Context(), userFrom(r.Context()).ID, f)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "limit": notes.MaxNotes, "max_bytes": notes.MaxNoteBytes})
}

func (s *Server) notesGet(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.Note(r.Context(), userFrom(r.Context()).ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "That note isn't here.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// noteInput is a create or a change. Fields left out stay as they are; for
// remind_at and desk, null clears them.
type noteInput struct {
	ID       string          `json:"id"` // create only: the client may pick it
	Title    *string         `json:"title"`
	Body     json.RawMessage `json:"body"`
	Color    *string         `json:"color"`
	Pinned   *bool           `json:"pinned"`
	Archived *bool           `json:"archived"`
	Trashed  *bool           `json:"trashed"`
	RemindAt json.RawMessage `json:"remind_at"`
	Desk     json.RawMessage `json:"desk"`
}

var errNoteInput = errors.New("bad note")

func isNull(m json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(m), []byte("null")) }

// apply puts in onto n; it reports whether anything that counts as an edit
// (not just a move) changed, or a problem for the person.
func (in noteInput) apply(n *store.Note, now int64) (edited bool, err error) {
	if in.Title != nil {
		t, err := notes.CleanTitle(*in.Title)
		if err != nil {
			return false, err
		}
		edited = edited || t != n.Title
		n.Title = t
	}
	if in.Body != nil {
		blocks, err := notes.Parse(in.Body)
		if err != nil {
			return false, err
		}
		body := notes.Encode(blocks)
		edited = edited || body != n.Body
		n.Body, n.Text = body, notes.PlainText(blocks)
	}
	if in.Color != nil {
		if !notes.ValidColor(*in.Color) {
			return false, fmt.Errorf("%w: unknown color", errNoteInput)
		}
		edited = edited || *in.Color != n.Color
		n.Color = *in.Color
	}
	if in.Pinned != nil {
		edited = edited || *in.Pinned != n.Pinned
		n.Pinned = *in.Pinned
	}
	if in.Archived != nil {
		edited = edited || *in.Archived != n.Archived
		n.Archived = *in.Archived
	}
	if in.Trashed != nil && *in.Trashed != (n.TrashedAt != nil) {
		edited = true
		if *in.Trashed {
			n.TrashedAt = &now
			n.Desk = nil // the trash doesn't stick to the desktop
		} else {
			n.TrashedAt = nil
		}
	}
	if in.RemindAt != nil {
		var at *int64
		if !isNull(in.RemindAt) {
			var v int64
			if err := json.Unmarshal(in.RemindAt, &v); err != nil || v <= 0 {
				return false, fmt.Errorf("%w: remind_at is a time in milliseconds", errNoteInput)
			}
			at = &v
		}
		if (at == nil) != (n.RemindAt == nil) || (at != nil && *at != *n.RemindAt) {
			edited = true
			n.RemindAt, n.Reminded = at, false
		}
	}
	if in.Desk != nil {
		if isNull(in.Desk) {
			n.Desk = nil
		} else {
			var d notes.Desk
			if err := json.Unmarshal(in.Desk, &d); err != nil {
				return false, fmt.Errorf("%w: desk is {x, y, w, h}", errNoteInput)
			}
			d = d.Clamp()
			n.Desk = &d
		}
	}
	if len(n.Title)+len(n.Body) > notes.MaxNoteBytes {
		return false, fmt.Errorf("%w: a note can hold up to 100 KB", notes.ErrTooLarge)
	}
	if edited {
		n.UpdatedAt = now
	}
	return edited, nil
}

func (s *Server) noteInputError(w http.ResponseWriter, err error) {
	if errors.Is(err, notes.ErrTooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", err.Error())
		return
	}
	writeError(w, http.StatusBadRequest, "bad_request", err.Error())
}

func (s *Server) notesCreate(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var in noteInput
	if !decodeJSONLimit(w, r, &in, maxNotesBody) {
		return
	}
	if in.ID == "" {
		in.ID = store.NewID()
	} else if !noteIDRe.MatchString(in.ID) {
		writeError(w, http.StatusBadRequest, "bad_request", "bad note id")
		return
	}
	now := time.Now().UnixMilli()
	n := &store.Note{ID: in.ID, Body: "[]", Color: "glass", CreatedAt: now, UpdatedAt: now}
	if _, err := in.apply(n, now); err != nil {
		s.noteInputError(w, err)
		return
	}
	n.UpdatedAt = now
	switch err := s.Store.CreateNote(r.Context(), u.ID, n, notes.MaxNotes); {
	case errors.Is(err, store.ErrLimit):
		writeError(w, http.StatusConflict, "too_many_notes", fmt.Sprintf("You have %d notes, the most there can be. Empty the trash or delete some first.", notes.MaxNotes))
		return
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "exists", "A note with that id already exists.")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	s.notesChanged(u.ID, noteEvent{Type: "upsert", Note: n, Client: clientTag(r)})
	writeJSON(w, http.StatusCreated, n)
}

func (s *Server) notesPatch(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var in noteInput
	if !decodeJSONLimit(w, r, &in, maxNotesBody) {
		return
	}
	if in.ID != "" {
		writeError(w, http.StatusBadRequest, "bad_request", "a note's id can't change")
		return
	}
	var inErr error
	changed := false
	n, err := s.Store.UpdateNote(r.Context(), u.ID, r.PathValue("id"), func(n *store.Note) bool {
		before := *n
		if _, inErr = in.apply(n, time.Now().UnixMilli()); inErr != nil {
			return false
		}
		changed = !sameNote(before, *n)
		return changed
	})
	if inErr != nil {
		s.noteInputError(w, inErr)
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "That note isn't here.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if changed {
		s.notesChanged(u.ID, noteEvent{Type: "upsert", Note: n, Client: clientTag(r)})
	}
	writeJSON(w, http.StatusOK, n)
}

func sameNote(a, b store.Note) bool {
	eqp := func(x, y *int64) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
	eqd := func(x, y *notes.Desk) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
	return a.Title == b.Title && a.Body == b.Body && a.Color == b.Color && a.Pinned == b.Pinned && a.Archived == b.Archived &&
		eqp(a.TrashedAt, b.TrashedAt) && eqp(a.RemindAt, b.RemindAt) && a.Reminded == b.Reminded && eqd(a.Desk, b.Desk)
}

func (s *Server) notesDelete(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	id := r.PathValue("id")
	n, err := s.Store.Note(r.Context(), u.ID, id)
	if err == nil {
		err = s.Store.DeleteNote(r.Context(), u.ID, id)
	}
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "That note isn't here.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// The audit log says that a note went, never what it said.
	s.audit(r, u.ID, "notes.delete", n.ID, true, "")
	s.notesChanged(u.ID, noteEvent{Type: "delete", IDs: []string{id}, Client: clientTag(r)})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) notesEmptyTrash(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	ids, err := s.Store.EmptyNotesTrash(r.Context(), u.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if len(ids) > 0 {
		s.audit(r, u.ID, "notes.empty_trash", "", true, fmt.Sprintf("%d note(s)", len(ids)))
		s.notesChanged(u.ID, noteEvent{Type: "delete", IDs: ids, Client: clientTag(r)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": len(ids)})
}

// notesDesk moves notes on (or off, with "desk": null) the web desktop.
func (s *Server) notesDesk(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var req struct {
		Items []struct {
			ID   string      `json:"id"`
			Desk *notes.Desk `json:"desk"`
		} `json:"items"`
	}
	if !decodeJSONLimit(w, r, &req, maxBodyBytes) {
		return
	}
	if len(req.Items) > 200 {
		writeError(w, http.StatusBadRequest, "bad_request", "up to 200 notes at a time")
		return
	}
	pos := make([]store.NoteDeskPos, 0, len(req.Items))
	for _, it := range req.Items {
		var d *notes.Desk
		if it.Desk != nil {
			c := it.Desk.Clamp()
			d = &c
		}
		pos = append(pos, store.NoteDeskPos{ID: it.ID, Desk: d})
	}
	moved, err := s.Store.SetNoteDesks(r.Context(), u.ID, pos)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for _, n := range moved {
		s.noteFeed.publish(u.ID, noteEvent{Type: "upsert", Note: n, Client: clientTag(r)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": moved})
}
