// Package claudesettings adds and removes htd's hook in Claude Code's
// settings.json. Everything else in the file is kept as written: other keys,
// their order and their formatting.
package claudesettings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Path is the user's settings file: $CLAUDE_CONFIG_DIR/settings.json, or
// ~/.claude/settings.json.
func Path() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "settings.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

// Hook is one command hook.
type Hook struct {
	Event   string // e.g. "PreToolUse"
	Matcher string // tool names, e.g. "Task|Agent"
	Command string
	Timeout int // seconds
}

type command struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

type group struct {
	Matcher string            `json:"matcher,omitempty"`
	Hooks   []json.RawMessage `json:"hooks"`
}

// Has reports whether a hook whose command satisfies match is set for the
// event.
func Has(path, event string, match func(cmd string) bool) (bool, error) {
	_, hooks, err := load(path)
	if err != nil {
		return false, err
	}
	groups, err := groupsOf(hooks, event)
	if err != nil {
		return false, err
	}
	for _, g := range groups {
		for _, raw := range g.Hooks {
			var c command
			if json.Unmarshal(raw, &c) == nil && match(c.Command) {
				return true, nil
			}
		}
	}
	return false, nil
}

// Add sets the hook, creating the file if needed. It reports false when a
// hook satisfying match was already there.
func Add(path string, h Hook, match func(cmd string) bool) (bool, error) {
	if ok, err := Has(path, h.Event, match); err != nil || ok {
		return false, err
	}
	doc, hooks, err := load(path)
	if err != nil {
		return false, err
	}
	groups, err := groupsOf(hooks, h.Event)
	if err != nil {
		return false, err
	}
	raw, _ := json.Marshal(command{Type: "command", Command: h.Command, Timeout: h.Timeout})
	groups = append(groups, group{Matcher: h.Matcher, Hooks: []json.RawMessage{raw}})
	return true, save(path, doc, hooks, h.Event, groups)
}

// Remove deletes every hook for the event whose command satisfies match. It
// reports whether there was one.
func Remove(path, event string, match func(cmd string) bool) (bool, error) {
	doc, hooks, err := load(path)
	if err != nil {
		return false, err
	}
	groups, err := groupsOf(hooks, event)
	if err != nil {
		return false, err
	}
	removed := false
	var kept []group
	for _, g := range groups {
		var keep []json.RawMessage
		for _, raw := range g.Hooks {
			var c command
			if json.Unmarshal(raw, &c) == nil && match(c.Command) {
				removed = true
				continue
			}
			keep = append(keep, raw)
		}
		if len(keep) > 0 {
			g.Hooks = keep
			kept = append(kept, g)
		}
	}
	if !removed {
		return false, nil
	}
	return true, save(path, doc, hooks, event, kept)
}

// object is a JSON object that keeps its keys in order and its values as
// written.
type object struct {
	keys   []string
	values map[string]json.RawMessage
}

func parseObject(data []byte) (*object, error) {
	o := &object{values: map[string]json.RawMessage{}}
	if len(bytes.TrimSpace(data)) == 0 {
		return o, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := t.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		if _, dup := o.values[key]; !dup {
			o.keys = append(o.keys, key)
		}
		o.values[key] = v
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return o, nil
}

func (o *object) set(key string, v json.RawMessage) {
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = v
}

func (o *object) remove(key string) {
	delete(o.values, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			return
		}
	}
}

// encode writes the object with two-space indents; values that weren't
// changed keep their own formatting.
func (o *object) encode(indent string) []byte {
	if len(o.keys) == 0 {
		return []byte("{}")
	}
	var b bytes.Buffer
	b.WriteString("{\n")
	for i, k := range o.keys {
		kb, _ := json.Marshal(k)
		b.WriteString(indent + "  ")
		b.Write(kb)
		b.WriteString(": ")
		b.Write(o.values[k])
		if i < len(o.keys)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString(indent + "}")
	return b.Bytes()
}

func load(path string) (doc, hooks *object, err error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}
	if doc, err = parseObject(data); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	if hooks, err = parseObject(doc.values["hooks"]); err != nil {
		return nil, nil, fmt.Errorf("%s: \"hooks\": %w", path, err)
	}
	return doc, hooks, nil
}

func groupsOf(hooks *object, event string) ([]group, error) {
	var groups []group
	if raw, ok := hooks.values[event]; ok {
		if err := json.Unmarshal(raw, &groups); err != nil {
			return nil, fmt.Errorf("\"hooks\".%q: %w", event, err)
		}
	}
	return groups, nil
}

func save(path string, doc, hooks *object, event string, groups []group) error {
	if len(groups) == 0 {
		hooks.remove(event)
	} else {
		raw, err := json.MarshalIndent(groups, "    ", "  ")
		if err != nil {
			return err
		}
		hooks.set(event, raw)
	}
	if len(hooks.keys) == 0 {
		doc.remove("hooks")
	} else {
		doc.set("hooks", hooks.encode("  "))
	}
	out := append(doc.encode(""), '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	mode := fs.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// IsCommand reports whether cmd runs the given htd subcommand, whatever
// path htd was called by.
func IsCommand(cmd, sub string) bool {
	prog, ok := strings.CutSuffix(strings.TrimSpace(cmd), " "+sub)
	return ok && filepath.Base(strings.Trim(strings.TrimSpace(prog), `"'`)) == "htd"
}
