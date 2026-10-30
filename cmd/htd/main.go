// Command htd is Hometown Discount: it proves which Claude Code work your
// local models can do, on your own jobs and code, and hands them that work.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/pricing"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/spendmap"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "0.2.1"

const usage = `htd: Hometown Discount

Stop paying out-of-state fees for work your hometown can handle.

Usage:
  htd setup                          First-run wizard: spend map, your local models, then snapshots
  htd map [--days N] [--json]        Where your subagent spend goes, and what it would cost on cheaper models
  htd bench [--repo DIR]             Skill checks built from your code: what each local model can be trusted with
  htd replay [--max N]               Replay your past jobs on each local model and grade each one
  htd mcp                            MCP server: Claude hands proven small tasks to your local models
                                     (claude mcp add --scope user htd -- htd mcp)
  htd card                           What each local model has earned: proven, learning or not good enough
  htd hooks [on|off]                 Snapshots: save uncommitted files as subagents start, so replays see what each job saw
  htd forget [--all]                 Delete what htd stored in ~/.htd (--all also deletes your settings)
  htd version                        Print the version

Advanced: htd probe [--url URL] (check a server, yours by default), htd gold (how many past jobs can be replayed),
          htd regrade (re-grade saved answers). Every command takes --help.
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return // --help already printed the command's usage
		}
		fmt.Fprintln(os.Stderr, "htd:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return nil
	}
	switch args[0] {
	case "map":
		return runMap(args[1:], stdout, stderr)
	case "gold":
		return runGold(args[1:], stdout, stderr)
	case "probe":
		return runProbe(args[1:], stdout, stderr)
	case "setup":
		return runSetup(args[1:], os.Stdin, stdout)
	case "replay":
		return runReplay(args[1:], stdout, stderr)
	case "forget":
		return runForget(args[1:], os.Stdin, stdout, stderr)
	case "mcp":
		return runMCP(args[1:], os.Stdin, stdout, stderr)
	case "bench":
		return runBench(args[1:], stdout, stderr)
	case "regrade":
		return runRegrade(args[1:], stdout, stderr)
	case "card":
		return runCard(args[1:], stdout, stderr)
	case "hooks":
		return runHooks(args[1:], stdout, stderr)
	case "snapshot":
		return runSnapshot(os.Stdin)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "htd", version)
		return nil
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q (run `htd help`)", args[0])
	}
}

// command is a subcommand's flags with help in one style: what to type, what
// it does, then its flags.
func command(name, args, about string, w io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(w)
	fs.Usage = func() {
		fmt.Fprintf(w, "Usage: htd %s\n\n%s\n", strings.TrimSpace(name+" "+args), about)
		flags := false
		fs.VisitAll(func(*flag.Flag) { flags = true })
		if flags {
			fmt.Fprintln(w, "\nFlags:")
			fs.PrintDefaults()
		}
	}
	return fs
}

func runMap(args []string, stdout, stderr io.Writer) error {
	fs := command("map", "[--days N] [--json]", "Where your subagent spend goes: jobs, cost, and what they'd cost on cheaper\nmodels, per job type, from your Claude Code transcripts.", stderr)
	days := fs.Int("days", 30, "how many days of history to include")
	asJSON := fs.Bool("json", false, "print JSON instead of a table")
	root := fs.String("projects", history.DefaultRoot(), "Claude Code transcripts folder")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *days < 1 {
		return fmt.Errorf("--days must be at least 1")
	}

	prices, err := pricing.Default()
	if err != nil {
		return err
	}
	since := time.Now().AddDate(0, 0, -*days)
	h, err := history.Scan(*root, since)
	if err != nil {
		return fmt.Errorf("read transcripts in %s: %w", *root, err)
	}
	if _, err := os.Stat(*root); os.IsNotExist(err) && !*asJSON {
		fmt.Fprintf(stdout, "No Claude Code transcripts found in %s. Once you've used Claude Code, run `htd map` again.\n", *root)
		return nil
	}
	m := spendmap.Build(h, prices, since, *days)
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(m)
	}
	m.Render(stdout)
	return nil
}
