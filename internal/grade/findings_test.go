package grade

import (
	"reflect"
	"strings"
	"testing"
)

func severities(fs []Finding) []int {
	out := []int{}
	for _, f := range fs {
		out = append(out, f.Severity)
	}
	return out
}

func TestSplitFindingsAuditReport(t *testing.T) {
	report := `# Security audit: internal/auth

Reviewed the login and session code.

## Critical

- **SQL injection in login** — ` + "`internal/auth/login.go:42`" + ` builds the query by concatenation.
- Session tokens are never rotated after login (` + "`internal/auth/session.go:17`, `CreateSession`" + `).

## Medium

- Missing rate limit on the login endpoint.
  - Brute force is possible.
  - See ` + "`internal/api/routes.go`" + `.
- Password hashing uses a fixed salt.

## Summary

- 4 issues found, 2 critical.

Overall the module needs work.
`
	fs := SplitFindings(report)
	if got, want := severities(fs), []int{2, 2, 1, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("severities = %v, want %v (findings %+v)", got, want, fs)
	}
	if !strings.Contains(fs[2].Text, "Brute force is possible.") || !strings.Contains(fs[2].Text, "See `internal/api/routes.go`") {
		t.Errorf("sub-bullets not folded into their parent: %q", fs[2].Text)
	}
	if got := render(fs[0].Anchors); !reflect.DeepEqual(got, []string{"line internal/auth/login.go:42-42"}) {
		t.Errorf("anchors of finding 1 = %q", got)
	}
	if got := render(fs[1].Anchors); !reflect.DeepEqual(got, []string{"line internal/auth/session.go:17-17", "quote CreateSession", "ident CreateSession"}) {
		t.Errorf("anchors of finding 2 = %q", got)
	}
	if got := render(fs[2].Anchors); !reflect.DeepEqual(got, []string{"path internal/api/routes.go"}) {
		t.Errorf("anchors of finding 3 = %q (sub-bullet anchors belong to the parent)", got)
	}
}

func TestSplitFindingsSeverity(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []int
	}{
		{"untagged list weighs 1", "- one\n- two\n- three", []int{1, 1, 1}},
		{"bracket tag", "- [HIGH] SQL injection\n- [Medium] naming", []int{2, 1}},
		{"paren tag", "- (P1) crash on nil\n- (P3) typo", []int{2, 1}},
		{"bold tag", "- **Critical:** token leak\n- **Blocker** build broken\n- **Minor** style", []int{2, 2, 1}},
		{"start of line", "- P0 - data loss\n- High: injection\n- Low - naming", []int{2, 2, 1}},
		{"severity label", "- Severity: High — injection\n- **Severity:** Critical\n- Priority: low", []int{2, 2, 1}},
		{"plain English is not a tag", "- The critical section is held too long\n- High traffic endpoints lack caching\n- Uses a high-level API wrongly", []int{1, 1, 1}},
		{"heading sets the section severity", "## High\n- a thing\n- another thing\n## Low\n- minor thing", []int{2, 2, 1}},
		{"own lower tag beats the heading", "## High\n- a thing\n- [Low] another thing", []int{2, 1}},
		{"heading with a count", "### Critical (2)\n- a\n- b", []int{2, 2}},
		{"bold line as heading", "**Critical**\n- a\n\n**Low**\n- b", []int{2, 1}},
		{"lead-in line ending in a colon", "Critical issues:\n- a\n- b\nMinor issues:\n- c", []int{2, 2, 1}},
		{"bullet that is only a label works as a heading", "- **High**\n  - a\n  - b\n- **Low**\n  - c", []int{2, 2, 1}},
		{"heading after a group resets", "## High\n- a\n## Findings\n- b", []int{2, 1}},
		{
			"table with a severity column",
			"| # | Severity | Location | Issue |\n|---|----------|----------|-------|\n| 1 | High | a.go:3 | bad |\n| 2 | Low | b.go:4 | meh |\n| 3 | P1 | c.go:5 | worse |",
			[]int{2, 1, 2},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := SplitFindings(c.text)
			if got := severities(fs); !reflect.DeepEqual(got, c.want) {
				t.Errorf("severities = %v, want %v (findings %+v)", got, c.want, fs)
			}
		})
	}
}

func TestSplitFindingsShapes(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string // finding texts
	}{
		{"bullets", "- one\n* two\n+ three", []string{"one", "two", "three"}},
		{"numbered items", "1. First\n2) Second\n   - sub of second\n3. Third", []string{"First", "Second\nsub of second", "Third"}},
		{"preamble and closing paragraph are not findings", "Here is the review:\n\n- one\n- two\n\nOverall the code is fine.", []string{"one", "two"}},
		{"lazy continuation line", "- one\ncontinues here\n\nA paragraph after.", []string{"one\ncontinues here"}},
		{"indented continuation after a blank line", "- one\n\n  more about one\n- two", []string{"one\nmore about one", "two"}},
		{"fenced code stays in its finding", "- Bad query\n  ```go\n  - not a bullet\n  q := x\n  ```\n- Second", []string{"Bad query\n- not a bullet\nq := x", "Second"}},
		{"checkbox marks are dropped", "- [ ] one\n- [x] two", []string{"one", "two"}},
		{"horizontal rules and empty bullets are not findings", "- one\n---\n- - -\n- two", []string{"one", "two"}},
		{"summary bullets are not findings", "- one\n- Summary: 1 issue found\n- Overall: fine", []string{"one"}},
		{"bullets under a summary heading are skipped", "- one\n\n## Summary\n- 1 issue\n- all fixed\n\n## More findings\n- two", []string{"one", "two"}},
		{"bullets under a scope heading are skipped", "## Scope\n- internal/auth\n## Findings\n- one", []string{"one"}},
		{"recommendations are findings", "## Recommendations\n- add a rate limit", []string{"add a rate limit"}},
		{
			"table rows, header and separator dropped",
			"| Where | What |\n|---|---|\n| a.go:3 | bad |\n| b.go:4 | worse |",
			[]string{"a.go:3 | bad", "b.go:4 | worse"},
		},
		{"a table closes the bullet above it", "- one\n| a | b |\n|---|---|\n| 1 | 2 |", []string{"one", "1 | 2"}},
		{"windows line endings", "- one\r\n- two\r\n", []string{"one", "two"}},
		{"tab-indented sub-bullet", "- one\n\t- sub\n- two", []string{"one\nsub", "two"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := []string{}
			for _, f := range SplitFindings(c.text) {
				got = append(got, f.Text)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("finding texts = %q, want %q", got, c.want)
			}
		})
	}
}

func TestNoIssues(t *testing.T) {
	empty := []string{
		"",
		"   \n",
		"No issues found.",
		"no issues found",
		"- No issues found.",
		"**No issues found**",
		"## Review\n\nNo issues found.",
		"No critical issues were identified in the reviewed files.",
		"No security issues found in internal/auth.",
		"Zero problems detected.",
		"No findings.",
		"There are no problems.",
		"I reviewed all 12 files and found no issues.",
		"Nothing to report.",
		"LGTM",
		"Looks good.",
		"The code looks clean.",
		"All clear.",
		"## Findings\n\nNone.",
		"Summary: no issues found",
		"- No bugs found\n- No vulnerabilities found",
	}
	for _, text := range empty {
		fs, ok := parseFindings(text)
		if len(fs) != 0 || !ok {
			t.Errorf("parseFindings(%q) = %d findings, ok=%v; want an understood empty set", text, len(fs), ok)
		}
	}

	findings := []string{
		"- No error handling around the db call",
		"- No rate limit on /api/login",
		"- No tests cover the refund path",
		"- No issues found in module A, but module B is broken",
	}
	for _, text := range findings {
		fs, ok := parseFindings(text)
		if len(fs) != 1 || !ok {
			t.Errorf("parseFindings(%q) = %d findings, ok=%v; want 1 finding", text, len(fs), ok)
		}
	}

	prose := []string{
		"The auth code has several problems worth a look, mostly around session handling and rate limits.",
		"# Review\n",
		"I'm not sure what you want me to review.",
	}
	for _, text := range prose {
		fs, ok := parseFindings(text)
		if len(fs) != 0 || ok {
			t.Errorf("parseFindings(%q) = %d findings, ok=%v; want not understood", text, len(fs), ok)
		}
	}
}

// Answers written as bold-labeled paragraphs (a persona panel, a findings
// write-up) split into one finding per paragraph, like bullets.
func TestSplitFindingsBoldParagraphs(t *testing.T) {
	text := "I read all five persona files.\n\n## Per-persona reactions\n\n" +
		"**Ana (home cook):** A shortcut inside Add is fine.\n\n" +
		"**Ben (privacy skeptic):** A photo of my receipts feels sensitive.\nIt needs a privacy note.\n\n" +
		"**Risks.** Finding the shortcut on mobile is the main cost.\n"
	fs := SplitFindings(text)
	if len(fs) != 3 {
		t.Fatalf("got %d findings, want 3: %+v", len(fs), fs)
	}
	if !strings.Contains(fs[1].Text, "privacy note") {
		t.Errorf("a following line belongs to its paragraph: %q", fs[1].Text)
	}
}
