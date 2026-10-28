package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/goldset"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/jobtype"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/workspace"
)

// runGold reports how many replayable jobs have what a replay needs. It
// prints counts only, never prompts or answers.
func runGold(args []string, stdout, stderr io.Writer) error {
	fs := command("gold", "[--days N] [--fidelity N]", "How many of your past jobs can be replayed, and why the rest can't.", stderr)
	days := fs.Int("days", 30, "how many days of history to include")
	root := fs.String("projects", history.DefaultRoot(), "Claude Code transcripts folder")
	sample := fs.Int("fidelity", 0, "rebuild the workspace for up to N repo jobs per type and report fidelity")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var cache string
	if *sample > 0 {
		dir, err := os.MkdirTemp("", "htd-gold-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		cache = dir
	}
	h, err := history.Scan(*root, time.Now().AddDate(0, 0, -*days))
	if err != nil {
		return err
	}

	type counts struct {
		replayable, prompt, answer, fullRead, web, problems int
		tried, noRepo, faithful                             int
		predSmall, actSmall, agree                          int
	}
	byType := map[string]*counts{}
	type sized struct {
		pred  string
		calls int
	}
	sizes := map[string][]sized{}
	for i := range h.Jobs {
		j := &h.Jobs[i]
		typ := jobtype.Of(j)
		if !jobtype.Replayable(j, typ) {
			continue
		}
		c := byType[typ]
		if c == nil {
			c = &counts{}
			byType[typ] = c
		}
		c.replayable++
		it, err := goldset.Extract(j.Path)
		if err != nil {
			continue
		}
		if it.Prompt != "" {
			c.prompt++
		}
		if it.Answer != "" {
			c.answer++
		}
		sizes[typ] = append(sizes[typ], sized{jobtype.PredictTier(it.Prompt), jobtype.ToolCalls(j.Tools)})
		for _, r := range it.Reads {
			if r.Full {
				c.fullRead++
				break
			}
		}
		if len(it.Web) > 0 {
			c.web++
		}
		if len(it.Problems) > 0 {
			c.problems++
		}
		if cache != "" && typ != jobtype.Research && c.tried < *sample {
			c.tried++
			ws, err := workspace.Prepare(context.Background(), it, jobFrom(j), filepath.Join(cache, "base"), filepath.Join(cache, "jobs", j.AgentID))
			switch {
			case err != nil:
				c.noRepo++
			default:
				if ws.Fidelity.OK() {
					c.faithful++
				}
				ws.Close()
			}
		}
	}

	// Compare the prompt-only tier prediction with each job's real size,
	// relative to its type's median tool calls.
	for typ, ss := range sizes {
		calls := make([]int, len(ss))
		for i, x := range ss {
			calls[i] = x.calls
		}
		sort.Ints(calls)
		median := calls[len(calls)/2]
		c := byType[typ]
		for _, x := range ss {
			act := jobtype.ActualTier(x.calls, median)
			if x.pred == jobtype.Small {
				c.predSmall++
			}
			if act == jobtype.Small {
				c.actSmall++
			}
			if x.pred == act {
				c.agree++
			}
		}
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "Job type\tReplayable\tPrompt\tAnswer\tFull file read\tWeb recording\tSmall (predicted/actual)\tTier agreement\tFidelity sample\t")
	for _, t := range jobtype.Order {
		if c := byType[t]; c != nil {
			fid := "-"
			if c.tried > 0 {
				fid = fmt.Sprintf("%d/%d faithful, %d no repo", c.faithful, c.tried, c.noRepo)
			}
			fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%d/%d\t%.0f%%\t%s\t\n", t, c.replayable, c.prompt, c.answer, c.fullRead, c.web,
				c.predSmall, c.actSmall, 100*float64(c.agree)/float64(max(c.replayable, 1)), fid)
		}
	}
	return tw.Flush()
}
