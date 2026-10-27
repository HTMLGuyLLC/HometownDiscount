package grade

import (
	"regexp"
	"strings"
)

// Finding is one item of a review: a bullet, a numbered item or a table row,
// with its sub-bullets folded in.
type Finding struct {
	Text     string // the finding's lines joined by newlines, bullet mark removed
	Anchors  []Anchor
	Severity int // 2 for critical/high/blocker/P0/P1, else 1
}

const (
	highWords = `critical|high|blocker|p0|p1`
	lowWords  = `medium|moderate|low|minor|info|informational|nit|nitpick|warning|p2|p3|p4`
	sevNoun   = `(?:severity|priority|risk)`
)

// sevTags matches a severity tag in a finding. Plain English ("a critical
// section", "high traffic") is not a tag: it must be bracketed, bold, at the
// start of the finding with a separator, labelled "severity:", or in a table cell.
type sevTags struct {
	anywhere []*regexp.Regexp // run on the lowercased text
	start    *regexp.Regexp   // run on the first line without markup
}

func newSevTags(words string) sevTags {
	return sevTags{
		anywhere: []*regexp.Regexp{
			// [HIGH]  (P1)  [severity: critical]
			regexp.MustCompile(`[\[(<]\s*(?:` + sevNoun + `\s*[:=-]?\s*)?(?:` + words + `)\s*` + sevNoun + `?\s*[\])>]`),
			// **High**  **Critical:**  **P1 - title**
			regexp.MustCompile(`(?:\*\*|__)\s*(?:` + sevNoun + `\s*[:=-]?\s*)?(?:` + words + `)\s*` + sevNoun + `?\s*(?::|\*\*|__|\s[-–—]\s)`),
			// Severity: High
			regexp.MustCompile(`\b` + sevNoun + `\s*[:=]?\W{0,4}(?:` + words + `)\b`),
			// | High |
			regexp.MustCompile(`(?:^|\|)\s*\W*(?:` + words + `)\W*\s*(?:\||$)`),
		},
		// High: ...  P0 - ...  Critical
		start: regexp.MustCompile(`^(?:` + words + `)(?:\s+(?:` + sevNoun + `|issues?|findings?))?(?:\s*\(\d+\))?\s*(?::|\s[-–—]\s|\||\)|$)`),
	}
}

var (
	highTags = newSevTags(highWords)
	lowTags  = newSevTags(lowWords)

	ruleRe        = regexp.MustCompile(`^(?:[-*_]\s*){3,}$`)
	headingRe     = regexp.MustCompile(`^#{1,6}\s+(.*?)\s*#*$`)
	boldLineRe    = regexp.MustCompile(`^(?:\*\*|__)[^*_]+(?:\*\*|__)\s*:?$`)
	boldLeadRe    = regexp.MustCompile(`^(?:\*\*|__)[^*_]{1,80}(?:\*\*|__)[:.]?\s+\S`)
	groupLabelRe  = regexp.MustCompile(`^(?:critical|high|medium|moderate|low|minor|info|informational|blocker|p[0-4])(?:\s+(?:severity|priority|risk|issues?|findings?))?:?$`)
	summaryLineRe = regexp.MustCompile(`^(?:summary|overall|in summary|in conclusion|conclusion|verdict|tl;dr)\b`)
	skipSectionRe = regexp.MustCompile(`^(?:summary|overview|conclusions?|scope|methodology|approach|notes?|next steps|verdict|positives?|strengths|what(?:'s| is| went| works) (?:good|well)|files (?:reviewed|examined|checked)|checked|reviewed)\b`)

	// noIssuesRe matches a whole line that says there is nothing to report.
	noIssuesRe = regexp.MustCompile(`^(?:(?:summary|result|verdict|findings?|conclusion|status|review)\s*[:\-–—]\s*)?(?:` +
		`(?:no|zero|0|none|nothing)\s+(?:[a-z\-]+\s+){0,3}?(?:issues?|problems?|findings?|bugs?|vulnerabilit(?:y|ies)|concerns?|violations?|defects?|regressions?|errors?|warnings?|flaws?)` +
		`(?:\s+(?:were\s+|was\s+|have\s+been\s+|has\s+been\s+|are\s+|is\s+)?(?:found|identified|detected|discovered|observed|noted|present|remaining|to\s+report|to\s+flag|reported|raised|here))?` +
		`(?:\s+(?:in|with|during|across|within|after|on|for)\s+[^,;]{0,60})?` +
		`|none(?:\s+found)?` +
		`|nothing\s+(?:to\s+report|found|wrong|of\s+note|notable|to\s+flag)` +
		`|lgtm|looks\s+(?:good|fine|clean)|all\s+clear` +
		`|(?:the\s+)?(?:code|changes?|implementation)\s+(?:looks|is|are)\s+(?:good|fine|clean|correct)` +
		`)[\s.!]*$`)
	// noIssuesAnywhereRe catches the same claim inside a sentence.
	noIssuesAnywhereRe = regexp.MustCompile(`\b(?:found|identified|detected|noticed|spotted|saw|see|are|were|is|was|has|have)\s+(?:no|zero|0)\s+(?:[a-z\-]+\s+){0,3}?(?:issues?|problems?|findings?|bugs?|vulnerabilit(?:y|ies)|concerns?|defects?|regressions?)\b`)
)

// maxEmptyClaim is the longest answer still read as "no issues found".
const maxEmptyClaim = 600

func (t sevTags) match(text string) bool {
	low := strings.ToLower(text)
	low = strings.NewReplacer("high-level", "highlevel", "high level", "highlevel").Replace(low)
	for _, re := range t.anywhere {
		if re.MatchString(low) {
			return true
		}
	}
	first, _, _ := strings.Cut(strings.TrimSpace(low), "\n")
	return t.start.MatchString(plainLower(first))
}

// tagSeverity reads a severity tag: 2 for a high tag, 1 for a lower tag, 0 for none.
func tagSeverity(text string) int {
	switch {
	case highTags.match(text):
		return 2
	case lowTags.match(text):
		return 1
	}
	return 0
}

// draft is a finding being collected.
type draft struct {
	indent  int
	lines   []string
	raw     string // what to read the severity tag from (a table row keeps its pipes)
	section int    // severity of the section the finding sits in
}

func (d *draft) finding() Finding {
	text := strings.Join(d.lines, "\n")
	src := text
	if d.raw != "" {
		src = d.raw
	}
	sev := tagSeverity(src)
	if sev == 0 {
		sev = d.section
	}
	return Finding{Text: text, Severity: sev}
}

func leadingSpaces(s string) int {
	n := 0
	for _, r := range s {
		if r != ' ' {
			break
		}
		n++
	}
	return n
}

// SplitFindings splits a review or audit into findings.
//
// Bullets, numbered items and table rows are findings. A more indented bullet
// belongs to the finding above it, and so do indented lines, lines that follow
// without a blank line, and fenced code. Headings, preamble, summary lines and
// "no issues found" statements are not findings. Findings come with their
// anchors and severity.
//
// Judgment calls:
//   - A finding's severity is its own tag if it has one (a lower tag counts),
//     otherwise the tag of the heading above it (so "## Critical" followed by
//     bullets works), otherwise 1.
//   - Bullets under a "Summary", "Overview", "Scope", "Notes" or similar heading
//     are not findings. "Recommendations" is not skipped.
//   - A bullet that is only a severity label ("- **High**") works like a heading.
//   - A table's header row (the row above "---") is not a finding.
//   - A paragraph that opens with a bold label and goes on ("**Ben:** a receipt
//     photo is...") is a finding, like a bullet.
func SplitFindings(text string) []Finding {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var (
		drafts     []*draft
		cur        *draft
		sectionSev = 1
		skip       bool
		inFence    bool
		prevBlank  bool
	)
	flush := func() {
		if cur != nil {
			drafts = append(drafts, cur)
			cur = nil
		}
	}
	section := func(title string) {
		flush()
		plain := plainLower(title)
		sectionSev, skip = 1, skipSectionRe.MatchString(plain)
		if s := tagSeverity(title); s != 0 {
			sectionSev = s
		}
	}
	for i, line := range lines {
		line = strings.ReplaceAll(line, "\t", "    ")
		t := strings.TrimSpace(line)
		if isFenceLine(t) {
			inFence = !inFence
			continue
		}
		if inFence {
			if cur != nil {
				cur.lines = append(cur.lines, t)
			}
			continue
		}
		if t == "" {
			prevBlank = true
			continue
		}
		wasBlank := prevBlank
		prevBlank = false
		indent := leadingSpaces(line)

		if m := headingRe.FindStringSubmatch(t); m != nil {
			section(m[1])
			continue
		}
		if boldLineRe.MatchString(t) {
			section(t)
			continue
		}
		if ruleRe.MatchString(t) {
			flush()
			continue
		}
		if strings.HasPrefix(t, "|") {
			flush()
			cells := splitCells(t)
			if isSeparatorRow(cells) || (i+1 < len(lines) && isSeparatorRow(splitCells(strings.TrimSpace(lines[i+1])))) || skip {
				continue
			}
			var kept []string
			for _, c := range cells {
				if c != "" {
					kept = append(kept, c)
				}
			}
			drafts = append(drafts, &draft{lines: []string{strings.Join(kept, " | ")}, raw: t, section: sectionSev})
			continue
		}
		if m := markerRe.FindString(t); m != "" {
			body := checkboxRe.ReplaceAllString(strings.TrimSpace(t[len(m):]), "")
			plain := plainLower(body)
			switch {
			case groupLabelRe.MatchString(plain):
				section(body)
			case skip || summaryLineRe.MatchString(plain) || !strings.ContainsAny(body, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"):
				// not a finding
			case cur != nil && indent > cur.indent:
				cur.lines = append(cur.lines, body)
			default:
				flush()
				cur = &draft{indent: indent, lines: []string{body}, section: sectionSev}
			}
			continue
		}
		// Plain text: a continuation of the finding above, a lead-in or summary
		// line, or a paragraph of preamble.
		switch {
		case cur != nil && indent > cur.indent:
			cur.lines = append(cur.lines, t)
		case (strings.HasSuffix(t, ":") && len(strings.Fields(t)) <= 8) || summaryLineRe.MatchString(plainLower(t)):
			section(t)
		case boldLeadRe.MatchString(t) && !skip:
			// "**Ben (privacy):** A receipt photo is..." starts a finding, like a bullet.
			flush()
			cur = &draft{indent: indent, lines: []string{t}, section: sectionSev}
		case cur != nil && !wasBlank:
			cur.lines = append(cur.lines, t)
		default:
			flush()
		}
	}
	flush()

	var out []Finding
	for _, d := range drafts {
		f := d.finding()
		first, _, _ := strings.Cut(f.Text, "\n")
		if noIssuesRe.MatchString(plainLower(first)) {
			continue
		}
		f.Anchors = anchorsOf(f.Text)
		out = append(out, f)
	}
	return out
}

// parseFindings splits text into findings and says whether the text was
// understood. Text with no findings is understood only when it is blank or
// says there is nothing to report; any other prose is not a list we can grade.
func parseFindings(text string) ([]Finding, bool) {
	if fs := SplitFindings(text); len(fs) > 0 {
		return fs, true
	}
	return nil, strings.TrimSpace(text) == "" || saysNoIssues(text)
}

// saysNoIssues reports whether a short answer with no findings says there are none.
func saysNoIssues(text string) bool {
	var lines []string
	in := false
	for _, l := range strings.Split(text, "\n") {
		t := strings.TrimSpace(l)
		if isFenceLine(t) {
			in = !in
			continue
		}
		if in || t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		t = checkboxRe.ReplaceAllString(markerRe.ReplaceAllString(t, ""), "")
		lines = append(lines, plainLower(t))
	}
	joined := strings.Join(lines, " ")
	if len(joined) > maxEmptyClaim {
		return false
	}
	for _, l := range lines {
		if noIssuesRe.MatchString(l) {
			return true
		}
	}
	return noIssuesAnywhereRe.MatchString(joined)
}
