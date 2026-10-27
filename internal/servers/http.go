package servers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	probeTimeout = 10 * time.Second // one probe request
	callTimeout  = 2 * time.Minute  // one generation (speed or tool-call test)
	maxBody      = 16 << 20
)

// client sends every request. Tests swap it to fake remote hosts.
var client = &http.Client{}

// unreachableError means no HTTP reply came back (refused, timed out, DNS...).
type unreachableError struct{ err error }

func (e *unreachableError) Error() string { return e.err.Error() }
func (e *unreachableError) Unwrap() error { return e.err }

// statusError is a non-2xx reply. Body is kept so callers can look for
// provider-shaped error JSON.
type statusError struct {
	Status int
	Body   []byte
}

func (e *statusError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Status, snippet(e.Body))
}

// snippet shortens a reply body for an error message.
func snippet(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

// reqOpt tweaks a request before it is sent.
type reqOpt func(*http.Request)

// anthropicHeaders marks a request as an Anthropic Messages call.
func anthropicHeaders(r *http.Request) {
	r.Header.Set("anthropic-version", "2023-06-01")
}

// send makes one request and decodes the JSON reply into out (nil to ignore it).
func send(ctx context.Context, timeout time.Duration, method, url string, body, out any, opts ...reqOpt) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, o := range opts {
		o(req)
	}

	resp, err := client.Do(req)
	if err != nil {
		return &unreachableError{err}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return &unreachableError{err}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &statusError{Status: resp.StatusCode, Body: data}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s %s: bad JSON: %w", method, url, err)
	}
	return nil
}

// getJSON is a probe-time GET.
func getJSON(ctx context.Context, url string, out any) error {
	return send(ctx, probeTimeout, http.MethodGet, url, nil, out)
}

// postProbe is a probe-time POST (Ollama's /api/show, the Anthropic check).
func postProbe(ctx context.Context, url string, body, out any, opts ...reqOpt) error {
	return send(ctx, probeTimeout, http.MethodPost, url, body, out, opts...)
}

// postJSON is a generation-time POST.
func postJSON(ctx context.Context, url string, body, out any, opts ...reqOpt) error {
	return send(ctx, callTimeout, http.MethodPost, url, body, out, opts...)
}
