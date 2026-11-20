package ui

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

const testKey = "0123456789abcdef"

func get(h http.Handler, method, path, host string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Host = host
	if strings.HasPrefix(path, "/api/") {
		req.AddCookie(&http.Cookie{Name: CookieName, Value: testKey})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestHandler(t *testing.T) {
	h := Handler(func() (Report, error) { return Build(Input{}), nil }, t.TempDir(), testKey)

	w := get(h, "GET", "/", "127.0.0.1:8765")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "What your local models have earned") {
		t.Fatalf("page: %d %s", w.Code, w.Body.String())
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("CSP: %q", csp)
	}
	for _, f := range []string{"/app.js", "/app.css", "/favicon.svg"} {
		if w := get(h, "GET", f, "localhost:8765"); w.Code != 200 {
			t.Errorf("%s: %d", f, w.Code)
		}
	}

	w = get(h, "GET", "/api/report", "[::1]:8765")
	var rep Report
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &rep) != nil || w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("report: %d %s", w.Code, w.Body.String())
	}
}

// Another site can't read the page by pointing its own name at 127.0.0.1,
// and nothing can be changed through it.
func TestHandlerGuards(t *testing.T) {
	h := Handler(func() (Report, error) { return Report{}, nil }, t.TempDir(), testKey)
	for _, host := range []string{"evil.example", "evil.example:8765", "127.0.0.1.evil.example", "192.168.1.5:8765", ""} {
		if w := get(h, "GET", "/api/report", host); w.Code != http.StatusForbidden {
			t.Errorf("host %q: %d", host, w.Code)
		}
	}
	for _, m := range []string{"POST", "PUT", "DELETE"} {
		if w := get(h, m, "/api/report", "127.0.0.1:1"); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d", m, w.Code)
		}
	}
}

func TestHandlerLoadError(t *testing.T) {
	h := Handler(func() (Report, error) { return Report{}, errors.New("unknown setting \"spede\"") }, t.TempDir(), testKey)
	w := get(h, "GET", "/api/report", "127.0.0.1:1")
	var body map[string]string
	if w.Code != 500 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body["error"] != `unknown setting "spede"` {
		t.Errorf("%d %s", w.Code, w.Body.String())
	}
}

func TestLocalHost(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1": true, "127.0.0.1:80": true, "localhost:9": true, "LOCALHOST": true, "[::1]:9": true, "::1": true,
		"example.com": false, "10.0.0.1:9": false, "localhost.example.com": false, "": false,
	} {
		if got := LocalHost(host); got != want {
			t.Errorf("LocalHost(%q) = %v", host, got)
		}
	}
}

// The page loads nothing from anywhere else: no fonts, scripts or images
// from the web. The only URL allowed is the SVG namespace, which is a name,
// not something fetched.
func TestNothingLoadsFromOutside(t *testing.T) {
	url := regexp.MustCompile(`(?i)(https?:)?//[a-z0-9.-]+\.[a-z]{2,}`)
	fs.WalkDir(web, "web", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := web.ReadFile(path)
		for _, m := range url.FindAllString(string(b), -1) {
			if m != "http://www.w3.org" {
				t.Errorf("%s refers to %s", path, m)
			}
		}
		return nil
	})
}

// The page is served to anyone on this computer, but its data needs the key
// htd ui printed: ?k= is swapped for a cookie, and /api/ needs the cookie.
func TestKeyAndCookie(t *testing.T) {
	h := Handler(func() (Report, error) { return Build(Input{}), nil }, t.TempDir(), testKey)
	api := func(path, cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Host = "127.0.0.1:1"
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	for _, path := range []string{"/api/report", "/api/settings"} {
		for _, cookie := range []string{"", "wrong", testKey + "x"} {
			w := api(path, cookie)
			var body map[string]string
			if w.Code != http.StatusUnauthorized || json.Unmarshal(w.Body.Bytes(), &body) != nil || body["error"] == "" {
				t.Errorf("%s with cookie %q: %d %s", path, cookie, w.Code, w.Body.String())
			}
		}
		if w := api(path, testKey); w.Code != 200 {
			t.Errorf("%s with the key: %d", path, w.Code)
		}
	}

	// The right key becomes a cookie and leaves the address bar.
	w := get(h, "GET", "/?k="+testKey, "127.0.0.1:1")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("key in the address: %d, goes to %q", w.Code, w.Header().Get("Location"))
	}
	cs := w.Result().Cookies()
	if len(cs) != 1 || cs[0].Name != CookieName || cs[0].Value != testKey || !cs[0].HttpOnly || cs[0].SameSite != http.SameSiteStrictMode || cs[0].Path != "/" {
		t.Errorf("cookie: %+v", cs)
	}

	// A wrong key sets nothing; the page still loads and its first call says the link expired.
	w = get(h, "GET", "/?k=nope", "127.0.0.1:1")
	if len(w.Result().Cookies()) != 0 || w.Code != 200 {
		t.Errorf("wrong key: %d, cookies %v", w.Code, w.Result().Cookies())
	}
	// The page and its files need no key.
	if w := get(h, "GET", "/", "127.0.0.1:1"); w.Code != 200 {
		t.Errorf("page without a key: %d", w.Code)
	}
	if w := get(h, "GET", "/app.js", "127.0.0.1:1"); w.Code != 200 {
		t.Errorf("script without a key: %d", w.Code)
	}
	// A run without a key at all turns everything away.
	none := Handler(func() (Report, error) { return Report{}, nil }, t.TempDir(), "")
	req := httptest.NewRequest("GET", "/api/report", nil)
	req.Host = "127.0.0.1:1"
	req.AddCookie(&http.Cookie{Name: CookieName, Value: ""})
	rec := httptest.NewRecorder()
	none.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("empty key: %d", rec.Code)
	}
	if w := get(none, "GET", "/?k=", "127.0.0.1:1"); len(w.Result().Cookies()) != 0 {
		t.Error("an empty key must not set a cookie")
	}
}
