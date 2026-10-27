package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/spendmap"
)

// map --json is the spend map as data: the job counted, typed and marked
// replayable.
func TestMapJSON(t *testing.T) {
	_, claude := hermetic(t)
	repo := newRepo(t)
	writeJob(t, claude, chargeJob(repo))
	writeJob(t, claude, refundJob(repo))
	var out bytes.Buffer

	if err := run([]string{"map", "--json"}, &out, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}

	var m spendmap.Map
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if m.Jobs != 2 || m.Days != 30 || len(m.Rows) != 1 {
		t.Fatalf("map = %+v, want 2 jobs over 30 days in one row", m)
	}
	if r := m.Rows[0]; r.Type != "find" || r.Jobs != 2 || r.Replayable != 2 || r.Cost <= 0 {
		t.Errorf("row = %+v, want 2 replayable find jobs with a cost", r)
	}
}

// The table version names the window and the job types and ends with a total.
func TestMapTable(t *testing.T) {
	_, claude := hermetic(t)
	writeJob(t, claude, chargeJob(newRepo(t)))
	var out bytes.Buffer

	if err := run([]string{"map", "--days", "7"}, &out, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}

	for _, want := range []string{"spend map · last 7 days", "Job type", "find", "total", "Gradable by replay"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("table lacks %q:\n%s", want, out.String())
		}
	}
}

// --projects reads another transcripts folder; with no jobs the map says so.
func TestMapProjectsFlagAndEmpty(t *testing.T) {
	_, claude := hermetic(t)
	writeJob(t, claude, chargeJob(newRepo(t)))
	empty := t.TempDir()
	var out bytes.Buffer

	if err := runMap([]string{"--projects", empty}, &out, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "No subagent jobs found in this window") {
		t.Errorf("want the empty-map message:\n%s", out.String())
	}
}

func TestMapErrors(t *testing.T) {
	hermetic(t)
	var out bytes.Buffer

	if err := runMap([]string{"--days", "0"}, &out, &out); err == nil || !strings.Contains(err.Error(), "--days must be at least 1") {
		t.Errorf("--days 0: %v", err)
	}
	// No transcripts folder isn't an error (a fresh install), but the map
	// says where it looked.
	missing := filepath.Join(t.TempDir(), "no-such-folder")
	out.Reset()
	if err := runMap([]string{"--projects", missing}, &out, &out); err != nil || !strings.Contains(out.String(), "No Claude Code transcripts found in "+missing) {
		t.Errorf("a missing transcripts folder should be named: %v\n%s", err, out.String())
	}
}

// version and help print and succeed; so do the dashed spellings.
func TestVersionAndHelp(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v"} {
		var out bytes.Buffer
		if err := run([]string{arg}, &out, &out); err != nil || out.String() != "htd "+version+"\n" {
			t.Errorf("%s: %q, %v", arg, out.String(), err)
		}
	}
	for _, arg := range []string{"help", "--help", "-h"} {
		var out bytes.Buffer
		if err := run([]string{arg}, &out, &out); err != nil || !strings.Contains(out.String(), "htd replay") || !strings.Contains(out.String(), "htd forget") {
			t.Errorf("%s: %v\n%s", arg, err, out.String())
		}
	}
	var out bytes.Buffer
	if err := run([]string{"frobnicate"}, &out, &out); err == nil || !strings.Contains(err.Error(), `unknown command "frobnicate"`) {
		t.Errorf("unknown command: %v", err)
	}
}

// run dispatches each subcommand to its handler.
func TestRunDispatch(t *testing.T) {
	hermetic(t)
	var out bytes.Buffer

	if err := run([]string{"card"}, &out, &out); err != nil || !strings.Contains(out.String(), "Nothing tested yet") {
		t.Errorf("card: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := run([]string{"gold"}, &out, &out); err != nil || !strings.HasPrefix(out.String(), "Job type") {
		t.Errorf("gold: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := run([]string{"regrade"}, &out, &out); err != nil || !strings.Contains(out.String(), "No grades changed.") {
		t.Errorf("regrade: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := run([]string{"forget", "--yes"}, &out, &out); err != nil || !strings.Contains(out.String(), "Deleted.") {
		t.Errorf("forget: %v\n%s", err, out.String())
	}
}
