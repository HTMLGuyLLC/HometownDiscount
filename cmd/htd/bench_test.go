package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

// A thinking model that uses up its answer budget fails the check, as
// ask_local would refuse the same answer, even if the visible text happens
// to hold the right answer.
func TestBenchCutOffFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HTD_HOME", home)
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "billing.go"), []byte("package billing\n\nfunc Charge() {}\n\nfunc Refund() {}\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "app.json"), []byte(`{"server": {"port": 8080}}`), 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Write([]byte(`{"object":"list","data":[{"id":"tiny-q4","object":"model"}]}`))
		case "/v1/chat/completions":
			json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{
				"finish_reason": "length",
				"message":       map[string]any{"role": "assistant", "content": "8080 <think>wait, let me check that again"},
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	var out bytes.Buffer
	if err := runBench([]string{"--url", srv.URL, "--repo", repo, "--max", "2"}, &out, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	st, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := st.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) == 0 {
		t.Fatalf("no checks ran:\n%s", out.String())
	}
	for _, r := range runs {
		if r.Pass || r.Reason != "cut off" {
			t.Errorf("run %s: pass=%v reason=%q, want a cut-off fail", r.JobID, r.Pass, r.Reason)
		}
	}
	if !strings.Contains(out.String(), "(cut off)") {
		t.Errorf("the log should say why:\n%s", out.String())
	}
}

// A server that crashes mid-check and recovers costs a skipped check, not
// the run, and nothing is recorded against the model.
func TestBenchWaitsForServerRecovery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HTD_HOME", home)
	defer func(w time.Duration) { serverWait = w }(serverWait)
	serverWait = 2 * time.Second
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "billing.go"), []byte("package billing\n\nfunc Charge() {}\n\nfunc Refund() {}\n"), 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	var mu sync.Mutex
	chats, down := 0, false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/v1/models":
			if down {
				down = false // recovers after one failed probe
				http.Error(w, "engine is recovering", http.StatusServiceUnavailable)
				return
			}
			w.Write([]byte(`{"object":"list","data":[{"id":"tiny-q4","object":"model"}]}`))
		case "/v1/chat/completions":
			if body, _ := io.ReadAll(r.Body); strings.Contains(string(body), "17 + 25") {
				w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"42"}}]}`)) // whether it thinks: no
				return
			}
			if chats++; chats <= 2 { // the check and its one retry
				down = true
				http.Error(w, "engine is recovering; retry shortly", http.StatusServiceUnavailable)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{
				"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": "8080"},
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	var out bytes.Buffer
	if err := runBench([]string{"--url", srv.URL, "--repo", repo, "--max", "1"}, &out, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "It's back") || !strings.Contains(out.String(), "server restarted") {
		t.Errorf("want a wait, then a skipped check:\n%s", out.String())
	}
}
