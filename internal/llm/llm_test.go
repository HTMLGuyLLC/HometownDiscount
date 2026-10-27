package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

// thinkingServer plays a thinking model: it thinks unless the request turns
// thinking off in the one spelling it understands, and refuses with a 400 the
// spellings listed in refuse.
func thinkingServer(t *testing.T, understands string, refuse ...string) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		json.NewDecoder(r.Body).Decode(&req)
		spelling := ""
		switch {
		case req.ReasoningEffort == "none":
			spelling = "reasoning_effort"
		case req.TemplateArgs["enable_thinking"] == false:
			spelling = "chat_template_kwargs"
		}
		seen = append(seen, spelling)
		for _, s := range refuse {
			if s == spelling {
				w.WriteHeader(400)
				w.Write([]byte(`{"error":{"message":"unknown field ` + s + `"}}`))
				return
			}
		}
		if spelling != "" && spelling == understands {
			w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"42"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1}}`))
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"42","reasoning_content":"17 plus 25..."},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":90,"completion_tokens_details":{"reasoning_tokens":88}}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// A server's way of turning thinking off is found once and then used first.
func TestNoThinking(t *testing.T) {
	ask := func(c *Client) (Message, Usage, error) {
		return c.Chat(context.Background(), []Message{{Role: "user", Content: "17+25?"}}, nil)
	}
	t.Run("first spelling", func(t *testing.T) {
		srv, seen := thinkingServer(t, "reasoning_effort")
		c := New(srv.URL, "m")
		c.NoThinking = true
		for range 2 {
			if msg, u, err := ask(c); err != nil || Thought(msg, u) {
				t.Fatalf("want an answer without thinking: %+v %+v %v", msg, u, err)
			}
		}
		if len(*seen) != 2 {
			t.Errorf("one request per question, got %v", *seen)
		}
	})
	t.Run("second spelling is remembered", func(t *testing.T) {
		srv, seen := thinkingServer(t, "chat_template_kwargs")
		c := New(srv.URL, "m")
		c.NoThinking = true
		for range 2 {
			if msg, u, err := ask(c); err != nil || Thought(msg, u) {
				t.Fatalf("want an answer without thinking: %+v %+v %v", msg, u, err)
			}
		}
		if want := []string{"reasoning_effort", "chat_template_kwargs", "chat_template_kwargs"}; strings.Join(*seen, ",") != strings.Join(want, ",") {
			t.Errorf("requests = %v, want %v", *seen, want)
		}
	})
	t.Run("refused spelling", func(t *testing.T) {
		srv, _ := thinkingServer(t, "chat_template_kwargs", "reasoning_effort")
		c := New(srv.URL, "m")
		c.NoThinking = true
		if _, _, err := ask(c); err != nil {
			t.Fatalf("a refused spelling should move on to the next: %v", err)
		}
	})
	t.Run("ignored", func(t *testing.T) {
		srv, seen := thinkingServer(t, "")
		c := New(srv.URL, "m")
		c.NoThinking = true
		for range 2 {
			if _, _, err := ask(c); !errors.Is(err, ErrThinking) {
				t.Fatalf("err = %v, want ErrThinking", err)
			}
		}
		if len(*seen) != len(offStyles) {
			t.Errorf("a server that ignores every spelling is only tried once: %v", *seen)
		}
	})
	t.Run("every spelling refused", func(t *testing.T) {
		srv, _ := thinkingServer(t, "", "reasoning_effort", "chat_template_kwargs")
		c := New(srv.URL, "m")
		c.NoThinking = true
		if _, _, err := ask(c); err == nil || errors.Is(err, ErrThinking) || !strings.Contains(err.Error(), "unknown field") {
			t.Errorf("want the server's own error, got %v", err)
		}
	})
	t.Run("left alone", func(t *testing.T) {
		srv, seen := thinkingServer(t, "reasoning_effort")
		if msg, u, err := ask(New(srv.URL, "m")); err != nil || !Thought(msg, u) || u.ReasoningTokens != 88 || msg.Reasoning == "" {
			t.Errorf("a plain client sends nothing extra: %+v %+v %v", msg, u, err)
		}
		if (*seen)[0] != "" {
			t.Errorf("sent %q", (*seen)[0])
		}
	})
}

func TestThinks(t *testing.T) {
	srv, _ := thinkingServer(t, "reasoning_effort")
	if yes, err := New(srv.URL, "m").Thinks(context.Background()); err != nil || !yes {
		t.Errorf("Thinks = %v, %v", yes, err)
	}
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"<think>\n\n</think>\n\n42"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":3}}`))
	}))
	defer plain.Close()
	if yes, err := New(plain.URL, "m").Thinks(context.Background()); err != nil || yes {
		t.Errorf("an empty think block isn't thinking: %v, %v", yes, err)
	}
}

func TestThought(t *testing.T) {
	cases := map[string]bool{
		"42":                          false,
		"<think>\n\n</think>\n\n42":   false,
		"<think></think>42":           false,
		"<think>17 plus 25</think>42": true,
		"17 plus 25</think>42":        true,
		"<think>17 plus":              true,
	}
	for in, want := range cases {
		if got := Thought(Message{Content: in}, Usage{}); got != want {
			t.Errorf("Thought(%q) = %v, want %v", in, got, want)
		}
	}
}
