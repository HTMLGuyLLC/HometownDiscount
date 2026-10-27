// Package llm is a small client for OpenAI-compatible chat servers (MTPLX,
// Splish, Ollama, LM Studio, llama.cpp, vLLM...), with tool calling.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Message is one chat message.
type Message struct {
	Role       string     `json:"role"` // system, user, assistant, tool
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`

	// FinishReason is the server's reason the reply ended ("stop", "length"
	// when it hit the token limit...). It's never sent back to the server.
	FinishReason string `json:"-"`
}

// ToolCall is a model's request to run a tool.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON text
	} `json:"function"`
}

// Tool describes a tool the model may call.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema
}

// Usage is the token count of one call.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// Client talks to one server and model.
type Client struct {
	BaseURL   string // e.g. http://127.0.0.1:8000
	Model     string
	MaxTokens int
	HTTP      *http.Client
}

// New returns a client with a 10-minute request timeout (local models can be slow).
func New(baseURL, model string) *Client {
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		Model:     model,
		MaxTokens: 8192,
		HTTP:      &http.Client{Timeout: 10 * time.Minute},
	}
}

type toolSpec struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type chatRequest struct {
	Model     string     `json:"model"`
	Messages  []Message  `json:"messages"`
	Tools     []toolSpec `json:"tools,omitempty"`
	MaxTokens int        `json:"max_tokens,omitempty"`
	Stream    bool       `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Chat sends the conversation and returns the model's reply.
func (c *Client) Chat(ctx context.Context, msgs []Message, tools []Tool) (Message, Usage, error) {
	req := chatRequest{Model: c.Model, Messages: msgs, MaxTokens: c.MaxTokens}
	for _, t := range tools {
		var s toolSpec
		s.Type = "function"
		s.Function.Name, s.Function.Description, s.Function.Parameters = t.Name, t.Description, t.Parameters
		req.Tools = append(req.Tools, s)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return Message{}, Usage{}, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Message{}, Usage{}, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(hreq)
	if err != nil {
		return Message{}, Usage{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return Message{}, Usage{}, err
	}
	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return Message{}, Usage{}, &ServerError{Status: resp.StatusCode, Message: fmt.Sprintf("%.200s", raw)}
	}
	if out.Error != nil {
		return Message{}, Usage{}, &ServerError{Status: resp.StatusCode, Message: out.Error.Message}
	}
	if resp.StatusCode != http.StatusOK || len(out.Choices) == 0 {
		return Message{}, Usage{}, &ServerError{Status: resp.StatusCode, Message: "no reply"}
	}
	msg := out.Choices[0].Message
	msg.Role = "assistant"
	msg.FinishReason = out.Choices[0].FinishReason
	return msg, out.Usage, nil
}

// ServerError is an error the model server reported.
type ServerError struct {
	Status  int
	Message string
}

func (e *ServerError) Error() string { return "server error: " + e.Message }

var overflow = regexp.MustCompile(`(?i)context|too long|too many tokens|maximum.*tokens|exceeds|n_ctx`)

// IsContextOverflow reports whether the server refused the request because
// the input didn't fit the model's context: a real limit of this setup,
// unlike a crash or a busy server.
func IsContextOverflow(err error) bool {
	var se *ServerError
	return errors.As(err, &se) && se.Status >= 400 && se.Status < 600 && overflow.MatchString(se.Message)
}

var thinkBlock = regexp.MustCompile(`(?s)<think>.*?</think>`)

// StripThinking removes reasoning that thinking models put in the reply text
// (<think>...</think>), including a reply whose opening tag was cut off.
func StripThinking(s string) string {
	s = thinkBlock.ReplaceAllString(s, "")
	if i := strings.Index(s, "</think>"); i >= 0 {
		s = s[i+len("</think>"):]
	}
	return strings.TrimSpace(s)
}

// Chatter is anything that can run a chat turn: a Client, or a Client
// behind the shared model lock.
type Chatter interface {
	Chat(ctx context.Context, msgs []Message, tools []Tool) (Message, Usage, error)
}
