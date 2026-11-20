package ui

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"strings"
)

//go:embed web
var web embed.FS

// Handler serves the page and its data. load is called for each data
// request, so the page shows new results as they're graded. dir is the
// folder config.toml is in, which the settings form reads and writes.
//
// The page is for this computer only: requests must name this computer
// (127.0.0.1, ::1 or localhost) as their host, which stops another site from
// reading it through DNS rebinding. The only writes are the settings form's,
// which need a token only this page can read. Everything the page needs is
// served from here; it loads nothing from anywhere else.
func Handler(load func() (Report, error), dir string) http.Handler {
	files, _ := fs.Sub(web, "web")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(files))
	mux.HandleFunc("GET /api/report", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		rep, err := load()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(rep)
	})
	api := newSettingsAPI(dir)
	mux.HandleFunc("GET /api/settings", api.get)
	mux.HandleFunc("POST /api/settings/preview", api.write(false))
	mux.HandleFunc("POST /api/settings", api.write(true))
	return guard(mux)
}

func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !LocalHost(r.Host) {
			http.Error(w, "htd ui answers only on this computer (127.0.0.1)", http.StatusForbidden)
			return
		}
		write := r.Method == http.MethodPost && (r.URL.Path == "/api/settings" || r.URL.Path == "/api/settings/preview")
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !write {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "read only", http.StatusMethodNotAllowed)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// LocalHost reports whether a request's Host names this computer.
func LocalHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
