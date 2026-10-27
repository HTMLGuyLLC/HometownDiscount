// Package spendmap builds the spend map: where subagent spend goes, and what
// each job type would have cost on cheaper Claude models.
package spendmap

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/jobtype"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/pricing"
)

// Cheaper Claude rungs the map reprices jobs on.
const (
	Sonnet = "claude-sonnet-5-5"
	Haiku  = "claude-haiku-5-5"
)

// Row is one job type on the map.
type Row struct {
	Type       string             `json:"type"`
	Jobs       int                `json:"jobs"`
	Replayable int                `json:"replayable"` // jobs a replay can grade
	ReplayCost float64            `json:"replayable_cost_usd"`
	Background int                `json:"background"`
	Cost       float64            `json:"cost_usd"`
	OnSonnet   float64            `json:"on_sonnet_usd"`
	OnHaiku    float64            `json:"on_haiku_usd"`
	ByFamily   map[string]float64 `json:"cost_by_family_usd"` // original cost by model family
	RanCheaper map[string]int     `json:"ran_cheaper"`        // jobs that already ran on sonnet or haiku
}

// Map is the whole spend map.
type Map struct {
	Since        time.Time `json:"since"`
	Days         int       `json:"days"`
	Rows         []Row     `json:"rows"`
	Jobs         int       `json:"jobs"`
	SubagentCost float64   `json:"subagent_cost_usd"`
	MainCost     float64   `json:"main_cost_usd"`
	Unpriced     int       `json:"unpriced_requests"` // requests on models missing from the price table
}

// ReplayableShare is the fraction of subagent spend in jobs a replay can grade.
func (m *Map) ReplayableShare() float64 {
	var v float64
	for _, r := range m.Rows {
		v += r.ReplayCost
	}
	if m.SubagentCost > 0 {
		return v / m.SubagentCost
	}
	return 0
}

// Share is subagent spend as a fraction of all Claude spend.
func (m *Map) Share() float64 {
	if total := m.SubagentCost + m.MainCost; total > 0 {
		return m.SubagentCost / total
	}
	return 0
}

// Build aggregates a history into a spend map.
func Build(h *history.History, prices *pricing.Table, since time.Time, days int) *Map {
	m := &Map{Since: since, Days: days}
	rows := map[string]*Row{}
	for i := range h.Jobs {
		j := &h.Jobs[i]
		typ := jobtype.Of(j)
		r := rows[typ]
		if r == nil {
			r = &Row{Type: typ, ByFamily: map[string]float64{}, RanCheaper: map[string]int{}}
			rows[typ] = r
		}
		r.Jobs++
		if j.Background {
			r.Background++
		}
		replayable := jobtype.Replayable(j, typ)
		if replayable {
			r.Replayable++
		}
		if p, ok := prices.Lookup(j.Model()); ok && (p.Family == "sonnet" || p.Family == "haiku") {
			r.RanCheaper[p.Family]++
		}
		for _, req := range j.Requests {
			usd, ok := prices.Cost(req.Model, req.Usage)
			if !ok {
				m.Unpriced++
				continue
			}
			r.Cost += usd
			if replayable {
				r.ReplayCost += usd
			}
			if p, ok := prices.Lookup(req.Model); ok {
				r.ByFamily[p.Family] += usd
			}
			// A cheaper rung never costs more than what actually ran.
			r.OnSonnet += min(usd, mustCost(prices, Sonnet, req.Usage))
			r.OnHaiku += min(usd, mustCost(prices, Haiku, req.Usage))
		}
		m.Jobs++
	}
	for _, req := range h.Main {
		if usd, ok := prices.Cost(req.Model, req.Usage); ok {
			m.MainCost += usd
		} else {
			m.Unpriced++
		}
	}
	for _, t := range jobtype.Order {
		if r := rows[t]; r != nil {
			m.Rows = append(m.Rows, *r)
			m.SubagentCost += r.Cost
		}
	}
	return m
}

func mustCost(prices *pricing.Table, model string, u pricing.Usage) float64 {
	u.Fast = false
	usd, ok := prices.Cost(model, u)
	if !ok {
		panic("price table is missing " + model)
	}
	return usd
}

// Render writes the map as a table.
func (m *Map) Render(w io.Writer) {
	fmt.Fprintf(w, "Hometown Discount · spend map · last %d days · API-equivalent prices\n\n", m.Days)
	if m.Jobs == 0 {
		fmt.Fprintln(w, "No subagent jobs found in this window. Claude Code keeps transcripts in ~/.claude/projects.")
		return
	}
	fmt.Fprintf(w, "%s subagent jobs · %s · %.0f%% of your Claude usage (%s)\n\n",
		commas(m.Jobs), usd(m.SubagentCost), m.Share()*100, usd(m.SubagentCost+m.MainCost))

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "Job type\tJobs\tRan on\tCost\tOn Sonnet 5.5\tOn Haiku 5.5\tReplayable\t")
	var sonnet, haiku float64
	for _, r := range m.Rows {
		sonnet += r.OnSonnet
		haiku += r.OnHaiku
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t\n",
			r.Type, commas(r.Jobs), mix(r), usd(r.Cost), usd(r.OnSonnet), usd(r.OnHaiku), replayable(r))
	}
	fmt.Fprintf(tw, "total\t%s\t\t%s\t%s\t%s\t\t\n", commas(m.Jobs), usd(m.SubagentCost), usd(sonnet), usd(haiku))
	tw.Flush()

	share := fmt.Sprintf("%.0f%%", m.ReplayableShare()*100)
	if s := m.ReplayableShare(); s > 0 && s < 0.005 {
		share = "under 1%" // some jobs are replayable; don't print 0%
	}
	fmt.Fprintf(w, "\nGradable by replay: %s of subagent spend (the \"Replayable\" jobs).\n\n", share)
	if hints := m.hints(); len(hints) > 0 {
		fmt.Fprintln(w, "Hints (not proof):")
		for _, h := range hints {
			fmt.Fprintln(w, "  "+h)
		}
	} else {
		fmt.Fprintln(w, "Hints: no evidence yet. None of these job types has run on a cheaper model in this window.")
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "\"On Sonnet\" and \"On Haiku\" reuse the same token counts. Real counts differ by model, and a lower")
	fmt.Fprintln(w, "price says nothing about quality: `htd replay` grades cheaper models on your own past jobs.")
	if m.Unpriced > 0 {
		fmt.Fprintf(w, "%s requests used models missing from the price table and aren't counted.\n", commas(m.Unpriced))
	}
}

func (m *Map) hints() []string {
	var out []string
	for _, r := range m.Rows {
		var parts []string
		for _, fam := range []string{"sonnet", "haiku"} {
			if n := r.RanCheaper[fam]; n > 0 {
				parts = append(parts, fmt.Sprintf("%s on %s", commas(n), titleCase(fam)))
			}
		}
		if len(parts) > 0 {
			noun := "jobs"
			if r.Jobs == 1 {
				noun = "job"
			}
			out = append(out, fmt.Sprintf("%s: of %s %s, %s already ran cheaper (%s)",
				r.Type, commas(r.Jobs), noun, commas(r.RanCheaper["sonnet"]+r.RanCheaper["haiku"]), strings.Join(parts, ", ")))
		}
	}
	return out
}

func mix(r Row) string {
	type fam struct {
		name string
		usd  float64
	}
	var fams []fam
	for name, v := range r.ByFamily {
		fams = append(fams, fam{name, v})
	}
	sort.Slice(fams, func(i, k int) bool { return fams[i].usd > fams[k].usd })
	var parts []string
	for _, f := range fams {
		if pct := f.usd / r.Cost * 100; pct >= 1 {
			parts = append(parts, fmt.Sprintf("%s %.0f%%", titleCase(f.name), pct))
		}
	}
	return strings.Join(parts, " · ")
}

func replayable(r Row) string {
	if !jobtype.Gradable(r.Type) {
		return "-"
	}
	return commas(r.Replayable)
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func usd(v float64) string {
	switch {
	case v >= 100:
		return "$" + commas(int(v+0.5))
	case v > 0 && v < 0.005:
		return "<$0.01"
	}
	return fmt.Sprintf("$%.2f", v)
}

func commas(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
