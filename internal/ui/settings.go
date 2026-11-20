package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/config"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/jobtype"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/skills"
)

// Settings is config.toml as the settings form edits it: plain fields and
// lists instead of TOML tables.
type Settings struct {
	Models        []ModelSetting `json:"models"`
	Prefer        string         `json:"prefer"` // fastest or listed
	Pins          []Pin          `json:"pins"`
	SpeedCheck    bool           `json:"speed_check"` // false: max_slower = -1
	MaxSlower     float64        `json:"max_slower"`
	ClaudeModel   string         `json:"claude_model"`
	Limits        []Limit        `json:"limits"`
	TrackHandoffs bool           `json:"track_handoffs"`
	Theme         string         `json:"theme"`
}

// ModelSetting is one [[local]] block.
type ModelSetting struct {
	URL         string `json:"url"`
	Model       string `json:"model"`
	Quant       string `json:"quant"`
	Loop        string `json:"loop"`
	AllowRemote bool   `json:"allow_remote"`
}

// Pin sends a skill (at one size, or "" for every size) to one model.
type Pin struct {
	Skill string `json:"skill"`
	Size  string `json:"size"`
	Model string `json:"model"`
}

// Limit is one [speed.by_task] entry: a percent, "always" or "never".
type Limit struct {
	Task    string  `json:"task"`
	Size    string  `json:"size"`
	Rule    string  `json:"rule"` // percent, always or never
	Percent float64 `json:"percent"`
}

// FromConfig is the form's view of a config.
func FromConfig(c config.Config) Settings {
	s := Settings{
		Models: []ModelSetting{}, Pins: []Pin{}, Limits: []Limit{},
		Prefer: c.Pick.Prefer, SpeedCheck: c.Speed.MaxSlower >= 0, MaxSlower: c.Speed.MaxSlower,
		ClaudeModel: c.Speed.ClaudeModel, TrackHandoffs: c.Privacy.TrackHandoffs, Theme: c.UI.Theme,
	}
	if !s.SpeedCheck {
		s.MaxSlower = config.Default().Speed.MaxSlower
	}
	for _, l := range c.Local {
		s.Models = append(s.Models, ModelSetting{URL: l.URL, Model: l.Model, Quant: l.Quant, Loop: l.Loop, AllowRemote: l.AllowRemote})
	}
	for _, k := range sortedKeys(c.Pick.ByTask) {
		skill, size, _ := strings.Cut(k, ":")
		s.Pins = append(s.Pins, Pin{Skill: skill, Size: size, Model: c.Pick.ByTask[k]})
	}
	for _, k := range sortedKeys(c.Speed.ByTask) {
		task, size, _ := strings.Cut(k, ":")
		l := Limit{Task: task, Size: size, Rule: "percent"}
		switch v := c.Speed.ByTask[k].(type) {
		case string:
			l.Rule = v
		case int64:
			l.Percent = float64(v)
		case float64:
			l.Percent = v
		}
		s.Limits = append(s.Limits, l)
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Config builds the config file from the form, and checks it the way htd
// checks the file when it reads it, plus what only a form can get wrong
// (an empty model, a URL that isn't one, a rule set twice). Errors are in
// the form's words, not the file's.
func (s Settings) Config() (config.Config, []byte, error) {
	c := config.Default()
	if s.Prefer != "fastest" && s.Prefer != "listed" {
		return c, nil, errors.New(`Which model goes first: choose "fastest" or "listed"`)
	}
	c.Pick.Prefer = s.Prefer
	c.Speed.MaxSlower = s.MaxSlower
	if !s.SpeedCheck {
		c.Speed.MaxSlower = -1
	} else if s.MaxSlower < 0 {
		return c, nil, errors.New("Speed limit: use 0 or more percent, or turn the speed check off")
	}
	c.Speed.ClaudeModel = strings.TrimSpace(s.ClaudeModel)
	if c.Speed.ClaudeModel == "" {
		return c, nil, errors.New(`Claude model: enter "auto" or a model id like claude-opus-5-5`)
	}
	c.Privacy.TrackHandoffs = s.TrackHandoffs
	if !slices.Contains(config.Themes, s.Theme) {
		return c, nil, errors.New("Theme: choose auto, light or dark")
	}
	c.UI.Theme = s.Theme
	var models []string
	for i, m := range s.Models {
		l := config.Local{URL: strings.TrimSpace(m.URL), Model: strings.TrimSpace(m.Model), Quant: strings.TrimSpace(m.Quant), Loop: m.Loop, AllowRemote: m.AllowRemote}
		if l.Loop == "" {
			l.Loop = "builtin"
		}
		if l.Quant == "" {
			l.Quant = "auto"
		}
		if u, err := url.Parse(l.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return c, nil, fmt.Errorf("Model %d: the server address should look like http://127.0.0.1:8000", i+1)
		}
		if l.Model == "" {
			return c, nil, fmt.Errorf("Model %d: enter the model's name as your server lists it", i+1)
		}
		c.Local = append(c.Local, l)
		models = append(models, l.Model)
	}
	if len(s.Pins) > 0 {
		c.Pick.ByTask = map[string]string{}
	}
	for _, p := range s.Pins {
		what := ruleName(p.Skill, p.Size)
		if !slices.Contains(skills.Order, p.Skill) {
			return c, nil, fmt.Errorf("Model choice: pick a task for %s", what)
		}
		if p.Size != "" && !slices.Contains(skills.SizeOrder, p.Size) {
			return c, nil, fmt.Errorf("Model choice for %s: pick small, medium or large, or any size", what)
		}
		if _, dup := c.Pick.ByTask[key(p.Skill, p.Size)]; dup {
			return c, nil, fmt.Errorf("Model choice for %s is set twice.", what)
		}
		if !slices.Contains(models, p.Model) {
			return c, nil, fmt.Errorf("The rule for %s uses %s, which isn't in your models anymore. Pick another model or remove the rule.", what, p.Model)
		}
		c.Pick.ByTask[key(p.Skill, p.Size)] = p.Model
	}
	if len(s.Limits) > 0 {
		c.Speed.ByTask = map[string]any{}
	}
	for _, l := range s.Limits {
		what := ruleName(l.Task, l.Size)
		if !slices.Contains(config.Tasks(), l.Task) {
			return c, nil, fmt.Errorf("Speed limit: pick a task for %s", what)
		}
		isSkill := slices.Contains(skills.Order, l.Task)
		if l.Size != "" && isSkill && !slices.Contains(skills.SizeOrder, l.Size) {
			return c, nil, fmt.Errorf("Speed limit for %s: skill checks are small, medium or large", what)
		}
		if l.Size != "" && !isSkill && !slices.Contains(jobtype.Tiers, l.Size) {
			return c, nil, fmt.Errorf("Speed limit for %s: replays are small or large", what)
		}
		if _, dup := c.Speed.ByTask[key(l.Task, l.Size)]; dup {
			return c, nil, fmt.Errorf("Speed limit for %s is set twice.", what)
		}
		switch l.Rule {
		case "always", "never":
			c.Speed.ByTask[key(l.Task, l.Size)] = l.Rule
		default:
			if l.Percent < 0 {
				return c, nil, fmt.Errorf("Speed limit for %s: use 0 or more percent", what)
			}
			c.Speed.ByTask[key(l.Task, l.Size)] = l.Percent
		}
	}
	b, err := config.Encode(c)
	if err != nil {
		return c, nil, err
	}
	parsed, err := config.Parse(b)
	if err != nil {
		return c, b, fmt.Errorf("These settings can't be saved: %v", err)
	}
	return parsed, b, nil
}

// ruleName is a task and size in plain words: "Find a definition (any size)".
func ruleName(task, size string) string {
	isSkill := slices.Contains(skills.Order, task)
	words := "any size"
	if size != "" {
		words = sizeWords(isSkill, size)
	}
	return taskName(taskType(task)) + " (" + words + ")"
}

func key(task, size string) string {
	if size == "" {
		return task
	}
	return task + ":" + size
}

// Options are the choices the form offers.
type Options struct {
	Skills      []Choice `json:"skills"`
	Tasks       []Choice `json:"tasks"` // skills and replayed job types
	SkillSizes  []string `json:"skill_sizes"`
	ReplaySizes []string `json:"replay_sizes"`
	Themes      []string `json:"themes"`
}

// Choice is a value with its plain words.
type Choice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

func options() Options {
	o := Options{SkillSizes: skills.SizeOrder, ReplaySizes: []string{"small", "large"}, Themes: config.Themes}
	for _, s := range skills.Order {
		o.Skills = append(o.Skills, Choice{s, skills.Names[s]})
	}
	for _, t := range config.Tasks() {
		o.Tasks = append(o.Tasks, Choice{t, taskName(taskType(t))})
	}
	return o
}

// taskType is a settings task name as stored: skills carry their prefix.
func taskType(task string) string {
	for _, s := range skills.Order {
		if s == task {
			return skills.TypePrefix + task
		}
	}
	return task
}

// settingsAPI serves the settings form. Who may call it is decided before
// it's reached (the host and the run's key); writes also have to be JSON from
// this page's own origin, and say which version of config.toml they were
// made from, so an edit made somewhere else in the meantime isn't lost.
type settingsAPI struct {
	dir string
	mu  sync.Mutex
}

func newSettingsAPI(dir string) *settingsAPI { return &settingsAPI{dir: dir} }

type settingsReply struct {
	Settings *Settings `json:"settings,omitempty"`
	Path     string    `json:"path,omitempty"`
	Options  *Options  `json:"options,omitempty"`
	TOML     string    `json:"toml,omitempty"`
	Error    string    `json:"error,omitempty"`
	Saved    bool      `json:"saved,omitempty"`
	// Version identifies the config.toml the form was made from ("" when
	// there's no file yet); the page sends it back, as X-HTD-Version, to save.
	Version *string `json:"version,omitempty"`
}

// VersionHeader carries the version a save was made from.
const VersionHeader = "X-HTD-Version"

func reply(w http.ResponseWriter, code int, r settingsReply) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(r)
}

// version is a fingerprint of config.toml's bytes, "" when there's no file.
func (a *settingsAPI) version() string {
	b, err := os.ReadFile(config.Path(a.dir))
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (a *settingsAPI) get(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	o := options()
	v := a.version()
	out := settingsReply{Path: tildePath(config.Path(a.dir)), Options: &o, Version: &v}
	c, err := config.Load(a.dir)
	if err != nil {
		out.Error = BrokenConfigWords(err)
		c = config.Default()
	}
	s := FromConfig(c)
	out.Settings = &s
	if b, err := config.Encode(c); err == nil {
		out.TOML = string(b)
	}
	reply(w, http.StatusOK, out)
}

// BrokenConfigWords is what the form says when config.toml can't be read.
func BrokenConfigWords(err error) string {
	return "Your config.toml has a problem: " + ConfigProblem(err) + ". The safest fix is to correct that line in the file. Saving this form replaces the whole file with what's shown here, which is the defaults, not your models."
}

// ConfigProblem is a config.Load error without the file's path (the page
// already says which file) or a closing period, ready to go in a sentence.
func ConfigProblem(err error) string {
	if inner := errors.Unwrap(err); inner != nil {
		err = inner
	}
	return strings.TrimRight(strings.TrimSpace(err.Error()), ".")
}

// tildePath shows a path in the home folder as ~/...
func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}

// write previews the file the form would save, or saves it.
func (a *settingsAPI) write(save bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := allowed(r); err != nil {
			reply(w, http.StatusForbidden, settingsReply{Error: err.Error()})
			return
		}
		var s Settings
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&s); err != nil {
			reply(w, http.StatusBadRequest, settingsReply{Error: "couldn't read the form: " + err.Error()})
			return
		}
		c, b, err := s.Config()
		if err != nil {
			reply(w, http.StatusUnprocessableEntity, settingsReply{Error: err.Error(), TOML: string(b)})
			return
		}
		if !save {
			reply(w, http.StatusOK, settingsReply{TOML: string(b)})
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		have := a.version()
		sent, sentAny := r.Header[http.CanonicalHeaderKey(VersionHeader)]
		if (sentAny && sent[0] != have) || (!sentAny && have != "") {
			reply(w, http.StatusConflict, settingsReply{Error: "config.toml changed since you opened this page. Reload your settings to see the change, then make your edit again."})
			return
		}
		if err := config.Save(a.dir, c); err != nil {
			reply(w, http.StatusInternalServerError, settingsReply{Error: "couldn't save: " + err.Error()})
			return
		}
		v := a.version()
		reply(w, http.StatusOK, settingsReply{TOML: string(b), Saved: true, Version: &v})
	}
}

// allowed checks a write came from this page: sent as JSON (a form post from
// another site can't do that) from this page's own origin.
func allowed(r *http.Request) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return errors.New("settings must be sent as JSON")
	}
	if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
		return errors.New("settings can only be changed from the htd ui page")
	}
	return nil
}
