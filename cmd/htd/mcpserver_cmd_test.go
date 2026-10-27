package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/config"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

// mcpSession drives `htd mcp` over pipes the way an MCP client does: one
// JSON-RPC message per line.
type mcpSession struct {
	t     *testing.T
	in    *io.PipeWriter
	lines chan string
	done  chan error
	next  int
	notes []string // notifications seen so far
}

func startMCP(t *testing.T) *mcpSession {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &mcpSession{t: t, in: inW, lines: make(chan string, 64), done: make(chan error, 1)}
	go func() {
		err := runMCP(nil, inR, outW, io.Discard)
		outW.Close()
		s.done <- err
	}()
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 64<<10), 4<<20)
		for sc.Scan() {
			s.lines <- sc.Text()
		}
		close(s.lines)
	}()
	t.Cleanup(func() { s.in.Close() })
	return s
}

// call sends a request and returns the reply's result (or its error object),
// noting any notifications that arrive first.
func (s *mcpSession) call(method string, params any) map[string]any {
	s.t.Helper()
	s.next++
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": s.next, "method": method, "params": params})
	if _, err := s.in.Write(append(req, '\n')); err != nil {
		s.t.Fatalf("write %s: %v", method, err)
	}
	for {
		select {
		case line, ok := <-s.lines:
			if !ok {
				s.t.Fatalf("server closed before answering %s", method)
			}
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				s.t.Fatalf("not JSON: %q", line)
			}
			if m, ok := msg["method"].(string); ok && msg["id"] == nil {
				s.notes = append(s.notes, m)
				continue
			}
			if int(msg["id"].(float64)) != s.next {
				s.t.Fatalf("reply %v to request %d", msg["id"], s.next)
			}
			if e, ok := msg["error"].(map[string]any); ok {
				return map[string]any{"error": e}
			}
			res, _ := msg["result"].(map[string]any)
			return res
		case <-time.After(10 * time.Second):
			s.t.Fatalf("no reply to %s within 10s", method)
		}
	}
}

// flush pings twice, so a notification sent after the first reply has
// arrived by the time it returns.
func (s *mcpSession) flush() {
	s.t.Helper()
	s.call("ping", nil)
	s.call("ping", nil)
}

// askLocal calls the ask_local tool and returns its text and error flag.
func (s *mcpSession) askLocal(args map[string]any) (string, bool) {
	s.t.Helper()
	res := s.call("tools/call", map[string]any{"name": "ask_local", "arguments": args})
	content, _ := res["content"].([]any)
	if len(content) != 1 {
		s.t.Fatalf("tool result = %v", res)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	isErr, _ := res["isError"].(bool)
	return text, isErr
}

// toolDescription is ask_local's description from tools/list.
func (s *mcpSession) toolDescription() string {
	s.t.Helper()
	tools, _ := s.call("tools/list", nil)["tools"].([]any)
	if len(tools) != 1 {
		s.t.Fatalf("want one tool, got %v", tools)
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "ask_local" {
		s.t.Fatalf("tool = %v", tool)
	}
	return tool["description"].(string)
}

// The tool description is how Claude learns what it may ask for. Its wording
// changes; these check what it has to say: the server can't be used, nothing
// is proven yet, or which skills are.
func saysUnreachable(d string) bool {
	return strings.Contains(d, "reachable") && strings.Contains(d, "every call is refused")
}

func saysNothingProven(d string) bool {
	return strings.Contains(d, "htd bench") && strings.Contains(d, "every call is refused")
}

func saysLookupProven(d string) bool {
	return strings.Contains(d, "lookup") && strings.Contains(d, "200 lines") && !strings.Contains(d, "every call is refused")
}

// mcpProject makes a project folder with one source file, and runs `htd mcp`
// from it, as `claude mcp add` would.
func mcpProject(t *testing.T) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app\n\nfunc Charge() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("TOKEN=abc\n"), 0o644)
	t.Chdir(dir)
}

// configureLocal saves a config that uses the fake server and returns the
// setup identity its evidence is kept under.
func configureLocal(t *testing.T, home string, srv *fakeServer, model string) string {
	t.Helper()
	cfg := config.Default()
	cfg.Local = []config.Local{{URL: srv.URL, Model: model, Quant: "auto", Loop: "builtin"}}
	if err := config.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	_, rung, _, err := probeRung(context.Background(), srv.URL, model, false)
	if err != nil {
		t.Fatal(err)
	}
	return withConfigQuant(rung, cfg).ID()
}

// proveLookup stores enough passing lookup skill checks on small files for
// the setup to have earned that skill.
func proveLookup(t *testing.T, home, rungID string) {
	t.Helper()
	var runs []store.Run
	for i := 0; i < 30; i++ {
		runs = append(runs, store.Run{
			JobID: fmt.Sprintf("lk%d", i), Type: "skill:lookup", Tier: "small", Rung: rungID,
			Graded: true, Pass: true, Seconds: 2, Turns: 1, At: time.Now().Add(-time.Duration(60-i) * time.Minute),
		})
	}
	addRuns(t, home, runs...)
}

// delegations reads the ask_local log back.
func delegations(t *testing.T, home string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(readFile(t, filepath.Join(home, "delegations.jsonl"))), "\n") {
		var d map[string]any
		if err := json.Unmarshal([]byte(l), &d); err != nil {
			t.Fatalf("bad log line %q", l)
		}
		out = append(out, d)
	}
	return out
}

// With no local model configured the server still starts and names itself,
// and every ask_local call is refused with how to fix it, and logged.
func TestMCPWithoutAConfiguredModel(t *testing.T) {
	home, _ := hermetic(t)
	mcpProject(t)
	s := startMCP(t)

	init := s.call("initialize", map[string]any{"protocolVersion": "2025-06-18"})
	if info, _ := init["serverInfo"].(map[string]any); info["name"] != "hometown-discount" || info["version"] != version {
		t.Errorf("serverInfo = %v", init["serverInfo"])
	}
	if d := s.toolDescription(); !saysUnreachable(d) {
		t.Errorf("the tool should tell Claude it can't be used:\n%s", d)
	}
	text, isErr := s.askLocal(map[string]any{"task": "Where is Charge defined?", "files": []string{"app.go"}, "skill": "lookup"})
	if isErr || !strings.Contains(text, "Not handed to the local model") || !strings.Contains(text, "no local model configured; run `htd setup`") {
		t.Errorf("want a refusal that says how to fix it, got %q (error %v)", text, isErr)
	}
	logged := delegations(t, home)
	if len(logged) != 1 || logged[0]["outcome"] != "refused" || logged[0]["skill"] != "lookup" {
		t.Errorf("the refusal should be logged: %v", logged)
	}
}

// Started in the user's home folder (or /), the server would let Claude hand
// over any file there, so it refuses until it's added from a project folder.
func TestMCPRefusesToServeAHomeFolder(t *testing.T) {
	home, _ := hermetic(t)
	userHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", userHome)
	t.Chdir(userHome)
	srv := newFakeServer(t, modelList("tiny-q4"), healthyModel)
	configureLocal(t, home, srv, "tiny-q4")
	before := srv.requestCount() // configureLocal probed it once to learn the setup's identity
	s := startMCP(t)

	text, _ := s.askLocal(map[string]any{"task": "Where is Charge defined?", "files": []string{"app.go"}, "skill": "lookup"})

	if !strings.Contains(text, "htd mcp started in "+userHome) || !strings.Contains(text, "so only that project's files can be read") {
		t.Errorf("got %q", text)
	}
	if srv.requestCount() != before {
		t.Error("a home folder start must not even connect to the model server")
	}
}

// A configured server that isn't running at start is reported as unreachable.
func TestMCPServerDown(t *testing.T) {
	home, _ := hermetic(t)
	mcpProject(t)
	cfg := config.Default()
	cfg.Local = []config.Local{{URL: closedServerURL(), Model: "tiny-q4", Quant: "auto", Loop: "builtin"}}
	config.Save(home, cfg)
	s := startMCP(t)

	if d := s.toolDescription(); !saysUnreachable(d) {
		t.Errorf("description:\n%s", d)
	}
	text, _ := s.askLocal(map[string]any{"task": "Where is Charge defined?", "files": []string{"app.go"}, "skill": "lookup"})
	if !strings.Contains(text, "isn't reachable") || !strings.Contains(text, "cannot reach") {
		t.Errorf("got %q", text)
	}
}

// A connected model that has proven nothing takes nothing: ask_local refuses
// at once, without bothering the model.
func TestMCPRefusesUnprovenSkills(t *testing.T) {
	home, _ := hermetic(t)
	mcpProject(t)
	srv := newFakeServer(t, modelList("tiny-q4"), healthyModel)
	configureLocal(t, home, srv, "tiny-q4")
	s := startMCP(t)

	if d := s.toolDescription(); !saysNothingProven(d) {
		t.Errorf("description:\n%s", d)
	}
	text, isErr := s.askLocal(map[string]any{"task": "Where is Charge defined?", "files": []string{"app.go"}, "skill": "lookup"})

	if isErr || !strings.Contains(text, `"lookup" at 3 lines isn't proven for tiny-q4`) || !strings.Contains(text, "it takes: nothing yet") {
		t.Errorf("got %q (error %v)", text, isErr)
	}
	if srv.chatCount() != 0 {
		t.Error("a refusal must not reach the model")
	}
}

// A proven skill is handed to the local model: it gets the file with line
// numbers and the task, Claude gets its answer, and the call is logged with
// the bytes kept out of Claude's context.
func TestMCPAnswersAProvenSkill(t *testing.T) {
	home, _ := hermetic(t)
	mcpProject(t)
	srv := newFakeServer(t, modelList("tiny-q4"), func(int, chatRequest) fakeReply {
		return fakeReply{Content: "Charge is defined on line 3.", Tokens: 8}
	})
	proveLookup(t, home, configureLocal(t, home, srv, "tiny-q4"))
	s := startMCP(t)

	if d := s.toolDescription(); !saysLookupProven(d) {
		t.Errorf("the description should list the proven skill:\n%s", d)
	}
	text, isErr := s.askLocal(map[string]any{"task": "On which line is Charge defined?", "files": []string{"app.go"}, "skill": "lookup"})

	if isErr || !strings.Contains(text, "Charge is defined on line 3.") || !strings.Contains(text, "lookup") || !strings.Contains(text, "data from another program") {
		t.Errorf("got %q (error %v)", text, isErr)
	}
	sent := srv.chat(1).task()
	for _, want := range []string{"File: app.go", "3\tfunc Charge() {}", "Task: On which line is Charge defined?"} {
		if !strings.Contains(sent, want) {
			t.Errorf("the model's prompt lacks %q:\n%s", want, sent)
		}
	}
	logged := delegations(t, home)
	if len(logged) != 1 || logged[0]["outcome"] != "answered" || logged[0]["size"] != "small" || logged[0]["lines"] != 3.0 || logged[0]["bytes"] != 30.0 {
		t.Errorf("the answered call should be logged with its size: %v", logged)
	}
}

// The model's answer is checked before Claude sees it: an answer that is
// cut off or has the wrong shape is an error, not data.
func TestMCPRejectsBadModelAnswers(t *testing.T) {
	for name, reply := range map[string]fakeReply{
		"cut off":            {Content: "Let me think about", Finish: "length", Tokens: 8},
		"no line number":     {Content: "It is in the billing file.", Tokens: 8},
		"server error":       {Status: 500, Err: "engine crashed"},
		"empty after thinks": {Content: "<think>hmm</think>", Tokens: 8},
	} {
		t.Run(name, func(t *testing.T) {
			home, _ := hermetic(t)
			mcpProject(t)
			srv := newFakeServer(t, modelList("tiny-q4"), func(int, chatRequest) fakeReply { return reply })
			proveLookup(t, home, configureLocal(t, home, srv, "tiny-q4"))
			s := startMCP(t)

			text, isErr := s.askLocal(map[string]any{"task": "On which line is Charge defined?", "files": []string{"app.go"}, "skill": "lookup"})

			if !isErr || !strings.Contains(text, "Do this yourself.") {
				t.Errorf("want an error telling Claude to do it, got %q (error %v)", text, isErr)
			}
			if logged := delegations(t, home); logged[0]["outcome"] != "error" {
				t.Errorf("logged %v", logged)
			}
		})
	}
}

// A skill the model has proven but the user's speed settings keep on Claude
// is refused, and the description says so.
func TestMCPRefusesProvenSkillsThatAreTooSlow(t *testing.T) {
	home, _ := hermetic(t)
	mcpProject(t)
	srv := newFakeServer(t, modelList("tiny-q4"), healthyModel)
	rungID := configureLocal(t, home, srv, "tiny-q4")
	proveLookup(t, home, rungID)
	cfg := savedConfig(t, home)
	cfg.Speed.ByTask = map[string]any{"lookup": "never"}
	if err := config.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	s := startMCP(t)

	if d := s.toolDescription(); !strings.Contains(d, "slower than the user allows") {
		t.Errorf("description:\n%s", d)
	}
	text, isErr := s.askLocal(map[string]any{"task": "On which line is Charge defined?", "files": []string{"app.go"}, "skill": "lookup"})

	if isErr || !strings.Contains(text, "is proven for tiny-q4") || !strings.Contains(text, "but Claude does it") {
		t.Errorf("got %q (error %v)", text, isErr)
	}
	if srv.chatCount() != 0 {
		t.Error("a refusal must not reach the model")
	}
}

// Files that look like secrets are never read, whatever the skill.
func TestMCPWontReadSecrets(t *testing.T) {
	home, _ := hermetic(t)
	mcpProject(t)
	srv := newFakeServer(t, modelList("tiny-q4"), healthyModel)
	proveLookup(t, home, configureLocal(t, home, srv, "tiny-q4"))
	s := startMCP(t)

	text, isErr := s.askLocal(map[string]any{"task": "What is the token?", "files": []string{".env"}, "skill": "lookup"})

	if !isErr || !strings.Contains(text, "looks like secrets or credentials") {
		t.Errorf("got %q (error %v)", text, isErr)
	}
	if srv.chatCount() != 0 {
		t.Error("a secrets file must never reach the model")
	}
}

// A call that isn't well formed is an error, as is an unknown tool.
func TestMCPBadCalls(t *testing.T) {
	hermetic(t)
	mcpProject(t)
	s := startMCP(t)

	if text, isErr := s.askLocal(map[string]any{"task": "", "files": []string{}, "skill": "lookup"}); !isErr || !strings.Contains(text, "needs a task, 1-5 files and a skill") {
		t.Errorf("got %q (error %v)", text, isErr)
	}
	res := s.call("tools/call", map[string]any{"name": "rm_rf", "arguments": map[string]any{}})
	if content, _ := res["content"].([]any); res["isError"] != true || len(content) != 1 || !strings.Contains(content[0].(map[string]any)["text"].(string), "unknown tool rm_rf") {
		t.Errorf("unknown tool: %v", res)
	}
	if res := s.call("resources/list", nil); res["error"] == nil {
		t.Errorf("an unsupported method should be a JSON-RPC error, got %v", res)
	}
}

// A skill proven by `htd bench` while Claude is open applies without a
// restart: the server announces the change, and the next list shows it.
func TestMCPPicksUpNewlyProvenSkills(t *testing.T) {
	home, _ := hermetic(t)
	mcpProject(t)
	srv := newFakeServer(t, modelList("tiny-q4"), func(int, chatRequest) fakeReply {
		return fakeReply{Content: "Charge is defined on line 3.", Tokens: 8}
	})
	rungID := configureLocal(t, home, srv, "tiny-q4")
	s := startMCP(t)
	s.call("initialize", map[string]any{"protocolVersion": "2025-06-18"})

	if d := s.toolDescription(); !saysNothingProven(d) {
		t.Fatalf("description before proof:\n%s", d)
	}
	s.flush()
	if len(s.notes) != 0 {
		t.Fatalf("nothing changed, but the server announced %v", s.notes)
	}

	proveLookup(t, home, rungID) // `htd bench` finishes in another terminal
	s.flush()
	if len(s.notes) == 0 || s.notes[0] != "notifications/tools/list_changed" {
		t.Fatalf("want a list_changed notification, got %v", s.notes)
	}
	if d := s.toolDescription(); !saysLookupProven(d) {
		t.Errorf("description after proof:\n%s", d)
	}
	text, isErr := s.askLocal(map[string]any{"task": "On which line is Charge defined?", "files": []string{"app.go"}, "skill": "lookup"})
	if isErr || !strings.Contains(text, "Charge is defined on line 3.") {
		t.Errorf("the newly proven skill should be served: %q (error %v)", text, isErr)
	}

	// Once the client has listed again, the announcements stop.
	s.notes = nil
	s.flush()
	if len(s.notes) != 0 {
		t.Errorf("announced again after the client re-listed: %v", s.notes)
	}
}

func TestMCPHelp(t *testing.T) {
	hermetic(t)
	var out strings.Builder
	err := runMCP([]string{"--help"}, strings.NewReader(""), &out, &out)
	if err == nil || !strings.Contains(out.String(), "claude mcp add htd -- htd mcp") {
		t.Errorf("err = %v, output %q", err, out.String())
	}
}
