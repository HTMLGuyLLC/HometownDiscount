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
		"no url":     {func(s *Settings) { s.Models = []ModelSetting{{URL: "localhost", Model: "m"}} }, "server address"},
		"no model":   {func(s *Settings) { s.Models = []ModelSetting{{URL: "http://127.0.0.1:8000"}} }, "model's name"},
		"pin":        {func(s *Settings) { s.Pins = []Pin{{Skill: "lookup", Model: "not-listed"}} }, "not-listed"},
		"bad theme":  {func(s *Settings) { s.Theme = "neon" }, "theme"},
		"bad size":   {func(s *Settings) { s.Limits = []Limit{{Task: "find", Size: "medium", Rule: "never"}} }, "small or large"},
		"negative":   {func(s *Settings) { s.MaxSlower = -5 }, "0 or more"},
		"no claude":  {func(s *Settings) { s.ClaudeModel = " " }, "claude_model"},
		"bad prefer": {func(s *Settings) { s.Prefer = "random" }, "prefer"},
	} {
		s := base
		s.Models, s.Pins, s.Limits = nil, nil, nil
		tc.edit(&s)
		if _, _, err := s.Config(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want it to mention %q", name, err, tc.want)
		}
	}
}

func post(h http.Handler, path, token string, body any, headers map[string]string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(b))
	req.Host = "127.0.0.1:8765"
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-HTD-Token", token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// The page reads the settings and its token, previews the file and saves it.
func TestSettingsAPI(t *testing.T) {
	dir := t.TempDir()
	h := Handler(func() (Report, error) { return Build(Input{}), nil }, dir)
	var got settingsReply
	if w := get(h, "GET", "/api/settings", "127.0.0.1:8765"); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Token == "" || got.Settings == nil || got.Options == nil {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	s := *got.Settings
	s.Theme = "dark"
	s.Models = []ModelSetting{{URL: "http://127.0.0.1:8000", Model: "tiny"}}

	w := post(h, "/api/settings/preview", got.Token, s, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `theme = \"dark\"`) {
		t.Errorf("preview: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(config.Path(dir)); err == nil {
		t.Error("a preview must not save")
	}

	w = post(h, "/api/settings", got.Token, s, map[string]string{"Origin": "http://127.0.0.1:8765"})
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	c, err := config.Load(dir)
	if err != nil || c.UI.Theme != "dark" || len(c.Local) != 1 || c.Local[0].Model != "tiny" || c.Local[0].Loop != "builtin" {
		t.Errorf("saved: %+v, %v", c, err)
	}

	s.Theme = "neon"
	if w := post(h, "/api/settings", got.Token, s, nil); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "theme") {
		t.Errorf("bad settings: %d %s", w.Code, w.Body.String())
	}
	if c, _ := config.Load(dir); c.UI.Theme != "dark" {
		t.Error("bad settings must not be saved")
	}
}

// Only this page can change settings: other sites can't read its token or
// send JSON with it, and a form post can't do either.
func TestSettingsWriteGuards(t *testing.T) {
	dir := t.TempDir()
	h := Handler(func() (Report, error) { return Report{}, nil }, dir)
	var got settingsReply
	json.Unmarshal(get(h, "GET", "/api/settings", "127.0.0.1:1").Body.Bytes(), &got)
	s := *got.Settings

	for name, w := range map[string]*httptest.ResponseRecorder{
		"no token":    post(h, "/api/settings", "", s, nil),
		"wrong token": post(h, "/api/settings", "nope", s, nil),
		"other site":  post(h, "/api/settings", got.Token, s, map[string]string{"Origin": "https://evil.example"}),
		"form post":   post(h, "/api/settings", got.Token, s, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}),
	} {
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if w := get(h, "GET", "/api/settings", "evil.example"); w.Code != http.StatusForbidden {
		t.Errorf("another host can't read the token: %d", w.Code)
	}
	if w := post(h, "/api/report", got.Token, s, nil); w.Code != http.StatusMethodNotAllowed {
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
	h := Handler(func() (Report, error) { return Report{}, nil }, dir)
	var got settingsReply
	json.Unmarshal(get(h, "GET", "/api/settings", "127.0.0.1:1").Body.Bytes(), &got)
	if !strings.Contains(got.Error, "max_slowr") || got.Settings == nil || got.Settings.Prefer != "fastest" {
		t.Errorf("%+v", got)
	}
}
