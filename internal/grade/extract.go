package grade

import (
	"regexp"
	"sort"
	"strings"
)

// item is one extracted list entry.
type item struct {
	raw       string // trimmed, original case: what to look for in the workspace
	key       string // normalized: what to compare
	checkable bool   // raw is plain text that should occur in the workspace
}

// Extract grades an "extract" answer: a list of items, as bullets, numbered
// lines, plain lines, or a JSON array or object.
//
// Items are normalized (trimmed, lowercased, spaces collapsed, no bullet and no
// trailing punctuation) and compared as sets. It passes when the set F1 is at
// least 0.95 and no candidate item is fabricated. A candidate item the reference
// lacks is fabricated when ws.Contains can't find it in the workspace.
//
// Judgment calls:
//   - When any line is a bullet or numbered item, only those lines are items
//     (the rest is preamble). Otherwise every non-blank line is an item, except
//     headings and lines ending in ":".
//   - A JSON array gives one item per element: strings and numbers as text,
//     objects and arrays as canonical JSON. A JSON object gives one "key: value"
//     item per key. JSON may sit in a fenced block after a preamble.
//   - Only plain text items (lines, JSON strings and numbers) are looked up in
//     the workspace; objects and "key: value" items are not.
//   - Two empty lists agree (F1 = 1). An empty reference with a non-empty
//     candidate fails, and the reverse too.
//   - Recall is the share of the reference's items found; Extra counts the
//     candidate's items the reference lacks.
//   - A reference whose plain-text items mostly aren't in the workspace is a
//     summary written as a list, not an extraction: exact matching can't
//     judge a rewording, so it isn't graded ("not-literal").
func Extract(ref, cand string, ws Workspace) Result {
	want, got := extractItems(ref), extractItems(cand)
	if ws != nil && !literal(want, ws) {
		return ungraded("not-literal")
	}
	wantSet, gotSet := map[string]bool{}, map[string]bool{}
	for _, it := range want {
		wantSet[it.key] = true
	}
	for _, it := range got {
		gotSet[it.key] = true
	}
	inter := 0
	for k := range wantSet {
		if gotSet[k] {
			inter++
		}
	}
	precision, recall := ratio(inter, len(gotSet)), ratio(inter, len(wantSet))
	f1 := 0.0
	if precision+recall > 0 {
		f1 = 2 * precision * recall / (precision + recall)
	}
	res := Result{Recall: recall, Extra: len(gotSet) - inter}
	if ws != nil {
		var cf, rf []string
		for _, it := range got {
			if !wantSet[it.key] && it.checkable && !ws.Contains(it.raw) {
				cf = append(cf, "item not in the inputs: "+it.raw)
			}
		}
		for _, it := range want {
			if it.checkable && !ws.Contains(it.raw) {
				rf = append(rf, "item not in the inputs: "+it.raw)
			}
		}
		res.Fabricated = calibrate(cf, rf)
	}
	var failed []string
	if !atLeast(f1, extractF1) {
		failed = append(failed, "f1")
	}
	if len(res.Fabricated) > 0 {
		failed = append(failed, "fabrication")
	}
	return verdict(res, failed)
}

// literal reports whether at least 80% of a reference's plain-text items
// occur verbatim in the workspace. Structured items (objects, "key: value")
// aren't looked up.
func literal(items []item, ws Workspace) bool {
	checkable, found := 0, 0
	for _, it := range items {
		if it.checkable {
			checkable++
			if ws.Contains(it.raw) {
				found++
			}
		}
	}
	return checkable == 0 || float64(found) >= 0.8*float64(checkable)
}

// extractItems parses a list answer into items, once per distinct key.
func extractItems(text string) []item {
	var items []item
	if v, ok := jsonContainer(text); ok {
		items = jsonItems(v)
	} else {
		items = lineItems(text)
	}
	seen := map[string]bool{}
	out := items[:0]
	for _, it := range items {
		if it.key != "" && !seen[it.key] {
			seen[it.key] = true
			out = append(out, it)
		}
	}
	return out
}

func newItem(raw string, checkable bool) item {
	return item{raw: raw, key: strings.ToLower(raw), checkable: checkable}
}

func jsonItems(v any) []item {
	var out []item
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			out = append(out, jsonItem(e))
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			it := jsonItem(x[k])
			out = append(out, newItem(normSpace(k)+": "+it.raw, false))
		}
	}
	return out
}

func jsonItem(v any) item {
	if s, ok := scalarText(v); ok {
		return newItem(cleanItem(s), true)
	}
	return newItem(canonJSON(v), false)
}

var headingLineRe = regexp.MustCompile(`^#{1,6}\s`)

func lineItems(text string) []item {
	var bullets, plain []item
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || isFenceLine(t) || headingLineRe.MatchString(t) {
			continue
		}
		switch {
		case markerRe.MatchString(t):
			bullets = append(bullets, newItem(cleanItem(t), true))
		case !strings.HasSuffix(t, ":"):
			plain = append(plain, newItem(cleanItem(t), true))
		}
	}
	if len(bullets) > 0 {
		return bullets
	}
	return plain
}
