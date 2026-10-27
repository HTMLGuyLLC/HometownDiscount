package skills

import (
	"encoding/json"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/llm"
	"regexp"
	"sort"
	"strings"
)

var (
	fence   = regexp.MustCompile("(?s)^```[a-zA-Z]*\\n(.*?)\\n?```$")
	integer = regexp.MustCompile(`\d+`)
	bullet  = regexp.MustCompile(`^\s*(?:[-*•]|\d+[.)])\s+`)
)

// Clean removes thinking blocks and a wrapping code fence from a reply.
func Clean(reply string) string {
	r := llm.StripThinking(reply)
	if m := fence.FindStringSubmatch(r); m != nil {
		r = strings.TrimSpace(m[1])
	}
	return r
}

// Grade reports whether a reply exactly answers the task. It's forgiving
// about format (bullets, backticks, order, a code fence) but not about content.
func Grade(t Task, reply string) bool {
	r := Clean(reply)
	switch t.Skill {
	case Lookup:
		n := integer.FindString(r)
		return n != "" && strings.TrimLeft(n, "0") == t.Answer
	case Lines:
		return sameSet(integer.FindAllString(r, -1), strings.Split(t.Answer, ","))
	case List, Files:
		return sameSet(items(r), strings.Split(t.Answer, "\n"))
	case JSON:
		return sameJSON(r, t.Answer)
	}
	return false
}

// items reads one item per line: bullets, backticks, trailing "()" and
// surrounding punctuation are ignored.
func items(r string) []string {
	var out []string
	for _, l := range strings.Split(r, "\n") {
		l = bullet.ReplaceAllString(l, "")
		l = strings.Trim(strings.TrimSpace(l), "`'\",;")
		l = strings.TrimSuffix(l, "()")
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func sameSet(got, want []string) bool {
	g, w := dedupe(got), dedupe(want)
	if len(g) != len(w) {
		return false
	}
	for i := range g {
		if g[i] != w[i] {
			return false
		}
	}
	return true
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		x = strings.TrimSpace(x)
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

func sameJSON(reply, want string) bool {
	var w any
	if json.Unmarshal([]byte(want), &w) != nil {
		return false
	}
	var g any
	if json.Unmarshal([]byte(reply), &g) == nil {
		gb, _ := json.Marshal(g)
		wb, _ := json.Marshal(w)
		return string(gb) == string(wb)
	}
	// An unquoted string is the right value written loosely.
	s, ok := w.(string)
	return ok && strings.Trim(reply, "'`") == s
}
