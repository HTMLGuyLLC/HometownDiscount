package grade

import (
	"encoding/json"
	"io"
	"regexp"
	"strings"
)

func normSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

func isFenceLine(t string) bool { return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") }

// unfence returns the inside of text when the whole text is one fenced code
// block, and the trimmed text otherwise.
func unfence(text string) string {
	t := strings.TrimSpace(text)
	if !isFenceLine(t) {
		return t
	}
	lines := strings.Split(t, "\n")
	if len(lines) < 2 {
		return t
	}
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "```" && last != "~~~" {
		return t
	}
	for _, l := range lines[1 : len(lines)-1] {
		if isFenceLine(strings.TrimSpace(l)) {
			return t
		}
	}
	return strings.Join(lines[1:len(lines)-1], "\n")
}

// fencedBlocks returns the contents of every closed fenced code block.
func fencedBlocks(text string) []string {
	var out, cur []string
	in := false
	for _, line := range strings.Split(text, "\n") {
		if isFenceLine(strings.TrimSpace(line)) {
			if in {
				out = append(out, strings.Join(cur, "\n"))
				cur = nil
			}
			in = !in
			continue
		}
		if in {
			cur = append(cur, line)
		}
	}
	return out
}

// parseJSON decodes exactly one JSON value. Numbers keep their literal text.
func parseJSON(s string) (any, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	return v, true
}

// jsonContainer finds a JSON array or object in text: the whole text, or the
// first fenced block that parses.
func jsonContainer(text string) (any, bool) {
	for _, c := range append([]string{unfence(text)}, fencedBlocks(text)...) {
		c = strings.TrimSpace(c)
		if !strings.HasPrefix(c, "[") && !strings.HasPrefix(c, "{") {
			continue
		}
		if v, ok := parseJSON(c); ok {
			return v, true
		}
	}
	return nil, false
}

// scalarText renders a JSON scalar as text.
func scalarText(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case json.Number:
		return x.String(), true
	case bool:
		if x {
			return "true", true
		}
		return "false", true
	}
	return "", false
}

var (
	markerRe   = regexp.MustCompile(`^(?:[-*+•]|\d{1,3}[.)])\s+`)
	checkboxRe = regexp.MustCompile(`^\[[ xX]\]\s+`)
)

// cleanItem trims a list line down to its item: no bullet, number or checkbox,
// no wrapping backticks, bold marks or quotes, single spaces, no trailing
// punctuation. Case is kept.
func cleanItem(s string) string {
	s = strings.TrimSpace(s)
	s = markerRe.ReplaceAllString(s, "")
	s = checkboxRe.ReplaceAllString(s, "")
	s = strings.Trim(s, "`*")
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		s = s[1 : len(s)-1]
	}
	s = normSpace(s)
	return strings.TrimSpace(strings.TrimRight(s, ".,;:!?"))
}

// plainLower drops bold and code marks, collapses spaces and lowercases.
func plainLower(s string) string {
	s = strings.NewReplacer("**", "", "__", "", "`", "").Replace(s)
	return strings.ToLower(normSpace(s))
}

var backtickRe = regexp.MustCompile("`([^`\n]+)`")

// backticks returns the whitespace-normalized contents of each inline code span.
func backticks(text string) []string {
	var out []string
	for _, m := range backtickRe.FindAllStringSubmatch(text, -1) {
		if s := normSpace(m[1]); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func isWordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// containsWord reports whether word appears in text not glued to other word characters.
func containsWord(text, word string) bool {
	if word == "" {
		return false
	}
	for i := 0; i < len(text); {
		j := strings.Index(text[i:], word)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(word)
		if (start == 0 || !isWordByte(text[start-1])) && (end == len(text) || !isWordByte(text[end])) {
			return true
		}
		i = start + 1
	}
	return false
}

// stopWords are dropped before comparing two findings by their words. Severity
// words are included so two unrelated "High" findings don't look alike.
var stopWords = setOf(strings.Fields(`
a an and are as at be been but by can could did do does for from had has have he her his how i if in into is it its
may might more most must no not of on or our she should so some such than that the their them then there these they
this those to too was we were what when where which while who why will with would you your also just only very via per
within without about above after again all any because before being below between both during each few further here
once other out over own same up using use used uses though through under until yet instead already currently
issue issues finding findings bug bugs file files code line lines function method class need needs needed
severity priority critical high medium low blocker minor major info nit p0 p1 p2 p3`))

func setOf(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

var wordRe = regexp.MustCompile(`[a-z0-9]+`)

// contentWords is the set of lowercase words in s with stop words removed and
// a plural "s" trimmed.
func contentWords(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range wordRe.FindAllString(strings.ToLower(s), -1) {
		if len(w) < 2 || stopWords[w] {
			continue
		}
		if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
			w = w[:len(w)-1]
		}
		out[w] = true
	}
	return out
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for w := range a {
		if b[w] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}
