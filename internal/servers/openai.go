package servers

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"strings"
)

// Generic OpenAI-compatible servers (MTPLX, Splish, llama.cpp, vLLM...) all
// answer GET /v1/models. MTPLX and Splish document no model-info, context or
// quantization endpoint, so for them the quant comes from the model id and the
// context is unknown. Where a server adds fields to the list, they are used:
// vLLM's max_model_len, llama.cpp's meta.n_ctx_train, and llama.cpp's /props
// (the context it was started with).

// contextKeys are the model-list fields that carry a served context length.
var contextKeys = []string{"max_model_len", "context_length", "max_context_length", "context_window", "n_ctx"}

func probeOpenAI(ctx context.Context, s *Server) (bool, error) {
	var list struct {
		Data []map[string]any `json:"data"`
	}
	if err := getJSON(ctx, s.URL+"/v1/models", &list); err != nil || list.Data == nil {
		return false, err
	}
	s.Flavor = FlavorOpenAI
	if anthropicList(list.Data) {
		s.Flavor = FlavorAnthropic
	}
	for _, e := range list.Data {
		id, _ := e["id"].(string)
		if id != "" {
			s.Models = append(s.Models, openAIModel(id, e))
		}
	}
	if s.Flavor == FlavorOpenAI {
		addServerInfo(ctx, s)
	}
	return true, nil
}

// anthropicList reports whether a /v1/models list has Anthropic's entry shape
// ("type":"model", no "object" field) instead of OpenAI's ("object":"model").
func anthropicList(entries []map[string]any) bool {
	if len(entries) == 0 {
		return false
	}
	for _, e := range entries {
		if e["type"] != "model" || e["object"] != nil {
			return false
		}
	}
	return true
}

// openAIModel reads a model-list entry. Quant comes from the id.
func openAIModel(id string, e map[string]any) Model {
	m := Model{ID: id, Quant: ParseQuantFromName(id)}
	for _, k := range contextKeys {
		if n := intOf(e[k]); n > 0 {
			m.Context, m.ContextSource = n, ContextReported
			break
		}
	}
	if meta, ok := e["meta"].(map[string]any); ok {
		m.MaxContext = intOf(meta["n_ctx_train"])
	}
	return m
}

// addServerInfo asks for the extras some servers offer. All of it is best
// effort. llama.cpp's /props has the context it was started with, the build,
// and the model file (whose name often carries the quant); vLLM's /version has
// the version.
func addServerInfo(ctx context.Context, s *Server) {
	var props struct {
		BuildInfo                 string `json:"build_info"`
		ModelPath                 string `json:"model_path"`
		DefaultGenerationSettings struct {
			NCtx int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}
	if getJSON(ctx, s.URL+"/props", &props) == nil {
		s.Version = props.BuildInfo
		if len(s.Models) == 1 { // /props describes the one model it serves
			applyProps(&s.Models[0], props.DefaultGenerationSettings.NCtx, props.ModelPath)
		}
	}
	if s.Version == "" {
		var v struct {
			Version string `json:"version"`
		}
		if getJSON(ctx, s.URL+"/version", &v) == nil {
			s.Version = v.Version
		}
	}
}

// applyProps fills what the model list left unknown.
func applyProps(m *Model, nCtx int, modelPath string) {
	if m.Context == 0 && nCtx > 0 {
		m.Context, m.ContextSource = nCtx, ContextReported
	}
	if !m.Quant.Known() && modelPath != "" {
		if q := ParseQuantFromName(path.Base(strings.ReplaceAll(modelPath, `\`, "/"))); q.Known() {
			m.Quant = q
		}
	}
}

// Anthropic-compatible servers: POST /v1/messages with a tiny request. A
// reply in Anthropic's shape, or an Anthropic-shaped error (unknown model, bad
// key), proves the API is there.

type anthropicReply struct {
	Type  string `json:"type"` // "message" or "error"
	Model string `json:"model"`
	Error *struct {
		Type string `json:"type"`
	} `json:"error"`
}

func probeAnthropic(ctx context.Context, s *Server) (bool, error) {
	body := map[string]any{
		"model":      "probe",
		"max_tokens": 1,
		"messages":   []chatMessage{{Role: "user", Content: "hi"}},
	}
	var reply anthropicReply
	err := postProbe(ctx, s.URL+"/v1/messages", body, &reply, anthropicHeaders)
	var status *statusError
	if errors.As(err, &status) {
		if json.Unmarshal(status.Body, &reply) != nil || reply.Type != "error" || reply.Error == nil || reply.Error.Type == "" {
			return false, err
		}
		s.Flavor = FlavorAnthropic
		return true, nil
	}
	if err != nil || reply.Type != "message" {
		return false, err
	}
	s.Flavor = FlavorAnthropic
	if reply.Model != "" {
		s.Models = []Model{{ID: reply.Model, Quant: ParseQuantFromName(reply.Model)}}
	}
	return true, nil
}
