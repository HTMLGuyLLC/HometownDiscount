package servers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	speedTokens    = 128 // tokens a speed run asks for
	minSpeedTokens = 16  // fewer than this is too short to time
	toolName       = "get_weather"
	toolMaxTokens  = 1024 // room for a reasoning model to think before it calls
)

const speedPrompt = "Count from 1 to 200 separated by spaces. Output only the numbers."

var toolCities = []string{"Paris", "Tokyo", "Lima", "Cairo", "Oslo", "Sydney", "Denver", "Mumbai"}

// toolSchema is the one tool defined for a tool-call test.
var toolSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"city": map[string]any{"type": "string", "description": "City name"},
	},
	"required": []string{"city"},
}

// now is the clock for speed runs. Tests replace it.
var now = time.Now

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// MeasureSpeed runs one short generation and returns decode tokens per second.
//
// Ollama reports its own decode time (eval_count / eval_duration). Everywhere
// else it is completion tokens over wall time, after a one-token warm-up so a
// model load doesn't count against the speed. Cloud models are refused.
func MeasureSpeed(ctx context.Context, s *Server, model string) (tokensPerSec float64, err error) {
	if err := checkModel(s, model); err != nil {
		return 0, err
	}
	switch s.Flavor {
	case FlavorOllama:
		return ollamaSpeed(ctx, s, model)
	case FlavorAnthropic:
		return timedSpeed(ctx, func(ctx context.Context, n int) (int, error) {
			r, err := anthropicChat(ctx, s, model, speedPrompt, n, false)
			return r.Usage.OutputTokens, err
		})
	}
	return timedSpeed(ctx, func(ctx context.Context, n int) (int, error) {
		r, err := openAIChat(ctx, s, model, speedPrompt, n, false)
		return r.Usage.CompletionTokens, err
	})
}

// ollamaSpeed uses /api/chat, whose reply times the decode exactly. It sets no
// num_ctx, so a loaded model isn't reloaded with a different context.
func ollamaSpeed(ctx context.Context, s *Server, model string) (float64, error) {
	body := map[string]any{
		"model":    model,
		"stream":   false,
		"messages": []chatMessage{{Role: "user", Content: speedPrompt}},
		"options":  map[string]any{"num_predict": speedTokens},
	}
	var r struct {
		EvalCount    int   `json:"eval_count"`
		EvalDuration int64 `json:"eval_duration"` // nanoseconds
	}
	if err := postJSON(ctx, s.URL+"/api/chat", body, &r); err != nil {
		return 0, err
	}
	return rate(r.EvalCount, time.Duration(r.EvalDuration))
}

// timedSpeed warms the model up, then times one generation by the wall clock.
func timedSpeed(ctx context.Context, generate func(ctx context.Context, maxTokens int) (int, error)) (float64, error) {
	if _, err := generate(ctx, 1); err != nil {
		return 0, fmt.Errorf("warm-up: %w", err)
	}
	start := now()
	n, err := generate(ctx, speedTokens)
	elapsed := now().Sub(start)
	if err != nil {
		return 0, err
	}
	return rate(n, elapsed)
}

func rate(tokens int, d time.Duration) (float64, error) {
	switch {
	case tokens <= 0:
		return 0, errors.New("the server reported no generated tokens")
	case tokens < minSpeedTokens:
		return 0, fmt.Errorf("reply too short to time: %d tokens", tokens)
	case d <= 0:
		return 0, errors.New("the server reported no decode time")
	}
	return float64(tokens) / d.Seconds(), nil
}

// ToolCallTest makes n small requests with one tool defined and counts the
// replies whose tool calls are well formed: every call names the defined tool,
// and its arguments are a JSON object with a non-empty string "city" (the
// tool's one required argument). A reply with no tool call, a wrong tool name,
// cut-off JSON or missing arguments is not counted. The first HTTP or
// transport failure stops the test and is returned with the count so far.
// Cloud models are refused.
func ToolCallTest(ctx context.Context, s *Server, model string, n int) (valid int, err error) {
	if n < 1 {
		return 0, errors.New("n must be at least 1")
	}
	if err := checkModel(s, model); err != nil {
		return 0, err
	}
	call := openAIToolCall
	if s.Flavor == FlavorAnthropic {
		call = anthropicToolCall
	}
	for i := 0; i < n; i++ {
		prompt := fmt.Sprintf("What is the weather in %s right now? Use the tool.", toolCities[i%len(toolCities)])
		ok, err := call(ctx, s, model, prompt)
		if err != nil {
			return valid, err
		}
		if ok {
			valid++
		}
	}
	return valid, nil
}

// validArgs reports whether raw is a JSON object with a non-empty string "city".
func validArgs(raw []byte) bool {
	var a struct {
		City string `json:"city"`
	}
	return json.Unmarshal(raw, &a) == nil && strings.TrimSpace(a.City) != ""
}

// OpenAI-compatible chat: POST /v1/chat/completions. Ollama and LM Studio
// speak it too.

type oaRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
	Stream    bool          `json:"stream"`
	Tools     []oaTool      `json:"tools,omitempty"`
}

type oaTool struct {
	Type     string     `json:"type"`
	Function oaFunction `json:"function"`
}

type oaFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type oaReply struct {
	Choices []struct {
		Message struct {
			ToolCalls []struct {
				Function struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func openAIChat(ctx context.Context, s *Server, model, prompt string, maxTokens int, withTool bool) (oaReply, error) {
	req := oaRequest{Model: model, Messages: []chatMessage{{Role: "user", Content: prompt}}, MaxTokens: maxTokens}
	if withTool {
		req.Tools = []oaTool{{Type: "function", Function: oaFunction{
			Name: toolName, Description: "Get the current weather for a city.", Parameters: toolSchema,
		}}}
	}
	var r oaReply
	err := postJSON(ctx, s.URL+"/v1/chat/completions", req, &r)
	return r, err
}

// openAIToolCall asks for one tool call. Arguments must arrive as a JSON
// string holding an object, as the OpenAI format says.
func openAIToolCall(ctx context.Context, s *Server, model, prompt string) (bool, error) {
	r, err := openAIChat(ctx, s, model, prompt, toolMaxTokens, true)
	if err != nil || len(r.Choices) == 0 {
		return false, err
	}
	calls := r.Choices[0].Message.ToolCalls
	if len(calls) == 0 {
		return false, nil
	}
	for _, c := range calls {
		var args string
		if c.Function.Name != toolName || json.Unmarshal(c.Function.Arguments, &args) != nil || !validArgs([]byte(args)) {
			return false, nil
		}
	}
	return true, nil
}

// Anthropic-compatible chat: POST /v1/messages.

type anRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	Messages  []chatMessage `json:"messages"`
	Tools     []anTool      `json:"tools,omitempty"`
}

type anTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type anReply struct {
	Content []struct {
		Type  string          `json:"type"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func anthropicChat(ctx context.Context, s *Server, model, prompt string, maxTokens int, withTool bool) (anReply, error) {
	req := anRequest{Model: model, MaxTokens: maxTokens, Messages: []chatMessage{{Role: "user", Content: prompt}}}
	if withTool {
		req.Tools = []anTool{{Name: toolName, Description: "Get the current weather for a city.", InputSchema: toolSchema}}
	}
	var r anReply
	err := postJSON(ctx, s.URL+"/v1/messages", req, &r, anthropicHeaders)
	return r, err
}

// anthropicToolCall asks for one tool call; every tool_use block must be valid.
func anthropicToolCall(ctx context.Context, s *Server, model, prompt string) (bool, error) {
	r, err := anthropicChat(ctx, s, model, prompt, toolMaxTokens, true)
	if err != nil {
		return false, err
	}
	found := false
	for _, b := range r.Content {
		if b.Type != "tool_use" {
			continue
		}
		if b.Name != toolName || !validArgs(b.Input) {
			return false, nil
		}
		found = true
	}
	return found, nil
}
