package jobtype

import (
	"regexp"
	"strings"
)

// Size tiers.
const (
	Small = "small"
	Large = "large"
)

// Tiers in replay order.
var Tiers = []string{Small, Large}

var exhaustive = regexp.MustCompile(`(?i)\b(all|every|everywhere|entire|whole|thorough(ly)?|exhaustive(ly)?|comprehensive(ly)?)\b|across the (code ?base|repo|project|app)`)

// pathish matches file paths and folders: "src/app.ts", "web/src/", "config.go".
var pathish = regexp.MustCompile(`(?:[\w@.-]+/)+[\w@.-]*|\b[\w-]+\.(?:go|ts|tsx|js|jsx|py|rb|rs|java|kt|swift|sql|md|json|ya?ml|toml|css|html|sh)\b`)

// PredictTier sizes a job from its prompt alone, as a router must before the
// job runs: large when it asks for exhaustive coverage, is long, or names
// four or more files or folders.
func PredictTier(prompt string) string {
	if exhaustive.MatchString(prompt) || len(prompt) > 2000 {
		return Large
	}
	seen := map[string]bool{}
	for _, p := range pathish.FindAllString(prompt, -1) {
		seen[strings.TrimSuffix(p, "/")] = true
	}
	if len(seen) >= 4 {
		return Large
	}
	return Small
}

// ActualTier sizes a finished job from what it did: small when it used no
// more tool calls than the median for its type in the user's own history.
// The split is relative because subagent workloads vary too much for a fixed
// cutoff.
func ActualTier(toolCalls, typeMedian int) string {
	if toolCalls <= typeMedian {
		return Small
	}
	return Large
}

// ToolCalls counts a job's tool calls from its per-tool tally.
func ToolCalls(tools map[string]int) int {
	n := 0
	for _, c := range tools {
		n += c
	}
	return n
}

// TypeOrder is the replay order, easiest types first.
var TypeOrder = []string{Classify, Extract, Transform, Find, Review, Research}
