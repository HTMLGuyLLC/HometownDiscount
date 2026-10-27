package goldset

import (
	"path/filepath"
	"regexp"
	"strings"
)

var (
	quotedArg  = regexp.MustCompile(`'[^']*'|"(?:[^"\\]|\\.)*"`)
	separators = regexp.MustCompile(`&&|\|\||[;|&\n]`)
	fileExt    = regexp.MustCompile(`\.[A-Za-z][A-Za-z0-9]{0,7}$`)
)

// CommandPaths lists the files and folders a shell command names, resolved
// against dir, and returns the folder the shell is in afterwards (after any
// cd). It is deliberately simple: quoted text (patterns, scripts) is
// ignored, and a token counts as a path when it has a slash or a file
// extension. A wrong guess only makes a replay skip a job, never grade one
// on a workspace that lacks what the job read.
func CommandPaths(cmd, dir string) ([]string, string) {
	var paths []string
	for _, seg := range separators.Split(quotedArg.ReplaceAllString(cmd, "''"), -1) {
		f := strings.Fields(seg)
		if len(f) == 0 {
			continue
		}
		if f[0] == "cd" {
			if len(f) > 1 && !strings.ContainsAny(f[1], "$~`'") {
				if d := resolve(dir, f[1]); d != "" {
					dir = d
				}
			}
			continue
		}
		for _, tok := range f[1:] {
			if pathLike(tok) {
				if p := resolve(dir, tok); p != "" {
					paths = append(paths, p)
				}
			}
		}
	}
	return paths, dir
}

func pathLike(tok string) bool {
	switch {
	case tok == "/" || tok == "." || tok == "..",
		strings.HasPrefix(tok, "-"),
		strings.HasPrefix(tok, "/dev/"),
		strings.ContainsAny(tok, "*?[]{}$<>=()~`:'\"\\,!%"),
		// ".name" is a jq filter or a dotfile more often than a path here.
		strings.HasPrefix(tok, ".") && !strings.HasPrefix(tok, "./") && !strings.HasPrefix(tok, "../"):
		return false
	}
	return strings.Contains(tok, "/") || fileExt.MatchString(tok)
}

// resolve makes p absolute against dir; "" when it can't.
func resolve(dir, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, p)
}
