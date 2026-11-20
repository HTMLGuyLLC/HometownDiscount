package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/config"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/delegate"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/pricing"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/speed"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/ui"
)

// runUI serves the scorecard page on this computer until Ctrl-C.
func runUI(args []string, stdout, stderr io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveUI(ctx, args, stdout, stderr, nil)
}

// openURL opens a page in the user's browser. Tests replace it.
var openURL = func(url string) error {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	return exec.Command(name, url).Start()
}

// scanHistory reads the Claude Code transcripts. Tests replace it.
var scanHistory = history.Scan

// lockedWriter lets the page's server and the background transcript scan
// share one stream.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// serveUI is runUI with the context it stops on; ready, when set, gets the
// page's address (with its key) once it's being served.
func serveUI(ctx context.Context, args []string, stdout, stderr io.Writer, ready func(url string)) error {
	fs := command("ui", "[--port N] [--no-open]", "Open the scorecard in your browser: savings from ask_local, each local model's\nresults per task and size, and a settings form. It's served only on this\ncomputer (127.0.0.1) and the address it prints carries a key for this run.\nIt reads ~/.htd and your Claude Code transcripts, and its Settings tab can\nrewrite ~/.htd/config.toml. It updates as results come in. Ctrl-C stops it.", stderr)
	port := fs.Int("port", 0, "port to serve on (default: any free port)")
	noOpen := fs.Bool("no-open", false, "print the address instead of opening your browser")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q (run `htd ui --help`)", fs.Arg(0))
	}
	if *port < 0 || *port > 65535 {
		return fmt.Errorf("--port must be between 0 and 65535")
	}
	stderr = &lockedWriter{w: stderr}
	dir := store.DefaultDir()
	st, err := store.Open(dir)
	if err != nil {
		return err
	}
	runs, err := st.All()
	if err != nil {
		return err
	}
	// A config.toml htd can't read doesn't stop the page: it's the page that
	// can show what's wrong. The defaults stand in until the file is fixed.
	cfg, err := config.Load(dir)
	if err != nil {
		fmt.Fprintf(stderr, "htd ui: your config.toml has a problem (%v); showing the defaults until it's fixed.\n", err)
		cfg = config.Default()
	}
	// The transcripts are read once, in the background, so the page opens at
	// once: Claude's times (saved from last time until then) and the room to
	// grow both come from that one read.
	saved, _ := speed.Load(dir)
	reads := &transcriptReads{days: uiReadDays}
	go reads.load(history.DefaultRoot(), time.Now(), dir, cfg.Speed.ClaudeModel, len(runs) > 0, stderr)
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(*port)))
	if err != nil {
		return fmt.Errorf("serve the scorecard: %w", err)
	}
	key := ui.NewKey()
	srv := &http.Server{Handler: ui.Handler(reportLoader(dir, saved, reads), dir, key), ReadHeaderTimeout: 10 * time.Second}
	url := "http://" + ln.Addr().String() + "/?k=" + key
	fmt.Fprintf(stdout, "Scorecard: %s\nOnly this computer can open it. Ctrl-C stops it.\n", url)
	if !*noOpen {
		if err := openURL(url); err != nil {
			fmt.Fprintf(stdout, "Couldn't open your browser (%v). Open the address above instead.\n", err)
		}
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	if ready != nil {
		go ready(url)
	}
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shut, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(shut); err != nil && !errors.Is(err, http.ErrServerClosed) {
		srv.Close() // a browser holding a connection open can't keep Ctrl-C waiting
	}
	fmt.Fprintln(stdout, "Stopped.")
	return nil
}

// uiReadDays is how many days of transcripts the page reads, for Claude's
// times and the "room to grow" estimate.
const uiReadDays = 30

// transcriptReads is what one read of the last few days of transcripts
// gave: the files Claude read, and Claude's measured times. Reading them can
// take a while, so the page opens first and fills in when they're ready.
type transcriptReads struct {
	mu     sync.Mutex
	days   int
	reads  []history.ReadSample
	err    error
	claude speed.Claude // measured from this read, when timed
	timed  bool
	ready  bool
}

// load reads the transcripts once. When there are results to judge (measure)
// and the read found something, Claude's times are measured from it and
// saved, so the next start has them at once.
func (t *transcriptReads) load(root string, now time.Time, dir, claudeModel string, measure bool, warn io.Writer) {
	h, err := scanHistory(root, now.AddDate(0, 0, -t.days))
	var claude speed.Claude
	timed := false
	if err == nil && measure && len(h.Reads) > 0 {
		claude, timed = claudeTimes(dir, root, h, t.days, claudeModel, nil), true
	}
	if err != nil {
		fmt.Fprintf(warn, "htd ui: couldn't read your Claude Code transcripts: %v\n", err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err == nil {
		t.reads = h.Reads
	} else {
		t.err = err
	}
	t.claude, t.timed, t.ready = claude, timed, true
}

// transcripts is what a report needs from the read.
type transcripts struct {
	reads  []history.ReadSample
	days   int
	ready  bool
	err    error
	claude speed.Claude
	timed  bool
}

func (t *transcriptReads) get() transcripts {
	if t == nil {
		return transcripts{ready: true}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return transcripts{reads: t.reads, days: t.days, ready: t.ready, err: t.err, claude: t.claude, timed: t.timed}
}

// reportLoader reads the stored results fresh for each page refresh. saved
// is Claude's last measured times, used until the transcripts have been
// read and timed again. A config.toml that can't be read gives the defaults
// and a message for the page, not an error.
func reportLoader(dir string, saved speed.Claude, reads *transcriptReads) func() (ui.Report, error) {
	prices, _ := pricing.Default()
	return func() (ui.Report, error) {
		st, err := store.Open(dir)
		if err != nil {
			return ui.Report{}, err
		}
		runs, err := st.All()
		if err != nil {
			return ui.Report{}, err
		}
		configError := ""
		cfg, err := config.Load(dir)
		if err != nil {
			configError = "Your config.toml has a problem: " + ui.ConfigProblem(err) + ". Fix that line in the file, or use Settings."
			cfg = config.Default()
		}
		tr := reads.get()
		claude := saved
		if tr.timed {
			claude = tr.claude
		}
		in := ui.Input{
			Runs: runs, Speed: cfg.Speed, Claude: claude,
			Delegations: delegate.Load(filepath.Join(dir, delegate.LogName)),
			Prices:      prices, Now: time.Now(), Theme: cfg.UI.Theme, Tracking: cfg.Privacy.TrackHandoffs,
			ConfigError: configError,
			Reads:       tr.reads, ReadsDays: tr.days, ReadsReady: tr.ready,
		}
		if tr.err != nil {
			in.ReadsErr = tr.err.Error()
		}
		return ui.Build(in), nil
	}
}
