// Package speed weighs the local model's time against Claude's, so a proven
// task goes local only when it's also fast enough: at most a set percent
// slower than Claude, per task and size.
package speed

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/config"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/pricing"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/skills"
)

// MinSamples is how many timings a median needs before speed is compared,
// for Claude's time and for the local model's.
const MinSamples = 5

// Sample is a median time and how many timings it came from.
type Sample struct {
	Seconds float64 `json:"seconds"`
	N       int     `json:"n"`
	// From says where the time came from when this model and size have too
	// few timings: a smaller size of the same model ("medium"), or every
	// Claude model ("all models"). Claude is no faster on bigger files, so a
	// smaller size's time never makes the local model look better.
	From string `json:"from,omitempty"`
}

// Claude is how long one Claude model typically takes to answer after
// reading one file, by input size, from the user's own transcripts.
type Claude struct {
	At     time.Time         `json:"at"`
	Days   int               `json:"days"`
	Model  string            `json:"model"` // "" when no model was known: every model's times
	BySize map[string]Sample `json:"by_size"`
}

// Estimate builds a Claude model's times from read samples. A size with too
// few timings for that model borrows its smaller size's time, then every
// model's time at that size.
func Estimate(reads []history.ReadSample, days int, model string) Claude {
	model = pricing.Normalize(model)
	mine, all := map[string][]float64{}, map[string][]float64{}
	for _, r := range reads {
		size := skills.SizeOf(r.Lines)
		if size == "" || r.Seconds <= 0 {
			continue
		}
		all[size] = append(all[size], r.Seconds)
		if model == "" || pricing.Normalize(r.Model) == model {
			mine[size] = append(mine[size], r.Seconds)
		}
	}
	c := Claude{At: time.Now(), Days: days, Model: model, BySize: map[string]Sample{}}
	last := ""
	for _, size := range skills.SizeOrder {
		switch v := mine[size]; {
		case len(v) >= MinSamples:
			c.BySize[size] = Sample{Seconds: Median(v), N: len(v)}
			last = size
		case last != "":
			s := c.BySize[last]
			c.BySize[size] = Sample{Seconds: s.Seconds, N: s.N, From: last}
		case len(all[size]) >= MinSamples:
			c.BySize[size] = Sample{Seconds: Median(all[size]), N: len(all[size]), From: "all models"}
		}
	}
	return c
}

// Median of a list; 0 when it's empty.
func Median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// cachePath is where Claude's times are kept between runs.
func cachePath(dir string) string { return filepath.Join(dir, "cache", "claude-speed.json") }

// Load reads Claude's saved times; ok is false when there are none.
func Load(dir string) (Claude, bool) {
	var c Claude
	b, err := os.ReadFile(cachePath(dir))
	if err != nil || json.Unmarshal(b, &c) != nil || c.BySize == nil {
		return Claude{}, false
	}
	return c, true
}

// Save keeps Claude's times for the MCP server and the card.
func Save(dir string, c Claude) error {
	if err := os.MkdirAll(filepath.Dir(cachePath(dir)), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cachePath(dir), b, 0o600)
}

// Rule is the speed limit that applies to one task and size.
type Rule struct {
	Mode      string  // "limit", "always" or "never"
	MaxSlower float64 // percent, for "limit"
	From      string  // the setting it came from: "lookup:small", "lookup" or "default"
}

// RuleFor picks the most specific setting: "task:size", then "task", then the default.
func RuleFor(s config.Speed, task, size string) Rule {
	for _, key := range []string{task + ":" + size, task} {
		v, ok := s.ByTask[key]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case string:
			return Rule{Mode: x, From: key}
		case int64:
			return Rule{Mode: "limit", MaxSlower: float64(x), From: key}
		case float64:
			return Rule{Mode: "limit", MaxSlower: x, From: key}
		}
	}
	if s.MaxSlower < 0 {
		return Rule{Mode: "always", From: "default"}
	}
	return Rule{Mode: "limit", MaxSlower: s.MaxSlower, From: "default"}
}

// Verdict is the speed side of whether the local model takes a task.
type Verdict struct {
	Kept     bool    // speed (or a "never" setting) keeps the task on Claude
	Compared bool    // there were enough timings on both sides to compare
	Ratio    float64 // local time ÷ Claude's time, when compared
	Rule     Rule
}

// Decide applies a rule to a measured ratio.
func Decide(r Rule, ratio float64, compared bool) Verdict {
	v := Verdict{Compared: compared, Ratio: ratio, Rule: r}
	switch {
	case r.Mode == "never":
		v.Kept = true
	case r.Mode == "always", !compared:
		v.Kept = false
	default:
		v.Kept = ratio > 1+r.MaxSlower/100
	}
	return v
}

// Limit is the rule in a few words, e.g. "limit +20%".
func (r Rule) Limit() string {
	switch r.Mode {
	case "always":
		return "always local"
	case "never":
		return "never local"
	}
	return fmt.Sprintf("limit +%g%%", r.MaxSlower)
}

// Pace says how the local model compares with Claude, e.g. "3.1× Claude's
// time" or "40% faster than Claude".
func (v Verdict) Pace() string {
	switch {
	case !v.Compared:
		return "speed not compared yet"
	case v.Ratio < 0.95:
		return fmt.Sprintf("%.0f%% faster than Claude", (1-v.Ratio)*100)
	case v.Ratio <= 1.05:
		return "about as fast as Claude"
	case v.Ratio < 2:
		return fmt.Sprintf("%.0f%% slower than Claude", (v.Ratio-1)*100)
	}
	return fmt.Sprintf("%.1f× Claude's time", v.Ratio)
}

// Why says, in plain words, why Claude keeps a task the local model has
// proven it can do.
func (v Verdict) Why() string {
	if v.Rule.Mode == "never" {
		return "your settings keep it on Claude (" + v.Rule.From + " = never)"
	}
	return fmt.Sprintf("it's %s (your %s)", v.Pace(), v.Rule.Limit())
}
