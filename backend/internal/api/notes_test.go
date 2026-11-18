package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"alfaos/alfad/internal/notes"
	"alfaos/alfad/internal/store"
)

type noteJSON struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Body      json.RawMessage `json:"body"`
	Text      string          `json:"text"`
	Color     string          `json:"color"`
	Pinned    bool            `json:"pinned"`
	Archived  bool            `json:"archived"`
	TrashedAt *int64          `json:"trashed_at"`
	RemindAt  *int64          `json:"remind_at"`
	Desk      *notes.Desk     `json:"desk"`
	CreatedAt int64           `json:"created_at"`
	UpdatedAt int64           `json:"updated_at"`
	Version   int64           `json:"version"`
}

func decodeNote(t *testing.T, rec *httptest.ResponseRecorder) noteJSON {
	t.Helper()
	var n noteJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &n); err != nil || n.ID == "" {
		t.Fatalf("note: %d %s", rec.Code, rec.Body)
	}
	return n
}

func listNotes(t *testing.T, w *spacesWorld, who, state string) []noteJSON {
	t.Helper()
	rec := webCall(w, who, w.s.notesList, "GET", "/api/v1/notes?state="+state, "", nil)
	var got struct {
		Items []noteJSON `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	return got.Items
}

const helloBody = `[{"type":"p","spans":[{"text":"hello "},{"text":"world","bold":true}]},{"type":"todo","done":true,"spans":[{"text":"milk"}]}]`

func TestNotesCRUDAndOwnership(t *testing.T) {
	w := deviceWorld(t)
	ctx := context.Background()
	rec := webCall(w, "alex", w.s.notesCreate, "POST", "/api/v1/notes", `{"title":" Groceries ","body":`+helloBody+`,"color":"mint","pinned":true}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	n := decodeNote(t, rec)
	if n.Title != "Groceries" || n.Color != "mint" || !n.Pinned || n.Text != "hello world\n[x] milk" || n.Version != 1 {
		t.Fatalf("created: %+v", n)
	}
	id := map[string]string{"id": n.ID}

	// Nobody else sees, changes or deletes it, administrators included.
	for _, who := range []string{"sam", "admin"} {
		if rec := webCall(w, who, w.s.notesGet, "GET", "/", "", id); rec.Code != http.StatusNotFound {
			t.Fatalf("%s reads: %d", who, rec.Code)
		}
		if rec := webCall(w, who, w.s.notesPatch, "PATCH", "/", `{"title":"mine now"}`, id); rec.Code != http.StatusNotFound {
			t.Fatalf("%s edits: %d", who, rec.Code)
		}
		if rec := webCall(w, who, w.s.notesDelete, "DELETE", "/", "", id); rec.Code != http.StatusNotFound {
			t.Fatalf("%s deletes: %d", who, rec.Code)
		}
		if l := listNotes(t, w, who, ""); len(l) != 0 {
			t.Fatalf("%s lists: %+v", who, l)
		}
		if rec := webCall(w, who, w.s.notesDesk, "PUT", "/", `{"items":[{"id":"`+n.ID+`","desk":{"x":1,"y":1,"w":200,"h":200}}]}`, nil); !strings.Contains(rec.Body.String(), `"items":[]`) {
			t.Fatalf("%s moves: %s", who, rec.Body)
		}
	}
	// Someone else's phone can't either.
	samTok := linkDevice(t, w.s, w.users["sam"])
	if rec := devPath(w.s, w.s.notesGet, "GET", "/", samTok, id); rec.Code != http.StatusNotFound {
		t.Fatalf("sam's phone: %d", rec.Code)
	}
	// The owner's phone can.
	tok := linkDevice(t, w.s, w.users["alex"])
	if rec := devPath(w.s, w.s.notesGet, "GET", "/", tok, id); rec.Code != http.StatusOK {
		t.Fatalf("alex's phone: %d %s", rec.Code, rec.Body)
	}
	if rec := asDevice(w.s, w.s.notesList, "GET", "/api/v1/device/notes", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	if rec := asDevice(w.s, w.s.notesCreate, "POST", "/api/v1/device/notes", tok, `{"title":"from phone"}`); rec.Code != http.StatusCreated {
		t.Fatalf("phone create: %d %s", rec.Code, rec.Body)
	}

	// Edits: only what's sent changes; moving isn't editing.
	rec = webCall(w, "alex", w.s.notesPatch, "PATCH", "/", `{"color":"grape","archived":true}`, id)
	p := decodeNote(t, rec)
	if p.Color != "grape" || !p.Archived || p.Title != "Groceries" || p.Version != 2 || p.UpdatedAt < n.UpdatedAt {
		t.Fatalf("patch: %+v", p)
	}
	rec = webCall(w, "alex", w.s.notesPatch, "PATCH", "/", `{"desk":{"x":40,"y":60,"w":10,"h":260}}`, id)
	d := decodeNote(t, rec)
	if d.Desk == nil || d.Desk.W != 160 || d.Desk.X != 40 || d.UpdatedAt != p.UpdatedAt {
		t.Fatalf("desk: %+v", d)
	}
	// Nothing changed: same version.
	if rec := webCall(w, "alex", w.s.notesPatch, "PATCH", "/", `{"color":"grape"}`, id); decodeNote(t, rec).Version != d.Version {
		t.Fatal("no-op patch bumped the version")
	}
	// The trash, then restore; trashing unsticks it from the desktop.
	rec = webCall(w, "alex", w.s.notesPatch, "PATCH", "/", `{"trashed":true}`, id)
	if tr := decodeNote(t, rec); tr.TrashedAt == nil || tr.Desk != nil {
		t.Fatalf("trash: %+v", tr)
	}
	if l := listNotes(t, w, "alex", "trash"); len(l) != 1 || l[0].ID != n.ID {
		t.Fatalf("trash list: %+v", l)
	}
	rec = webCall(w, "alex", w.s.notesPatch, "PATCH", "/", `{"trashed":false}`, id)
	if tr := decodeNote(t, rec); tr.TrashedAt != nil {
		t.Fatalf("restore: %+v", tr)
	}
	if l := listNotes(t, w, "alex", "archived"); len(l) != 1 {
		t.Fatalf("archived list: %+v", l)
	}
	if rec := webCall(w, "alex", w.s.notesList, "GET", "/?state=bogus", "", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad state: %d", rec.Code)
	}

	// Audit: deletes only, never what the note said.
	if c, _ := w.s.Store.CountAudit(ctx, w.users["alex"].ID, "notes.delete"); c != 0 {
		t.Fatalf("audited before a delete: %d", c)
	}
	if rec := webCall(w, "alex", w.s.notesDelete, "DELETE", "/", "", id); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if c, _ := w.s.Store.CountAudit(ctx, w.users["alex"].ID, "notes.delete"); c != 1 {
		t.Fatalf("delete audit: %d", c)
	}
	if rec := webCall(w, "alex", w.s.notesGet, "GET", "/", "", id); rec.Code != http.StatusNotFound {
		t.Fatalf("after delete: %d", rec.Code)
	}
}

func TestNotesValidationAndLimits(t *testing.T) {
	w := deviceWorld(t)
	for body, code := range map[string]int{
		`{"body":[{"type":"p","spans":[{"text":"x","href":"javascript:alert(1)"}]}]}`: http.StatusBadRequest,
		`{"body":"<b>html</b>"}`:                       http.StatusBadRequest,
		`{"color":"yellow"}`:                           http.StatusBadRequest,
		`{"remind_at":-5}`:                             http.StatusBadRequest,
		`{"id":"../../etc"}`:                           http.StatusBadRequest,
		`{"desk":"left"}`:                              http.StatusBadRequest,
		`{"unknown":1}`:                                http.StatusBadRequest,
		`{"title":"` + strings.Repeat("x", 301) + `"}`: http.StatusRequestEntityTooLarge,
		`{"body":[{"type":"p","spans":[{"text":"` + strings.Repeat("x", notes.MaxNoteBytes) + `"}]}]}`: http.StatusRequestEntityTooLarge,
		`{"body":[{"type":"p","spans":[{"text":"` + strings.Repeat("x", 90<<10) + `"}]}]}`:             http.StatusCreated,
		`{"id":"client-made-id-1","title":"ok"}`:                                                       http.StatusCreated,
	} {
		if rec := webCall(w, "alex", w.s.notesCreate, "POST", "/api/v1/notes", body, nil); rec.Code != code {
			t.Errorf("%.60s: %d %s", body, rec.Code, rec.Body)
		}
	}
	if rec := webCall(w, "alex", w.s.notesCreate, "POST", "/api/v1/notes", `{"id":"client-made-id-1"}`, nil); rec.Code != http.StatusConflict {
		t.Fatalf("same id: %d", rec.Code)
	}
	// 5,000 notes each, at most.
	ctx := context.Background()
	uid := w.users["sam"].ID
	for i := 0; i < notes.MaxNotes; i++ {
		n := &store.Note{ID: store.NewID(), Body: "[]", Color: "glass", CreatedAt: 1, UpdatedAt: 1}
		if err := w.s.Store.CreateNote(ctx, uid, n, notes.MaxNotes); err != nil {
			t.Fatal(i, err)
		}
	}
	rec := webCall(w, "sam", w.s.notesCreate, "POST", "/api/v1/notes", `{"title":"one more"}`, nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "too_many_notes") {
		t.Fatalf("over the limit: %d %s", rec.Code, rec.Body)
	}
	if rec := webCall(w, "alex", w.s.notesCreate, "POST", "/api/v1/notes", `{"title":"alex still can"}`, nil); rec.Code != http.StatusCreated {
		t.Fatalf("alex: %d", rec.Code)
	}
}

func TestNotesLiveAndReminders(t *testing.T) {
	w := deviceWorld(t)
	alex := w.users["alex"]
	feed, stop := w.s.noteFeed.subscribe(alex.ID)
	defer stop()
	samFeed, samStop := w.s.noteFeed.subscribe(w.users["sam"].ID)
	defer samStop()

	h := w.s.link()
	h.mu.Lock()
	cursor := h.seq
	h.mu.Unlock()

	r := httptest.NewRequest("POST", "/api/v1/notes", strings.NewReader(`{"title":"call mom","body":[{"type":"p","spans":[{"text":"about sunday"}]}],"remind_at":1000}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(notesClientHeader, "window-abc123")
	r = r.WithContext(context.WithValue(r.Context(), userKey, alex))
	rec := httptest.NewRecorder()
	w.s.notesCreate(rec, r)
	n := decodeNote(t, rec)
	select {
	case e := <-feed:
		if e.Type != "upsert" || e.Note == nil || e.Note.ID != n.ID || e.Client != "window-abc123" {
			t.Fatalf("event: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no live event")
	}
	// The phone hears that notes changed.
	h.mu.Lock()
	ev := h.events(alex.ID, "dev", false, false, cursor)
	h.mu.Unlock()
	if !ev.Notes || ev.empty() {
		t.Fatalf("phone events: %+v", ev)
	}

	// The reminder fires once, to its owner only.
	if c := w.s.fireNoteReminders(context.Background(), time.UnixMilli(999)); c != 0 {
		t.Fatalf("early: %d", c)
	}
	if c := w.s.fireNoteReminders(context.Background(), time.UnixMilli(5000)); c != 1 {
		t.Fatalf("due: %d", c)
	}
	select {
	case e := <-feed:
		if e.Type != "reminder" || e.Note.ID != n.ID {
			t.Fatalf("reminder: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no reminder")
	}
	select {
	case e := <-samFeed:
		t.Fatalf("sam heard: %+v", e)
	default:
	}
	h.mu.Lock()
	ev = h.events(alex.ID, "dev", false, false, cursor)
	h.mu.Unlock()
	if len(ev.Reminders) != 1 || ev.Reminders[0].NoteID != n.ID || ev.Reminders[0].Title != "call mom" || ev.Reminders[0].Text != "about sunday" {
		t.Fatalf("phone reminder: %+v", ev.Reminders)
	}
	if c := w.s.fireNoteReminders(context.Background(), time.UnixMilli(9000)); c != 0 {
		t.Fatalf("fired twice: %d", c)
	}
	// Setting a new time arms it again.
	rec = webCall(w, "alex", w.s.notesPatch, "PATCH", "/", `{"remind_at":20000}`, map[string]string{"id": n.ID})
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	if c := w.s.fireNoteReminders(context.Background(), time.UnixMilli(20000)); c != 1 {
		t.Fatalf("re-armed: %d", c)
	}

	// Empty trash tells everyone and is audited.
	webCall(w, "alex", w.s.notesPatch, "PATCH", "/", `{"trashed":true}`, map[string]string{"id": n.ID})
	for len(feed) > 0 {
		<-feed
	}
	rec = webCall(w, "alex", w.s.notesEmptyTrash, "DELETE", "/", "", nil)
	if !strings.Contains(rec.Body.String(), `"deleted":1`) {
		t.Fatalf("empty trash: %s", rec.Body)
	}
	if e := <-feed; e.Type != "delete" || len(e.IDs) != 1 || e.IDs[0] != n.ID {
		t.Fatalf("delete event: %+v", e)
	}
	if c, _ := w.s.Store.CountAudit(context.Background(), alex.ID, "notes.empty_trash"); c != 1 {
		t.Fatalf("empty trash audit: %d", c)
	}
}
