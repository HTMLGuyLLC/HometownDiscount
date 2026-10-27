package goldset

import "testing"

func TestExtract(t *testing.T) {
	it, err := Extract("testdata/agent-g1.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if it.Prompt != "Audit config.go for unsafe defaults" {
		t.Errorf("prompt = %q", it.Prompt)
	}
	if it.Answer != "- **High**: `Debug` defaults to true in config.go:3" {
		t.Errorf("answer = %q", it.Answer)
	}
	if it.Cwd != "/Users/me/app" || it.GitBranch != "main" {
		t.Errorf("cwd/branch = %q %q", it.Cwd, it.GitBranch)
	}
	if len(it.Reads) != 2 {
		t.Fatalf("reads = %d, want 2 (the failed read is skipped)", len(it.Reads))
	}
	full, part := it.Reads[0], it.Reads[1]
	if !full.Full || full.Lines[3] != "var Debug = true" || full.Lines[2] != "" || len(full.Lines) != 3 {
		t.Errorf("full read = %+v", full)
	}
	if part.Full || part.Lines[11] != "func b() {}" {
		t.Errorf("partial read = %+v", part)
	}
	if len(it.Web) != 2 || it.Web[0].URL != "https://example.com/docs" || it.Web[0].Result != "Default is false." || it.Web[1].Query != "go config defaults" {
		t.Errorf("web = %+v", it.Web)
	}
	if want := []string{"/Users/me/app/docs/plan.md", "/Users/me/app/src"}; len(it.Named) != 2 || it.Named[0] != want[0] || it.Named[1] != want[1] {
		t.Errorf("named = %v, want %v (shell and Grep paths, after the cd)", it.Named, want)
	}
	if len(it.Problems) != 0 {
		t.Errorf("problems = %v", it.Problems)
	}
}
