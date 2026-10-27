package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestForget(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HTD_HOME", dir)
	for _, f := range []string{"runs.jsonl", "config.toml", "delegations.jsonl"} {
		os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600)
	}
	os.MkdirAll(filepath.Join(dir, "answers", "r"), 0o700)

	var out bytes.Buffer
	if err := runForget(nil, strings.NewReader("n\n"), &out, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "runs.jsonl")); err != nil {
		t.Error("answering no must delete nothing")
	}
	if err := runForget([]string{"--yes"}, nil, &out, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "runs.jsonl")); !os.IsNotExist(err) {
		t.Error("runs.jsonl should be gone")
	}
	if _, err := os.Stat(filepath.Join(dir, "answers")); !os.IsNotExist(err) {
		t.Error("answers/ should be gone")
	}
	if _, err := os.Stat(filepath.Join(dir, "config.toml")); err != nil {
		t.Error("config must survive without --all")
	}
}

func TestUnknownCommand(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"nope"}, &out, &out); err == nil {
		t.Error("unknown commands should fail")
	}
	if err := run(nil, &out, &out); err != nil || !strings.Contains(out.String(), "htd setup") {
		t.Errorf("usage = %q, %v", out.String(), err)
	}
}

// --help shows a command's usage and does nothing else: setup must not run
// the wizard or save a config.
func TestHelpRunsNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HTD_HOME", dir)
	for _, cmd := range []string{"setup", "mcp", "map", "bench", "replay", "card", "probe", "gold", "regrade", "forget"} {
		var out bytes.Buffer
		if err := run([]string{cmd, "--help"}, &out, &out); !errors.Is(err, flag.ErrHelp) {
			t.Errorf("%s --help: err = %v, want flag.ErrHelp (main exits quietly on it)", cmd, err)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("--help wrote %d files to HTD_HOME", len(entries))
	}
}
