// Package goldset pulls what a replay needs out of a subagent transcript:
// the task, the original (reference) answer, the files it read and the web
// pages it saw.
package goldset

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Item is one replayable job.
type Item struct {
	Prompt    string
	Answer    string // the reference: the hand-back message if any, else the last text reply
	Cwd       string
	GitBranch string
	Reads     []FileRead
	Named     []string // absolute paths the job's shell commands, Grep and Glob named
	Web       []WebCall
	Problems  []string // why the item may not replay faithfully, e.g. a truncated tool result
}

// FileRead is what one Read call returned.
type FileRead struct {
	Path  string
	Full  bool           // read without offset/limit and not cut short
	Lines map[int]string // 1-based line number -> text
}

// WebCall is one recorded WebFetch or WebSearch.
type WebCall struct {
	Tool   string // "WebFetch" or "WebSearch"
	URL    string // WebFetch
	Prompt string // WebFetch's question about the page
	Query  string // WebSearch
	Result string // what the tool returned to the model
}

type entry struct {
	Type      string `json:"type"`
	Cwd       string `json:"cwd"`
	GitBranch string `json:"gitBranch"`
	Message   *struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Text      string          `json:"text"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type readInput struct {
	FilePath string `json:"file_path"`
	Offset   *int   `json:"offset"`
	Limit    *int   `json:"limit"`
}

type webInput struct {
	URL    string `json:"url"`
	Prompt string `json:"prompt"`
	Query  string `json:"query"`
}

type handbackInput struct {
	Message string `json:"message"`
}

// Extract reads one subagent transcript.
func Extract(path string) (*Item, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	it := &Item{}
	calls := map[string]block{} // tool_use id -> call
	var lastText, handback string
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, rerr := r.ReadBytes('\n')
		var e entry
		if len(line) > 0 && json.Unmarshal(line, &e) == nil && e.Message != nil {
			if it.Cwd == "" {
				it.Cwd, it.GitBranch = e.Cwd, e.GitBranch
			}
			switch e.Type {
			case "user":
				if it.Prompt == "" {
					it.Prompt = textOf(e.Message.Content)
				}
				for _, b := range blocks(e.Message.Content) {
					if b.Type == "tool_result" {
						it.addResult(calls[b.ToolUseID], b)
					}
				}
			case "assistant":
				for _, b := range blocks(e.Message.Content) {
					switch b.Type {
					case "text":
						if strings.TrimSpace(b.Text) != "" {
							lastText = b.Text
						}
					case "tool_use":
						calls[b.ID] = b
						if b.Name == "SubagentHandback" {
							var in handbackInput
							if json.Unmarshal(b.Input, &in) == nil && in.Message != "" {
								handback = in.Message
							}
						}
					}
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, rerr
		}
	}
	it.Answer = lastText
	if handback != "" {
		it.Answer = handback
	}
	return it, nil
}

var numbered = regexp.MustCompile(`^(\d+)\t(.*)$`)

// Read tells the model when it cut a file short; seeing this means the
// result isn't the whole file even without offset/limit.
var cutShort = regexp.MustCompile(`(?i)(truncated|file is too large|exceeds|use offset and limit)`)

func (it *Item) addResult(call, res block) {
	if call.ID == "" || res.IsError {
		return
	}
	text := textOf(res.Content)
	switch call.Name {
	case "Read":
		var in readInput
		if json.Unmarshal(call.Input, &in) != nil || in.FilePath == "" {
			return
		}
		fr := FileRead{Path: in.FilePath, Full: in.Offset == nil && in.Limit == nil, Lines: map[int]string{}}
		for _, l := range strings.Split(text, "\n") {
			if m := numbered.FindStringSubmatch(l); m != nil {
				n, _ := strconv.Atoi(m[1])
				fr.Lines[n] = m[2]
			} else if cutShort.MatchString(l) {
				fr.Full = false
			}
		}
		it.Reads = append(it.Reads, fr)
	case "Bash":
		var in struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(call.Input, &in) == nil {
			// Each call starts in the job's folder: jobs often cd in one
			// call and use repo-root paths in the next.
			named, _ := CommandPaths(in.Command, it.Cwd)
			it.Named = append(it.Named, named...)
		}
	case "Grep", "Glob":
		var in struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(call.Input, &in) == nil && in.Path != "" {
			if p := resolve(it.Cwd, in.Path); p != "" {
				it.Named = append(it.Named, p)
			}
		}
	case "WebFetch", "WebSearch":
		var in webInput
		_ = json.Unmarshal(call.Input, &in)
		if text == "" {
			it.Problems = append(it.Problems, call.Name+" returned nothing")
		}
		it.Web = append(it.Web, WebCall{Tool: call.Name, URL: in.URL, Prompt: in.Prompt, Query: in.Query, Result: text})
	}
}

// textOf flattens message or tool-result content (a string or a list of
// text blocks) into plain text.
func textOf(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	if content[0] == '"' {
		var s string
		_ = json.Unmarshal(content, &s)
		return s
	}
	var parts []string
	for _, b := range blocks(content) {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func blocks(content json.RawMessage) []block {
	if len(content) == 0 || content[0] != '[' {
		return nil
	}
	var bs []block
	_ = json.Unmarshal(content, &bs)
	return bs
}
