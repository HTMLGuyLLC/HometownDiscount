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
	URL   string `toml:"url"`
	Model string `toml:"model"`
	Quant string `toml:"quant"` // "auto" = detect, or e.g. "4bit", "Q4_K_M"
	Loop  string `toml:"loop"`  // the agent loop replays run in: "builtin"
	// AllowRemote opts in to a server off this machine and local network:
	// replays and ask_local would send code there.
	AllowRemote bool `toml:"allow_remote,omitempty"`
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
	Local   []Local `toml:"local"`
	Pick    Pick    `toml:"pick"`
	Speed   Speed   `toml:"speed"`
	UI      UI      `toml:"ui"`
	Privacy Privacy `toml:"privacy"`
}

// Privacy is what htd keeps about its own use. TrackHandoffs logs each
// ask_local call (task, size, model, outcome; never file contents) to
// ~/.htd/delegations.jsonl, which the savings report is built from.
type Privacy struct {
	TrackHandoffs bool `toml:"track_handoffs"`
}

// UI is how the scorecard page (`htd ui`) looks. Theme is "auto" (follow
// the computer's light or dark setting), "light" or "dark".
type UI struct {
	Theme string `toml:"theme"`
}

// Themes are the values [ui] theme accepts.
var Themes = []string{"auto", "light", "dark"}

// Default is what a fresh install uses.
func Default() Config {
	return Config{
		Pick:    Pick{Prefer: "fastest"},
		Speed:   Speed{MaxSlower: 20, ClaudeModel: "auto"},
		UI:      UI{Theme: "auto"},
		Privacy: Privacy{TrackHandoffs: true},
	}
}

// retired are settings older versions wrote that nothing uses now. They're
// accepted so an old file still loads, and dropped the next time it's saved.
var retired = []string{"routing", "calibration", "report", "local.effort"}

// Path is the config file in dir.
func Path(dir string) string { return filepath.Join(dir, "config.toml") }

// Load reads the config in dir, or returns the defaults when there's none.
func Load(dir string) (Config, error) {
	b, err := os.ReadFile(Path(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Default(), err
	}
	c, err := Parse(b)
	if err != nil {
		return c, fmt.Errorf("%s: %w", Path(dir), err)
	}
	return c, nil
}

// Parse reads a config file's contents over the defaults, and checks it
// the way Load does: a misspelled or unusable setting is an error.
func Parse(b []byte) (Config, error) {
	c := Default()
	md, err := toml.Decode(string(b), &c)
	if err != nil {
		return c, err
	}
	// A misspelled setting would otherwise be ignored without a word.
	for _, k := range md.Undecoded() {
		if !isRetired(k.String()) {
			return c, fmt.Errorf("unknown setting %q", k.String())
		}
	}
	if err := c.Speed.check(); err != nil {
		return c, err
	}
	if c.Pick.Prefer == "" {
		c.Pick.Prefer = "fastest"
	}
	if err := c.Pick.check(c.Local); err != nil {
		return c, err
	}
	if c.UI.Theme == "" {
		c.UI.Theme = "auto"
	}
	if !contains(Themes, c.UI.Theme) {
		return c, fmt.Errorf(`[ui] theme = %q: use "auto", "light" or "dark"`, c.UI.Theme)
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

// Encode is the config file's contents, as Save writes them. [ui] and
// [privacy] are left out while they're the defaults, so a file that never
// changed them still loads in older versions of htd, which don't know them.
func Encode(c Config) ([]byte, error) {
	f := struct {
		Local   []Local  `toml:"local"`
		Pick    Pick     `toml:"pick"`
		Speed   Speed    `toml:"speed"`
		UI      *UI      `toml:"ui,omitempty"`
		Privacy *Privacy `toml:"privacy,omitempty"`
	}{Local: c.Local, Pick: c.Pick, Speed: c.Speed}
	def := Default()
	if c.UI != def.UI {
		f.UI = &c.UI
	}
	if c.Privacy != def.Privacy {
		f.Privacy = &c.Privacy
	}
	var buf bytes.Buffer
	buf.WriteString("# Hometown Discount settings. Every default costs $0 extra.\n\n")
	if err := toml.NewEncoder(&buf).Encode(f); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Save writes the config to dir, readable only by the user. It writes a
// temporary file in the same folder and renames it over the old one, so a
// crash leaves the old file whole. If config.toml is a link, the file it
// points to is the one replaced, and the link stays.
func Save(dir string, c Config) (err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := Encode(c)
	if err != nil {
		return err
	}
	path := Path(dir)
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".config.toml.*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Chmod(f.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
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
		if !contains(Tasks(), task) {
			return fmt.Errorf("[speed.by_task] %q: use a task (%s), optionally with a size", k, strings.Join(Tasks(), ", "))
		}
		// Skill checks come in three sizes; replays are small or large.
		if sizes := skills.SizeOrder; hasSize && contains(skills.Order, task) && !contains(sizes, size) {
			return fmt.Errorf("[speed.by_task] %q: skill checks are small, medium or large", k)
		}
		if hasSize && !contains(skills.Order, task) && !contains(jobtype.Tiers, size) {
			return fmt.Errorf("[speed.by_task] %q: replays are small or large", k)
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

func isRetired(key string) bool {
	for _, r := range retired {
		if key == r || strings.HasPrefix(key, r+".") {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
