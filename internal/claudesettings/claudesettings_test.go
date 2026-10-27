package claudesettings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const existing = `{
  "model": "opus",
  "permissions": {
    "allow": ["Bash(git status)"]
  },
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "say done"}]}],
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "guard.sh"}]}
    ]
  },
  "theme": "dark"
}
`

func isOurs(cmd string) bool { return IsCommand(cmd, "snapshot") }

var hook = Hook{Event: "PreToolUse", Matcher: "Task|Agent", Command: "htd snapshot", Timeout: 10}

// Adding the hook keeps every other setting, hook and key order; adding it
// again changes nothing; removing it gives the original settings back.
func TestAddAndRemoveKeepOtherSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(path, []byte(existing), 0o640)

	added, err := Add(path, hook, isOurs)
	if err != nil || !added {
		t.Fatalf("add: %v %v", added, err)
	}
	b, _ := os.ReadFile(path)
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, b)
	}
	got := string(b)
	for _, want := range []string{`"model": "opus"`, `"allow": ["Bash(git status)"]`, `"command": "say done"`, `"command": "guard.sh"`, `"command": "htd snapshot"`, `"matcher": "Task|Agent"`} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %s:\n%s", want, got)
		}
	}
	if strings.Index(got, `"model"`) > strings.Index(got, `"permissions"`) || strings.Index(got, `"hooks"`) > strings.Index(got, `"theme"`) {
		t.Errorf("key order changed:\n%s", got)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o640 {
		t.Errorf("file mode changed to %v", st.Mode().Perm())
	}
	if on, _ := Has(path, "PreToolUse", isOurs); !on {
		t.Error("Has should see the hook")
	}

	if added, err := Add(path, Hook{Event: "PreToolUse", Matcher: "Task", Command: "/opt/bin/htd snapshot"}, isOurs); err != nil || added {
		t.Errorf("a second add should do nothing: %v %v", added, err)
	}

	removed, err := Remove(path, "PreToolUse", isOurs)
	if err != nil || !removed {
		t.Fatalf("remove: %v %v", removed, err)
	}
	var before, after map[string]any
	json.Unmarshal([]byte(existing), &before)
	b, _ = os.ReadFile(path)
	json.Unmarshal(b, &after)
	if bj, aj := mustJSON(before), mustJSON(after); bj != aj {
		t.Errorf("removing should restore the settings:\nbefore %s\nafter  %s", bj, aj)
	}
	if removed, _ := Remove(path, "PreToolUse", isOurs); removed {
		t.Error("nothing left to remove")
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// No settings file yet: one is made with just the hook; removing the only
// hook leaves no empty "hooks" behind.
func TestNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude", "settings.json")
	if added, err := Add(path, hook, isOurs); err != nil || !added {
		t.Fatalf("add: %v %v", added, err)
	}
	b, _ := os.ReadFile(path)
	var doc struct {
		Hooks map[string][]group `json:"hooks"`
	}
	if err := json.Unmarshal(b, &doc); err != nil || len(doc.Hooks["PreToolUse"]) != 1 {
		t.Fatalf("want one PreToolUse group: %v\n%s", err, b)
	}
	Remove(path, "PreToolUse", isOurs)
	if b, _ := os.ReadFile(path); strings.TrimSpace(string(b)) != "{}" {
		t.Errorf("want an empty object, got %s", b)
	}
}

// A settings file that isn't valid JSON is left alone.
func TestBadFileUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(path, []byte(`{"model": `), 0o600)
	if _, err := Add(path, hook, isOurs); err == nil {
		t.Error("want an error")
	}
	if b, _ := os.ReadFile(path); string(b) != `{"model": ` {
		t.Errorf("file changed: %s", b)
	}
}

func TestIsCommand(t *testing.T) {
	for cmd, want := range map[string]bool{
		"htd snapshot": true, "/Users/me/.local/bin/htd snapshot": true, "'/Applications/My Tools/htd' snapshot": true,
		"htd snapshots": false, "nothtd snapshot": false, "htd mcp": false, "echo htd snapshot": false,
	} {
		if got := IsCommand(cmd, "snapshot"); got != want {
			t.Errorf("IsCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
}
