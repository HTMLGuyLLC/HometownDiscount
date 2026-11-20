package ui

import (
	"regexp"
	"testing"
)

// Every element the page's scripts look up by id is in the page. The
// scripts and page are read here, not listed, so a renamed id fails this
// test instead of quietly breaking the page.
func TestScriptIdsExist(t *testing.T) {
	page, err := web.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, m := range regexp.MustCompile(`\bid\s*=\s*["']([^"']+)["']`).FindAllSubmatch(page, -1) {
		have[string(m[1])] = true
	}
	lookup := regexp.MustCompile(`(?:getElementById|\$)\(\s*["']([^"']+)["']\s*\)`)
	for _, file := range []string{"web/app.js", "web/settings.js"} {
		b, err := web.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		found := lookup.FindAllSubmatch(b, -1)
		if len(found) == 0 {
			t.Errorf("%s: no id lookups found; has the script's lookup style changed?", file)
		}
		for _, m := range found {
			if id := string(m[1]); !have[id] {
				t.Errorf("%s looks up #%s, which index.html doesn't have", file, id)
			}
		}
	}
}
