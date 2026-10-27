// Package loop is the built-in agent loop that runs a replayed job on a local
// model: the model gets the job's original task, a shell in the sandbox and a
// file reader, and works until it answers.
//
// The model sees the original repo paths. Tools translate them to the
// workspace copy and back, so answers can be compared with the reference.
package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/llm"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/sandbox"
)

// Model is what the loop needs from a chat client.
type Model interface {
	Chat(ctx context.Context, msgs []llm.Message, tools []llm.Tool) (llm.Message, llm.Usage, error)
}

// Web serves recorded web results to research replays. Nil means no web tools.
type Web interface {
	Fetch(url, prompt string) (string, bool)
	Search(query string) (string, bool)
}

// Job is one replay.
type Job struct {
	Task     string // the original subagent prompt
	Dir      string // workspace copy (read-only to commands)
	Tmp      string // scratch folder commands may write to
	RepoRoot string // the original repo root the task refers to
	Web      Web

	canonDir string
}

// Limits keep a replay bounded.
type Limits struct {
	MaxTurns   int
	Timeout    time.Duration
	CmdTimeout time.Duration
}

// maxCutoffs is how many replies in a row may run out of output room before
// the replay stops as "cut off".
const maxCutoffs = 2

const cutOffNudge = "Your last reply ran out of room before you finished. Don't think at length: call a tool, or give your final answer now."

// DefaultLimits fit most read-only jobs.
var DefaultLimits = Limits{MaxTurns: 80, Timeout: 20 * time.Minute, CmdTimeout: 60 * time.Second}

// Result is how a replay ended.
type Result struct {
	Answer    string
	Turns     int
	ToolCalls int
	Usage     llm.Usage
	Stopped   string // "" when the model answered; otherwise why it stopped
	Elapsed   time.Duration
}

const system = `You are a coding agent doing one task in a repository. Use the tools to look at the
code; don't guess. Shell commands run in a sandbox with no network. When you're done, reply
with your final answer as plain text, with no tool calls. The answer is all the user will see,
so include every finding, path and detail they asked for.`

var shellTool = llm.Tool{
	Name:        "bash",
	Description: "Run a shell command (POSIX /bin/sh) in the repository: grep, find, cat, sed -n, awk, ls, wc. The repository is read-only and has no git history; write scratch files to $TMPDIR. There's no network.",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`),
}

var readTool = llm.Tool{
	Name:        "read_file",
	Description: "Read a file with line numbers. Optional offset (1-based first line) and limit (number of lines).",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer"},"limit":{"type":"integer"}},"required":["path"]}`),
}

var fetchTool = llm.Tool{
	Name:        "web_fetch",
	Description: "Fetch a web page and answer a question about it. Only pages from the original research are available offline.",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"},"prompt":{"type":"string"}},"required":["url"]}`),
}

var searchTool = llm.Tool{
	Name:        "web_search",
	Description: "Search the web. Only searches close to the original research return results offline.",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
}

// Run replays one job on the model.
func Run(ctx context.Context, m Model, job Job, lim Limits) (Result, error) {
	start := time.Now()
	if job.Tmp == "" {
		job.Tmp = job.Dir + "-tmp"
	}
	// Commands report the canonical path (macOS: /private/var/...), so map
	// that form back to the original repo too.
	if c, err := filepath.EvalSymlinks(job.Dir); err == nil {
		job.canonDir = c
	}
	ctx, cancel := context.WithTimeout(ctx, lim.Timeout)
	defer cancel()

	tools := []llm.Tool{shellTool, readTool}
	if job.Web != nil {
		tools = append(tools, fetchTool, searchTool)
	}
	msgs := []llm.Message{
		{Role: "system", Content: system + "\nThe repository is at " + job.RepoRoot + "."},
		{Role: "user", Content: job.Task},
	}
	var res Result
	cutoffs := 0
	for res.Turns < lim.MaxTurns {
		reply, usage, err := m.Chat(ctx, msgs, tools)
		res.Turns++
		res.Usage.PromptTokens += usage.PromptTokens
		res.Usage.CompletionTokens += usage.CompletionTokens
		if err != nil {
			res.Elapsed = time.Since(start)
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				res.Stopped = "timeout"
				return res, nil
			}
			return res, err
		}
		if len(reply.ToolCalls) == 0 && reply.FinishReason == "length" {
			// The reply ran out of output room, usually mid-thought: it's not
			// an answer. Ask for a short next step, a couple of times.
			if cutoffs++; cutoffs > maxCutoffs {
				res.Stopped = "cut off"
				res.Elapsed = time.Since(start)
				return res, nil
			}
			msgs = append(msgs, llm.Message{Role: "user", Content: cutOffNudge})
			continue
		}
		cutoffs = 0
		msgs = append(msgs, reply)
		if len(reply.ToolCalls) == 0 {
			res.Answer = llm.StripThinking(reply.Content)
			if res.Answer == "" {
				res.Stopped = "empty answer"
			}
			res.Elapsed = time.Since(start)
			return res, nil
		}
		for _, call := range reply.ToolCalls {
			res.ToolCalls++
			out := job.call(ctx, call, lim)
			msgs = append(msgs, llm.Message{Role: "tool", ToolCallID: call.ID, Content: out})
		}
	}
	res.Stopped = "too many turns"
	res.Elapsed = time.Since(start)
	return res, nil
}

func (j Job) call(ctx context.Context, call llm.ToolCall, lim Limits) string {
	var args struct {
		Command string `json:"command"`
		Path    string `json:"path"`
		Offset  int    `json:"offset"`
		Limit   int    `json:"limit"`
		URL     string `json:"url"`
		Prompt  string `json:"prompt"`
		Query   string `json:"query"`
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return "error: the tool arguments aren't valid JSON: " + err.Error()
	}
	switch call.Function.Name {
	case "bash":
		r, err := sandbox.Run(ctx, j.Dir, j.Tmp, j.toWorkspace(args.Command), lim.CmdTimeout)
		if err != nil {
			return "error: " + err.Error()
		}
		out := r.Stdout
		if r.Stderr != "" {
			out += "\n[stderr]\n" + r.Stderr
		}
		if r.TimedOut {
			out += "\n[command timed out]"
		} else if r.ExitCode != 0 {
			out += fmt.Sprintf("\n[exit code %d]", r.ExitCode)
		}
		return j.toOriginal(out)
	case "read_file":
		return j.toOriginal(j.readFile(args.Path, args.Offset, args.Limit))
	case "web_fetch":
		if j.Web != nil {
			if page, ok := j.Web.Fetch(args.URL, args.Prompt); ok {
				return page
			}
		}
		return "error: this page isn't available offline"
	case "web_search":
		if j.Web != nil {
			if results, ok := j.Web.Search(args.Query); ok {
				return results
			}
		}
		return "No results."
	}
	return "error: unknown tool " + call.Function.Name
}

// readFile reads inside the workspace only; os.Root refuses paths that
// escape it (including through symlinks).
func (j Job) readFile(path string, offset, limit int) string {
	rel := strings.TrimPrefix(j.toWorkspace(path), j.Dir)
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return "error: no path given"
	}
	root, err := os.OpenRoot(j.Dir)
	if err != nil {
		return "error: " + err.Error()
	}
	defer root.Close()
	b, err := root.ReadFile(rel)
	if err != nil {
		return "error: " + strings.ReplaceAll(err.Error(), j.Dir, j.RepoRoot)
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if offset < 1 {
		offset = 1
	}
	if limit <= 0 || limit > 2000 {
		limit = 2000
	}
	var sb strings.Builder
	for n := offset; n < offset+limit && n <= len(lines); n++ {
		fmt.Fprintf(&sb, "%d\t%s\n", n, lines[n-1])
	}
	if offset+limit <= len(lines) {
		fmt.Fprintf(&sb, "[file has %d lines; use offset and limit to read more]\n", len(lines))
	}
	return sb.String()
}

func (j Job) toWorkspace(s string) string {
	if j.RepoRoot == "" {
		return s
	}
	return strings.ReplaceAll(s, j.RepoRoot, j.Dir)
}

func (j Job) toOriginal(s string) string {
	if j.RepoRoot == "" {
		return s
	}
	if j.canonDir != "" && j.canonDir != j.Dir {
		s = strings.ReplaceAll(s, j.canonDir, j.RepoRoot)
	}
	return strings.ReplaceAll(s, j.Dir, j.RepoRoot)
}
