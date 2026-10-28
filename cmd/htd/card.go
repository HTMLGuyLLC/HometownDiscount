package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
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
	st, err := store.Open(dir)
	if err != nil {
		return err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}
	runs, err := st.All()
	if err != nil {
		return err
	}
	labels := map[string]string{}
	for _, r := range runs {
		var rung store.Rung
		if _, ok := labels[r.Rung]; !ok && json.Unmarshal([]byte(r.Rung), &rung) == nil {
			labels[r.Rung] = rung.Label()
		}
	}
	rows := card.Build(runs, labels)
	claude, _ := speed.Load(dir)
	if len(rows) > 0 {
		claude = claudeTimes(dir, history.DefaultRoot(), nil, 30, cfg.Speed.ClaudeModel, stdout)
		rows = card.WithSpeed(rows, cfg.Speed, claude)
	}
	if *details {
		card.Render(stdout, rows)
		return nil
	}
	card.Headline(stdout, rows)
	prices, _ := pricing.Default()
	delegationSummary(stdout, filepath.Join(dir, "delegations.jsonl"), claude, prices)
	if len(rows) > 0 {
		fmt.Fprintln(stdout, "Every number behind this: `htd card --details`.")
	}
	return nil
}

// answerGuess is the bytes an answer took before answers were logged.
const answerGuess = 600

// delegationSummary reports ask_local use so far: tasks answered locally,
// the file contents kept out of Claude's context (about 4 bytes a token, net
// of the answer that went back), and what that's worth at API prices. A file
// Claude reads is written to its cache once and re-read on each later turn,
// typically RideAlong turns in the user's own transcripts.
func delegationSummary(w io.Writer, path string, claude speed.Claude, prices *pricing.Table) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	var answered, refused, bytes int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var d delegate.Delegation
		if json.Unmarshal(sc.Bytes(), &d) != nil {
			continue
		}
		switch d.Outcome {
		case "answered":
			answered++
			back := d.AnswerBytes
			if back == 0 {
				back = answerGuess
			}
			bytes += max(0, d.Bytes-back)
		case "refused":
			refused++
		}
	}
	if answered+refused == 0 {
		return
	}
	tasks := "tasks"
	if answered == 1 {
		tasks = "task"
	}
	line := fmt.Sprintf("ask_local: %d %s answered locally, %d refused · about %s tokens kept out of Claude's context", answered, tasks, refused, commas(bytes/4))
	if tokens := bytes / 4; tokens > 0 && prices != nil && claude.Model != "" {
		// Priced per 1,000 tokens and scaled, so the total never trips a
		// model's long-prompt price, which applies to single requests.
		unit := pricing.Usage{CacheWrite1h: 1000, CacheRead: int64(1000 * claude.RideAlong)}
		if per1k, ok := prices.Cost(claude.Model, unit); ok {
			line += fmt.Sprintf(" · worth about $%.2f at %s API prices", per1k*float64(tokens)/1000, pricing.Display(claude.Model))
		}
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
