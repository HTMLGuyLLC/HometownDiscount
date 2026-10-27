package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/config"
)

// seedHome fills the htd folder the way real use would: results, saved
// answers, logs, caches and settings, plus a lock file another htd may hold.
func seedHome(t *testing.T, home string) {
	t.Helper()
	for _, f := range []string{"runs.jsonl", "delegations.jsonl", "config.toml", "model.lock"} {
		if err := os.WriteFile(filepath.Join(home, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{"answers/abc", "cache/base-1", "snapshots/blobs"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(home, "answers", "abc", "job.md"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(home, "cache", "claude-speed.json"), []byte("{}"), 0o600)
}

func forget(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runForget(args, strings.NewReader(stdin), &out, &out); err != nil {
		t.Fatalf("runForget: %v\n%s", err, out.String())
	}
	return out.String()
}

// forget deletes results, saved answers, the ask_local log and caches, and
// keeps the settings and the lock file another htd may hold.
func TestForgetDeletesWhatHtdStoredButKeepsSettings(t *testing.T) {
	home, _ := hermetic(t)
	seedHome(t, home)

	out := forget(t, "y\n")

	for _, gone := range []string{"runs.jsonl", "delegations.jsonl", "answers", "cache", "snapshots"} {
		if exists(filepath.Join(home, gone)) {
			t.Errorf("%s should be deleted", gone)
		}
	}
	for _, kept := range []string{"config.toml", "model.lock"} {
		if !exists(filepath.Join(home, kept)) {
			t.Errorf("%s must be kept without --all", kept)
		}
	}
	if !strings.Contains(out, "Delete runs.jsonl, answers, delegations.jsonl, cache, snapshots from "+home+"? [y/N]") || !strings.Contains(out, "Deleted.") {
		t.Errorf("output:\n%s", out)
	}
}

// Only a clear yes deletes: "yes" and "Y" do, anything else, or no answer at
// all, deletes nothing.
func TestForgetConfirmation(t *testing.T) {
	for _, tc := range []struct {
		answer string
		delete bool
	}{{"yes\n", true}, {"Y\n", true}, {"n\n", false}, {"\n", false}, {"maybe\n", false}, {"", false}} {
		home, _ := hermetic(t)
		seedHome(t, home)

		out := forget(t, tc.answer)

		if got := !exists(filepath.Join(home, "runs.jsonl")); got != tc.delete {
			t.Errorf("answer %q: deleted = %v, want %v", tc.answer, got, tc.delete)
		}
		if !tc.delete && !strings.Contains(out, "Nothing deleted.") {
			t.Errorf("answer %q: want \"Nothing deleted.\":\n%s", tc.answer, out)
		}
	}
}

// --all --yes also deletes the settings, with no question asked. The folder
// goes too, unless something else (the lock file) is still in it.
func TestForgetAll(t *testing.T) {
	home, _ := hermetic(t)
	seedHome(t, home)

	out := forget(t, "", "--all", "--yes")

	if strings.Contains(out, "[y/N]") {
		t.Errorf("--yes must not ask:\n%s", out)
	}
	if exists(config.Path(home)) || exists(filepath.Join(home, "runs.jsonl")) || exists(filepath.Join(home, "cache")) {
		t.Error("--all should delete the settings too")
	}
	if !exists(filepath.Join(home, "model.lock")) || !exists(home) {
		t.Error("model.lock may be held by another htd: it, and so the folder, stay")
	}

	// With nothing else left, the folder itself is removed.
	home2, _ := hermetic(t)
	os.WriteFile(filepath.Join(home2, "runs.jsonl"), []byte("x"), 0o600)
	os.WriteFile(config.Path(home2), []byte("x"), 0o600)
	forget(t, "", "--all", "--yes")
	if exists(home2) {
		t.Error("an emptied htd folder should be removed")
	}
}

// The prompt names the settings file when --all is given.
func TestForgetAllPromptNamesConfig(t *testing.T) {
	home, _ := hermetic(t)
	seedHome(t, home)

	out := forget(t, "n\n", "--all")

	if !strings.Contains(out, "Delete runs.jsonl, answers, delegations.jsonl, cache, snapshots, config.toml from ") {
		t.Errorf("output:\n%s", out)
	}
	if !exists(config.Path(home)) {
		t.Error("answering no must delete nothing")
	}
}

// Forgetting when nothing was stored is fine.
func TestForgetNothingStored(t *testing.T) {
	hermetic(t)
	if out := forget(t, "", "--yes"); !strings.Contains(out, "Deleted.") {
		t.Errorf("output:\n%s", out)
	}
}

func TestForgetUnknownFlag(t *testing.T) {
	hermetic(t)
	var out bytes.Buffer
	if err := runForget([]string{"--bogus"}, strings.NewReader(""), &out, &out); err == nil {
		t.Error("an unknown flag should fail")
	}
}
