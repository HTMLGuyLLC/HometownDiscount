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

// serveUI is runUI with the context it stops on; ready, when set, gets the
// page's address once it's being served.
func serveUI(ctx context.Context, args []string, stdout, stderr io.Writer, ready func(url string)) error {
	fs := command("ui", "[--port N] [--no-open]", "Open the scorecard in your browser: what each local model has earned, per task\nand size, how it got there, and what ask_local has handed off. It's served\nonly on this computer (127.0.0.1), reads ~/.htd and changes nothing.\nIt updates as results come in. Ctrl-C stops it.", stderr)
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
	dir := store.DefaultDir()
	// Claude's times come from the transcripts, which can take a while to
	// read; they're measured once here and change slowly. Runs, settings and
	// handoffs are read again on each refresh.
	ev, err := loadEvidence(dir, stdout)
	if err != nil {
		return err
	}
	reads := &transcriptReads{days: uiReadDays}
	go reads.load(history.DefaultRoot(), time.Now())
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(*port)))
	if err != nil {
		return fmt.Errorf("serve the scorecard: %w", err)
	}
	srv := &http.Server{Handler: ui.Handler(reportLoader(dir, ev.claude, reads), dir), ReadHeaderTimeout: 10 * time.Second}
	url := "http://" + ln.Addr().String() + "/"
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
		return err
	}
	fmt.Fprintln(stdout, "Stopped.")
	return nil
}

// uiReadDays is how many days of transcripts the "room to grow" estimate
// reads.
const uiReadDays = 30

// transcriptReads are the files Claude read in the last few days of
// transcripts. Reading them can take a while, so the page opens first and
// the estimate fills in when they're ready.
type transcriptReads struct {
	mu    sync.Mutex
	days  int
	reads []history.ReadSample
	ready bool
}

func (t *transcriptReads) load(root string, now time.Time) {
	h, err := history.Scan(root, now.AddDate(0, 0, -t.days))
	t.mu.Lock()
	defer t.mu.Unlock()
	if err == nil {
		t.reads = h.Reads
	} else {
		t.days = 0 // nothing to estimate from
	}
	t.ready = true
}

func (t *transcriptReads) get() ([]history.ReadSample, int, bool) {
	if t == nil {
		return nil, 0, true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reads, t.days, t.ready
}

// reportLoader reads the stored results fresh for each page refresh.
func reportLoader(dir string, claude speed.Claude, reads *transcriptReads) func() (ui.Report, error) {
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
		cfg, err := config.Load(dir)
		if err != nil {
			return ui.Report{}, err
		}
		rs, days, ready := reads.get()
		return ui.Build(ui.Input{
			Runs: runs, Speed: cfg.Speed, Claude: claude,
			Delegations: delegate.Load(filepath.Join(dir, delegate.LogName)),
			Prices:      prices, Now: time.Now(), Theme: cfg.UI.Theme, Tracking: cfg.Privacy.TrackHandoffs,
			Reads: rs, ReadsDays: days, ReadsReady: ready,
		}), nil
	}
}
