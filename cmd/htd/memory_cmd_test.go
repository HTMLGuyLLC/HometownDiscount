package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/memory"
)

// fakeMemory reports each status in turn, then the last one forever.
func fakeMemory(t *testing.T, seq ...memory.Status) {
	t.Helper()
	old, oldWait, oldPoll := readMemory, memoryWait, memoryPoll
	t.Cleanup(func() { readMemory, memoryWait, memoryPoll = old, oldWait, oldPoll })
	memoryWait, memoryPoll = 300*time.Millisecond, 10*time.Millisecond
	i := 0
	readMemory = func() memory.Status {
		s := seq[min(i, len(seq)-1)]
		i++
		return s
	}
}

var (
	lowMemory = memory.Status{FreePct: 4}
	okMemory  = memory.Status{FreePct: 60}
)

// A short dip pauses the run, which then carries on and finishes.
func TestBenchPausesWhileMemoryIsLow(t *testing.T) {
	home, _ := hermetic(t)
	repo := benchRepo(t)
	chat, total := answersFor(t, repo, true)
	srv := newFakeServer(t, modelList("tiny-q4"), chat)
	fakeMemory(t, lowMemory, lowMemory, okMemory)

	out, err := bench(t, srv, repo)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"Pausing: this machine is low on memory (4% free)", "Memory recovered (60% free)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if n := len(storedRuns(t, home)); n != total {
		t.Errorf("want all %d checks after the pause, got %d", total, n)
	}
}

// Memory that stays low stops the run before a single check: more work would
// only push the machine further.
func TestBenchStopsWhenMemoryStaysLow(t *testing.T) {
	home, _ := hermetic(t)
	repo := benchRepo(t)
	chat, _ := answersFor(t, repo, true)
	srv := newFakeServer(t, modelList("tiny-q4"), chat)
	fakeMemory(t, lowMemory)

	out, err := bench(t, srv, repo)
	if !errors.Is(err, errLowMemory) {
		t.Fatalf("want errLowMemory, got %v\n%s", err, out)
	}
	if srv.chatCount() != 0 || len(storedRuns(t, home)) != 0 {
		t.Errorf("nothing should run or be stored: %d calls", srv.chatCount())
	}
}

func TestWaitForMemory(t *testing.T) {
	var w bytes.Buffer
	fakeMemory(t, okMemory)
	if !waitForMemory(context.Background(), &w) || w.Len() != 0 {
		t.Errorf("enough memory should pass silently, said %q", w.String())
	}

	fakeMemory(t, memory.Status{FreePct: 50, Pressure: true}, okMemory)
	if !waitForMemory(context.Background(), &w) || !strings.Contains(w.String(), "memory pressure") {
		t.Errorf("system pressure should pause, then pass: %q", w.String())
	}

	fakeMemory(t, memory.Status{FreePct: -1})
	if !waitForMemory(context.Background(), &w) {
		t.Error("unknown memory must never block a run")
	}

	fakeMemory(t, lowMemory)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitForMemory(ctx, &w) {
		t.Error("a stopped run shouldn't wait")
	}
}
