package main

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/servers"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/slot"
)

// syncBuffer is a writer a test can read while a command is still writing.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// waitFor polls until cond holds, or fails the test.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A skill check run while the model is busy with another htd job (an
// ask_local call, say) tells the user it's waiting, and sends nothing to the
// model until the model is free.
func TestBenchWaitsForTheModel(t *testing.T) {
	home, _ := hermetic(t)
	repo := benchRepo(t)
	chat, _ := answersFor(t, repo, true)
	srv := newFakeServer(t, modelList("tiny-q4"), chat)
	release, err := slot.Acquire(context.Background(), home, 0, nil) // another htd holds the model
	if err != nil {
		t.Fatal(err)
	}
	var out syncBuffer
	done := make(chan error, 1)
	go func() { done <- runBench([]string{"--url", srv.URL, "--repo", repo, "--max", "1"}, &out, &out) }()

	waitFor(t, "the waiting notice", func() bool {
		return strings.Contains(out.String(), "Waiting for another htd command to finish with the local model...")
	})
	if srv.chatCount() != 0 {
		t.Error("the model is busy: nothing may be sent to it")
	}
	release()

	if err := <-done; err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if srv.chatCount() != 1 || !strings.Contains(out.String(), "1 checks, 1 passed.") {
		t.Errorf("the check should run once the model is free:\n%s", out.String())
	}
}

// Time a replay spends waiting for the model isn't held against the model:
// the stored time is the work, not the queue.
func TestReplayDoesNotCountTimeSpentWaitingForTheModel(t *testing.T) {
	needSandbox(t)
	home, claude := hermetic(t)
	writeJob(t, claude, chargeJob(newRepo(t)))
	srv := newFakeServer(t, modelList("tiny-q4"), answerByTask(map[string]string{"Charge": "`Charge` is defined at `billing.go:3`."}))
	release, err := slot.Acquire(context.Background(), home, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out syncBuffer
	done := make(chan error, 1)
	go func() { done <- runReplay([]string{"--url", srv.URL}, &out, &out) }()

	time.Sleep(1500 * time.Millisecond) // the replay is queued behind the other job
	if srv.chatCount() != 0 {
		t.Error("the model is busy: nothing may be sent to it")
	}
	release()

	if err := <-done; err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	runs := storedRuns(t, home)
	if len(runs) != 1 || !runs[0].Pass {
		t.Fatalf("want one pass, got %+v\n%s", runs, out.String())
	}
	if runs[0].Seconds >= 1 {
		t.Errorf("the run took %.2fs; the 1.5s spent queued should not count", runs[0].Seconds)
	}
}

func TestCtxOf(t *testing.T) {
	srv := &servers.Server{Models: []servers.Model{{ID: "known", Context: 8192}, {ID: "unknown"}}}
	if got := ctxOf(srv, "known"); got != 8192 {
		t.Errorf("ctxOf(known) = %d", got)
	}
	if got := ctxOf(srv, "unknown"); got != 0 {
		t.Errorf("ctxOf(unknown) = %d, want 0 for an unknown context", got)
	}
	if got := ctxOf(srv, "missing"); got != 0 {
		t.Errorf("ctxOf(missing) = %d, want 0", got)
	}
}

// A server that only speaks the Anthropic API, or lists nothing, can't be a
// hometown model; the error says why.
func TestProbeRungRefusals(t *testing.T) {
	hermetic(t)
	ctx := context.Background()

	anthropic := newFakeServer(t, []map[string]any{{"id": "claude-x", "type": "model"}}, healthyModel)
	if _, _, _, err := probeRung(ctx, anthropic.URL, "", false); err == nil || !strings.Contains(err.Error(), "only speaks the Anthropic API") {
		t.Errorf("anthropic: %v", err)
	}
	empty := newFakeServer(t, []map[string]any{}, healthyModel)
	if _, _, _, err := probeRung(ctx, empty.URL, "", false); err == nil || !strings.Contains(err.Error(), "lists no chat models") {
		t.Errorf("empty: %v", err)
	}
	ok := newFakeServer(t, modelList("alpha-4bit", "beta-8bit"), healthyModel)
	_, rung, id, err := probeRung(ctx, ok.URL, "beta-8bit", false)
	if err != nil || id != "beta-8bit" || rung.Model != "beta-8bit" || rung.Loop != "builtin" || rung.Quant != "8bit" {
		t.Errorf("rung = %+v, id %q, err %v", rung, id, err)
	}
}
