package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/config"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/delegate"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/llm"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/mcp"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/slot"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/speed"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

// runMCP serves ask_local over stdio for Claude Code, Codex or any MCP
// client: claude mcp add htd -- htd mcp
func runMCP(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: htd mcp\n\nMCP server (stdio) with the ask_local tool. Add it to Claude Code with:\n  claude mcp add htd -- htd mcp")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	dir := store.DefaultDir()
	st, err := store.Open(dir)
	if err != nil {
		return err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}
	h := &refreshing{st: st, cfg: cfg, dir: dir, h: &delegate.Handler{Root: root, Label: "no local model set up"}}
	h.h.Log = func(d delegate.Delegation) { st.LogJSON("delegations.jsonl", d) }
	home, _ := os.UserHomeDir()
	switch {
	case unsafeRoot(root, home) != "":
		h.h.Setup = fmt.Errorf("htd mcp started in %s, %s; start Claude in a project folder so only that project's files can be read", root, unsafeRoot(root, home))
	case len(cfg.Local) == 0:
		h.h.Setup = fmt.Errorf("no local model configured; run `htd setup`")
	default:
		h.connect()
	}
	return mcp.Serve(context.Background(), stdin, stdout, mcp.Info{Name: "hometown-discount", Version: version}, h)
}

// unsafeRoot says why ask_local can't serve files from root, or "" when it
// can. The home folder, every folder above it, and the hidden folders in it
// (~/.claude, ~/.ssh, ~/.aws...) hold credentials and other projects' files.
func unsafeRoot(root, home string) string {
	real := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	root = real(root)
	sep := string(filepath.Separator)
	if root == sep {
		return "the top of the disk"
	}
	if home == "" {
		return ""
	}
	home = real(home)
	switch {
	case root == home:
		return "your home folder"
	case strings.HasPrefix(home, root+sep):
		return "a folder that holds your home folder"
	}
	if rel, err := filepath.Rel(home, root); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+sep) {
		if top := strings.Split(rel, sep)[0]; strings.HasPrefix(top, ".") {
			return "a hidden folder in your home folder (" + top + ")"
		}
	}
	return ""
}

// connect probes the configured server. If the server is down when Claude
// Code starts, later calls try again (at most every 30 seconds).
func (r *refreshing) connect() {
	r.tried = time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	l := r.cfg.Local[0]
	srv, rung, modelID, err := probeRung(ctx, l.URL, l.Model, l.AllowRemote)
	if err != nil {
		r.h.Setup = err
		return
	}
	rung = withConfigQuant(rung, r.cfg)
	r.h.Context = ctxOf(srv, modelID)
	r.h.Setup = nil
	r.rungID = rung.ID()
	r.h.Label = rung.Label()
	// Wait up to 2 minutes for a replay or skill check to finish; after
	// that, tell Claude the model is busy.
	r.h.Client = slot.Locked{Inner: llm.New(srv.URL, modelID), Dir: r.dir, Wait: 2 * time.Minute} // the cleaned URL: no credentials
}

// refreshing re-reads the evidence, the speed settings and Claude's times
// before each list and call, so skills proven by a `htd bench` run, or a
// changed speed limit, apply without restarting the server.
type refreshing struct {
	listed string // the proven skills the client last saw
	st     *store.Store
	cfg    config.Config
	dir    string
	rungID string
	tried  time.Time
	h      *delegate.Handler
}

func (r *refreshing) refresh() {
	if r.rungID == "" && len(r.cfg.Local) > 0 && !r.tried.IsZero() && time.Since(r.tried) > 30*time.Second {
		r.connect()
	}
	if r.rungID == "" {
		return
	}
	if c, err := config.Load(r.dir); err == nil {
		r.cfg.Speed = c.Speed // a bad edit keeps the last good settings
	}
	claude, _ := speed.Load(r.dir) // none yet: speed isn't compared, proof decides
	if runs, err := r.st.All(); err == nil {
		r.h.Gate = delegate.Evidence(runs, r.rungID, r.cfg.Speed, claude)
	}
}

func (r *refreshing) Tools() []mcp.Tool {
	r.refresh()
	r.listed = r.h.Gate.Summary()
	return r.h.Tools()
}

// Changed reports whether newly proven skills (e.g. from a `htd bench` run
// while Claude is open) should be announced, so Claude sees them.
func (r *refreshing) Changed() bool {
	r.refresh()
	return r.h.Gate.Summary() != r.listed
}

func (r *refreshing) Call(ctx context.Context, name string, args json.RawMessage) mcp.Result {
	r.refresh()
	return r.h.Call(ctx, name, args)
}
