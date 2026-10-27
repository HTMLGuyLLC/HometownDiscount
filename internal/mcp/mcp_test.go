package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type echo struct{}

func (echo) Tools() []Tool {
	return []Tool{{Name: "echo", Description: "echo", InputSchema: json.RawMessage(`{"type":"object"}`)}}
}

func (echo) Call(_ context.Context, name string, args json.RawMessage) Result {
	return Result{Text: name + ":" + string(args)}
}

func TestServe(t *testing.T) {
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":"x","method":"tools/call","params":{"name":"echo","arguments":{"a":1}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"nope"}`,
		`not json`,
	}, "\n")
	var out bytes.Buffer
	if err := Serve(context.Background(), strings.NewReader(in), &out, Info{"htd", "test"}, echo{}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("got %d responses (notifications get none):\n%s", len(lines), out.String())
	}
	want := []string{`"protocolVersion":"2025-06-18"`, `"name":"echo"`, `"text":"echo:{\"a\":1}"`, `method not found`, `parse error`}
	for i, w := range want {
		if !strings.Contains(lines[i], w) {
			t.Errorf("response %d = %s, want it to contain %s", i, lines[i], w)
		}
	}
	if !strings.Contains(lines[2], `"id":"x"`) {
		t.Error("string ids must be echoed back unchanged")
	}
}

type changing struct {
	echo
	changed bool
}

func (c *changing) Changed() bool { v := c.changed; c.changed = false; return v }

func TestListChanged(t *testing.T) {
	h := &changing{changed: true}
	in := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{}}}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"ping"}`
	var out bytes.Buffer
	Serve(context.Background(), strings.NewReader(in), &out, Info{"htd", "test"}, h)
	if strings.Count(out.String(), "notifications/tools/list_changed") != 1 {
		t.Errorf("want exactly one list_changed notification:\n%s", out.String())
	}
}
