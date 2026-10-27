// Package localhttp builds the HTTP clients htd talks to model servers with.
// Their requests carry code, so they never go through a proxy (Go would use
// HTTP_PROXY for any LAN address), are never redirected to another host,
// and a name that counts as local only by where it points is checked again
// when each connection opens.
package localhttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// Client returns an HTTP client for a model server.
func Client(timeout time.Duration) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dial(ctx, d, network, addr)
	}
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

// LocalName reports whether host is a bare name ("my-mac") or a .local
// name. Those count as local only when every address they resolve to is
// local: either can still point at the internet.
func LocalName(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	return h != "" && h != "localhost" && net.ParseIP(h) == nil && (!strings.Contains(h, ".") || strings.HasSuffix(h, ".local"))
}

// LocalIP reports whether an address is on this machine or its network.
func LocalIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}

// ErrLeftNetwork means a local name pointed off the network when htd
// connected, though it was local when checked.
var ErrLeftNetwork = errors.New("isn't on this machine or your local network any more")

// lookup resolves a name. Tests swap it.
var lookup = net.DefaultResolver.LookupIPAddr

// dial connects. A local name is resolved here and connected by address,
// only when every address is local: checking the name at setup and then
// letting the connection resolve it again would leave a window for it to
// change in between (DNS rebinding on the local network). IP addresses and
// full names were checked, or allowed by the user, as written.
func dial(ctx context.Context, d *net.Dialer, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || !LocalName(host) {
		return d.DialContext(ctx, network, addr)
	}
	addrs, err := lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%s: no addresses", host)
	}
	for _, a := range addrs {
		if !LocalIP(a.IP) {
			return nil, fmt.Errorf("%s now points to %s, which %w; htd won't send your code there", host, a.IP, ErrLeftNetwork)
		}
	}
	var last error
	for _, a := range addrs {
		ip := a.IP.String()
		if a.Zone != "" {
			ip += "%" + a.Zone
		}
		c, err := d.DialContext(ctx, network, net.JoinHostPort(ip, port))
		if err == nil {
			return c, nil
		}
		last = err
	}
	return nil, last
}
