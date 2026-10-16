package tlsutil

import (
	"net"
	"net/http"
	"strings"
)

// RedirectHandler sends plain-HTTP requests to the same host over HTTPS on
// httpsPort, so http://nocap.local (and http://<ip>) open NoCapOS.
func RedirectHandler(httpsPort string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		if host == "" || strings.ContainsAny(host, "/\\@ ") {
			http.Error(w, "use https", http.StatusBadRequest)
			return
		}
		if strings.Contains(host, ":") {
			host = "[" + host + "]" // IPv6
		}
		if httpsPort != "" && httpsPort != "443" {
			host += ":" + httpsPort
		}
		target := "https://" + host + r.URL.RequestURI()
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
	})
}
