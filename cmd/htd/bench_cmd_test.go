package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/config"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/skills"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

// benchRepo is a small repo with source and JSON files, so skill checks of
// several kinds can be built from it.
func benchRepo(t *testing.T) string {
	t.Helper()
	repo := newRepo(t)
	os.WriteFile(filepath.Join(repo, "app.json"), []byte(`{"server": {"port": 8080}, "name": "demo"}`), 0o644)
	if out, err := exec.Command("git", "-C", repo, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}
	return repo
}

// bench runs `htd bench` on a repo against a fake server.
func bench(t *testing.T, srv *fakeServer, repo string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runBench(append([]string{"--url", srv.URL, "--repo", repo}, args...), &out, &out)
	return out.String(), err
}

// answersFor is a model that knows the right answer to every skill check
// built from repo, or answers "wrong" when `right` is false.
func answersFor(t *testing.T, repo string, right bool) (func(int, chatRequest) fakeReply, int) {
	t.Helper()
	tasks, err := skills.Generate(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) == 0 {
		t.Fatal("the fixture repo yields no skill checks")
	}
	byPrompt := map[string]string{}
	for _, task := range tasks {
		byPrompt[task.Prompt] = task.Answer
	}
	return func(_ int, r chatRequest) fakeReply {
		if !right {
			return fakeReply{Content: "wrong", Tokens: 2}
		}
		return fakeReply{Content: byPrompt[r.task()], Tokens: 10}
	}, len(tasks)
}

// Skill checks built from the user's own repo are run on the model, graded
// exactly, and stored as skill results with their size and timing. Running
// again does only what's left.
func TestBenchGradesAndStoresSkillChecks(t *testing.T) {
	home, _ := hermetic(t)
	repo := benchRepo(t)
	chat, total := answersFor(t, repo, true)
	srv := newFakeServer(t, modelList("tiny-q4"), chat)

	out, err := bench(t, srv, repo)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	runs := storedRuns(t, home)
	if len(runs) != total {
		t.Fatalf("want all %d checks stored, got %d:\n%s", total, len(runs), out)
	}
	for _, r := range runs {
		if !r.Graded || !r.Pass || !strings.HasPrefix(r.Type, skills.TypePrefix) || r.Tier == "" || r.Turns != 1 || r.Seconds <= 0 || r.Reference != "" {
			t.Errorf("a correct answer should be a graded skill pass, got %+v", r)
		}
	}
	for _, want := range []string{"Skill checks from " + filepath.Base(repo) + " on tiny-q4", "checks, ", " passed.", "Run `htd card`"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	out, err = bench(t, srv, repo)
	if err != nil || !strings.Contains(out, "No new skill checks for this setup") {
		t.Errorf("a second run has nothing left: %v\n%s", err, out)
	}
	if srv.chatCount() != total+1 { // and one question to see whether it thinks
		t.Errorf("the second run must not call the model again: %d calls for %d checks", srv.chatCount(), total)
	}
}

// A wrong answer is a recorded fail, and the answer is saved for review.
func TestBenchWrongAnswersFail(t *testing.T) {
	home, _ := hermetic(t)
	repo := benchRepo(t)
	chat, total := answersFor(t, repo, false)
	srv := newFakeServer(t, modelList("tiny-q4"), chat)

	out, err := bench(t, srv, repo)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	runs := storedRuns(t, home)
	if len(runs) == 0 || len(runs) > total {
		t.Fatalf("got %d runs for %d checks:\n%s", len(runs), total, out)
	}
	for _, r := range runs {
		if !r.Graded || r.Pass || r.Reason != "wrong answer" {
			t.Errorf("want a graded wrong-answer fail, got %+v", r)
		}
	}
	if !strings.Contains(out, "0 passed.") {
		t.Errorf("output:\n%s", out)
	}
	if !exists(filepath.Join(home, "answers")) {
		t.Error("answers should be saved for review")
	}
}

// A check bigger than the context the server gives the model is skipped, not
// failed: that's a server setting, not skill.
func TestBenchSkipsChecksThatDoNotFitTheContext(t *testing.T) {
	home, _ := hermetic(t)
	repo := benchRepo(t)
	chat, _ := answersFor(t, repo, true)
	models := []map[string]any{{"id": "tiny-q4", "object": "model", "n_ctx": 500}}
	srv := newFakeServer(t, models, chat)

	out, err := bench(t, srv, repo)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	if srv.chatCount() > 1 || len(storedRuns(t, home)) != 0 { // at most the question whether it thinks
		t.Errorf("nothing should run: %d model calls, %d runs", srv.chatCount(), len(storedRuns(t, home)))
	}
	if !strings.Contains(out, "too long for the server's context") || !strings.Contains(out, "0 checks, 0 passed.") {
		t.Errorf("output:\n%s", out)
	}
}

// --max stops after that many checks.
func TestBenchMax(t *testing.T) {
	home, _ := hermetic(t)
	repo := benchRepo(t)
	chat, total := answersFor(t, repo, true)
	if total < 2 {
		t.Skip("the fixture repo yields too few checks")
	}
	srv := newFakeServer(t, modelList("tiny-q4"), chat)

	out, err := bench(t, srv, repo, "--max", "1")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if n := len(storedRuns(t, home)); n != 1 || !strings.Contains(out, "1 checks, 1 passed.") {
		t.Errorf("--max 1 ran %d checks:\n%s", n, out)
	}
	if strings.Contains(out, "Every skill check this repo can make has run") {
		t.Errorf("checks remain, so the run isn't complete:\n%s", out)
	}
}

// The command fails clearly when the server is down or the folder isn't a repo.
func TestBenchErrors(t *testing.T) {
	hermetic(t)
	repo := benchRepo(t)
	var out bytes.Buffer

	if err := runBench([]string{"--url", closedServerURL(), "--repo", repo}, &out, &out); err == nil || !strings.Contains(err.Error(), "cannot reach") {
		t.Errorf("unreachable server: %v", err)
	}
	srv := newFakeServer(t, modelList("tiny-q4"), healthyModel)
	if _, err := bench(t, srv, t.TempDir()); err == nil {
		t.Error("a folder that isn't a git repo should fail")
	}
	if _, err := bench(t, srv, repo, "--model", "nope"); err == nil || !strings.Contains(err.Error(), `doesn't serve "nope"`) {
		t.Errorf("unknown model: %v", err)
	}
}

// The quant written in config.toml stands in for one the server can't report.
func TestWithConfigQuantAndRemoteAllowed(t *testing.T) {
	home, _ := hermetic(t)
	os.WriteFile(filepath.Join(home, "config.toml"), []byte("[[local]]\nurl = \"http://127.0.0.1:1\"\nmodel = \"mystery\"\nquant = \"Q4_K_M\"\nallow_remote = true\n"), 0o600)
	srv := newFakeServer(t, modelList("mystery"), healthyModel)

	_, rung, _, err := probeRung(t.Context(), srv.URL, "mystery", false)
	if err != nil {
		t.Fatal(err)
	}
	if rung.Quant != "quant unknown" {
		t.Fatalf("the server can't tell the quant: %+v", rung)
	}
	cfg := savedConfig(t, home)
	if got := withConfigQuant(rung, cfg).Quant; got != "Q4_K_M" {
		t.Errorf("withConfigQuant = %q, want the configured Q4_K_M", got)
	}
	if ms := localModels(flags(t), cfg, "", "", false); len(ms) != 1 || !ms[0].AllowRemote {
		t.Errorf("allow_remote = true should be honored: %+v", ms)
	}
	other := rung
	other.Model = "other"
	if got := withConfigQuant(other, cfg).Quant; got != "quant unknown" {
		t.Errorf("another model keeps its own quant, got %q", got)
	}
	known := rung
	known.Quant = "8-bit"
	if got := withConfigQuant(known, cfg).Quant; got != "8-bit" {
		t.Errorf("a quant the server reported is not overridden, got %q", got)
	}
}

// A model that thinks first is checked with thinking off too, first, and its
// results are kept apart. Asking again finds nothing left and doesn't wake
// the model.
func TestBenchChecksAThinkingModelBothWays(t *testing.T) {
	home, _ := hermetic(t)
	repo := benchRepo(t)
	answer, total := answersFor(t, repo, true)
	srv := newFakeServer(t, modelList("tiny-q4"), func(n int, r chatRequest) fakeReply {
		rep := answer(n, r)
		if r.ReasoningEffort != "none" {
			rep.Reasoning = "let me think"
		}
		return rep
	})

	out, err := bench(t, srv, repo)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	byRung := map[string]int{}
	for _, r := range storedRuns(t, home) {
		byRung[r.Rung]++
	}
	if len(byRung) != 2 {
		t.Fatalf("want results for thinking on and off, got %v", byRung)
	}
	for rung, n := range byRung {
		if n != total {
			t.Errorf("%s: %d of %d checks", rung, n, total)
		}
	}
	off := strings.Index(out, "on tiny-q4 · Q4 · builtin · thinking off")
	on := strings.Index(out, "on tiny-q4 · Q4 · builtin\n")
	if !strings.Contains(out, "thinks before it answers") || off < 0 || on < off {
		t.Errorf("want thinking off first, then on:\n%s", out)
	}

	calls := srv.chatCount()
	if out, err = bench(t, srv, repo); err != nil || srv.chatCount() != calls || !strings.Contains(out, "No new skill checks") {
		t.Errorf("nothing left: %d new calls, %v\n%s", srv.chatCount()-calls, err, out)
	}
}

// A server that ignores every way of turning thinking off is checked the
// usual way only.
func TestBenchWhenThinkingCannotBeTurnedOff(t *testing.T) {
	home, _ := hermetic(t)
	repo := benchRepo(t)
	answer, total := answersFor(t, repo, true)
	srv := newFakeServer(t, modelList("tiny-q4"), func(n int, r chatRequest) fakeReply {
		rep := answer(n, r)
		rep.Reasoning = "let me think"
		return rep
	})
	out, err := bench(t, srv, repo)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if runs := storedRuns(t, home); len(runs) != total || strings.Contains(runs[0].Rung, "none") {
		t.Errorf("want only thinking-on results, got %d runs", len(runs))
	}
	if !strings.Contains(out, "doesn't let the model answer without thinking") {
		t.Errorf("output:\n%s", out)
	}
}

// flags is bench's --url and --model, set as given ("url", "http://...").
func flags(t *testing.T, set ...string) *flag.FlagSet {
	t.Helper()
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.String("url", "", "")
	fs.String("model", "", "")
	var args []string
	for i := 0; i+1 < len(set); i += 2 {
		args = append(args, "--"+set[i], set[i+1])
	}
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	return fs
}

// Bench and replay run every configured model in order, unless one is named.
func TestLocalModels(t *testing.T) {
	cfg := config.Config{Local: []config.Local{
		{URL: "http://127.0.0.1:1234", Model: "small"},
		{URL: "http://127.0.0.1:8001", Model: "big", AllowRemote: true},
	}}
	names := func(ms []config.Local) string {
		var out []string
		for _, m := range ms {
			out = append(out, m.Model+"@"+m.URL)
		}
		return strings.Join(out, ", ")
	}
	if got := names(localModels(flags(t), cfg, "http://127.0.0.1:1234", "small", false)); got != "small@http://127.0.0.1:1234, big@http://127.0.0.1:8001" {
		t.Errorf("none named: %s", got)
	}
	if got := localModels(flags(t, "model", "big"), cfg, "http://127.0.0.1:1234", "big", false); names(got) != "big@http://127.0.0.1:8001" || !got[0].AllowRemote {
		t.Errorf("--model alone uses that model's own server and settings: %+v", got)
	}
	if got := localModels(flags(t, "url", "http://10.0.0.5:8000", "model", "big"), cfg, "http://10.0.0.5:8000", "big", false); names(got) != "big@http://10.0.0.5:8000" || got[0].AllowRemote {
		t.Errorf("another server needs its own --allow-remote: %+v", got)
	}
	if got := names(localModels(flags(t), config.Config{}, "http://127.0.0.1:8000", "", false)); got != "@http://127.0.0.1:8000" {
		t.Errorf("nothing set up: %s", got)
	}
}

// With several models set up, bench checks each in turn, each under its own
// setup; one whose server is down is skipped and the others still run.
func TestBenchChecksEachConfiguredModel(t *testing.T) {
	home, _ := hermetic(t)
	repo := benchRepo(t)
	chat, total := answersFor(t, repo, true)
	srv := newFakeServer(t, modelList("small-q4", "big-q8"), chat)
	down := newFakeServer(t, modelList("gone-q4"), chat)
	down.Close()
	cfg := config.Default()
	cfg.Local = []config.Local{{URL: srv.URL, Model: "small-q4"}, {URL: down.URL, Model: "gone-q4"}, {URL: srv.URL, Model: "big-q8"}}
	if err := config.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runBench([]string{"--repo", repo}, &out, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	byModel := map[string]int{}
	for _, r := range storedRuns(t, home) {
		var rung store.Rung
		json.Unmarshal([]byte(r.Rung), &rung)
		byModel[rung.Model]++
	}
	if byModel["small-q4"] != total || byModel["big-q8"] != total || len(byModel) != 2 {
		t.Errorf("want every check on both reachable models, got %v", byModel)
	}
	small, big := strings.Index(out.String(), "on small-q4"), strings.Index(out.String(), "on big-q8")
	if small < 0 || big < small || !strings.Contains(out.String(), "Skipped gone-q4") || !strings.Contains(out.String(), "what each model can be trusted with") {
		t.Errorf("output:\n%s", out.String())
	}
}
