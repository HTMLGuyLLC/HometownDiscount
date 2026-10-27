// Package grade grades a cheaper model's answer against the original answer
// (the reference) with mechanical rules only.
//
// Each graded job type has one entry point: Find, Extract, Classify,
// Transform, Review and Research. They all return a Result. Nothing in this
// package calls a model.
//
// Shared conventions:
//   - A nil Workspace (or nil Recording) switches off every check that needs it.
//   - Fabrication checks are calibrated per job against the reference (see
//     calibrate): the candidate is only held to checks the original answer
//     passes, so the original answer always passes its own grade.
//   - Thresholds are compared with a tiny epsilon, so 4/5 passes "at least 0.8".
//   - Reason is a short code. For an ungraded job it says why ("low-anchor",
//     "no-key", ...). For a graded job that failed it lists the rules that
//     failed, comma separated ("recall, fabrication"). It is empty on a pass.
package grade

import "strings"

// Workspace answers questions about the files the job could read (repo snapshot).
// A nil Workspace turns off every check that needs one.
type Workspace interface {
	FileExists(path string) bool            // a file or a directory
	Line(path string, n int) (string, bool) // 1-based line text
	LineCount(path string) (int, bool)
	// Contains reports whether text appears in any readable file. It is given
	// whitespace-normalized text, so it should compare whitespace loosely.
	Contains(text string) bool
}

// Recording is the web pages and searches a research job made.
// A nil Recording turns off every check that needs one.
type Recording interface {
	HasURL(normalizedURL string) bool // fetched, or returned by a recorded search
	PagesContain(text string) bool    // appears in any recorded page or search result
}

// Result is the grade of one candidate answer.
type Result struct {
	Pass       bool
	Recall     float64  // weighted recall of the reference's key items (0..1)
	Fabricated []string // anchors, URLs and facts that don't exist (find also lists mis-cited lines)
	Extra      int      // candidate items that are not in the reference
	// Graded is false when the job can't be graded (Reason says why). For a
	// "low-anchor" review Pass is still computed, for comparing
	// Jaccard-only matching with hand labels, but it must not count toward
	// proof. For every other ungraded reason Pass is false.
	Graded bool
	Reason string
}

// Thresholds from the rubric. θ_review and θ_research are passed in.
const (
	findRecall        = 0.8
	extractF1         = 0.95
	classifyAgreement = 0.95
	lineWindow        = 5 // a cited line matches within this many lines
	epsilon           = 1e-9
)

func atLeast(x, min float64) bool { return x >= min-epsilon }

// ratio is hit/total, and 1 when there is nothing to hit.
func ratio(hit, total int) float64 {
	if total == 0 {
		return 1
	}
	return float64(hit) / float64(total)
}

// ungraded is the result for a job the rules can't grade.
func ungraded(reason string) Result { return Result{Reason: reason} }

// verdict finishes a graded result: it passes when no rule failed.
func verdict(r Result, failed []string) Result {
	r.Graded = true
	r.Pass = len(failed) == 0
	r.Reason = strings.Join(failed, ", ")
	return r
}

// highPrecision fabrication kinds name things that exist or don't. The rest
// (quotes, facts, a cited line's identifiers) also depend on how an answer is
// written: paraphrase in backticks, line numbers that drift, pages recorded
// as summaries.
var highPrecision = map[string]bool{"missing path": true, "line past end of file": true}

// calibrate keeps the candidate's fabrication findings that the reference,
// checked the same way on the same job, doesn't share. An item the reference
// also has is never held against the candidate. When the reference trips a
// low-precision check at all, that check can't tell made-up text from how
// answers are written on this job, so the whole kind is dropped.
func calibrate(cand, ref []string) []string {
	refItems, refKinds := map[string]bool{}, map[string]bool{}
	for _, f := range ref {
		refItems[f], refKinds[fabKind(f)] = true, true
	}
	var out []string
	for _, f := range cand {
		if k := fabKind(f); refItems[f] || refKinds[k] && !highPrecision[k] {
			continue
		}
		out = append(out, f)
	}
	return out
}

// fabKind is the kind of a fabrication finding: the text before its colon.
func fabKind(f string) string {
	if strings.HasPrefix(f, "cited line lacks") {
		return "cited line lacks"
	}
	k, _, _ := strings.Cut(f, ":")
	return k
}
