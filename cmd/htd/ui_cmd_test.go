package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/speed"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/ui"
)

// serveOnce runs `htd ui` with args, fetches the report while it's up, then
// stops it. It returns the report, what the command printed, and the address
// it opened in the browser ("" when it didn't).
func serveOnce(t *testing.T, args ...string) (ui.Report, string, string) {
	t.Helper()
	opened := ""
	old := openURL
	openURL = func(u string) error { opened = u; return nil }
	t.Cleanup(func() { openURL = old })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	var rep ui.Report
	var fetchErr error
	err := serveUI(ctx, args, &out, &out, func(url string) {
		defer cancel()
		jar, _ := cookiejar.New(nil)
		client := &http.Client{Jar: jar}
		// Opening the address with its key sets the cookie the data needs.
		page, err := client.Get(url)
		if err != nil {
			fetchErr = err
			return
		}
		page.Body.Close()
		res, err := client.Get(strings.SplitN(url, "?", 2)[0] + "api/report")
		if err != nil {
			fetchErr = err
			return
		}
		defer res.Body.Close()
		fetchErr = json.NewDecoder(res.Body).Decode(&rep)
	})
	if err != nil {
		t.Fatalf("serveUI: %v\n%s", err, out.String())
	}
	if fetchErr != nil {
		t.Fatalf("fetch report: %v", fetchErr)
	}
	return rep, out.String(), opened
}

// `htd ui` serves the stored results on 127.0.0.1, opens the browser, and
// stops cleanly.
func TestUIServesResults(t *testing.T) {
	home, _ := hermetic(t)
	addRuns(t, home, findRuns(3, 2, 60, 30)...)
	os.WriteFile(filepath.Join(home, "delegations.jsonl"), []byte(`{"outcome":"answered","skill":"json","size":"small","bytes":4600,"answer_bytes":600}`+"\n"), 0o600)

	os.WriteFile(filepath.Join(home, "config.toml"), []byte("[ui]\ntheme = \"dark\"\n"), 0o600)

	rep, out, opened := serveOnce(t)
	if rep.Theme != "dark" {
		t.Errorf("theme %q, want the [ui] setting", rep.Theme)
	}
	if !strings.HasPrefix(opened, "http://127.0.0.1:") || !strings.Contains(opened, "/?k=") {
		t.Errorf("opened %q, want the address with this run's key", opened)
	}
	if !strings.Contains(out, "Scorecard: "+opened) || !strings.Contains(out, "Stopped.") {
		t.Errorf("output:\n%s", out)
	}
	if len(rep.Models) != 1 || rep.Models[0].Name != testRung.Model || rep.Totals.Graded != 3 || rep.Savings.Answered != 1 {
		t.Errorf("report: %+v", rep)
	}
	if c := rep.Models[0].Replays[0].Cells[0]; c.Result != ui.Learning || c.Passed != 2 || c.Next != 24 {
		t.Errorf("cell: %+v", c)
	}
}

// --no-open leaves the browser alone; with nothing stored the page still
// serves, empty.
func TestUINoOpenEmpty(t *testing.T) {
	hermetic(t)
	rep, out, opened := serveOnce(t, "--no-open")
	if opened != "" {
		t.Errorf("--no-open opened %q", opened)
	}
	if len(rep.Models) != 0 || rep.Theme != "auto" || !strings.Contains(out, "Scorecard: http://127.0.0.1:") || !strings.Contains(out, "/?k=") {
		t.Errorf("report %+v, output:\n%s", rep, out)
	}
}

func TestUIBadArgs(t *testing.T) {
	hermetic(t)
	var out bytes.Buffer
	for _, args := range [][]string{{"--port", "70000"}, {"extra"}} {
		if err := serveUI(context.Background(), args, &out, &out, nil); err == nil {
			t.Errorf("%v: want an error", args)
		}
	}
}

// A setting htd can't read shows on the page instead of stopping the server.
func TestUIBadConfigShowsError(t *testing.T) {
	home, _ := hermetic(t)
	h := ui.Handler(reportLoader(home, speed.Claude{}, nil), home, "k")
	os.WriteFile(filepath.Join(home, "config.toml"), []byte("[speed]\nmax_slowr = 5\n"), 0o600)
	req, _ := http.NewRequest("GET", "/api/report", nil)
	req.Host = "127.0.0.1"
	req.AddCookie(&http.Cookie{Name: ui.CookieName, Value: "k"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var rep ui.Report
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &rep) != nil || !strings.Contains(rep.ConfigError, "max_slowr") || !strings.Contains(rep.ConfigError, "or use Settings") {
		t.Errorf("%d %s", w.Code, w.Body.String())
	}
}

// A broken config.toml doesn't stop `htd ui` from starting: the page and its
// data still come up, on the defaults, with a message saying what's wrong.
func TestUIStartsWithBrokenConfig(t *testing.T) {
	home, _ := hermetic(t)
	os.WriteFile(filepath.Join(home, "config.toml"), []byte("[speed]\nmax_slowr = 5\n"), 0o600)
	var out, errOut bytes.Buffer
	var body string
	err := runServe(t, &out, &errOut, func(client *http.Client, base string) {
		res, err := client.Get(base + "api/report")
		if err != nil {
			t.Error(err)
			return
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		body = string(b)
		if page, err := client.Get(base); err != nil || page.StatusCode != 200 {
			t.Errorf("page: %v", err)
		}
	})
	if err != nil {
		t.Fatalf("serveUI: %v", err)
	}
	var rep ui.Report
	if json.Unmarshal([]byte(body), &rep) != nil || !strings.Contains(rep.ConfigError, "Your config.toml has a problem: ") || rep.Theme != "auto" {
		t.Errorf("report: %s", body)
	}
	if !strings.Contains(errOut.String(), "your config.toml has a problem") || strings.Count(errOut.String(), "\n") != 1 {
		t.Errorf("want one warning line on stderr, got %q", errOut.String())
	}
}

// runServe starts `htd ui --no-open`, runs fn against it with the key
// already turned into a cookie, and stops it.
func runServe(t *testing.T, out, errOut io.Writer, fn func(client *http.Client, base string)) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	return serveUI(ctx, []string{"--no-open"}, out, errOut, func(url string) {
		defer cancel()
		jar, _ := cookiejar.New(nil)
		client := &http.Client{Jar: jar}
		if res, err := client.Get(url); err == nil {
			res.Body.Close()
		}
		fn(client, strings.SplitN(url, "?", 2)[0])
	})
}

// A transcript scan that fails is said on stderr and on the page, not
// mistaken for "nothing to estimate yet".
func TestUITranscriptScanFails(t *testing.T) {
	home, _ := hermetic(t)
	addRuns(t, home, findRuns(3, 2, 60, 30)...)
	old := scanHistory
	scanHistory = func(string, time.Time) (*history.History, error) { return nil, errors.New("disk on fire") }
	t.Cleanup(func() { scanHistory = old })
	var out, errOut bytes.Buffer
	var rep ui.Report
	err := runServe(t, &out, &errOut, func(client *http.Client, base string) {
		for range 100 { // the scan runs in the background
			res, err := client.Get(base + "api/report")
			if err != nil {
				t.Error(err)
				return
			}
			rep = ui.Report{}
			json.NewDecoder(res.Body).Decode(&rep)
			res.Body.Close()
			if rep.Savings.RoomStatus != "reading" {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Savings.RoomStatus != "error" || !strings.Contains(rep.Savings.RoomError, "disk on fire") {
		t.Errorf("room: %q %q", rep.Savings.RoomStatus, rep.Savings.RoomError)
	}
	if !strings.Contains(errOut.String(), "disk on fire") {
		t.Errorf("stderr: %q", errOut.String())
	}
}

// The transcripts are read once, and Claude's times come from that read.
func TestUIScansTranscriptsOnce(t *testing.T) {
	home, _ := hermetic(t)
	addRuns(t, home, findRuns(3, 2, 60, 30)...)
	old := scanHistory
	var scans atomic.Int32
	scanHistory = func(root string, since time.Time) (*history.History, error) {
		scans.Add(1)
		return old(root, since)
	}
	t.Cleanup(func() { scanHistory = old })
	var out bytes.Buffer
	var rep ui.Report
	err := runServe(t, &out, &out, func(client *http.Client, base string) {
		for range 100 {
			res, err := client.Get(base + "api/report")
			if err != nil {
				t.Error(err)
				return
			}
			rep = ui.Report{}
			json.NewDecoder(res.Body).Decode(&rep)
			res.Body.Close()
			if rep.Savings.RoomStatus != "reading" {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if scans.Load() != 1 {
		t.Errorf("transcripts read %d times, want once", scans.Load())
	}
}
