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
	"github.com/HTMLGuyLLC/HometownDiscount/internal/skills"
)

// MinSamples is how many timings a median needs before speed is compared,
// for Claude's time and for the local model's.
const MinSamples = 5

// Sample is a median time and how many timings it came from.
type Sample struct {
	Seconds float64 `json:"seconds"`
	N       int     `json:"n"`
	// From names a smaller size whose time stands in for this one when this
	// size has too few timings. Claude is no faster on bigger files, so the
	// stand-in never makes the local model look better than it is.
	From string `json:"from,omitempty"`
}

// Claude is how long Claude typically takes to answer after reading one file,
// by input size, from the user's own transcripts.
type Claude struct {
	At     time.Time         `json:"at"`
	Days   int               `json:"days"`
	BySize map[string]Sample `json:"by_size"`
}

// Estimate builds Claude's times from read samples.
func Estimate(reads []history.ReadSample, days int) Claude {
	by := map[string][]float64{}
	for _, r := range reads {
		if size := skills.SizeOf(r.Lines); size != "" && r.Seconds > 0 {
			by[size] = append(by[size], r.Seconds)
		}
	}
	c := Claude{At: time.Now(), Days: days, BySize: map[string]Sample{}}
	last := ""
	for _, size := range skills.SizeOrder {
		if v := by[size]; len(v) >= MinSamples {
			c.BySize[size] = Sample{Seconds: Median(v), N: len(v)}
			last = size
		} else if last != "" {
			s := c.BySize[last]
			c.BySize[size] = Sample{Seconds: s.Seconds, N: s.N, From: last}
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
