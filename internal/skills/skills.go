// Package skills generates skill checks from a user's own repo:
// small, bounded tasks whose answers are computed by code, so grading is
// exact, free and needs no Claude output.
package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/secrets"
)

// Bar is the fixed pass rate a skill must prove (as a Wilson lower bound).
// Skill checks have an exact answer key, so there's no reference model to
// measure a bar against. At 0.85, proof takes 24 of 24 or 36 of 37 correct;
// 0.90 would need 37 of 37, too strict to be useful.
const Bar = 0.85

// TypePrefix marks skill-check results in the store, apart from replays.
const TypePrefix = "skill:"

// SystemPrompt is used for skill checks and for ask_local alike, so what's
// proven is measured the way it's used.
const SystemPrompt = "Answer using only the files given. Be exact: give line numbers, names and values exactly as they appear. Answer in the format asked, with no explanation. If the files don't contain the answer, say so."

// Skills, in replay order (easiest first).
const (
	JSON   = "json"
	Lookup = "lookup"
	Lines  = "lines"
	List   = "list"
	Files  = "files"
)

// Order is the run order, easiest skills first.
var Order = []string{JSON, Lookup, Lines, List, Files}

// Sizes, by the number of input lines the model must read.
const (
	Small  = "small"  // ≤ 200 lines
	Medium = "medium" // ≤ 1,000 lines
	Large  = "large"  // ≤ 4,000 lines
)

// SizeOrder is the run order within a skill.
var SizeOrder = []string{Small, Medium, Large}

// SizeOf maps an input line count to a size, or "" when it's too big to use.
func SizeOf(lines int) string {
	switch {
	case lines <= 200:
		return Small
	case lines <= 1000:
		return Medium
	case lines <= 4000:
		return Large
	}
	return ""
}

// SizeOfInput sizes an input by lines and bytes, so a few very long lines
// can't pass as a small input: small ≤ 200 lines and 16 KB, medium ≤ 1,000
// lines and 64 KB, large ≤ 4,000 lines and 256 KB.
func SizeOfInput(lines, bytes int) string {
	byBytes := ""
	switch {
	case bytes <= 16<<10:
		byBytes = Small
	case bytes <= 64<<10:
		byBytes = Medium
	case bytes <= 256<<10:
		byBytes = Large
	default:
		return ""
	}
	byLines := SizeOf(lines)
	if byLines == "" {
		return ""
	}
	if rank(byBytes) > rank(byLines) {
		return byBytes
	}
	return byLines
}

func rank(size string) int {
	for i, s := range SizeOrder {
		if s == size {
			return i
		}
	}
	return -1
}

// MaxLines is the largest input a skill check uses.
const MaxLines = 4000

// Task is one skill check.
type Task struct {
	ID     string
	Skill  string
	Size   string
	Prompt string
	Answer string // canonical expected answer; Grade parses the model's reply
	Lines  int    // input lines the model reads
}

// PerCell caps how many tasks each skill × size gets. Proof is checked at 24,
// 37, 50 and 75 checks: with 80, a model that's right 95% of the time can
// usually be proven, not just a near-perfect one.
const PerCell = 80

// Generate builds skill checks from the tracked text files of a git repo.
// The same repo state always gives the same tasks.
func Generate(repo string) ([]Task, error) {
	files, err := trackedFiles(repo)
	if err != nil {
		return nil, err
	}
	if abs, err := filepath.Abs(repo); err == nil {
		repo = abs
	}
	if r, err := filepath.EvalSymlinks(repo); err == nil {
		repo = r
	}
	seed := sha256.Sum256([]byte(repo))
	rng := rand.New(rand.NewPCG(uint64(seed[0])<<8|uint64(seed[1]), uint64(seed[2])))
	rng.Shuffle(len(files), func(i, k int) { files[i], files[k] = files[k], files[i] })

	cells := map[string]int{}
	var tasks []Task
	add := func(t Task) {
		key := t.Skill + "/" + t.Size
		if t.Size == "" || cells[key] >= PerCell {
			return
		}
		h := sha256.Sum256([]byte(t.Skill + "\x00" + t.Prompt))
		t.ID = t.Skill + "-" + hex.EncodeToString(h[:6])
		cells[key]++
		tasks = append(tasks, t)
	}

	byDir := map[string][]string{}
	root, err := os.OpenRoot(repo)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for _, rel := range files {
		b, ok := readTracked(root, rel)
		if !ok {
			continue
		}
		byDir[filepath.Dir(rel)] = append(byDir[filepath.Dir(rel)], rel)
		text := string(b)
		lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
		size := SizeOfInput(len(lines), len(b))
		if size == "" {
			continue
		}
		if strings.HasSuffix(rel, ".json") {
			if t, ok := jsonTask(rel, b, rng); ok {
				t.Size, t.Lines = size, len(lines)
				add(t)
			}
			continue
		}
		lang := langOf(rel)
		if lang == nil {
			continue
		}
		if t, ok := lookupTask(rel, lines, lang, rng); ok {
			t.Size, t.Lines = size, len(lines)
			add(t)
		}
		if t, ok := listTask(rel, lines, lang); ok {
			t.Size, t.Lines = size, len(lines)
			add(t)
		}
		if t, ok := linesTask(rel, lines, rng); ok {
			t.Size, t.Lines = size, len(lines)
			add(t)
		}
	}
	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		if t, ok := filesTask(root, byDir[d], rng); ok {
			add(t)
		}
	}
	sort.SliceStable(tasks, func(i, k int) bool { return tasks[i].Lines < tasks[k].Lines })
	return tasks, nil
}

// readTracked reads a tracked file for a skill check: regular files only (no
// symlinks), never secrets, at most 512 KB, text only. Reads go through
// os.Root, so nothing outside the repo is touched.
func readTracked(root *os.Root, rel string) ([]byte, bool) {
	if secrets.Sensitive(rel) {
		return nil, false
	}
	st, err := root.Lstat(rel)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 512<<10 {
		return nil, false
	}
	b, err := root.ReadFile(rel)
	if err != nil || !isText(b) {
		return nil, false
	}
	return b, true
}

func trackedFiles(repo string) ([]string, error) {
	out, err := exec.Command("git", "-C", repo, "ls-files", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("%s isn't a git repo: %w", repo, err)
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f == "" || skipPath(f) {
			continue
		}
		files = append(files, f)
	}
	sort.Strings(files)
	return files, nil
}

var skipParts = []string{"node_modules/", "vendor/", "dist/", "build/", ".next/", "testdata/", "fixtures/", "__snapshots__/"}

func skipPath(p string) bool {
	for _, s := range skipParts {
		if strings.Contains(p, s) {
			return true
		}
	}
	base := filepath.Base(p)
	return strings.HasSuffix(base, ".min.js") || strings.HasSuffix(base, ".lock") || base == "package-lock.json" || base == "pnpm-lock.yaml"
}

func isText(b []byte) bool {
	if len(b) == 0 || len(b) > 512<<10 {
		return false
	}
	for _, c := range b[:min(len(b), 8000)] {
		if c == 0 {
			return false
		}
	}
	return true
}

// numbered renders file text the way Claude Code's Read tool shows it.
func numbered(lines []string) string {
	var b strings.Builder
	for i, l := range lines {
		fmt.Fprintf(&b, "%d\t%s\n", i+1, l)
	}
	return b.String()
}

func fileBlock(rel string, lines []string) string {
	return "File: " + rel + "\n```\n" + numbered(lines) + "```\n"
}

// --- lookup ---------------------------------------------------------------

type lang struct {
	name    string
	defs    *regexp.Regexp // group 1 = symbol name, at a definition
	exports *regexp.Regexp // group 1 = exported symbol name
	private func(string) bool
}

var (
	goLang = &lang{
		name:    "Go",
		defs:    regexp.MustCompile(`^(?:func(?: \([^)]*\))? |type |const |var )(\w+)`),
		exports: regexp.MustCompile(`^(?:func(?: \([^)]*\))? |type )([A-Z]\w*)`),
	}
	tsLang = &lang{
		name:    "TypeScript/JavaScript",
		defs:    regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?(?:declare\s+)?(?:abstract\s+)?(?:async\s+)?(?:function\*?|class|const\s+enum|const|let|var|interface|type|enum)\s+([A-Za-z_$][\w$]*)`),
		exports: regexp.MustCompile(`^export\s+(?:default\s+)?(?:declare\s+)?(?:abstract\s+)?(?:async\s+)?(?:function\*?|class|const\s+enum|const|let|var|interface|type|enum)\s+([A-Za-z_$][\w$]*)`),
	}
	pyLang = &lang{
		name:    "Python",
		defs:    regexp.MustCompile(`^(?:async\s+)?(?:def|class)\s+(\w+)`),
		exports: regexp.MustCompile(`^(?:async\s+)?(?:def|class)\s+([A-Za-z]\w*)`),
	}
)

// keywords can follow `class` or `function` when a declaration has no name
// (export default class extends Base).
var keywords = map[string]bool{"extends": true, "implements": true, "async": true, "function": true}

// nested finds a name defined anywhere below the top level (methods, inner
// functions), which would make "where is X defined" ambiguous.
func nested(lines []string, name string) bool {
	re := regexp.MustCompile(`^\s+(?:export\s+)?(?:static\s+)?(?:async\s+)?(?:def|class|func|function|const|let|var|type|interface)\s+` + regexp.QuoteMeta(name) + `\b|^\s+(?:async\s+)?` + regexp.QuoteMeta(name) + `\s*\(.*\)\s*\{`)
	for _, l := range lines {
		if re.MatchString(l) {
			return true
		}
	}
	return false
}

func langOf(rel string) *lang {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".go":
		return goLang
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		return tsLang
	case ".py":
		return pyLang
	}
	return nil
}

func lookupTask(rel string, lines []string, lg *lang, rng *rand.Rand) (Task, bool) {
	at := map[string][]int{}
	var names []string
	for i, l := range lines {
		if m := lg.defs.FindStringSubmatch(l); m != nil && len(m[1]) >= 3 && !keywords[m[1]] {
			if len(at[m[1]]) == 0 {
				names = append(names, m[1])
			}
			at[m[1]] = append(at[m[1]], i+1)
		}
	}
	var unique []string
	for _, n := range names {
		if len(at[n]) == 1 && !nested(lines, n) {
			unique = append(unique, n)
		}
	}
	if len(unique) == 0 {
		return Task{}, false
	}
	name := unique[rng.IntN(len(unique))]
	return Task{
		Skill:  Lookup,
		Prompt: fileBlock(rel, lines) + fmt.Sprintf("\nOn which line is `%s` defined (its top-level definition) in %s? Reply with the line number only.", name, rel),
		Answer: fmt.Sprint(at[name][0]),
	}, true
}

// --- list -----------------------------------------------------------------

func listTask(rel string, lines []string, lg *lang) (Task, bool) {
	if lg == goLang {
		for _, l := range lines {
			if strings.HasPrefix(l, "type (") { // grouped types make "all exported types" ambiguous
				return Task{}, false
			}
		}
	}
	seen := map[string]bool{}
	var names []string
	for _, l := range lines {
		if m := lg.exports.FindStringSubmatch(l); m != nil && !seen[m[1]] && !keywords[m[1]] {
			seen[m[1]] = true
			names = append(names, m[1])
		}
	}
	if len(names) < 2 || len(names) > 40 {
		return Task{}, false
	}
	what := "names declared with the `export` keyword at the start of a line (functions, classes, constants, types, interfaces, enums; not `export { ... }` lists)"
	switch lg {
	case goLang:
		what = "exported top-level functions, methods and types (names starting with a capital letter; not constants or variables)"
	case pyLang:
		what = "top-level functions and classes (not methods inside classes, and skip names starting with an underscore)"
	}
	sort.Strings(names)
	return Task{
		Skill:  List,
		Prompt: fileBlock(rel, lines) + fmt.Sprintf("\nList the %s declared in %s. Reply with one name per line and nothing else.", what, rel),
		Answer: strings.Join(names, "\n"),
	}, true
}

// --- lines ----------------------------------------------------------------

// linesTask asks which lines contain an identifier that appears on 2 to 12
// distinct lines: a scan the model must do carefully, line by line.
func linesTask(rel string, lines []string, rng *rand.Rand) (Task, bool) {
	at := map[string][]int{}
	for i, l := range lines {
		seen := map[string]bool{}
		for _, w := range identWord.FindAllString(l, -1) {
			if !seen[w] {
				seen[w] = true
				at[w] = append(at[w], i+1)
			}
		}
	}
	var words []string
	for w, ls := range at {
		if len(ls) >= 2 && len(ls) <= 12 {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return Task{}, false
	}
	sort.Strings(words)
	word := words[rng.IntN(len(words))]
	nums := make([]string, len(at[word]))
	for i, n := range at[word] {
		nums[i] = fmt.Sprint(n)
	}
	return Task{
		Skill:  Lines,
		Prompt: fileBlock(rel, lines) + fmt.Sprintf("\nWhich lines of %s contain the exact word `%s` (case-sensitive, whole word)? Reply with the line numbers, comma-separated, and nothing else.", rel, word),
		Answer: strings.Join(nums, ","),
	}, true
}

// --- json -----------------------------------------------------------------

func jsonTask(rel string, b []byte, rng *rand.Rand) (Task, bool) {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return Task{}, false
	}
	var leaves [][2]string
	walkJSON(v, "", func(path string, leaf any) {
		enc, _ := json.Marshal(leaf)
		if path != "" && len(enc) <= 80 {
			leaves = append(leaves, [2]string{path, string(enc)})
		}
	})
	if len(leaves) < 3 {
		return Task{}, false
	}
	pick := leaves[rng.IntN(len(leaves))]
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	return Task{
		Skill:  JSON,
		Prompt: fileBlock(rel, lines) + fmt.Sprintf("\nIn %s, what is the value at `%s`? Reply with the value as JSON (strings in double quotes) and nothing else.", rel, pick[0]),
		Answer: pick[1],
	}, true
}

func walkJSON(v any, path string, leaf func(string, any)) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p := k
			if strings.ContainsAny(k, ". []") {
				p = fmt.Sprintf("[%q]", k)
			}
			if path != "" {
				if strings.HasPrefix(p, "[") {
					p = path + p
				} else {
					p = path + "." + p
				}
			}
			walkJSON(t[k], p, leaf)
		}
	case []any:
		for i, x := range t {
			walkJSON(x, fmt.Sprintf("%s[%d]", path, i), leaf)
		}
	default:
		leaf(path, v)
	}
}

// --- files ----------------------------------------------------------------

var identWord = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]{5,}\b`)

func filesTask(root *os.Root, dirFiles []string, rng *rand.Rand) (Task, bool) {
	if len(dirFiles) < 3 || len(dirFiles) > 12 {
		return Task{}, false
	}
	type doc struct {
		rel   string
		lines []string
	}
	var docs []doc
	total, bytes := 0, 0
	for _, rel := range dirFiles {
		b, ok := readTracked(root, rel)
		if !ok || langOf(rel) == nil {
			continue
		}
		lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
		total += len(lines)
		bytes += len(b)
		docs = append(docs, doc{rel, lines})
	}
	if len(docs) < 3 || SizeOfInput(total, bytes) == "" {
		return Task{}, false
	}
	// Candidate words: identifiers that appear in some files but not all.
	in := map[string]map[string]bool{}
	for _, d := range docs {
		for _, w := range identWord.FindAllString(strings.Join(d.lines, "\n"), -1) {
			if in[w] == nil {
				in[w] = map[string]bool{}
			}
			in[w][d.rel] = true
		}
	}
	var words []string
	for w, set := range in {
		if len(set) >= 1 && len(set) <= len(docs)/2 && len(set) <= 5 {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return Task{}, false
	}
	sort.Strings(words)
	word := words[rng.IntN(len(words))]
	var b strings.Builder
	for _, d := range docs {
		b.WriteString(fileBlock(d.rel, d.lines))
		b.WriteString("\n")
	}
	var want []string
	for rel := range in[word] {
		want = append(want, rel)
	}
	sort.Strings(want)
	return Task{
		Skill:  Files,
		Size:   SizeOfInput(total, bytes),
		Lines:  total,
		Prompt: b.String() + fmt.Sprintf("\nWhich of these files contain the exact word `%s` (case-sensitive, whole word)? Reply with the file paths, one per line, and nothing else.", word),
		Answer: strings.Join(want, "\n"),
	}, true
}
