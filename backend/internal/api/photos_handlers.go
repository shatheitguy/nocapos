package api

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/photos"
	"alfaos/alfad/internal/store"
)

// Photos: the pictures and videos in the Photos folder of each storage
// location. Same access as Files (admin only). Thumbnails and originals are
// loaded by <img>/<video>, which can't send the Bearer token, so the app gets
// one ticket per location scoped to its Photos folder.

const photoTicketTTL = time.Hour

func (s *Server) photosList(w http.ResponseWriter, r *http.Request) {
	root, err := s.Photos.EnsureFolder()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	items, scanning, err := s.Photos.List(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	_, building, _ := s.FileIndex.Stats()
	writeJSON(w, http.StatusOK, map[string]any{
		"items":    items,
		"scanning": scanning || building,
		// Where uploads go.
		"upload": map[string]string{"root": root, "path": "/" + photos.Folder},
	})
}

func (s *Server) photosTickets(w http.ResponseWriter, r *http.Request) {
	uid := userFrom(r.Context()).ID
	out, trash := map[string]string{}, map[string]string{}
	var exp time.Time
	for _, root := range s.Files.Roots() {
		if root.ID == "system" {
			continue
		}
		out[root.ID], exp = s.fileTickets.issueTTL(uid, root.ID, photos.Folder, photoTicketTTL)
		// Recently deleted photos are shown from the recycle bin.
		trash[root.ID], _ = s.fileTickets.issueTTL(uid, root.ID, files.RecycleDir, photoTicketTTL)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tickets": out, "trash": trash, "expires_at": exp.UTC()})
}

func (s *Server) photosThumb(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	root := q.Get("root")
	rel, err := files.Clean(q.Get("path"))
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	userID, ok := s.fileTickets.check(q.Get("t"), root, rel)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid_ticket", "invalid or expired photo link")
		return
	}
	if u, err := s.Store.UserByID(r.Context(), userID); err != nil || u.Disabled {
		writeError(w, http.StatusUnauthorized, "invalid_ticket", "invalid or expired photo link")
		return
	}
	size := q.Get("size")
	if size == "" {
		size = "s"
	}
	p, err := s.Photos.Thumb(r.Context(), root, rel, size)
	if errors.Is(err, photos.ErrNoThumb) {
		writeError(w, http.StatusNotFound, "no_thumbnail", err.Error())
		return
	}
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/jpeg")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	// The URL carries the file's size and time, so a changed photo gets a new URL.
	h.Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, "thumb.jpg", st.ModTime(), f)
}

type photoItemsRequest struct {
	Items []store.PhotoKey `json:"items"`
	On    bool             `json:"on"`
}

// cleanPhotoKeys validates the photos named in a request.
func (s *Server) cleanPhotoKeys(keys []store.PhotoKey) ([]store.PhotoKey, error) {
	if len(keys) == 0 || len(keys) > 5000 {
		return nil, files.ErrBadPath
	}
	out := make([]store.PhotoKey, 0, len(keys))
	for _, k := range keys {
		if _, err := s.Files.Root(k.Root); err != nil {
			return nil, err
		}
		rel, err := files.Clean(k.Path)
		if err != nil || rel == "." {
			return nil, files.ErrBadPath
		}
		out = append(out, store.PhotoKey{Root: k.Root, Path: "/" + rel})
	}
	return out, nil
}

func (s *Server) photosFavorite(w http.ResponseWriter, r *http.Request) {
	var req photoItemsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	keys, err := s.cleanPhotoKeys(req.Items)
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	if err := s.Store.SetPhotoFavorite(r.Context(), userFrom(r.Context()).ID, keys, req.On); err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// photosDelete moves photos to the recycle bin of their location.
func (s *Server) photosDelete(w http.ResponseWriter, r *http.Request) {
	var req photoItemsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	keys, err := s.cleanPhotoKeys(req.Items)
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	byRoot := map[string][]string{}
	for _, k := range keys {
		byRoot[k.Root] = append(byRoot[k.Root], strings.TrimPrefix(k.Path, "/"))
	}
	uid := userFrom(r.Context()).ID
	for root, rels := range byRoot {
		err := s.Photos.TrashRoot(r.Context(), root, rels)
		s.audit(r, uid, "photos.delete", root+":"+strings.Join(rels, ", "), err == nil, "to recycle bin")
		if err != nil {
			s.fileError(w, r, err)
			return
		}
		s.FileIndex.Remove(root, rels)
	}
	if err := s.Photos.Forget(r.Context(), keys); err != nil {
		s.Log.Warn("photos: forget deleted", "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) photosAlbums(w http.ResponseWriter, r *http.Request) {
	albums, err := s.Store.PhotoAlbums(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if albums == nil {
		albums = []*store.PhotoAlbum{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"albums": albums})
}

type albumRequest struct {
	Name  string           `json:"name"`
	Items []store.PhotoKey `json:"items"`
}

func albumName(n string) (string, bool) {
	n = strings.TrimSpace(n)
	return n, n != "" && len(n) <= 80
}

func (s *Server) photosAlbumCreate(w http.ResponseWriter, r *http.Request) {
	var req albumRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name, ok := albumName(req.Name)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "album name must be 1-80 characters")
		return
	}
	uid := userFrom(r.Context()).ID
	id, err := s.Store.CreatePhotoAlbum(r.Context(), uid, name)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if len(req.Items) > 0 {
		keys, err := s.cleanPhotoKeys(req.Items)
		if err == nil {
			err = s.Store.AddToPhotoAlbum(r.Context(), uid, id, keys)
		}
		if err != nil {
			s.fileError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func albumID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "not_found", "no such album")
		return 0, false
	}
	return id, true
}

func (s *Server) albumError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no such album")
		return
	}
	s.fileError(w, r, err)
}

func (s *Server) photosAlbumRename(w http.ResponseWriter, r *http.Request) {
	id, ok := albumID(w, r)
	if !ok {
		return
	}
	var req albumRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name, ok := albumName(req.Name)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "album name must be 1-80 characters")
		return
	}
	if err := s.Store.RenamePhotoAlbum(r.Context(), userFrom(r.Context()).ID, id, name); err != nil {
		s.albumError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) photosAlbumDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := albumID(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeletePhotoAlbum(r.Context(), userFrom(r.Context()).ID, id); err != nil {
		s.albumError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// photosAlbumItems adds photos to an album ({"items": [...]}), or removes them ({"items": [...], "on": false}).
func (s *Server) photosAlbumItems(w http.ResponseWriter, r *http.Request) {
	id, ok := albumID(w, r)
	if !ok {
		return
	}
	req := photoItemsRequest{On: true}
	if !decodeJSON(w, r, &req) {
		return
	}
	keys, err := s.cleanPhotoKeys(req.Items)
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	uid := userFrom(r.Context()).ID
	if req.On {
		err = s.Store.AddToPhotoAlbum(r.Context(), uid, id, keys)
	} else {
		err = s.Store.RemoveFromPhotoAlbum(r.Context(), uid, id, keys)
	}
	if err != nil {
		s.albumError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// photosInfo returns the camera details of one photo.
func (s *Server) photosInfo(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d, err := s.Photos.Info(q.Get("root"), q.Get("path"))
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// photosAlbumCover sets an album's cover ({"root", "path"}), or resets it ({}).
func (s *Server) photosAlbumCover(w http.ResponseWriter, r *http.Request) {
	id, ok := albumID(w, r)
	if !ok {
		return
	}
	var req store.PhotoKey
	if !decodeJSON(w, r, &req) {
		return
	}
	var cover *store.PhotoKey
	if req.Path != "" {
		keys, err := s.cleanPhotoKeys([]store.PhotoKey{req})
		if err != nil {
			s.fileError(w, r, err)
			return
		}
		cover = &keys[0]
	}
	if err := s.Store.SetPhotoAlbumCover(r.Context(), userFrom(r.Context()).ID, id, cover); err != nil {
		s.albumError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) photosTrash(w http.ResponseWriter, r *http.Request) {
	items, err := s.Photos.Deleted(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type photoTrashRequest struct {
	IDs []int64 `json:"ids"`
}

func (s *Server) photoTrashIDs(w http.ResponseWriter, r *http.Request) ([]int64, bool) {
	var req photoTrashRequest
	if !decodeJSON(w, r, &req) {
		return nil, false
	}
	if len(req.IDs) == 0 || len(req.IDs) > 5000 {
		writeError(w, http.StatusBadRequest, "bad_request", "choose 1-5000 items")
		return nil, false
	}
	return req.IDs, true
}

// photosRestore puts recently deleted photos back.
func (s *Server) photosRestore(w http.ResponseWriter, r *http.Request) {
	ids, ok := s.photoTrashIDs(w, r)
	if !ok {
		return
	}
	keys, err := s.Photos.Restore(r.Context(), ids)
	s.audit(r, userFrom(r.Context()).ID, "photos.restore", strconv.Itoa(len(keys))+" item(s)", err == nil, "from recycle bin")
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	if keys == nil {
		keys = []store.PhotoKey{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": keys})
}

// photosPurge deletes recently deleted photos for good.
func (s *Server) photosPurge(w http.ResponseWriter, r *http.Request) {
	ids, ok := s.photoTrashIDs(w, r)
	if !ok {
		return
	}
	err := s.Photos.Purge(r.Context(), ids)
	s.audit(r, userFrom(r.Context()).ID, "photos.purge", strconv.Itoa(len(ids))+" item(s)", err == nil, "deleted for good")
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
