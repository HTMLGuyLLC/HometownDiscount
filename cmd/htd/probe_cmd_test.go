package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/servers"
)

func probe(args ...string) (string, error) {
	var out bytes.Buffer
	err := runProbe(args, &out, &out)
	return out.String(), err
}

// probe lists the server and its models, measures speed and counts the valid
// tool calls out of the number asked for.
func TestProbeHealthyServer(t *testing.T) {
	hermetic(t)
	srv := newFakeServer(t, modelList("alpha-4bit", "beta-8bit"), healthyModel)

	out, err := probe("--url", srv.URL, "--tool-calls", "3")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"Server: " + srv.URL + " (openai)",
		"alpha-4bit · 4bit · context unknown",
		"beta-8bit · 8bit",
		"Testing alpha-4bit...", // the first model, when none is named
		"speed: ",
		"tool calls: 3 of 3 valid · ok",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// --model picks which model is tested.
func TestProbeModelFlag(t *testing.T) {
	hermetic(t)
	srv := newFakeServer(t, modelList("alpha-4bit", "beta-8bit"), healthyModel)

	out, err := probe("--url", srv.URL, "--model", "beta-8bit", "--tool-calls", "1")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "Testing beta-8bit...") {
		t.Errorf("want beta-8bit tested:\n%s", out)
	}
	if srv.chat(1).Model != "beta-8bit" {
		t.Errorf("the requests went to %q", srv.chat(1).Model)
	}

	if _, err := probe("--url", srv.URL, "--model", "nope"); err == nil || !strings.Contains(err.Error(), `doesn't serve "nope"`) {
		t.Errorf("an unknown model should fail clearly, got %v", err)
	}
}

// A model whose tool calls are mostly not well formed is called too
// unreliable for replays, which need 19 of 20.
func TestProbeUnreliableToolCalls(t *testing.T) {
	hermetic(t)
	srv := newFakeServer(t, modelList("alpha-4bit"), noToolCalls)

	out, err := probe("--url", srv.URL, "--tool-calls", "4")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "tool calls: 0 of 4 valid · too unreliable for replays (needs 19 of 20)") {
		t.Errorf("want the unreliable verdict:\n%s", out)
	}
}

// 19 of 20 is the line: one miss in 20 is still fine.
func TestProbeToolCallThreshold(t *testing.T) {
	hermetic(t)
	misses := 0
	srv := newFakeServer(t, modelList("alpha-4bit"), func(n int, r chatRequest) fakeReply {
		if len(r.Tools) > 0 && misses == 0 {
			misses++
			return fakeReply{Content: "It is sunny.", Tokens: 20} // the first tool request misses
		}
		return healthyModel(n, r)
	})

	out, err := probe("--url", srv.URL, "--tool-calls", "20")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "tool calls: 19 of 20 valid · ok") {
		t.Errorf("19 of 20 should pass:\n%s", out)
	}
}

// A model run with less context than it supports gets a warning.
func TestProbeWarnsAboutLimitedContext(t *testing.T) {
	hermetic(t)
	models := []map[string]any{{"id": "big-4bit", "object": "model", "n_ctx": 4096, "meta": map[string]any{"n_ctx_train": 262144}}}
	srv := newFakeServer(t, models, healthyModel)

	out, err := probe("--url", srv.URL, "--tool-calls", "1")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"big-4bit · 4bit · 4096 ctx (reported)", "Warning: big-4bit runs with 4096 tokens of context but supports 262144"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// A server with nothing loaded, an unreachable server and a cloud model are
// each refused with a clear error, before any speed test.
func TestProbeRefusals(t *testing.T) {
	hermetic(t)

	empty := newFakeServer(t, []map[string]any{}, healthyModel)
	if _, err := probe("--url", empty.URL); err == nil || !strings.Contains(err.Error(), "lists no chat models") {
		t.Errorf("no models: %v", err)
	}

	if _, err := probe("--url", closedServerURL()); err == nil || !strings.Contains(err.Error(), "cannot reach") {
		t.Errorf("unreachable: %v", err)
	}

	cloud := newFakeServer(t, modelList("glm-cloud"), healthyModel)
	if _, err := probe("--url", cloud.URL); err == nil || !strings.Contains(err.Error(), "cloud model") {
		t.Errorf("cloud model: %v", err)
	}
	if cloud.chatCount() != 0 {
		t.Error("a cloud model must be refused before any request is sent to it")
	}
}

// A server that errors during the speed test fails the probe with the stage named.
func TestProbeSpeedTestError(t *testing.T) {
	hermetic(t)
	srv := newFakeServer(t, modelList("alpha-4bit"), func(int, chatRequest) fakeReply {
		return fakeReply{Status: 500, Err: "engine crashed"}
	})

	if _, err := probe("--url", srv.URL); err == nil || !strings.Contains(err.Error(), "speed test") {
		t.Errorf("want a speed test error, got %v", err)
	}
}

// A server that fails after some valid tool calls reports how many were valid so far.
func TestProbeToolCallError(t *testing.T) {
	hermetic(t)
	srv := newFakeServer(t, modelList("alpha-4bit"), func(n int, r chatRequest) fakeReply {
		if len(r.Tools) > 0 && n > 3 { // 2 speed requests, then 1 good tool call
			return fakeReply{Status: 500, Err: "engine crashed"}
		}
		return healthyModel(n, r)
	})

	_, err := probe("--url", srv.URL, "--tool-calls", "5")
	if err == nil || !strings.Contains(err.Error(), "tool-call test (1 of 5 valid so far)") {
		t.Errorf("want the partial count in the error, got %v", err)
	}
}

func TestProbeLabels(t *testing.T) {
	for _, tc := range []struct {
		q    servers.Quant
		want string
	}{
		{servers.Quant{Detail: "Q4_K_M", Bits: 4}, "Q4_K_M"},
		{servers.Quant{Bits: 8}, "8-bit"},
		{servers.Quant{Bits: 3.5}, "3.5-bit"},
		{servers.Quant{}, "quant unknown"},
	} {
		if got := quantLabel(tc.q); got != tc.want {
			t.Errorf("quantLabel(%+v) = %q, want %q", tc.q, got, tc.want)
		}
	}
	if got := contextLabel(servers.Model{}); got != "context unknown" {
		t.Errorf("contextLabel with no context = %q", got)
	}
	if got := contextLabel(servers.Model{Context: 8192, ContextSource: servers.ContextLoaded}); got != "8192 ctx (loaded)" {
		t.Errorf("contextLabel = %q", got)
	}
	if prefixed(" ", "") != "" || prefixed(" ", "1.2") != " 1.2" {
		t.Error("prefixed adds the prefix only to a non-empty string")
	}
}
