package grade

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// fakeWS is an in-memory Workspace: path -> lines.
type fakeWS struct {
	lines map[string][]string
	flat  map[string]string // whitespace-normalized file text, for Contains
}

func newWS(files map[string]string) *fakeWS {
	w := &fakeWS{lines: map[string][]string{}, flat: map[string]string{}}
	for p, text := range files {
		w.lines[p] = strings.Split(text, "\n")
		w.flat[p] = normSpace(text)
	}
	return w
}

func (w *fakeWS) FileExists(p string) bool {
	if _, ok := w.lines[p]; ok {
		return true
	}
	for f := range w.lines { // a directory
		if strings.HasPrefix(f, p+"/") {
			return true
		}
	}
	return false
}

func (w *fakeWS) Line(p string, n int) (string, bool) {
	ls, ok := w.lines[p]
	if !ok || n < 1 || n > len(ls) {
		return "", false
	}
	return ls[n-1], true
}

func (w *fakeWS) LineCount(p string) (int, bool) {
	ls, ok := w.lines[p]
	return len(ls), ok
}

func (w *fakeWS) Contains(text string) bool {
	text = normSpace(text)
	for _, f := range w.flat {
		if strings.Contains(f, text) {
			return true
		}
	}
	return false
}

// fileWith builds an n-line file of filler with some lines replaced (1-based).
func fileWith(n int, at map[int]string) string {
	ls := make([]string, n)
	for i := range ls {
		ls[i] = fmt.Sprintf("// filler line %d", i+1)
		if s, ok := at[i+1]; ok {
			ls[i] = s
		}
	}
	return strings.Join(ls, "\n")
}

// fakeRec is an in-memory Recording. Page matching ignores case, like a real
// recording index would.
type fakeRec struct {
	urls  map[string]bool
	pages []string
}

func newRec(pages map[string]string, searchURLs ...string) *fakeRec {
	r := &fakeRec{urls: map[string]bool{}}
	for u, text := range pages {
		r.urls[NormalizeURL(u)] = true
		r.pages = append(r.pages, strings.ToLower(normSpace(text)))
	}
	for _, u := range searchURLs {
		r.urls[NormalizeURL(u)] = true
	}
	return r
}

func (r *fakeRec) HasURL(u string) bool { return r.urls[u] }

func (r *fakeRec) PagesContain(text string) bool {
	text = strings.ToLower(normSpace(text))
	for _, p := range r.pages {
		if strings.Contains(p, text) {
			return true
		}
	}
	return false
}

// want is the expected shape of a Result.
type want struct {
	pass   bool
	graded bool
	reason string   // exact
	fab    []string // each must be a substring of one Fabricated entry; no other entries allowed
	extra  int
	recall float64
}

func check(t *testing.T, got Result, w want) {
	t.Helper()
	if got.Pass != w.pass || got.Graded != w.graded || got.Reason != w.reason {
		t.Errorf("pass=%v graded=%v reason=%q, want pass=%v graded=%v reason=%q (result %+v)",
			got.Pass, got.Graded, got.Reason, w.pass, w.graded, w.reason, got)
	}
	if math.Abs(got.Recall-w.recall) > 1e-9 {
		t.Errorf("recall = %v, want %v", got.Recall, w.recall)
	}
	if got.Extra != w.extra {
		t.Errorf("extra = %d, want %d", got.Extra, w.extra)
	}
	if len(got.Fabricated) != len(w.fab) {
		t.Errorf("fabricated = %q, want %d entries matching %q", got.Fabricated, len(w.fab), w.fab)
		return
	}
	for _, sub := range w.fab {
		found := false
		for _, f := range got.Fabricated {
			found = found || strings.Contains(f, sub)
		}
		if !found {
			t.Errorf("fabricated = %q, want an entry containing %q", got.Fabricated, sub)
		}
	}
}
