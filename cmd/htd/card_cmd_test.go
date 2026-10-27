package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

func cardOut(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runCard(args, &out, &out); err != nil {
		t.Fatalf("runCard: %v\n%s", err, out.String())
	}
	return out.String()
}

// findRuns is n graded find replays on testRung against opus 5.5, the first
// `passes` of them passing, each taking `secs` against the original job's
// `refSecs`.
func findRuns(n, passes int, secs, refSecs float64) []store.Run {
	var runs []store.Run
	t0 := time.Now().Add(-48 * time.Hour)
	for i := 0; i < n; i++ {
		r := store.Run{
			JobID: fmt.Sprintf("find%d", i), Type: "find", Tier: "small", Reference: "claude-opus-5-5",
			Rung: testRung.ID(), Graded: true, Pass: i < passes, Seconds: secs, RefSeconds: refSecs, Turns: 3,
			At: t0.Add(time.Duration(i) * time.Minute),
		}
		if !r.Pass {
			r.Reason = "recall"
		}
		runs = append(runs, r)
	}
	return runs
}

// With nothing stored the card says so and what to run.
func TestCardEmpty(t *testing.T) {
	hermetic(t)

	out := cardOut(t)
	if !strings.Contains(out, "Nothing tested yet. Run `htd bench` in a repo (minutes), then `htd replay` (overnight).") {
		t.Errorf("headline:\n%s", out)
	}
	if strings.Contains(out, "htd card --details") {
		t.Errorf("no rows, no pointer to details:\n%s", out)
	}

	out = cardOut(t, "--details")
	if !strings.Contains(out, "Nothing tested yet. Run `htd bench` or `htd replay` with a local model to start.") {
		t.Errorf("details:\n%s", out)
	}
}

// A job type that passed enough replays, faster than the original, shows up
// under "Can take now" with its setup named; --details has every number.
func TestCardProvenReplays(t *testing.T) {
	home, _ := hermetic(t)
	addRuns(t, home, findRuns(30, 30, 10, 100)...)

	out := cardOut(t)

	for _, want := range []string{
		"Hometown Discount · what your local model has earned",
		"tiny-q4 · q4 · builtin", // the setup, in words
		"Can take now:",
		"find jobs vs Opus 5.5 (small)",
		"Every number behind this: `htd card --details`.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("headline lacks %q:\n%s", want, out)
		}
	}
	for _, not := range []string{"Too slow:", "Not proven yet:", "Not good enough:"} {
		if strings.Contains(out, not) {
			t.Errorf("headline has %q:\n%s", not, out)
		}
	}

	details := cardOut(t, "--details")
	for _, want := range []string{
		"Hometown Discount · card · all the numbers",
		"Speed (rule)", // column header
		"Job type", "Lower bound", "Bar (source)", "Goes to",
		"find vs Opus 5.5",
		"proven",
		"30 of 30",
		"0.86 (default)",
		"90% faster than Claude (limit +20%)",
		"same jobs",
	} {
		if !strings.Contains(details, want) {
			t.Errorf("details lack %q:\n%s", want, details)
		}
	}
	if !regexp.MustCompile(`proven\s+30 of 30\s+0\.\d\d`).MatchString(details) {
		t.Errorf("want the proven row with its Wilson lower bound:\n%s", details)
	}
}

// A proven job type that the local model runs much slower than the original
// job stays with Claude, and the speed rule is named.
func TestCardProvenButTooSlow(t *testing.T) {
	home, _ := hermetic(t)
	addRuns(t, home, findRuns(30, 30, 300, 100)...)

	out := cardOut(t)

	if !strings.Contains(out, "Too slow:") || !strings.Contains(out, "(vs Claude: limit +20%)") || !strings.Contains(out, "nothing proven yet") {
		t.Errorf("a slow model keeps proven work on Claude:\n%s", out)
	}
	details := cardOut(t, "--details")
	if !strings.Contains(details, "3.0× Claude's time (limit +20%)") || !strings.Contains(details, "Claude (too slow)") {
		t.Errorf("details should show the 3× pace and where it goes:\n%s", details)
	}
}

// A [speed.by_task] setting in config.toml changes where proven work goes.
func TestCardHonorsSpeedSettings(t *testing.T) {
	home, _ := hermetic(t)
	addRuns(t, home, findRuns(30, 30, 10, 100)...)
	os.WriteFile(filepath.Join(home, "config.toml"), []byte("[speed]\nmax_slower = 20.0\nclaude_model = \"auto\"\n[speed.by_task]\nfind = \"never\"\n"), 0o600)

	out := cardOut(t)
	if !strings.Contains(out, "(your setting: never local)") {
		t.Errorf("want the never-local setting named:\n%s", out)
	}
	if details := cardOut(t, "--details"); !strings.Contains(details, "Claude (your setting)") {
		t.Errorf("details:\n%s", details)
	}
}

// Failing a clear majority of replays stops a type early as "not good
// enough"; a mixed early record is still being learned.
func TestCardNotGoodEnoughAndLearning(t *testing.T) {
	home, _ := hermetic(t)
	runs := findRuns(5, 0, 10, 100) // 0 of 5
	extract := findRuns(4, 3, 10, 100)
	for i := range extract {
		extract[i].JobID, extract[i].Type = fmt.Sprintf("ex%d", i), "extract"
	}
	addRuns(t, home, append(runs, extract...)...)

	out := cardOut(t)

	if !strings.Contains(out, "Not good enough:") || !strings.Contains(out, "find jobs vs Opus 5.5 (small, 0 of 5 passed)") {
		t.Errorf("want find stopped early:\n%s", out)
	}
	if !strings.Contains(out, "Not proven yet:") || !strings.Contains(out, "extract jobs vs Opus 5.5 (small, 3 of 4 passed)") {
		t.Errorf("want extract still learning:\n%s", out)
	}
	if !strings.Contains(out, "To prove more: run `htd bench` again") {
		t.Errorf("a learning type should point at more checks:\n%s", out)
	}
}

// Skill checks and replays show side by side, each in its own words.
func TestCardSkillChecks(t *testing.T) {
	home, _ := hermetic(t)
	var runs []store.Run
	for i := 0; i < 30; i++ {
		runs = append(runs, store.Run{
			JobID: fmt.Sprintf("s%d", i), Type: "skill:lookup", Tier: "small", Rung: testRung.ID(),
			Graded: true, Pass: true, Seconds: 2, Turns: 1, At: time.Now().Add(-time.Duration(60-i) * time.Minute),
		})
	}
	addRuns(t, home, runs...)

	out := cardOut(t)
	if !strings.Contains(out, "lookup (files up to 200 lines)") {
		t.Errorf("want the lookup skill proven for small files:\n%s", out)
	}
}

// The card also reports ask_local use from the delegation log.
func TestCardDelegationSummary(t *testing.T) {
	home, _ := hermetic(t)
	os.WriteFile(filepath.Join(home, "delegations.jsonl"), []byte(strings.Join([]string{
		`{"outcome":"answered","bytes":40000,"skill":"lookup"}`,
		`{"outcome":"answered","bytes":40000,"skill":"lines"}`,
		`{"outcome":"refused","skill":"list"}`,
	}, "\n")+"\n"), 0o600)

	out := cardOut(t)

	if !regexp.MustCompile(`ask_local: 2 tasks answered locally, 1 refused.* · about [\d,]+ tokens`).MatchString(out) {
		t.Errorf("want the ask_local summary:\n%s", out)
	}
}

// Claude's times are measured from the transcripts on the first card with
// results, then reused.
func TestCardMeasuresClaudeOnce(t *testing.T) {
	home, _ := hermetic(t)
	addRuns(t, home, findRuns(30, 30, 10, 100)...)

	first := cardOut(t)
	second := cardOut(t)

	if !strings.Contains(first, "Timing Claude from your transcripts") {
		t.Errorf("the first card should say it is timing Claude:\n%s", first)
	}
	if strings.Contains(second, "Timing Claude from your transcripts") {
		t.Errorf("the second card should reuse the saved times:\n%s", second)
	}
	if !exists(filepath.Join(home, "cache", "claude-speed.json")) {
		t.Error("Claude's times should be saved in the htd cache")
	}
}

// A broken config.toml is reported with its path rather than ignored.
func TestCardBadConfig(t *testing.T) {
	home, _ := hermetic(t)
	os.WriteFile(filepath.Join(home, "config.toml"), []byte("[speed]\nclaude_model = \"\"\n"), 0o600)

	var out bytes.Buffer
	err := runCard(nil, &out, &out)

	if err == nil || !strings.Contains(err.Error(), filepath.Join(home, "config.toml")) {
		t.Errorf("want an error naming the config file, got %v", err)
	}
}

func TestCommas(t *testing.T) {
	for in, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567"} {
		if got := commas(in); got != want {
			t.Errorf("commas(%d) = %q, want %q", in, got, want)
		}
	}
}
