package grade

import "testing"

func TestTransform(t *testing.T) {
	cases := []struct {
		name      string
		ref, cand string
		want      want
	}{
		{
			name: "json: key order and whitespace don't matter",
			ref:  `{"name":"a","tags":["x","y"],"n":{"b":2,"a":1}}`,
			cand: "{\n  \"n\": {\"a\": 1, \"b\": 2},\n  \"tags\": [\"x\", \"y\"],\n  \"name\": \"a\"\n}\n",
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "json in a fenced block",
			ref:  `{"a":1}`,
			cand: "```json\n{\"a\": 1}\n```",
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "json: array order matters",
			ref:  `["x","y"]`,
			cand: `["y","x"]`,
			want: want{graded: true, reason: "mismatch"},
		},
		{
			name: "json: a different value fails",
			ref:  `{"a":1}`,
			cand: `{"a":2}`,
			want: want{graded: true, reason: "mismatch"},
		},
		{
			name: "json: numbers compare by their text",
			ref:  `{"a":1}`,
			cand: `{"a":1.0}`,
			want: want{graded: true, reason: "mismatch"},
		},
		{
			name: "json reference, candidate does not parse",
			ref:  `{"a":1}`,
			cand: "Sure! {\"a\": 1}",
			want: want{graded: true, reason: "candidate-unparsed"},
		},
		{
			name: "json reference, truncated candidate does not parse",
			ref:  `{"a":1,"b":2}`,
			cand: `{"a":1,"b":`,
			want: want{graded: true, reason: "candidate-unparsed"},
		},
		{
			name: "text: line endings, trailing spaces and blank edges don't matter",
			ref:  "line one\nline two\n",
			cand: "\r\nline one  \r\nline two\t\r\n\r\n",
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "text: runs of spaces and blank lines collapse",
			ref:  "a b\n\nc",
			cand: "a    b\n\n\n\nc",
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "text: indentation is kept",
			ref:  "items:\n  - a\n  - b",
			cand: "items:\n- a\n- b",
			want: want{graded: true, reason: "mismatch"},
		},
		{
			name: "text: tab indentation equals four spaces",
			ref:  "if x:\n    y",
			cand: "if x:\n\ty",
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "text: fenced block is read as its contents",
			ref:  "key: value\nother: 2",
			cand: "```yaml\nkey: value\nother: 2\n```",
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "text: a different word fails",
			ref:  "Hello, world",
			cand: "Hello, World",
			want: want{graded: true, reason: "mismatch"},
		},
		{
			name: "text reference, json candidate fails",
			ref:  "name = a",
			cand: `{"name":"a"}`,
			want: want{graded: true, reason: "mismatch"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			check(t, Transform(c.ref, c.cand), c.want)
		})
	}
}
