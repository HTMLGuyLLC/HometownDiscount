package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/speed"
)

const timingNote = "Timing Claude from your transcripts"

// reads is n samples of Claude taking `secs` to answer after reading a small
// file, on opus 5.5.
func reads(n int, secs float64) []history.ReadSample {
	var out []history.ReadSample
	for i := 0; i < n; i++ {
		out = append(out, history.ReadSample{Lines: 50, Bytes: 2000, Seconds: secs, Model: "claude-opus-5-5", Time: time.Now()})
	}
	return out
}

// A history scan the caller already did is measured and saved, for the card
// and the MCP server to use later.
func TestClaudeTimesMeasuresAGivenHistory(t *testing.T) {
	home, claude := hermetic(t)
	h := &history.History{Reads: reads(6, 4)}

	c := claudeTimes(home, filepath.Join(claude, "projects"), h, 30, "claude-opus-5-5", nil)

	if c.Model != "claude-opus-5-5" || c.BySize["small"].Seconds != 4 || c.BySize["small"].N != 6 {
		t.Errorf("times = %+v, want a 4s median over 6 reads for small files", c)
	}
	if saved, ok := speed.Load(home); !ok || saved.BySize["small"].Seconds != 4 {
		t.Errorf("the measurement should be saved, got %+v (found %v)", saved, ok)
	}
}

// Saved times for the same model, less than a week old, are reused without
// reading the transcripts.
func TestClaudeTimesReusesFreshTimes(t *testing.T) {
	home, claude := hermetic(t)
	speed.Save(home, speed.Claude{At: time.Now().Add(-time.Hour), Model: "claude-opus-5-5", BySize: map[string]speed.Sample{"small": {Seconds: 99, N: 7}}})
	var note bytes.Buffer

	c := claudeTimes(home, filepath.Join(claude, "projects"), nil, 30, "claude-opus-5-5", &note)

	if c.BySize["small"].Seconds != 99 || note.Len() != 0 {
		t.Errorf("want the saved 99s without a new measurement: %+v, note %q", c, note.String())
	}
}

// Times that are a week old, or for another Claude model, are measured again.
func TestClaudeTimesRemeasuresStaleOrOtherModel(t *testing.T) {
	for name, saved := range map[string]speed.Claude{
		"a week old":    {At: time.Now().Add(-8 * 24 * time.Hour), Model: "claude-opus-5-5", BySize: map[string]speed.Sample{"small": {Seconds: 99, N: 7}}},
		"another model": {At: time.Now(), Model: "claude-sonnet-5", BySize: map[string]speed.Sample{"small": {Seconds: 99, N: 7}}},
	} {
		t.Run(name, func(t *testing.T) {
			home, claude := hermetic(t)
			speed.Save(home, saved)
			var note bytes.Buffer

			c := claudeTimes(home, filepath.Join(claude, "projects"), nil, 30, "claude-opus-5-5", &note)

			if !strings.Contains(note.String(), timingNote) {
				t.Errorf("want the measuring note, got %q", note.String())
			}
			// Nothing new to measure: the same model's older times are kept;
			// another model's are dropped, as they'd be wrong for this one.
			if name == "a week old" {
				if c.BySize["small"].Seconds != 99 {
					t.Errorf("the same model's older times beat none; got %+v", c)
				}
				return
			}
			if c.Model != "claude-opus-5-5" || len(c.BySize) != 0 {
				t.Errorf("another model's times don't count; got %+v", c)
			}
			if again, _ := speed.Load(home); again.Model != "claude-opus-5-5" {
				t.Errorf("the new (empty) measurement should be saved, got %+v", again)
			}
		})
	}
}

// "auto" follows the Claude model used most in the last few days, so
// switching models is noticed.
func TestClaudeTimesAutoFollowsRecentModel(t *testing.T) {
	home, claude := hermetic(t)
	proj := filepath.Join(claude, "projects", "-proj")
	os.MkdirAll(proj, 0o755)
	now := time.Now().UTC().Format(time.RFC3339)
	line := func(id string) string {
		return `{"type":"assistant","timestamp":"` + now + `","message":{"id":"` + id + `","model":"claude-sonnet-5","usage":{"input_tokens":1,"output_tokens":1},"content":[]}}`
	}
	os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(line("a")+"\n"+line("b")+"\n"), 0o644)
	speed.Save(home, speed.Claude{At: time.Now(), Model: "claude-opus-5-5", BySize: map[string]speed.Sample{"small": {Seconds: 99, N: 7}}})
	var note bytes.Buffer

	c := claudeTimes(home, filepath.Join(claude, "projects"), nil, 30, "auto", &note)

	if c.Model != "claude-sonnet-5" || !strings.Contains(note.String(), timingNote) {
		t.Errorf("auto should have moved to sonnet 5 and re-measured: %+v, note %q", c, note.String())
	}
}

// If the transcripts can't be read, older saved times beat none.
func TestClaudeTimesFallsBackToSavedTimes(t *testing.T) {
	home, _ := hermetic(t)
	speed.Save(home, speed.Claude{At: time.Now().Add(-30 * 24 * time.Hour), Model: "claude-opus-5-5", BySize: map[string]speed.Sample{"small": {Seconds: 99, N: 7}}})
	missing := filepath.Join(t.TempDir(), "no-such-folder")

	c := claudeTimes(home, missing, nil, 30, "claude-opus-5-5", nil)

	if c.BySize["small"].Seconds != 99 {
		t.Errorf("want the old saved times, got %+v", c)
	}
}
