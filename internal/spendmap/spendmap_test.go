package spendmap

import (
	"bytes"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/pricing"
)

func TestBuild(t *testing.T) {
	prices, err := pricing.Default()
	if err != nil {
		t.Fatal(err)
	}
	mtok := pricing.Usage{Input: 1_000_000, Output: 1_000_000}
	h := &history.History{
		Jobs: []history.Job{
			{AgentType: "Explore", Description: "Find the router", Tools: map[string]int{"Bash": 2},
				Requests: []history.Request{{Model: "claude-opus-5-5", Usage: mtok}}},
			{AgentType: "Explore", Description: "Find tests", Tools: map[string]int{"Read": 1}, Background: true,
				Requests: []history.Request{{Model: "claude-sonnet-5-5", Usage: mtok}}},
			{AgentType: "general-purpose", Description: "Fix the bug", Tools: map[string]int{"Edit": 1},
				Requests: []history.Request{{Model: "claude-opus-5-5", Usage: mtok}, {Model: "mystery-model", Usage: mtok}}},
		},
		Main: []history.Request{{Model: "claude-opus-5-5", Usage: mtok}},
	}
	m := Build(h, prices, time.Time{}, 30)

	if m.Jobs != 3 || m.Unpriced != 1 {
		t.Fatalf("jobs=%d unpriced=%d", m.Jobs, m.Unpriced)
	}
	if len(m.Rows) != 2 || m.Rows[0].Type != "find" || m.Rows[1].Type != "edit" {
		t.Fatalf("rows = %+v", m.Rows)
	}
	find := m.Rows[0]
	// Opus 5.5 $24 + Sonnet 5.5 $12.
	if math.Abs(find.Cost-36) > 1e-9 {
		t.Errorf("find cost = %v, want 36", find.Cost)
	}
	// On Sonnet: 12 + 12 (the Sonnet job can't get cheaper on Sonnet).
	if math.Abs(find.OnSonnet-24) > 1e-9 {
		t.Errorf("find on Sonnet = %v, want 24", find.OnSonnet)
	}
	// On Haiku: 1M-token prompts are over Haiku 5.5's 100K tier, so 0.50 + 2.50 each.
	if math.Abs(find.OnHaiku-6) > 1e-9 {
		t.Errorf("find on Haiku = %v, want 6", find.OnHaiku)
	}
	if find.Replayable != 2 || find.Background != 1 || find.RanCheaper["sonnet"] != 1 {
		t.Errorf("find counts = %+v", find)
	}
	if m.Rows[1].Replayable != 0 {
		t.Error("edit jobs are never replayable")
	}
	if math.Abs(m.Share()-60.0/84.0) > 1e-9 {
		t.Errorf("share = %v", m.Share())
	}

	var buf bytes.Buffer
	m.Render(&buf)
	out := buf.String()
	for _, want := range []string{"3 subagent jobs", "find", "Opus 67% · Sonnet 33%", "already ran cheaper (1 on Sonnet)", "missing from the price table"} {
		if !strings.Contains(out, want) {
			t.Errorf("render output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderEmpty(t *testing.T) {
	var buf bytes.Buffer
	(&Map{Days: 30}).Render(&buf)
	if !strings.Contains(buf.String(), "No subagent jobs found") {
		t.Errorf("empty map output: %s", buf.String())
	}
}

// A sliver of replayable spend reads "under 1%", not "0%".
func TestRenderReplayableSliver(t *testing.T) {
	m := &Map{Days: 7, Jobs: 2, SubagentCost: 1000, Rows: []Row{
		{Type: "research", Jobs: 1, Replayable: 1, ReplayCost: 2, Cost: 2},
		{Type: "edit", Jobs: 1, Cost: 998},
	}}
	var buf bytes.Buffer
	m.Render(&buf)
	if !strings.Contains(buf.String(), "Gradable by replay: under 1% of subagent spend") {
		t.Errorf("got:\n%s", buf.String())
	}
}
