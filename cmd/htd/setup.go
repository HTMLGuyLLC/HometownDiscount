package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/claudesettings"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/config"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/pricing"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/servers"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/spendmap"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

// runSetup is the first-run wizard: spend map, the hometown model, then
// snapshots for replays.
// Every step can be skipped with Enter, and nothing changes until the end.
func runSetup(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := command("setup", "", "First-run wizard: your spend map, your local models, then snapshots. Your\nanswers are saved to ~/.htd/config.toml.", stdout)
	if err := fs.Parse(args); err != nil {
		return err
	}
	in := bufio.NewReader(stdin)
	ask := func(prompt, def string) string {
		fmt.Fprintf(stdout, "%s [%s] ", prompt, def)
		line, _ := in.ReadString('\n')
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
		return def
	}
	dir := store.DefaultDir()
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}
	ctx := context.Background()

	fmt.Fprintln(stdout, "Hometown Discount setup. Stop paying out-of-state fees for work your hometown can handle.")
	fmt.Fprintln(stdout)

	// 1/3: the spend map.
	prices, err := pricing.Default()
	if err != nil {
		return err
	}
	since := time.Now().AddDate(0, 0, -30)
	h, err := history.Scan(history.DefaultRoot(), since)
	if err != nil {
		return err
	}
	claudeTimes(dir, history.DefaultRoot(), h, 30, cfg.Speed.ClaudeModel, nil) // for the speed limits
	m := spendmap.Build(h, prices, since, 30)
	fmt.Fprintf(stdout, "1/3 Your spend map ..... %d subagent jobs in 30 days (%.0f%% of your Claude usage) → `htd map`\n", m.Jobs, m.Share()*100)

	// 2/3: the hometown models. The first answer is the main one; more can
	// be added, e.g. a small fast model next to a big one.
	def := defaultURL
	if len(cfg.Local) > 0 {
		def = cfg.Local[0].URL
	}
	fmt.Fprintln(stdout, "2/3 Hometown model ..... any local server with an OpenAI-compatible API (MTPLX, Splish, Ollama, LM Studio, llama.cpp, vLLM)")
	url := ask("    Server URL? (or \"none\")", def)
	if url == "none" && len(cfg.Local) > 0 {
		cfg.Local = nil // "none" means no local model, even ones set up before
		fmt.Fprintln(stdout, "    Removed the local models: htd won't use one until you run `htd setup` again.")
	}
	if url != "none" {
		if l, ok := setupModel(ctx, url, ask, stdout); ok {
			cfg.Local = placeModel(cfg.Local, 0, l)
		}
		for len(cfg.Local) > 0 {
			for _, l := range cfg.Local[1:] {
				fmt.Fprintf(stdout, "    Also set up: %s at %s\n", l.Model, l.URL)
			}
			if !strings.HasPrefix(strings.ToLower(ask("    Add another model? (a small, fast one next to a big one, say) [y/N]", "n")), "y") {
				break
			}
			if l, ok := setupModel(ctx, ask("    Server URL?", cfg.Local[len(cfg.Local)-1].URL), ask, stdout); ok {
				cfg.Local = placeModel(cfg.Local, len(cfg.Local), l)
			}
		}
		if len(cfg.Local) > 1 {
			fmt.Fprintln(stdout, "    Each task goes to the fastest model that's proven for it ([pick] in config.toml changes that).")
		}
	}

	// 3/3: snapshots, which only replays use.
	addHook := false
	if len(cfg.Local) > 0 {
		on, err := claudesettings.Has(claudesettings.Path(), "PreToolUse", isSnapshotHook)
		if err != nil {
			fmt.Fprintf(stdout, "3/3 Snapshots .......... skipped: couldn't read %s (%v)\n", claudesettings.Path(), err)
		} else if on {
			fmt.Fprintln(stdout, "3/3 Snapshots .......... on")
		} else {
			fmt.Fprintln(stdout, "3/3 Snapshots .......... save your repo's uncommitted files (text only, no secrets, kept in ~/.htd) each time")
			fmt.Fprintln(stdout, "    Claude starts a subagent, so replays see what each job saw. It adds a hook to Claude Code's settings.")
			addHook = !strings.HasPrefix(strings.ToLower(ask("    Turn on? [Y/n]", "y")), "n")
		}
	}

	if err := config.Save(dir, cfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "\nSaved %s.\n", config.Path(dir))
	if addHook {
		if _, err := claudesettings.Add(claudesettings.Path(), snapshotHook(), isSnapshotHook); err != nil {
			fmt.Fprintf(stdout, "Couldn't turn on snapshots (%v); `htd hooks on` tries again.\n", err)
		} else {
			fmt.Fprintf(stdout, "Snapshots are on (a hook in %s; `htd hooks off` stops them).\n", claudesettings.Path())
		}
	}
	if len(cfg.Local) > 0 {
		fmt.Fprintln(stdout, "Next, from inside one of your repos:")
		fmt.Fprintln(stdout, "  htd bench    skill checks on your code (minutes; a few hours for a big repo)")
		fmt.Fprintln(stdout, "  htd card     what your models have earned")
		fmt.Fprintln(stdout, "  claude mcp add --scope user htd -- htd mcp    let Claude hand them proven work")
		fmt.Fprintln(stdout, "  htd replay   replays of your real past jobs (slow: run it overnight)")
	} else {
		fmt.Fprintln(stdout, "Next: `htd map` shows where your subagent spend goes. Run `htd setup` again once a local model is running.")
	}
	return nil
}

// placeModel puts l at index i of the list (replacing what's there, or
// adding it at the end), dropping any other entry for the same model on the
// same server.
func placeModel(list []config.Local, i int, l config.Local) []config.Local {
	var out []config.Local
	for j, x := range list {
		switch {
		case j == i:
			out = append(out, l)
		case x.URL != l.URL || x.Model != l.Model:
			out = append(out, x)
		}
	}
	if i >= len(list) {
		out = append(out, l)
	}
	return out
}

// setupModel checks a server URL, lets the user pick a model, tests it and
// returns it. A server off this machine and network needs the user's OK. The
// tool-call test only matters for replays, so a model that fails it is still
// set up for skill checks and ask_local.
func setupModel(ctx context.Context, url string, ask func(string, string) string, stdout io.Writer) (config.Local, bool) {
	allowRemote := false
	if servers.IsRemote(url) {
		fmt.Fprintln(stdout, "    That host isn't on this machine or your local network: skill checks, replays and ask_local would send your code there.")
		if allowRemote = strings.HasPrefix(strings.ToLower(ask("    Allow it anyway? [y/N]", "n")), "y"); !allowRemote {
			return config.Local{}, false
		}
	}
	s, err := servers.Probe(ctx, url)
	switch {
	case err != nil:
		fmt.Fprintf(stdout, "    Couldn't reach a model server there (%v). Start your server, then run `htd setup` again.\n", err)
		return config.Local{}, false
	case s.Flavor == servers.FlavorAnthropic:
		fmt.Fprintln(stdout, "    That server only speaks the Anthropic API; htd needs an OpenAI-compatible /v1/chat/completions endpoint.")
		return config.Local{}, false
	case len(s.Models) == 0:
		fmt.Fprintln(stdout, "    The server lists no chat models. Load one, then run `htd setup` again.")
		return config.Local{}, false
	}
	mod := s.Models[0]
	if len(s.Models) > 1 {
		for i, mm := range s.Models {
			fmt.Fprintf(stdout, "    %d) %s\n", i+1, mm.ID)
		}
		for tries := 1; ; tries++ {
			if n, err := strconv.Atoi(ask("    Which model?", "1")); err == nil && n >= 1 && n <= len(s.Models) {
				mod = s.Models[n-1]
				break
			}
			if tries == 3 {
				fmt.Fprintln(stdout, "    No model picked; run `htd setup` again to choose one.")
				return config.Local{}, false
			}
			fmt.Fprintf(stdout, "    Pick a number from 1 to %d.\n", len(s.Models))
		}
	}
	fmt.Fprintf(stdout, "    found %s · %s · %s\n", mod.ID, quantLabel(mod.Quant), contextLabel(mod))
	if mod.Cloud {
		fmt.Fprintln(stdout, "    That model runs in the cloud, not on your machine, so it can't be a hometown model.")
		return config.Local{}, false
	}
	quant := "auto"
	if !mod.Quant.Known() {
		quant = ask("    Couldn't tell the quantization. Which is it? (e.g. 4bit, 8bit, Q4_K_M)", "unknown")
	}
	if mod.ContextLimited() {
		fmt.Fprintf(stdout, "    Warning: the server gives it %d tokens of context but it supports %d. Raise it to at least %d in your server's settings, or bigger checks will be skipped.\n", mod.Context, mod.MaxContext, min(servers.WantContext, mod.MaxContext))
	}
	fmt.Fprintln(stdout, "    Testing speed and tool calls (about a minute)...")
	tps, serr := servers.MeasureSpeed(ctx, s, mod.ID)
	valid, terr := servers.ToolCallTest(ctx, s, mod.ID, 20)
	switch {
	case serr != nil:
		fmt.Fprintf(stdout, "    Speed test failed (%v); saved anyway.\n", serr)
	case terr != nil || valid < 19:
		fmt.Fprintf(stdout, "    %.0f tok/s · %d of 20 tool calls valid. Replays need 19 of 20, so they may fail; skill checks and ask_local don't use tools.\n", tps, valid)
	default:
		fmt.Fprintf(stdout, "    %.0f tok/s ✓ · %d of 20 tool calls valid ✓\n", tps, valid)
	}
	return config.Local{URL: s.URL, Model: mod.ID, Quant: quant, Loop: "builtin", AllowRemote: allowRemote}, true
}
