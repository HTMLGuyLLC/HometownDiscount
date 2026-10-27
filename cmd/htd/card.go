package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/card"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/config"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/delegate"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

// runCard shows the evidence per setup and job type.
func runCard(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("card", flag.ContinueOnError)
	fs.SetOutput(stderr)
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
	if len(rows) > 0 {
		rows = card.WithSpeed(rows, cfg.Speed, claudeTimes(dir, history.DefaultRoot(), nil, 30, cfg.Speed.ClaudeModel, stdout))
	}
	if *details {
		card.Render(stdout, rows)
		return nil
	}
	card.Headline(stdout, rows)
	delegationSummary(stdout, filepath.Join(dir, "delegations.jsonl"))
	if len(rows) > 0 {
		fmt.Fprintln(stdout, "Every number behind this: `htd card --details`.")
	}
	return nil
}

// delegationSummary reports ask_local use: tasks answered locally and the
// file contents that never entered Claude's context (about 4 bytes a token).
func delegationSummary(w io.Writer, path string) {
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
			bytes += d.Bytes
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
	fmt.Fprintf(w, "ask_local: %d %s answered locally, %d refused (not proven) · about %s tokens of file contents kept out of Claude's context\n\n",
		answered, tasks, refused, commas(bytes/4))
}

func commas(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
