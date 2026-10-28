package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/claudesettings"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/snapshot"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/workspace"
)

// snapshots is where uncommitted files are saved as subagents start.
func snapshots() *snapshot.Store {
	return snapshot.Open(filepath.Join(store.DefaultDir(), "snapshots"))
}

// jobFrom is what a replay workspace needs to know about a past job.
func jobFrom(j *history.Job) workspace.From {
	return workspace.From{Start: j.Start, Session: j.SessionID, Snapshots: snapshots()}
}

// snapshotHook is htd's Claude Code hook: it runs just before a subagent
// starts, when the subagent can't have changed anything yet.
func snapshotHook() claudesettings.Hook {
	return claudesettings.Hook{Event: "PreToolUse", Matcher: "Task|Agent", Command: htdCommand() + " snapshot", Timeout: 10}
}

// isSnapshotHook recognizes htd's hook, whatever path it calls htd by.
func isSnapshotHook(cmd string) bool {
	return claudesettings.IsCommand(cmd, "snapshot") || strings.TrimSpace(cmd) == snapshotHook().Command
}

// htdCommand is how a hook should call this htd: by name when that's the
// one on PATH, else by its full path.
func htdCommand() string {
	self, err := os.Executable()
	if err != nil {
		return "htd"
	}
	self, _ = filepath.EvalSymlinks(self)
	if onPath, err := exec.LookPath("htd"); err == nil {
		if p, _ := filepath.EvalSymlinks(onPath); p == self {
			return "htd"
		}
	}
	if strings.ContainsAny(self, " '\"") {
		return "'" + strings.ReplaceAll(self, "'", `'\''`) + "'"
	}
	return self
}

// runSnapshot is the hook itself: it saves the repo's uncommitted files so
// a later replay can see what the job saw. It must never get in the way of
// the subagent, so it prints nothing and errors are dropped.
func runSnapshot(stdin io.Reader) error {
	var in struct {
		SessionID string `json:"session_id"`
		Cwd       string `json:"cwd"`
	}
	if err := json.NewDecoder(io.LimitReader(stdin, 1<<20)).Decode(&in); err != nil || in.Cwd == "" {
		return nil
	}
	if _, err := os.Stat(store.DefaultDir()); err != nil {
		return nil // htd isn't set up, or its data was deleted
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	s := snapshots()
	_, _ = s.Take(ctx, in.Cwd, in.SessionID, time.Now())
	marker := filepath.Join(store.DefaultDir(), "snapshots", "pruned")
	if st, err := os.Stat(marker); err != nil || time.Since(st.ModTime()) > 24*time.Hour {
		_ = os.WriteFile(marker, nil, 0o600)
		_ = s.Prune(time.Now())
	}
	return nil
}

// runHooks turns snapshots on or off: htd's hook in Claude Code's settings.
func runHooks(args []string, stdout, stderr io.Writer) error {
	fs := command("hooks", "[on|off]", fmt.Sprintf("Snapshots: when on, htd saves your repo's uncommitted files (text only, no\nsecrets, kept %d days in ~/.htd) each time Claude starts a subagent, so a\nreplay sees what the job saw. They're set as a hook in %s.", int(snapshot.MaxAge.Hours()/24), claudesettings.Path()), stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	path := claudesettings.Path()
	switch fs.Arg(0) {
	case "":
		on, err := claudesettings.Has(path, "PreToolUse", isSnapshotHook)
		if err != nil {
			return err
		}
		if on {
			fmt.Fprintln(stdout, "Snapshots are on: htd saves uncommitted files as each subagent starts. `htd hooks off` stops it.")
		} else {
			fmt.Fprintln(stdout, "Snapshots are off. `htd hooks on` lets replays see uncommitted files, so more past jobs can be graded.")
		}
		return nil
	case "on":
		added, err := claudesettings.Add(path, snapshotHook(), isSnapshotHook)
		if err != nil {
			return err
		}
		if added {
			fmt.Fprintf(stdout, "Snapshots are on: added htd's hook to %s. New Claude Code sessions pick it up.\n", path)
		} else {
			fmt.Fprintln(stdout, "Snapshots were already on.")
		}
		return nil
	case "off":
		removed, err := claudesettings.Remove(path, "PreToolUse", isSnapshotHook)
		if err != nil {
			return err
		}
		if removed {
			fmt.Fprintf(stdout, "Snapshots are off: removed htd's hook from %s. Saved snapshots are kept until they expire, or `htd forget` deletes them.\n", path)
		} else {
			fmt.Fprintln(stdout, "Snapshots were already off.")
		}
		return nil
	}
	return fmt.Errorf("hooks takes on or off, not %q", fs.Arg(0))
}
