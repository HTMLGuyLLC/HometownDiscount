package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/config"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/pricing"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/servers"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/spendmap"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

// runSetup is the first-run wizard: spend map, then the hometown model.
// Every step can be skipped with Enter, and nothing changes until the end.
func runSetup(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() {
		fmt.Fprintln(stdout, "Usage: htd setup\n\nFirst-run wizard: your spend map, then your local model. Your answers are saved\nto ~/.htd/config.toml.")
	}
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
	claudeTimes(dir, h, 30, nil) // for the speed limits
	m := spendmap.Build(h, prices, since, 30)
	fmt.Fprintf(stdout, "1/2 Your spend map ..... %d subagent jobs in 30 days (%.0f%% of your Claude usage) → `htd map`\n", m.Jobs, m.Share()*100)

	// 2/2: the hometown model.
	def := "http://127.0.0.1:8000"
	if len(cfg.Local) > 0 {
		def = cfg.Local[0].URL
	}
	fmt.Fprintln(stdout, "2/2 Hometown model ..... any local server with an OpenAI-compatible API (MTPLX, Splish, Ollama, LM Studio, llama.cpp, vLLM)")
	url := ask("    Server URL? (or \"none\")", def)
	if url != "none" {
		allowRemote := false
		if servers.IsRemote(url) {
			fmt.Fprintln(stdout, "    That host isn't on this machine or your local network: skill checks, replays and ask_local would send your code there.")
			allowRemote = strings.HasPrefix(strings.ToLower(ask("    Allow it anyway? [y/N]", "n")), "y")
			if !allowRemote {
				url = "none"
			}
		}
		if url != "none" {
			configureModel(ctx, url, allowRemote, ask, stdout, &cfg)
		}
	}

	if err := config.Save(dir, cfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "\nSaved %s.\n", config.Path(dir))
	if len(cfg.Local) > 0 {
		fmt.Fprintln(stdout, "Next, from inside one of your repos:")
		fmt.Fprintln(stdout, "  htd bench    skill checks on your code (minutes; a few hours for a big repo)")
		fmt.Fprintln(stdout, "  htd card     what your model has earned")
		fmt.Fprintln(stdout, "  claude mcp add htd -- htd mcp    let Claude hand it proven work")
		fmt.Fprintln(stdout, "  htd replay   replays of your real past jobs (slow: run it overnight)")
	} else {
		fmt.Fprintln(stdout, "Next: `htd map` shows where your subagent spend goes. Run `htd setup` again once a local model is running.")
	}
	return nil
}

// configureModel probes a server, lets the user pick a model, tests it and
// saves it. The tool-call test only matters for replays, so a model that
// fails it is still saved for skill checks and ask_local.
func configureModel(ctx context.Context, url string, allowRemote bool, ask func(string, string) string, stdout io.Writer, cfg *config.Config) {
	s, err := servers.Probe(ctx, url)
	switch {
	case err != nil:
		fmt.Fprintf(stdout, "    Couldn't reach a model server there (%v). Start your server, then run `htd setup` again.\n", err)
		return
	case s.Flavor == servers.FlavorAnthropic:
		fmt.Fprintln(stdout, "    That server only speaks the Anthropic API; htd needs an OpenAI-compatible /v1/chat/completions endpoint.")
		return
	case len(s.Models) == 0:
		fmt.Fprintln(stdout, "    The server lists no chat models. Load one, then run `htd setup` again.")
		return
	}
	mod := s.Models[0]
	if len(s.Models) > 1 {
		for i, mm := range s.Models {
			fmt.Fprintf(stdout, "    %d) %s\n", i+1, mm.ID)
		}
		if n, err := strconv.Atoi(ask("    Which model?", "1")); err == nil && n >= 1 && n <= len(s.Models) {
			mod = s.Models[n-1]
		}
	}
	fmt.Fprintf(stdout, "    found %s · %s · %s\n", mod.ID, quantLabel(mod.Quant), contextLabel(mod))
	if mod.Cloud {
		fmt.Fprintln(stdout, "    That model runs in the cloud, not on your machine, so it can't be a hometown model.")
		return
	}
	quant := "auto"
	if !mod.Quant.Known() {
		quant = ask("    Couldn't tell the quantization. Which is it? (e.g. 4bit, 8bit, Q4_K_M)", "unknown")
	}
	if mod.ContextLimited() {
		fmt.Fprintf(stdout, "    Warning: the server gives it %d tokens of context but it supports %d. Raise it in your server's settings, or bigger checks will be skipped.\n", mod.Context, mod.MaxContext)
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
	cfg.Local = []config.Local{{URL: s.URL, Model: mod.ID, Quant: quant, Loop: "builtin", AllowRemote: allowRemote}}
}
