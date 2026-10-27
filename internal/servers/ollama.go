package servers

import (
	"context"
	"strconv"
	"strings"
)

// Ollama API (docs.ollama.com): GET /api/version, GET /api/tags,
// POST /api/show, GET /api/ps.

type ollamaTag struct {
	Name    string `json:"name"`
	Model   string `json:"model"`
	Details struct {
		Format            string `json:"format"`
		QuantizationLevel string `json:"quantization_level"`
	} `json:"details"`
	RemoteModel string `json:"remote_model"`
	RemoteHost  string `json:"remote_host"`
}

func (t ollamaTag) id() string { return firstNonEmpty(t.Name, t.Model) }

type ollamaShow struct {
	Parameters string `json:"parameters"`
	Details    struct {
		Format            string `json:"format"`
		QuantizationLevel string `json:"quantization_level"`
	} `json:"details"`
	ModelInfo    map[string]any `json:"model_info"`
	Capabilities []string       `json:"capabilities"`
}

func probeOllama(ctx context.Context, s *Server) (bool, error) {
	var v struct {
		Version string `json:"version"`
	}
	if err := getJSON(ctx, s.URL+"/api/version", &v); err != nil || v.Version == "" {
		return false, err
	}
	var tags struct {
		Models []ollamaTag `json:"models"`
	}
	if err := getJSON(ctx, s.URL+"/api/tags", &tags); err != nil {
		return false, err
	}
	s.Flavor, s.Version = FlavorOllama, v.Version
	s.Models = ollamaModels(ctx, s.URL, tags.Models)
	return true, nil
}

// ollamaModels details each local model. Cloud models are flagged and never
// asked about (/api/show would call out to the cloud). Show and ps are best
// effort: a model whose details fail to load keeps what /api/tags gave.
func ollamaModels(ctx context.Context, base string, tags []ollamaTag) []Model {
	loaded := ollamaLoaded(ctx, base)
	var out []Model
	for _, t := range tags {
		id := t.id()
		if id == "" || ctx.Err() != nil {
			continue
		}
		if isCloudName(id) || t.RemoteHost != "" || t.RemoteModel != "" {
			out = append(out, Model{ID: id, Cloud: true, Quant: withNameQuant(reportedQuant(t.Details.Format, t.Details.QuantizationLevel), id)})
			continue
		}
		show, ok := ollamaShowModel(ctx, base, id)
		if ok && len(show.Capabilities) > 0 && !contains(show.Capabilities, "completion") {
			continue // embedding-only
		}
		m := Model{ID: id}
		format := firstNonEmpty(show.Details.Format, t.Details.Format)
		level := firstNonEmpty(show.Details.QuantizationLevel, t.Details.QuantizationLevel)
		m.Quant = withNameQuant(reportedQuant(format, level), id)
		m.MaxContext = ollamaMaxContext(show.ModelInfo)
		m.Context, m.ContextSource = ollamaContext(loaded[id], show.Parameters, ok)
		out = append(out, m)
	}
	return out
}

// ollamaContext is the context Ollama will really use. A loaded model shows
// its actual value in /api/ps; otherwise a num_ctx in the model's parameters;
// otherwise the default, which is small. If /api/show failed and the model
// isn't loaded, it is unknown.
func ollamaContext(loaded int, parameters string, showed bool) (int, string) {
	switch {
	case loaded > 0:
		return loaded, ContextLoaded
	case !showed:
		return 0, ""
	}
	if n := numCtxParam(parameters); n > 0 {
		return n, ContextConfigured
	}
	return OllamaDefaultContext, ContextDefault
}

// ollamaShowModel returns /api/show for one model.
func ollamaShowModel(ctx context.Context, base, id string) (ollamaShow, bool) {
	var show ollamaShow
	err := postProbe(ctx, base+"/api/show", map[string]any{"model": id}, &show)
	return show, err == nil
}

// ollamaLoaded maps loaded model names to the context they were loaded with.
func ollamaLoaded(ctx context.Context, base string) map[string]int {
	var ps struct {
		Models []struct {
			Name          string `json:"name"`
			Model         string `json:"model"`
			ContextLength int    `json:"context_length"`
		} `json:"models"`
	}
	loaded := map[string]int{}
	if err := getJSON(ctx, base+"/api/ps", &ps); err != nil {
		return loaded
	}
	for _, m := range ps.Models {
		for _, id := range []string{m.Name, m.Model} {
			if id != "" {
				loaded[id] = m.ContextLength
			}
		}
	}
	return loaded
}

// numCtxParam reads "num_ctx 8192" out of a Modelfile parameters string.
func numCtxParam(parameters string) int {
	for _, line := range strings.Split(parameters, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "num_ctx" {
			n, _ := strconv.Atoi(f[1])
			return n
		}
	}
	return 0
}

// ollamaMaxContext reads the model's own limit from model_info:
// "<architecture>.context_length".
func ollamaMaxContext(info map[string]any) int {
	if arch, _ := info["general.architecture"].(string); arch != "" {
		if n := intOf(info[arch+".context_length"]); n > 0 {
			return n
		}
	}
	best := 0
	for k, v := range info {
		if strings.HasSuffix(k, ".context_length") {
			best = max(best, intOf(v))
		}
	}
	return best
}

// isCloudName reports whether an Ollama-style name is a remote cloud model.
func isCloudName(id string) bool {
	id = strings.ToLower(id)
	return strings.HasSuffix(id, ":cloud") || strings.HasSuffix(id, "-cloud")
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// intOf reads a JSON number that was decoded into an any.
func intOf(v any) int {
	f, _ := v.(float64)
	return int(f)
}
