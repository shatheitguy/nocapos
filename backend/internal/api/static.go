package api

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

// staticHandler serves the embedded UI. Unknown paths fall back to index.html
// so client-side routes survive a reload. Scripts, styles and the page itself
// go out gzipped to clients that accept it.
func staticHandler(assets fs.FS) http.Handler {
	files := http.FileServer(http.FS(assets))
	gz := &gzipCache{fsys: assets, m: map[string][]byte{}}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if _, err := fs.Stat(assets, name); err == nil {
				switch {
				case strings.HasPrefix(name, "assets/"): // content-hashed by the build
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
					if gz.serve(w, r, name) {
						return
					}
				case strings.HasPrefix(name, "wallpapers/"):
					w.Header().Set("Cache-Control", "public, max-age=604800")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		// Serve index.html directly; FileServer would redirect /index.html → /.
		if gz.serve(w, r, "index.html") {
			return
		}
		index, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}

// compressible lists the text formats worth gzipping. Images and fonts
// (woff/woff2) are already compressed.
var compressible = map[string]bool{
	".html": true, ".js": true, ".mjs": true, ".css": true, ".json": true,
	".svg": true, ".txt": true, ".map": true, ".webmanifest": true,
}

// gzipCache compresses each embedded file once, on first request, and keeps
// the result in memory. The files never change while alfad runs.
type gzipCache struct {
	fsys fs.FS
	mu   sync.Mutex
	m    map[string][]byte // nil entry: not worth compressing
}

// serve writes the gzipped file when that applies; false means the caller
// should serve it as is.
func (c *gzipCache) serve(w http.ResponseWriter, r *http.Request, name string) bool {
	ext := path.Ext(name)
	if !compressible[ext] {
		return false
	}
	w.Header().Add("Vary", "Accept-Encoding")
	if !acceptsGzip(r.Header.Get("Accept-Encoding")) {
		return false
	}
	body := c.get(name)
	if body == nil {
		return false
	}
	ctype := mime.TypeByExtension(ext)
	if ext == ".html" {
		ctype = "text/html; charset=utf-8"
	}
	if ctype != "" {
		w.Header().Set("Content-Type", ctype)
	}
	w.Header().Set("Content-Encoding", "gzip")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
	return true
}

func (c *gzipCache) get(name string) []byte {
	c.mu.Lock()
	b, ok := c.m[name]
	c.mu.Unlock()
	if ok {
		return b
	}
	// Compressed outside the lock so one big bundle doesn't hold up the rest;
	// two first requests may both compress, which is harmless.
	raw, err := fs.ReadFile(c.fsys, name)
	if err != nil {
		return nil // not cached: the plain path reports the error
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	var out []byte
	if buf.Len() < len(raw)*9/10 { // skip files that barely shrink
		out = buf.Bytes()
	}
	c.mu.Lock()
	c.m[name] = out
	c.mu.Unlock()
	return out
}

// acceptsGzip reports whether an Accept-Encoding header allows gzip.
func acceptsGzip(h string) bool {
	for _, part := range strings.Split(h, ",") {
		enc, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		enc = strings.ToLower(strings.TrimSpace(enc))
		if enc != "gzip" && enc != "*" {
			continue
		}
		q := strings.ReplaceAll(strings.ToLower(params), " ", "")
		if q == "q=0" || q == "q=0.0" || q == "q=0.00" || q == "q=0.000" {
			if enc == "gzip" {
				return false
			}
			continue
		}
		return true
	}
	return false
}
