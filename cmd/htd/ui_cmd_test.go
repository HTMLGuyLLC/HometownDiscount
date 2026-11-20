package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		res, err := http.Get(url + "api/report")
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
	if !strings.HasPrefix(opened, "http://127.0.0.1:") {
		t.Errorf("opened %q", opened)
	}
	if !strings.Contains(out, "Scorecard: "+opened) || !strings.Contains(out, "Stopped.") {
		t.Errorf("output:\n%s", out)
	}
	if len(rep.Models) != 1 || rep.Models[0].Name != testRung.Model || rep.Totals.Graded != 3 || rep.Totals.Answered != 1 {
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
	if len(rep.Models) != 0 || rep.Theme != "auto" || !strings.Contains(out, "Scorecard: http://127.0.0.1:") {
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
	h := ui.Handler(reportLoader(home, speed.Claude{}, nil), home)
	os.WriteFile(filepath.Join(home, "config.toml"), []byte("[speed]\nmax_slowr = 5\n"), 0o600)
	req, _ := http.NewRequest("GET", "/api/report", nil)
	req.Host = "127.0.0.1"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 500 || !strings.Contains(w.Body.String(), "max_slowr") {
		t.Errorf("%d %s", w.Code, w.Body.String())
	}
}
