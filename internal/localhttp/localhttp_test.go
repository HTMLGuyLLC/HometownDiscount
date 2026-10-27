package localhttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A proxy setting is ignored: a LAN model server's prompts carry code.
func TestNoProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy.example:3128")
	t.Setenv("HTTPS_PROXY", "http://proxy.example:3128")
	c := Client(time.Second)
	if c.Transport.(*http.Transport).Proxy != nil {
		t.Error("the transport must not read proxy settings")
	}
}

// A redirect to another host is refused; one on the same host is followed.
func TestRedirects(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("leaked")) }))
	defer other.Close()
	var self *httptest.Server
	self = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/away":
			http.Redirect(w, r, other.URL+"/x", http.StatusTemporaryRedirect)
		case "/here":
			http.Redirect(w, r, self.URL+"/ok", http.StatusTemporaryRedirect)
		default:
			w.Write([]byte("ok"))
		}
	}))
	defer self.Close()
	c := Client(5 * time.Second)
	if _, err := c.Post(self.URL+"/away", "application/json", strings.NewReader(`{"code":"secret"}`)); err == nil || !strings.Contains(err.Error(), "refused a redirect") {
		t.Errorf("redirect to another host: err = %v", err)
	}
	resp, err := c.Get(self.URL + "/here")
	if err != nil || resp.StatusCode != 200 {
		t.Errorf("same-host redirect: %v %v", resp, err)
	}
}
