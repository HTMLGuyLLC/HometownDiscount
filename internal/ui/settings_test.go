package ui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/config"
)

// The form and the file say the same thing both ways.
func TestSettingsRoundTrip(t *testing.T) {
	c := config.Default()
	c.Local = []config.Local{{URL: "http://127.0.0.1:8000", Model: "small", Quant: "auto", Loop: "builtin"}, {URL: "http://10.0.0.5:8000", Model: "big", Quant: "4bit", Loop: "builtin", AllowRemote: true}}
	c.Pick = config.Pick{Prefer: "listed", ByTask: map[string]string{"lookup": "small", "lines:large": "big"}}
	c.Speed.ByTask = map[string]any{"lookup:small": int64(5), "files:large": "never"}
	c.Privacy.TrackHandoffs = false
	c.UI.Theme = "light"

	s := FromConfig(c)
	if len(s.Models) != 2 || !s.Models[1].AllowRemote || len(s.Pins) != 2 || s.Pins[0].Skill != "lines" || s.Pins[0].Size != "large" {
		t.Fatalf("form: %+v", s)
	}
	if len(s.Limits) != 2 || s.Limits[0].Rule != "never" || s.Limits[1].Percent != 5 || s.TrackHandoffs || s.Theme != "light" || !s.SpeedCheck {
		t.Fatalf("form: %+v", s)
	}
	got, b, err := s.Config()
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	if got.Pick.ByTask["lines:large"] != "big" || got.Speed.ByTask["files:large"] != "never" || got.Speed.ByTask["lookup:small"] != 5.0 ||
		got.Privacy.TrackHandoffs || got.UI.Theme != "light" || !got.Local[1].AllowRemote || got.Pick.Prefer != "listed" {
		t.Errorf("back to a config: %+v", got)
	}
}

// Turning the speed check off is max_slower = -1, and back.
func TestSettingsSpeedOff(t *testing.T) {
	s := FromConfig(config.Default())
	s.SpeedCheck = false
	c, _, err := s.Config()
	if err != nil || c.Speed.MaxSlower != -1 {
		t.Fatalf("off: %v, %v", c.Speed.MaxSlower, err)
	}
	if back := FromConfig(c); back.SpeedCheck || back.MaxSlower != 20 {
		t.Errorf("read back: %+v", back)
	}
}

// What the form can get wrong is caught before anything is saved, in words.
func TestSettingsErrors(t *testing.T) {
	base := FromConfig(config.Default())
	for name, tc := range map[string]struct {
		edit func(*Settings)
		want string
	}{
		"no url":    {func(s *Settings) { s.Models = []ModelSetting{{URL: "localhost", Model: "m"}} }, "server address"},
		"no model":  {func(s *Settings) { s.Models = []ModelSetting{{URL: "http://127.0.0.1:8000"}} }, "model's name"},
		"pin":       {func(s *Settings) { s.Pins = []Pin{{Skill: "lookup", Model: "not-listed"}} }, "The rule for Find a definition (any size) uses not-listed, which isn't in your models anymore. Pick another model or remove the rule."},
		"bad theme": {func(s *Settings) { s.Theme = "neon" }, "Theme: choose auto, light or dark"},
		"dup limit": {func(s *Settings) {
			s.Limits = []Limit{{Task: "lookup", Rule: "never"}, {Task: "lookup", Rule: "percent", Percent: 5}}
		}, "Speed limit for Find a definition (any size) is set twice."},
		"dup pin": {func(s *Settings) {
			s.Models = []ModelSetting{{URL: "http://127.0.0.1:8000", Model: "m"}}
			s.Pins = []Pin{{Skill: "lookup", Size: "small", Model: "m"}, {Skill: "lookup", Size: "small", Model: "m"}}
		}, "Model choice for Find a definition (up to 200 lines) is set twice."},
		"unknown task": {func(s *Settings) {
			s.Limits = []Limit{{Task: "lookup", Rule: "never"}, {Task: "lookup", Size: "small", Rule: "always"}, {Task: "bogus", Rule: "never"}}
		}, "pick a task"},
		"bad size":   {func(s *Settings) { s.Limits = []Limit{{Task: "find", Size: "medium", Rule: "never"}} }, "small or large"},
		"negative":   {func(s *Settings) { s.MaxSlower = -5 }, "0 or more"},
		"no claude":  {func(s *Settings) { s.ClaudeModel = " " }, "Claude model"},
		"bad prefer": {func(s *Settings) { s.Prefer = "random" }, "Which model goes first"},
	} {
		s := base
		s.Models, s.Pins, s.Limits = nil, nil, nil
		tc.edit(&s)
		if _, _, err := s.Config(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want it to mention %q", name, err, tc.want)
		} else if strings.Contains(err.Error(), "[") || strings.Contains(err.Error(), "by_task") {
			t.Errorf("%s: %q reads like the file, not the form", name, err)
		}
	}
}

func post(h http.Handler, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(b))
	req.Host = "127.0.0.1:8765"
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: CookieName, Value: testKey})
	for k, v := range headers {
		switch v {
		case "":
			req.Header.Del(k)
		case "<empty>": // sent, with nothing in it
			req.Header[http.CanonicalHeaderKey(k)] = []string{""}
		default:
			req.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func version(v *string) map[string]string {
	if v == nil {
		return nil
	}
	return map[string]string{VersionHeader: *v}
}

func readSettings(t *testing.T, h http.Handler) settingsReply {
	t.Helper()
	var got settingsReply
	w := get(h, "GET", "/api/settings", "127.0.0.1:8765")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Settings == nil || got.Options == nil || got.Version == nil {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "token") {
		t.Errorf("no token any more: %s", w.Body.String())
	}
	return got
}

// The page reads the settings, previews the file and saves it, and each
// save hands back the version the next one is made from.
func TestSettingsAPI(t *testing.T) {
	dir := t.TempDir()
	h := Handler(func() (Report, error) { return Build(Input{}), nil }, dir, testKey)
	got := readSettings(t, h)
	if *got.Version != "" {
		t.Errorf("no file yet, no version: %q", *got.Version)
	}
	s := *got.Settings
	s.Theme = "dark"
	s.Models = []ModelSetting{{URL: "http://127.0.0.1:8000", Model: "tiny"}}

	w := post(h, "/api/settings/preview", s, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `theme = \"dark\"`) {
		t.Errorf("preview: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(config.Path(dir)); err == nil {
		t.Error("a preview must not save")
	}

	w = post(h, "/api/settings", s, map[string]string{"Origin": "http://127.0.0.1:8765", VersionHeader: *got.Version})
	var saved settingsReply
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &saved) != nil || !saved.Saved || saved.Version == nil || *saved.Version == "" {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	c, err := config.Load(dir)
	if err != nil || c.UI.Theme != "dark" || len(c.Local) != 1 || c.Local[0].Model != "tiny" || c.Local[0].Loop != "builtin" {
		t.Errorf("saved: %+v, %v", c, err)
	}
	if again := readSettings(t, h); *again.Version != *saved.Version {
		t.Errorf("the version after a save is the one it returned: %q vs %q", *again.Version, *saved.Version)
	}

	// The new version lets the page save again; the old one doesn't.
	s.Theme = "light"
	if w := post(h, "/api/settings", s, version(saved.Version)); w.Code != 200 {
		t.Errorf("second save: %d %s", w.Code, w.Body.String())
	}
	s.Theme = "neon"
	if w := post(h, "/api/settings", s, version(got.Version)); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Theme") {
		t.Errorf("bad settings: %d %s", w.Code, w.Body.String())
	}
	if c, _ := config.Load(dir); c.UI.Theme != "light" {
		t.Error("bad settings must not be saved")
	}
}

// A save made from an old copy of the page doesn't wipe out what changed
// in the file since.
func TestSettingsVersionConflict(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(config.Path(dir), []byte("[ui]\ntheme = \"light\"\n"), 0o600)
	h := Handler(func() (Report, error) { return Report{}, nil }, dir, testKey)
	got := readSettings(t, h)
	if len(*got.Version) != 64 {
		t.Errorf("version %q", *got.Version)
	}
	s := *got.Settings
	s.Theme = "dark"

	// Edited by hand (or by an AI) after the page loaded.
	os.WriteFile(config.Path(dir), []byte("[ui]\ntheme = \"light\"\n[speed]\nmax_slower = 5\n"), 0o600)
	for name, hdr := range map[string]map[string]string{"old version": version(got.Version), "no version": nil, "empty version": {VersionHeader: "<empty>"}} {
		w := post(h, "/api/settings", s, hdr)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "changed since you opened this page") {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if c, _ := config.Load(dir); c.UI.Theme != "light" || c.Speed.MaxSlower != 5 {
		t.Errorf("the newer file must be left alone: %+v", c)
	}
	// A preview isn't a save, so it doesn't check.
	if w := post(h, "/api/settings/preview", s, nil); w.Code != 200 {
		t.Errorf("preview: %d", w.Code)
	}
	// Reload, edit again, save.
	fresh := readSettings(t, h)
	s = *fresh.Settings
	s.Theme = "dark"
	if w := post(h, "/api/settings", s, version(fresh.Version)); w.Code != 200 {
		t.Errorf("after a reload: %d %s", w.Code, w.Body.String())
	}
	if c, _ := config.Load(dir); c.Speed.MaxSlower != 5 || c.UI.Theme != "dark" {
		t.Errorf("both edits kept: %+v", c)
	}
}

// Writes need JSON from this page's own origin.
func TestSettingsWriteGuards(t *testing.T) {
	dir := t.TempDir()
	h := Handler(func() (Report, error) { return Report{}, nil }, dir, testKey)
	got := readSettings(t, h)
	s := *got.Settings

	for name, w := range map[string]*httptest.ResponseRecorder{
		"other site": post(h, "/api/settings", s, map[string]string{"Origin": "https://evil.example"}),
		"form post":  post(h, "/api/settings", s, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}),
	} {
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	// Without the key cookie nothing is read or written.
	b, _ := json.Marshal(s)
	req := httptest.NewRequest("POST", "/api/settings", bytes.NewReader(b))
	req.Host = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("no cookie: %d", w.Code)
	}
	if w := get(h, "GET", "/api/settings", "evil.example"); w.Code != http.StatusForbidden {
		t.Errorf("another host can't read settings: %d", w.Code)
	}
	if w := post(h, "/api/report", s, nil); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("other paths stay read-only: %d", w.Code)
	}
	if _, err := os.Stat(config.Path(dir)); err == nil {
		t.Error("nothing should have been saved")
	}
}

// A broken config.toml still opens the form, with the defaults and why.
func TestSettingsBrokenFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(config.Path(dir), []byte("[speed]\nmax_slowr = 5\n"), 0o600)
	h := Handler(func() (Report, error) { return Report{}, nil }, dir, testKey)
	var got settingsReply
	json.Unmarshal(get(h, "GET", "/api/settings", "127.0.0.1:1").Body.Bytes(), &got)
	if !strings.Contains(got.Error, "max_slowr") || !strings.Contains(got.Error, "safest fix is to correct that line") || !strings.Contains(got.Error, "not your models") || got.Settings == nil || got.Settings.Prefer != "fastest" {
		t.Errorf("%+v", got)
	}
	if strings.Contains(got.Error, dir) || strings.Contains(got.Error, "..") {
		t.Errorf("the message should leave out the path and read as one sentence: %s", got.Error)
	}
}
