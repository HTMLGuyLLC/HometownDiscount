package main

import (
	"os"
	"path/filepath"
	"testing"
)

// ask_local serves one project's files: never the home folder, a folder
// above it, or a hidden folder in it, where credentials live.
func TestUnsafeRoot(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "code", "app"), 0o755)
	os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o755)
	os.Symlink(filepath.Join(home, ".claude"), filepath.Join(home, "code", "claude-link"))
	for root, unsafe := range map[string]bool{
		"/":                            true,
		filepath.Dir(home):             true,
		home:                           true,
		filepath.Join(home, ".claude"): true,
		filepath.Join(home, ".claude", "projects"): true,
		filepath.Join(home, "code", "claude-link"): true, // a link into ~/.claude
		filepath.Join(home, "code", "app"):         false,
		filepath.Join(home, "code"):                false,
	} {
		if got := unsafeRoot(root, home) != ""; got != unsafe {
			t.Errorf("unsafeRoot(%s) = %q, want unsafe=%v", root, unsafeRoot(root, home), unsafe)
		}
	}
}
