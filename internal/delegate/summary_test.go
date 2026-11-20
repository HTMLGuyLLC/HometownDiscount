package delegate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/speed"
)

func TestLoadAndSummarize(t *testing.T) {
	path := filepath.Join(t.TempDir(), LogName)
	os.WriteFile(path, []byte(`{"outcome":"answered","bytes":4000,"answer_bytes":400}
{"outcome":"answered","bytes":1000}
{"outcome":"refused","bytes":9000}
not json
{"outcome":"error"}
`), 0o644)
	ds := Load(path)
	if len(ds) != 4 {
		t.Fatalf("loaded %d, want 4 (the bad line skipped)", len(ds))
	}
	s := Summarize(ds)
	// 4000-400 and 1000-600 (the guess for answers before they were logged).
	if s.Answered != 2 || s.Refused != 1 || s.KeptBytes != 4000 || s.Tokens() != 1000 {
		t.Errorf("summary: %+v", s)
	}
	if Load(filepath.Join(t.TempDir(), "none")) != nil {
		t.Error("a missing log is no calls")
	}
}

func TestKept(t *testing.T) {
	if k := (Delegation{Outcome: "answered", Bytes: 100, AnswerBytes: 400}).Kept(); k != 0 {
		t.Errorf("an answer longer than the files keeps nothing, got %d", k)
	}
	if k := (Delegation{Outcome: "refused", Bytes: 5000}).Kept(); k != 0 {
		t.Errorf("a refusal keeps nothing, got %d", k)
	}
}

func TestWorthUnknown(t *testing.T) {
	if _, ok := Worth(1000, speed.Claude{}, nil); ok {
		t.Error("no prices, no worth")
	}
}
