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
	MaxSlower float64        `toml:"max_slower"` // percent; -1 turns the speed check off
	ByTask    map[string]any `toml:"by_task,omitempty"`
}

// Config is the whole file.
type Config struct {
	Routing     string      `toml:"routing"` // ask | auto | off
	Local       []Local     `toml:"local"`
	Speed       Speed       `toml:"speed"`
	Calibration Calibration `toml:"calibration"`
	Report      Report      `toml:"report"`
}

// Default is what a fresh install uses.
func Default() Config {
	return Config{
		Routing:     "ask",
		Speed:       Speed{MaxSlower: 20},
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

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
