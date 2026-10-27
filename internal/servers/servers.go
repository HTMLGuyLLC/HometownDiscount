// Package servers probes a local model server: what kind it is, which models
// it serves, the context it really uses, how they are quantized, how fast it
// decodes and whether it makes well-formed tool calls.
//
// A server is configured as just a URL; everything else is detected.
package servers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Server flavors.
const (
	FlavorOllama    = "ollama"
	FlavorLMStudio  = "lmstudio"
	FlavorOpenAI    = "openai" // generic OpenAI-compatible: MTPLX, Splish, llama.cpp, vLLM...
	FlavorAnthropic = "anthropic"
)

// Where a Model's Context came from.
const (
	ContextLoaded     = "loaded"     // the model is loaded with this context right now
	ContextConfigured = "configured" // the model's own settings pin it (Ollama num_ctx)
	ContextDefault    = "default"    // nothing says otherwise, so the server default applies
	ContextReported   = "reported"   // the server states it in its model list
)

// OllamaDefaultContext is the context Ollama uses when nothing sets num_ctx.
// Newer versions pick more on big GPUs, but 4096 is the floor, and the only
// value that is safe to assume for a model that isn't loaded.
const OllamaDefaultContext = 4096

var (
	// ErrUnknownServer means the URL answered, but not with any API we know.
	ErrUnknownServer = errors.New("no Ollama, LM Studio, OpenAI-compatible or Anthropic-compatible API found")
	// ErrCloudModel means the model runs off this machine and is refused.
	ErrCloudModel = errors.New("cloud model: it runs off this machine")
)

// Server is a probed model server.
type Server struct {
	URL     string
	Flavor  string // FlavorOllama, FlavorLMStudio, FlavorOpenAI, FlavorAnthropic
	Version string // when the server reports one
	Models  []Model
	Remote  bool // host isn't loopback/private: warn that data leaves the machine
}

// Model is one chat model a server offers.
type Model struct {
	ID            string
	Context       int    // max context the server will actually use for this model (0 = unknown)
	ContextSource string // ContextLoaded, ContextConfigured, ContextDefault, ContextReported, or "" (unknown)
	MaxContext    int    // the model's own maximum, when the server says (0 = unknown)
	Quant         Quant
	Cloud         bool // e.g. Ollama ":cloud" models that run remotely: must be refused
}

// ContextLimited reports whether the server will use less context than the
// model supports, e.g. Ollama at its 4096 default for a 262144-token model.
// The caller should warn and say how to raise it.
func (m Model) ContextLimited() bool {
	return m.Context > 0 && m.MaxContext > m.Context
}

// Find returns the model with this id. A bare Ollama name matches ":latest".
func (s *Server) Find(id string) (Model, bool) {
	for _, m := range s.Models {
		if m.ID == id {
			return m, true
		}
	}
	if !strings.Contains(id, ":") {
		for _, m := range s.Models {
			if m.ID == id+":latest" {
				return m, true
			}
		}
	}
	return Model{}, false
}

// detector tries to recognize one kind of server. On success it fills in s
// and returns true; otherwise it returns false and the last error it saw.
type detector func(ctx context.Context, s *Server) (bool, error)

// Probe works out what runs at rawURL. It tries Ollama, then LM Studio, then a
// generic OpenAI-compatible server, then an Anthropic-compatible one.
func Probe(ctx context.Context, rawURL string) (*Server, error) {
	u, err := parseURL(rawURL)
	if err != nil {
		return nil, err
	}
	s := &Server{URL: u.String(), Remote: !isLocalHost(u.Hostname())}

	var denied error
	for _, detect := range []detector{probeOllama, probeLMStudio, probeOpenAI, probeAnthropic} {
		found, err := detect(ctx, s)
		if found {
			return s, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var unreachable *unreachableError
		if errors.As(err, &unreachable) {
			return nil, fmt.Errorf("cannot reach %s: %w", s.URL, unreachable.err)
		}
		var status *statusError
		if errors.As(err, &status) && (status.Status == 401 || status.Status == 403) {
			denied = err
		}
	}
	if denied != nil {
		return nil, fmt.Errorf("%s asked for credentials, which htd doesn't send (%v)", s.URL, denied)
	}
	return nil, fmt.Errorf("%s: %w", s.URL, ErrUnknownServer)
}

// IsRemote reports whether the URL's host is off this machine's own network.
// Loopback, private (RFC 1918 and IPv6 ULA), link-local, ".local" and bare LAN
// names count as local. A URL that can't be read counts as remote.
func IsRemote(rawURL string) bool {
	u, err := parseURL(rawURL)
	return err != nil || !isLocalHost(u.Hostname())
}

// parseURL cleans a configured URL down to scheme, host and any proxy prefix:
// "127.0.0.1:8000" gets http://, and a trailing "/v1" or "/" is dropped.
func parseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("server URL is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("bad server URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, fmt.Errorf("bad server URL %q: want http(s)://host[:port]", raw)
	}
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/v1")
	u.RawPath, u.RawQuery, u.Fragment, u.RawFragment = "", "", "", ""
	u.User = nil // never keep (or print) credentials
	return u, nil
}

// isLocalHost reports whether a host name or IP is on this machine's network.
func isLocalHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if i := strings.IndexByte(h, '%'); i >= 0 { // IPv6 zone: fe80::1%en0
		h = h[:i]
	}
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
	}
	return !strings.Contains(h, ".") // "my-mac": a bare name can only be on the LAN
}

// checkModel validates the arguments of a benchmark call and refuses cloud models.
func checkModel(s *Server, model string) error {
	if s == nil || s.Flavor == "" {
		return errors.New("server not probed")
	}
	if model == "" {
		return errors.New("model name is empty")
	}
	if m, ok := s.Find(model); (ok && m.Cloud) || isCloudName(model) {
		return fmt.Errorf("%s: %w", model, ErrCloudModel)
	}
	return nil
}
