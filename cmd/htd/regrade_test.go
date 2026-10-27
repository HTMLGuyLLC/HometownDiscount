package main

import (
	"testing"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/grade"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/jobtype"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/schedule"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
	"strings"
)

func TestGradable(t *testing.T) {
	if why := gradable(jobtype.Find, "The config lives in `web/config.ts:12`.", nil, nil); why != "" {
		t.Errorf("a reference with entities is gradable, got %q", why)
	}
	if why := gradable(jobtype.Find, "I looked around and it seems fine.", nil, nil); why != "not gradable (no-entities)" {
		t.Errorf("got %q", why)
	}
	if why := gradable("edit", "anything", nil, nil); why != "not gradable" {
		t.Errorf("a type with no grader: got %q", why)
	}
}

func TestApplyGradeAndLabel(t *testing.T) {
	r := store.Run{Graded: true, Reason: "recall"}
	applyGrade(&r, grade.Result{Graded: true, Fabricated: []string{"missing path: a.go"}})
	if r.Pass || !r.Severe || r.Reason != "made up: missing path: a.go" || gradeLabel(r) != "fail (made up: missing path: a.go)" {
		t.Errorf("run = %+v, label %q", r, gradeLabel(r))
	}
	applyGrade(&r, grade.Result{Reason: "low-anchor"})
	if r.Graded || gradeLabel(r) != "not graded (low-anchor)" {
		t.Errorf("label = %q", gradeLabel(r))
	}
}

// A regrade that only changes what was made up still saves, so the details
// shown later match the current graders.
func TestSameGrade(t *testing.T) {
	a := store.Run{Graded: true, Reason: "fabrication", Recall: 0.9, Severe: true, Made: []string{"URL not in the recording: https://example.com", "URL not in the recording: https://example.org/x"}}
	b := a
	if !sameGrade(a, b) {
		t.Error("identical grades should match")
	}
	b.Made = a.Made[1:]
	if sameGrade(a, b) {
		t.Error("a shorter made-up list is a change")
	}
	b = a
	b.Extra = 2
	if sameGrade(a, b) {
		t.Error("a different extra count is a change")
	}
}

// Replays are scheduled per job type and Claude model, keeping the usual
// type order.
func TestReplayGroupsByClaudeModel(t *testing.T) {
	if k := refKey("review", "claude-opus-5-5[1m]"); k != "review|claude-opus-5-5" {
		t.Errorf("refKey = %q", k)
	}
	if typ, model := splitRef("review|claude-opus-5-5"); typ != "review" || model != "claude-opus-5-5" {
		t.Errorf("splitRef = %q, %q", typ, model)
	}
	if l := refLabel("review|claude-sonnet-5"); l != "review vs Sonnet 5" {
		t.Errorf("refLabel = %q", l)
	}
	jobs := []schedule.Job{{ID: "1", Type: "review|claude-sonnet-5"}, {ID: "2", Type: "find|claude-opus-5-5"}}
	prior := map[schedule.Key][]bool{{Type: "review|claude-opus-5-5", Tier: "small"}: {true}}
	got := replayOrder(jobs, prior).Types
	want := []string{"find|claude-opus-5-5", "review|claude-opus-5-5", "review|claude-sonnet-5"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}
}
