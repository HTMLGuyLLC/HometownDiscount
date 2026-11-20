package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strconv"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/card"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/config"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/delegate"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/pricing"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/speed"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

// runCard shows the evidence per setup and job type.
func runCard(args []string, stdout, stderr io.Writer) error {
	fs := command("card", "[--details]", "What each local model has earned, per task and size: proven, too slow,\nlearning or not good enough.", stderr)
	details := fs.Bool("details", false, "show every number: passes, bounds, bars and timing per type and size")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir := store.DefaultDir()
	ev, err := loadEvidence(dir, stdout)
	if err != nil {
		return err
	}
	rows := card.Build(ev.runs, rungLabels(ev.runs))
	if len(rows) > 0 {
		rows = card.WithSpeed(rows, ev.cfg.Speed, ev.claude)
	}
	if *details {
		card.Render(stdout, rows)
		return nil
	}
	card.Headline(stdout, rows)
	prices, _ := pricing.Default()
	delegationSummary(stdout, filepath.Join(dir, delegate.LogName), ev.claude, prices)
	if len(rows) > 0 {
		fmt.Fprintln(stdout, "Every number behind this: `htd card --details`.")
	}
	return nil
}

// evidence is what the card and the scorecard page are built from.
type evidence struct {
	cfg    config.Config
	runs   []store.Run
	claude speed.Claude
}

// loadEvidence reads the stored runs, the settings and Claude's typical
// times. Claude is timed from the transcripts only when there are runs to
// compare (and then only when the saved times are stale); w gets a note when
// that takes a while.
func loadEvidence(dir string, w io.Writer) (evidence, error) {
	st, err := store.Open(dir)
	if err != nil {
		return evidence{}, err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return evidence{}, err
	}
	runs, err := st.All()
	if err != nil {
		return evidence{}, err
	}
	claude, _ := speed.Load(dir)
	if len(runs) > 0 {
		claude = claudeTimes(dir, history.DefaultRoot(), nil, 30, cfg.Speed.ClaudeModel, w)
	}
	return evidence{cfg: cfg, runs: runs, claude: claude}, nil
}

// rungLabels names each stored setup in plain words.
func rungLabels(runs []store.Run) map[string]string {
	labels := map[string]string{}
	for _, r := range runs {
		var rung store.Rung
		if _, ok := labels[r.Rung]; !ok && json.Unmarshal([]byte(r.Rung), &rung) == nil {
			labels[r.Rung] = rung.Label()
		}
	}
	return labels
}

// delegationSummary reports ask_local use so far: tasks answered locally,
// the file contents kept out of Claude's context, and what that's worth at
// API prices.
func delegationSummary(w io.Writer, path string, claude speed.Claude, prices *pricing.Table) {
	s := delegate.Summarize(delegate.Load(path))
	if s.Answered+s.Refused == 0 {
		return
	}
	tasks := "tasks"
	if s.Answered == 1 {
		tasks = "task"
	}
	line := fmt.Sprintf("ask_local: %d %s answered locally, %d refused · about %s tokens kept out of Claude's context", s.Answered, tasks, s.Refused, commas(s.Tokens()))
	if worth, ok := delegate.Worth(s.Tokens(), claude, prices); ok {
		line += fmt.Sprintf(" · worth about $%.2f at %s API prices", worth, pricing.Display(claude.Model))
	}
	fmt.Fprintln(w, line)
	fmt.Fprintln(w)
}

func commas(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
