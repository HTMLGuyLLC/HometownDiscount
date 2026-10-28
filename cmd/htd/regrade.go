package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/goldset"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/grade"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/jobtype"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/recording"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/skills"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/workspace"
)

// runRegrade re-grades saved replay answers with the current graders and
// replay rules, without running the model again, and prints old vs new
// verdicts. With --apply it saves the new grades, so evidence gathered under
// older rules counts by the current ones.
func runRegrade(args []string, stdout, stderr io.Writer) error {
	fs := command("regrade", "[--apply]", "Grades saved replay answers again with this version's rules, without running\nthe model. --apply saves the new grades.", stderr)
	root := fs.String("projects", history.DefaultRoot(), "Claude Code transcripts folder")
	apply := fs.Bool("apply", false, "save the new grades (the card then uses them)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir := store.DefaultDir()
	st, err := store.Open(dir)
	if err != nil {
		return err
	}
	runs, err := st.All()
	if err != nil {
		return err
	}
	h, err := history.Scan(*root, time.Now().AddDate(-1, 0, 0))
	if err != nil {
		return err
	}
	jobs := map[string]*history.Job{}
	for i := range h.Jobs {
		jobs[h.Jobs[i].AgentID] = &h.Jobs[i]
	}

	ctx := context.Background()
	os.MkdirAll(filepath.Join(dir, "cache"), 0o700)
	cache, err := os.MkdirTemp(filepath.Join(dir, "cache"), "base-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(cache)
	changed := 0
	for _, r := range runs {
		if strings.HasPrefix(r.Type, skills.TypePrefix) {
			continue // skill checks are graded exactly; nothing to re-grade
		}
		if r.Graded && !r.Pass && stoppedReasons[r.Reason] {
			continue // the model ran out of turns or time: there's no full answer to grade
		}
		cand, ok := savedCandidate(st, r)
		j := jobs[r.JobID]
		if !ok || j == nil {
			continue
		}
		it, err := goldset.Extract(j.Path)
		if err != nil {
			continue
		}
		jobDir, err := os.MkdirTemp("", "htd-regrade-")
		if err != nil {
			return err
		}
		next := r
		next.Regraded = time.Now()
		var rec *recording.Recording
		if r.Type == jobtype.Research && recording.Complete(it.Web) {
			rec = recording.New(it.Web)
		}
		ws, err := workspace.Prepare(ctx, it, jobFrom(j), cache, filepath.Join(jobDir, "ws"))
		switch {
		case r.Type == jobtype.Research && rec == nil:
			applyGrade(&next, grade.Result{Reason: "incomplete recording"})
		case err != nil && r.Type != jobtype.Research:
			applyGrade(&next, grade.Result{Reason: "no repo"})
		case err == nil && !ws.Fidelity.OK():
			applyGrade(&next, grade.Result{Reason: "unfaithful workspace"})
		default: // ws is nil for research with no repo
			if why := gradable(r.Type, it.Answer, ws, rec); why != "" {
				applyGrade(&next, grade.Result{Reason: why})
			} else if g, ok := gradeAnswer(r.Type, it.Answer, cand, ws, rec); ok {
				applyGrade(&next, g)
			}
		}
		os.RemoveAll(jobDir)
		note := ""
		if gradeLabel(r) == gradeLabel(next) && !sameGrade(r, next) {
			note = "  (details changed)"
		}
		fmt.Fprintf(stdout, "%-8.8s %-8s was %s → now %s  recall %.2f→%.2f%s\n",
			r.JobID, r.Type, gradeLabel(r), gradeLabel(next), r.Recall, next.Recall, note)
		if sameGrade(r, next) {
			continue
		}
		changed++
		if *apply {
			if err := st.Add(next); err != nil {
				return err
			}
		}
	}
	grades := "grades"
	if changed == 1 {
		grades = "grade"
	}
	switch {
	case changed == 0:
		fmt.Fprintln(stdout, "\nNo grades changed.")
	case *apply:
		fmt.Fprintf(stdout, "\nSaved %d new %s. `htd card` uses them now.\n", changed, grades)
	default:
		fmt.Fprintf(stdout, "\n%d %s would change. Run `htd regrade --apply` to save them.\n", changed, grades)
	}
	return nil
}

// stoppedReasons are the loop's reasons for ending a replay without a full
// answer (see loop.Result.Stopped).
var stoppedReasons = map[string]bool{"timeout": true, "too many turns": true, "empty answer": true, "cut off": true}

// gradeLabel is a run's grade in words: pass, fail, or not graded (why).
func gradeLabel(r store.Run) string {
	switch {
	case !r.Graded && (strings.HasPrefix(r.Reason, "not gradable") || r.Reason == "original fails its own grade"):
		return r.Reason
	case !r.Graded:
		return "not graded (" + r.Reason + ")"
	case r.Pass:
		return "pass"
	}
	return "fail (" + r.Reason + ")"
}

// savedCandidate reads the answer this run's setup gave.
func savedCandidate(st *store.Store, r store.Run) (string, bool) {
	b, err := os.ReadFile(st.AnswerPath(r.Rung, r.JobID))
	if err != nil {
		return "", false
	}
	_, cand, ok := strings.Cut(string(b), "# Candidate (the replay)\n\n")
	return strings.TrimSpace(cand), ok
}

func verdict(pass bool) string {
	if pass {
		return "pass"
	}
	return "fail"
}

// sameGrade reports whether two runs of a job carry the same grade, details
// included, so a regrade saves a new record only when something changed.
func sameGrade(a, b store.Run) bool {
	return a.Pass == b.Pass && a.Graded == b.Graded && a.Reason == b.Reason && a.Recall == b.Recall &&
		a.Extra == b.Extra && a.Severe == b.Severe && slices.Equal(a.Made, b.Made)
}
