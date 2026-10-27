package grade

import (
	"fmt"
	"reflect"
	"testing"
)

func render(as []Anchor) []string {
	out := []string{}
	for _, a := range as {
		switch a.Kind {
		case KindLine:
			out = append(out, fmt.Sprintf("line %s:%d-%d", a.Path, a.Line, a.End))
		case KindPath:
			out = append(out, "path "+a.Path)
		case KindQuote:
			out = append(out, "quote "+a.Text)
		default:
			out = append(out, "ident "+a.Text)
		}
	}
	return out
}

func TestAnchorsOf(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"path:line", "bug at internal/auth/login.go:42", []string{"line internal/auth/login.go:42-42"}},
		{"path:line:col", "see src/app.ts:10:5 here", []string{"line src/app.ts:10-10"}},
		{"line range", "see `src/app.ts:10-20`", []string{"line src/app.ts:10-20"}},
		{"github style", "see src/app.ts#L33", []string{"line src/app.ts:33-33"}},
		{"line in words", "in db/query.go, line 77, the query", []string{"line db/query.go:77-77"}},
		{"lines in parens", "in db/query.go (lines 70-80)", []string{"line db/query.go:70-80"}},
		{"bare path", "the handler in cmd/server/main.go is long", []string{"path cmd/server/main.go"}},
		{"bare file name and trailing period", "Edit main.go.", []string{"path main.go"}},
		{"leading ./ is dropped", "see ./lib/util.js", []string{"path lib/util.js"}},
		{"extension match is case-insensitive", "open README.MD", []string{"path README.MD"}},
		{"two paths", "compare a/x.go and b/y.go:3", []string{"path a/x.go", "line b/y.go:3-3"}},
		{"same path twice is listed once", "a/x.go and again a/x.go", []string{"path a/x.go"}},
		{"not paths: abbreviations, domains, product names", "e.g. visit example.com or use Node.js, i.e. next.js", []string{}},
		{"URL pieces are not anchors", "see https://github.com/acme/repo/blob/main/lib/util.go#L10 for getUserById", []string{"ident getUserById"}},
		{"backticked path is a path, not a quote", "`internal/auth/login.go`", []string{"path internal/auth/login.go"}},
		{"backticked directory", "everything under `internal/auth/`", []string{"path internal/auth"}},
		{"backticked span of 8+ characters is a quote", "the line `if err != nil {` is wrong", []string{"quote if err != nil {"}},
		{"backticked span under 8 characters is not", "the call `Save()` fails", []string{}},
		{"quote spaces are normalized", "the text `a   b\tcdefg` appears", []string{"quote a b cdefg"}},
		{"double quoted phrase", `it says "Bearer token expired" twice`, []string{"quote Bearer token expired"}},
		{"curly quoted phrase", "it says “Bearer token expired”", []string{"quote Bearer token expired"}},
		{"short quote is not an anchor", `it says "hello" once`, []string{}},
		{"shell commands in backticks are advice, not quotes", "run `go test ./internal/...` and `git diff --stat`", []string{}},
		{"CamelCase", "UserService calls getUserById and HTTPServer", []string{"ident UserService", "ident getUserById", "ident HTTPServer"}},
		{"snake_case and SCREAMING_SNAKE", "the user_id and MAX_RETRIES values", []string{"ident user_id", "ident MAX_RETRIES"}},
		{"dotted names", "req.body is used, and fs.readFileSync too", []string{"ident req.body", "ident fs.readFileSync"}},
		{"plain words and brand names are not identifiers", "The JavaScript on GitHub talks to PostgreSQL over OAuth", []string{}},
		{"identifier inside a path is not listed twice", "see user_service/get_user.go:5", []string{"line user_service/get_user.go:5-5"}},
		{
			"mixed", "SQL injection: `internal/auth/login.go:42` builds `\"SELECT * FROM users WHERE name = '\" + name` via buildQuery",
			[]string{"line internal/auth/login.go:42-42", "quote \"SELECT * FROM users WHERE name = '\" + name", "ident buildQuery"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := render(anchorsOf(c.text)); !reflect.DeepEqual(got, c.want) {
				t.Errorf("anchorsOf(%q)\n got %q\nwant %q", c.text, got, c.want)
			}
		})
	}
}

func TestPairKind(t *testing.T) {
	line := func(p string, n int) Anchor { return Anchor{Kind: KindLine, Path: p, Line: n, End: n} }
	path := func(p string) Anchor { return Anchor{Kind: KindPath, Path: p} }
	cases := []struct {
		name string
		x, y Anchor
		want Kind
	}{
		{"same line", line("a.go", 10), line("a.go", 10), KindLine},
		{"5 lines apart", line("a.go", 10), line("a.go", 15), KindLine},
		{"6 lines apart", line("a.go", 10), line("a.go", 16), 0},
		{"5 lines apart, other direction", line("a.go", 20), line("a.go", 15), KindLine},
		{"other file", line("a.go", 10), line("b.go", 10), 0},
		{"ranges that overlap", Anchor{Kind: KindLine, Path: "a.go", Line: 10, End: 30}, line("a.go", 25), KindLine},
		{"range and a line 5 past its end", Anchor{Kind: KindLine, Path: "a.go", Line: 10, End: 30}, line("a.go", 35), KindLine},
		{"bare path against a line", path("a.go"), line("a.go", 99), KindPath},
		{"line against a bare path", line("a.go", 99), path("a.go"), KindPath},
		{"two bare paths", path("a.go"), path("a.go"), KindPath},
		{"different bare paths", path("a.go"), path("b.go"), 0},
		{"same quote", Anchor{Kind: KindQuote, Text: "x y z w"}, Anchor{Kind: KindQuote, Text: "x y z w"}, KindQuote},
		{"same text, different kinds", Anchor{Kind: KindQuote, Text: "getUser1"}, Anchor{Kind: KindIdent, Text: "getUser1"}, 0},
		{"same identifier", Anchor{Kind: KindIdent, Text: "user_id"}, Anchor{Kind: KindIdent, Text: "user_id"}, KindIdent},
		{"identifiers are case-sensitive", Anchor{Kind: KindIdent, Text: "user_id"}, Anchor{Kind: KindIdent, Text: "USER_ID"}, 0},
	}
	for _, c := range cases {
		if got := pairKind(c.x, c.y); got != c.want {
			t.Errorf("%s: pairKind = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSamePath(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"web/src/billing/page.tsx", "billing/page.tsx", true},
		{"payment-history.tsx", "web/src/billing/payment-history.tsx", true},
		{"/Users/me/app/web/a.ts", "web/a.ts", true},
		{"web/src/billing/page.tsx", "ling/page.tsx", false},
		{"a/page.tsx", "b/page.tsx", false},
	}
	for _, c := range cases {
		if got := samePath(c.a, c.b); got != c.want {
			t.Errorf("samePath(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestPathLikeTokenInCodeIsNotMadeUp(t *testing.T) {
	ws := newWS(map[string]string{"web/ua.ts": "const ua = 'AcmeApp/1.0'"})
	ref := "- Native detection lives in `web/ua.ts`"
	cand := "- Native detection lives in `web/ua.ts`, matching `AcmeApp/1.0`"
	if r := Find(ref, cand, ws); len(r.Fabricated) != 0 {
		t.Errorf("fabricated = %v", r.Fabricated)
	}
}

// A path with a placeholder or route segment in it can't be checked, so its
// tail must not be read as a separate, made-up path.
func TestPlaceholderPathIsNotMadeUp(t *testing.T) {
	ws := newWS(map[string]string{"build.mjs": "copy()", "app/items/[id]/page.tsx": "x"})
	ref := "- `build.mjs` copies the manifest"
	cand := "- `build.mjs` copies it to `dist/<target>/manifest.json`, `${out}/bg.js` and `app/items/[id]/page.tsx`"
	if r := Find(ref, cand, ws); len(r.Fabricated) != 0 || !r.Pass {
		t.Errorf("fabricated = %v, pass = %v", r.Fabricated, r.Pass)
	}
}
