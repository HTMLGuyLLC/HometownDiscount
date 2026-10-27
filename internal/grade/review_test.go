package grade

import "testing"

func reviewWS() *fakeWS {
	return newWS(map[string]string{
		"internal/auth/login.go": fileWith(80, map[int]string{
			42: `	q := "SELECT * FROM users WHERE name = '" + name`,
			60: `	log.Printf("login failed for %s", name)`,
		}),
		"internal/auth/session.go": fileWith(40, map[int]string{
			17: "func CreateSession(userID string) *Session {",
			30: `	http.SetCookie(w, &http.Cookie{Name: "sid", Value: tok})`,
		}),
		"internal/api/routes.go": fileWith(50, map[int]string{12: `	mux.HandleFunc("/api/login", loginHandler)`}),
	})
}

// refReport is an audit report with severity headings: weights 2, 2, 1, 1.
const refReport = "# Security audit\n\n" +
	"## Critical\n\n" +
	"- **SQL injection** in `internal/auth/login.go:42`: the query is built with `\"SELECT * FROM users WHERE name = '\" + name`.\n\n" +
	"## High\n\n" +
	"- Session cookie is set without HttpOnly or Secure flags in `internal/auth/session.go:30`.\n\n" +
	"## Medium\n\n" +
	"- The `/api/login` route in `internal/api/routes.go` has no rate limit.\n" +
	"- Failed logins log the user name (`log.Printf(\"login failed for %s\", name)`), which can leak PII.\n\n" +
	"## Summary\n\n" +
	"Four issues, one critical.\n"

const (
	cand1 = "1. [CRITICAL] SQL injection at `internal/auth/login.go:44` — user input is concatenated into the query.\n"
	cand2 = "2. [HIGH] Cookie flags: `internal/auth/session.go:30` sets no HttpOnly/Secure.\n"
	cand3 = "3. No rate limiting on `/api/login` (see internal/api/routes.go).\n"
	cand4 = "4. Logging the user name on failed login leaks PII: `log.Printf(\"login failed for %s\", name)`\n"
)

func TestReview(t *testing.T) {
	const theta = 0.6
	cases := []struct {
		name      string
		ref, cand string
		ws        Workspace
		theta     float64
		want      want
	}{
		{
			name: "paraphrased report with the same anchors passes",
			ref:  refReport, cand: "## Findings\n\n" + cand1 + cand2 + cand3 + cand4,
			ws: reviewWS(), theta: theta,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "line drift of 5 still matches",
			ref:  refReport, cand: "- SQL injection at `internal/auth/login.go:47`\n" + cand2 + cand3 + cand4,
			ws: reviewWS(), theta: theta,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "line drift of 6 misses the critical finding",
			ref:  refReport, cand: "- SQL injection at `internal/auth/login.go:48`\n" + cand2 + cand3 + cand4,
			ws: reviewWS(), theta: theta,
			want: want{graded: true, reason: "high-severity-missed", recall: 4.0 / 6, extra: 1},
		},
		{
			name: "a missed medium finding is fine when recall holds",
			ref:  refReport, cand: cand1 + cand2 + cand3,
			ws: reviewWS(), theta: theta,
			want: want{pass: true, graded: true, recall: 5.0 / 6},
		},
		{
			name: "the same report fails a stricter theta",
			ref:  refReport, cand: cand1 + cand2 + cand3,
			ws: reviewWS(), theta: 0.9,
			want: want{graded: true, reason: "recall", recall: 5.0 / 6},
		},
		{
			name: "missed high finding fails even when recall is high",
			ref:  refReport, cand: cand1 + cand3 + cand4,
			ws: reviewWS(), theta: theta,
			want: want{graded: true, reason: "high-severity-missed", recall: 4.0 / 6},
		},
		{
			name: "a fabricated path fails the job",
			ref:  refReport, cand: cand1 + cand2 + cand3 + cand4 + "5. Hardcoded secret in `internal/config/secrets.go:3`.\n",
			ws: reviewWS(), theta: theta,
			want: want{graded: true, reason: "fabrication", recall: 1, extra: 1, fab: []string{"missing path: internal/config/secrets.go"}},
		},
		{
			name: "a line past the end of the file is fabricated",
			ref:  refReport, cand: cand1 + cand2 + cand3 + cand4 + "5. Odd cookie handling at internal/auth/session.go:99.\n",
			ws: reviewWS(), theta: theta,
			want: want{graded: true, reason: "fabrication", recall: 1, extra: 1, fab: []string{"line past end of file: internal/auth/session.go:99 (file has 40 lines)"}},
		},
		{
			name: "a quote found in no file is fabricated",
			ref:  refReport,
			cand: cand1 + cand2 + cand3 + "4. Failed logins log the user name: `log.Printf(\"user %s failed\", name)`\n",
			ws:   reviewWS(), theta: theta,
			want: want{graded: true, reason: "fabrication", recall: 1, fab: []string{`quote not found: log.Printf("user %s failed", name)`}},
		},
		{
			name: "a double-quoted phrase found in no file is fabricated",
			ref:  refReport,
			cand: cand1 + cand2 + cand3 + cand4 + `5. The comment says "this is a temporary hack" near internal/auth/login.go:60.` + "\n",
			ws:   reviewWS(), theta: theta,
			want: want{graded: true, reason: "fabrication", recall: 1, extra: 1, fab: []string{"quote not found: this is a temporary hack"}},
		},
		{
			name: "without a workspace nothing is fabricated",
			ref:  refReport, cand: cand1 + cand2 + cand3 + cand4 + "5. Hardcoded secret in `internal/config/secrets.go:3`.\n",
			ws: nil, theta: theta,
			want: want{pass: true, graded: true, recall: 1, extra: 1},
		},
		{
			name: "a quote in the candidate that is in the workspace is fine",
			ref:  refReport, cand: cand1 + cand2 + cand3 + cand4,
			ws: reviewWS(), theta: theta,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "a candidate finding goes to one reference finding only",
			ref:  "- Query built by concatenation in `internal/auth/login.go`\n- Error message leaks the user name in `internal/auth/login.go`\n",
			cand: "- Something is wrong in internal/auth/login.go\n",
			ws:   reviewWS(), theta: theta,
			want: want{graded: true, reason: "recall", recall: 0.5},
		},
		{
			name: "two candidate findings may match one reference finding",
			ref:  "- Query built by concatenation in `internal/auth/login.go`\n",
			cand: "- Concatenation in internal/auth/login.go\n- Also in internal/auth/login.go\n",
			ws:   reviewWS(), theta: theta,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "a spare candidate finding is not wasted on an already matched reference finding",
			ref:  "- A in `internal/auth/login.go`\n- B in `internal/auth/login.go`\n",
			cand: "- one in internal/auth/login.go\n- two in internal/auth/login.go\n",
			ws:   reviewWS(), theta: 1,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "an identifier alone matches",
			ref:  "- `getUserById` skips the tenant check\n",
			cand: "- The tenant check is missing in getUserById.\n",
			ws:   reviewWS(), theta: theta,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "a quote alone matches across different wording",
			ref:  "- The query `\"SELECT * FROM users WHERE name = '\" + name` is injectable\n",
			cand: "- Building SQL by hand (`\"SELECT * FROM users WHERE name = '\" + name`) is unsafe\n",
			ws:   reviewWS(), theta: theta,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "table reference with a severity column, bullet candidate",
			ref: "| # | Severity | Location | Issue |\n|---|---|---|---|\n" +
				"| 1 | High | internal/auth/login.go:42 | SQL injection |\n" +
				"| 2 | Low | internal/api/routes.go:12 | Route naming |\n",
			cand: "- SQL injection at internal/auth/login.go:42\n",
			ws:   reviewWS(), theta: 0.6,
			want: want{pass: true, graded: true, recall: 2.0 / 3},
		},
		{
			name: "the same table fails a higher theta",
			ref: "| # | Severity | Location | Issue |\n|---|---|---|---|\n" +
				"| 1 | High | internal/auth/login.go:42 | SQL injection |\n" +
				"| 2 | Low | internal/api/routes.go:12 | Route naming |\n",
			cand: "- SQL injection at internal/auth/login.go:42\n",
			ws:   reviewWS(), theta: 0.7,
			want: want{graded: true, reason: "recall", recall: 2.0 / 3},
		},
		{
			name: "half of the reference anchored is graded, with Jaccard for the rest",
			ref: "- Race in `internal/auth/session.go:17` when sessions are created concurrently\n" +
				"- Missing rate limit in `internal/api/routes.go`\n" +
				"- Passwords are stored without hashing\n" +
				"- Error messages reveal whether a user exists\n",
			cand: "- Concurrent session creation races: internal/auth/session.go:17\n" +
				"- Add rate limiting; see internal/api/routes.go\n" +
				"- Passwords are stored in plain text without hashing\n" +
				"- Error messages reveal if a user exists\n",
			ws: reviewWS(), theta: theta,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "no-anchor list is not graded, but the match is still computed",
			ref:  "- Passwords are stored without hashing\n- The login form has no rate limit\n- Error messages reveal whether a user exists\n",
			cand: "- Passwords are stored in plain text without hashing\n- No rate limit on login form\n- Error messages reveal if a user exists\n",
			ws:   reviewWS(), theta: theta,
			want: want{pass: true, graded: false, reason: "low-anchor", recall: 1},
		},
		{
			name: "no-anchor list with unrelated candidate findings",
			ref:  "- Passwords are stored without hashing\n- The login form has no rate limit\n",
			cand: "- Variable names are unclear\n- Comments are out of date\n- Tests are slow\n",
			ws:   reviewWS(), theta: theta,
			want: want{pass: false, graded: false, reason: "low-anchor", recall: 0, extra: 3},
		},
		{
			name: "an anchored candidate finding does not match an anchored reference finding by words alone",
			ref:  "- Passwords are stored without hashing in `internal/auth/login.go:42`\n",
			cand: "- Passwords are stored without hashing in `internal/api/routes.go:12`\n",
			ws:   reviewWS(), theta: theta,
			want: want{graded: true, reason: "recall", recall: 0, extra: 1},
		},
		{
			name: "an anchorless candidate finding can match an anchored reference finding by words",
			ref:  "- Passwords are stored without hashing in `internal/auth/login.go:42`\n",
			cand: "- Passwords are stored without any hashing\n",
			ws:   reviewWS(), theta: theta,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "empty reference and empty candidate pass",
			ref:  "No issues found.", cand: "**No issues found**",
			ws: reviewWS(), theta: theta,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "empty reference and blank candidate pass",
			ref:  "No issues found.", cand: "",
			ws: reviewWS(), theta: theta,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "empty reference, candidate has a finding",
			ref:  "No issues found.", cand: "- SQL injection at internal/auth/login.go:42\n",
			ws: reviewWS(), theta: theta,
			want: want{graded: true, reason: "reference-empty", recall: 1, extra: 1},
		},
		{
			name: "reference with findings, candidate says no issues",
			ref:  refReport, cand: "No issues found.",
			ws: reviewWS(), theta: theta,
			want: want{graded: true, reason: "recall, high-severity-missed", recall: 0},
		},
		{
			name: "reference with findings, blank candidate",
			ref:  refReport, cand: "",
			ws: reviewWS(), theta: theta,
			want: want{graded: true, reason: "recall, high-severity-missed", recall: 0},
		},
		{
			name: "candidate prose without a list can't be understood",
			ref:  refReport, cand: "The login code has an injection problem and the cookies are not protected well enough.",
			ws: reviewWS(), theta: theta,
			want: want{graded: true, reason: "candidate-unparsed"},
		},
		{
			name: "reference prose without a list can't be graded",
			ref:  "The login code has an injection problem and the cookies are not protected well enough.",
			cand: cand1,
			ws:   reviewWS(), theta: theta,
			want: want{reason: "reference-unparsed"},
		},
		{
			name: "a missing path the reference also cites isn't held against the candidate",
			ref:  "- Missing check in `internal/gone/old.go:7`\n",
			cand: "- Missing check in internal/gone/old.go:7\n",
			ws:   reviewWS(), theta: theta,
			want: want{pass: true, graded: true, recall: 1},
		},
		{
			name: "a different missing path still is, whatever the reference cites",
			ref:  "- Missing check in `internal/gone/old.go:7`\n",
			cand: "- Missing check in internal/gone/old.go:7\n- Also in `internal/made/up.go`\n",
			ws:   reviewWS(), theta: theta,
			want: want{graded: true, reason: "fabrication", recall: 1, extra: 1, fab: []string{"missing path: internal/made/up.go"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			check(t, Review(c.ref, c.cand, c.ws, c.theta), c.want)
		})
	}
}

func TestReviewExtraCountsUnmatchedCandidateFindings(t *testing.T) {
	ref := "- SQL injection at internal/auth/login.go:42\n"
	cand := "- SQL injection at internal/auth/login.go:42\n- Unused import in internal/api/routes.go\n- Typo in internal/auth/session.go\n"
	res := Review(ref, cand, reviewWS(), 0.6)
	check(t, res, want{pass: true, graded: true, recall: 1, extra: 2})
}

// Quote checks are calibrated per job: when the original answer writes
// suggested code in backticks (not in any file), the candidate may too; when
// every quote in the original is exact, a candidate's made-up quote fails.
func TestReviewQuoteCheckIsCalibrated(t *testing.T) {
	ws := reviewWS()
	cand := "- **High**: SQL injection at internal/auth/login.go:42, use `db.Query(sql, name)` instead\n"
	loose := "- **High**: SQL injection in internal/auth/login.go:42; fix with `db.QueryContext(ctx, q, args...)`\n"
	if r := Review(loose, cand, ws, 0.5); !r.Pass || len(r.Fabricated) != 0 {
		t.Errorf("reference writes code that isn't in the files, so the candidate may: %+v", r)
	}
	exact := "- **High**: SQL injection: `q := \"SELECT * FROM users WHERE name = '\" + name` at internal/auth/login.go:42\n"
	if r := Review(exact, cand, ws, 0.5); r.Pass || len(r.Fabricated) != 1 {
		t.Errorf("reference quotes only real code, so a made-up quote counts: %+v", r)
	}
	// The original answer always passes its own grade.
	for _, ref := range []string{loose, exact} {
		if r := Review(ref, ref, ws, 0.5); !r.Pass {
			t.Errorf("reference fails itself: %+v", r)
		}
	}
}

// A persona's words in quotes are in no file, so they don't anchor a
// finding: a review made of them can't be matched mechanically.
func TestReviewInventedQuotesDontAnchor(t *testing.T) {
	ws := reviewWS()
	ref := "- **Ana:** \"A shortcut helps on busy weeknights when I cook for four.\"\n- **Ben:** \"A big upload button makes me nervous about my photos.\"\n"
	cand := "- **Ana:** the shortcut helps on busy weeknights.\n- **Ben:** a big upload button feels risky for photos.\n"
	if r := Review(ref, cand, ws, 0.5); r.Graded || r.Reason != "low-anchor" {
		t.Errorf("got %+v, want not graded (low-anchor)", r)
	}
	if r := Review(ref, "I liked it overall.", ws, 0.5); r.Graded || r.Reason != "low-anchor" {
		t.Errorf("an unparsed candidate on an ungradable job: got %+v, want not graded (low-anchor)", r)
	}
}
