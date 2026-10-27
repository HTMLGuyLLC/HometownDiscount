// Package localhttp builds the HTTP clients htd talks to model servers with.
// Their requests carry code, so they never go through a proxy (Go would use
// HTTP_PROXY for any LAN address) and are never redirected to another host.
package localhttp

import (
	"fmt"
	"net/http"
	"time"
)

// Client returns an HTTP client for a model server.
func Client(timeout time.Duration) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return &http.Client{Timeout: timeout, Transport: t, CheckRedirect: sameHost}
}

// sameHost allows a redirect only to the same scheme and host.
func sameHost(req *http.Request, via []*http.Request) error {
	if len(via) >= 3 {
		return fmt.Errorf("too many redirects")
	}
	if first := via[0].URL; req.URL.Scheme != first.Scheme || req.URL.Host != first.Host {
		return fmt.Errorf("refused a redirect to %s: htd only talks to the model server you set", req.URL.Host)
	}
	return nil
}
