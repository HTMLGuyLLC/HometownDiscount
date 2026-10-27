package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/config"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/llm"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/schedule"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/servers"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/skills"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/slot"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

// skillOrder is the scheduling order for skill checks.
var skillOrder = schedule.Order{Types: skills.Order, Tiers: skills.SizeOrder}

// runBench runs skill checks built from the user's own repo on a local model:
// easiest skill and smallest input first, one at a time, stopping each size
// as soon as it clearly can't reach the bar.
func runBench(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := store.DefaultDir()
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}
	defURL, defModel := "http://127.0.0.1:8000", ""
	if len(cfg.Local) > 0 {
		defURL, defModel = cfg.Local[0].URL, cfg.Local[0].Model
	}
	wd, _ := os.Getwd()
	url := fs.String("url", defURL, "local model server URL")
	model := fs.String("model", defModel, "model to test (default: the first one served)")
	repo := fs.String("repo", wd, "git repo to build skill checks from")
	max := fs.Int("max", 150, "stop after this many checks (each way, for a model that thinks)")
	timeout := fs.Duration("timeout", 5*time.Minute, "time limit per check")
	allowRemote := fs.Bool("allow-remote", false, "allow a model server that isn't on this machine or local network (your code goes there)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv, rung, mod, err := probeRung(ctx, *url, *model, *allowRemote || remoteAllowed(cfg))
	if err != nil {
		return err
	}
	rung = withConfigQuant(rung, cfg)
	tasks, err := skills.Generate(*repo)
	if err != nil {
		return err
	}
	st, err := store.Open(dir)
	if err != nil {
		return err
	}
	runs, err := st.All()
	if err != nil {
		return err
	}
	b := benchRun{dir: dir, st: st, runs: runs, tasks: tasks, repo: filepath.Base(*repo), srv: srv, ctxSize: ctxOf(srv, mod), max: *max, out: stdout}
	plain := llm.New(srv.URL, mod)
	plain.HTTP.Timeout = *timeout
	off := rung
	off.Effort = store.ThinkingOff
	// A model that thinks first is also checked with thinking off: often far
	// quicker, and as good at some skills. ask_local picks per skill and size.
	ways := []way{{rung, plain}}
	thinks, known := cachedThinks(dir, rung.ID(), time.Now())
	if !known && (b.pending(rung) || b.pending(off)) {
		if !waitForMemory(ctx, stdout) {
			if ctx.Err() != nil {
				return errors.New("skill checks stopped (0 done)")
			}
			return fmt.Errorf("skill checks stopped (0 done): %w", errLowMemory)
		}
		if thinks, known, err = b.thinks(ctx, plain); err != nil {
			return err
		}
		if known {
			saveThinks(dir, rung.ID(), thinks, time.Now())
		}
	}
	if thinks {
		quiet := llm.New(srv.URL, mod)
		quiet.HTTP.Timeout = *timeout
		quiet.NoThinking = true
		ways = []way{{off, quiet}, {rung, plain}}
		fmt.Fprintln(stdout, "This model thinks before it answers, so it's checked with thinking off first (much quicker), then on.\nask_local uses whichever is proven and faster for each skill and size.")
		fmt.Fprintln(stdout)
	}
	keepAwake()
	for _, w := range ways {
		err := b.run(ctx, w)
		if errors.Is(err, llm.ErrThinking) {
			fmt.Fprintln(stdout, "This server doesn't let the model answer without thinking, so it's only checked with thinking on.")
			fmt.Fprintln(stdout)
			continue
		}
		if err != nil {
			return err
		}
	}
	fmt.Fprintln(stdout, "Run `htd card` to see what this model can be trusted with.")
	return nil
}

// benchRun is one `htd bench` run: skill checks from one repo on one model.
type benchRun struct {
	dir     string
	st      *store.Store
	runs    []store.Run
	tasks   []skills.Task
	repo    string // its folder name
	srv     *servers.Server
	ctxSize int
	max     int
	out     io.Writer
}

// way is one way of running the model: the setup its results are kept
// under, and a client that asks the model that way.
type way struct {
	rung   store.Rung
	client *llm.Client
}

// pending reports whether checks are left for a setup.
func (b *benchRun) pending(r store.Rung) bool {
	done, err := b.st.Done(r.ID())
	if err != nil {
		return true
	}
	for _, t := range b.tasks {
		if !done[t.ID] {
			return true
		}
	}
	return false
}

// thinks asks the model one easy question, with the model to itself, to see
// whether it thinks first. known is false when the server didn't answer:
// the checks then run the usual way, and the server errors get their usual
// handling there.
func (b *benchRun) thinks(ctx context.Context, c *llm.Client) (thinks, known bool, err error) {
	release, err := slot.Acquire(ctx, b.dir, 0, waiting(b.out))
	if err != nil {
		return false, false, err
	}
	defer release()
	thinks, err = c.Thinks(ctx)
	return thinks, err == nil, ctx.Err()
}

// thinksCache is where bench remembers which setups think first, so a run
// with nothing left doesn't wake the model to ask. It asks again weekly, in
// case the server's settings changed.
func thinksCache(dir string) string { return filepath.Join(dir, "cache", "thinking.json") }

type thinksEntry struct {
	Thinks bool      `json:"thinks"`
	At     time.Time `json:"at"`
}

func cachedThinks(dir, rungID string, now time.Time) (thinks, known bool) {
	var m map[string]thinksEntry
	b, err := os.ReadFile(thinksCache(dir))
	if err != nil || json.Unmarshal(b, &m) != nil {
		return false, false
	}
	e, ok := m[rungID]
	return e.Thinks, ok && now.Sub(e.At) < 7*24*time.Hour
}

func saveThinks(dir, rungID string, thinks bool, now time.Time) {
	m := map[string]thinksEntry{}
	if b, err := os.ReadFile(thinksCache(dir)); err == nil {
		json.Unmarshal(b, &m)
	}
	m[rungID] = thinksEntry{Thinks: thinks, At: now.UTC()}
	if b, err := json.MarshalIndent(m, "", "  "); err == nil && os.MkdirAll(filepath.Dir(thinksCache(dir)), 0o700) == nil {
		os.WriteFile(thinksCache(dir), b, 0o600) // only a shortcut: losing it costs one question
	}
}

// run does the checks left for one way of running the model, easiest first.
func (b *benchRun) run(ctx context.Context, w way) error {
	rung, client, stdout, st, dir, ctxSize := w.rung, w.client, b.out, b.st, b.dir, b.ctxSize
	done, err := st.Done(rung.ID())
	if err != nil {
		return err
	}
	prior := map[schedule.Key][]bool{}
	for _, r := range b.runs {
		if r.Rung == rung.ID() && r.Graded && strings.HasPrefix(r.Type, skills.TypePrefix) {
			k := schedule.Key{Type: strings.TrimPrefix(r.Type, skills.TypePrefix), Tier: r.Tier}
			prior[k] = append(prior[k], r.Pass)
		}
	}
	byID := map[string]skills.Task{}
	var jobs []schedule.Job
	for _, t := range b.tasks {
		if done[t.ID] {
			continue
		}
		byID[t.ID] = t
		jobs = append(jobs, schedule.Job{ID: t.ID, Type: t.Skill, Tier: t.Size, Calls: t.Lines})
	}
	if len(jobs) == 0 {
		fmt.Fprintf(stdout, "No new skill checks for this setup (%s).\n\n", rung.Label())
		return nil
	}
	sched := schedule.New(skillOrder, jobs, prior, skills.Bar)
	fmt.Fprintf(stdout, "Skill checks from %s on %s\nEasiest skills and smallest files first; a size stops as soon as it clearly can't reach %.0f%%.\n\n",
		b.repo, rung.Label(), skills.Bar*100)

	up := serverUp(ctx, b.srv.URL)
	var ran, passed, errorsInRow int
	skipped := map[string]int{}
	for ran < b.max {
		j, ok := sched.Next()
		if !ok {
			break
		}
		t := byID[j.ID]
		if ctxSize > 0 && len(t.Prompt)/3+1000 > ctxSize {
			skipped["too long for the server's context"]++
			sched.Record(j, false, false)
			continue
		}
		if !waitForMemory(ctx, stdout) {
			if ctx.Err() != nil {
				return fmt.Errorf("skill checks stopped (%d done)", ran)
			}
			return fmt.Errorf("skill checks stopped (%d done): %w", ran, errLowMemory)
		}
		release, err := slot.Acquire(ctx, dir, 0, waiting(stdout)) // the timer starts once the model is ours
		if err != nil {
			return err
		}
		start := time.Now()
		reply, err := chatOnce(ctx, client, []llm.Message{
			{Role: "system", Content: skills.SystemPrompt},
			{Role: "user", Content: t.Prompt},
		})
		release()
		run := store.Run{JobID: t.ID, JobTime: start, Type: skills.TypePrefix + t.Skill, Tier: t.Size, Rung: rung.ID(),
			Graded: true, Seconds: time.Since(start).Seconds(), Turns: 1, At: time.Now()}
		var netErr net.Error
		switch {
		case ctx.Err() != nil:
			return fmt.Errorf("skill checks stopped (%d done)", ran)
		case errors.As(err, &netErr) && netErr.Timeout():
			run.Reason = "timeout"
		case llm.IsContextOverflow(err):
			// A limit of the server's settings, not the model's skill: skip.
			skipped["too long for the server's context"]++
			continue
		case errors.Is(err, llm.ErrThinking):
			return err // this way of running the model isn't available here
		case err != nil:
			// A crash, an overloaded or restarting server: not the model's
			// fault, so nothing is recorded. A server that stops answering
			// gets time to recover; several errors in a row stop the run.
			if !up() {
				if !waitForServer(ctx, up, stdout) {
					return fmt.Errorf("skill checks stopped (%d done): %w: %w", ran, errServerDown, err)
				}
				errorsInRow = 0
				skipped["the server restarted mid-check"]++
				continue
			}
			if errorsInRow++; errorsInRow >= 3 {
				return fmt.Errorf("skill checks stopped (%d done): the server keeps failing: %w", ran, err)
			}
			skipped["server error"]++
			continue
		case reply.FinishReason == "length":
			// Thinking used up the answer budget. ask_local refuses these
			// answers too, so a check doesn't pass on one.
			run.Reason = "cut off"
			_ = st.SaveAnswer(rung.ID(), t.ID, t.Answer, skills.Clean(reply.Content))
		default:
			run.Pass = skills.Grade(t, reply.Content)
			if !run.Pass {
				run.Reason = "wrong answer"
			}
			_ = st.SaveAnswer(rung.ID(), t.ID, t.Answer, skills.Clean(reply.Content))
		}
		errorsInRow = 0
		if err := st.Add(run); err != nil {
			return err
		}
		k := schedule.Key{Type: t.Skill, Tier: t.Size}
		before := sched.State(k)
		sched.Record(j, true, run.Pass)
		ran++
		if run.Pass {
			passed++
		}
		note := ""
		if run.Reason == "cut off" || run.Reason == "timeout" {
			note = "  (" + run.Reason + ")"
		}
		fmt.Fprintf(stdout, "[%d] %-6s %-6s %-5s %4d lines  %s%s\n", ran, t.Size, t.Skill, verdict(run.Pass), t.Lines, secsLabel(run.Seconds), note)
		if after := sched.State(k); after != before {
			fmt.Fprintf(stdout, "    → %s %s: %s\n", t.Size, t.Skill, after)
		}
	}
	fmt.Fprintf(stdout, "\n%d checks, %d passed.\n", ran, passed)
	printSkipped(stdout, skipped)
	if _, more := sched.Next(); !more {
		fmt.Fprintln(stdout, "Every skill check this repo can make has run on this setup. Run `htd bench` in another repo for more evidence.")
	}
	fmt.Fprintln(stdout)
	return nil
}

// chatOnce runs one chat turn, retrying once after a short pause if the
// server errors (not on timeouts, context overflow or a model that won't
// stop thinking, which are answers).
func chatOnce(ctx context.Context, c llm.Chatter, msgs []llm.Message) (llm.Message, error) {
	reply, _, err := c.Chat(ctx, msgs, nil)
	var netErr net.Error
	if err == nil || ctx.Err() != nil || llm.IsContextOverflow(err) || errors.Is(err, llm.ErrThinking) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return reply, err
	}
	select {
	case <-ctx.Done():
		return reply, err
	case <-time.After(5 * time.Second):
	}
	reply, _, err = c.Chat(ctx, msgs, nil)
	return reply, err
}

// ctxOf is the context the server gives a model, or 0 when unknown.
func ctxOf(srv *servers.Server, model string) int {
	if m, ok := srv.Find(model); ok {
		return m.Context
	}
	return 0
}

// waiting tells the user, once, that another htd command has the model.
func waiting(w io.Writer) func() {
	return func() { fmt.Fprintln(w, "Waiting for another htd command to finish with the local model...") }
}

func printSkipped(w io.Writer, skipped map[string]int) {
	if len(skipped) == 0 {
		return
	}
	var parts []string
	for why, n := range skipped {
		parts = append(parts, fmt.Sprintf("%d %s", n, why))
	}
	sort.Strings(parts)
	fmt.Fprintf(w, "Skipped (not counted either way): %s.\n", strings.Join(parts, ", "))
}

// withConfigQuant uses the quant the user wrote in config.toml when the
// server doesn't report one, so unknown quants don't all share one key.
func withConfigQuant(r store.Rung, cfg config.Config) store.Rung {
	if len(cfg.Local) > 0 && r.Model == cfg.Local[0].Model {
		if q := cfg.Local[0].Quant; q != "" && q != "auto" && (r.Quant == "" || r.Quant == "quant unknown") {
			r.Quant = q
		}
	}
	return r
}

// remoteAllowed reports whether the configured model opted in to a server
// off this machine.
func remoteAllowed(cfg config.Config) bool {
	return len(cfg.Local) > 0 && cfg.Local[0].AllowRemote
}

// probeRung finds the server and model and returns the setup identity
// evidence is kept under.
func probeRung(ctx context.Context, url, model string, allowRemote bool) (*servers.Server, store.Rung, string, error) {
	srv, err := servers.Probe(ctx, url)
	if err != nil {
		return nil, store.Rung{}, "", err
	}
	if srv.Remote && !allowRemote {
		return nil, store.Rung{}, "", fmt.Errorf("%s isn't on this machine or your local network, so your code would be sent there; pass --allow-remote (or set allow_remote = true for it in config.toml) if that's what you want", srv.URL)
	}
	if srv.Flavor == servers.FlavorAnthropic {
		return nil, store.Rung{}, "", fmt.Errorf("%s only speaks the Anthropic API; htd needs an OpenAI-compatible /v1/chat/completions endpoint (MTPLX, Ollama, LM Studio, llama.cpp and vLLM all have one)", srv.URL)
	}
	if len(srv.Models) == 0 {
		return nil, store.Rung{}, "", fmt.Errorf("%s lists no chat models", srv.URL)
	}
	m, ok := srv.Models[0], true
	if model != "" {
		m, ok = srv.Find(model)
	}
	if !ok {
		return nil, store.Rung{}, "", fmt.Errorf("%s doesn't serve %q", srv.URL, model)
	}
	if m.Cloud {
		return nil, store.Rung{}, "", fmt.Errorf("%s: %w", m.ID, servers.ErrCloudModel)
	}
	rung := store.Rung{Model: m.ID, Quant: quantLabel(m.Quant), Server: strings.TrimSpace(srv.Flavor + " " + srv.Version), Context: m.Context, Loop: "builtin"}
	return srv, rung, m.ID, nil
}
