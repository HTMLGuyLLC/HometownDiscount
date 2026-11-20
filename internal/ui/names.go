package ui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/jobtype"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/skills"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/speed"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

// NotRecorded stands in for a model name the log doesn't have.
const NotRecorded = "Not recorded"

// replayNames are replayed job types in plain words.
var replayNames = map[string]string{
	jobtype.Find:      "Find jobs",
	jobtype.Extract:   "Extract jobs",
	jobtype.Classify:  "Classify jobs",
	jobtype.Transform: "Transform jobs",
	jobtype.Review:    "Review jobs",
	jobtype.Research:  "Research jobs",
}

// taskName is a stored task type in plain words.
func taskName(typ string) string {
	if s, ok := strings.CutPrefix(typ, skills.TypePrefix); ok {
		if n, ok := skills.Names[s]; ok {
			return n
		}
		return s
	}
	if n, ok := replayNames[typ]; ok {
		return n
	}
	return typ + " jobs"
}

func sizesFor(isSkill bool, tier string) []string {
	switch {
	case isSkill:
		return skills.SizeOrder
	case tier == "":
		return []string{"any"}
	}
	return []string{"small", "large"}
}

func sizeWords(isSkill bool, size string) string {
	if isSkill && skills.SizeLines(size) > 0 {
		return skills.SizeWords(size)
	}
	switch size {
	case "small":
		return "few tool calls"
	case "large":
		return "many tool calls"
	}
	return "any size"
}

// setupWords splits a stored setup into a short name, e.g. "Qwen3.8-27B",
// and the rest, e.g. "mlx-community · 4bit · builtin loop · thinking off".
func setupWords(id string) (string, string) {
	var r store.Rung
	if json.Unmarshal([]byte(id), &r) != nil || r.Model == "" {
		return id, ""
	}
	name := r.Model
	var parts []string
	if org, short, ok := strings.Cut(r.Model, "/"); ok && short != "" {
		name = short
		parts = append(parts, org)
	}
	if r.Quant != "" {
		parts = append(parts, r.Quant)
	}
	if r.Loop != "" {
		parts = append(parts, r.Loop+" loop")
	}
	if e := r.EffortWords(); e != "" {
		parts = append(parts, e)
	}
	return name, strings.Join(parts, " · ")
}

// shortModel drops the publisher from a model id: "mlx-community/Qwen" is "Qwen".
func shortModel(id string) string {
	if _, short, ok := strings.Cut(id, "/"); ok && short != "" {
		return short
	}
	return id
}

// paceWords is how a local model's time compares with Claude's, short
// enough for a table cell.
func paceWords(v speed.Verdict) string {
	switch {
	case !v.Compared:
		return "speed not compared yet"
	case v.Ratio >= 0.45 && v.Ratio <= 0.55:
		return "half Claude's time"
	case v.Ratio < 0.95:
		return fmt.Sprintf("%.0f%% faster than Claude", (1-v.Ratio)*100)
	case v.Ratio <= 1.05:
		return "as fast as Claude"
	case v.Ratio < 2:
		return fmt.Sprintf("%.0f%% slower than Claude", (v.Ratio-1)*100)
	}
	return fmt.Sprintf("%.1f× as long as Claude", v.Ratio)
}

// limitWords is the speed rule that applies, in plain words.
func limitWords(r speed.Rule) string {
	switch r.Mode {
	case "always":
		if r.From == "default" {
			return "speed check is off"
		}
		return "your setting: always local"
	case "never":
		return "your setting: never local"
	}
	return fmt.Sprintf("speed limit: up to %g%% slower", r.MaxSlower)
}

// thinkingOff is whether a stored setup runs with thinking turned off.
func thinkingOff(id string) bool {
	var r store.Rung
	return json.Unmarshal([]byte(id), &r) == nil && r.Effort == store.ThinkingOff
}
