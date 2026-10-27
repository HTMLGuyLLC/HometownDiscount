package grade

import (
	"encoding/json"
	"strings"
)

// Transform grades a "transform" answer. If the reference parses as JSON, the
// candidate must parse too and the two must be equal as canonical JSON (object
// keys sorted, no insignificant whitespace; numbers compare by their literal
// text). Otherwise the two are compared as text after normalizing whitespace
// and line endings. A match is a pass; there is no partial credit.
//
// Judgment calls:
//   - An answer that is one fenced block is read as the block's contents.
//   - Text normalization turns CRLF and CR into LF, drops trailing spaces, keeps
//     each line's leading indentation (tabs count as 4 spaces) because it can
//     carry meaning, collapses other runs of spaces, collapses runs of blank
//     lines, and trims blank lines at both ends.
//   - A reference that parses is the reference-free check: the candidate must
//     parse ("candidate-unparsed").
func Transform(ref, cand string) Result {
	r, c := unfence(ref), unfence(cand)
	rv, rJSON := parseJSON(r)
	if rJSON {
		cv, cJSON := parseJSON(c)
		switch {
		case !cJSON:
			return verdict(Result{}, []string{"candidate-unparsed"})
		case canonJSON(rv) != canonJSON(cv):
			return verdict(Result{}, []string{"mismatch"})
		}
		return verdict(Result{Recall: 1}, nil)
	}
	if normText(r) != normText(c) {
		return verdict(Result{}, []string{"mismatch"})
	}
	return verdict(Result{Recall: 1}, nil)
}

// canonJSON is the compact JSON of v with object keys sorted.
func canonJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// normText normalizes line endings and spaces. See Transform.
func normText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var out []string
	blank := true // drops leading blank lines, and collapses runs
	for _, line := range strings.Split(s, "\n") {
		body := strings.TrimLeft(line, " \t")
		indent := strings.ReplaceAll(line[:len(line)-len(body)], "\t", "    ")
		body = normSpace(body)
		if body == "" {
			if !blank {
				out = append(out, "")
			}
			blank = true
			continue
		}
		out = append(out, indent+body)
		blank = false
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}
