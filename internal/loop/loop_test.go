package loop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/llm"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/sandbox"
)

// scripted is a fake model that makes the given tool calls, then answers
// with whatever the last tool returned.
type scripted struct {
	calls []llm.ToolCall
	seen  []llm.Message
}

func call(name, args string) llm.ToolCall {
	var c llm.ToolCall
	c.ID, c.Type = name+"-1", "function"
	c.Function.Name, c.Function.Arguments = name, args
	return c
}

func (s *scripted) Chat(_ context.Context, msgs []llm.Message, _ []llm.Tool) (llm.Message, llm.Usage, error) {
	s.seen = msgs
	usage := llm.Usage{PromptTokens: 10, CompletionTokens: 2}
	if len(s.calls) > 0 {
		c := s.calls[0]
		s.calls = s.calls[1:]
		return llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{c}}, usage, nil
	}
	return llm.Message{Role: "assistant", Content: "answer: " + msgs[len(msgs)-1].Content}, usage, nil
}

func TestRunReadsWithOriginalPaths(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.go"), []byte("package config\nvar Debug = true\n"), 0o644)
	m := &scripted{calls: []llm.ToolCall{call("read_file", `{"path":"/Users/me/app/config.go","offset":2}`)}}

	res, err := Run(context.Background(), m, Job{Task: "audit", Dir: dir, RepoRoot: "/Users/me/app"}, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if res.Answer != "answer: 2\tvar Debug = true" || res.Turns != 2 || res.ToolCalls != 1 || res.Usage.PromptTokens != 20 {
		t.Errorf("result = %+v", res)
	}
}

func TestReadFileCannotEscape(t *testing.T) {
	dir := t.TempDir()
	j := Job{Dir: dir, RepoRoot: "/Users/me/app"}
	if out := j.readFile("../../etc/passwd", 0, 0); !strings.HasPrefix(out, "error:") {
		t.Errorf("escaped the workspace: %q", out)
	}
}

func TestBashMapsPathsBothWays(t *testing.T) {
	if sandbox.Available() != nil {
		t.Skip("no sandbox")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o644)
	m := &scripted{calls: []llm.ToolCall{call("bash", `{"command":"ls /Users/me/app && pwd"}`)}}
	res, err := Run(context.Background(), m, Job{Task: "t", Dir: dir, RepoRoot: "/Users/me/app"}, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Answer, "a.go") || strings.Contains(res.Answer, dir) {
		t.Errorf("answer = %q", res.Answer)
	}
}

func TestBadArgsAndLimits(t *testing.T) {
	m := &scripted{calls: []llm.ToolCall{call("read_file", `{not json`)}}
	res, _ := Run(context.Background(), m, Job{Task: "t", Dir: t.TempDir()}, DefaultLimits)
	if !strings.Contains(res.Answer, "aren't valid JSON") {
		t.Errorf("answer = %q", res.Answer)
	}

	loops := &scripted{}
	for i := 0; i < 5; i++ {
		loops.calls = append(loops.calls, call("read_file", `{"path":"x"}`))
	}
	lim := DefaultLimits
	lim.MaxTurns = 3
	res, _ = Run(context.Background(), loops, Job{Task: "t", Dir: t.TempDir()}, lim)
	if res.Stopped != "too many turns" {
		t.Errorf("stopped = %q", res.Stopped)
	}
}

type fakeWeb struct{}

func (fakeWeb) Fetch(url, _ string) (string, bool) {
	return "page for " + url, url == "https://a.example"
}
func (fakeWeb) Search(q string) (string, bool) { return "", false }

func TestWebTools(t *testing.T) {
	m := &scripted{calls: []llm.ToolCall{call("web_fetch", `{"url":"https://a.example"}`)}}
	res, _ := Run(context.Background(), m, Job{Task: "t", Dir: t.TempDir(), Web: fakeWeb{}}, DefaultLimits)
	if res.Answer != "answer: page for https://a.example" {
		t.Errorf("answer = %q", res.Answer)
	}

	m = &scripted{calls: []llm.ToolCall{call("web_fetch", `{"url":"https://other.example"}`)}}
	res, _ = Run(context.Background(), m, Job{Task: "t", Dir: t.TempDir(), Web: fakeWeb{}}, DefaultLimits)
	if !strings.Contains(res.Answer, "isn't available offline") {
		t.Errorf("answer = %q", res.Answer)
	}
}

// cutOff is a fake model whose first replies run out of room mid-thought.
type cutOff struct{ left int }

func (c *cutOff) Chat(_ context.Context, msgs []llm.Message, _ []llm.Tool) (llm.Message, llm.Usage, error) {
	if c.left > 0 {
		c.left--
		return llm.Message{Role: "assistant", FinishReason: "length"}, llm.Usage{}, nil
	}
	return llm.Message{Role: "assistant", Content: "done", FinishReason: "stop"}, llm.Usage{}, nil
}

// A reply cut off by the output limit isn't an answer: the loop asks for a
// short next step, and stops as "cut off" only when that keeps happening.
func TestCutOffReplyIsNotAnAnswer(t *testing.T) {
	dir := t.TempDir()
	res, err := Run(context.Background(), &cutOff{left: 2}, Job{Task: "find x", Dir: dir, RepoRoot: dir}, DefaultLimits)
	if err != nil || res.Answer != "done" || res.Stopped != "" || res.Turns != 3 {
		t.Errorf("two cut-offs then an answer: %+v, %v", res, err)
	}
	res, err = Run(context.Background(), &cutOff{left: 9}, Job{Task: "find x", Dir: dir, RepoRoot: dir}, DefaultLimits)
	if err != nil || res.Stopped != "cut off" || res.Turns != 3 {
		t.Errorf("cut off every time: %+v, %v", res, err)
	}
}
