package main

import (
	"context"
	"encoding/json"
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
// client: claude mcp add --scope user htd -- htd mcp
func runMCP(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := command("mcp", "", "MCP server (stdio) with the ask_local tool. Add it to Claude Code, for every\nproject, with:\n  claude mcp add --scope user htd -- htd mcp", stderr)
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

// connect probes every configured model. A model whose server is down is
// left out, and later calls try again (at most every 30 seconds); the others
// work meanwhile.
func (r *refreshing) connect() {
	r.tried = time.Now()
	r.rungIDs, r.missing = nil, 0
	models := map[string]delegate.Model{}
	var labels []string
	var failed error
	for _, l := range r.cfg.Local {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		srv, rung, modelID, err := probeRung(ctx, l.URL, l.Model, l.AllowRemote)
		cancel()
		if err != nil {
			failed = err
			r.missing++
			continue
		}
		rung = withConfigQuant(rung, r.cfg)
		off := rung
		off.Effort = store.ThinkingOff
		quiet := llm.New(srv.URL, modelID) // the cleaned URL: no credentials
		quiet.NoThinking = true
		// Wait up to 2 minutes for a replay or skill check to finish; after
		// that, tell Claude the model is busy.
		size := ctxOf(srv, modelID)
		models[rung.ID()] = delegate.Model{Client: slot.Locked{Inner: llm.New(srv.URL, modelID), Dir: r.dir, Wait: 2 * time.Minute}, Context: size}
		models[off.ID()] = delegate.Model{Client: slot.Locked{Inner: quiet, Dir: r.dir, Wait: 2 * time.Minute}, Context: size}
		r.rungIDs = append(r.rungIDs, rung.ID(), off.ID()) // thinking off counts only once a bench has proven it
		labels = append(labels, rung.Label())
	}
	r.h.Models = models
	if len(labels) == 0 {
		r.h.Setup = failed
		return
	}
	r.h.Setup = nil
	r.h.Label = strings.Join(labels, " or ")
}

// refreshing re-reads the evidence, the speed and pick settings and Claude's times
// before each list and call, so skills proven by a `htd bench` run, or a
// changed speed limit, apply without restarting the server.
type refreshing struct {
	listed  string // the proven skills the client last saw
	st      *store.Store
	cfg     config.Config
	dir     string
	rungIDs []string // every configured model that answered, as it runs by default and with thinking off
	missing int      // configured models that didn't answer
	tried   time.Time
	h       *delegate.Handler
}

func (r *refreshing) refresh() {
	if (len(r.rungIDs) == 0 || r.missing > 0) && len(r.cfg.Local) > 0 && !r.tried.IsZero() && time.Since(r.tried) > 30*time.Second {
		r.connect()
	}
	if len(r.rungIDs) == 0 {
		return
	}
	if c, err := config.Load(r.dir); err == nil {
		r.cfg.Speed, r.cfg.Pick = c.Speed, c.Pick // a bad edit keeps the last good settings
	}
	claude, _ := speed.Load(r.dir) // none yet: speed isn't compared, proof decides
	if runs, err := r.st.All(); err == nil {
		r.h.Gate = delegate.Evidence(runs, r.rungIDs, r.cfg.Speed, r.cfg.Pick, claude)
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
