package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

const (
	goodCandidate = "`Charge` is defined at `billing.go:3`."
	fakeCandidate = "`Charge` is defined at `ghost.go:3`."
)

// storeReplay records a past replay of a job on testRung, with the answer the
// local model gave, as `htd replay` would have.
func storeReplay(t *testing.T, home string, r store.Run, reference, candidate string) store.Run {
	t.Helper()
	r.Rung, r.Type, r.Reference = testRung.ID(), "find", "claude-opus-5-5"
	if r.Tier == "" {
		r.Tier = "small"
	}
	if r.At.IsZero() {
		r.At = time.Now().Add(-time.Hour)
	}
	st, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Add(r); err != nil {
		t.Fatal(err)
	}
	if candidate != "" {
		if err := st.SaveAnswer(r.Rung, r.JobID, reference, candidate); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func regrade(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runRegrade(args, &out, &out); err != nil {
		t.Fatalf("runRegrade: %v\n%s", err, out.String())
	}
	return out.String()
}

// A run graded under old rules is graded again from its saved answer without
// running the model. A preview shows old and new verdicts and saves nothing;
// --apply saves the new grade, keeping the run's place in the proof order.
func TestRegradePreviewThenApply(t *testing.T) {
	home, claude := hermetic(t)
	repo := newRepo(t)
	j := chargeJob(repo)
	writeJob(t, claude, j)
	old := storeReplay(t, home, store.Run{JobID: "charge1", Graded: true, Pass: false, Reason: "recall", Recall: 0.5, Seconds: 7}, j.Answer, goodCandidate)

	out := regrade(t)

	for _, want := range []string{"charge1", "find", "was fail (recall) → now pass", "recall 0.50→1.00", "1 grade would change. Run `htd regrade --apply` to save them."} {
		if !strings.Contains(out, want) {
			t.Errorf("preview lacks %q:\n%s", want, out)
		}
	}
	if runs := storedRuns(t, home); len(runs) != 1 || runs[0].Pass || !runs[0].Regraded.IsZero() {
		t.Errorf("a preview must save nothing, store has %+v", runs)
	}

	out = regrade(t, "--apply")

	if !strings.Contains(out, "Saved 1 new grade. `htd card` uses them now.") {
		t.Errorf("apply output:\n%s", out)
	}
	runs := storedRuns(t, home)
	if len(runs) != 1 {
		t.Fatalf("the newest grade of a job replaces the old one, got %d runs", len(runs))
	}
	r := runs[0]
	if !r.Pass || !r.Graded || r.Reason != "" || r.Recall != 1 || r.Regraded.IsZero() {
		t.Errorf("the saved grade should be a pass marked regraded, got %+v", r)
	}
	if !r.At.Equal(old.At) || r.Seconds != old.Seconds || r.Tier != old.Tier {
		t.Errorf("regrading must keep the run's time, speed and tier: %+v vs %+v", r, old)
	}

	out = regrade(t)
	if !strings.Contains(out, "was pass → now pass") || !strings.Contains(out, "No grades changed.") {
		t.Errorf("a settled run should not change again:\n%s", out)
	}
}

// A candidate that cites a file that doesn't exist is graded a made-up fail;
// a run whose label stays the same but whose details change is flagged.
func TestRegradeFlagsChangedDetails(t *testing.T) {
	home, claude := hermetic(t)
	j := chargeJob(newRepo(t))
	writeJob(t, claude, j)
	storeReplay(t, home, store.Run{JobID: "charge1", Graded: true, Pass: false, Reason: "recall, fabrication", Recall: 0.5}, j.Answer, fakeCandidate)

	out := regrade(t, "--apply")

	if !strings.Contains(out, "was fail (recall, fabrication) → now fail (recall, fabrication)") || !strings.Contains(out, "(details changed)") {
		t.Errorf("want the changed-details note:\n%s", out)
	}
	r := storedRuns(t, home)[0]
	if !r.Severe || len(r.Made) != 1 || !strings.Contains(r.Made[0], "ghost.go") {
		t.Errorf("the made-up file should now be recorded, got %+v", r)
	}
}

// Runs that can't or needn't be regraded are left alone and not listed:
// skill checks (graded exactly), replays that stopped without a full answer,
// replays with no saved answer, and jobs whose transcript is gone.
func TestRegradeLeavesOtherRunsAlone(t *testing.T) {
	home, claude := hermetic(t)
	repo := newRepo(t)
	j := chargeJob(repo)
	writeJob(t, claude, j)
	noAnswer := chargeJob(repo)
	noAnswer.ID = "noans1"
	writeJob(t, claude, noAnswer)

	skill := store.Run{JobID: "skill1", Type: "skill:lookup", Graded: true, Pass: true, Rung: testRung.ID(), At: time.Now()}
	addRuns(t, home, skill)
	storeReplay(t, home, store.Run{JobID: "stopped1", Graded: true, Pass: false, Reason: "too many turns"}, j.Answer, goodCandidate)
	storeReplay(t, home, store.Run{JobID: "noans1", Graded: true, Pass: false, Reason: "recall"}, "", "")
	storeReplay(t, home, store.Run{JobID: "ghost1", Graded: true, Pass: false, Reason: "recall"}, j.Answer, goodCandidate)

	out := regrade(t, "--apply")

	for _, id := range []string{"skill1", "stopped1", "noans1", "ghost1"} {
		if strings.Contains(out, id) {
			t.Errorf("%s should be skipped:\n%s", id, out)
		}
	}
	if !strings.Contains(out, "No grades changed.") {
		t.Errorf("nothing was regradable:\n%s", out)
	}
	if n := len(storedRuns(t, home)); n != 4 {
		t.Errorf("no run should be added or lost, store has %d", n)
	}
}

// When the job's repo is gone the old grade can't be defended: the run is
// marked not graded, so it stops counting toward proof.
func TestRegradeRepoGone(t *testing.T) {
	home, claude := hermetic(t)
	j := chargeJob(newRepo(t))
	j.Cwd = t.TempDir() // no git repo here
	writeJob(t, claude, j)
	storeReplay(t, home, store.Run{JobID: "charge1", Graded: true, Pass: true, Recall: 1}, j.Answer, goodCandidate)

	out := regrade(t, "--apply")

	if !strings.Contains(out, "was pass → now not graded (no repo)") {
		t.Errorf("want the run marked not graded:\n%s", out)
	}
	if r := storedRuns(t, home)[0]; r.Graded || r.Pass || r.Reason != "no repo" {
		t.Errorf("saved %+v", r)
	}
}

// An original answer that names nothing checkable makes the old grade
// unsupportable, too.
func TestRegradeOriginalNotGradable(t *testing.T) {
	home, claude := hermetic(t)
	j := chargeJob(newRepo(t))
	j.Answer = "It is somewhere in the billing code."
	writeJob(t, claude, j)
	storeReplay(t, home, store.Run{JobID: "charge1", Graded: true, Pass: true, Recall: 1}, j.Answer, goodCandidate)

	out := regrade(t, "--apply")

	if !strings.Contains(out, "now not gradable (no-entities)") {
		t.Errorf("want not gradable:\n%s", out)
	}
	if r := storedRuns(t, home)[0]; r.Graded || r.Reason != "not gradable (no-entities)" {
		t.Errorf("saved %+v", r)
	}
}

// Nothing stored, nothing to regrade.
func TestRegradeEmptyStore(t *testing.T) {
	hermetic(t)
	if out := regrade(t); !strings.Contains(out, "No grades changed.") {
		t.Errorf("got %q", out)
	}
}

// savedCandidate reads back the replay's half of a saved answer file.
func TestSavedCandidate(t *testing.T) {
	home, _ := hermetic(t)
	st, _ := store.Open(home)
	r := store.Run{Rung: testRung.ID(), JobID: "j1"}
	if _, ok := savedCandidate(st, r); ok {
		t.Error("no saved answer should read as not found")
	}
	if err := st.SaveAnswer(r.Rung, r.JobID, "the reference", "  the candidate\nover two lines \n"); err != nil {
		t.Fatal(err)
	}
	if got, ok := savedCandidate(st, r); !ok || got != "the candidate\nover two lines" {
		t.Errorf("savedCandidate = %q, %v", got, ok)
	}
}

func TestGradeLabels(t *testing.T) {
	for _, tc := range []struct {
		r    store.Run
		want string
	}{
		{store.Run{Graded: true, Pass: true}, "pass"},
		{store.Run{Graded: true, Reason: "recall"}, "fail (recall)"},
		{store.Run{Reason: "low-anchor"}, "not graded (low-anchor)"},
		{store.Run{Reason: "not gradable (no-entities)"}, "not gradable (no-entities)"},
		{store.Run{Reason: "original fails its own grade"}, "original fails its own grade"},
		{store.Run{Reason: "no repo"}, "not graded (no repo)"},
	} {
		if got := gradeLabel(tc.r); got != tc.want {
			t.Errorf("gradeLabel(%+v) = %q, want %q", tc.r, got, tc.want)
		}
	}
	if verdict(true) != "pass" || verdict(false) != "fail" {
		t.Error("verdict")
	}
}
