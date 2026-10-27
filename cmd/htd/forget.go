package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/claudesettings"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/config"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

// runForget deletes what htd stored: results, saved answers, logs, caches
// and snapshots, and with --all the config and htd's Claude Code hook too.
// It asks first unless --yes is given.
func runForget(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("forget", flag.ContinueOnError)
	fs.SetOutput(stderr)
	all := fs.Bool("all", false, "also delete your settings (config.toml) and htd's Claude Code hook")
	yes := fs.Bool("yes", false, "don't ask for confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir := store.DefaultDir()
	targets := []string{"runs.jsonl", "answers", "delegations.jsonl", "cache", "snapshots"} // not model.lock: another htd may hold it
	if *all {
		targets = append(targets, filepath.Base(config.Path(dir)))
	}
	if !*yes {
		fmt.Fprintf(stdout, "Delete %s from %s? [y/N] ", strings.Join(targets, ", "), dir)
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(stdout, "Nothing deleted.")
			return nil
		}
	}
	for _, t := range targets {
		if err := os.RemoveAll(filepath.Join(dir, t)); err != nil {
			return err
		}
	}
	if *all {
		_ = os.Remove(dir) // only if now empty
		if removed, err := claudesettings.Remove(claudesettings.Path(), "PreToolUse", isSnapshotHook); err != nil {
			fmt.Fprintf(stderr, "Couldn't remove htd's hook from %s: %v\n", claudesettings.Path(), err)
		} else if removed {
			fmt.Fprintf(stdout, "Removed htd's hook from %s.\n", claudesettings.Path())
		}
	}
	fmt.Fprintln(stdout, "Deleted.")
	return nil
}
