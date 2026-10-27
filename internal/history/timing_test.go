package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Claude's time to answer after reading a file: a lone Read result counts,
// timed to the end of Claude's next message; results that arrive together
// don't, since their time can't be split per file.
func TestReadTimings(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "-Users-me-app")
	os.MkdirAll(filepath.Join(proj, "s1", "subagents"), 0o755)
	main := strings.Join([]string{
		`{"type":"assistant","timestamp":"2026-10-20T10:00:00Z","message":{"id":"m1","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"r1","name":"Read","input":{"file_path":"/a.go"}}]}}`,
		`{"type":"user","timestamp":"2026-10-20T10:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"r1","content":"1\tpackage a\n2\t\n3\tfunc A() {}\n"}]}}`,
		`{"type":"assistant","timestamp":"2026-10-20T10:00:03Z","message":{"id":"m2","model":"claude-opus-5-5","content":[{"type":"thinking","thinking":"..."}]}}`,
		`{"type":"assistant","timestamp":"2026-10-20T10:00:05Z","message":{"id":"m2","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"r2","name":"Read","input":{}},{"type":"tool_use","id":"r3","name":"Read","input":{}}]}}`,
		`{"type":"user","timestamp":"2026-10-20T10:00:06Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"r2","content":"x"},{"type":"tool_result","tool_use_id":"r3","content":"y"}]}}`,
		`{"type":"assistant","timestamp":"2026-10-20T10:00:09Z","message":{"id":"m3","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"r4","name":"Read","input":{}}]}}`,
		`{"type":"user","timestamp":"2026-10-20T10:00:10Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"r4","content":"File does not exist.","is_error":true}]}}`,
		`{"type":"assistant","timestamp":"2026-10-20T10:00:12Z","message":{"id":"m4","model":"claude-opus-5-5","content":[{"type":"text","text":"done"}]}}`,
	}, "\n")
	os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(main+"\n"), 0o644)
	sub := strings.Join([]string{
		`{"type":"user","agentId":"a1","sessionId":"s1","timestamp":"2026-10-20T11:00:00Z","message":{"role":"user","content":"find it"}}`,
		`{"type":"assistant","agentId":"a1","timestamp":"2026-10-20T11:00:02Z","message":{"id":"s1","model":"claude-sonnet-5","usage":{"input_tokens":5,"output_tokens":5},"content":[{"type":"tool_use","id":"q1","name":"Read","input":{}}]}}`,
		`{"type":"user","agentId":"a1","timestamp":"2026-10-20T11:00:03Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"q1","content":[{"type":"text","text":"1\ta\n2\tb"}]}]}}`,
		`{"type":"assistant","agentId":"a1","timestamp":"2026-10-20T11:01:03Z","message":{"id":"s2","model":"claude-sonnet-5","usage":{"input_tokens":5,"output_tokens":9},"content":[{"type":"text","text":"It's in a."}]}}`,
	}, "\n")
	os.WriteFile(filepath.Join(proj, "s1", "subagents", "agent-a1.jsonl"), []byte(sub+"\n"), 0o644)

	h, err := Scan(root, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Reads) != 2 {
		t.Fatalf("reads = %+v, want the two lone, successful Read results", h.Reads)
	}
	byLines := map[int]ReadSample{}
	for _, r := range h.Reads {
		byLines[r.Lines] = r
	}
	if r := byLines[3]; r.Seconds != 4 || r.Model != "claude-opus-5-5" {
		t.Errorf("main read = %+v, want 4s (to the end of Claude's next message) on opus 5.5", r)
	}
	if r := byLines[2]; r.Seconds != 60 || r.Later != 1 {
		t.Errorf("subagent read = %+v, want 60s, carried by 1 later request", r)
	}
	if len(h.Jobs) != 1 || h.Jobs[0].Duration() != 63*time.Second {
		t.Errorf("job duration = %v, want 63s", h.Jobs[0].Duration())
	}
}

// The current model comes from recently changed main transcripts only.
func TestRecentModel(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "-Users-me-app")
	os.MkdirAll(filepath.Join(proj, "s2", "subagents"), 0o755)
	now := time.Now().UTC().Format(time.RFC3339)
	line := func(id, model string) string {
		return `{"type":"assistant","timestamp":"` + now + `","message":{"id":"` + id + `","model":"` + model + `","usage":{"input_tokens":1,"output_tokens":1},"content":[]}}`
	}
	os.WriteFile(filepath.Join(proj, "new.jsonl"), []byte(line("a", "claude-opus-5-5")+"\n"+line("b", "claude-opus-5-5[1m]")+"\n"+line("c", "claude-sonnet-5")+"\n"), 0o644)
	old := filepath.Join(proj, "old.jsonl")
	os.WriteFile(old, []byte(line("d", "claude-opus-5")+"\n"+line("e", "claude-opus-5")+"\n"+line("f", "claude-opus-5")+"\n"), 0o644)
	os.Chtimes(old, time.Now().AddDate(0, 0, -10), time.Now().AddDate(0, 0, -10))
	sub := filepath.Join(proj, "s2", "subagents", "agent-x.jsonl")
	os.WriteFile(sub, []byte(line("g", "claude-haiku-5-5")+"\n"+line("h", "claude-haiku-5-5")+"\n"+line("i", "claude-haiku-5-5")+"\n"), 0o644)

	if got := RecentModel(root, 3*24*time.Hour); got != "claude-opus-5-5" {
		t.Errorf("RecentModel = %q, want claude-opus-5-5 (old files and subagents don't count)", got)
	}
	if got := RecentModel(t.TempDir(), time.Hour); got != "" {
		t.Errorf("no transcripts: %q", got)
	}
}
