package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// staticHandler serves the embedded UI. Unknown paths fall back to index.html
// so client-side routes survive a reload.
func staticHandler(assets fs.FS) http.Handler {
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if _, err := fs.Stat(assets, name); err == nil {
				files.ServeHTTP(w, r)
				return
			}
		}
		// Serve index.html directly; FileServer would redirect /index.html → /.
		index, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}
