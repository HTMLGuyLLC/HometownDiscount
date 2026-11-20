package ui

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"strings"
)

//go:embed web
var web embed.FS

// CookieName is the cookie the page's key travels in.
const CookieName = "htd_ui"

// NewKey is a fresh random key for one run of htd ui.
func NewKey() string {
	b := make([]byte, 24)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Handler serves the page and its data. load is called for each data
// request, so the page shows new results as they're graded. dir is the
// folder config.toml is in, which the settings form reads and writes.
//
// The page is for this computer only: requests must name this computer
// (127.0.0.1, ::1 or localhost) as their host, which stops another site from
// reading it through DNS rebinding. Its data is for whoever opened the
// address htd ui printed: that address carries a key (?k=...), which is
// swapped for a cookie and dropped from the address bar, and every /api/
// request needs the cookie. Another account on this computer can see that
// the port is open but can't read or change anything without the key. The
// only writes are the settings form's. Everything the page needs is served
// from here; it loads nothing from anywhere else.
func Handler(load func() (Report, error), dir, key string) http.Handler {
	files, _ := fs.Sub(web, "web")
	static := http.FileServerFS(files)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{path...}", func(w http.ResponseWriter, r *http.Request) {
		if k := r.URL.Query().Get("k"); k != "" && r.URL.Path == "/" && sameKey(k, key) {
			http.SetCookie(w, &http.Cookie{Name: CookieName, Value: key, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		static.ServeHTTP(w, r)
	})
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
	return guard(withKey(mux, key))
}

// sameKey compares keys without leaking how much matched. An empty key
// never matches.
func sameKey(got, key string) bool {
	return key != "" && subtle.ConstantTimeCompare([]byte(got), []byte(key)) == 1
}

// withKey turns away /api/ requests that don't carry the run's key cookie.
func withKey(next http.Handler, key string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if c, err := r.Cookie(CookieName); err != nil || !sameKey(c.Value, key) {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "This page's link has expired. Run `htd ui` again; it opens a fresh page."})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
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
