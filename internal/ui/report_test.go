package ui

import (
	"fmt"
	"testing"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/config"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/delegate"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/speed"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

var (
	rung    = store.Rung{Model: "acme/Tiny-7B", Quant: "q4", Loop: "builtin", Effort: store.ThinkingOff}
	day1    = time.Date(2026, 10, 27, 9, 0, 0, 0, time.Local)
	now     = time.Date(2026, 10, 30, 12, 0, 0, 0, time.Local)
	speeds  = config.Speed{MaxSlower: 20}
	claude2 = speed.Claude{Model: "claude-opus-5-5", BySize: map[string]speed.Sample{"small": {Seconds: 2, N: 30}, "medium": {Seconds: 2, N: 30}}}
)

// checks is n skill checks of one kind and size, the first `passes`
// passing, each taking secs, one a minute from start.
func checks(typ, tier string, n, passes int, secs float64, start time.Time) []store.Run {
	var runs []store.Run
	for i := 0; i < n; i++ {
		runs = append(runs, store.Run{
			JobID: fmt.Sprintf("%s-%s-%d", typ, tier, i), Type: typ, Tier: tier, Rung: rung.ID(),
			Graded: true, Pass: i < passes, Seconds: secs, At: start.Add(time.Duration(i) * time.Minute),
		})
	}
	return runs
}

func cell(t *testing.T, m Model, name, size string) Cell {
	t.Helper()
	for _, task := range append(m.Skills, m.Replays...) {
		if task.Name != name {
			continue
		}
		for _, c := range task.Cells {
			if c.Size == size {
				return c
			}
		}
	}
	t.Fatalf("no %s cell at %s", name, size)
	return Cell{}
}

// Each cell gets the plain result the card's headline would give it.
func TestBuildResults(t *testing.T) {
	var runs []store.Run
	runs = append(runs, checks("skill:lookup", "small", 24, 24, 1, day1)...)                  // proven, faster than Claude
	runs = append(runs, checks("skill:lookup", "medium", 24, 24, 10, day1.Add(time.Hour))...) // proven, 5× Claude's time
	runs = append(runs, checks("skill:lines", "small", 10, 9, 1, day1.Add(2*time.Hour))...)   // too few to decide
	runs = append(runs, checks("skill:json", "small", 5, 0, 1, day1.Add(3*time.Hour))...)     // clearly can't
	runs = append(runs, store.Run{JobID: "skip", Type: "skill:json", Tier: "small", Rung: rung.ID(), At: day1.Add(4 * time.Hour)})
	rep := Build(Input{Runs: runs, Speed: speeds, Claude: claude2, Now: now})

	if len(rep.Models) != 1 {
		t.Fatalf("models: %+v", rep.Models)
	}
	m := rep.Models[0]
	if m.Name != "Tiny-7B" || m.Detail != "acme · q4 · builtin loop · thinking off" {
		t.Errorf("name %q, detail %q", m.Name, m.Detail)
	}
	if c := cell(t, m, "Find a definition", "small"); c.Result != Local || c.Speed != "ok" || c.Passed != 24 || c.Graded != 24 {
		t.Errorf("small lookup: %+v", c)
	}
	if c := cell(t, m, "Find a definition", "medium"); c.Result != Slow || c.Speed != "slow" || c.PaceWords != "5.0× as long as Claude" || c.LimitWords != "speed limit: up to 20% slower" || c.NeverLocal {
		t.Errorf("medium lookup: %+v", c)
	}
	if c := cell(t, m, "Find a definition", "large"); c.Result != NotRun {
		t.Errorf("large lookup was never run: %+v", c)
	}
	if c := cell(t, m, "Find matching lines", "small"); c.Result != Learning || c.Next != 24 || c.Passed != 9 {
		t.Errorf("lines: %+v", c)
	}
	if c := cell(t, m, "Read a JSON value", "small"); c.Result != NotGood || c.Skipped != 1 {
		t.Errorf("json: %+v", c)
	}
	if m.Counts[Local] != 1 || m.Counts[Slow] != 1 || m.Counts[Learning] != 1 || m.Counts[NotGood] != 1 {
		t.Errorf("counts: %v", m.Counts)
	}
	if rep.Totals.GoLocal != 1 || rep.Totals.Graded != 63 || rep.Totals.Models != 1 {
		t.Errorf("totals: %+v", rep.Totals)
	}
	if !rep.Totals.LastRun.Equal(day1.Add(4 * time.Hour)) {
		t.Errorf("last run %v", rep.Totals.LastRun)
	}
}

// Replays are rows per job type and Claude model, sized small and large.
func TestBuildReplays(t *testing.T) {
	runs := []store.Run{
		{JobID: "a", Type: "find", Tier: "large", Reference: "claude-sonnet-5", Rung: rung.ID(), Graded: true, Pass: true, Seconds: 60, RefSeconds: 30, At: day1},
		{JobID: "b", Type: "find", Tier: "small", Reference: "claude-opus-5-5", Rung: rung.ID(), Graded: true, Pass: false, Seconds: 60, At: day1},
	}
	m := Build(Input{Runs: runs, Speed: speeds, Now: now}).Models[0]
	if len(m.Skills) != 0 || len(m.Replays) != 2 {
		t.Fatalf("rows: %+v", m)
	}
	for _, task := range m.Replays {
		if task.Name != "Find jobs" || len(task.Cells) != 2 || task.Cells[0].Size != "small" || task.Cells[1].Size != "large" {
			t.Errorf("replay row: %+v", task)
		}
	}
	if vs := []string{m.Replays[0].Vs, m.Replays[1].Vs}; vs[0] == vs[1] {
		t.Errorf("one row per Claude model: %v", vs)
	}
}

// The trend follows a cell check by check, and is thinned on long runs.
func TestTrend(t *testing.T) {
	runs := checks("skill:list", "small", 4, 3, 1, day1) // pass, pass, pass, fail
	c := cell(t, Build(Input{Runs: runs, Speed: speeds, Now: now}).Models[0], "List what a file declares", "small")
	if len(c.Trend) != 4 {
		t.Fatalf("trend: %+v", c.Trend)
	}
	if last := c.Trend[3]; last.N != 4 || last.Rate != 0.75 || last.Lower <= 0 || last.Lower >= 0.75 {
		t.Errorf("last point: %+v", last)
	}
	if !c.Last.Equal(day1.Add(3 * time.Minute)) {
		t.Errorf("last check %v", c.Last)
	}

	long := checks("skill:list", "small", 500, 500, 1, day1)
	c = cell(t, Build(Input{Runs: long, Speed: speeds, Now: now}).Models[0], "List what a file declares", "small")
	if len(c.Trend) != maxTrend || c.Trend[0].N != 1 || c.Trend[maxTrend-1].N != 500 {
		t.Errorf("thinned: %d points, first %d, last %d", len(c.Trend), c.Trend[0].N, c.Trend[len(c.Trend)-1].N)
	}
}

func TestNextLook(t *testing.T) {
	for graded, want := range map[int]int{0: 24, 23: 24, 24: 37, 80: 100, 199: 200, 200: 0} {
		if got := nextLook(graded); got != want {
			t.Errorf("nextLook(%d) = %d, want %d", graded, got, want)
		}
	}
}

// Days run from the day before the first result to today, with no gaps,
// and count what went local by each day's end.
func TestDays(t *testing.T) {
	runs := checks("skill:lookup", "small", 24, 24, 1, day1.Add(24*time.Hour)) // proven on Oct 28
	ds := []delegate.Delegation{{At: day1.Add(48 * time.Hour), Outcome: "answered", Bytes: 4600, AnswerBytes: 600}}
	days := Build(Input{Runs: runs, Speed: speeds, Claude: claude2, Delegations: ds, Now: now}).Days
	var dates []string
	for _, d := range days {
		dates = append(dates, d.Date)
	}
	if want := "[2026-10-27 2026-10-28 2026-10-29 2026-10-30]"; fmt.Sprint(dates) != want {
		t.Fatalf("dates %v, want %s", dates, want)
	}
	id := rung.ID()
	if days[0].Local[id] != 0 || days[1].Local[id] != 1 || days[3].Local[id] != 1 {
		t.Errorf("local by day: %+v", days)
	}
	if days[1].Checks != 24 || days[2].Handoffs != 1 {
		t.Errorf("checks and handoffs: %+v", days)
	}
	if got := Build(Input{Now: now}).Days; len(got) != 0 {
		t.Errorf("nothing stored, no days: %+v", got)
	}
}

// Handoffs are newest first; totals price what was kept out of Claude.
func TestHandoffs(t *testing.T) {
	ds := []delegate.Delegation{
		{At: day1, Skill: "json", Size: "small", Lines: 93, Bytes: 4600, AnswerBytes: 600, Seconds: 3.5, Outcome: "answered"},
		{At: day1.Add(time.Hour), Skill: "lookup", Size: "large", Outcome: "refused"},
	}
	rep := Build(Input{Delegations: ds, Now: now})
	if len(rep.Handoffs) != 2 || rep.Handoffs[0].Outcome != "refused" || rep.Handoffs[1].Task != "Read a JSON value" || rep.Handoffs[1].TokensKept != 1000 {
		t.Errorf("handoffs: %+v", rep.Handoffs)
	}
	if s := rep.Savings; s.Answered != 1 || s.Refused != 1 || s.TokensKept != 1000 || s.Worth != 0 {
		t.Errorf("savings (no prices, no dollar figure): %+v", s)
	}
}

// An empty store is a report with empty lists, never nulls the page trips on.
func TestBuildEmpty(t *testing.T) {
	rep := Build(Input{})
	if rep.Models == nil || rep.Days == nil || rep.Handoffs == nil || rep.Generated.IsZero() || rep.Theme != "auto" || len(rep.Looks) != 7 || rep.Looks[0] != 24 || rep.ConfigError != "" {
		t.Errorf("empty report: %+v", rep)
	}
}

func TestSetupWords(t *testing.T) {
	for id, want := range map[string][2]string{
		store.Rung{Model: "big", Loop: "builtin"}.ID():                          {"big", "builtin loop"},
		store.Rung{Model: "o/m", Quant: "8bit", Loop: "x", Effort: "high"}.ID(): {"m", "o · 8bit · x loop · effort high"},
		"not json": {"not json", ""},
	} {
		if n, d := setupWords(id); n != want[0] || d != want[1] {
			t.Errorf("setupWords(%s) = %q, %q; want %q", id, n, d, want)
		}
	}
}

func TestPaceAndLimitWords(t *testing.T) {
	v := func(ratio float64, rule speed.Rule) speed.Verdict { return speed.Decide(rule, ratio, true) }
	limit := speed.Rule{Mode: "limit", MaxSlower: 20, From: "default"}
	for _, c := range []struct {
		v    speed.Verdict
		want string
	}{
		{speed.Decide(limit, 0, false), "speed not compared yet"},
		{v(0.5, limit), "half Claude's time"},
		{v(0.7, limit), "30% faster than Claude"},
		{v(1, limit), "as fast as Claude"},
		{v(1.3, limit), "30% slower than Claude"},
		{v(2.9, limit), "2.9× as long as Claude"},
	} {
		if got := paceWords(c.v); got != c.want {
			t.Errorf("paceWords(%+v) = %q, want %q", c.v, got, c.want)
		}
	}
	for rule, want := range map[speed.Rule]string{
		limit:                             "speed limit: up to 20% slower",
		{Mode: "never", From: "lookup"}:   "your setting: never local",
		{Mode: "always", From: "lookup"}:  "your setting: always local",
		{Mode: "always", From: "default"}: "speed check is off",
	} {
		if got := limitWords(rule); got != want {
			t.Errorf("limitWords(%+v) = %q, want %q", rule, got, want)
		}
	}
}

// A never rule is flagged so the page can say why it stays with Claude.
func TestNeverLocalCell(t *testing.T) {
	runs := checks("skill:lookup", "small", 24, 24, 1, day1)
	rep := Build(Input{Runs: runs, Speed: config.Speed{MaxSlower: 20, ByTask: map[string]any{"lookup": "never"}}, Claude: claude2, Now: now})
	c := cell(t, rep.Models[0], "Find a definition", "small")
	if c.Result != Slow || !c.NeverLocal || c.LimitWords != "your setting: never local" {
		t.Errorf("%+v", c)
	}
	if !rep.Models[0].ThinkingOff {
		t.Error("the test setup runs with thinking off")
	}
}

// Where the clocks change at midnight, every date shows once and today is
// the last one.
func TestDaysAcrossMidnightDST(t *testing.T) {
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Skip("no time zone data")
	}
	// Chile's clocks skip midnight on the first Sunday of September.
	start := time.Date(2026, 9, 1, 10, 0, 0, 0, loc)
	today := time.Date(2026, 9, 12, 15, 0, 0, 0, loc)
	var runs []store.Run
	for i := 0; i < 3; i++ {
		runs = append(runs, checks("skill:lookup", "small", 2, 2, 1, start.AddDate(0, 0, 4*i))...)
	}
	got := Build(Input{Runs: runs, Speed: speeds, Claude: claude2, Now: today}).Days
	seen := map[string]bool{}
	for _, d := range got {
		if seen[d.Date] {
			t.Errorf("%s twice", d.Date)
		}
		seen[d.Date] = true
	}
	if len(got) != 13 || got[0].Date != "2026-08-31" || got[len(got)-1].Date != "2026-09-12" || !seen["2026-09-06"] || !seen["2026-09-07"] {
		t.Errorf("%d days, first %s, last %s", len(got), got[0].Date, got[len(got)-1].Date)
	}
}
