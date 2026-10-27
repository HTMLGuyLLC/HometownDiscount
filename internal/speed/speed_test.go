package speed

import (
	"testing"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/config"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
)

func TestEstimate(t *testing.T) {
	var reads []history.ReadSample
	for i := 1; i <= 9; i++ {
		reads = append(reads, history.ReadSample{Lines: 50, Seconds: float64(i)})       // small: median 5
		reads = append(reads, history.ReadSample{Lines: 500, Seconds: float64(10 + i)}) // medium: median 15
	}
	reads = append(reads, history.ReadSample{Lines: 3000, Seconds: 1}) // one large: too few
	c := Estimate(reads, 30, "")
	if s := c.BySize["small"]; s.Seconds != 5 || s.N != 9 || s.From != "" {
		t.Errorf("small = %+v", s)
	}
	if s := c.BySize["medium"]; s.Seconds != 15 {
		t.Errorf("medium = %+v", s)
	}
	// Too few large timings: medium's time stands in, never a faster guess.
	if s := c.BySize["large"]; s.Seconds != 15 || s.From != "medium" {
		t.Errorf("large = %+v", s)
	}
	if c := Estimate(nil, 30, ""); len(c.BySize) != 0 {
		t.Errorf("no reads should give no times: %+v", c)
	}
}

func TestMedian(t *testing.T) {
	if Median(nil) != 0 || Median([]float64{3, 1, 2}) != 2 || Median([]float64{4, 1, 3, 2}) != 2.5 {
		t.Error("median wrong")
	}
}

func TestRuleFor(t *testing.T) {
	s := config.Speed{MaxSlower: 20, ByTask: map[string]any{
		"lookup:small": int64(5), "review:large": 25.0, "review": int64(15), "files:large": "never", "json": "always",
	}}
	for _, c := range []struct {
		task, size string
		want       Rule
	}{
		{"lookup", "small", Rule{Mode: "limit", MaxSlower: 5, From: "lookup:small"}},
		{"lookup", "large", Rule{Mode: "limit", MaxSlower: 20, From: "default"}},
		{"review", "large", Rule{Mode: "limit", MaxSlower: 25, From: "review:large"}},
		{"review", "small", Rule{Mode: "limit", MaxSlower: 15, From: "review"}},
		{"files", "large", Rule{Mode: "never", From: "files:large"}},
		{"json", "medium", Rule{Mode: "always", From: "json"}},
	} {
		if got := RuleFor(s, c.task, c.size); got != c.want {
			t.Errorf("%s:%s = %+v, want %+v", c.task, c.size, got, c.want)
		}
	}
	if got := RuleFor(config.Speed{MaxSlower: -1}, "lookup", "small"); got.Mode != "always" {
		t.Errorf("-1 should turn the speed check off: %+v", got)
	}
}

func TestDecide(t *testing.T) {
	limit := Rule{Mode: "limit", MaxSlower: 20}
	for _, c := range []struct {
		name     string
		rule     Rule
		ratio    float64
		compared bool
		kept     bool
	}{
		{"faster", limit, 0.5, true, false},
		{"within the limit", limit, 1.2, true, false},
		{"over the limit", limit, 1.21, true, true},
		{"not enough timings: proof decides", limit, 9, false, false},
		{"always", Rule{Mode: "always"}, 9, true, false},
		{"never", Rule{Mode: "never"}, 0.5, true, true},
		{"zero limit: at least as fast", Rule{Mode: "limit"}, 1.01, true, true},
	} {
		if v := Decide(c.rule, c.ratio, c.compared); v.Kept != c.kept {
			t.Errorf("%s: kept = %v, want %v", c.name, v.Kept, c.kept)
		}
	}
}

func TestWords(t *testing.T) {
	limit := Rule{Mode: "limit", MaxSlower: 20, From: "default"}
	for ratio, want := range map[float64]string{0.5: "50% faster than Claude", 1.02: "about as fast as Claude", 1.5: "50% slower than Claude", 3.14: "3.1× Claude's time"} {
		if got := Decide(limit, ratio, true).Pace(); got != want {
			t.Errorf("%v: %q, want %q", ratio, got, want)
		}
	}
	if got := Decide(limit, 2, false).Pace(); got != "speed not compared yet" {
		t.Errorf("uncompared: %q", got)
	}
	if got := Decide(limit, 3, true).Why(); got != "it's 3.0× Claude's time (your limit +20%)" {
		t.Errorf("why = %q", got)
	}
	if got := Decide(Rule{Mode: "never", From: "files:large"}, 0, false).Why(); got != "your settings keep it on Claude (files:large = never)" {
		t.Errorf("why = %q", got)
	}
}

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	if _, ok := Load(dir); ok {
		t.Error("nothing saved yet")
	}
	c := Claude{Days: 30, BySize: map[string]Sample{"small": {Seconds: 3, N: 10}}}
	if err := Save(dir, c); err != nil {
		t.Fatal(err)
	}
	got, ok := Load(dir)
	if !ok || got.BySize["small"].Seconds != 3 || got.Days != 30 {
		t.Errorf("loaded %+v", got)
	}
}

// Times are per Claude model: Opus 5 being quick says nothing about Opus 5.5.
// A size with too few timings for the model borrows its own smaller size
// first, then every model's time.
func TestEstimatePerModel(t *testing.T) {
	var reads []history.ReadSample
	for i := 0; i < 6; i++ {
		reads = append(reads, history.ReadSample{Lines: 50, Seconds: 10, Model: "claude-opus-5-5"})
	}
	for i := 0; i < 10; i++ {
		reads = append(reads,
			history.ReadSample{Lines: 50, Seconds: 2, Model: "claude-opus-5[1m]"},
			history.ReadSample{Lines: 500, Seconds: 3, Model: "claude-opus-5"})
	}
	c := Estimate(reads, 30, "claude-opus-5-5")
	if c.Model != "claude-opus-5-5" || c.BySize["small"].Seconds != 10 || c.BySize["small"].From != "" {
		t.Errorf("small = %+v", c.BySize["small"])
	}
	if s := c.BySize["medium"]; s.Seconds != 10 || s.From != "small" {
		t.Errorf("medium should borrow Opus 5.5's small time, not Opus 5's: %+v", s)
	}
	h := Estimate(reads, 30, "claude-haiku-5-5")
	if s := h.BySize["small"]; s.From != "all models" || s.N != 16 {
		t.Errorf("a model with no timings uses every model's: %+v", s)
	}
}
