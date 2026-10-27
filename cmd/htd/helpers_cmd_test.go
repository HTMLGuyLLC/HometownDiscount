package main

// Shared fixtures for the command-layer tests (*_cmd_test.go): a hermetic
// environment, a fake OpenAI-compatible model server, a tiny git repo and
// Claude Code transcripts built inline. Nothing here reads the real ~/.htd,
// the real transcripts or a real model server.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/sandbox"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

// hermetic points htd at throwaway folders for its own files (HTD_HOME) and
// for Claude Code's transcripts (CLAUDE_CONFIG_DIR), so a test can't read or
// write the real ones. It returns both folders; the transcripts folder
// already has the empty projects/ directory Claude Code would have made.
func hermetic(t *testing.T) (home, claude string) {
	t.Helper()
	home, claude = t.TempDir(), t.TempDir()
	t.Setenv("HTD_HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	if err := os.MkdirAll(filepath.Join(claude, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home, claude
}

// needSandbox skips a test that replays jobs on a machine whose command
// sandbox can't run (replays refuse to start without it).
func needSandbox(t *testing.T) {
	t.Helper()
	if err := sandbox.Available(); err != nil {
		t.Skipf("replays need the command sandbox: %v", err)
	}
	if err := sandbox.Preflight(context.Background()); err != nil {
		t.Skipf("the command sandbox can't start here: %v", err)
	}
}

// chatRequest is the part of an OpenAI chat request the fake servers read.
type chatRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Tools []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
	MaxTokens int `json:"max_tokens"`
}

// task is the first user message: a replay's original task, or an ask_local
// call's file blocks and question.
func (r chatRequest) task() string {
	for _, m := range r.Messages {
		if m.Role == "user" {
			return m.Content
		}
	}
	return ""
}

// toolResults are the contents of the tool messages sent back to the model.
func (r chatRequest) toolResults() []string {
	var out []string
	for _, m := range r.Messages {
		if m.Role == "tool" {
			out = append(out, m.Content)
		}
	}
	return out
}

// fakeCall is a tool call the fake model makes.
type fakeCall struct{ Name, Args string }

// fakeReply is one answer from the fake model.
type fakeReply struct {
	Status  int    // HTTP status; 0 is 200
	Err     string // when set, an OpenAI-style {"error": {"message": ...}}
	Content string
	Finish  string // finish_reason; "" is "stop"
	Calls   []fakeCall
	Tokens  int // usage.completion_tokens
	// FailProbes makes the next n /v1/models requests fail, as if this reply
	// were the server's last before going down.
	FailProbes int
}

// fakeServer speaks the part of the OpenAI API htd uses (/v1/models and
// /v1/chat/completions) on a loopback port.
type fakeServer struct {
	*httptest.Server
	mu    sync.Mutex
	chats []chatRequest
	hits  int // every request, whatever the path
	// failProbes is how many /v1/models requests still get a 503, to play a
	// server that is down or restarting.
	failProbes int
}

// failNextProbes makes the next n model-list requests fail with a 503.
func (f *fakeServer) failNextProbes(n int) {
	f.mu.Lock()
	f.failProbes = n
	f.mu.Unlock()
}

// newFakeServer starts a server listing models. chat answers each chat
// request; n counts them from 1.
func newFakeServer(t *testing.T, models []map[string]any, chat func(n int, r chatRequest) fakeReply) *fakeServer {
	t.Helper()
	fs := &fakeServer{}
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.mu.Lock()
		fs.hits++
		fs.mu.Unlock()
		switch r.URL.Path {
		case "/v1/models":
			fs.mu.Lock()
			down := fs.failProbes > 0
			if down {
				fs.failProbes--
			}
			fs.mu.Unlock()
			if down {
				http.Error(w, "engine is recovering", http.StatusServiceUnavailable)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": models})
		case "/v1/chat/completions":
			var req chatRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			fs.mu.Lock()
			fs.chats = append(fs.chats, req)
			n := len(fs.chats)
			fs.mu.Unlock()
			rep := chat(n, req)
			if rep.FailProbes > 0 {
				fs.failNextProbes(rep.FailProbes)
			}
			status := rep.Status
			if status == 0 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			if rep.Err != "" {
				json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": rep.Err}})
				return
			}
			msg := map[string]any{"role": "assistant", "content": rep.Content}
			var calls []map[string]any
			for i, c := range rep.Calls {
				calls = append(calls, map[string]any{
					"id": fmt.Sprintf("call_%d", i), "type": "function",
					"function": map[string]any{"name": c.Name, "arguments": c.Args},
				})
			}
			if calls != nil {
				msg["tool_calls"] = calls
			}
			finish := rep.Finish
			if finish == "" {
				finish = "stop"
			}
			json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"finish_reason": finish, "message": msg}},
				"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": rep.Tokens},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fs.Close)
	return fs
}

// requestCount is how many requests of any kind the server has seen.
func (f *fakeServer) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits
}

// chatCount is how many chat requests the server has answered.
func (f *fakeServer) chatCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.chats)
}

// chat is the nth chat request (1-based).
func (f *fakeServer) chat(n int) chatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.chats[n-1]
}

// modelList is a /v1/models list of plain models.
func modelList(ids ...string) []map[string]any {
	var out []map[string]any
	for _, id := range ids {
		out = append(out, map[string]any{"id": id, "object": "model"})
	}
	return out
}

// healthyModel is a model that decodes fast and makes well-formed tool calls,
// as setup and probe test it: a get_weather call when given the tool,
// otherwise 128 tokens of counting.
func healthyModel(_ int, r chatRequest) fakeReply {
	if len(r.Tools) > 0 && r.Tools[0].Function.Name == "get_weather" {
		return fakeReply{Calls: []fakeCall{{Name: "get_weather", Args: `{"city":"Paris"}`}}, Tokens: 20}
	}
	return fakeReply{Content: "1 2 3 4 5 6 7 8", Tokens: 128}
}

// noToolCalls is a model that answers in prose even when given a tool.
func noToolCalls(_ int, r chatRequest) fakeReply {
	if len(r.Tools) > 0 {
		return fakeReply{Content: "It is sunny in Paris.", Tokens: 20}
	}
	return fakeReply{Content: "1 2 3 4 5 6 7 8", Tokens: 128}
}

// closedServerURL is the address of a server that was shut down, so
// connections to it are refused at once.
func closedServerURL() string {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	return url
}

// The repo every job fixture works in. Line 3 holds Charge and line 5 Refund.
const billingGo = "package billing\n\nfunc Charge() {}\n\nfunc Refund() {}\n"

// newRepo makes a git repo with billing.go committed three days ago, so it
// predates any job the fixtures run "recently". It returns the repo's real
// path (macOS temp folders are symlinks; git reports the real one).
func newRepo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "billing.go"), []byte(billingGo), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "."},
		{"-c", "user.name=htd test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+when, "GIT_COMMITTER_DATE="+when)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// job describes one subagent transcript. Zero fields get defaults: a Grep
// call, a 90-second run that started two hours ago on opus 5.5.
type job struct {
	ID          string
	Description string // what the parent said; it decides the job type ("Find ..." is a find job)
	Prompt      string
	Answer      string
	Cwd         string
	Start       time.Time
	Duration    time.Duration
	Tool        string // the one tool the job used
	Input       map[string]any
	Result      string // what that tool returned
	EmptyResult bool   // the tool returned nothing at all
	Model       string
}

// writeJob writes the job's transcript and meta file under the fake Claude
// Code projects folder and returns the transcript path.
func writeJob(t *testing.T, claude string, j job) string {
	t.Helper()
	if j.Start.IsZero() {
		j.Start = time.Now().Add(-2 * time.Hour)
	}
	if j.Duration == 0 {
		j.Duration = 90 * time.Second
	}
	if j.Tool == "" {
		j.Tool = "Grep"
	}
	if j.Model == "" {
		j.Model = "claude-opus-5-5"
	}
	if j.Input == nil {
		j.Input = map[string]any{"pattern": "func"}
	}
	if j.Result == "" && !j.EmptyResult {
		j.Result = "billing.go:3:func Charge() {}"
	}
	ts := func(d time.Duration) string { return j.Start.Add(d).UTC().Format(time.RFC3339Nano) }
	line := func(v map[string]any) string {
		v["agentId"], v["sessionId"], v["cwd"], v["gitBranch"] = j.ID, "sess1", j.Cwd, ""
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	usage := map[string]any{"input_tokens": 100, "output_tokens": 50}
	lines := []string{
		line(map[string]any{"type": "user", "timestamp": ts(0), "message": map[string]any{"role": "user", "content": j.Prompt}}),
		line(map[string]any{"type": "assistant", "timestamp": ts(j.Duration / 3), "message": map[string]any{
			"id": "m1-" + j.ID, "model": j.Model, "usage": usage,
			"content": []any{map[string]any{"type": "tool_use", "id": "t1", "name": j.Tool, "input": j.Input}},
		}}),
		line(map[string]any{"type": "user", "timestamp": ts(j.Duration / 2), "message": map[string]any{"role": "user",
			"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": j.Result}},
		}}),
		line(map[string]any{"type": "assistant", "timestamp": ts(j.Duration), "message": map[string]any{
			"id": "m2-" + j.ID, "model": j.Model, "usage": usage,
			"content": []any{map[string]any{"type": "text", "text": j.Answer}},
		}}),
	}
	dir := filepath.Join(claude, "projects", "-proj", "sess1", "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "agent-"+j.ID+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(map[string]any{"agentType": "Explore", "description": j.Description})
	if err := os.WriteFile(strings.TrimSuffix(path, ".jsonl")+".meta.json", meta, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// chargeJob is a replayable "find" job in repo that asks where Charge is
// defined and answers with a correct citation.
func chargeJob(repo string) job {
	return job{
		ID:          "charge1",
		Description: "Find where Charge is defined",
		Prompt:      "Find where Charge is defined in billing.go. Cite file:line.",
		Answer:      "`Charge` is defined at `billing.go:3`.",
		Cwd:         repo,
	}
}

// refundJob is the same kind of job about Refund (billing.go:5).
func refundJob(repo string) job {
	return job{
		ID:          "refund1",
		Description: "Find where Refund is defined",
		Prompt:      "Find where Refund is defined in billing.go. Cite file:line.",
		Answer:      "`Refund` is defined at `billing.go:5`.",
		Cwd:         repo,
	}
}

// timeoutJob is a replayable research job: it fetched one docs page and
// answered from it. It needs no repo.
func timeoutJob(cwd string) job {
	return job{
		ID:          "timeout1",
		Description: "Research the default timeout",
		Prompt:      "Research the default timeout in the Acme docs and cite the page.",
		Answer:      "The default timeout is 30 seconds (https://example.com/docs/timeout).",
		Cwd:         cwd,
		Tool:        "WebFetch",
		Input:       map[string]any{"url": "https://example.com/docs/timeout", "prompt": "What is the default timeout?"},
		Result:      "Acme requests time out after 30 seconds unless you set a timeout.",
	}
}

// retryJob is a second research job, about retries.
func retryJob(cwd string) job {
	return job{
		ID:          "retry1",
		Description: "Research the retry delay",
		Prompt:      "Research the retry delay in the Acme docs and cite the page.",
		Answer:      "Failed requests are retried after 15 seconds (https://example.com/docs/retries).",
		Cwd:         cwd,
		Tool:        "WebFetch",
		Input:       map[string]any{"url": "https://example.com/docs/retries", "prompt": "What is the retry delay?"},
		Result:      "Acme retries a failed request after 15 seconds.",
	}
}

// testRung is the setup identity the stored-run fixtures use.
var testRung = store.Rung{Model: "tiny-q4", Quant: "q4", Server: "openai", Context: 8192, Loop: "builtin"}

// storedRuns reads every run back from the store in home.
func storedRuns(t *testing.T, home string) []store.Run {
	t.Helper()
	st, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := st.All()
	if err != nil {
		t.Fatal(err)
	}
	return runs
}

// addRuns appends runs to the store in home.
func addRuns(t *testing.T, home string, runs ...store.Run) {
	t.Helper()
	st, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range runs {
		if err := st.Add(r); err != nil {
			t.Fatal(err)
		}
	}
}

// exists reports whether a path is there.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// readFile returns a file's text.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
