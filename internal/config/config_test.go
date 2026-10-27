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
