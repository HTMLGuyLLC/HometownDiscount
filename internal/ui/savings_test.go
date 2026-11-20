package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/delegate"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

// handoff is an answered ask_local call that kept tokens*4 bytes (plus the
// answer) out of Claude.
func handoff(at time.Time, model, skill string, tokens int) delegate.Delegation {
	return delegate.Delegation{At: at, Model: model, Skill: skill, Size: "small", Bytes: tokens*4 + 100, AnswerBytes: 100, Outcome: "answered"}
}

// What went where, and the pace it's going at.
func TestSavings(t *testing.T) {
	ds := []delegate.Delegation{
		handoff(now.AddDate(0, 0, -40), "acme/Big", "lookup", 1000), // before the pace window
		handoff(now.AddDate(0, 0, -10), "acme/Big", "lookup", 1000),
		handoff(now.AddDate(0, 0, -5), "acme/Small", "json", 500),
		handoff(now.AddDate(0, 0, -1), "", "json", 500), // logged before models were
		{At: now.AddDate(0, 0, -1), Skill: "files", Outcome: "refused", Reason: "slow"},
		{At: now.AddDate(0, 0, -1), Skill: "files", Outcome: "refused"}, // logged before reasons were
		{At: now.AddDate(0, 0, -1), Skill: "files", Outcome: "error"},
	}
	s := Build(Input{Delegations: ds, Claude: claude2, Now: now, Tracking: true, ReadsReady: true}).Savings
	if !s.Tracking || s.Answered != 4 || s.Refused != 2 || s.Failed != 1 || s.TokensKept != 3000 {
		t.Fatalf("totals: %+v", s)
	}
	if len(s.RefusedWhy) != 2 || s.RefusedWhy["slow"] != 1 || s.RefusedWhy["other"] != 1 {
		t.Errorf("why refused: %v", s.RefusedWhy)
	}
	if len(s.ByModel) != 3 || s.ByModel[0].Name != "Big" || s.ByModel[0].Tasks != 2 || s.ByModel[0].Tokens != 2000 {
		t.Errorf("by model: %+v", s.ByModel)
	}
	if s.ByModel[2].Name != "Small" && s.ByModel[1].Name != NotRecorded {
		t.Errorf("by model names: %+v", s.ByModel)
	}
	if len(s.ByTask) != 2 || s.ByTask[0].Tasks != 2 {
		t.Errorf("by task: %+v", s.ByTask)
	}
	// The log goes back 40 days, so the last 30 are a full month: 3 tasks, 2,000 tokens.
	if s.Pace == nil || s.Pace.Tasks != 3 || s.Pace.Tokens != 2000 || s.Pace.Days != 30 {
		t.Errorf("pace: %+v", s.Pace)
	}
	if !s.First.Equal(now.AddDate(0, 0, -40)) {
		t.Errorf("first: %v", s.First)
	}
}

// A short log is scaled to a month, but never from fewer than 7 days.
func TestSavingsPaceShortLog(t *testing.T) {
	ds := []delegate.Delegation{handoff(now.Add(-2*time.Hour), "m", "json", 700)}
	s := Build(Input{Delegations: ds, Now: now, Tracking: true}).Savings
	if s.Pace == nil || s.Pace.Days != 7 || s.Pace.Tokens != 3000 || s.Pace.Tasks != 4 {
		t.Errorf("pace: %+v", s.Pace)
	}
}

// Room to grow counts Claude's reads at sizes a model goes local for, and
// says which skills that assumes.
func TestRoomToGrow(t *testing.T) {
	runs := checks("skill:lookup", "small", 24, 24, 1, day1) // goes local, small files
	reads := []history.ReadSample{
		{Lines: 100, Bytes: 4600},  // small: counted
		{Lines: 150, Bytes: 4600},  // small: counted
		{Lines: 900, Bytes: 30000}, // medium: not proven
	}
	in := Input{Runs: runs, Speed: speeds, Claude: claude2, Now: now, Reads: reads, ReadsDays: 15, ReadsReady: true}
	s := Build(in).Savings
	if s.RoomStatus != "ready" || s.Room == nil {
		t.Fatalf("room: %+v", s)
	}
	// 2 reads of 1,000 tokens each (net of a 600-byte answer) in 15 days: 4 a month.
	if s.Room.Tasks != 4 || s.Room.Tokens != 4000 || s.Room.Skills != "Find a definition" || s.Room.Sizes != "files up to 200 lines" {
		t.Errorf("room: %+v", s.Room)
	}

	in.ReadsReady = false
	if s := Build(in).Savings; s.RoomStatus != "reading" || s.Room != nil {
		t.Errorf("while reading: %+v", s)
	}
	in.ReadsReady, in.ReadsErr = true, "permission denied"
	if s := Build(in).Savings; s.RoomStatus != "error" || !strings.Contains(s.RoomError, "permission denied") || s.Room != nil {
		t.Errorf("a failed scan: %+v", s)
	}
	in.ReadsErr, in.Runs = "", []store.Run{}
	if s := Build(in).Savings; s.RoomStatus != "none" || s.Room != nil {
		t.Errorf("nothing goes local: %+v", s)
	}
}

// Tokens are summed as bytes and divided once, the way `htd card` totals
// them, so three small calls don't each lose a rounding.
func TestSavingsTokensRoundOnce(t *testing.T) {
	var ds []delegate.Delegation
	for range 3 {
		ds = append(ds, delegate.Delegation{At: now, Model: "m", Skill: "json", Size: "small", Bytes: 603, AnswerBytes: 100, Outcome: "answered"}) // 503 bytes kept
	}
	rep := Build(Input{Delegations: ds, Now: now, Tracking: true})
	if want := delegate.Summarize(ds).Tokens(); rep.Savings.TokensKept != want || rep.Savings.ByModel[0].Tokens != want || want != 377 {
		t.Errorf("tokens %d (by model %d), want %d", rep.Savings.TokensKept, rep.Savings.ByModel[0].Tokens, want)
	}
}
