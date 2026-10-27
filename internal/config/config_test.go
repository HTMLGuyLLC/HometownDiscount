package config

import (
	"os"
	"testing"
)

func TestLoadSave(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(dir)
	if err != nil || c.Routing != "ask" || c.Calibration.CapUSD != 0 || !c.Report.SupportPrompt {
		t.Fatalf("defaults = %+v, %v", c, err)
	}
	c.Local = []Local{{URL: "http://127.0.0.1:8000", Model: "Qwen3.8-27B"}}
	if err := Save(dir, c); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(Path(dir)); st.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %v, want 0600", st.Mode().Perm())
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Local) != 1 || got.Local[0].Loop != "builtin" || got.Local[0].Quant != "auto" || got.Local[0].Model != "Qwen3.8-27B" {
		t.Errorf("round trip = %+v", got.Local)
	}

	os.WriteFile(Path(dir), []byte("routing = [broken"), 0o600)
	if _, err := Load(dir); err == nil {
		t.Error("a broken file should be an error, not silently replaced by defaults")
	}
}

func TestSpeedSettings(t *testing.T) {
	dir := t.TempDir()
	if c, _ := Load(dir); c.Speed.MaxSlower != 20 {
		t.Errorf("default limit = %v, want 20%%", c.Speed.MaxSlower)
	}
	os.WriteFile(Path(dir), []byte(`[speed]
max_slower = 10
[speed.by_task]
"lookup:small" = 5
"review:large" = 25.5
"files" = "never"
"json:medium" = "always"
`), 0o600)
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Speed.MaxSlower != 10 || c.Speed.ByTask["lookup:small"] != int64(5) || c.Speed.ByTask["review:large"] != 25.5 || c.Speed.ByTask["files"] != "never" {
		t.Errorf("speed = %+v", c.Speed)
	}

	// A typo must stop htd rather than quietly route work somewhere unexpected.
	for _, bad := range []string{`"lookpu" = 5`, `"lookup:huge" = 5`, `"lookup" = "sometimes"`, `"lookup" = true`} {
		os.WriteFile(Path(dir), []byte("[speed.by_task]\n"+bad+"\n"), 0o600)
		if _, err := Load(dir); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
}

func TestClaudeModelSetting(t *testing.T) {
	dir := t.TempDir()
	if c, _ := Load(dir); c.Speed.ClaudeModel != "auto" {
		t.Errorf("default claude_model = %q, want auto", c.Speed.ClaudeModel)
	}
	os.WriteFile(Path(dir), []byte("[speed]\nclaude_model = \"\"\n"), 0o600)
	if _, err := Load(dir); err == nil {
		t.Error("an empty claude_model should be an error")
	}
}
