package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/config"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

// runForget deletes what htd stored: results, saved answers, logs and caches,
// and with --all the config too. It asks first unless --yes is given.
func runForget(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("forget", flag.ContinueOnError)
	fs.SetOutput(stderr)
	all := fs.Bool("all", false, "also delete your settings (config.toml)")
	yes := fs.Bool("yes", false, "don't ask for confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir := store.DefaultDir()
	targets := []string{"runs.jsonl", "answers", "delegations.jsonl", "cache"} // not model.lock: another htd may hold it
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
	}
	fmt.Fprintln(stdout, "Deleted.")
	return nil
}
