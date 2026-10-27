package main

import (
	"testing"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/grade"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/jobtype"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
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
