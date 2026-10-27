package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Model != "qwen" || len(req.Tools) != 1 || req.Tools[0].Function.Name != "bash" {
			http.Error(w, "bad request", 400)
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":12,"completion_tokens":3}}`))
	}))
	defer srv.Close()

	c := New(srv.URL+"/", "qwen")
	msg, usage, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, []Tool{{Name: "bash", Parameters: json.RawMessage(`{"type":"object"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Arguments != `{"command":"ls"}` || usage.PromptTokens != 12 {
		t.Errorf("msg = %+v usage = %+v", msg, usage)
	}
}

func TestChatErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"error":{"message":"model not loaded"}}`))
	}))
	defer srv.Close()
	if _, _, err := New(srv.URL, "x").Chat(context.Background(), nil, nil); err == nil || err.Error() != "server error: model not loaded" {
		t.Errorf("err = %v", err)
	}
}

func TestStripThinking(t *testing.T) {
	cases := map[string]string{
		"<think>hmm</think>\nanswer":        "answer",
		"plain":                             "plain",
		"cut off reasoning</think>answer":   "answer",
		"<think>a</think>x<think>b</think>": "x",
	}
	for in, want := range cases {
		if got := StripThinking(in); got != want {
			t.Errorf("StripThinking(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsContextOverflow(t *testing.T) {
	if !IsContextOverflow(&ServerError{Status: 400, Message: "This model's maximum context length is 8192 tokens"}) {
		t.Error("context overflow not recognized")
	}
	if IsContextOverflow(&ServerError{Status: 500, Message: "out of memory"}) || IsContextOverflow(errors.New("context length")) {
		t.Error("only server errors about the context count")
	}
}
