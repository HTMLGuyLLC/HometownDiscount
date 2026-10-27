// Package recording serves a research job's recorded web results back to a
// replay, and answers the grader's questions about them.
package recording

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/goldset"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/grade"
)

// Recording is what one research job saw on the web.
type Recording struct {
	pages    map[string]string // normalized URL -> what WebFetch returned
	searches []search
	urls     map[string]bool // every URL fetched or listed in search results
	hosts    map[string]bool // the sites those URLs are on
	text     string          // all recorded text, for fact checks
}

type search struct {
	words  map[string]bool
	result string
}

var urlPattern = regexp.MustCompile(`https?://[^\s)\]>"'` + "`" + `]+`)

// New builds a recording from a gold-set item's web calls.
func New(calls []goldset.WebCall) *Recording {
	r := &Recording{pages: map[string]string{}, urls: map[string]bool{}, hosts: map[string]bool{}}
	var all strings.Builder
	for _, c := range calls {
		all.WriteString(c.Result)
		all.WriteByte('\n')
		switch c.Tool {
		case "WebFetch":
			u := grade.NormalizeURL(c.URL)
			r.pages[u] += c.Result
			r.add(u)
		case "WebSearch":
			r.searches = append(r.searches, search{words: words(c.Query), result: c.Result})
		}
		for _, u := range urlPattern.FindAllString(c.Result, -1) {
			r.add(grade.NormalizeURL(strings.TrimRight(u, ".,;:")))
		}
	}
	r.text = all.String()
	return r
}

// Fetch returns the recorded page for a URL.
func (r *Recording) Fetch(url, _ string) (string, bool) {
	p, ok := r.pages[grade.NormalizeURL(url)]
	return p, ok
}

// Search returns the results of the closest recorded query, if any query
// shares at least 30% of its content words.
func (r *Recording) Search(query string) (string, bool) {
	q := words(query)
	best, bestScore := "", 0.0
	for _, s := range r.searches {
		if score := overlap(q, s.words); score > bestScore {
			best, bestScore = s.result, score
		}
	}
	return best, bestScore >= 0.3
}

func (r *Recording) add(normalizedURL string) {
	r.urls[normalizedURL] = true
	if u, err := url.Parse(normalizedURL); err == nil && u.Host != "" {
		r.hosts[u.Host] = true
	}
}

// HasURL reports whether a normalized URL was fetched or listed in results.
// A bare homepage counts when any URL on that site does: citing the site a
// job read isn't making a source up.
func (r *Recording) HasURL(normalizedURL string) bool {
	if r.urls[normalizedURL] {
		return true
	}
	u, err := url.Parse(normalizedURL)
	return err == nil && u.Host != "" && u.Path == "" && u.RawQuery == "" && r.hosts[u.Host]
}

// PagesContain reports whether text appears anywhere in the recording.
func (r *Recording) PagesContain(text string) bool {
	return text != "" && strings.Contains(strings.ToLower(r.text), strings.ToLower(text))
}

// Complete reports whether every call returned something; an incomplete
// recording can't be replayed fairly.
func Complete(calls []goldset.WebCall) bool {
	for _, c := range calls {
		if strings.TrimSpace(c.Result) == "" {
			return false
		}
	}
	return len(calls) > 0
}

var wordPattern = regexp.MustCompile(`[a-z0-9]+`)

var stop = map[string]bool{"the": true, "a": true, "an": true, "of": true, "for": true, "and": true, "or": true, "in": true, "on": true, "to": true, "how": true, "what": true, "is": true, "are": true, "with": true, "vs": true, "best": true}

func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range wordPattern.FindAllString(strings.ToLower(s), -1) {
		if !stop[w] && len(w) > 1 {
			out[w] = true
		}
	}
	return out
}

// overlap is the share of a's words that appear in b.
func overlap(a, b map[string]bool) float64 {
	if len(a) == 0 {
		return 0
	}
	n := 0
	for w := range a {
		if b[w] {
			n++
		}
	}
	return float64(n) / float64(len(a))
}
