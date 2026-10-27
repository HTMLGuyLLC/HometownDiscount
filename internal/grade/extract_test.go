package grade

import (
	"fmt"
	"strings"
	"testing"
)

// envList builds "- VAR_01\n- VAR_02..." for n variables, starting at from.
func envList(from, n int) string {
	var b strings.Builder
	for i := from; i < from+n; i++ {
		fmt.Fprintf(&b, "- VAR_%02d\n", i)
	}
	return b.String()
}

func extractWS() *fakeWS {
	var src strings.Builder
	for i := 1; i <= 25; i++ {
		fmt.Fprintf(&src, "os.Getenv(\"VAR_%02d\")\n", i)
	}
	return newWS(map[string]string{"config/env.go": src.String()})
}

func TestExtract(t *testing.T) {
	ref20 := envList(1, 20)
	cases := []struct {
		name      string
		ref, cand string
		ws        Workspace
		want      want
	}{
		{
			name: "same set, different order, case and punctuation",
			ref:  "- API_KEY\n- Db_Url.\n- SECRET,\n",
			cand: "* secret\n* api_key\n* DB_URL\n",
			ws:   nil,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "bullets, numbers and backticks mean the same",
			ref:  "- `API_KEY`\n- `DB_URL`\n",
			cand: "1. API_KEY\n2) DB_URL\n",
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "preamble and closing line are not items when bullets exist",
			ref:  "Here are the variables:\n\n- API_KEY\n- DB_URL\n\nThat is all.",
			cand: "- API_KEY\n- DB_URL",
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "plain lines without bullets",
			ref:  "Variables used:\napi_key\ndb_url\n",
			cand: "db_url\napi_key",
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "json array against bullets",
			ref:  `["API_KEY", "DB_URL"]`,
			cand: "- API_KEY\n- DB_URL",
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "json in a fenced block after a preamble",
			ref:  "- API_KEY\n- DB_URL",
			cand: "Here you go:\n```json\n[\"DB_URL\", \"API_KEY\"]\n```\n",
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "json array of objects compares as canonical json",
			ref:  `[{"name":"a","port":80},{"name":"b","port":81}]`,
			cand: `[{"port":81,"name":"b"}, {"port": 80, "name": "a"}]`,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "json object gives key: value items",
			ref:  `{"API_KEY": "required", "DB_URL": "optional"}`,
			cand: "- api_key: required\n- db_url: optional",
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "one of twenty missing: F1 0.974 passes",
			ref:  ref20,
			cand: envList(1, 19),
			ws:   extractWS(),
			want: want{pass: true, graded: true, recall: 0.95},
		},
		{
			name: "two of twenty missing: F1 0.947 fails",
			ref:  ref20,
			cand: envList(1, 18),
			ws:   extractWS(),
			want: want{graded: true, reason: "f1", recall: 0.9},
		},
		{
			name: "an extra item that is in the workspace is only counted",
			ref:  ref20,
			cand: envList(1, 21),
			ws:   extractWS(),
			want: want{pass: true, graded: true, recall: 1, extra: 1},
		},
		{
			name: "an extra item that is in no file is fabricated",
			ref:  ref20,
			cand: envList(1, 20) + "- VAR_99\n",
			ws:   extractWS(),
			want: want{graded: true, reason: "fabrication", recall: 1, extra: 1, fab: []string{"VAR_99"}},
		},
		{
			name: "no workspace skips the fabrication check",
			ref:  ref20,
			cand: envList(1, 20) + "- VAR_99\n",
			ws:   nil,
			want: want{pass: true, graded: true, recall: 1, extra: 1},
		},
		{
			name: "json objects are not looked up in the workspace",
			ref:  `[{"name":"a"}]`,
			cand: `[{"name":"a"},{"name":"zzz"}]`,
			ws:   extractWS(),
			want: want{graded: true, reason: "f1", recall: 1, extra: 1},
		},
		{
			name: "two empty lists agree",
			ref:  "",
			cand: "[]",
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "empty reference with a non-empty candidate fails",
			ref:  "[]",
			cand: "- API_KEY",
			want: want{graded: true, reason: "f1", recall: 1, extra: 1},
		},
		{
			name: "empty candidate fails",
			ref:  "- API_KEY",
			cand: "I could not find any.:",
			want: want{graded: true, reason: "f1", recall: 0},
		},
		{
			name: "duplicates collapse",
			ref:  "- A\n- A\n- B",
			cand: "- b\n- a",
			want: want{pass: true, graded: true, recall: 1},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			check(t, Extract(c.ref, c.cand, c.ws), c.want)
		})
	}
}

// A summary written as a list can't be graded by exact matching: its items
// aren't in the inputs, so any rewording would "fail".
func TestExtractSummaryIsNotGraded(t *testing.T) {
	ws := newWS(map[string]string{"personas.md": "P01 Night-shift nurse\nP02 Weekend gardener\n"})
	ref := "- Nurses want quick check-ins between shifts\n- Gardeners plan by season"
	cand := "- Nurses need short check-ins\n- Gardeners think in seasons"
	if r := Extract(ref, cand, ws); r.Graded || r.Reason != "not-literal" {
		t.Errorf("got %+v, want not graded (not-literal)", r)
	}
	// A literal extraction is still graded.
	if r := Extract("- P01 Night-shift nurse\n- P02 Weekend gardener", "- P01 Night-shift nurse", ws); !r.Graded || r.Pass {
		t.Errorf("literal extract: got %+v, want a graded fail (missed an item)", r)
	}
}
