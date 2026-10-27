package grade

import (
	"strings"
	"testing"
)

// Odd input must never panic, whatever the job type.
func TestOddInputDoesNotPanic(t *testing.T) {
	inputs := []string{
		"", " ", "\n\n", "|", "||", "|---|", "| a |", "-", "- ", "•", "1.", "1. ", "#", "# ", "##### x",
		"`", "``", "```", "```\n", "~~~", "\"", "“", "“x”", "[]", "{}", "[", "{", "null", "[null]", "[[1]]", `{"a":{"b":[1,2]}}`,
		"http://", "https://", "https://[", "https://%zz.com/", "x:1", "a.go:", "a.go:0", "a.go:99999999999999999999", "a.go#L", "a.go line",
		"$", "$1,", "1,2,3", "v", "v.", "March", "March 99, 99999", "\x00\xff\xfe", "ü​•—",
		strings.Repeat("a", 5000), strings.Repeat("- x\n", 300), strings.Repeat("`", 1001),
	}
	ws := newWS(map[string]string{"a.go": "x\ny"})
	rec := newRec(map[string]string{"https://a.com/x": "page"})
	for _, a := range inputs {
		for _, b := range inputs {
			if len(a) > 1000 && len(b) > 1000 {
				continue
			}
			for _, w := range []Workspace{nil, ws} {
				Find(a, b, w)
				Extract(a, b, w)
				Review(a, b, w, 0.6)
			}
			Classify(a, b, nil)
			Classify(a, b, []string{"bug", ""})
			Transform(a, b)
			Research(a, b, nil, 0.7)
			Research(a, b, rec, 0.7)
			NormalizeURL(a)
			SplitFindings(a)
		}
	}
}
