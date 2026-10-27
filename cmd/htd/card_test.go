package main

import (
	"bytes"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/pricing"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/speed"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDelegationSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "delegations.jsonl")
	os.WriteFile(path, []byte(`{"outcome":"answered","bytes":4000,"answer_bytes":400}
{"outcome":"refused"}
{"outcome":"refused"}
{"outcome":"error"}
not json
`), 0o644)
	var out bytes.Buffer
	delegationSummary(&out, path, speed.Claude{}, nil)
	if want := "ask_local: 1 task answered locally, 2 refused · about 900 tokens kept out of Claude's context\n"; !strings.Contains(out.String(), want) {
		t.Errorf("got %q, want it to contain %q (file bytes minus the answer, over 4)", out.String(), want)
	}

	out.Reset()
	delegationSummary(&out, filepath.Join(t.TempDir(), "none.jsonl"), speed.Claude{}, nil)
	if out.Len() != 0 {
		t.Errorf("no log, no line; got %q", out.String())
	}
}

// The saving is priced the way Claude would have paid: one 1-hour cache
// write, then a cache read on each later turn the file would have ridden
// along for. 99,900 tokens at Opus 5.5 ($8/M write, $0.20/M read) over 47
// turns: 99.9 × ($0.008 + $0.0094) = $1.74.
func TestDelegationSummaryPriced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "delegations.jsonl")
	os.WriteFile(path, []byte(`{"outcome":"answered","bytes":400000,"answer_bytes":400}`+"\n"), 0o644)
	prices, err := pricing.Default()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	delegationSummary(&out, path, speed.Claude{Model: "claude-opus-5-5", RideAlong: 47}, prices)
	if want := "about 99,900 tokens kept out of Claude's context · worth about $1.74 at Opus 5.5 API prices"; !strings.Contains(out.String(), want) {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}
