// Package card summarizes replay evidence per setup and job type: how many
// jobs were graded, how many passed, and whether the rung has earned the type.
package card

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/jobtype"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/ladder"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/skills"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

// Row is one rung × job type.
type Row struct {
	Rung      string
	Type      string
	Tier      string // small, large, or "" for runs from before tiers
	Graded    int
	Passed    int
	Skipped   int // replays that couldn't be graded
	Lower     float64
	Bar       float64
	BarSource string
	State     ladder.State
	AvgSecs   float64
	AvgExtra  float64
}

// Label is the plain word the user sees: proven, learning, hint or off.
func (r Row) Label() string {
	switch r.State {
	case ladder.Proven:
		return "proven"
	case ladder.NotEnough:
		return "not good enough"
	case ladder.NotProven:
		return "hint"
	}
	if r.Graded == 0 {
		return "off"
	}
	return "learning"
}

// Build groups runs by rung and job type and evaluates each group.
func Build(runs []store.Run, labels map[string]string) []Row {
	type key struct{ rung, typ, tier string }
	groups := map[key][]store.Run{}
	for _, r := range runs {
		k := key{r.Rung, r.Type, r.Tier}
		groups[k] = append(groups[k], r)
	}
	var rows []Row
	for k, rs := range groups {
		row := Row{Rung: k.rung, Type: k.typ, Tier: k.tier, Bar: ladder.Bar(ladder.DefaultST, ladder.DefaultMargin), BarSource: "default"}
		if strings.HasPrefix(k.typ, skills.TypePrefix) {
			row.Bar, row.BarSource = skills.Bar, "fixed"
		}
		if l, ok := labels[k.rung]; ok {
			row.Rung = l
		}
		var outcomes []bool
		var secs, extra float64
		for _, r := range rs {
			if !r.Graded {
				row.Skipped++
				continue
			}
			outcomes = append(outcomes, r.Pass)
			row.Graded++
			if r.Pass {
				row.Passed++
			}
			secs += r.Seconds
			extra += float64(r.Extra)
		}
		if row.Graded > 0 {
			row.Lower = ladder.WilsonLower(row.Passed, row.Graded)
			row.AvgSecs = secs / float64(row.Graded)
			row.AvgExtra = extra / float64(row.Graded)
		}
		row.State, _ = ladder.Evaluate(outcomes, row.Bar)
		rows = append(rows, row)
	}
	order := map[string]int{}
	for i, t := range jobtype.Order {
		order[t] = i
	}
	for i, t := range skills.Order {
		order[skills.TypePrefix+t] = len(jobtype.Order) + i
	}
	sort.Slice(rows, func(i, k int) bool {
		if rows[i].Rung != rows[k].Rung {
			return rows[i].Rung < rows[k].Rung
		}
		if rows[i].Type != rows[k].Type {
			return order[rows[i].Type] < order[rows[k].Type]
		}
		return tierOrder(rows[i].Tier) < tierOrder(rows[k].Tier)
	})
	return rows
}

func tierOrder(t string) int {
	switch t {
	case "small":
		return 0
	case "medium":
		return 1
	case "large":
		return 2
	}
	return 3
}

// Headline writes the short version: per setup, what the model can take now,
// what it hasn't proven yet and what it isn't good enough for.
func Headline(w io.Writer, rows []Row) {
	fmt.Fprintln(w, "Hometown Discount · what your local model has earned")
	fmt.Fprintln(w)
	if len(rows) == 0 {
		fmt.Fprintln(w, "Nothing tested yet. Run `htd bench` in a repo (minutes), then `htd replay` (overnight).")
		return
	}
	var rungs []string
	byRung := map[string][]Row{}
	for _, r := range rows {
		if _, ok := byRung[r.Rung]; !ok {
			rungs = append(rungs, r.Rung)
		}
		byRung[r.Rung] = append(byRung[r.Rung], r)
	}
	anyLearning := false
	for _, rung := range rungs {
		fmt.Fprintln(w, rung)
		var can, learning, cannot []string
		best := map[string]Row{}
		var order []string
		for _, r := range byRung[rung] {
			switch r.State {
			case ladder.Proven:
				if b, ok := best[r.Type]; !ok || tierOrder(r.Tier) > tierOrder(b.Tier) {
					if !ok {
						order = append(order, r.Type)
					}
					best[r.Type] = r
				}
			case ladder.NotEnough, ladder.NotProven:
				cannot = append(cannot, fmt.Sprintf("%s (%s, %d of %d passed)", name(r.Type), sizeWords(r), r.Passed, r.Graded))
			default:
				if r.Graded > 0 {
					learning = append(learning, fmt.Sprintf("%s (%s, %d of %d passed)", name(r.Type), sizeWords(r), r.Passed, r.Graded))
				}
			}
		}
		for _, t := range order {
			can = append(can, fmt.Sprintf("%s (%s)", name(t), sizeWords(best[t])))
		}
		line := func(label string, items []string) {
			if len(items) > 0 {
				fmt.Fprintf(w, "  %-16s %s\n", label, strings.Join(items, " · "))
			}
		}
		if len(can) == 0 {
			can = []string{"nothing proven yet"}
		}
		line("Can take now:", can)
		line("Not proven yet:", learning)
		line("Not good enough:", cannot)
		fmt.Fprintln(w)
		anyLearning = anyLearning || len(learning) > 0
	}
	if anyLearning {
		fmt.Fprintln(w, "To prove more: run `htd bench` again or in another repo (minutes), or `htd replay` (overnight).")
		fmt.Fprintln(w)
	}
}

// name is a row type in plain words: skills by name, replays as "jobs".
func name(typ string) string {
	if s, ok := strings.CutPrefix(typ, skills.TypePrefix); ok {
		return s
	}
	return typ + " jobs"
}

func sizeWords(r Row) string {
	if strings.HasPrefix(r.Type, skills.TypePrefix) {
		switch r.Tier {
		case "small":
			return "files up to 200 lines"
		case "medium":
			return "up to 1,000 lines"
		case "large":
			return "up to 4,000 lines"
		}
	}
	switch r.Tier {
	case "small":
		return "small"
	case "large":
		return "large"
	}
	return "any size"
}

// Render writes the full card with every number.
func Render(w io.Writer, rows []Row) {
	fmt.Fprintln(w, "Hometown Discount · card · all the numbers")
	fmt.Fprintln(w)
	if len(rows) == 0 {
		fmt.Fprintln(w, "Nothing tested yet. Run `htd bench` or `htd replay` with a local model to start.")
		return
	}
	rung := ""
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, r := range rows {
		if r.Rung != rung {
			if rung != "" {
				fmt.Fprintln(tw, "\t\t\t\t\t\t")
			}
			rung = r.Rung
			fmt.Fprintf(tw, "%s\n", r.Rung)
			fmt.Fprintln(tw, "  Job type\tSize\tStatus\tPassed\tLower bound\tBar (source)\tAvg time\t")
		}
		tier := r.Tier
		if tier == "" {
			tier = "-"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%d of %d\t%.2f\t%.2f (%s)\t%s\t\n",
			r.Type, tier, r.Label(), r.Passed, r.Graded, r.Lower, r.Bar, r.BarSource, secs(r.AvgSecs))
	}
	tw.Flush()
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Proven: the lower bound cleared the bar at a checkpoint (24, 37, 50, 75, 100, 150, 200 jobs).")
	fmt.Fprintln(w, "Not good enough: it clearly can't reach the bar (checked at 5, 10, 24... jobs).")
	fmt.Fprintln(w, "Replays (find, review...): small = no more tool calls than your median for that type; the bar is the original")
	fmt.Fprintln(w, "  model's self-agreement minus 7 points, \"default\" (0.86) until that's measured.")
	fmt.Fprintln(w, "Skill checks (skill:...): exact answers from your code; small ≤ 200 lines, medium ≤ 1,000, large ≤ 4,000; fixed bar 0.85.")
	if skipped := sum(rows, func(r Row) int { return r.Skipped }); skipped > 0 {
		noun := "replays"
		if skipped == 1 {
			noun = "replay"
		}
		fmt.Fprintf(w, "%d %s couldn't be graded (low-anchor reviews, incomplete recordings) and don't count either way.\n", skipped, noun)
	}
}

func sum(rows []Row, f func(Row) int) int {
	n := 0
	for _, r := range rows {
		n += f(r)
	}
	return n
}

func secs(s float64) string {
	if s <= 0 {
		return "-"
	}
	if s < 90 {
		return fmt.Sprintf("%.0fs", s)
	}
	return fmt.Sprintf("%.1fm", s/60)
}
