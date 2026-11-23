package api

import (
	"net/http"
	"testing"
	"time"
)

// Saving preferences tells the person's other windows, with the saving
// window's tag, and nobody else's.
func TestPrefsSaveTellsOtherWindows(t *testing.T) {
	w := newSpacesWorld(t)
	s := w.s
	alex, cancel := s.prefsFeed.subscribe(w.users["alex"].ID)
	defer cancel()
	admin, cancelAdmin := s.prefsFeed.subscribe(w.users["admin"].ID)
	defer cancelAdmin()

	rec := w.call(t, "alex", s.prefsPut, "PUT", "/api/v1/me/prefs", map[string]any{
		"prefs": map[string]any{"titleButtons": "left"}, "client": "win-1",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("save = %d (%s)", rec.Code, rec.Body)
	}
	select {
	case e := <-alex:
		if e.Client != "win-1" {
			t.Errorf("client = %q, want win-1", e.Client)
		}
	case <-time.After(time.Second):
		t.Fatal("alex's other windows weren't told")
	}
	select {
	case e := <-admin:
		t.Errorf("someone else was told: %+v", e)
	default:
	}

	// A tag that isn't one is dropped, the change still goes out.
	w.call(t, "alex", s.prefsPut, "PUT", "/api/v1/me/prefs", map[string]any{
		"prefs": map[string]any{"titleButtons": "right"}, "client": "<script>",
	})
	select {
	case e := <-alex:
		if e.Client != "" {
			t.Errorf("bad tag kept: %q", e.Client)
		}
	case <-time.After(time.Second):
		t.Fatal("second change wasn't told")
	}
}
