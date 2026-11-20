// Package ui is the scorecard page `htd ui` serves on this computer: each
// local model's results per task and size, how they got there, and what
// ask_local has handed off. It reads the same evidence as `htd card` and
// judges it the same way, so the two never disagree.
package ui

import (
	"sort"
	"strings"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/card"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/config"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/delegate"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/ladder"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/pricing"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/skills"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/speed"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

// Results a cell can have, in plain words on the page.
const (
	Local    = "local"    // proven and fast enough: goes local
	Slow     = "slow"     // proven, but Claude keeps it for speed (or a "never" setting)
	Learning = "learning" // not enough checks to decide yet
	NotGood  = "no"       // it clearly can't reach the bar
	NotRun   = "none"     // no checks at this size
)

// Limits that keep the page quick.
const (
	maxTrend    = 120 // points per trend line
	maxDays     = 60  // days on the over-time chart
	maxHandoffs = 50  // recent ask_local calls listed
)

// Input is everything a report is built from.
type Input struct {
	Runs        []store.Run // in the order they ran, as store.All returns them
	Speed       config.Speed
	Claude      speed.Claude
	Delegations []delegate.Delegation
	Prices      *pricing.Table // nil: no dollar figure
	Now         time.Time
	Theme       string // [ui] theme: auto, light or dark
	Tracking    bool   // [privacy] track_handoffs
	// ConfigError is why config.toml couldn't be read, in plain words; the
	// report then uses the defaults.
	ConfigError string
	// Reads are the files Claude read in the last ReadsDays days of
	// transcripts, for the most ask_local could take. ReadsReady is false
	// while they're still being read.
	Reads      []history.ReadSample
	ReadsDays  int
	ReadsReady bool
	ReadsErr   string // why the transcripts couldn't be read, "" when they could
}

// Report is the page's data, sent as JSON.
type Report struct {
	Generated   time.Time `json:"generated"`
	Theme       string    `json:"theme"`                  // auto (follow the computer), light or dark
	ConfigError string    `json:"config_error,omitempty"` // config.toml couldn't be read: these are the defaults
	Looks       []int     `json:"looks"`                  // the check-ins a learning cell heads for
	Totals      Totals    `json:"totals"`
	Models      []Model   `json:"models"`
	Days        []Day     `json:"days"`
	Handoffs    []Handoff `json:"handoffs"`
	Savings     Savings   `json:"savings"`
}

// Totals are the headline numbers.
type Totals struct {
	GoLocal int       `json:"go_local"` // task sizes that go local now, over every model
	Graded  int       `json:"graded"`   // checks and replays graded
	Models  int       `json:"models"`
	LastRun time.Time `json:"last_run,omitzero"`
}

// Model is one setup: a local model and how it runs.
type Model struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Detail      string         `json:"detail"`
	ThinkingOff bool           `json:"thinking_off"` // runs with thinking turned off
	Counts      map[string]int `json:"counts"`       // cells per result
	Skills      []Task         `json:"skills"`
	Replays     []Task         `json:"replays"`
}

// Task is one row: a skill check, or a replayed job type against one Claude
// model, with a cell per size.
type Task struct {
	Type  string `json:"type"` // as stored, e.g. "skill:lookup" or "find"
	Name  string `json:"name"` // in plain words
	Vs    string `json:"vs,omitempty"`
	Cells []Cell `json:"cells"`
}

// Cell is one task at one size.
type Cell struct {
	Size       string    `json:"size"`
	SizeWords  string    `json:"size_words"`
	Result     string    `json:"result"`
	Passed     int       `json:"passed"`
	Graded     int       `json:"graded"`
	Skipped    int       `json:"skipped,omitempty"`
	Next       int       `json:"next,omitempty"` // the check-in a learning cell is heading for
	Bar        float64   `json:"bar"`
	Secs       float64   `json:"secs,omitempty"`        // the local model's median time
	ClaudeSecs float64   `json:"claude_secs,omitempty"` // skill checks: Claude's typical time
	Speed      string    `json:"speed"`                 // ok, slow or unknown
	PaceWords  string    `json:"pace_words"`            // e.g. "2.9× as long as Claude"
	LimitWords string    `json:"limit_words"`           // e.g. "speed limit: up to 20% slower"
	NeverLocal bool      `json:"never_local"`           // your settings keep it on Claude
	Last       time.Time `json:"last,omitzero"`
	Trend      []Point   `json:"trend,omitempty"`
}

// Point is a cell's standing after its Nth graded check.
type Point struct {
	N     int       `json:"n"`
	Rate  float64   `json:"rate"`  // share right so far
	Lower float64   `json:"lower"` // the proof line so far
	At    time.Time `json:"at"`
}

// Day is one day on the over-time chart.
type Day struct {
	Date     string         `json:"date"` // YYYY-MM-DD, local time
	Checks   int            `json:"checks"`
	Handoffs int            `json:"handoffs"`
	Local    map[string]int `json:"local"` // model id -> task sizes that went local by the day's end
}

// Handoff is one ask_local call.
type Handoff struct {
	At         time.Time `json:"at"`
	Model      string    `json:"model,omitempty"`
	Task       string    `json:"task"`
	Size       string    `json:"size"`
	Lines      int       `json:"lines"`
	Secs       float64   `json:"secs"`
	Outcome    string    `json:"outcome"`
	TokensKept int       `json:"tokens_kept,omitempty"`
}

// Build turns the evidence into the page's data.
func Build(in Input) Report {
	if in.Now.IsZero() {
		in.Now = time.Now()
	}
	rows := card.WithSpeed(card.Build(in.Runs, nil), in.Speed, in.Claude)
	if in.Theme == "" {
		in.Theme = "auto"
	}
	rep := Report{Generated: in.Now, Theme: in.Theme, ConfigError: in.ConfigError, Looks: ladder.Looks}
	rep.Models = models(rows, trends(in.Runs))
	rep.Days = days(in)
	rep.Handoffs = handoffs(in.Delegations)
	rep.Savings = savings(in, rows)

	rep.Totals.Models = len(rep.Models)
	for _, r := range rows {
		rep.Totals.Graded += r.Graded
		if result(r) == Local {
			rep.Totals.GoLocal++
		}
	}
	if n := len(in.Runs); n > 0 {
		rep.Totals.LastRun = in.Runs[n-1].At
	}
	return rep
}

// result is a row's plain answer, judged as the card's headline judges it.
func result(r card.Row) string {
	switch {
	case r.State == ladder.Proven && r.Speed.Kept:
		return Slow
	case r.State == ladder.Proven:
		return Local
	case r.State == ladder.NotEnough || r.State == ladder.NotProven:
		return NotGood
	case r.Graded > 0:
		return Learning
	}
	return NotRun
}

// nextLook is the check-in a learning cell is heading for, 0 past the last.
func nextLook(graded int) int {
	for _, l := range ladder.Looks {
		if l > graded {
			return l
		}
	}
	return 0
}

type trend struct {
	points []Point
	last   time.Time
}

// trends follows each group check by check: share right and the proof line
// after each graded run, thinned to maxTrend points (the last one kept).
func trends(runs []store.Run) map[card.GroupKey]trend {
	all := map[card.GroupKey][]Point{}
	last := map[card.GroupKey]time.Time{}
	passed := map[card.GroupKey]int{}
	for _, r := range runs {
		k := card.KeyOf(r)
		if r.At.After(last[k]) {
			last[k] = r.At
		}
		if !r.Graded {
			continue
		}
		if r.Pass {
			passed[k]++
		}
		n := len(all[k]) + 1
		all[k] = append(all[k], Point{N: n, Rate: float64(passed[k]) / float64(n), Lower: ladder.WilsonLower(passed[k], n), At: r.At})
	}
	out := map[card.GroupKey]trend{}
	for k, t := range last {
		out[k] = trend{points: thin(all[k], maxTrend), last: t}
	}
	return out
}

// thin keeps at most max points, evenly spread, always with the last.
func thin(p []Point, max int) []Point {
	if len(p) <= max {
		return p
	}
	out := make([]Point, 0, max)
	step := float64(len(p)-1) / float64(max-1)
	for i := 0; i < max; i++ {
		out = append(out, p[int(float64(i)*step+0.5)])
	}
	out[max-1] = p[len(p)-1]
	return out
}

// models arranges rows by setup, then by task with a cell per size.
func models(rows []card.Row, tr map[card.GroupKey]trend) []Model {
	out := []Model{}
	index := map[string]int{}
	taskIdx := map[[2]string]int{} // setup + type|ref -> index in Skills or Replays
	for _, r := range rows {
		i, ok := index[r.Rung]
		if !ok {
			i = len(out)
			index[r.Rung] = i
			name, detail := setupWords(r.Rung)
			out = append(out, Model{ID: r.Rung, Name: name, Detail: detail, ThinkingOff: thinkingOff(r.Rung), Counts: map[string]int{}, Skills: []Task{}, Replays: []Task{}})
		}
		m := &out[i]
		_, isSkill := strings.CutPrefix(r.Type, skills.TypePrefix)
		list := &m.Replays
		if isSkill {
			list = &m.Skills
		}
		tk := [2]string{r.Rung, r.Type + "|" + r.Reference}
		ti, ok := taskIdx[tk]
		if !ok {
			ti = len(*list)
			taskIdx[tk] = ti
			t := Task{Type: r.Type, Name: taskName(r.Type)}
			if r.Reference != "" {
				t.Vs = pricing.Display(r.Reference)
			}
			for _, size := range sizesFor(isSkill, r.Tier) {
				t.Cells = append(t.Cells, Cell{Size: size, SizeWords: sizeWords(isSkill, size), Result: NotRun, Speed: "unknown"})
			}
			*list = append(*list, t)
		}
		c := cellFrom(r, tr[card.KeyOf(store.Run{Rung: r.Rung, Type: r.Type, Tier: r.Tier, Reference: r.Reference})])
		m.Counts[c.Result]++
		task := &(*list)[ti]
		placed := false
		for j := range task.Cells {
			if task.Cells[j].Size == c.Size {
				task.Cells[j], placed = c, true
			}
		}
		if !placed { // a size outside the usual set, e.g. runs from before sizes
			task.Cells = append(task.Cells, c)
		}
	}
	return out
}

func cellFrom(r card.Row, t trend) Cell {
	isSkill := strings.HasPrefix(r.Type, skills.TypePrefix)
	size := r.Tier
	if size == "" {
		size = "any"
	}
	c := Cell{
		Size: size, SizeWords: sizeWords(isSkill, size), Result: result(r),
		Passed: r.Passed, Graded: r.Graded, Skipped: r.Skipped, Bar: r.Bar,
		Secs: r.MedianSecs, ClaudeSecs: r.ClaudeSecs,
		PaceWords: paceWords(r.Speed), LimitWords: limitWords(r.Speed.Rule), NeverLocal: r.Speed.Rule.Mode == "never",
		Speed: "unknown",
		Last:  t.last, Trend: t.points,
	}
	switch {
	case r.Speed.Kept:
		c.Speed = "slow"
	case r.Speed.Compared:
		c.Speed = "ok"
	}
	if c.Result == Learning {
		c.Next = nextLook(r.Graded)
	}
	return c
}

// days is the over-time chart: per day with runs or handoffs, the checks
// graded that day, the handoffs, and each setup's task sizes that went local
// by the day's end (judged with today's speed settings).
func days(in Input) []Day {
	loc := in.Now.Location()
	dates := map[string]bool{}
	checks, hand := map[string]int{}, map[string]int{}
	for _, r := range in.Runs {
		d := r.At.In(loc).Format(time.DateOnly)
		dates[d] = true
		if r.Graded {
			checks[d]++
		}
	}
	for _, d := range in.Delegations {
		if d.At.IsZero() {
			continue
		}
		day := d.At.In(loc).Format(time.DateOnly)
		dates[day] = true
		if d.Outcome == "answered" {
			hand[day]++
		}
	}
	if len(dates) == 0 {
		return []Day{}
	}
	// Every day from the one before the first result (when nothing went
	// local yet) to today, so the chart's spacing is true to time.
	var seen []string
	for d := range dates {
		seen = append(seen, d)
	}
	sort.Strings(seen)
	// Days are stepped at noon, not midnight: where the clocks change at
	// midnight (Santiago, Havana) a midnight-to-midnight step lands on the
	// same date twice or skips one.
	first := noon(seen[0], loc)
	today := noon(in.Now.In(loc).Format(time.DateOnly), loc)
	if last := noon(seen[len(seen)-1], loc); last.After(today) {
		today = last
	}
	var order []string
	for d := first.AddDate(0, 0, -1); !d.After(today); d = d.AddDate(0, 0, 1) {
		order = append(order, d.Format(time.DateOnly))
	}
	if len(order) > maxDays {
		order = order[len(order)-maxDays:]
	}
	out := make([]Day, 0, len(order))
	upTo, local := -1, map[string]int{}
	for _, d := range order {
		day := noon(d, loc)
		end := time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, loc)
		n := 0 // runs are in time order: the ones before the day's end
		for n < len(in.Runs) && in.Runs[n].At.Before(end) {
			n++
		}
		if n != upTo { // a day without new results keeps the day before's
			upTo, local = n, map[string]int{}
			for _, r := range card.WithSpeed(card.Build(in.Runs[:n], nil), in.Speed, in.Claude) {
				if result(r) == Local {
					local[r.Rung]++
				}
			}
		}
		out = append(out, Day{Date: d, Checks: checks[d], Handoffs: hand[d], Local: local})
	}
	return out
}

// noon is the middle of a YYYY-MM-DD day in loc.
func noon(date string, loc *time.Location) time.Time {
	t, _ := time.Parse(time.DateOnly, date)
	return time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, loc)
}

// handoffs are the latest ask_local calls, newest first.
func handoffs(ds []delegate.Delegation) []Handoff {
	out := []Handoff{}
	for i := len(ds) - 1; i >= 0 && len(out) < maxHandoffs; i-- {
		d := ds[i]
		out = append(out, Handoff{
			At: d.At, Model: shortModel(d.Model), Task: taskName(skills.TypePrefix + d.Skill), Size: d.Size, Lines: d.Lines,
			Secs: d.Seconds, Outcome: d.Outcome, TokensKept: d.Kept() / 4,
		})
	}
	return out
}
