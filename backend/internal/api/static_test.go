package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestStaticGzip(t *testing.T) {
	js := strings.Repeat("console.log('hello');\n", 200)
	html := "<!doctype html><title>x</title>" + strings.Repeat("<div></div>", 200)
	h := staticHandler(fstest.MapFS{
		"index.html":        {Data: []byte(html)},
		"assets/app.js":     {Data: []byte(js)},
		"assets/font.woff2": {Data: []byte(strings.Repeat("x", 4000))},
	})
	get := func(p, enc string) *http.Response {
		r := httptest.NewRequest(http.MethodGet, p, nil)
		if enc != "" {
			r.Header.Set("Accept-Encoding", enc)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Result()
	}

	res := get("/assets/app.js", "gzip, deflate, br")
	if res.Header.Get("Content-Encoding") != "gzip" || !strings.Contains(res.Header.Get("Vary"), "Accept-Encoding") {
		t.Fatalf("app.js headers: %v", res.Header)
	}
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("app.js Content-Type %q", ct)
	}
	zr, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if body, _ := io.ReadAll(zr); string(body) != js {
		t.Error("app.js: gzipped body doesn't match")
	}

	if res := get("/assets/app.js", ""); res.Header.Get("Content-Encoding") != "" {
		t.Error("no Accept-Encoding: must not gzip")
	}
	if res := get("/assets/app.js", "gzip;q=0"); res.Header.Get("Content-Encoding") != "" {
		t.Error("gzip;q=0: must not gzip")
	}
	if res := get("/assets/font.woff2", "gzip"); res.Header.Get("Content-Encoding") != "" {
		t.Error("fonts are already compressed")
	}
	res = get("/some/client/route", "gzip")
	if res.Header.Get("Content-Encoding") != "gzip" || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
		t.Errorf("index fallback headers: %v", res.Header)
	}
}
