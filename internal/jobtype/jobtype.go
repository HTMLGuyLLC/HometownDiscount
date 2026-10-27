// Package jobtype sorts subagent jobs into job types with simple, visible rules.
package jobtype

import (
	"regexp"
	"strings"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
)

// Types a cheaper rung can be graded on (rubric v1).
const (
	Find      = "find"
	Extract   = "extract"
	Classify  = "classify"
	Transform = "transform"
	Review    = "review"   // audits and reviews: a list of findings
	Research  = "research" // web research, replayed on the pages the original job fetched
)

// Types that are shown on the map but not graded in v1.
const (
	Explore = "explore"
	Browser = "browser" // driving a browser: stateful, not replayable
	Plan    = "plan"
	Tests   = "tests"
	Edit    = "edit"
	Other   = "other"
)

// Order is the display order on the map.
var Order = []string{Find, Extract, Classify, Transform, Review, Research, Explore, Browser, Plan, Tests, Edit, Other}

// Gradable reports whether a type has a mechanical grading rule in v1.
func Gradable(t string) bool {
	switch t {
	case Find, Extract, Classify, Transform, Review, Research:
		return true
	}
	return false
}

// Replayable reports whether a job of the given type can be replayed in the
// sandbox: a gradable type that used only tools the replay can provide and
// ran no project tooling.
// Research jobs may also use web tools; the replay serves the recorded pages.
func Replayable(j *history.Job, typ string) bool {
	if !Gradable(typ) || UsesTooling(j) {
		return false
	}
	for name := range j.Tools {
		if !readOnlyTools[name] && !(typ == Research && webTools[name]) {
			return false
		}
	}
	return true
}

// readOnlyTools can run in a replay sandbox. Anything else (writes, the web,
// browsers, MCP servers, nested agents) makes a job not replayable.
var readOnlyTools = map[string]bool{
	"Read": true, "Grep": true, "Glob": true, "Bash": true, "LSP": true,
	"ToolSearch": true, "TodoWrite": true, "SubagentHandback": true,
}

var editTools = map[string]bool{"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true}
var webTools = map[string]bool{"WebFetch": true, "WebSearch": true}

// browserTool matches MCP browser servers. Other MCP tools don't decide the
// type; they only make a job not replayable.
func browserTool(name string) bool {
	n := strings.ToLower(name)
	return strings.HasPrefix(n, "mcp__") && (strings.Contains(n, "browser") || strings.Contains(n, "chrome") || strings.Contains(n, "playwright"))
}

type rule struct {
	typ string
	re  *regexp.Regexp
}

// Checked in order; the first match wins.
var rules = []rule{
	{Classify, regexp.MustCompile(`\b(classif|categori[sz]|triage)`)},
	{Extract, regexp.MustCompile(`\b(extract|list (all|every|the)|collect|enumerate|inventory|pull out|gather)`)},
	{Transform, regexp.MustCompile(`\b(convert|transform|reformat|translate|rewrite .* as|to json|to yaml)`)},
	{Find, regexp.MustCompile(`\b(find|locate|where|search|look for|look up .* in|grep|which (file|files|function)|trace)`)},
	{Tests, regexp.MustCompile(`\b(run (the )?tests?|tests? (pass|fail)|e2e|test suite)`)},
	{Review, regexp.MustCompile(`\b(review|audit|verify|check|scan|inspect|security|validate)`)},
	{Research, regexp.MustCompile(`\b(research|investigate|compare|docs|documentation|best practice)`)},
	{Plan, regexp.MustCompile(`\b(plan|design|architect|propose)`)},
	{Explore, regexp.MustCompile(`\b(explore|understand|map|survey|overview|how does|walk through)`)},
}

// Shell commands that change files or the repo. A job that ran one is an
// edit job, whatever its description says.
var (
	shellWrite = regexp.MustCompile(`\b(sed|perl)\s+(-[a-zA-Z]*i|--in-place)|\btee\b|(^|[;&|]\s*|\s)(mv|cp|rm|mkdir|touch|chmod|ln|truncate|patch)\s|\bfind\b.*\s-delete\b|\bgit\s+(commit|add|checkout|switch|reset|restore|stash|merge|rebase|push|apply|cherry-pick|rm|mv|tag|clone)\b|\.(write|write_text|write_bytes|writeFileSync|writeFile)\(|open\([^)]*['"][wa]`)
	redirect   = regexp.MustCompile(`(^|[^&>])>{1,2}\s*([^\s;&|<>]+)`)
	quoted     = regexp.MustCompile(`'[^']*'|"(?:[^"\\]|\\.)*"`)
	// Project tooling a replay can't run: the repo copy has no installed
	// dependencies, no network and no git history.
	tooling = regexp.MustCompile(`\b(git|npm|pnpm|yarn|npx|bun|bunx|deno|node|ts-node|tsx|pip|pip3|poetry|cargo|make|gradle|gradlew|mvn|bundle|rake|rspec|docker|kubectl|terraform|supabase|vercel|gh|curl|wget)\b|\bgo\s+(test|run|build|vet|mod)\b|\b(pytest|jest|vitest|playwright|tsc|eslint)\b`)
)

// WritesFiles reports whether any of a job's shell commands changed files.
func WritesFiles(j *history.Job) bool {
	for _, c := range j.Commands {
		if shellWrite.MatchString(c) {
			return true
		}
		// Quoted text can hold ">" (grep "=>", awk '$1 > 5'): only real
		// redirects count, and only to a file (not /dev/null or another fd).
		for _, m := range redirect.FindAllStringSubmatch(quoted.ReplaceAllString(c, "''"), -1) {
			if t := m[2]; t != "/dev/null" && !strings.HasPrefix(t, "&") {
				return true
			}
		}
	}
	return false
}

// UsesTooling reports whether a job ran project tooling (tests, package
// scripts, builds, network tools) that a replay can't provide.
func UsesTooling(j *history.Job) bool {
	for _, c := range j.Commands {
		if tooling.MatchString(c) {
			return true
		}
	}
	return false
}

// Of returns a job's type.
func Of(j *history.Job) string {
	for name := range j.Tools {
		if editTools[name] {
			return Edit
		}
	}
	if WritesFiles(j) {
		return Edit
	}
	for name := range j.Tools {
		if browserTool(name) {
			return Browser
		}
	}
	for name := range j.Tools {
		if webTools[name] {
			return Research
		}
	}
	if j.AgentType == "Plan" {
		return Plan
	}
	text := strings.ToLower(j.Description)
	for _, r := range rules {
		if r.re.MatchString(text) {
			return r.typ
		}
	}
	if j.AgentType == "Explore" {
		return Explore
	}
	return Other
}
