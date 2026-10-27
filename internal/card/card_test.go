package card

import (
	"bytes"
	"strings"
	"testing"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/config"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/speed"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

func TestBuildAndRender(t *testing.T) {
	var runs []store.Run
	for i := 0; i < 24; i++ {
		runs = append(runs, store.Run{JobID: "f", Type: "find", Tier: "small", Rung: "r1", Graded: true, Pass: true, Seconds: 30})
	}
	for i := 0; i < 5; i++ {
		runs = append(runs, store.Run{JobID: "g", Type: "find", Tier: "large", Rung: "r1", Graded: true, Pass: false, Seconds: 600})
	}
	for i := 0; i < 10; i++ {
		runs = append(runs, store.Run{JobID: "v", Type: "review", Rung: "r1", Graded: true, Pass: i != 3, Seconds: 120})
	}
	runs = append(runs, store.Run{JobID: "x", Type: "review", Rung: "r1", Graded: false, Reason: "low-anchor"})

	rows := Build(runs, map[string]string{"r1": "Qwen3.8-27B · 4bit · builtin"})
	if len(rows) != 3 || rows[0].Tier != "small" || rows[1].Tier != "large" || rows[2].Type != "review" {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Label() != "proven" {
		t.Errorf("24/24 at the default bar should be proven, got %s", rows[0].Label())
	}
	if rows[1].Label() != "not good enough" {
		t.Errorf("0/5 should be not good enough, got %s", rows[1].Label())
	}
	if rows[2].Label() != "learning" || rows[2].Skipped != 1 || rows[2].Graded != 10 {
		t.Errorf("review row = %+v", rows[2])
	}

	var buf bytes.Buffer
	Render(&buf, rows)
	out := buf.String()
	for _, want := range []string{"Qwen3.8-27B · 4bit · builtin", "proven", "24 of 24", "0.86 (default)", "not good enough", "10.0m", "Skill checks", "1 replay couldn't be graded"} {
		if !strings.Contains(out, want) {
			t.Errorf("card missing %q:\n%s", want, out)
		}
	}
}

func TestRenderEmpty(t *testing.T) {
	var buf bytes.Buffer
	Render(&buf, nil)
	Headline(&buf, nil)
	if strings.Count(buf.String(), "Nothing tested yet") != 2 {
		t.Error(buf.String())
	}
}

func TestHeadline(t *testing.T) {
	var runs []store.Run
	for i := 0; i < 24; i++ {
		runs = append(runs,
			store.Run{Type: "skill:lookup", Tier: "small", Rung: "r1", Graded: true, Pass: true},
			store.Run{Type: "skill:lookup", Tier: "medium", Rung: "r1", Graded: true, Pass: true})
	}
	for i := 0; i < 5; i++ {
		runs = append(runs, store.Run{Type: "find", Tier: "small", Rung: "r1", Graded: true})
	}
	runs = append(runs, store.Run{Type: "skill:list", Tier: "small", Rung: "r1", Graded: true, Pass: true})
	var buf bytes.Buffer
	Headline(&buf, Build(runs, map[string]string{"r1": "Qwen"}))
	out := buf.String()
	for _, want := range []string{"Can take now:    lookup (up to 1,000 lines)", "Not proven yet:  list (files up to 200 lines, 1 of 1 passed)", "Not good enough: find jobs (small, 0 of 5 passed)", "To prove more: run `htd bench`"} {
		if !strings.Contains(out, want) {
			t.Errorf("headline missing %q:\n%s", want, out)
		}
	}
}

// A proven skill that's too slow for its limit moves to "Too slow", and the
// details say why; a size within the limit still counts as taken.
func TestSpeedOnTheCard(t *testing.T) {
	var runs []store.Run
	for i := 0; i < 24; i++ {
		runs = append(runs,
			store.Run{Type: "skill:lookup", Tier: "small", Rung: "r1", Graded: true, Pass: true, Seconds: 3},
			store.Run{Type: "skill:lookup", Tier: "large", Rung: "r1", Graded: true, Pass: true, Seconds: 48},
		)
	}
	for i := 0; i < 5; i++ {
		runs = append(runs, store.Run{Type: "review", Tier: "large", Rung: "r1", Graded: true, Pass: i < 4, Seconds: 600, RefSeconds: 200})
	}
	claude := speed.Claude{BySize: map[string]speed.Sample{
		"small": {Seconds: 3, N: 100}, "large": {Seconds: 4, N: 100, From: "medium"},
	}}
	rows := WithSpeed(Build(runs, map[string]string{"r1": "Qwen"}), config.Speed{MaxSlower: 20}, claude)

	var buf bytes.Buffer
	Headline(&buf, rows)
	out := buf.String()
	for _, want := range []string{
		"Can take now:    lookup (files up to 200 lines)",
		"Too slow:        lookup over 1,000 lines (vs Claude: limit +20%)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("headline missing %q:\n%s", want, out)
		}
	}

	buf.Reset()
	Render(&buf, rows)
	out = buf.String()
	for _, want := range []string{"Claude (too slow)", "≈4s", "3.0× Claude's time (limit +20%)", "same jobs", "Speed (rule)"} {
		if !strings.Contains(out, want) {
			t.Errorf("details missing %q:\n%s", want, out)
		}
	}
}
