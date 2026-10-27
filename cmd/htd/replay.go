package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/config"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/goldset"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/grade"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/jobtype"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/ladder"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/llm"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/loop"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/recording"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/sandbox"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/schedule"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/servers"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/slot"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/workspace"
)

// Provisional thresholds for review and research. Cards label these results
// accordingly.
const (
	defaultThetaReview   = 0.5
	defaultThetaResearch = 0.5
)

// runReplay replays past jobs on a local model and grades each one against
// what Claude produced. Results go to ~/.htd/runs.jsonl; `htd card` reads them.
func runReplay(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := store.DefaultDir()
	cfg, err := config.Load(dir)
	if err != nil {
		return fmt.Errorf("read %s: %w", config.Path(dir), err)
	}
	defURL, defModel := "http://127.0.0.1:8000", ""
	if len(cfg.Local) > 0 {
		defURL, defModel = cfg.Local[0].URL, cfg.Local[0].Model
	}
	url := fs.String("url", defURL, "local model server URL")
	model := fs.String("model", defModel, "model to replay on (default: the first one served)")
	days := fs.Int("days", 30, "how many days of history to replay from")
	max := fs.Int("max", 40, "stop after this many replays")
	types := fs.String("types", "", "comma-separated job types (default: all gradable)")
	root := fs.String("projects", history.DefaultRoot(), "Claude Code transcripts folder")
	allowRemote := fs.Bool("allow-remote", false, "allow a model server that isn't on this machine or local network (your code goes there)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := sandbox.Preflight(ctx); err != nil {
		return err
	}

	srv, rung, modelID, err := probeRung(ctx, *url, *model, *allowRemote || remoteAllowed(cfg))
	if err != nil {
		return err
	}
	rung = withConfigQuant(rung, cfg)

	st, err := store.Open(dir)
	if err != nil {
		return err
	}
	done, err := st.Done(rung.ID())
	if err != nil {
		return err
	}
	h, err := history.Scan(*root, time.Now().AddDate(0, 0, -*days))
	if err != nil {
		return err
	}
	candidates, byID := replayCandidates(h.Jobs, done, *types)
	if len(candidates) == 0 {
		fmt.Fprintln(stdout, "Nothing new to replay on this setup. Run `htd card` to see the results.")
		return nil
	}
	prior, err := priorEvidence(st, rung.ID())
	if err != nil {
		return err
	}
	bar := ladder.Bar(ladder.DefaultST, ladder.DefaultMargin)
	sched := schedule.New(schedule.Replays, candidates, prior, bar)
	fmt.Fprintf(stdout, "Replaying up to %d jobs on %s\nEasiest job types first, small jobs before large; a tier stops as soon as it clearly can't reach the bar.\n\n", *max, rung.Label())
	keepAwake()

	// Repo exports are full copies; each run gets its own and deletes it.
	os.MkdirAll(filepath.Join(dir, "cache"), 0o700)
	cache, err := os.MkdirTemp(filepath.Join(dir, "cache"), "base-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(cache)
	client := llm.New(srv.URL, modelID)
	client.MaxTokens = replayMaxTokens
	up := serverUp(ctx, srv.URL)
	var ran, passed int
	skipped := map[string]int{}
	touched := map[schedule.Key]bool{}
	for ran < *max {
		sj, ok := sched.Next()
		if !ok {
			break
		}
		q := queued{job: byID[sj.ID], typ: sj.Type}
		// The model is locked per turn, not per job, so an ask_local call
		// from Claude waits for one turn at most. Time spent waiting for the
		// lock doesn't count against the replay.
		var waited time.Duration
		locked := slot.Locked{Inner: client, Dir: dir, Waited: &waited}
		run, why, err := replayOne(ctx, locked, q, rung, cache, st, up)
		if errors.Is(err, errServerDown) && waitForServer(ctx, up, stdout) {
			why, err = "the server restarted mid-job", nil // not the model's fault: tried again next time
		}
		if err != nil {
			return fmt.Errorf("replay stopped (%d done): %w", ran, err)
		}
		if why == "" && run.Reason == "timeout" && waited > 0 {
			why = "timed out while sharing the model" // not a fair fail; tried again next time
		}
		run.Seconds -= waited.Seconds()
		if why != "" {
			skipped[why]++
			continue
		}
		run.Tier = sj.Tier
		if err := st.Add(run); err != nil {
			return err
		}
		k := schedule.Key{Type: sj.Type, Tier: sj.Tier}
		before := sched.State(k)
		sched.Record(sj, run.Graded, run.Pass)
		touched[k] = true
		ran++
		status := "fail"
		switch {
		case !run.Graded:
			status = "not graded (" + run.Reason + ")"
		case run.Pass:
			status = "pass"
			passed++
		case run.Reason != "":
			status = "fail (" + run.Reason + ")"
		}
		fmt.Fprintf(stdout, "[%d] %-6s %-9s %-34s %s\n", ran, sj.Tier, sj.Type, status, secsLabel(run.Seconds))
		if after := sched.State(k); after != before {
			fmt.Fprintf(stdout, "    → %s %s: %s\n", sj.Tier, sj.Type, after)
		}
	}
	fmt.Fprintf(stdout, "\n%d replayed, %d passed.\n", ran, passed)
	if len(skipped) > 0 {
		var parts []string
		for why, n := range skipped {
			parts = append(parts, fmt.Sprintf("%d %s", n, why))
		}
		sort.Strings(parts)
		fmt.Fprintf(stdout, "Skipped (not counted either way): %s.\n", strings.Join(parts, ", "))
	}
	for _, typ := range jobtype.TypeOrder {
		for _, tier := range jobtype.Tiers {
			if k := (schedule.Key{Type: typ, Tier: tier}); touched[k] {
				fmt.Fprintf(stdout, "  %s %s: %s\n", tier, typ, sched.State(k))
			}
		}
	}
	fmt.Fprintln(stdout, "Run `htd card` to see where each job type stands.")
	return nil
}

type queued struct {
	job *history.Job
	typ string
}

// replayCandidates lists replayable jobs not yet run on this setup, with each
// job's size tier relative to the median tool calls of its type.
func replayCandidates(jobs []history.Job, done map[string]bool, types string) ([]schedule.Job, map[string]*history.Job) {
	want := map[string]bool{}
	for _, t := range strings.Split(types, ",") {
		if t = strings.TrimSpace(t); t != "" {
			want[t] = true
		}
	}
	calls := map[string][]int{}
	var eligible []*history.Job
	var typs []string
	for i := range jobs {
		j := &jobs[i]
		typ := jobtype.Of(j)
		if !jobtype.Replayable(j, typ) {
			continue
		}
		calls[typ] = append(calls[typ], jobtype.ToolCalls(j.Tools))
		if !done[j.AgentID] && (len(want) == 0 || want[typ]) {
			eligible = append(eligible, j)
			typs = append(typs, typ)
		}
	}
	median := map[string]int{}
	for typ, cs := range calls {
		sort.Ints(cs)
		median[typ] = cs[len(cs)/2]
	}
	var out []schedule.Job
	byID := map[string]*history.Job{}
	for i, j := range eligible {
		n := jobtype.ToolCalls(j.Tools)
		out = append(out, schedule.Job{ID: j.AgentID, Type: typs[i], Tier: jobtype.ActualTier(n, median[typs[i]]), Calls: n})
		byID[j.AgentID] = j
	}
	return out, byID
}

// priorEvidence is this setup's graded outcomes per type and tier, in job order.
func priorEvidence(st *store.Store, rungID string) (map[schedule.Key][]bool, error) {
	runs, err := st.All()
	if err != nil {
		return nil, err
	}
	prior := map[schedule.Key][]bool{}
	for _, r := range runs {
		if r.Rung == rungID && r.Graded && r.Tier != "" {
			k := schedule.Key{Type: r.Type, Tier: r.Tier}
			prior[k] = append(prior[k], r.Pass)
		}
	}
	return prior, nil
}

// replayOne returns a graded run, or a reason the job was skipped, or an
// error that should stop the whole replay (e.g. the server went away).
func replayOne(ctx context.Context, client llm.Chatter, q queued, rung store.Rung, cache string, st *store.Store, up func() bool) (store.Run, string, error) {
	it, err := goldset.Extract(q.job.Path)
	if err != nil || it.Prompt == "" || it.Answer == "" {
		return store.Run{}, "no task or answer", nil
	}
	var rec *recording.Recording
	if q.typ == jobtype.Research {
		if !recording.Complete(it.Web) {
			return store.Run{}, "incomplete recording", nil
		}
		rec = recording.New(it.Web)
	}

	jobDir, err := os.MkdirTemp("", "htd-job-")
	if err != nil {
		return store.Run{}, "", err
	}
	defer os.RemoveAll(jobDir)
	ws, err := workspace.Prepare(ctx, it, q.job.Start, cache, filepath.Join(jobDir, "ws"))
	switch {
	case err != nil && q.typ != jobtype.Research:
		return store.Run{}, "no repo", nil
	case err == nil && !ws.Fidelity.OK():
		return store.Run{}, "unfaithful workspace", nil
	}
	// Skip before spending any model time on a job that can't be judged.
	if why := gradable(q.typ, it.Answer, ws, rec); why != "" {
		return store.Run{}, why, nil
	}
	job := loop.Job{Task: it.Prompt, Tmp: filepath.Join(jobDir, "tmp")}
	if ws != nil {
		job.Dir, job.RepoRoot = ws.Root, ws.RepoRoot
	} else {
		job.Dir = jobDir // research with no repo: an empty folder
	}
	if rec != nil {
		job.Web = rec
	}

	res, err := loop.Run(ctx, client, job, loop.DefaultLimits)
	run := store.Run{
		JobID: q.job.AgentID, JobTime: q.job.Start, Type: q.typ, Reference: q.job.Model(), Rung: rung.ID(),
		Seconds: res.Elapsed.Seconds(), Turns: res.Turns, At: time.Now(), Graded: true,
	}
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return store.Run{}, "", err // stopped by the user
		case llm.IsContextOverflow(err):
			return store.Run{}, "too long for the server's context", nil // a server setting, not skill
		case !up():
			return store.Run{}, "", fmt.Errorf("%w: %w", errServerDown, err)
		}
		// A crash or busy server on this job: not the model's fault, so
		// nothing is recorded and the job is tried again next time.
		return store.Run{}, "server error", nil
	}
	_ = st.SaveAnswer(rung.ID(), q.job.AgentID, it.Answer, res.Answer)
	if res.Stopped != "" {
		run.Reason = res.Stopped // ran out of turns or time: a fail for this rung
		return run, "", nil
	}

	g, ok := gradeAnswer(q.typ, it.Answer, res.Answer, ws, rec)
	if !ok {
		return store.Run{}, "not gradable", nil
	}
	applyGrade(&run, g)
	return run, "", nil
}

// gradable grades the original answer against itself. When the rules can't
// grade it, or it fails its own grade, no replay of the job can be judged
// fairly, and the reason is returned.
func gradable(typ, ref string, ws *workspace.Workspace, rec *recording.Recording) string {
	self, ok := gradeAnswer(typ, ref, ref, ws, rec)
	switch {
	case !ok:
		return "not gradable"
	case !self.Graded:
		return "not gradable (" + self.Reason + ")"
	case !self.Pass:
		return "original fails its own grade"
	}
	return ""
}

// applyGrade records a grade on a run.
func applyGrade(run *store.Run, g grade.Result) {
	run.Pass, run.Graded, run.Recall, run.Extra = g.Pass, g.Graded, g.Recall, g.Extra
	run.Severe = len(g.Fabricated) > 0
	run.Made = first(g.Fabricated, 5)
	run.Reason = g.Reason
	if run.Reason == "" && run.Severe {
		run.Reason = "made up: " + strings.Join(first(g.Fabricated, 2), ", ")
	}
}

// gradeAnswer grades a candidate answer against the reference for a job type.
func gradeAnswer(typ, ref, cand string, ws *workspace.Workspace, rec *recording.Recording) (grade.Result, bool) {
	var gws grade.Workspace
	if ws != nil {
		gws = ws
	}
	var grec grade.Recording
	if rec != nil {
		grec = rec
	}
	switch typ {
	case jobtype.Find:
		return grade.Find(ref, cand, gws), true
	case jobtype.Extract:
		return grade.Extract(ref, cand, gws), true
	case jobtype.Classify:
		return grade.Classify(ref, cand, nil), true
	case jobtype.Transform:
		return grade.Transform(ref, cand), true
	case jobtype.Review:
		return grade.Review(ref, cand, gws, defaultThetaReview), true
	case jobtype.Research:
		return grade.Research(ref, cand, grec, defaultThetaResearch), true
	}
	return grade.Result{}, false
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func first(xs []string, n int) []string {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}

// keepAwake stops macOS from sleeping while this process runs.
func keepAwake() {
	if runtime.GOOS != "darwin" {
		return
	}
	if err := exec.Command("caffeinate", "-i", "-w", strconv.Itoa(os.Getpid())).Start(); err != nil && !errors.Is(err, exec.ErrNotFound) {
		fmt.Fprintln(os.Stderr, "htd: couldn't keep the Mac awake:", err)
	}
}

func secsLabel(s float64) string {
	if s < 90 {
		return fmt.Sprintf("%.0fs", s)
	}
	return fmt.Sprintf("%.1fm", s/60)
}

var errServerDown = errors.New("the server stopped answering")

// replayMaxTokens is the output room per turn in a replay. Thinking models
// can spend 8,192 tokens thinking before they act; a whole job deserves more
// room than a one-shot ask_local answer.
const replayMaxTokens = 16384

// serverUp returns a check that the model server answers a probe.
func serverUp(ctx context.Context, url string) func() bool {
	return func() bool {
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		_, err := servers.Probe(c, url)
		return err == nil
	}
}

// serverWait is how long a batch waits for a crashed or recovering model
// server; they often come back within a minute or two.
var serverWait = 5 * time.Minute

// waitForServer waits until the model server answers again, up to serverWait.
func waitForServer(ctx context.Context, up func() bool, w io.Writer) bool {
	fmt.Fprintf(w, "    The model server isn't answering; waiting up to %s for it to come back...\n", serverWait)
	for deadline := time.Now().Add(serverWait); time.Now().Before(deadline); {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(min(15*time.Second, serverWait)):
		}
		if up() {
			fmt.Fprintln(w, "    It's back; carrying on.")
			return true
		}
	}
	return false
}
