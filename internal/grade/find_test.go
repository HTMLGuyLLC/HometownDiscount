package grade

import "testing"

func findWS() *fakeWS {
	return newWS(map[string]string{
		"internal/auth/login.go": fileWith(60, map[int]string{
			18: "func hashPassword(pw string) string {",
			42: "func Login(w http.ResponseWriter, r *http.Request) {",
		}),
		"internal/auth/session.go": fileWith(30, map[int]string{17: "func CreateSession(userID string) *Session {"}),
		"internal/db/query.go":     fileWith(100, map[int]string{77: "rows, err := db.Query(q)"}),
		"cmd/server/main.go":       fileWith(20, nil),
	})
}

func TestFind(t *testing.T) {
	const threeCites = "- `Login` is in `internal/auth/login.go:42`\n" +
		"- `hashPassword` is in `internal/auth/login.go:18`\n" +
		"- `CreateSession` is in `internal/auth/session.go:17`\n"
	const fiveEntities = "`Login` and `hashPassword` live in internal/auth/login.go; `CreateSession` in internal/auth/session.go."

	cases := []struct {
		name      string
		ref, cand string
		ws        Workspace
		want      want
	}{
		{
			name: "same answer",
			ref:  threeCites, cand: threeCites, ws: findWS(),
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "lines drift within 5 and identifiers named in plain text",
			ref:  threeCites,
			cand: "Login lives at internal/auth/login.go:44, hashPassword at internal/auth/login.go:20 and CreateSession at internal/auth/session.go:17.",
			ws:   findWS(),
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "line drift of 6 is a miss",
			ref:  "`Login` is in `internal/auth/login.go:42`",
			cand: "Login is in internal/auth/login.go:48",
			ws:   findWS(),
			want: want{graded: true, reason: "recall", recall: 0.5},
		},
		{
			name: "cited line lacks the backticked identifier",
			ref:  threeCites,
			cand: "- `Login` is in `internal/auth/login.go:44`\n" +
				"- `hashPassword` is in `internal/auth/login.go:18`\n" +
				"- `CreateSession` is in `internal/auth/session.go:17`\n",
			ws:   findWS(),
			want: want{graded: true, reason: "fabrication", recall: 1, fab: []string{"cited line lacks"}},
		},
		{
			name: "dotted identifier is found by its last part",
			ref:  "`db.Query` is called in `internal/db/query.go:77`",
			cand: "`db.Query` is called in `internal/db/query.go:77`",
			ws:   findWS(),
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "cited file does not exist",
			ref:  threeCites,
			cand: "- `Login` is in `internal/auth/logon.go:42`\n" +
				"- `hashPassword` is in `internal/auth/login.go:18`\n" +
				"- `CreateSession` is in `internal/auth/session.go:17`\n",
			ws:   findWS(),
			want: want{graded: true, reason: "fabrication", recall: 5.0 / 6, extra: 1, fab: []string{"missing path: internal/auth/logon.go"}},
		},
		{
			name: "cited line is past the end of the file",
			ref:  threeCites,
			cand: "Login is at internal/auth/login.go:42, hashPassword at internal/auth/login.go:18, CreateSession at internal/auth/session.go:99.",
			ws:   findWS(),
			want: want{graded: true, reason: "fabrication", recall: 5.0 / 6, fab: []string{"line past end of file: internal/auth/session.go:99 (file has 30 lines)"}},
		},
		{
			name: "recall of exactly 0.8 passes",
			ref:  fiveEntities,
			cand: "`Login` and `CreateSession` are in internal/auth/login.go and internal/auth/session.go.",
			ws:   findWS(),
			want: want{pass: true, graded: true, recall: 0.8},
		},
		{
			name: "recall of 0.4 fails",
			ref:  fiveEntities,
			cand: "`Login` is in internal/auth/login.go.",
			ws:   findWS(),
			want: want{graded: true, reason: "recall", recall: 0.4},
		},
		{
			name: "identifier-only reference",
			ref:  "The helpers are `CreateSession()` and `hashPassword`.",
			cand: "CreateSession, hashPassword",
			ws:   findWS(),
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "leading ./ is dropped",
			ref:  "See internal/auth/login.go",
			cand: "See ./internal/auth/login.go",
			ws:   findWS(),
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "directory written with a trailing slash",
			ref:  "The auth code is in `internal/auth/`.",
			cand: "It's all under `internal/auth/`.",
			ws:   findWS(),
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "line range in the reference",
			ref:  "The query runs at internal/db/query.go:70-80",
			cand: "The query runs at internal/db/query.go:77",
			ws:   findWS(),
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "extra files are counted",
			ref:  "`Login` is in internal/auth/login.go",
			cand: "`Login` is in internal/auth/login.go, started from cmd/server/main.go",
			ws:   findWS(),
			want: want{pass: true, graded: true, recall: 1, extra: 1},
		},
		{
			name: "no workspace skips existence checks",
			ref:  "`Login` is in internal/auth/login.go:42",
			cand: "`Login` is in internal/auth/nowhere.go:42, and internal/auth/login.go:42",
			ws:   nil,
			want: want{pass: true, graded: true, recall: 1, extra: 1},
		},
		{
			name: "prose and product names are not paths",
			ref:  "Use Node.js, e.g. for example.com, in internal/auth/login.go",
			cand: "internal/auth/login.go",
			ws:   findWS(),
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "reference without entities can't be graded",
			ref:  "There is no such function in the repo.",
			cand: "Nothing like that exists.",
			ws:   findWS(),
			want: want{reason: "no-entities"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			check(t, Find(c.ref, c.cand, c.ws), c.want)
		})
	}
}
