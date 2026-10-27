package history

import (
	"testing"
	"time"
)

func TestScan(t *testing.T) {
	since := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	h, err := Scan("testdata/projects", since)
	if err != nil {
		t.Fatal(err)
	}

	if len(h.Jobs) != 1 {
		t.Fatalf("got %d jobs, want 1 (a2 is before the window)", len(h.Jobs))
	}
	j := h.Jobs[0]
	if j.AgentID != "a1" || j.SessionID != "sess1" || j.Project != "-Users-me-app" {
		t.Errorf("ids = %q %q %q", j.AgentID, j.SessionID, j.Project)
	}
	if j.AgentType != "Explore" || j.Description != "Find config loader" || j.SpawnDepth != 1 {
		t.Errorf("meta = %q %q %d", j.AgentType, j.Description, j.SpawnDepth)
	}
	if !j.Background {
		t.Error("Background should come from the parent's isAsync result")
	}
	if j.Model() != "claude-opus-5-5" {
		t.Errorf("Model() = %q", j.Model())
	}
	if j.Tools["Bash"] != 1 || j.Tools["Read"] != 1 {
		t.Errorf("tools = %v", j.Tools)
	}

	// msg_s1 appears twice; only the final line (40 output tokens) counts.
	if len(j.Requests) != 2 {
		t.Fatalf("got %d requests, want 2", len(j.Requests))
	}
	if got := j.Requests[0].Usage; got.Output != 40 || got.CacheWrite5m != 2000 {
		t.Errorf("first request usage = %+v", got)
	}

	// Main: msg_main1 once (dedupe, 50 output, 1h cache write); msg_old is outside the window.
	if len(h.Main) != 1 {
		t.Fatalf("got %d main requests, want 1", len(h.Main))
	}
	if u := h.Main[0].Usage; u.Output != 50 || u.CacheWrite1h != 1000 || u.CacheWrite5m != 0 {
		t.Errorf("main usage = %+v", u)
	}
}
