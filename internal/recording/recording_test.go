package recording

import (
	"testing"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/goldset"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/grade"
)

func TestRecording(t *testing.T) {
	calls := []goldset.WebCall{
		{Tool: "WebFetch", URL: "https://www.Example.com/docs/?utm_source=x", Result: "Version 2.4 raised the limit to 512 MB."},
		{Tool: "WebSearch", Query: "go sqlite driver comparison", Result: "1. https://pkg.go.dev/modernc.org/sqlite - pure Go\n2. https://github.com/mattn/go-sqlite3."},
	}
	r := New(calls)
	var _ grade.Recording = r

	if p, ok := r.Fetch("https://example.com/docs", ""); !ok || p == "" {
		t.Error("normalized fetch should hit the recording")
	}
	if _, ok := r.Fetch("https://example.com/other", ""); ok {
		t.Error("unrecorded page should miss")
	}
	if res, ok := r.Search("sqlite driver for go"); !ok || res == "" {
		t.Error("a close query should return the recorded results")
	}
	if _, ok := r.Search("rust web frameworks"); ok {
		t.Error("an unrelated query should return nothing")
	}
	if !r.HasURL(grade.NormalizeURL("https://github.com/mattn/go-sqlite3")) {
		t.Error("URLs listed in search results count as recorded (trailing period stripped)")
	}
	if !r.HasURL(grade.NormalizeURL("https://example.com/")) || !r.HasURL(grade.NormalizeURL("https://github.com")) {
		t.Error("the homepage of a recorded site counts as recorded")
	}
	if r.HasURL(grade.NormalizeURL("https://example.com/pricing")) || r.HasURL(grade.NormalizeURL("https://other.com")) {
		t.Error("an invented page on a recorded site, or another site, doesn't")
	}
	if !r.PagesContain("512 mb") || r.PagesContain("1024 MB") {
		t.Error("PagesContain wrong")
	}
	if !Complete(calls) || Complete(nil) || Complete([]goldset.WebCall{{Tool: "WebFetch"}}) {
		t.Error("Complete wrong")
	}
}
