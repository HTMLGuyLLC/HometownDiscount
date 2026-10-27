package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func gold(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runGold(args, &out, &out); err != nil {
		t.Fatalf("runGold: %v\n%s", err, out.String())
	}
	return out.String()
}

// goldRow is the table row for a job type, split into cells ("faithful," and
// the like stay separate words), or nil when the type has no row.
func goldRow(out, typ string) []string {
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] == typ {
			return f
		}
	}
	return nil
}

// gold counts the replayable jobs and what each has that a replay needs.
// Edit jobs aren't replayable and aren't counted, and the report holds counts
// only: no prompt or answer text.
func TestGoldCountsReplayableJobs(t *testing.T) {
	_, claude := hermetic(t)
	repo := newRepo(t)
	writeJob(t, claude, chargeJob(repo))
	writeJob(t, claude, refundJob(repo))
	edit := chargeJob(repo)
	edit.ID, edit.Tool = "edit1", "Edit"
	writeJob(t, claude, edit)

	out := gold(t)

	row := goldRow(out, "find")
	// type, replayable, prompt, answer, full file read, web recording, small predicted/actual, tier agreement, fidelity
	want := []string{"find", "2", "2", "2", "0", "0", "2/2", "100%", "-"}
	if strings.Join(row, " ") != strings.Join(want, " ") {
		t.Errorf("find row = %v, want %v\n%s", row, want, out)
	}
	if !strings.Contains(out, "Job type") || !strings.Contains(out, "Fidelity sample") {
		t.Errorf("want the table header:\n%s", out)
	}
	if goldRow(out, "edit") != nil {
		t.Errorf("edit jobs are not replayable:\n%s", out)
	}
	for _, private := range []string{"Charge", "Refund", "billing.go", "Cite file:line"} {
		if strings.Contains(out, private) {
			t.Errorf("gold must print counts only, but the output has %q:\n%s", private, out)
		}
	}
}

// --fidelity rebuilds the workspace of repo jobs and reports how many matched
// what the job saw, and how many had no repo to rebuild from.
func TestGoldFidelitySample(t *testing.T) {
	_, claude := hermetic(t)
	repo := newRepo(t)
	writeJob(t, claude, chargeJob(repo))
	gone := refundJob(repo)
	gone.Cwd = t.TempDir() // not a git repo
	writeJob(t, claude, gone)

	out := gold(t, "--fidelity", "2")

	if got := strings.Join(goldRow(out, "find"), " "); !strings.HasSuffix(got, "1/2 faithful, 1 no repo") {
		t.Errorf("fidelity cell = %q\n%s", got, out)
	}
}

// --days is the window of history to look at.
func TestGoldDaysWindow(t *testing.T) {
	_, claude := hermetic(t)
	old := chargeJob(newRepo(t))
	old.Start = time.Now().Add(-40 * 24 * time.Hour)
	writeJob(t, claude, old)

	if row := goldRow(gold(t), "find"); row != nil {
		t.Errorf("a 40-day-old job is outside the default 30 days: %v", row)
	}
	if row := goldRow(gold(t, "--days", "60"), "find"); row == nil || row[1] != "1" {
		t.Errorf("--days 60 should include it: %v", row)
	}
}

// No jobs, no rows: just the header.
func TestGoldEmpty(t *testing.T) {
	hermetic(t)
	out := gold(t)
	if !strings.HasPrefix(out, "Job type") || len(strings.Split(strings.TrimSpace(out), "\n")) != 1 {
		t.Errorf("want only the header, got %q", out)
	}
}
