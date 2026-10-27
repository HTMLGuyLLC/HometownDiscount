package grade

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Kind says what an anchor points at. A higher kind is a stronger anchor.
type Kind int

// Anchor kinds, weakest first.
const (
	KindIdent Kind = iota + 1 // CamelCase, snake_case or dotted.name
	KindQuote                 // backticked span or quoted phrase, 8+ characters
	KindPath                  // file path
	KindLine                  // path:line
)

// Anchor is something in a finding that can be checked or matched.
type Anchor struct {
	Kind Kind
	Path string // KindPath, KindLine: relative to the repo root
	Line int    // KindLine: first cited line
	End  int    // KindLine: last cited line (== Line for one line)
	Text string // KindQuote, KindIdent
}

func (a Anchor) key() string {
	switch a.Kind {
	case KindLine:
		return fmt.Sprintf("L|%s|%d|%d", a.Path, a.Line, a.End)
	case KindPath:
		return "P|" + a.Path
	case KindQuote:
		return "Q|" + a.Text
	}
	return "I|" + a.Text
}

// minQuote is the shortest quote or backticked span that counts as an anchor.
const minQuote = 8

// fileExts are the extensions that make a word a file path. A fixed list keeps
// "e.g.", "example.com" and "a.m." from being read as files.
var fileExts = setOf(strings.Fields(`go ts tsx js jsx mjs cjs py rb rs java kt kts swift c h cc cpp hpp cs php sh bash zsh
sql md mdx json jsonc yaml yml toml html htm css scss sass less xml txt lock mod sum env conf cfg ini proto graphql gql
vue svelte tf gradle plist csv tsv svg ipynb dart ex exs lua scala`))

// notFiles are product names that look like files ("Node.js").
var notFiles = setOf(strings.Fields(`node.js next.js vue.js nuxt.js express.js react.js angular.js three.js d3.js
chart.js ember.js backbone.js`))

// commonIdents are brand and product names that look like identifiers.
var commonIdents = setOf(strings.Fields(`javascript typescript github gitlab postgresql mysql mongodb graphql oauth openai
sqlite youtube linkedin macos ios nodejs nextjs devops wordpress fastapi socket.io
node.js next.js vue.js nuxt.js express.js react.js angular.js three.js d3.js chart.js ember.js backbone.js`))

// domainSuffix ends a bare domain name ("example.com"), which is not an identifier.
var domainSuffix = setOf(strings.Fields(`com org net edu gov`))

var (
	pathRe       = regexp.MustCompile(`(^|[^\w@./~\-])((?:~?/)?(?:[\w@.\-]+/)*[\w@.\-]+\.([A-Za-z][A-Za-z0-9]{0,7}))`)
	lineSuffixRe = regexp.MustCompile(`^(?:[:#]L?(\d+)(?::\d+)?(?:\s*[-–]\s*L?(\d+))?|[\s,:(]*(?:lines?|ln|L)\.?\s*(\d+)(?:\s*[-–]\s*(\d+))?)`)
	dquoteRe     = regexp.MustCompile(`"([^"\n]+)"|“([^”\n]+)”`)
	identRe      = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*`)
	camelRe      = regexp.MustCompile(`[a-z0-9][A-Z]|[A-Z][A-Z][a-z]`)
	snakeRe      = regexp.MustCompile(`[A-Za-z0-9]_[A-Za-z0-9]`)
)

// shellWords start a backticked shell command, which is advice, not a quote.
var shellWords = setOf(strings.Fields(`go git npm pnpm yarn npx pip pip3 python python3 node deno bun make cargo curl wget
docker kubectl sudo cd ls cat grep rg sed awk bash sh zsh brew apt apt-get export echo mkdir rm mv cp chmod tar ssh psql
sqlite3 tsc eslint prettier vercel supabase gh claude`))

// pathRef is a file path in text, with the lines cited next to it (0 for none).
type pathRef struct {
	Path string
	Line int
	End  int
}

// pathHit is a pathRef with its place in the text.
type pathHit struct {
	pathRef
	start, end int
}

// samePath reports whether two cited paths name the same file. Answers often
// cite partial paths ("billing/page.tsx" for "web/src/app/billing/page.tsx")
// or absolute ones, so one path matching the end of the other at a folder
// boundary counts.
func samePath(a, b string) bool {
	return a == b || strings.HasSuffix(a, "/"+b) || strings.HasSuffix(b, "/"+a)
}

func cleanPath(p string) string {
	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	return p
}

// scanPaths finds file paths, with an optional line or line range after each:
// path:12, path:12-20, path:12:5, path#L12, "path line 12", "path lines 12-20".
func scanPaths(text string) []pathHit {
	var out []pathHit
	for _, m := range pathRe.FindAllStringSubmatchIndex(text, -1) {
		p := text[m[4]:m[5]]
		if !fileExts[strings.ToLower(text[m[6]:m[7]])] {
			continue
		}
		// The tail of a path with a placeholder, glob or route segment in it
		// ("dist/<target>/manifest.json", "${dir}/a.ts", "[id]/route.ts"):
		// the whole path can't be read, so it isn't checked either way.
		if m[3] > m[2] && strings.ContainsAny(text[m[2]:m[3]], ">}])*") && strings.HasPrefix(p, "/") {
			continue
		}
		if !strings.Contains(p, "/") && notFiles[strings.ToLower(p)] {
			continue
		}
		h := pathHit{pathRef: pathRef{Path: cleanPath(p)}, start: m[4], end: m[5]}
		if s := lineSuffixRe.FindStringSubmatch(text[m[5]:]); s != nil {
			first, last := s[1], s[2]
			if first == "" {
				first, last = s[3], s[4]
			}
			if n, _ := strconv.Atoi(first); n > 0 {
				h.Line, h.End = n, n
				if e, _ := strconv.Atoi(last); e > n {
					h.End = e
				}
				h.end = m[5] + len(s[0])
			}
		}
		out = append(out, h)
	}
	return out
}

// pathRefs returns the file paths in text. A backticked directory written with
// a trailing slash (`internal/grade/`) counts too.
func pathRefs(text string) []pathRef {
	text = stripURLs(text)
	var out []pathRef
	for _, h := range scanPaths(text) {
		out = append(out, h.pathRef)
	}
	for _, s := range backticks(text) {
		if isDirSpan(s) {
			out = append(out, pathRef{Path: strings.TrimRight(cleanPath(s), "/")})
		}
	}
	return out
}

// isDirSpan reports whether a backticked span is a directory written with a
// trailing slash.
func isDirSpan(s string) bool {
	return strings.HasSuffix(s, "/") && !strings.ContainsAny(s, " \t") &&
		strings.ContainsAny(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
}

func pathAnchors(refs []pathRef) []Anchor {
	out := make([]Anchor, 0, len(refs))
	for _, r := range refs {
		if r.Line > 0 {
			out = append(out, Anchor{Kind: KindLine, Path: r.Path, Line: r.Line, End: r.End})
		} else {
			out = append(out, Anchor{Kind: KindPath, Path: r.Path})
		}
	}
	return out
}

// isPathSpan reports whether a backticked span is just a file path or directory.
func isPathSpan(s string) bool {
	return isDirSpan(s) || !strings.ContainsAny(s, " \t") && len(pathRefs(s)) > 0
}

// isShellCommand reports whether a backticked span is a command line.
func isShellCommand(s string) bool {
	if strings.HasPrefix(s, "$ ") {
		return true
	}
	first, _, _ := strings.Cut(s, " ")
	return shellWords[first]
}

// quotes returns the backticked spans and quoted phrases of 8+ characters.
// File paths and shell commands in backticks are not quotes.
func quotes(text string) []string {
	var out []string
	for _, s := range backticks(text) {
		if len([]rune(s)) >= minQuote && !isPathSpan(s) && !isShellCommand(s) {
			out = append(out, s)
		}
	}
	rest := backtickRe.ReplaceAllString(text, " ")
	for _, m := range dquoteRe.FindAllStringSubmatch(rest, -1) {
		if s := normSpace(m[1] + m[2]); len([]rune(s)) >= minQuote {
			out = append(out, s)
		}
	}
	return out
}

// identifiers returns the CamelCase, snake_case and dotted.name words in text,
// leaving out file paths and URLs.
func identifiers(text string) []string {
	var out []string
	for _, tok := range identRe.FindAllString(withoutPaths(stripURLs(text)), -1) {
		if isAnchorIdent(tok) {
			out = append(out, tok)
		}
	}
	return out
}

// withoutPaths blanks the file paths (and their cited lines) in text.
func withoutPaths(text string) string {
	b := []byte(text)
	for _, h := range scanPaths(text) {
		for i := h.start; i < h.end; i++ {
			b[i] = ' '
		}
	}
	return string(b)
}

func isAnchorIdent(tok string) bool {
	if len(tok) < 3 || commonIdents[strings.ToLower(tok)] {
		return false
	}
	if strings.Contains(tok, ".") {
		segs := strings.Split(tok, ".")
		if domainSuffix[strings.ToLower(segs[len(segs)-1])] {
			return false
		}
		for _, seg := range segs {
			if len(seg) >= 3 {
				return true
			}
		}
		return false
	}
	return snakeRe.MatchString(tok) || camelRe.MatchString(tok)
}

// anchorsOf lists a finding's anchors, once each: paths and cited lines, then
// quotes, then identifiers, each in order of appearance.
func anchorsOf(text string) []Anchor {
	out := pathAnchors(pathRefs(text))
	for _, q := range quotes(text) {
		out = append(out, Anchor{Kind: KindQuote, Text: q})
	}
	for _, id := range identifiers(text) {
		out = append(out, Anchor{Kind: KindIdent, Text: id})
	}
	seen := map[string]bool{}
	uniq := out[:0]
	for _, a := range out {
		if k := a.key(); !seen[k] {
			seen[k] = true
			uniq = append(uniq, a)
		}
	}
	return uniq
}

// fabricated lists the anchors that name something the workspace doesn't have:
// a path that doesn't exist, a line past the end of its file, a quote found in
// no file. Each problem is listed once.
func fabricated(anchors []Anchor, ws Workspace) []string {
	if ws == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, a := range anchors {
		switch a.Kind {
		case KindPath, KindLine:
			if !ws.FileExists(a.Path) {
				// A path-like token that appears in the code (a user-agent
				// "App/1.0", a route, a package name) isn't made up.
				if a.Kind == KindPath && ws.Contains(a.Path) {
					continue
				}
				add("missing path: " + a.Path)
				continue
			}
			if a.Kind != KindLine {
				continue
			}
			if n, ok := ws.LineCount(a.Path); ok && (a.Line < 1 || max(a.Line, a.End) > n) {
				add(fmt.Sprintf("line past end of file: %s:%d (file has %d lines)", a.Path, max(a.Line, a.End), n))
			}
		case KindQuote:
			if !ws.Contains(a.Text) {
				add("quote not found: " + a.Text)
			}
		}
	}
	return out
}
