package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStore(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "htd"))
	if err != nil {
		t.Fatal(err)
	}
	if runs, err := s.All(); err != nil || len(runs) != 0 {
		t.Fatalf("empty store: %v %v", runs, err)
	}
	r := Rung{Model: "Qwen3.8-27B", Quant: "4bit", Server: "openai", Context: 262144, Loop: "builtin"}
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	// Runs come back in the order they ran (At), whatever their job's date.
	s.Add(Run{JobID: "b", JobTime: t0, At: t0.Add(2 * time.Hour), Rung: r.ID(), Pass: true, Graded: true})
	s.Add(Run{JobID: "a", JobTime: t0.Add(time.Hour), At: t0.Add(time.Hour), Rung: r.ID(), Graded: true})
	// A torn last line (killed mid-write) is skipped.
	f, _ := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"job_id":"c",`)
	f.Close()

	runs, err := s.All()
	if err != nil || len(runs) != 2 || runs[0].JobID != "a" {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	done, _ := s.Done(r.ID())
	if !done["a"] || !done["b"] || done["c"] {
		t.Errorf("done = %v", done)
	}
	if got := r.Label(); got != "Qwen3.8-27B · 4bit · 262,144 ctx · builtin" {
		t.Errorf("label = %q", got)
	}
	other := r
	other.Quant = "8bit"
	if other.ID() == r.ID() {
		t.Error("a different quant must be a different rung")
	}
	loaded := r
	loaded.Context, loaded.Server = 4096, "ollama 0.13"
	if loaded.ID() != r.ID() {
		t.Error("context and server version are shown, not part of the key")
	}
	old := `{"model":"Qwen3.8-27B","quant":"4bit","server":"openai","context":262144,"loop":"builtin"}`
	if CanonicalID(old) != r.ID() {
		t.Errorf("CanonicalID(%s) = %s", old, CanonicalID(old))
	}
}

// A regraded copy replaces its run in place: the latest grade wins and the
// run keeps its place in the order.
func TestRegradedRunReplacesTheOriginal(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "htd"))
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	rung := Rung{Model: "m", Quant: "4bit", Loop: "builtin"}.ID()
	s.Add(Run{JobID: "a", At: t0, Rung: rung, Graded: true, Reason: "recall"})
	s.Add(Run{JobID: "b", At: t0.Add(time.Hour), Rung: rung, Graded: true, Pass: true})
	s.Add(Run{JobID: "a", At: t0, Rung: rung, Graded: false, Reason: "low-anchor", Regraded: t0.Add(2 * time.Hour)})
	runs, _ := s.All()
	if len(runs) != 2 || runs[0].JobID != "a" || runs[0].Graded || runs[0].Reason != "low-anchor" {
		t.Errorf("runs = %+v", runs)
	}
}
