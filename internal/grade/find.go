package grade

import (
	"fmt"
	"strings"
)

// maxCitedRange caps how many lines of a cited range are read when checking
// that a cited line holds a key identifier.
const maxCitedRange = 20

// entity is one thing the reference names: a file, a cited line, or an identifier.
type entity struct {
	path     string
	line     int // 0 for a file or an identifier
	end      int
	ident    string
	isIdent  bool
	wantLine bool
}

func (e entity) key() string {
	switch {
	case e.isIdent:
		return "I|" + e.ident
	case e.wantLine:
		return fmt.Sprintf("L|%s|%d|%d", e.path, e.line, e.end)
	}
	return "P|" + e.path
}

// Find grades a "find" answer. Its key is file paths, path:line citations and
// backticked identifiers.
//
// It passes when all of these hold:
//   - nothing is fabricated: every path exists and every cited line is in its file;
//   - the candidate has at least 80% of the reference's entities;
//   - every cited path:line holds a key identifier from the same line of the
//     answer, when that line has one (a miss is listed in Fabricated).
//
// Judgment calls:
//   - A path the reference cites with a line is one "line" entity per cited line,
//     and the candidate must cite that file within 5 lines of it. A path cited
//     without a line is one "file" entity, met by any mention of the file.
//   - An identifier is met by the word anywhere in the candidate, backticked or not.
//   - The key identifiers for a citation are the backticked words on the same
//     text line as the citation. A dotted name is also met by its last part.
//   - Paths are compared as written, after dropping a leading "./". Rewrite
//     absolute paths to repo-relative ones before grading.
//   - A reference with no entities can't be graded ("no-entities").
//   - Extra counts the files the candidate names that the reference doesn't.
func Find(ref, cand string, ws Workspace) Result {
	want := findEntities(ref)
	if len(want) == 0 {
		return ungraded("no-entities")
	}
	got := pathRefs(cand)
	hit := 0
	for _, e := range want {
		if e.metBy(cand, got) {
			hit++
		}
	}
	res := Result{Recall: float64(hit) / float64(len(want))}
	res.Fabricated = calibrate(
		append(fabricated(pathAnchors(got), ws), citedLineProblems(cand, ws)...),
		append(fabricated(pathAnchors(pathRefs(ref)), ws), citedLineProblems(ref, ws)...))
	res.Extra = extraFiles(ref, got)

	var failed []string
	if !atLeast(res.Recall, findRecall) {
		failed = append(failed, "recall")
	}
	if len(res.Fabricated) > 0 {
		failed = append(failed, "fabrication")
	}
	return verdict(res, failed)
}

func findEntities(text string) []entity {
	refs := pathRefs(text)
	withLine := map[string]bool{}
	for _, r := range refs {
		if r.Line > 0 {
			withLine[r.Path] = true
		}
	}
	var out []entity
	seen := map[string]bool{}
	add := func(e entity) {
		if k := e.key(); !seen[k] {
			seen[k] = true
			out = append(out, e)
		}
	}
	for _, r := range refs {
		switch {
		case r.Line > 0:
			add(entity{path: r.Path, line: r.Line, end: r.End, wantLine: true})
		case !withLine[r.Path]:
			add(entity{path: r.Path})
		}
	}
	for _, s := range backticks(text) {
		if id, ok := spanIdent(s); ok {
			add(entity{ident: id, isIdent: true})
		}
	}
	return out
}

// spanIdent reads a backticked span as an identifier: one word of 3+ characters,
// not a path, with a trailing "()" dropped.
func spanIdent(s string) (string, bool) {
	s = strings.TrimSuffix(s, "()")
	if len(s) < 3 || strings.ContainsAny(s, " \t/") || !strings.ContainsAny(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		return "", false
	}
	if len(pathRefs(s)) > 0 {
		return "", false
	}
	return s, true
}

func (e entity) metBy(cand string, got []pathRef) bool {
	switch {
	case e.isIdent:
		return containsWord(cand, e.ident)
	case e.wantLine:
		for _, g := range got {
			if samePath(g.Path, e.path) && g.Line > 0 && lineGap(e.line, e.end, g.Line, g.End) <= lineWindow {
				return true
			}
		}
	default:
		for _, g := range got {
			if samePath(g.Path, e.path) {
				return true
			}
		}
	}
	return false
}

// lineGap is the number of lines between two cited ranges (0 when they overlap).
func lineGap(aFrom, aTo, bFrom, bTo int) int {
	return max(0, aFrom-bTo, bFrom-aTo)
}

// citedLineProblems checks, for each text line of the answer that cites
// path:line and backticks identifiers, that a cited line holds one of them.
func citedLineProblems(cand string, ws Workspace) []string {
	if ws == nil {
		return nil
	}
	var out []string
	for _, text := range strings.Split(cand, "\n") {
		var idents []string
		for _, s := range backticks(text) {
			if id, ok := spanIdent(s); ok {
				idents = append(idents, id)
			}
		}
		if len(idents) == 0 {
			continue
		}
		for _, r := range pathRefs(text) {
			if r.Line == 0 {
				continue
			}
			seen, found := false, false
			for n := r.Line; n <= min(r.End, r.Line+maxCitedRange-1); n++ {
				if line, ok := ws.Line(r.Path, n); ok {
					seen = true
					found = found || holdsAny(line, idents)
				}
			}
			if seen && !found {
				out = append(out, fmt.Sprintf("cited line lacks `%s`: %s:%d", strings.Join(idents, "`/`"), r.Path, r.Line))
			}
		}
	}
	return out
}

// holdsAny reports whether line holds one of the identifiers (or the last part
// of a dotted one).
func holdsAny(line string, idents []string) bool {
	for _, id := range idents {
		if strings.Contains(line, id) {
			return true
		}
		if i := strings.LastIndex(id, "."); i >= 0 && len(id)-i-1 >= 3 && strings.Contains(line, id[i+1:]) {
			return true
		}
	}
	return false
}

// extraFiles counts the files cand names that the reference doesn't.
func extraFiles(ref string, got []pathRef) int {
	refs := pathRefs(ref)
	extra := map[string]bool{}
	for _, g := range got {
		known := false
		for _, r := range refs {
			if samePath(r.Path, g.Path) {
				known = true
				break
			}
		}
		if !known {
			extra[g.Path] = true
		}
	}
	return len(extra)
}
