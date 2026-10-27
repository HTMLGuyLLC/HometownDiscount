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
	End         time.Time // the job's last transcript line
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

// Duration is how long the job took, start to finish.
func (j *Job) Duration() time.Duration {
	if j.End.Before(j.Start) {
		return 0
	}
	return j.End.Sub(j.Start)
}

// ReadSample is one time Claude read a file and then answered: how big the
// file was and how long Claude's next message took to finish.
type ReadSample struct {
	Lines   int
	Bytes   int
	Seconds float64
	Model   string
	Time    time.Time
	// Later counts the requests after the read in the same conversation:
	// each one carried the file again (from the cache) until the end or a
	// compaction, so it's an upper bound.
	Later int
}

// countLater sets each sample's Later from a conversation's requests.
func countLater(samples []ReadSample, reqs []Request) {
	for i := range samples {
		for _, r := range reqs {
			if r.Time.After(samples[i].Time) {
				samples[i].Later++
			}
		}
	}
}

// History is everything read from the transcripts in a time window.
type History struct {
	Jobs  []Job
	Main  []Request    // main-conversation requests (not subagents)
	Reads []ReadSample // Claude's time to answer after reading one file
	Files int          // transcript files read
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
		job, reads, err := readSubagent(path, root)
		if err != nil || job == nil {
			continue
		}
		h.addReads(reads, since)
		if job.Start.Before(since) {
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
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// readTimer pairs a Read tool result with the message Claude wrote next. Only
// a result that arrives alone counts: with several, the time isn't one file's.
type readTimer struct {
	reads   map[string]bool // tool_use ids of Read calls
	pending *ReadSample     // a lone Read result waiting for Claude's reply
	at      time.Time       // when that result arrived
	reply   string          // the id of Claude's reply message
	samples []ReadSample
}

func (r *readTimer) see(e *rawEntry) {
	if r.reads == nil {
		r.reads = map[string]bool{}
	}
	t := parseTime(e.Timestamp)
	if e.Message == nil || t.IsZero() {
		return
	}
	bs := blocks(e.Message.Content)
	if e.Type == "assistant" {
		if r.pending != nil && (r.reply == "" || r.reply == e.Message.ID) {
			r.reply = e.Message.ID
			r.pending.Seconds = t.Sub(r.at).Seconds()
			r.pending.Model = pricing.Normalize(e.Message.Model)
		} else {
			r.flush()
		}
		for _, b := range bs {
			if b.Type == "tool_use" && b.Name == "Read" {
				r.reads[b.ID] = true
			}
		}
		return
	}
	var results []rawBlock
	for _, b := range bs {
		if b.Type == "tool_result" {
			results = append(results, b)
		}
	}
	if len(results) == 0 {
		return
	}
	r.flush()
	if b := results[0]; len(results) == 1 && r.reads[b.ToolUseID] && !b.IsError {
		if text := resultText(b.Content); text != "" {
			r.pending, r.at = &ReadSample{Lines: strings.Count(strings.TrimSuffix(text, "\n"), "\n") + 1, Bytes: len(text), Time: t}, t
		}
	}
}

// flush keeps the pending sample once Claude's reply has been seen.
func (r *readTimer) flush() {
	if r.pending != nil && r.reply != "" && r.pending.Seconds > 0 {
		r.samples = append(r.samples, *r.pending)
	}
	r.pending, r.reply = nil, ""
}

// resultText is a tool result's text, whether stored as a string or as text blocks.
func resultText(content json.RawMessage) string {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
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
	var timer readTimer
	agentCalls := map[string]agentInput{} // tool_use id -> Agent call input
	err := eachEntry(path, func(e *rawEntry) {
		if !e.IsSidechain {
			reqs.add(e)
			timer.see(e)
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
	timer.flush()
	countLater(timer.samples, reqs.list)
	h.addReads(timer.samples, since)
	return err
}

func (h *History) addReads(samples []ReadSample, since time.Time) {
	for _, s := range samples {
		if !s.Time.Before(since) {
			h.Reads = append(h.Reads, s)
		}
	}
}

type rawMeta struct {
	AgentType   string `json:"agentType"`
	Description string `json:"description"`
	SpawnDepth  int    `json:"spawnDepth"`
}

func readSubagent(path, root string) (*Job, []ReadSample, error) {
	job := &Job{Path: path, Tools: map[string]int{}}
	var reqs requests
	var timer readTimer
	seenTools := map[string]bool{}
	err := eachEntry(path, func(e *rawEntry) {
		if job.AgentID == "" && e.AgentID != "" {
			job.AgentID = e.AgentID
		}
		if job.SessionID == "" && e.SessionID != "" {
			job.SessionID = e.SessionID
		}
		if t := parseTime(e.Timestamp); !t.IsZero() {
			if job.Start.IsZero() || t.Before(job.Start) {
				job.Start = t
			}
			if t.After(job.End) {
				job.End = t
			}
		}
		reqs.add(e)
		timer.see(e)
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
		return nil, nil, err
	}
	timer.flush()
	countLater(timer.samples, reqs.list)
	if job.AgentID == "" {
		job.AgentID = strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "agent-"), ".jsonl")
	}
	// Ids end up in file names; anything but a plain id is skipped.
	if !safeID.MatchString(job.AgentID) {
		return nil, nil, nil
	}
	job.Requests = reqs.list
	if len(job.Requests) == 0 {
		return nil, nil, nil
	}
	if rel, err := filepath.Rel(root, path); err == nil {
		job.Project = strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
	}
	var meta rawMeta
	if b, err := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json"); err == nil && json.Unmarshal(b, &meta) == nil {
		job.AgentType, job.Description, job.SpawnDepth = meta.AgentType, meta.Description, meta.SpawnDepth
	}
	return job, timer.samples, nil
}

// RecentModel is the Claude model the user has used most in main
// conversations over the last `within`, read from recently changed
// transcripts only, so it's quick. "" when there are none.
func RecentModel(root string, within time.Duration) string {
	since := time.Now().Add(-within)
	count := map[string]int{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") || filepath.Base(filepath.Dir(path)) == "subagents" {
			return nil
		}
		if info, err := d.Info(); err != nil || info.ModTime().Before(since) {
			return nil
		}
		var reqs requests
		_ = eachEntry(path, func(e *rawEntry) {
			if !e.IsSidechain {
				reqs.add(e)
			}
		})
		for _, r := range reqs.list {
			if !r.Time.Before(since) {
				count[r.Model]++
			}
		}
		return nil
	})
	best, n := "", 0
	for m, c := range count {
		if c > n || (c == n && m < best) {
			best, n = m, c
		}
	}
	return best
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
