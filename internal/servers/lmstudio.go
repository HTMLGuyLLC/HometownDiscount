package servers

import "context"

// LM Studio REST API (lmstudio.ai/docs/developer/rest): GET /api/v0/models
// lists models with arch, quantization, compatibility_type, state,
// max_context_length and loaded_context_length. The newer GET /api/v1/models
// is tried when v0 is gone.

type lmV0Model struct {
	ID                  string `json:"id"`
	Type                string `json:"type"` // "llm", "vlm", "embeddings"
	CompatibilityType   string `json:"compatibility_type"`
	Quantization        string `json:"quantization"`
	State               string `json:"state"` // "loaded" or "not-loaded"
	MaxContextLength    int    `json:"max_context_length"`
	LoadedContextLength int    `json:"loaded_context_length"`
}

type lmV1Model struct {
	Key          string `json:"key"`
	Type         string `json:"type"` // "llm", "embedding"
	Format       string `json:"format"`
	Quantization struct {
		Name          string  `json:"name"`
		BitsPerWeight float64 `json:"bits_per_weight"`
	} `json:"quantization"`
	LoadedInstances []struct {
		Config struct {
			ContextLength int `json:"context_length"`
		} `json:"config"`
	} `json:"loaded_instances"`
	MaxContextLength int `json:"max_context_length"`
}

func probeLMStudio(ctx context.Context, s *Server) (bool, error) {
	var v0 struct {
		Data []lmV0Model `json:"data"`
	}
	err := getJSON(ctx, s.URL+"/api/v0/models", &v0)
	if err == nil && v0.Data != nil {
		s.Flavor, s.Models = FlavorLMStudio, lmV0Models(v0.Data)
		return true, nil
	}
	var v1 struct {
		Models []lmV1Model `json:"models"`
	}
	if err2 := getJSON(ctx, s.URL+"/api/v1/models", &v1); err2 != nil || v1.Models == nil {
		return false, firstErr(err, err2)
	}
	s.Flavor, s.Models = FlavorLMStudio, lmV1Models(v1.Models)
	return true, nil
}

// lmV0Models converts the v0 list. The context is only known for a loaded
// model: LM Studio picks it at load time, so an unloaded model's real context
// is unknown (MaxContext still says what the model could do).
func lmV0Models(list []lmV0Model) []Model {
	var out []Model
	for _, m := range list {
		if m.ID == "" || m.Type == "embeddings" {
			continue
		}
		model := Model{
			ID:         m.ID,
			MaxContext: m.MaxContextLength,
			Quant:      withNameQuant(reportedQuant(m.CompatibilityType, m.Quantization), m.ID),
		}
		if m.State == "loaded" && m.LoadedContextLength > 0 {
			model.Context, model.ContextSource = m.LoadedContextLength, ContextLoaded
		}
		out = append(out, model)
	}
	return out
}

// lmV1Models converts the v1 list; a loaded model has an instance with its context.
func lmV1Models(list []lmV1Model) []Model {
	var out []Model
	for _, m := range list {
		if m.Key == "" || m.Type == "embedding" {
			continue
		}
		q := reportedQuant(m.Format, m.Quantization.Name)
		if q.Source != "" && !q.Known() {
			q.Bits = m.Quantization.BitsPerWeight
		}
		model := Model{ID: m.Key, MaxContext: m.MaxContextLength, Quant: withNameQuant(q, m.Key)}
		for _, inst := range m.LoadedInstances {
			if n := inst.Config.ContextLength; n > 0 {
				model.Context, model.ContextSource = n, ContextLoaded
				break
			}
		}
		out = append(out, model)
	}
	return out
}

// firstErr returns the first non-nil error.
func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
