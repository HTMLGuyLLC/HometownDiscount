package pricing

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"claude-opus-5-5":           "claude-opus-5-5",
		"claude-opus-5-5[1m]":       "claude-opus-5-5",
		"claude-haiku-4-5-20251001": "claude-haiku-4-5",
		" Claude-Sonnet-5-5 ":       "claude-sonnet-5-5",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCost(t *testing.T) {
	tbl, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	u := Usage{Input: 1_000_000, CacheWrite5m: 1_000_000, CacheWrite1h: 1_000_000, CacheRead: 1_000_000, Output: 1_000_000}

	// Opus 5.5: 4 + 5 + 8 + 0.20 + 20
	if got, ok := tbl.Cost("claude-opus-5-5", u); !ok || !near(got, 37.20) {
		t.Errorf("opus 5.5 cost = %v, %v; want 37.20", got, ok)
	}

	fast := u
	fast.Fast = true
	if got, _ := tbl.Cost("claude-opus-5-5", fast); !near(got, 74.40) {
		t.Errorf("opus 5.5 fast cost = %v; want 74.40", got)
	}
	// Sonnet has no fast mode, so the flag is ignored.
	if got, _ := tbl.Cost("claude-sonnet-5-5", fast); !near(got, 2+2.5+4+0.2+10) {
		t.Errorf("sonnet 5.5 fast cost = %v", got)
	}

	// Haiku 5.5 switches to long-context prices above 100K prompt tokens.
	short := Usage{Input: 50_000, Output: 1_000}
	if got, _ := tbl.Cost("claude-haiku-5-5", short); !near(got, (50_000*0.10+1_000*0.50)/1e6) {
		t.Errorf("haiku short cost = %v", got)
	}
	long := Usage{Input: 150_000, Output: 1_000}
	if got, _ := tbl.Cost("claude-haiku-5-5", long); !near(got, (150_000*0.50+1_000*2.50)/1e6) {
		t.Errorf("haiku long cost = %v", got)
	}

	if _, ok := tbl.Cost("<synthetic>", u); ok {
		t.Error("unknown model should not be priced")
	}
}
