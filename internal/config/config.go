// Package config reads and writes ~/.htd/config.toml. Every default costs $0 extra.
package config

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
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

// Config is the whole file.
type Config struct {
	Routing     string      `toml:"routing"` // ask | auto | off
	Local       []Local     `toml:"local"`
	Calibration Calibration `toml:"calibration"`
	Report      Report      `toml:"report"`
}

// Default is what a fresh install uses.
func Default() Config {
	return Config{
		Routing:     "ask",
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
