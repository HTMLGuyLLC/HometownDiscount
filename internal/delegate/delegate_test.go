package delegate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/llm"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

func TestEvidence(t *testing.T) {
	var runs []store.Run
	for i := 0; i < 24; i++ {
		runs = append(runs,
			store.Run{Rung: "r1", Type: "skill:lookup", Tier: "small", Graded: true, Pass: true},
			store.Run{Rung: "r1", Type: "skill:lookup", Tier: "medium", Graded: true, Pass: true},
			store.Run{Rung: "r1", Type: "skill:list", Tier: "small", Graded: true, Pass: i%3 != 0},
			store.Run{Rung: "r2", Type: "skill:json", Tier: "small", Graded: true, Pass: true},
		)
	}
	g := Evidence(runs, "r1")
	if g["lookup"] != "medium" || g["list"] != "" || g["json"] != "" {
		t.Errorf("gate = %v", g)
	}
	if !g.Allows("lookup", "small") || !g.Allows("lookup", "medium") || g.Allows("lookup", "large") || g.Allows("list", "small") {
		t.Error("Allows wrong")
	}
	if g.Summary() != "lookup (up to 1,000 lines)" {
		t.Errorf("summary = %q", g.Summary())
	}
}

func handler(t *testing.T, gate Gate) (*Handler, *[]Delegation) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"<think>hm</think>Line 3."}}]}`))
	}))
	t.Cleanup(srv.Close)
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc Parse() {}\n"), 0o644)
	os.WriteFile(filepath.Join(filepath.Dir(root), "secret.txt"), []byte("token"), 0o644)
	var log []Delegation
	return &Handler{Root: root, Client: llm.New(srv.URL, "m"), Label: "Qwen", Gate: gate, Log: func(d Delegation) { log = append(log, d) }}, &log
}

func call(h *Handler, a string) string {
	return h.Call(context.Background(), "ask_local", json.RawMessage(a)).Text
}

func TestCall(t *testing.T) {
	h, log := handler(t, Gate{"lookup": "small"})
	if out := call(h, `{"task":"where is Parse defined?","files":["a.go"],"skill":"lookup"}`); !strings.Contains(out, "Line 3.") || strings.Contains(out, "think") {
		t.Errorf("answered = %q", out)
	}
	if out := call(h, `{"task":"list","files":["a.go"],"skill":"list"}`); !strings.HasPrefix(out, "Not handed to the local model") {
		t.Errorf("unproven skill should be refused: %q", out)
	}
	if out := call(h, `{"task":"x","files":["../secret.txt"],"skill":"lookup"}`); !strings.Contains(out, "can't read") {
		t.Errorf("escape should fail: %q", out)
	}
	if out := call(h, `{"task":"x","files":["/etc/hosts"],"skill":"lookup"}`); !strings.Contains(out, "outside the project") {
		t.Errorf("absolute path outside root should fail: %q", out)
	}
	os.WriteFile(filepath.Join(h.Root, ".env"), []byte("KEY=x"), 0o644)
	if out := call(h, `{"task":"x","files":[".env"],"skill":"lookup"}`); !strings.Contains(out, "secrets") {
		t.Errorf(".env should be refused: %q", out)
	}
	os.Symlink(".env", filepath.Join(h.Root, "notes.txt"))
	if out := call(h, `{"task":"x","files":["notes.txt"],"skill":"lookup"}`); !strings.Contains(out, "links to a file") {
		t.Errorf("a link to .env should be refused: %q", out)
	}
	if out := call(h, `{"files":["a.go"]}`); !strings.Contains(out, "needs a task") {
		t.Errorf("missing task: %q", out)
	}
	outcomes := []string{}
	for _, d := range *log {
		outcomes = append(outcomes, d.Outcome)
	}
	if strings.Join(outcomes, ",") != "answered,refused,error,error,error,error" {
		t.Errorf("log outcomes = %v", outcomes)
	}
	if (*log)[0].Bytes == 0 || (*log)[0].Size != "small" {
		t.Errorf("log = %+v", (*log)[0])
	}
}

func TestDescriptionFollowsEvidence(t *testing.T) {
	h, _ := handler(t, Gate{})
	if d := h.Tools()[0].Description; !strings.Contains(d, "hasn't proven any skill") {
		t.Errorf("description = %q", d)
	}
	h.Gate = Gate{"json": "large"}
	if d := h.Tools()[0].Description; !strings.Contains(d, "json (up to 4,000 lines)") {
		t.Errorf("description = %q", d)
	}
}

func TestBadShape(t *testing.T) {
	if badShape("lookup", "It's on line 12.") != "" || badShape("lookup", "I couldn't find it") == "" {
		t.Error("lookup answers need a line number")
	}
	if badShape("list", strings.Repeat("a\n", 400)) == "" || badShape("json", "8080") != "" {
		t.Error("shape limits wrong")
	}
	if badShape("files", strings.Repeat("x", MaxAnswer+1)) == "" {
		t.Error("answers over the cap must be rejected")
	}
}
