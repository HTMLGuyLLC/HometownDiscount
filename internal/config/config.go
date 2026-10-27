// Package config reads and writes ~/.htd/config.toml. Every default costs $0 extra.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/jobtype"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/skills"
)

// Local is one local model: a server URL and a model name.
type Local struct {
	URL    string `toml:"url"`
	Model  string `toml:"model"`
	Quant  string `toml:"quant"` // "auto" = detect, or e.g. "4bit", "Q4_K_M"
	Loop   string `toml:"loop"`  // "builtin" in v1
	Effort string `toml:"effort,omitempty"`
	// AllowRemote opts in to a server off this machine and local network:
	// replays and ask_local would send code there.
	AllowRemote bool `toml:"allow_remote,omitempty"`
}

// Calibration runs Claude rungs on past jobs with the user's own API key.
type Calibration struct {
	CapUSD     float64  `toml:"calibration_cap_usd"` // monthly cap; 0 = off
	Rungs      []string `toml:"rungs"`
	OwnControl bool     `toml:"own_control"`
}

// Report controls the weekly summary.
type Report struct {
	WeeklySummary bool    `toml:"weekly_summary"`
	SupportPrompt bool    `toml:"support_prompt"`
	SupportMinUSD float64 `toml:"support_prompt_min_usd"`
}

// Speed decides when the local model is too slow to be worth it: a proven
// task goes local only when it's at most MaxSlower percent slower than
// Claude. ByTask overrides that per task ("lookup") or task and size
// ("lookup:large") with a percent, "always" (skip the speed check) or
// "never" (Claude always does it).
type Speed struct {
	MaxSlower float64 `toml:"max_slower"` // percent; -1 turns the speed check off
	// ClaudeModel is the Claude model speed is compared with: "auto" (the
	// one used most in the last few days) or a model id like "claude-opus-5-5".
	ClaudeModel string         `toml:"claude_model"`
	ByTask      map[string]any `toml:"by_task,omitempty"`
}

// Pick decides which local model takes a skill when more than one has
// proven it and is fast enough. Prefer is "fastest" (the quickest at that
// size; ties go to the one listed first) or "listed" (the first in the
// [[local]] list). ByTask pins a skill ("lookup") or skill and size
// ("lookup:large") to a model, used whenever that model is proven and fast
// enough for it.
type Pick struct {
	Prefer string            `toml:"prefer"`
	ByTask map[string]string `toml:"by_task,omitempty"`
}

// Config is the whole file.
type Config struct {
	Routing     string      `toml:"routing"` // ask | auto | off
	Local       []Local     `toml:"local"`
	Pick        Pick        `toml:"pick"`
	Speed       Speed       `toml:"speed"`
	Calibration Calibration `toml:"calibration"`
	Report      Report      `toml:"report"`
}

// Default is what a fresh install uses.
func Default() Config {
	return Config{
		Routing:     "ask",
		Pick:        Pick{Prefer: "fastest"},
		Speed:       Speed{MaxSlower: 20, ClaudeModel: "auto"},
		Calibration: Calibration{CapUSD: 0, Rungs: []string{"haiku"}},
		Report:      Report{WeeklySummary: true, SupportPrompt: true, SupportMinUSD: 5},
	}
}

// Path is the config file in dir.
func Path(dir string) string { return filepath.Join(dir, "config.toml") }

// Load reads the config in dir, or returns the defaults when there's none.
func Load(dir string) (Config, error) {
	c := Default()
	b, err := os.ReadFile(Path(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if _, err := toml.Decode(string(b), &c); err != nil {
		return c, err
	}
	if err := c.Speed.check(); err != nil {
		return c, fmt.Errorf("%s: %w", Path(dir), err)
	}
	if c.Pick.Prefer == "" {
		c.Pick.Prefer = "fastest"
	}
	if err := c.Pick.check(c.Local); err != nil {
		return c, fmt.Errorf("%s: %w", Path(dir), err)
	}
	for i := range c.Local {
		if c.Local[i].Loop == "" {
			c.Local[i].Loop = "builtin"
		}
		if c.Local[i].Quant == "" {
			c.Local[i].Quant = "auto"
		}
	}
	return c, nil
}

// Save writes the config to dir, readable only by the user.
func Save(dir string, c Config) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.WriteString("# Hometown Discount settings. Every default costs $0 extra.\n\n")
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		return err
	}
	tmp := Path(dir) + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, Path(dir))
}

// Tasks are the names [speed.by_task] accepts: skill checks and the job
// types replays grade.
func Tasks() []string {
	var out []string
	out = append(out, skills.Order...)
	for _, t := range jobtype.Order {
		if jobtype.Gradable(t) {
			out = append(out, t)
		}
	}
	return out
}

// check rejects by_task entries that would silently do nothing, so a typo
// can't send work somewhere the user didn't mean.
func (s Speed) check() error {
	if strings.TrimSpace(s.ClaudeModel) == "" {
		return errors.New(`[speed] claude_model: use "auto" or a model id like "claude-opus-5-5"`)
	}
	keys := make([]string, 0, len(s.ByTask))
	for k := range s.ByTask {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		task, size, hasSize := strings.Cut(k, ":")
		if !contains(Tasks(), task) || (hasSize && !contains(skills.SizeOrder, size)) {
			return fmt.Errorf("[speed.by_task] %q: use a task (%s), optionally with :small, :medium or :large", k, strings.Join(Tasks(), ", "))
		}
		switch v := s.ByTask[k].(type) {
		case int64, float64:
		case string:
			if v != "always" && v != "never" {
				return fmt.Errorf("[speed.by_task] %q = %q: use a percent, \"always\" or \"never\"", k, v)
			}
		default:
			return fmt.Errorf("[speed.by_task] %q: use a percent, \"always\" or \"never\"", k)
		}
	}
	return nil
}

// check rejects pick settings that would silently do nothing.
func (p Pick) check(local []Local) error {
	if p.Prefer != "fastest" && p.Prefer != "listed" {
		return fmt.Errorf(`[pick] prefer = %q: use "fastest" or "listed"`, p.Prefer)
	}
	var models []string
	for _, l := range local {
		models = append(models, l.Model)
	}
	keys := make([]string, 0, len(p.ByTask))
	for k := range p.ByTask {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		skill, size, hasSize := strings.Cut(k, ":")
		if !contains(skills.Order, skill) || (hasSize && !contains(skills.SizeOrder, size)) {
			return fmt.Errorf("[pick.by_task] %q: use a skill (%s), optionally with :small, :medium or :large", k, strings.Join(skills.Order, ", "))
		}
		if !contains(models, p.ByTask[k]) {
			return fmt.Errorf("[pick.by_task] %q = %q: that model isn't in your [[local]] list (%s)", k, p.ByTask[k], strings.Join(models, ", "))
		}
	}
	return nil
}

// PinnedFor is the model pinned to a skill and size, "" when none: the most
// specific setting wins.
func (p Pick) PinnedFor(skill, size string) string {
	if m, ok := p.ByTask[skill+":"+size]; ok {
		return m
	}
	return p.ByTask[skill]
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
