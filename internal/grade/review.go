package grade

import (
	"slices"
	"sort"
)

// jaccardMin is the content-word overlap that matches findings without anchors.
const jaccardMin = 0.4

// Review grades a review or audit answer by its findings.
//
// Both answers are split into findings (see SplitFindings). A reference finding
// is matched when a candidate finding shares one of its anchors: a cited line
// (same file, within 5 lines), a file path, a quote or backticked span, or an
// identifier. A pair with no shared anchor, where one of the two has no anchors
// at all, matches on content-word Jaccard >= 0.4 (file paths are left out of the
// words, so a long path doesn't dilute the overlap). Each candidate finding goes to
// at most one reference finding, strongest shared anchor first.
//
// It passes when the severity-weighted recall (weight 2 for high findings, else
// 1) is at least theta, every high reference finding is matched, and nothing in
// the candidate is fabricated (see fabricated: a missing path, a line past the
// end of its file, or a quote found in no file).
//
// Judgment calls:
//   - A path:line is its own anchor, not also a path anchor. A bare file name on
//     one side still matches a path:line on the other (same file). Two path:line
//     anchors on the same file far apart do not match.
//   - Matching runs in two passes so one candidate finding does not hide another
//     reference finding: the first pass only gives a candidate to a reference
//     finding that has no match yet; the second lets leftover candidates match
//     reference findings that are already matched. A candidate that matched
//     nothing is Extra.
//   - Backticked spans of 8+ characters are quotes and are checked against the
//     workspace, except file paths and shell commands. A suggested fix in backticks
//     that is in no file therefore counts as fabricated, as the plan says.
//   - A reference or candidate with no findings is an empty set only if it is
//     blank or says so ("No issues found."). Other prose with no list items is not
//     understood: an ungraded reference ("reference-unparsed"), a failing candidate
//     ("candidate-unparsed").
//   - An empty reference passes only an empty candidate, and the reverse. Its
//     Recall is 1.
//   - With a workspace, a quote only anchors a finding when it is in a file: a
//     quote in no file (a persona's words, a suggested fix) points nowhere in
//     the code, so it can't show that two findings are the same.
//   - When fewer than half of the reference's findings have an anchor, Graded is
//     false with Reason "low-anchor"; Pass, Recall and the rest are still computed.
func Review(ref, cand string, ws Workspace, theta float64) Result {
	rf, rok := parseFindings(ref)
	cf, cok := parseFindings(cand)
	switch {
	case !rok:
		return ungraded("reference-unparsed")
	case !cok && lowAnchor(rf, ws):
		return ungraded("low-anchor") // the job can't be graded, whatever the candidate wrote
	case !cok:
		return verdict(Result{}, []string{"candidate-unparsed"})
	}

	var anchors, refAnchors []Anchor
	for _, f := range cf {
		anchors = append(anchors, f.Anchors...)
	}
	for _, f := range rf {
		refAnchors = append(refAnchors, f.Anchors...)
	}
	res := Result{Fabricated: calibrate(fabricated(anchors, ws), fabricated(refAnchors, ws))}
	if ws != nil {
		rf, cf = verifiedQuotes(rf, ws), verifiedQuotes(cf, ws)
	}

	if len(rf) == 0 {
		res.Recall, res.Extra = 1, len(cf)
		var failed []string
		if len(cf) > 0 {
			failed = append(failed, "reference-empty")
		}
		return verdict(res, failed)
	}

	matched, used := matchFindings(rf, cf)
	var total, got float64
	highMissed := false
	for i, f := range rf {
		total += float64(f.Severity)
		if matched[i] {
			got += float64(f.Severity)
		} else if f.Severity >= 2 {
			highMissed = true
		}
	}
	res.Recall = got / total
	for _, u := range used {
		if !u {
			res.Extra++
		}
	}

	var failed []string
	if !atLeast(res.Recall, theta) {
		failed = append(failed, "recall")
	}
	if highMissed {
		failed = append(failed, "high-severity-missed")
	}
	if len(res.Fabricated) > 0 {
		failed = append(failed, "fabrication")
	}
	res = verdict(res, failed)

	if lowAnchor(rf, nil) { // rf's quotes are already verified
		res.Graded, res.Reason = false, "low-anchor"
	}
	return res
}

// lowAnchor reports whether fewer than half of the findings have an anchor
// (with a workspace, quotes count only when they're in a file).
func lowAnchor(fs []Finding, ws Workspace) bool {
	if ws != nil {
		fs = verifiedQuotes(fs, ws)
	}
	anchored := 0
	for _, f := range fs {
		if len(f.Anchors) > 0 {
			anchored++
		}
	}
	return anchored*2 < len(fs)
}

// edge is a possible match between reference finding r and candidate finding c.
type edge struct {
	r, c     int
	strength Kind // 0 for a Jaccard match
	jaccard  float64
}

// matchFindings returns which reference findings are matched and which
// candidate findings were used.
func matchFindings(rf, cf []Finding) (matched, used []bool) {
	rw, cw := make([]map[string]bool, len(rf)), make([]map[string]bool, len(cf))
	for i, f := range rf {
		rw[i] = contentWords(withoutPaths(f.Text))
	}
	for j, f := range cf {
		cw[j] = contentWords(withoutPaths(f.Text))
	}
	var edges []edge
	for i, r := range rf {
		for j, c := range cf {
			s := sharedKind(r.Anchors, c.Anchors)
			j2 := jaccard(rw[i], cw[j])
			switch {
			case s > 0:
				edges = append(edges, edge{i, j, s, j2})
			case (len(r.Anchors) == 0 || len(c.Anchors) == 0) && j2 >= jaccardMin-epsilon:
				edges = append(edges, edge{i, j, 0, j2})
			}
		}
	}
	sort.SliceStable(edges, func(a, b int) bool {
		if edges[a].strength != edges[b].strength {
			return edges[a].strength > edges[b].strength
		}
		return edges[a].jaccard > edges[b].jaccard
	})
	matched, used = make([]bool, len(rf)), make([]bool, len(cf))
	for pass := 0; pass < 2; pass++ {
		for _, e := range edges {
			if used[e.c] || (pass == 0 && matched[e.r]) {
				continue
			}
			used[e.c], matched[e.r] = true, true
		}
	}
	return matched, used
}

// sharedKind is the strongest kind of anchor two findings share, or 0.
func sharedKind(a, b []Anchor) Kind {
	var best Kind
	for _, x := range a {
		for _, y := range b {
			if k := pairKind(x, y); k > best {
				best = k
			}
		}
	}
	return best
}

func pairKind(x, y Anchor) Kind {
	switch {
	case x.Kind == KindLine && y.Kind == KindLine:
		if samePath(x.Path, y.Path) && lineGap(x.Line, x.End, y.Line, y.End) <= lineWindow {
			return KindLine
		}
	case (x.Kind == KindPath || x.Kind == KindLine) && (y.Kind == KindPath || y.Kind == KindLine):
		if samePath(x.Path, y.Path) {
			return KindPath
		}
	case x.Kind == y.Kind && x.Text == y.Text:
		return x.Kind
	}
	return 0
}

// verifiedQuotes returns the findings with only the quote anchors that occur
// in the workspace.
func verifiedQuotes(fs []Finding, ws Workspace) []Finding {
	out := make([]Finding, len(fs))
	for i, f := range fs {
		f.Anchors = slices.DeleteFunc(slices.Clone(f.Anchors), func(a Anchor) bool {
			return a.Kind == KindQuote && !ws.Contains(a.Text)
		})
		out[i] = f
	}
	return out
}
