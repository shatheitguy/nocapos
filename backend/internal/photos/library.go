// Package photos is the Photos library: the pictures and videos in the
// "Photos" folder of each storage location, dated from their EXIF / movie
// headers, with cached thumbnails. Favorites and albums live in the store.
package photos

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	_ "image/gif"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"alfaos/alfad/internal/files"
	"alfaos/alfad/internal/store"
)

// Folder is the library folder at the top of each storage location.
const Folder = "Photos"

var (
	imageExt = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".bmp": true, ".heic": true, ".heif": true}
	videoExt = map[string]bool{".mp4": true, ".mov": true, ".m4v": true, ".webm": true}
	// Pictures the browser can't show and Go can't decode; thumbnails need ffmpeg.
	needsFFmpeg = map[string]bool{".heic": true, ".heif": true}
)

func isMedia(lower string) bool {
	ext := path.Ext(lower)
	return imageExt[ext] || videoExt[ext]
}

// Item is one photo or video in the library.
type Item struct {
	Root     string    `json:"root"`
	Path     string    `json:"path"`
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"mod_time"`
	Taken    time.Time `json:"taken"`
	Width    int       `json:"width,omitempty"`
	Height   int       `json:"height,omitempty"`
	Video    bool      `json:"video,omitempty"`
	Favorite bool      `json:"favorite,omitempty"`
}

type Library struct {
	files *files.Service
	index *files.Index
	store *store.Store
	cache string
	log   *slog.Logger

	mu       sync.Mutex
	scanning bool

	thumbSlots chan struct{}
	ffmpeg     string // "" when not installed
}

func New(fsvc *files.Service, ix *files.Index, st *store.Store, dataDir string, log *slog.Logger) *Library {
	n := runtime.NumCPU()
	if n < 2 {
		n = 2
	}
	ff, _ := exec.LookPath("ffmpeg")
	return &Library{files: fsvc, index: ix, store: st, cache: filepath.Join(dataDir, "photos-cache"), log: log, thumbSlots: make(chan struct{}, n), ffmpeg: ff}
}

// EnsureFolder creates the Photos folder on the first storage location that
// isn't the whole-disk System view, so uploads have somewhere to go.
func (l *Library) EnsureFolder() (root string, err error) {
	for _, r := range l.files.Roots() {
		if r.ID == "system" {
			continue
		}
		return r.ID, os.MkdirAll(filepath.Join(r.Path, Folder), 0o750)
	}
	return "", errors.New("no storage location for photos")
}

// List returns the library, newest first, with this user's favorites marked.
// Photos not yet dated are dated in the background (scanning reports that);
// meanwhile they sort by file time.
func (l *Library) List(ctx context.Context, userID string) (items []Item, scanning bool, err error) {
	hits := l.index.Under(Folder, isMedia)
	metas, err := l.store.PhotoMetas(ctx)
	if err != nil {
		return nil, false, err
	}
	favs, err := l.store.PhotoFavorites(ctx, userID)
	if err != nil {
		return nil, false, err
	}
	var todo []files.Hit
	items = make([]Item, 0, len(hits))
	for _, h := range hits {
		k := h.Root + "\x00" + h.Path
		it := Item{Root: h.Root, Path: h.Path, Name: h.Name, Size: h.Size, ModTime: h.ModTime, Taken: h.ModTime, Video: videoExt[strings.ToLower(path.Ext(h.Name))], Favorite: favs[k]}
		if m, ok := metas[k]; ok && m.Size == h.Size && m.MTime == h.ModTime.Unix() {
			it.Taken, it.Width, it.Height = time.Unix(m.Taken, 0).UTC(), m.Width, m.Height
		} else {
			todo = append(todo, h)
		}
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].Taken.Equal(items[j].Taken) {
			return items[i].Taken.After(items[j].Taken)
		}
		return items[i].Path < items[j].Path
	})
	l.mu.Lock()
	if len(todo) > 0 && !l.scanning {
		l.scanning = true
		go l.scan(todo, hits)
	}
	scanning = l.scanning
	l.mu.Unlock()
	return items, scanning, nil
}

// scan dates the given files and forgets cached facts about deleted ones.
func (l *Library) scan(todo, all []files.Hit) {
	defer func() {
		l.mu.Lock()
		l.scanning = false
		l.mu.Unlock()
	}()
	ctx := context.Background()
	batch := make([]store.PhotoMeta, 0, 64)
	flush := func() {
		if err := l.store.SavePhotoMetas(ctx, batch); err != nil {
			l.log.Warn("photos: save metadata", "err", err)
		}
		batch = batch[:0]
	}
	for _, h := range todo {
		m := store.PhotoMeta{PhotoKey: store.PhotoKey{Root: h.Root, Path: h.Path}, Size: h.Size, MTime: h.ModTime.Unix(), Taken: h.ModTime.Unix()}
		l.readMeta(h, &m)
		batch = append(batch, m)
		if len(batch) == cap(batch) {
			flush()
		}
	}
	flush()
	keep := make(map[string]bool, len(all))
	for _, h := range all {
		keep[h.Root+"\x00"+h.Path] = true
	}
	if err := l.store.PrunePhotoMetas(ctx, keep); err != nil {
		l.log.Warn("photos: prune metadata", "err", err)
	}
}

// readMeta fills in the date taken and the picture size, where it can.
func (l *Library) readMeta(h files.Hit, m *store.PhotoMeta) {
	f, _, err := l.files.Open(h.Root, strings.TrimPrefix(h.Path, "/"))
	if err != nil {
		return
	}
	defer f.Close()
	ext := strings.ToLower(path.Ext(h.Name))
	switch {
	case videoExt[ext]:
		if t, err := readMP4Created(f); err == nil {
			m.Taken = t.Unix()
		}
		return
	case ext == ".jpg" || ext == ".jpeg":
		if ex, err := readJPEGExif(f); err == nil {
			if !ex.taken.IsZero() {
				m.Taken = ex.taken.Unix()
			}
			defer func() {
				if ex.orientation >= 5 { // rotated a quarter turn: swap
					m.Width, m.Height = m.Height, m.Width
				}
			}()
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return
		}
	}
	if cfg, _, err := image.DecodeConfig(f); err == nil {
		m.Width, m.Height = cfg.Width, cfg.Height
	}
}

// Thumb sizes: the grid, and a large one for pictures the browser can't show.
var thumbSizes = map[string]int{"s": 480, "l": 1920}

// ErrNoThumb means no thumbnail can be made (a video or HEIC without ffmpeg).
var ErrNoThumb = errors.New("no thumbnail for this file")

// Thumb returns the path of a cached JPEG thumbnail, making it if needed.
func (l *Library) Thumb(ctx context.Context, root, rel, size string) (string, error) {
	px, ok := thumbSizes[size]
	if !ok {
		return "", fmt.Errorf("unknown size %q", size)
	}
	rel, err := files.Clean(rel)
	if err != nil {
		return "", err
	}
	ext := strings.ToLower(path.Ext(rel))
	if !imageExt[ext] && !videoExt[ext] {
		return "", ErrNoThumb
	}
	f, info, err := l.files.Open(root, rel)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if info.IsDir() {
		return "", ErrNoThumb
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%d", root, rel, info.Size(), info.ModTime().UnixNano())))
	out := filepath.Join(l.cache, size, hex.EncodeToString(sum[:2]), hex.EncodeToString(sum[:])+".jpg")
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}

	select {
	case l.thumbSlots <- struct{}{}:
		defer func() { <-l.thumbSlots }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
		return "", err
	}
	tmp := out + ".tmp"
	if videoExt[ext] || needsFFmpeg[ext] {
		if l.ffmpeg == "" {
			return "", ErrNoThumb
		}
		if err := l.ffmpegThumb(ctx, f, root, rel, tmp, px, videoExt[ext]); err != nil {
			os.Remove(tmp)
			return "", ErrNoThumb
		}
	} else if err := makeThumb(f, ext, px, tmp); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return out, os.Rename(tmp, out)
}

// makeThumb decodes a picture, stands it upright and writes a JPEG no larger than px.
func makeThumb(f *os.File, ext string, px int, out string) error {
	orientation := 1
	if ext == ".jpg" || ext == ".jpeg" {
		if ex, err := readJPEGExif(f); err == nil {
			orientation = ex.orientation
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
	}
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return err
	}
	if cfg.Width*cfg.Height > 120_000_000 {
		return errors.New("picture too large to preview")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	src, _, err := image.Decode(f)
	if err != nil {
		return err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > px || h > px {
		if w >= h {
			w, h = px, max(1, h*px/w)
		} else {
			w, h = max(1, w*px/h), px
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	// Transparent pictures get a white background in the JPEG.
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Over, nil)
	img := orient(dst, orientation)

	tf, err := os.Create(out)
	if err != nil {
		return err
	}
	if err := jpeg.Encode(tf, img, &jpeg.Options{Quality: 82}); err != nil {
		tf.Close()
		return err
	}
	return tf.Close()
}

// orient applies an EXIF orientation (2..8) so the picture is upright.
func orient(src *image.RGBA, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			i, j := src.PixOffset(x, y), dst.PixOffset(dx, dy)
			copy(dst.Pix[j:j+4], src.Pix[i:i+4])
		}
	}
	return dst
}

// ffmpegThumb grabs a frame from a video (or converts a HEIC picture) with ffmpeg.
// On Linux ffmpeg reads the already-opened file (so it can't be pointed outside
// the storage location); elsewhere it gets the file's path.
func (l *Library) ffmpegThumb(ctx context.Context, f *os.File, root, rel, out string, px int, video bool) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	in := "/dev/fd/3"
	if runtime.GOOS != "linux" {
		r, err := l.files.Root(root)
		if err != nil {
			return err
		}
		in = filepath.Join(r.Path, filepath.FromSlash(rel))
	}
	run := func(seek string) error {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		args := []string{"-hide_banner", "-loglevel", "error", "-y"}
		if video {
			args = append(args, "-ss", seek)
		}
		scale := fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease", px, px)
		args = append(args, "-i", in, "-frames:v", "1", "-vf", scale, "-f", "image2", "-c:v", "mjpeg", "-q:v", "4", out)
		cmd := exec.CommandContext(ctx, l.ffmpeg, args...)
		cmd.ExtraFiles = []*os.File{f}
		return cmd.Run()
	}
	if err := run("1"); err != nil {
		if !video {
			return err
		}
		return run("0") // shorter than a second: take the first frame
	}
	if st, err := os.Stat(out); err != nil || st.Size() == 0 {
		return ErrNoThumb
	}
	return nil
}

// Forget removes deleted photos from favorites, albums and the cache of facts.
func (l *Library) Forget(ctx context.Context, keys []store.PhotoKey) error {
	return l.store.ForgetPhotos(ctx, keys)
}

// Scanning reports whether photos are being dated in the background.
func (l *Library) Scanning() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.scanning
}
