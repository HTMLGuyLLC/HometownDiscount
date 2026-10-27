package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/schedule"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

// replay runs `htd replay` against a fake server and returns its output.
func replay(t *testing.T, srv *fakeServer, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runReplay(append([]string{"--url", srv.URL}, args...), &out, &out)
	return out.String(), err
}

// answerByTask is a fake model that answers each replayed task with the
// answer for the first key its task contains.
func answerByTask(answers map[string]string) func(int, chatRequest) fakeReply {
	return func(_ int, r chatRequest) fakeReply {
		for key, ans := range answers {
			if strings.Contains(r.task(), key) {
				return fakeReply{Content: ans, Tokens: 30}
			}
		}
		return fakeReply{Content: "I don't know.", Tokens: 5}
	}
}

// The whole replay path on a tiny repo: a past find job is rebuilt from its
// transcript and the repo, the local model answers it, and the answer is
// graded against what Claude said. A right citation passes; a citation of a
// file that doesn't exist fails as made up.
func TestReplayGradesPassAndFail(t *testing.T) {
	needSandbox(t)
	home, claude := hermetic(t)
	repo := newRepo(t)
	writeJob(t, claude, chargeJob(repo))
	writeJob(t, claude, refundJob(repo))
	srv := newFakeServer(t, modelList("tiny-q4"), answerByTask(map[string]string{
		"Charge": "`Charge` is defined at `billing.go:3`.",
		"Refund": "`Refund` is defined at `ghost.go:5`.", // no such file
	}))

	out, err := replay(t, srv)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	runs := map[string]store.Run{}
	for _, r := range storedRuns(t, home) {
		runs[r.JobID] = r
	}
	if len(runs) != 2 {
		t.Fatalf("want 2 stored runs, got %d:\n%s", len(runs), out)
	}

	pass := runs["charge1"]
	if !pass.Graded || !pass.Pass || pass.Type != "find" || pass.Tier != "small" || pass.Recall != 1 || pass.Reason != "" {
		t.Errorf("the right citation should be a graded small find pass, got %+v", pass)
	}
	if pass.Reference != "claude-opus-5-5" {
		t.Errorf("reference_model = %q, want the model that wrote the original answer", pass.Reference)
	}
	if pass.RefSeconds != 90 {
		t.Errorf("ref_seconds = %v, want the original job's 90s", pass.RefSeconds)
	}
	if pass.Seconds <= 0 || pass.Turns != 1 || pass.At.IsZero() || pass.JobTime.IsZero() {
		t.Errorf("run timing missing: %+v", pass)
	}
	if !strings.Contains(pass.Rung, `"model":"tiny-q4"`) || !strings.Contains(pass.Rung, `"loop":"builtin"`) {
		t.Errorf("rung = %s", pass.Rung)
	}

	fail := runs["refund1"]
	if !fail.Graded || fail.Pass || !fail.Severe {
		t.Errorf("citing a missing file should be a graded, severe fail, got %+v", fail)
	}
	if !strings.Contains(fail.Reason, "fabrication") || len(fail.Made) == 0 || !strings.Contains(fail.Made[0], "ghost.go") {
		t.Errorf("fail reason = %q, made = %v; want the made-up file named", fail.Reason, fail.Made)
	}

	for _, want := range []string{"2 replayed, 1 passed.", "pass", "fail (recall, fabrication)", "find vs Opus 5.5", "Run `htd card`"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	// The model got the original task and a system prompt that names the repo.
	first := srv.chat(1)
	if !strings.Contains(first.task(), "Find where") || first.Messages[0].Role != "system" || !strings.Contains(first.Messages[0].Content, repo) {
		t.Errorf("the model should get the original task and the repo path, got %+v", first.Messages)
	}
	if len(first.Tools) != 2 || first.Tools[0].Function.Name != "bash" || first.Tools[1].Function.Name != "read_file" {
		t.Errorf("a repo replay offers bash and read_file only, got %+v", first.Tools)
	}
	if first.MaxTokens != replayMaxTokens {
		t.Errorf("max_tokens = %d, want %d", first.MaxTokens, replayMaxTokens)
	}

	// Each answer is saved for hand review, next to the reference.
	st, _ := store.Open(home)
	b := readFile(t, st.AnswerPath(pass.Rung, "charge1"))
	if !strings.Contains(b, "# Reference (the original answer)") || !strings.Contains(b, "`Charge` is defined at `billing.go:3`.") {
		t.Errorf("saved answer = %q", b)
	}
}

// A job that was replayed on this setup is not replayed again.
func TestReplaySkipsJobsAlreadyDone(t *testing.T) {
	needSandbox(t)
	home, claude := hermetic(t)
	writeJob(t, claude, chargeJob(newRepo(t)))
	srv := newFakeServer(t, modelList("tiny-q4"), answerByTask(map[string]string{"Charge": "`Charge` is defined at `billing.go:3`."}))

	if out, err := replay(t, srv); err != nil || !strings.Contains(out, "1 replayed, 1 passed.") {
		t.Fatalf("first replay: %v\n%s", err, out)
	}
	out, err := replay(t, srv)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "Nothing new to replay on this setup") {
		t.Errorf("want nothing new:\n%s", out)
	}
	if n := len(storedRuns(t, home)); n != 1 || srv.chatCount() != 1 {
		t.Errorf("%d runs and %d model calls; the second replay must do no work", n, srv.chatCount())
	}
}

// --types limits which kinds of jobs are replayed; --max stops early.
func TestReplayTypesAndMaxFlags(t *testing.T) {
	needSandbox(t)
	home, claude := hermetic(t)
	repo := newRepo(t)
	writeJob(t, claude, chargeJob(repo))
	writeJob(t, claude, refundJob(repo))
	srv := newFakeServer(t, modelList("tiny-q4"), answerByTask(map[string]string{
		"Charge": "`Charge` is defined at `billing.go:3`.",
		"Refund": "`Refund` is defined at `billing.go:5`.",
	}))

	out, err := replay(t, srv, "--types", "extract,review")
	if err != nil || !strings.Contains(out, "Nothing new to replay") {
		t.Errorf("no extract or review jobs: %v\n%s", err, out)
	}

	out, err = replay(t, srv, "--types", "find", "--max", "1")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "1 replayed, 1 passed.") || len(storedRuns(t, home)) != 1 {
		t.Errorf("--max 1 should replay one job:\n%s", out)
	}
}

// The model works the repo with its tools: a shell command in the sandbox and
// a file read. It sees the job's original repo paths, never the scratch copy.
func TestReplayModelUsesTools(t *testing.T) {
	needSandbox(t)
	home, claude := hermetic(t)
	repo := newRepo(t)
	writeJob(t, claude, chargeJob(repo))
	srv := newFakeServer(t, modelList("tiny-q4"), func(n int, r chatRequest) fakeReply {
		switch n {
		case 1:
			return fakeReply{Calls: []fakeCall{{Name: "bash", Args: mustJSON(map[string]string{"command": "grep -rn Charge " + repo})}}, Tokens: 10}
		case 2:
			return fakeReply{Calls: []fakeCall{{Name: "read_file", Args: mustJSON(map[string]any{"path": repo + "/billing.go", "offset": 3, "limit": 1})}}, Tokens: 10}
		}
		return fakeReply{Content: "`Charge` is defined at `billing.go:3`.", Tokens: 10}
	})

	out, err := replay(t, srv)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	shell := srv.chat(2).toolResults()
	if len(shell) != 1 || !strings.Contains(shell[0], repo+"/billing.go:3:func Charge() {}") {
		t.Errorf("the shell result should show the original repo path, got %q", shell)
	}
	read := srv.chat(3).toolResults()
	if len(read) != 2 || !strings.HasPrefix(read[1], "3\tfunc Charge() {}\n") || strings.Contains(read[1], "Refund") {
		t.Errorf("the file read should return just the numbered line asked for, got %q", read)
	}
	runs := storedRuns(t, home)
	if len(runs) != 1 || !runs[0].Pass || runs[0].Turns != 3 {
		t.Errorf("want one passing run in 3 turns, got %+v", runs)
	}
}

// A model that never stops calling tools fails the job as "too many turns":
// it's a fail for this setup, and it's recorded.
func TestReplayModelThatNeverAnswersFails(t *testing.T) {
	needSandbox(t)
	home, claude := hermetic(t)
	repo := newRepo(t)
	writeJob(t, claude, chargeJob(repo))
	srv := newFakeServer(t, modelList("tiny-q4"), func(int, chatRequest) fakeReply {
		return fakeReply{Calls: []fakeCall{{Name: "read_file", Args: mustJSON(map[string]string{"path": repo + "/billing.go"})}}, Tokens: 10}
	})

	out, err := replay(t, srv)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	runs := storedRuns(t, home)
	if len(runs) != 1 {
		t.Fatalf("want one run, got %d:\n%s", len(runs), out)
	}
	if r := runs[0]; !r.Graded || r.Pass || r.Reason != "too many turns" || r.Turns != 80 {
		t.Errorf("want a graded fail for too many turns, got %+v", r)
	}
	if !strings.Contains(out, "fail (too many turns)") {
		t.Errorf("the log should say why:\n%s", out)
	}
}

// A server error during a job isn't the model's fault: nothing is recorded
// and the job is tried again next time. A context overflow is a server
// setting, so it is skipped the same way.
func TestReplayServerProblemsAreSkippedNotRecorded(t *testing.T) {
	needSandbox(t)
	for _, tc := range []struct {
		name   string
		reply  fakeReply
		reason string
	}{
		{"server error", fakeReply{Status: 500, Err: "engine crashed"}, "1 server error"},
		{"context overflow", fakeReply{Status: 400, Err: "maximum context length exceeded"}, "1 too long for the server's context"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, claude := hermetic(t)
			writeJob(t, claude, chargeJob(newRepo(t)))
			srv := newFakeServer(t, modelList("tiny-q4"), func(int, chatRequest) fakeReply { return tc.reply })

			out, err := replay(t, srv)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if n := len(storedRuns(t, home)); n != 0 {
				t.Errorf("a skipped job must record nothing, got %d runs", n)
			}
			if !strings.Contains(out, "0 replayed, 0 passed.") || !strings.Contains(out, "Skipped (not counted either way): "+tc.reason) {
				t.Errorf("want the skip reason %q:\n%s", tc.reason, out)
			}
		})
	}
}

// Jobs that can't be judged fairly are skipped before any model time is
// spent: the repo is gone, or the original answer names nothing to check.
func TestReplaySkipsJobsItCannotGrade(t *testing.T) {
	needSandbox(t)
	home, claude := hermetic(t)
	repo := newRepo(t)
	gone := chargeJob(repo)
	gone.ID, gone.Cwd = "gone1", t.TempDir() // not a git repo
	vague := refundJob(repo)
	vague.ID, vague.Answer = "vague1", "I looked around and it seems fine."
	writeJob(t, claude, gone)
	writeJob(t, claude, vague)
	srv := newFakeServer(t, modelList("tiny-q4"), answerByTask(nil))

	out, err := replay(t, srv)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if srv.chatCount() != 0 || len(storedRuns(t, home)) != 0 {
		t.Errorf("no model time should be spent on ungradable jobs: %d calls, %d runs", srv.chatCount(), len(storedRuns(t, home)))
	}
	for _, want := range []string{"1 no repo", "1 not gradable (no-entities)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// A job that edited files, or ran project tooling, can't be replayed.
func TestReplayIgnoresEditJobs(t *testing.T) {
	needSandbox(t)
	home, claude := hermetic(t)
	edit := chargeJob(newRepo(t))
	edit.Tool = "Edit"
	writeJob(t, claude, edit)
	srv := newFakeServer(t, modelList("tiny-q4"), answerByTask(nil))

	out, err := replay(t, srv)
	if err != nil || !strings.Contains(out, "Nothing new to replay") {
		t.Errorf("an edit job is not replayable: %v\n%s", err, out)
	}
	if len(storedRuns(t, home)) != 0 || srv.chatCount() != 0 {
		t.Error("nothing should run")
	}
}

// A server that isn't running fails the command, not a job, and records nothing.
func TestReplayUnreachableServer(t *testing.T) {
	needSandbox(t)
	home, claude := hermetic(t)
	writeJob(t, claude, chargeJob(newRepo(t)))
	var out bytes.Buffer

	err := runReplay([]string{"--url", closedServerURL()}, &out, &out)

	if err == nil || !strings.Contains(err.Error(), "cannot reach") {
		t.Errorf("want a cannot-reach error, got %v", err)
	}
	if len(storedRuns(t, home)) != 0 {
		t.Error("nothing should be recorded")
	}
}

// replay uses the model named in --model, and refuses one the server doesn't serve.
func TestReplayModelFlag(t *testing.T) {
	needSandbox(t)
	_, claude := hermetic(t)
	writeJob(t, claude, chargeJob(newRepo(t)))
	srv := newFakeServer(t, modelList("alpha-4bit", "beta-8bit"), answerByTask(map[string]string{"Charge": "`Charge` is defined at `billing.go:3`."}))

	if _, err := replay(t, srv, "--model", "nope"); err == nil || !strings.Contains(err.Error(), `doesn't serve "nope"`) {
		t.Errorf("an unknown model should fail clearly, got %v", err)
	}
	if out, err := replay(t, srv, "--model", "beta-8bit"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if srv.chat(1).Model != "beta-8bit" {
		t.Errorf("replayed on %q, want beta-8bit", srv.chat(1).Model)
	}
}

// Candidates are the replayable jobs not yet done on this setup. Their size
// is relative to the median tool calls of their type, and each is keyed by the
// Claude model that wrote the original answer.
func TestReplayCandidates(t *testing.T) {
	findJob := func(id string, model string, calls int, extra map[string]int) history.Job {
		tools := map[string]int{"Grep": calls}
		for k, v := range extra {
			tools[k] = v
		}
		return history.Job{
			AgentID: id, Description: "Find the thing", Tools: tools,
			Requests: []history.Request{{Model: model}},
		}
	}
	jobs := []history.Job{
		findJob("a", "claude-opus-5-5", 1, nil),
		findJob("b", "claude-opus-5-5", 2, nil),
		findJob("c", "claude-opus-5-5", 9, nil),
		findJob("done", "claude-opus-5-5", 1, nil),
		findJob("edit", "claude-opus-5-5", 1, map[string]int{"Edit": 1}),
		{AgentID: "tests", Description: "Run the tests", Tools: map[string]int{"Bash": 1}},
	}

	got, byID := replayCandidates(jobs, map[string]bool{"done": true}, "")

	tiers := map[string]string{}
	for _, c := range got {
		tiers[c.ID] = c.Tier
		if c.Type != "find|claude-opus-5-5" {
			t.Errorf("job %s type = %q, want it keyed by the Claude model", c.ID, c.Type)
		}
	}
	// The median of 1, 1, 2, 9 tool calls (the done job still counts toward it) is 2.
	want := map[string]string{"a": "small", "b": "small", "c": "large"}
	if len(tiers) != 3 || tiers["a"] != want["a"] || tiers["b"] != want["b"] || tiers["c"] != want["c"] {
		t.Errorf("tiers = %v, want %v (done and edit jobs excluded)", tiers, want)
	}
	if byID["a"] != &jobs[0] || byID["done"] != nil {
		t.Error("byID should point at the original jobs and leave out the done one")
	}

	if none, _ := replayCandidates(jobs, nil, "extract"); len(none) != 0 {
		t.Errorf("--types extract leaves no find jobs, got %v", none)
	}
	if some, _ := replayCandidates(jobs, nil, " find , review"); len(some) != 4 {
		t.Errorf("--types with spaces should still match find jobs, got %d", len(some))
	}
}

// Prior evidence is this setup's graded outcomes per type, Claude model and
// tier, in the order they ran. Other setups and ungraded runs don't count.
func TestPriorEvidence(t *testing.T) {
	home, _ := hermetic(t)
	rung := testRung.ID()
	t0 := time.Now().Add(-time.Hour)
	addRuns(t, home,
		store.Run{JobID: "1", Type: "find", Tier: "small", Reference: "claude-opus-5-5", Rung: rung, Graded: true, Pass: true, At: t0},
		store.Run{JobID: "2", Type: "find", Tier: "small", Reference: "claude-opus-5-5", Rung: rung, Graded: true, Pass: false, At: t0.Add(time.Minute)},
		store.Run{JobID: "3", Type: "find", Tier: "small", Reference: "claude-opus-5-5", Rung: rung, Graded: false, At: t0.Add(2 * time.Minute)},
		store.Run{JobID: "4", Type: "find", Tier: "small", Reference: "claude-opus-5-5", Rung: `{"model":"other"}`, Graded: true, Pass: true, At: t0.Add(3 * time.Minute)},
		store.Run{JobID: "5", Type: "find", Tier: "", Reference: "claude-opus-5-5", Rung: rung, Graded: true, Pass: true, At: t0.Add(4 * time.Minute)},
	)
	st, _ := store.Open(home)

	prior, err := priorEvidence(st, rung)
	if err != nil {
		t.Fatal(err)
	}

	got := prior[schedule.Key{Type: "find|claude-opus-5-5", Tier: "small"}]
	if len(prior) != 1 || len(got) != 2 || !got[0] || got[1] {
		t.Errorf("prior = %v, want one tier with [pass fail] in run order", prior)
	}
}

func TestReplayLabels(t *testing.T) {
	if got := shorten("abcdef", 3); got != "abc…" {
		t.Errorf("shorten = %q", got)
	}
	if got := shorten("abc", 3); got != "abc" {
		t.Errorf("shorten of a short string = %q", got)
	}
	if got := first([]string{"a", "b", "c"}, 2); len(got) != 2 {
		t.Errorf("first = %v", got)
	}
	if got := secsLabel(42); got != "42s" {
		t.Errorf("secsLabel(42) = %q", got)
	}
	if got := secsLabel(150); got != "2.5m" {
		t.Errorf("secsLabel(150) = %q", got)
	}
	if got := refLabel("find"); got != "find" {
		t.Errorf("refLabel with no model = %q", got)
	}
	if got := refKey("find", ""); got != "find" {
		t.Errorf("refKey with no model = %q", got)
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// chatsAbout returns the chat requests whose task mentions word.
func chatsAbout(srv *fakeServer, word string) []chatRequest {
	var out []chatRequest
	for n := 1; n <= srv.chatCount(); n++ {
		if c := srv.chat(n); strings.Contains(c.task(), word) {
			out = append(out, c)
		}
	}
	return out
}

// A research job is replayed on the web pages the original job saw, and
// nothing else: the model can fetch a recorded page, can't fetch any other,
// and is graded on the pages' facts and the sources it cites.
func TestReplayResearchUsesTheRecordedPages(t *testing.T) {
	needSandbox(t)
	home, claude := hermetic(t)
	cwd := t.TempDir() // research needs no repo
	writeJob(t, claude, timeoutJob(cwd))
	writeJob(t, claude, retryJob(cwd))
	srv := newFakeServer(t, modelList("tiny-q4"), func(_ int, r chatRequest) fakeReply {
		if strings.Contains(r.task(), "timeout") {
			switch len(r.toolResults()) {
			case 0:
				return fakeReply{Calls: []fakeCall{{Name: "web_fetch", Args: mustJSON(map[string]string{"url": "https://example.com/docs/timeout"})}}, Tokens: 5}
			case 1:
				return fakeReply{Calls: []fakeCall{{Name: "web_fetch", Args: mustJSON(map[string]string{"url": "https://example.com/not-recorded"})}}, Tokens: 5}
			}
			return fakeReply{Content: "The default timeout is 30 seconds (https://example.com/docs/timeout).", Tokens: 20}
		}
		// The retry job: a made-up number and a page nobody fetched.
		return fakeReply{Content: "Failed requests are retried after 40 seconds (https://example.com/docs/backoff).", Tokens: 20}
	})

	out, err := replay(t, srv)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	runs := map[string]store.Run{}
	for _, r := range storedRuns(t, home) {
		runs[r.JobID] = r
	}
	if r := runs["timeout1"]; r.Type != "research" || !r.Graded || !r.Pass || r.Turns != 3 {
		t.Errorf("citing the recorded page and its fact should pass: %+v", r)
	}
	r := runs["retry1"]
	if !r.Graded || r.Pass || !r.Severe || !strings.Contains(r.Reason, "fabrication") {
		t.Errorf("a made-up number and source should fail as fabrication: %+v", r)
	}
	if !strings.Contains(strings.Join(r.Made, "\n"), "https://example.com/docs/backoff") {
		t.Errorf("the page nobody fetched should be named: %v", r.Made)
	}
	if !strings.Contains(out, "research vs Opus 5.5") {
		t.Errorf("output:\n%s", out)
	}

	timeout := chatsAbout(srv, "timeout")
	if len(timeout[0].Tools) != 4 {
		t.Errorf("a research replay offers shell, file, fetch and search tools, got %d", len(timeout[0].Tools))
	}
	if got := timeout[1].toolResults()[0]; got != "Acme requests time out after 30 seconds unless you set a timeout." {
		t.Errorf("the recorded page should be served as fetched, got %q", got)
	}
	if got := timeout[2].toolResults()[1]; got != "error: this page isn't available offline" {
		t.Errorf("an unrecorded page must not be fetched, got %q", got)
	}
}

// A research job with a web call that returned nothing can't be replayed
// fairly and is skipped before any model time is spent.
func TestReplaySkipsIncompleteRecordings(t *testing.T) {
	needSandbox(t)
	home, claude := hermetic(t)
	j := timeoutJob(t.TempDir())
	j.EmptyResult = true
	j.Result = ""
	writeJob(t, claude, j)
	srv := newFakeServer(t, modelList("tiny-q4"), answerByTask(nil))

	out, err := replay(t, srv)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "1 incomplete recording") || srv.chatCount() != 0 || len(storedRuns(t, home)) != 0 {
		t.Errorf("want a skip with no model time:\n%s", out)
	}
}

// A server that goes down mid-job and comes back is waited for; the job in
// flight is skipped, not counted against the model.
func TestReplayWaitsForARestartingServer(t *testing.T) {
	needSandbox(t)
	defer func(w time.Duration) { serverWait = w }(serverWait)
	serverWait = time.Second
	home, claude := hermetic(t)
	writeJob(t, claude, chargeJob(newRepo(t)))
	srv := newFakeServer(t, modelList("tiny-q4"), func(int, chatRequest) fakeReply {
		return fakeReply{Status: 503, Err: "engine is recovering", FailProbes: 1}
	})

	out, err := replay(t, srv)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"The model server isn't answering; waiting up to 1s", "It's back; carrying on.", "1 the server restarted mid-job"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if len(storedRuns(t, home)) != 0 {
		t.Error("the interrupted job must not be recorded")
	}
}

// A server that doesn't come back ends the replay with an error, keeping
// what was recorded before.
func TestReplayStopsWhenTheServerStaysDown(t *testing.T) {
	needSandbox(t)
	defer func(w time.Duration) { serverWait = w }(serverWait)
	serverWait = time.Second
	home, claude := hermetic(t)
	writeJob(t, claude, chargeJob(newRepo(t)))
	srv := newFakeServer(t, modelList("tiny-q4"), func(int, chatRequest) fakeReply {
		return fakeReply{Status: 503, Err: "engine is gone", FailProbes: 1000}
	})

	out, err := replay(t, srv)

	if err == nil || !strings.Contains(err.Error(), "replay stopped (0 done)") || !strings.Contains(err.Error(), "the server stopped answering") {
		t.Errorf("want a stopped error, got %v\n%s", err, out)
	}
	if len(storedRuns(t, home)) != 0 {
		t.Error("nothing should be recorded")
	}
}

// gradeAnswer sends each job type to its own grader and says no to types
// that have none.
func TestGradeAnswerDispatch(t *testing.T) {
	for _, tc := range []struct {
		typ, ref, cand string
		pass           bool
	}{
		{"find", "`Charge` is in `billing.go:3`.", "`Charge` is in `billing.go:3`.", true},
		{"find", "`Charge` is in `billing.go:3`.", "It is in the billing code.", false},
		{"transform", `{"a":1,"b":[1,2]}`, "```json\n{\"b\": [1, 2], \"a\": 1}\n```", true},
		{"transform", `{"a":1}`, `{"a":2}`, false},
		{"extract", "- API_KEY\n- DB_URL\n", "* db_url\n* api_key\n", true},
		{"classify", "a -> bug\nb -> feature\n", "a: bug\nb: feature\n", true},
		{"classify", "a -> bug\nb -> feature\n", "a: feature\nb: feature\n", false},
	} {
		g, ok := gradeAnswer(tc.typ, tc.ref, tc.cand, nil, nil)
		if !ok || !g.Graded || g.Pass != tc.pass {
			t.Errorf("%s %q vs %q: ok=%v graded=%v pass=%v, want pass=%v", tc.typ, tc.ref, tc.cand, ok, g.Graded, g.Pass, tc.pass)
		}
	}
	for _, typ := range []string{"review", "research"} {
		if _, ok := gradeAnswer(typ, "x", "x", nil, nil); !ok {
			t.Errorf("%s has a grader", typ)
		}
	}
	for _, typ := range []string{"edit", "explore", ""} {
		if _, ok := gradeAnswer(typ, "x", "x", nil, nil); ok {
			t.Errorf("%q has no grader", typ)
		}
	}
}
