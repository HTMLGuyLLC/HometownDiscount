package grade

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Classify grades a "classify" answer: one label per item.
//
// Both answers are read into item -> label maps. An item line looks like
// "item: label", "item - label", "item -> label" or "item = label"; a table
// row is "| item | label |"; JSON is an object {"item": "label"} or an array of
// objects with an item key (item, id, name, file, path, title, text, key) and a
// label key (label, category, class, classification, type, verdict, decision,
// result). Items and labels are lowercased and tidied the same way as in Extract.
//
// It passes when the candidate agrees with the reference on at least 95% of the
// reference's items and every label it uses is allowed. Allowed labels default
// to the labels the reference uses. A label that isn't allowed is listed in
// Fabricated.
//
// Judgment calls:
//   - A line splits at its first separator, so an item may contain colons and
//     dashes only when the separator is the first one. When the labels are
//     known, text after a known label ("bug (see above)") is dropped.
//   - In a table with 3+ columns the first cell is the item and the last the label.
//     A header row (the row above the "---" row) is ignored.
//   - A reference with no readable items can't be graded ("no-items").
//   - Recall is the agreement. Extra counts the candidate's items the reference lacks.
func Classify(ref, cand string, allowed []string) Result {
	var hint []string
	for _, a := range allowed {
		hint = append(hint, tidy(a))
	}
	want := parseLabels(ref, hint)
	if len(want) == 0 {
		return ungraded("no-items")
	}
	if len(hint) == 0 {
		for _, l := range want {
			hint = append(hint, l)
		}
	}
	ok := setOf(hint)
	got := parseLabels(cand, hint)

	items := make([]string, 0, len(want))
	for k := range want {
		items = append(items, k)
	}
	sort.Strings(items)
	agree := 0
	for _, k := range items {
		if l, found := got[k]; found && l == want[k] {
			agree++
		}
	}
	res := Result{Recall: float64(agree) / float64(len(want))}
	gotItems := make([]string, 0, len(got))
	for k := range got {
		gotItems = append(gotItems, k)
	}
	sort.Strings(gotItems)
	for _, k := range gotItems {
		if _, inRef := want[k]; !inRef {
			res.Extra++
		}
		if !ok[got[k]] {
			res.Fabricated = append(res.Fabricated, fmt.Sprintf("label not allowed: %s -> %s", k, got[k]))
		}
	}
	var failed []string
	if !atLeast(res.Recall, classifyAgreement) {
		failed = append(failed, "agreement")
	}
	if len(res.Fabricated) > 0 {
		failed = append(failed, "label")
	}
	return verdict(res, failed)
}

// tidy is the comparison form of an item or a label.
func tidy(s string) string { return strings.ToLower(cleanItem(s)) }

var (
	labelSepRe = regexp.MustCompile(`^(.+?)(?:\s*(?:->|=>|→)\s*|:\s+|\s+[-–—]\s+|\s+=\s+|\t+)(.+)$`)
	tableSepRe = regexp.MustCompile(`^:?-+:?$`)

	itemKeys  = []string{"item", "id", "name", "file", "path", "title", "text", "key"}
	labelKeys = []string{"label", "category", "class", "classification", "type", "verdict", "decision", "result"}
)

// parseLabels reads text into item -> label. hint lists the labels already
// known (tidied), which lets it cut an explanation that follows a label.
func parseLabels(text string, hint []string) map[string]string {
	out := map[string]string{}
	if v, ok := jsonContainer(text); ok {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				if s, ok := scalarText(val); ok {
					out[tidy(k)] = tidy(s)
				}
			}
		case []any:
			for _, e := range x {
				obj, ok := e.(map[string]any)
				if !ok {
					continue
				}
				i, iok := pick(obj, itemKeys)
				l, lok := pick(obj, labelKeys)
				if iok && lok {
					out[tidy(i)] = tidy(l)
				}
			}
		}
		return out
	}
	byLength := append([]string(nil), hint...)
	sort.Slice(byLength, func(a, b int) bool { return len(byLength[a]) > len(byLength[b]) })

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		t := strings.TrimSpace(line)
		switch {
		case t == "" || isFenceLine(t) || headingLineRe.MatchString(t):
		case strings.HasPrefix(t, "|"):
			cells := splitCells(t)
			if isSeparatorRow(cells) || (i+1 < len(lines) && isSeparatorRow(splitCells(strings.TrimSpace(lines[i+1])))) {
				continue
			}
			if len(cells) >= 2 {
				out[tidy(cells[0])] = tidy(cells[len(cells)-1])
			}
		default:
			if m := labelSepRe.FindStringSubmatch(markerRe.ReplaceAllString(t, "")); m != nil {
				out[tidy(m[1])] = knownLabel(tidy(m[2]), byLength)
			}
		}
	}
	return out
}

// knownLabel cuts a label at the end of a known label it starts with.
func knownLabel(s string, labels []string) string {
	for _, l := range labels {
		if l != "" && strings.HasPrefix(s, l) && (len(s) == len(l) || !isWordByte(s[len(l)])) {
			return l
		}
	}
	return s
}

func pick(obj map[string]any, keys []string) (string, bool) {
	for _, want := range keys {
		for k, v := range obj {
			if strings.EqualFold(k, want) {
				if s, ok := scalarText(v); ok {
					return s, true
				}
			}
		}
	}
	return "", false
}

func splitCells(row string) []string {
	row = strings.Trim(strings.TrimSpace(row), "|")
	var cells []string
	for _, c := range strings.Split(row, "|") {
		cells = append(cells, strings.TrimSpace(c))
	}
	return cells
}

func isSeparatorRow(cells []string) bool {
	seen := false
	for _, c := range cells {
		if c == "" {
			continue
		}
		if !tableSepRe.MatchString(c) {
			return false
		}
		seen = true
	}
	return seen
}
