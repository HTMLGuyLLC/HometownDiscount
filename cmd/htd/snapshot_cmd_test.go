package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/claudesettings"
)

// htd hooks on/off adds and removes the hook in Claude Code's settings, and
// says where things stand.
func TestHooksOnOff(t *testing.T) {
	_, claude := hermetic(t)
	settings := filepath.Join(claude, "settings.json")
	os.WriteFile(settings, []byte(`{"model": "opus"}`), 0o600)
	do := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := run(append([]string{"hooks"}, args...), &out, &out); err != nil {
			t.Fatalf("hooks %v: %v\n%s", args, err, out.String())
		}
		return out.String()
	}
	if out := do(); !strings.Contains(out, "Snapshots are off") {
		t.Errorf("status: %s", out)
	}
	if out := do("on"); !strings.Contains(out, "Snapshots are on: added htd's hook to "+settings) {
		t.Errorf("on: %s", out)
	}
	b, _ := os.ReadFile(settings)
	if !strings.Contains(string(b), `"model": "opus"`) || !strings.Contains(string(b), ` snapshot"`) || !strings.Contains(string(b), `"Task|Agent"`) {
		t.Errorf("settings after on:\n%s", b)
	}
	if out := do("on"); !strings.Contains(out, "already on") {
		t.Errorf("second on: %s", out)
	}
	if out := do(); !strings.Contains(out, "Snapshots are on") {
		t.Errorf("status: %s", out)
	}
	if out := do("off"); !strings.Contains(out, "Snapshots are off: removed") {
		t.Errorf("off: %s", out)
	}
	if on, _ := claudesettings.Has(settings, "PreToolUse", isSnapshotHook); on {
		t.Error("hook still set after off")
	}
	var out bytes.Buffer
	if err := run([]string{"hooks", "sideways"}, &out, &out); err == nil {
		t.Error("an unknown argument should fail")
	}
}

// The hook saves the repo's uncommitted files under the session that started
// the subagent, prints nothing, and never fails, even on junk input.
func TestSnapshotHook(t *testing.T) {
	home, _ := hermetic(t)
	repo := newRepo(t)
	os.WriteFile(filepath.Join(repo, "draft.md"), []byte("# Draft\n"), 0o644)

	in := `{"session_id": "sess-9", "cwd": "` + repo + `", "hook_event_name": "PreToolUse", "tool_name": "Task"}`
	if err := runSnapshot(strings.NewReader(in)); err != nil {
		t.Fatal(err)
	}
	m, ok := snapshots().Find(repo, "sess-9", time.Now())
	if !ok || m.Files["draft.md"] == "" {
		t.Fatalf("want draft.md saved, got %v %+v", ok, m)
	}
	if _, err := os.Stat(filepath.Join(home, "snapshots", "pruned")); err != nil {
		t.Error("the first snapshot of the day should prune old ones")
	}
	for _, junk := range []string{"", "not json", `{"cwd": "/no/such/folder"}`} {
		if err := runSnapshot(strings.NewReader(junk)); err != nil {
			t.Errorf("%q: %v", junk, err)
		}
	}
}

// With htd's folder gone (never set up, or forgotten), the hook saves
// nothing rather than recreating it.
func TestSnapshotHookWithoutHtd(t *testing.T) {
	home, _ := hermetic(t)
	os.RemoveAll(home)
	repo := newRepo(t)
	os.WriteFile(filepath.Join(repo, "draft.md"), []byte("# Draft\n"), 0o644)
	runSnapshot(strings.NewReader(`{"session_id": "s", "cwd": "` + repo + `"}`))
	if _, err := os.Stat(home); err == nil {
		t.Error("the hook recreated htd's folder")
	}
}

// forget --all also takes htd's hook out of Claude Code's settings.
func TestForgetAllRemovesHook(t *testing.T) {
	_, claude := hermetic(t)
	settings := filepath.Join(claude, "settings.json")
	if _, err := claudesettings.Add(settings, snapshotHook(), isSnapshotHook); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"forget", "--all", "--yes"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if on, _ := claudesettings.Has(settings, "PreToolUse", isSnapshotHook); on || !strings.Contains(out.String(), "Removed htd's hook") {
		t.Errorf("hook should be gone: %s", out.String())
	}
}
