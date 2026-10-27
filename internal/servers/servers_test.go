package servers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// reply writes v as a JSON reply.
func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// obj is a JSON object literal.
type obj = map[string]any

// fakeServer serves the given routes ("GET /path") and 404s everything else.
func fakeServer(t *testing.T, routes map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for pattern, h := range routes {
		mux.HandleFunc(pattern, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// json200 is a handler that always replies with v.
func json200(v any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { reply(w, v) }
}

// ollamaFake emulates Ollama: six chat models, one embedding model, two cloud
// models. It counts /api/show calls per model.
type ollamaFake struct {
	mu    sync.Mutex
	shown map[string]int
}

func (f *ollamaFake) count(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shown[id]
}

func newOllamaFake(t *testing.T) (*httptest.Server, *ollamaFake) {
	f := &ollamaFake{shown: map[string]int{}}
	details := func(format, level string) obj {
		return obj{"format": format, "family": "x", "quantization_level": level}
	}
	tags := obj{"models": []obj{
		{"name": "qwen3:27b-q4_K_M", "model": "qwen3:27b-q4_K_M", "details": details("gguf", "Q4_K_M")},
		{"name": "llama3.1:8b", "model": "llama3.1:8b", "details": details("gguf", "Q4_0")},
		{"name": "mistral:7b", "model": "mistral:7b", "details": details("gguf", "")},
		{"name": "qwen3-vl:8b-instruct-q8_0", "model": "qwen3-vl:8b-instruct-q8_0", "details": details("gguf", "")},
		{"name": "nomic-embed-text:latest", "model": "nomic-embed-text:latest", "details": details("gguf", "F16")},
		{"name": "gpt-oss:120b-cloud", "model": "gpt-oss:120b-cloud", "remote_host": "https://ollama.com:443", "remote_model": "gpt-oss:120b"},
		{"name": "kimi-k2:cloud", "model": "kimi-k2:cloud"},
	}}
	shows := map[string]obj{
		"qwen3:27b-q4_K_M": {
			"parameters":   "temperature 0.6",
			"details":      details("gguf", "Q4_K_M"),
			"model_info":   obj{"general.architecture": "qwen3", "qwen3.context_length": 262144},
			"capabilities": []string{"completion", "tools"},
		},
		"llama3.1:8b": {
			"parameters":   "stop \"<|eot_id|>\"\nnum_ctx                        16384\ntemperature 0.7",
			"details":      details("gguf", "Q4_0"),
			"model_info":   obj{"general.architecture": "llama", "llama.context_length": 131072},
			"capabilities": []string{"completion"},
		},
		"mistral:7b": {
			"details":      details("gguf", ""),
			"model_info":   obj{"general.architecture": "llama", "llama.context_length": 32768},
			"capabilities": []string{"completion"},
		},
		"qwen3-vl:8b-instruct-q8_0": {
			"details":      details("gguf", ""),
			"model_info":   obj{"general.architecture": "qwen3vl", "qwen3vl.context_length": 262144},
			"capabilities": []string{"completion", "vision"},
		},
		"nomic-embed-text:latest": {
			"details":      details("gguf", "F16"),
			"model_info":   obj{"general.architecture": "nomic-bert", "nomic-bert.context_length": 2048},
			"capabilities": []string{"embedding"},
		},
	}
	srv := fakeServer(t, map[string]http.HandlerFunc{
		"GET /api/version": json200(obj{"version": "0.12.3"}),
		"GET /api/tags":    json200(tags),
		"GET /api/ps": json200(obj{"models": []obj{
			{"name": "qwen3:27b-q4_K_M", "model": "qwen3:27b-q4_K_M", "context_length": 65536},
		}}),
		"POST /api/show": func(w http.ResponseWriter, r *http.Request) {
			var req struct{ Model string }
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.mu.Lock()
			f.shown[req.Model]++
			f.mu.Unlock()
			show, ok := shows[req.Model]
			if !ok {
				http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
				return
			}
			reply(w, show)
		},
	})
	return srv, f
}

func TestProbeOllama(t *testing.T) {
	srv, fake := newOllamaFake(t)
	s, err := Probe(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if s.Flavor != FlavorOllama || s.Version != "0.12.3" || s.Remote {
		t.Fatalf("server = %+v", s)
	}

	want := []Model{
		// Loaded: /api/ps shows the real context, below the model's 262144 max.
		{ID: "qwen3:27b-q4_K_M", Context: 65536, ContextSource: ContextLoaded, MaxContext: 262144,
			Quant: Quant{"gguf", 4, "Q4_K_M", SourceServer}},
		// Not loaded, num_ctx set in the model's parameters.
		{ID: "llama3.1:8b", Context: 16384, ContextSource: ContextConfigured, MaxContext: 131072,
			Quant: Quant{"gguf", 4, "Q4_0", SourceServer}},
		// Not loaded, nothing set: Ollama's default, and no quant anywhere.
		{ID: "mistral:7b", Context: OllamaDefaultContext, ContextSource: ContextDefault, MaxContext: 32768},
		// The server reports no level, so the quant comes from the tag.
		{ID: "qwen3-vl:8b-instruct-q8_0", Context: OllamaDefaultContext, ContextSource: ContextDefault, MaxContext: 262144,
			Quant: Quant{"gguf", 8, "Q8_0", SourceName}},
		// Cloud models are flagged, not detailed.
		{ID: "gpt-oss:120b-cloud", Cloud: true},
		{ID: "kimi-k2:cloud", Cloud: true},
	}
	if !reflect.DeepEqual(s.Models, want) {
		for i := range s.Models {
			t.Logf("model %d = %+v", i, s.Models[i])
		}
		t.Fatalf("models differ from want (embedding model should be left out, got %d models)", len(s.Models))
	}

	for _, id := range []string{"gpt-oss:120b-cloud", "kimi-k2:cloud"} {
		if fake.count(id) != 0 {
			t.Errorf("/api/show was called for cloud model %s", id)
		}
	}
	if !s.Models[0].ContextLimited() || s.Models[0].Context != 65536 {
		t.Error("loaded 64k of a 256k model should read as limited")
	}
	if !s.Models[2].ContextLimited() {
		t.Error("Ollama's 4096 default for a 32k model should read as limited")
	}
	if m, ok := s.Find("mistral"); ok {
		t.Errorf("Find(mistral) = %+v, but only mistral:7b exists, not mistral:latest", m)
	}
}

func TestOllamaShowFailureLeavesContextUnknown(t *testing.T) {
	srv := fakeServer(t, map[string]http.HandlerFunc{
		"GET /api/version": json200(obj{"version": "0.12.3"}),
		"GET /api/tags":    json200(obj{"models": []obj{{"name": "a:latest", "details": obj{"quantization_level": "Q4_K_M", "format": "gguf"}}}}),
		// no /api/show, no /api/ps
	})
	s, err := Probe(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	m := s.Models[0]
	if m.Context != 0 || m.ContextSource != "" || m.Quant.Bits != 4 {
		t.Errorf("model = %+v", m)
	}
	if got, ok := s.Find("a"); !ok || got.ID != "a:latest" {
		t.Errorf("Find(a) = %+v, %v", got, ok)
	}
}

func TestProbeLMStudioV0(t *testing.T) {
	srv := fakeServer(t, map[string]http.HandlerFunc{
		"GET /api/v0/models": json200(obj{"object": "list", "data": []obj{
			{"id": "mlx-community/qwen3.8-27b-4bit", "object": "model", "type": "llm", "publisher": "mlx-community",
				"arch": "qwen3", "compatibility_type": "mlx", "quantization": "4bit", "state": "loaded",
				"max_context_length": 262144, "loaded_context_length": 131072},
			{"id": "qwen3-coder-30b", "object": "model", "type": "llm", "arch": "qwen3moe",
				"compatibility_type": "gguf", "quantization": "Q4_K_M", "state": "not-loaded", "max_context_length": 262144},
			{"id": "text-embedding-nomic-embed-text-v1.5", "object": "model", "type": "embeddings",
				"compatibility_type": "gguf", "quantization": "Q4_K_M", "state": "not-loaded", "max_context_length": 2048},
		}}),
		"GET /v1/models": json200(obj{"object": "list", "data": []obj{{"id": "should-not-be-used", "object": "model"}}}),
	})
	s, err := Probe(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	want := []Model{
		{ID: "mlx-community/qwen3.8-27b-4bit", Context: 131072, ContextSource: ContextLoaded, MaxContext: 262144,
			Quant: Quant{"mlx", 4, "4bit", SourceServer}},
		// Not loaded: LM Studio chooses the context at load time, so it is unknown.
		{ID: "qwen3-coder-30b", MaxContext: 262144, Quant: Quant{"gguf", 4, "Q4_K_M", SourceServer}},
	}
	if s.Flavor != FlavorLMStudio || !reflect.DeepEqual(s.Models, want) {
		t.Errorf("flavor = %q, models = %+v", s.Flavor, s.Models)
	}
}

func TestProbeLMStudioV1Fallback(t *testing.T) {
	srv := fakeServer(t, map[string]http.HandlerFunc{
		"GET /api/v1/models": json200(obj{"models": []obj{
			{"type": "llm", "key": "acme/sample-7b", "format": "gguf", "max_context_length": 32768,
				"quantization":     obj{"name": "Q5_K_M", "bits_per_weight": 5},
				"loaded_instances": []obj{{"id": "acme/sample-7b", "config": obj{"context_length": 8192}}}},
			{"type": "llm", "key": "acme/odd-7b", "format": "gguf", "max_context_length": 32768,
				"quantization": obj{"name": "weird", "bits_per_weight": 6}, "loaded_instances": []obj{}},
			{"type": "embedding", "key": "acme/embed", "format": "gguf", "quantization": obj{"name": "F16", "bits_per_weight": 16}},
		}}),
	})
	s, err := Probe(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	want := []Model{
		{ID: "acme/sample-7b", Context: 8192, ContextSource: ContextLoaded, MaxContext: 32768,
			Quant: Quant{"gguf", 5, "Q5_K_M", SourceServer}},
		{ID: "acme/odd-7b", MaxContext: 32768, Quant: Quant{"gguf", 6, "weird", SourceServer}},
	}
	if s.Flavor != FlavorLMStudio || !reflect.DeepEqual(s.Models, want) {
		t.Errorf("flavor = %q, models = %+v", s.Flavor, s.Models)
	}
}

func TestProbeOpenAIMTPLX(t *testing.T) {
	// MTPLX and Splish document no info endpoint: only /v1/models and a name.
	srv := fakeServer(t, map[string]http.HandlerFunc{
		"GET /v1/models": json200(obj{"object": "list", "data": []obj{
			{"id": "mlx-community/Qwen3.8-27B-4bit", "object": "model", "capability": "chat"},
			{"id": "Qwen3.8-27B", "object": "model", "capability": "chat"},
		}}),
	})
	s, err := Probe(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	want := []Model{
		{ID: "mlx-community/Qwen3.8-27B-4bit", Quant: Quant{"mlx", 4, "4bit", SourceName}},
		{ID: "Qwen3.8-27B"}, // quant and context unknown: the wizard asks
	}
	if s.Flavor != FlavorOpenAI || s.Version != "" || !reflect.DeepEqual(s.Models, want) {
		t.Errorf("flavor = %q, version = %q, models = %+v", s.Flavor, s.Version, s.Models)
	}
	if s.Models[1].Quant.Known() {
		t.Error("an id without a quant must leave it unknown")
	}
}

func TestProbeOpenAIVLLM(t *testing.T) {
	srv := fakeServer(t, map[string]http.HandlerFunc{
		"GET /v1/models": json200(obj{"object": "list", "data": []obj{
			{"id": "Qwen/Qwen3-32B-AWQ", "object": "model", "max_model_len": 40960},
		}}),
		"GET /version": json200(obj{"version": "0.11.0"}),
	})
	s, err := Probe(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	want := Model{ID: "Qwen/Qwen3-32B-AWQ", Context: 40960, ContextSource: ContextReported,
		Quant: Quant{"safetensors", 4, "awq", SourceName}}
	if s.Flavor != FlavorOpenAI || s.Version != "0.11.0" || !reflect.DeepEqual(s.Models, []Model{want}) {
		t.Errorf("server = %+v", s)
	}
}

func TestProbeOpenAILlamaCpp(t *testing.T) {
	srv := fakeServer(t, map[string]http.HandlerFunc{
		"GET /v1/models": json200(obj{"object": "list", "data": []obj{
			{"id": "qwen", "object": "model", "owned_by": "llamacpp", "meta": obj{"n_ctx_train": 262144}},
		}}),
		"GET /props": json200(obj{
			"build_info":                  "b6789-abc1234",
			"model_path":                  "/models/Qwen3-27B-Q5_K_M.gguf",
			"default_generation_settings": obj{"n_ctx": 32768},
		}),
	})
	s, err := Probe(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	want := Model{ID: "qwen", Context: 32768, ContextSource: ContextReported, MaxContext: 262144,
		Quant: Quant{"gguf", 5, "Q5_K_M", SourceName}}
	if s.Version != "b6789-abc1234" || !reflect.DeepEqual(s.Models, []Model{want}) {
		t.Errorf("server = %+v", s)
	}
	if !s.Models[0].ContextLimited() {
		t.Error("32k served of a 256k model should read as limited")
	}
}

func TestProbeAnthropic(t *testing.T) {
	t.Run("message reply", func(t *testing.T) {
		var gotVersion string
		srv := fakeServer(t, map[string]http.HandlerFunc{
			"POST /v1/messages": func(w http.ResponseWriter, r *http.Request) {
				gotVersion = r.Header.Get("anthropic-version")
				reply(w, obj{"id": "msg_1", "type": "message", "role": "assistant", "model": "qwen3-27b-q4_K_M",
					"content": []obj{{"type": "text", "text": "hi"}}, "usage": obj{"input_tokens": 1, "output_tokens": 1}})
			},
		})
		s, err := Probe(context.Background(), srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		want := []Model{{ID: "qwen3-27b-q4_K_M", Quant: Quant{"gguf", 4, "Q4_K_M", SourceName}}}
		if s.Flavor != FlavorAnthropic || !reflect.DeepEqual(s.Models, want) {
			t.Errorf("server = %+v", s)
		}
		if gotVersion == "" {
			t.Error("anthropic-version header not sent")
		}
	})

	t.Run("error in Anthropic's shape", func(t *testing.T) {
		srv := fakeServer(t, map[string]http.HandlerFunc{
			"POST /v1/messages": func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				reply(w, obj{"type": "error", "error": obj{"type": "not_found_error", "message": "model: probe"}})
			},
		})
		s, err := Probe(context.Background(), srv.URL)
		if err != nil || s.Flavor != FlavorAnthropic || len(s.Models) != 0 {
			t.Errorf("server = %+v, err = %v", s, err)
		}
	})

	t.Run("model list in Anthropic's shape", func(t *testing.T) {
		srv := fakeServer(t, map[string]http.HandlerFunc{
			"GET /v1/models": json200(obj{"has_more": false, "data": []obj{
				{"type": "model", "id": "claude-local", "display_name": "Local", "created_at": "2026-01-01T00:00:00Z"},
			}}),
		})
		s, err := Probe(context.Background(), srv.URL)
		if err != nil || s.Flavor != FlavorAnthropic || len(s.Models) != 1 {
			t.Errorf("server = %+v, err = %v", s, err)
		}
	})
}

func TestProbeOrder(t *testing.T) {
	// Ollama also serves /v1/models and /v1/messages; it must still be "ollama".
	both := fakeServer(t, map[string]http.HandlerFunc{
		"GET /api/version": json200(obj{"version": "0.12.3"}),
		"GET /api/tags":    json200(obj{"models": []obj{}}),
		"GET /v1/models":   json200(obj{"object": "list", "data": []obj{{"id": "x", "object": "model"}}}),
		"POST /v1/messages": func(w http.ResponseWriter, _ *http.Request) {
			reply(w, obj{"type": "message", "model": "x"})
		},
	})
	s, err := Probe(context.Background(), both.URL)
	if err != nil || s.Flavor != FlavorOllama {
		t.Errorf("flavor = %q, err = %v", s.Flavor, err)
	}
}

func TestProbeErrors(t *testing.T) {
	t.Run("unknown API", func(t *testing.T) {
		srv := fakeServer(t, nil)
		_, err := Probe(context.Background(), srv.URL)
		if !errors.Is(err, ErrUnknownServer) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("HTML page everywhere", func(t *testing.T) {
		srv := fakeServer(t, map[string]http.HandlerFunc{
			"/": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>hello</html>")) },
		})
		_, err := Probe(context.Background(), srv.URL)
		if !errors.Is(err, ErrUnknownServer) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("needs credentials", func(t *testing.T) {
		srv := fakeServer(t, map[string]http.HandlerFunc{
			"/": func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no key", http.StatusUnauthorized) },
		})
		_, err := Probe(context.Background(), srv.URL)
		if err == nil || !strings.Contains(err.Error(), "credentials") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("nothing listening", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close()
		_, err := Probe(context.Background(), url)
		if err == nil || !strings.Contains(err.Error(), "cannot reach") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		srv := fakeServer(t, nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := Probe(ctx, srv.URL); !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("bad URL", func(t *testing.T) {
		if _, err := Probe(context.Background(), "ftp://host"); err == nil {
			t.Error("want an error")
		}
	})
}

func TestProbeURLForms(t *testing.T) {
	srv, _ := newOllamaFake(t)
	bare := strings.TrimPrefix(srv.URL, "http://")
	for _, in := range []string{bare, srv.URL + "/", srv.URL + "/v1", "  " + srv.URL + "/v1/ "} {
		s, err := Probe(context.Background(), in)
		if err != nil {
			t.Errorf("Probe(%q): %v", in, err)
			continue
		}
		if s.URL != srv.URL {
			t.Errorf("Probe(%q).URL = %q, want %q", in, s.URL, srv.URL)
		}
	}
}

func TestParseURL(t *testing.T) {
	tests := []struct {
		in, want string
		wantErr  bool
	}{
		{"127.0.0.1:8000", "http://127.0.0.1:8000", false},
		{"http://localhost:1234/v1/", "http://localhost:1234", false},
		{"https://host.example.com/proxy/v1", "https://host.example.com/proxy", false},
		{"http://user:secret@host:80?x=1#frag", "http://host:80", false},
		{"", "", true},
		{"ftp://host", "", true},
		{"http://", "", true},
	}
	for _, tt := range tests {
		u, err := parseURL(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseURL(%q) err = %v", tt.in, err)
			continue
		}
		if err == nil && u.String() != tt.want {
			t.Errorf("parseURL(%q) = %q, want %q", tt.in, u, tt.want)
		}
	}
}

func TestIsRemote(t *testing.T) {
	defer func(f func(string) ([]net.IP, error)) { lookupIP = f }(lookupIP)
	lookupIP = func(h string) ([]net.IP, error) {
		switch h {
		case "my-mac":
			return []net.IP{net.ParseIP("10.0.0.7")}, nil
		case "studio.local":
			return []net.IP{net.ParseIP("192.168.1.5"), net.ParseIP("fe80::1")}, nil
		case "134744072": // the system resolver reads this as 8.8.8.8
			return []net.IP{net.ParseIP("8.8.8.8")}, nil
		case "evil.local":
			return []net.IP{net.ParseIP("192.168.1.5"), net.ParseIP("8.8.4.4")}, nil
		}
		return nil, fmt.Errorf("no such host")
	}
	tests := []struct {
		url    string
		remote bool
	}{
		{"http://127.0.0.1:8000", false},
		{"http://localhost:11434", false},
		{"http://[::1]:8080", false},
		{"http://0.0.0.0:8000", false},
		{"http://10.0.0.5:1234", false},
		{"http://172.16.0.9", false},
		{"http://172.31.255.1", false},
		{"http://192.168.1.20:8000", false},
		{"http://169.254.10.10", false},
		{"http://[fd00::1]:8000", false},
		{"http://studio.local:8000", false},
		{"http://studio.local.:8000", false},
		{"http://llm.localhost", false},
		{"http://my-mac:8000", false},
		{"http://134744072:8000", true},  // a bare name that resolves to the internet
		{"http://evil.local:8000", true}, // one public address is enough
		{"http://nowhere:8000", true},    // a name that doesn't resolve
		{"172.32.0.1:8000", true},
		{"http://8.8.8.8", true},
		{"http://100.64.0.1:8000", true}, // Tailscale-style CGNAT is not RFC 1918
		{"https://api.example.com", true},
		{"https://llm.mycompany.io:8443", true},
		{"http://[2001:db8::1]:8000", true},
		{"", true},
	}
	for _, tt := range tests {
		if got := IsRemote(tt.url); got != tt.remote {
			t.Errorf("IsRemote(%q) = %v, want %v", tt.url, got, tt.remote)
		}
	}
}

// TestProbeRemoteFlag sends a request meant for a public host to a local fake.
func TestProbeRemoteFlag(t *testing.T) {
	srv := fakeServer(t, map[string]http.HandlerFunc{
		"GET /v1/models": json200(obj{"object": "list", "data": []obj{{"id": "m-4bit", "object": "model"}}}),
	})
	addr := strings.TrimPrefix(srv.URL, "http://")
	old := client
	t.Cleanup(func() { client = old })
	client = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}

	remote, err := Probe(context.Background(), "http://llm.example.com:8000")
	if err != nil {
		t.Fatal(err)
	}
	if !remote.Remote || remote.URL != "http://llm.example.com:8000" {
		t.Errorf("remote server = %+v", remote)
	}
	local, err := Probe(context.Background(), "http://192.168.1.50:8000")
	if err != nil {
		t.Fatal(err)
	}
	if local.Remote {
		t.Error("a LAN address must not be flagged remote")
	}
}
