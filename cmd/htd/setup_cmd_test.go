package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/claudesettings"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/config"
)

// setup runs the wizard with the given stdin answers and returns its output.
func setup(t *testing.T, answers string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runSetup(nil, strings.NewReader(answers), &out); err != nil {
		t.Fatalf("runSetup: %v\n%s", err, out.String())
	}
	return out.String()
}

// savedConfig reads back the config the wizard saved in home.
func savedConfig(t *testing.T, home string) config.Config {
	t.Helper()
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatalf("saved config doesn't load: %v", err)
	}
	return cfg
}

// A server URL and a model choice end up in config.toml, the model is tested
// for speed and tool calls, and the user is pointed at what to run next.
func TestSetupSavesTheChosenModel(t *testing.T) {
	for _, tc := range []struct{ pick, want string }{{"1", "alpha-4bit"}, {"2", "beta-8bit"}} {
		t.Run("pick "+tc.pick, func(t *testing.T) {
			home, _ := hermetic(t)
			srv := newFakeServer(t, modelList("alpha-4bit", "beta-8bit"), healthyModel)

			out := setup(t, srv.URL+"\n"+tc.pick+"\n")

			cfg := savedConfig(t, home)
			if len(cfg.Local) != 1 {
				t.Fatalf("want one local model, got %+v\n%s", cfg.Local, out)
			}
			l := cfg.Local[0]
			if l.URL != srv.URL || l.Model != tc.want || l.Quant != "auto" || l.Loop != "builtin" || l.AllowRemote {
				t.Errorf("saved %+v, want %s on %s (quant auto, builtin loop, not remote)", l, tc.want, srv.URL)
			}
			for _, want := range []string{"1) alpha-4bit", "2) beta-8bit", "found " + tc.want, "20 of 20 tool calls valid", "Saved " + config.Path(home), "htd bench"} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
		})
	}
}

// Answering "none" saves the config with no local model and says what to do
// instead. The server isn't contacted.
func TestSetupNoneSavesNoLocalModel(t *testing.T) {
	home, _ := hermetic(t)
	srv := newFakeServer(t, modelList("alpha-4bit"), healthyModel)

	out := setup(t, "none\n")

	if cfg := savedConfig(t, home); len(cfg.Local) != 0 {
		t.Errorf("none must save no local model, got %+v", cfg.Local)
	}
	if !exists(config.Path(home)) {
		t.Error("the config file should still be written")
	}
	if !strings.Contains(out, "Run `htd setup` again once a local model is running") || strings.Contains(out, "htd bench") {
		t.Errorf("want the no-model next step:\n%s", out)
	}
	if n := srv.requestCount(); n != 0 {
		t.Errorf("none must not talk to a server, but it got %d requests", n)
	}
}

// Pressing Enter at the prompt keeps the server already in config.toml.
func TestSetupEnterKeepsTheConfiguredServer(t *testing.T) {
	home, _ := hermetic(t)
	srv := newFakeServer(t, modelList("alpha-4bit"), healthyModel)
	if err := config.Save(home, config.Config{
		Speed: config.Default().Speed,
		Local: []config.Local{{URL: srv.URL, Model: "old-model", Quant: "auto", Loop: "builtin"}},
	}); err != nil {
		t.Fatal(err)
	}

	setup(t, "\n")

	cfg := savedConfig(t, home)
	if len(cfg.Local) != 1 || cfg.Local[0].URL != srv.URL || cfg.Local[0].Model != "alpha-4bit" {
		t.Errorf("Enter should re-test the configured server and pick its model, got %+v", cfg.Local)
	}
}

// Settings the wizard doesn't ask about survive it.
func TestSetupKeepsSpeedSettings(t *testing.T) {
	home, _ := hermetic(t)
	os.WriteFile(config.Path(home), []byte("[pick]\nprefer = \"listed\"\n[speed]\nmax_slower = 50.0\nclaude_model = \"claude-opus-5-5\"\n[speed.by_task]\nlookup = \"never\"\n"), 0o600)

	setup(t, "none\n")

	cfg := savedConfig(t, home)
	if cfg.Pick.Prefer != "listed" || cfg.Speed.MaxSlower != 50 || cfg.Speed.ClaudeModel != "claude-opus-5-5" || cfg.Speed.ByTask["lookup"] != "never" {
		t.Errorf("setup dropped existing settings: %+v", cfg)
	}
}

// A server that isn't running is reported, not fatal, and nothing is saved
// as the local model.
func TestSetupUnreachableServer(t *testing.T) {
	home, _ := hermetic(t)

	out := setup(t, closedServerURL()+"\n")

	if !strings.Contains(out, "Couldn't reach a model server there") {
		t.Errorf("want an unreachable message:\n%s", out)
	}
	if cfg := savedConfig(t, home); len(cfg.Local) != 0 {
		t.Errorf("an unreachable server must not be saved: %+v", cfg.Local)
	}
}

// A server that only speaks the Anthropic API can't run htd's checks.
func TestSetupAnthropicOnlyServer(t *testing.T) {
	home, _ := hermetic(t)
	srv := newFakeServer(t, []map[string]any{{"id": "claude-x", "type": "model"}}, healthyModel)

	out := setup(t, srv.URL+"\n")

	if !strings.Contains(out, "only speaks the Anthropic API") {
		t.Errorf("want the Anthropic message:\n%s", out)
	}
	if cfg := savedConfig(t, home); len(cfg.Local) != 0 {
		t.Errorf("saved %+v", cfg.Local)
	}
}

// A server with nothing loaded is reported, and nothing is saved.
func TestSetupServerWithNoModels(t *testing.T) {
	home, _ := hermetic(t)
	srv := newFakeServer(t, []map[string]any{}, healthyModel)

	out := setup(t, srv.URL+"\n")

	if !strings.Contains(out, "lists no chat models") {
		t.Errorf("want a no-models message:\n%s", out)
	}
	if cfg := savedConfig(t, home); len(cfg.Local) != 0 {
		t.Errorf("saved %+v", cfg.Local)
	}
}

// A server off this machine and network is not contacted unless the user
// says yes; answering no drops it.
func TestSetupRemoteServerNeedsOptIn(t *testing.T) {
	home, _ := hermetic(t)

	out := setup(t, "http://models.example.com:8000\nn\n")

	if !strings.Contains(out, "isn't on this machine or your local network") || !strings.Contains(out, "Allow it anyway?") {
		t.Errorf("want the remote warning and the question:\n%s", out)
	}
	if strings.Contains(out, "Couldn't reach") {
		t.Errorf("a refused remote server must not be probed:\n%s", out)
	}
	if cfg := savedConfig(t, home); len(cfg.Local) != 0 {
		t.Errorf("saved %+v", cfg.Local)
	}
}

// A model whose quantization can't be read from its name is asked about once,
// and the answer is saved.
func TestSetupAsksForAnUnknownQuant(t *testing.T) {
	home, _ := hermetic(t)
	srv := newFakeServer(t, modelList("mystery"), healthyModel)

	out := setup(t, srv.URL+"\nQ4_K_M\n")

	if !strings.Contains(out, "Couldn't tell the quantization") {
		t.Errorf("want the quant question:\n%s", out)
	}
	if cfg := savedConfig(t, home); len(cfg.Local) != 1 || cfg.Local[0].Quant != "Q4_K_M" {
		t.Errorf("saved %+v, want quant Q4_K_M", cfg.Local)
	}
}

// A server that gives a model less context than it supports gets a warning,
// and the model is still saved.
func TestSetupWarnsAboutLimitedContext(t *testing.T) {
	home, _ := hermetic(t)
	models := []map[string]any{{"id": "big-4bit", "object": "model", "n_ctx": 4096, "meta": map[string]any{"n_ctx_train": 262144}}}
	srv := newFakeServer(t, models, healthyModel)

	out := setup(t, srv.URL+"\n")

	if !strings.Contains(out, "Warning: the server gives it 4096 tokens of context but it supports 262144") {
		t.Errorf("want the context warning:\n%s", out)
	}
	if cfg := savedConfig(t, home); len(cfg.Local) != 1 || cfg.Local[0].Model != "big-4bit" {
		t.Errorf("saved %+v", cfg.Local)
	}
}

// A model that can't make tool calls is still saved (skill checks and
// ask_local don't use tools), with a note that replays may fail.
func TestSetupSavesAModelWithoutToolCalls(t *testing.T) {
	home, _ := hermetic(t)
	srv := newFakeServer(t, modelList("alpha-4bit"), noToolCalls)

	out := setup(t, srv.URL+"\n")

	if !strings.Contains(out, "0 of 20 tool calls valid. Replays need 19 of 20") {
		t.Errorf("want the tool-call warning:\n%s", out)
	}
	if cfg := savedConfig(t, home); len(cfg.Local) != 1 || cfg.Local[0].Model != "alpha-4bit" {
		t.Errorf("saved %+v", cfg.Local)
	}
}

// A model whose speed test fails is saved anyway.
func TestSetupSavesAModelWhenTheSpeedTestFails(t *testing.T) {
	home, _ := hermetic(t)
	srv := newFakeServer(t, modelList("alpha-4bit"), func(int, chatRequest) fakeReply {
		return fakeReply{Status: 500, Err: "engine crashed"}
	})

	out := setup(t, srv.URL+"\n")

	if !strings.Contains(out, "Speed test failed") || !strings.Contains(out, "saved anyway") {
		t.Errorf("want the speed failure note:\n%s", out)
	}
	if cfg := savedConfig(t, home); len(cfg.Local) != 1 {
		t.Errorf("saved %+v", cfg.Local)
	}
}

// The wizard's first step reports the spend map from the user's transcripts.
func TestSetupReportsTheSpendMap(t *testing.T) {
	_, claude := hermetic(t)
	writeJob(t, claude, chargeJob(newRepo(t)))

	out := setup(t, "none\n")

	if !strings.Contains(out, "1/3 Your spend map ..... 1 subagent jobs in 30 days") {
		t.Errorf("want the job counted in the spend map line:\n%s", out)
	}
}

// A machine with no Claude Code transcripts folder (a Codex-only user, or a
// fresh install) can still set up its local model.
func TestSetupWithoutATranscriptsFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HTD_HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "nothing-here"))
	var out bytes.Buffer
	if err := runSetup(nil, strings.NewReader("none\n"), &out); err != nil {
		t.Fatalf("setup without a transcripts folder: %v", err)
	}
	if !exists(config.Path(home)) {
		t.Error("the config should be saved")
	}
}

// "none" removes a model that was set up before: it means no local model.
func TestSetupNoneRemovesTheConfiguredModel(t *testing.T) {
	home, _ := hermetic(t)
	srv := newFakeServer(t, modelList("alpha-4bit"), healthyModel)
	setup(t, srv.URL+"\n")
	if cfg := savedConfig(t, home); len(cfg.Local) != 1 {
		t.Fatalf("first setup should save the model: %+v", cfg.Local)
	}
	out := setup(t, "none\n")
	if cfg := savedConfig(t, home); len(cfg.Local) != 0 {
		t.Errorf("\"none\" should remove the model, still have %+v\n%s", cfg.Local, out)
	}
	if !strings.Contains(out, "Removed the local model") {
		t.Errorf("say what happened:\n%s", out)
	}
}

// A model number out of range is asked again rather than quietly read as 1;
// three bad answers pick nothing.
func TestSetupAsksAgainForABadModelNumber(t *testing.T) {
	home, _ := hermetic(t)
	srv := newFakeServer(t, modelList("alpha-4bit", "beta-8bit"), healthyModel)
	out := setup(t, srv.URL+"\n5\n2\n")
	if cfg := savedConfig(t, home); len(cfg.Local) != 1 || cfg.Local[0].Model != "beta-8bit" {
		t.Errorf("want beta-8bit after a second try, got %+v\n%s", cfg.Local, out)
	}
	if !strings.Contains(out, "Pick a number from 1 to 2") {
		t.Errorf("say what's allowed:\n%s", out)
	}

	home, _ = hermetic(t)
	out = setup(t, srv.URL+"\n5\nx\n0\n")
	if cfg := savedConfig(t, home); len(cfg.Local) != 0 {
		t.Errorf("three bad answers should pick nothing, got %+v\n%s", cfg.Local, out)
	}
}

// With a local model set, the wizard offers snapshots: Enter turns them on
// (htd's hook in Claude Code's settings), "n" leaves the settings alone, and
// a later run just says they're on.
func TestSetupOffersSnapshots(t *testing.T) {
	_, claude := hermetic(t)
	settings := filepath.Join(claude, "settings.json")
	srv := newFakeServer(t, modelList("alpha-4bit"), healthyModel)

	out := setup(t, srv.URL+"\nn\nn\n") // no other model, no snapshots
	if !strings.Contains(out, "3/3 Snapshots") {
		t.Errorf("snapshots not offered:\n%s", out)
	}
	if _, err := os.Stat(settings); err == nil {
		t.Error("\"n\" must not touch Claude Code's settings")
	}

	out = setup(t, srv.URL+"\n\n")
	if on, _ := claudesettings.Has(settings, "PreToolUse", isSnapshotHook); !on || !strings.Contains(out, "Snapshots are on") {
		t.Errorf("Enter should turn snapshots on:\n%s", out)
	}
	if out = setup(t, srv.URL+"\n"); !strings.Contains(out, "3/3 Snapshots .......... on") {
		t.Errorf("a later run should just say they're on:\n%s", out)
	}

	// No local model: replays can't run, so snapshots aren't offered.
	hermetic(t)
	if out := setup(t, "none\n"); strings.Contains(out, "3/3") {
		t.Errorf("snapshots offered without a model:\n%s", out)
	}
}

// More models can be added after the main one: each is tested and saved in
// order, a later run keeps them, and "none" removes them all.
func TestSetupAddsAnotherModel(t *testing.T) {
	home, _ := hermetic(t)
	srv := newFakeServer(t, modelList("alpha-4bit", "beta-8bit"), healthyModel)

	out := setup(t, srv.URL+"\n1\ny\n\n2\nn\nn\n")
	cfg := savedConfig(t, home)
	if len(cfg.Local) != 2 || cfg.Local[0].Model != "alpha-4bit" || cfg.Local[1].Model != "beta-8bit" || cfg.Local[1].URL != srv.URL {
		t.Fatalf("want alpha then beta, got %+v\n%s", cfg.Local, out)
	}
	if !strings.Contains(out, "Also set up: beta-8bit") || !strings.Contains(out, "fastest model that's proven") {
		t.Errorf("output:\n%s", out)
	}

	// Picking the second model as the main one doesn't list it twice.
	out = setup(t, "\n2\nn\nn\n")
	if cfg := savedConfig(t, home); len(cfg.Local) != 1 || cfg.Local[0].Model != "beta-8bit" {
		t.Errorf("want just beta, got %+v\n%s", cfg.Local, out)
	}

	setup(t, srv.URL+"\n1\ny\n\n2\nn\nn\n")
	if out := setup(t, "none\n"); len(savedConfig(t, home).Local) != 0 || !strings.Contains(out, "Removed the local models") {
		t.Errorf("none removes every model:\n%s", out)
	}
}

func TestPlaceModel(t *testing.T) {
	a := config.Local{URL: "u1", Model: "a"}
	b := config.Local{URL: "u1", Model: "b"}
	c := config.Local{URL: "u2", Model: "c"}
	names := func(ls []config.Local) (s string) {
		for _, l := range ls {
			s += l.Model
		}
		return s
	}
	for _, tc := range []struct {
		list []config.Local
		i    int
		l    config.Local
		want string
	}{
		{nil, 0, a, "a"},
		{[]config.Local{a, b}, 0, c, "cb"},
		{[]config.Local{a, b}, 2, c, "abc"},
		{[]config.Local{a, b}, 0, b, "b"},       // the new main model was listed after it
		{[]config.Local{a, b}, 2, a, "b" + "a"}, // added again: moves to the end
	} {
		if got := names(placeModel(tc.list, tc.i, tc.l)); got != tc.want {
			t.Errorf("placeModel(%s, %d, %s) = %s, want %s", names(tc.list), tc.i, tc.l.Model, got, tc.want)
		}
	}
}
