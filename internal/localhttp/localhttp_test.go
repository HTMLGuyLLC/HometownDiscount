package localhttp

import (
	"context"
	"errors"
	"io"
	"net"
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

// A bare or .local name is checked again when the connection opens: one
// that points off the network by then is refused, so a name can't pass the
// setup check and then send code somewhere else (DNS rebinding).
func TestLocalNamesCheckedAtConnect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok " + r.Host)) }))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	defer func(f func(context.Context, string) ([]net.IPAddr, error)) { lookup = f }(lookup)
	answer := "127.0.0.1"
	lookup = func(_ context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP(answer)}}, nil
	}
	c := Client(5 * time.Second)

	resp, err := c.Get("http://gpu-box:" + port + "/")
	if err != nil {
		t.Fatalf("a name pointing here should connect: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok gpu-box:"+port {
		t.Errorf("the request should still name the host it was sent to, got %q", body)
	}

	answer = "203.0.113.9"
	c = Client(5 * time.Second) // no kept-alive connection to reuse
	if _, err := c.Get("http://gpu-box.local:" + port + "/"); !errors.Is(err, ErrLeftNetwork) {
		t.Errorf("a name that now points off the network must be refused, got %v", err)
	}
}

func TestLocalName(t *testing.T) {
	for h, want := range map[string]bool{
		"gpu-box": true, "GPU-Box.local.": true, "studio.local": true,
		"localhost": false, "127.0.0.1": false, "::1": false, "models.example.com": false, "": false,
	} {
		if got := LocalName(h); got != want {
			t.Errorf("LocalName(%q) = %v, want %v", h, got, want)
		}
	}
}
