package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDelegationSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "delegations.jsonl")
	os.WriteFile(path, []byte(`{"outcome":"answered","bytes":4000}
{"outcome":"refused"}
{"outcome":"refused"}
{"outcome":"error"}
not json
`), 0o644)
	var out bytes.Buffer
	delegationSummary(&out, path)
	if want := "ask_local: 1 task answered locally, 2 refused (not proven) · about 1,000 tokens"; !strings.Contains(out.String(), want) {
		t.Errorf("got %q, want it to contain %q", out.String(), want)
	}

	out.Reset()
	delegationSummary(&out, filepath.Join(t.TempDir(), "none.jsonl"))
	if out.Len() != 0 {
		t.Errorf("no log, no line; got %q", out.String())
	}
}
