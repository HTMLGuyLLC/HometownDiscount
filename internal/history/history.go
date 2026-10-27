// Package history reads Claude Code's local transcripts (~/.claude/projects)
// and turns them into subagent jobs and main-conversation requests.
package history

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/pricing"
)

// Request is one billed API request.
type Request struct {
	Model string
	Usage pricing.Usage
	Time  time.Time
}

// Job is one subagent run.
type Job struct {
	AgentID     string
	SessionID   string
	Path        string // the subagent's transcript file
	Project     string // transcript folder name, e.g. "-Users-me-code-app"
	AgentType   string // "Explore", "general-purpose", a custom agent name...
	Description string
	Background  bool
	SpawnDepth  int
	Start       time.Time
	Requests    []Request
	Tools       map[string]int // tool calls by tool name
	Commands    []string       // the shell commands it ran (Bash tool inputs)
}

// Model is the model that produced most of the job's output tokens.
func (j *Job) Model() string {
	out := map[string]int64{}
	for _, r := range j.Requests {
		out[r.Model] += r.Usage.Output + 1 // +1 so zero-output requests still count
	}
	best, bestN := "", int64(-1)
	for m, n := range out {
		if n > bestN || (n == bestN && m < best) {
			best, bestN = m, n
		}
	}
	return best
}

// History is everything read from the transcripts in a time window.
type History struct {
	Jobs  []Job
	Main  []Request // main-conversation requests (not subagents)
	Files int       // transcript files read
}

// DefaultRoot is where Claude Code keeps transcripts.
func DefaultRoot() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "projects")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

// spawnInfo is what the parent conversation knows about a subagent call.
type spawnInfo struct {
	Description string
	AgentType   string
	Background  bool
}

// Scan reads all transcripts under root and keeps what happened at or after since.
func Scan(root string, since time.Time) (*History, error) {
	h := &History{}
	spawns := map[string]spawnInfo{} // agentId -> parent's view of the spawn
	var subagentFiles []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) == "subagents" {
			subagentFiles = append(subagentFiles, path)
			return nil
		}
		h.Files++
		return readMain(path, since, h, spawns)
	})
	if err != nil {
		return nil, err
	}

	for _, path := range subagentFiles {
		h.Files++
		job, err := readSubagent(path, root)
		if err != nil || job == nil || job.Start.Before(since) {
			continue
		}
		if s, ok := spawns[job.AgentID]; ok {
			if job.Description == "" {
				job.Description = s.Description
			}
			if job.AgentType == "" {
				job.AgentType = s.AgentType
			}
			job.Background = job.Background || s.Background
		}
		if job.AgentType == "" {
			job.AgentType = "general-purpose"
		}
		h.Jobs = append(h.Jobs, *job)
	}
	sort.Slice(h.Jobs, func(i, k int) bool { return h.Jobs[i].Start.Before(h.Jobs[k].Start) })
	return h, nil
}

type rawEntry struct {
	Type          string          `json:"type"`
	IsSidechain   bool            `json:"isSidechain"`
	AgentID       string          `json:"agentId"`
	SessionID     string          `json:"sessionId"`
	Timestamp     string          `json:"timestamp"`
	Message       *rawMessage     `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
}

type rawMessage struct {
	ID      string          `json:"id"`
	Model   string          `json:"model"`
	Usage   *rawUsage       `json:"usage"`
	Content json.RawMessage `json:"content"`
}

type rawUsage struct {
	InputTokens              int64  `json:"input_tokens"`
	CacheCreationInputTokens int64  `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64  `json:"cache_read_input_tokens"`
	OutputTokens             int64  `json:"output_tokens"`
	Speed                    string `json:"speed"`
	CacheCreation            *struct {
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

func (u *rawUsage) toUsage() pricing.Usage {
	out := pricing.Usage{
		Input:     u.InputTokens,
		CacheRead: u.CacheReadInputTokens,
		Output:    u.OutputTokens,
		Fast:      u.Speed == "fast",
	}
	if c := u.CacheCreation; c != nil && c.Ephemeral5m+c.Ephemeral1h > 0 {
		out.CacheWrite5m, out.CacheWrite1h = c.Ephemeral5m, c.Ephemeral1h
	} else {
		out.CacheWrite5m = u.CacheCreationInputTokens
	}
	return out
}

type rawBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
}

type agentInput struct {
	Description     string `json:"description"`
	SubagentType    string `json:"subagent_type"`
	RunInBackground bool   `json:"run_in_background"`
}

type agentResult struct {
	AgentID string `json:"agentId"`
	IsAsync bool   `json:"isAsync"`
}

// requests collects billed requests, de-duplicated by message id. Claude Code
// writes one transcript line per content block, each repeating the usage seen
// so far; the line with the most output tokens is the final count.
type requests struct {
	list  []Request
	index map[string]int
}

func (r *requests) add(e *rawEntry) {
	m := e.Message
	if e.Type != "assistant" || m == nil || m.Usage == nil || m.Model == "" || strings.HasPrefix(m.Model, "<") {
		return
	}
	if r.index == nil {
		r.index = map[string]int{}
	}
	req := Request{Model: pricing.Normalize(m.Model), Usage: m.Usage.toUsage(), Time: parseTime(e.Timestamp)}
	if i, ok := r.index[m.ID]; ok && m.ID != "" {
		if req.Usage.Output >= r.list[i].Usage.Output {
			req.Time = r.list[i].Time
			r.list[i] = req
		}
		return
	}
	r.index[m.ID] = len(r.list)
	r.list = append(r.list, req)
}

func readMain(path string, since time.Time, h *History, spawns map[string]spawnInfo) error {
	var reqs requests
	agentCalls := map[string]agentInput{} // tool_use id -> Agent call input
	err := eachEntry(path, func(e *rawEntry) {
		if !e.IsSidechain {
			reqs.add(e)
		}
		if e.Message == nil {
			return
		}
		for _, b := range blocks(e.Message.Content) {
			switch {
			case b.Type == "tool_use" && (b.Name == "Agent" || b.Name == "Task"):
				var in agentInput
				_ = json.Unmarshal(b.Input, &in)
				agentCalls[b.ID] = in
			case b.Type == "tool_result" && len(e.ToolUseResult) > 0:
				var res agentResult
				if json.Unmarshal(e.ToolUseResult, &res) != nil || res.AgentID == "" {
					continue
				}
				in := agentCalls[b.ToolUseID]
				spawns[res.AgentID] = spawnInfo{
					Description: in.Description,
					AgentType:   in.SubagentType,
					Background:  in.RunInBackground || res.IsAsync,
				}
			}
		}
	})
	for _, r := range reqs.list {
		if !r.Time.Before(since) {
			h.Main = append(h.Main, r)
		}
	}
	return err
}

type rawMeta struct {
	AgentType   string `json:"agentType"`
	Description string `json:"description"`
	SpawnDepth  int    `json:"spawnDepth"`
}

func readSubagent(path, root string) (*Job, error) {
	job := &Job{Path: path, Tools: map[string]int{}}
	var reqs requests
	seenTools := map[string]bool{}
	err := eachEntry(path, func(e *rawEntry) {
		if job.AgentID == "" && e.AgentID != "" {
			job.AgentID = e.AgentID
		}
		if job.SessionID == "" && e.SessionID != "" {
			job.SessionID = e.SessionID
		}
		if t := parseTime(e.Timestamp); !t.IsZero() && (job.Start.IsZero() || t.Before(job.Start)) {
			job.Start = t
		}
		reqs.add(e)
		if e.Type != "assistant" || e.Message == nil {
			return
		}
		for _, b := range blocks(e.Message.Content) {
			if b.Type == "tool_use" && !seenTools[b.ID] {
				seenTools[b.ID] = true
				job.Tools[b.Name]++
				if b.Name == "Bash" {
					var in struct {
						Command string `json:"command"`
					}
					if json.Unmarshal(b.Input, &in) == nil && in.Command != "" {
						job.Commands = append(job.Commands, in.Command)
					}
				}
			}
		}
	})
	if err != nil {
		return nil, err
	}
	if job.AgentID == "" {
		job.AgentID = strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "agent-"), ".jsonl")
	}
	// Ids end up in file names; anything but a plain id is skipped.
	if !safeID.MatchString(job.AgentID) {
		return nil, nil
	}
	job.Requests = reqs.list
	if len(job.Requests) == 0 {
		return nil, nil
	}
	if rel, err := filepath.Rel(root, path); err == nil {
		job.Project = strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
	}
	var meta rawMeta
	if b, err := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json"); err == nil && json.Unmarshal(b, &meta) == nil {
		job.AgentType, job.Description, job.SpawnDepth = meta.AgentType, meta.Description, meta.SpawnDepth
	}
	return job, nil
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// eachEntry calls fn for every JSON line in a file, skipping lines that don't parse.
// Lines can be very long (pasted images), so it reads without a size limit.
func eachEntry(path string, fn func(*rawEntry)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var e rawEntry
			if json.Unmarshal(line, &e) == nil {
				fn(&e)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func blocks(content json.RawMessage) []rawBlock {
	if len(content) == 0 || content[0] != '[' {
		return nil
	}
	var bs []rawBlock
	_ = json.Unmarshal(content, &bs)
	return bs
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}
