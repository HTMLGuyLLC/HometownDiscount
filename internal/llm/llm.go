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
	"sync/atomic"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/localhttp"
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
	// Reasoning is thinking the server returned apart from the reply text.
	// It's never sent back either.
	Reasoning string `json:"-"`
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
	ReasoningTokens  int `json:"-"` // the part of CompletionTokens spent thinking, when the server says
}

// Client talks to one server and model.
type Client struct {
	BaseURL   string // e.g. http://127.0.0.1:8000
	Model     string
	MaxTokens int
	HTTP      *http.Client
	// NoThinking asks the server to answer without thinking first. Servers
	// spell that differently; the first spelling that works is remembered.
	// A server that ignores every one fails with ErrThinking.
	NoThinking bool
	off        atomic.Int32 // the spelling in offStyles to try first
}

// ErrThinking means the server kept thinking although asked not to.
var ErrThinking = errors.New("the server doesn't let this model answer without thinking")

// offStyles are the ways servers are asked not to think: reasoning_effort
// (LM Studio, Ollama and other OpenAI-style servers), then the chat-template
// switch (vLLM, SGLang, llama.cpp and MLX servers).
var offStyles = []func(*chatRequest){
	func(r *chatRequest) { r.ReasoningEffort = "none" },
	func(r *chatRequest) { r.TemplateArgs = map[string]any{"enable_thinking": false} },
}

// New returns a client with a 10-minute request timeout (local models can be slow).
func New(baseURL, model string) *Client {
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		Model:     model,
		MaxTokens: 8192,
		HTTP:      localhttp.Client(10 * time.Minute),
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
	Model           string         `json:"model"`
	Messages        []Message      `json:"messages"`
	Tools           []toolSpec     `json:"tools,omitempty"`
	MaxTokens       int            `json:"max_tokens,omitempty"`
	Stream          bool           `json:"stream"`
	ReasoningEffort string         `json:"reasoning_effort,omitempty"`
	TemplateArgs    map[string]any `json:"chat_template_kwargs,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Message
			ReasoningContent string `json:"reasoning_content"` // vLLM, llama.cpp, LM Studio
			Reasoning        string `json:"reasoning"`         // Ollama
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		Usage
		Details struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
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
	if !c.NoThinking {
		return c.send(ctx, req)
	}
	ignored := false // a spelling was accepted, but the model thought anyway
	var refused error
	for i := int(c.off.Load()); i < len(offStyles); i++ {
		r := req
		offStyles[i](&r)
		msg, u, err := c.send(ctx, r)
		var se *ServerError
		switch {
		case err == nil && !Thought(msg, u):
			c.off.Store(int32(i))
			return msg, u, nil
		case err == nil:
			ignored = true
		case errors.As(err, &se) && se.Status >= 400 && se.Status < 500 && !IsContextOverflow(err):
			refused = err // this server doesn't know the spelling
		default:
			return msg, u, err // a real failure
		}
	}
	if !ignored && refused != nil {
		return Message{}, Usage{}, refused // maybe the request, not the spelling: don't remember
	}
	c.off.Store(int32(len(offStyles)))
	return Message{}, Usage{}, ErrThinking
}

// thinksPrompt is a question with nothing to think about.
const thinksPrompt = "Reply with only the number: what is 17 + 25?"

// Thinks reports whether the model thinks before it answers when the server
// is left to its defaults, from one short question.
func (c *Client) Thinks(ctx context.Context) (bool, error) {
	msg, u, err := c.send(ctx, chatRequest{Model: c.Model, Messages: []Message{{Role: "user", Content: thinksPrompt}}, MaxTokens: 1024})
	if err != nil {
		return false, err
	}
	return Thought(msg, u) || (msg.FinishReason == "length" && StripThinking(msg.Content) == ""), nil
}

// thoughtText finds thinking in reply text: a <think> block with something in
// it, or text before a closing tag whose opening one was cut off. An empty
// block, which some servers echo when thinking is off, doesn't count.
var thoughtText = regexp.MustCompile(`(?s)<think>\s*[^\s<]|[^\s>]\s*</think>`)

// Thought reports whether a reply shows the model thought before answering.
func Thought(m Message, u Usage) bool {
	return u.ReasoningTokens > 0 || strings.TrimSpace(m.Reasoning) != "" || thoughtText.MatchString(m.Content)
}

func (c *Client) send(ctx context.Context, req chatRequest) (Message, Usage, error) {
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
	ch := out.Choices[0]
	msg := ch.Message.Message
	msg.Role = "assistant"
	msg.FinishReason = ch.FinishReason
	msg.Reasoning = ch.Message.ReasoningContent + ch.Message.Reasoning
	u := out.Usage.Usage
	u.ReasoningTokens = out.Usage.Details.ReasoningTokens
	return msg, u, nil
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
