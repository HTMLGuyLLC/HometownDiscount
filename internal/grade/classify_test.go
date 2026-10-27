package grade

import (
	"fmt"
	"strings"
	"testing"
)

// labelled builds "item-01: label" lines; labels[i] is the label of item i+1.
func labelled(labels ...string) string {
	var b strings.Builder
	for i, l := range labels {
		fmt.Fprintf(&b, "item-%02d: %s\n", i+1, l)
	}
	return b.String()
}

func repeat(label string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = label
	}
	return out
}

func TestClassify(t *testing.T) {
	allowed := []string{"bug", "feature", "question"}
	ref20 := labelled(repeat("bug", 20)...)
	// 19 of 20 agree.
	cand19 := labelled(append(repeat("bug", 19), "feature")...)
	// 18 of 20 agree.
	cand18 := labelled(append(repeat("bug", 18), "feature", "question")...)

	cases := []struct {
		name      string
		ref, cand string
		allowed   []string
		want      want
	}{
		{
			name: "colon lines, case and bullets ignored",
			ref:  "- Login crashes: Bug\n- Add dark mode: Feature\n",
			cand: "1. login crashes - bug\n2. add dark mode - feature\n",
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "arrow and equals separators",
			ref:  "a -> bug\nb = feature\n",
			cand: "a: bug\nb: feature\n",
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "item containing a colon splits at the first separator",
			ref:  "src/a.go:12: bug\n",
			cand: "src/a.go:12 - bug\n",
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "table rows, header and separator ignored",
			ref:  "| Item | Label |\n| --- | --- |\n| login | bug |\n| dark mode | feature |\n",
			cand: "login: bug\ndark mode: feature\n",
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "json map",
			ref:  `{"login": "bug", "dark mode": "feature"}`,
			cand: "login: bug\ndark mode: feature\n",
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "json array of objects",
			ref:  `[{"id": "login", "label": "bug"}, {"id": "dark mode", "category": "feature"}]`,
			cand: `{"login": "bug", "dark mode": "feature"}`,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "json in a fenced block",
			ref:  "login: bug\ndark mode: feature\n",
			cand: "```json\n{\"login\": \"bug\", \"dark mode\": \"feature\"}\n```",
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name:    "19 of 20 agree: 0.95 passes",
			ref:     ref20,
			cand:    cand19,
			allowed: allowed,
			want:    want{pass: true, graded: true, recall: 0.95},
		},
		{
			name:    "18 of 20 agree: 0.9 fails",
			ref:     ref20,
			cand:    cand18,
			allowed: allowed,
			want:    want{graded: true, reason: "agreement", recall: 0.9},
		},
		{
			name:    "a label outside the allowed set fails even with full agreement",
			ref:     "a: bug\nb: feature\n",
			cand:    "a: bug\nb: feature\nc: chore\n",
			allowed: allowed,
			want:    want{graded: true, reason: "label", recall: 1, extra: 1, fab: []string{"label not allowed: c -> chore"}},
		},
		{
			name: "without an allowed list the reference's labels are the set",
			ref:  "a: bug\nb: feature\n",
			cand: "a: bug\nb: feature\nc: chore\n",
			want: want{graded: true, reason: "label", recall: 1, extra: 1, fab: []string{"c -> chore"}},
		},
		{
			name:    "an explanation after a known label is cut",
			ref:     "a: bug\nb: feature\n",
			cand:    "a: bug (it crashes)\nb: feature - nice to have\n",
			allowed: allowed,
			want:    want{pass: true, graded: true, recall: 1},
		},
		{
			name: "a missing item is a disagreement",
			ref:  "a: bug\nb: feature\n",
			cand: "a: bug\n",
			want: want{graded: true, reason: "agreement", recall: 0.5},
		},
		{
			name: "extra items are counted",
			ref:  "a: bug\n",
			cand: "a: bug\nb: bug\n",
			want: want{pass: true, graded: true, recall: 1, extra: 1},
		},
		{
			name: "reference with no readable items can't be graded",
			ref:  "I could not decide.",
			cand: "a: bug",
			want: want{reason: "no-items"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			check(t, Classify(c.ref, c.cand, c.allowed), c.want)
		})
	}
}
