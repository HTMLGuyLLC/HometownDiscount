package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/goldset"
)

// gitRepo makes a repo with two commits: config.go at day 1, changed at day 3.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(date string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("2026-10-01T00:00:00Z", "init", "-q", "-b", "main")
	write("config.go", "package config\nvar Debug = false\n")
	write("big.go", "line1\nline2\nline3\n")
	run("2026-10-01T00:00:00Z", "add", ".")
	run("2026-10-01T00:00:00Z", "commit", "-q", "-m", "one")
	write("config.go", "package config\nvar Debug = false\nvar Later = 1\n")
	run("2026-10-03T00:00:00Z", "commit", "-q", "-am", "two")
	return dir
}

func TestPrepare(t *testing.T) {
	repo := gitRepo(t)
	repo, _ = filepath.EvalSymlinks(repo)
	it := &goldset.Item{
		Cwd:       repo,
		GitBranch: "main",
		Reads: []goldset.FileRead{
			// The job saw an uncommitted change: Debug = true.
			{Path: filepath.Join(repo, "config.go"), Full: true, Lines: map[int]string{1: "package config", 2: "var Debug = true"}},
			{Path: filepath.Join(repo, "big.go"), Lines: map[int]string{2: "line2"}},
			{Path: "/etc/hosts", Full: true, Lines: map[int]string{1: "x"}},
		},
	}
	start := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC) // between the two commits
	cache := t.TempDir()
	ws, err := Prepare(context.Background(), it, start, cache, filepath.Join(t.TempDir(), "job1"))
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	if got, _ := ws.Line("config.go", 2); got != "var Debug = true" {
		t.Errorf("overlay not applied: line 2 = %q", got)
	}
	if n, _ := ws.LineCount("config.go"); n != 2 {
		t.Errorf("config.go has %d lines; the day-3 commit's third line should be absent", n)
	}
	if ws.Fidelity.OK() || ws.Fidelity.Restored != 1 || len(ws.Fidelity.Outside) != 1 {
		t.Errorf("a job that read /etc/hosts depended on a file outside the repo: fidelity = %+v", ws.Fidelity)
	}
	if !ws.FileExists(filepath.Join(repo, "big.go")) || ws.FileExists("nope.go") {
		t.Error("FileExists wrong")
	}
	if !ws.Contains("var Debug = true") || ws.Contains("var Later") {
		t.Error("Contains wrong")
	}

	// A partial read that doesn't match the base breaks fidelity.
	it.Reads = []goldset.FileRead{{Path: filepath.Join(repo, "big.go"), Lines: map[int]string{2: "changed"}}}
	ws2, err := Prepare(context.Background(), it, start, cache, filepath.Join(t.TempDir(), "job2"))
	if err != nil {
		t.Fatal(err)
	}
	defer ws2.Close()
	if ws2.Fidelity.OK() {
		t.Error("mismatched partial read should fail fidelity")
	}

	// Files the job read through the shell must at least exist: a doc that
	// was never committed can't be read in a replay.
	it.Reads = nil
	it.Named = []string{filepath.Join(repo, "config.go"), filepath.Join(repo, "docs", "plan.md")}
	ws3, err := Prepare(context.Background(), it, start, cache, filepath.Join(t.TempDir(), "job3"))
	if err != nil {
		t.Fatal(err)
	}
	defer ws3.Close()
	if ws3.Fidelity.OK() || len(ws3.Fidelity.Missing) != 1 || ws3.Fidelity.Missing[0] != filepath.Join("docs", "plan.md") {
		t.Errorf("an uncommitted file read through the shell should fail fidelity: %+v", ws3.Fidelity)
	}
}

func TestPrepareNoRepo(t *testing.T) {
	_, err := Prepare(context.Background(), &goldset.Item{Cwd: t.TempDir()}, time.Now(), t.TempDir(), filepath.Join(t.TempDir(), "x"))
	if err != ErrNoRepo {
		t.Errorf("err = %v, want ErrNoRepo", err)
	}
}

func TestPartialPaths(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "web/src/billing"), 0o755)
	os.WriteFile(filepath.Join(root, "web/src/billing/page.tsx"), []byte("a\nb\nc\n"), 0o644)
	ws := &Workspace{Root: root, RepoRoot: "/Users/me/app"}
	if !ws.FileExists("billing/page.tsx") || !ws.FileExists("page.tsx") || ws.FileExists("ling/page.tsx") {
		t.Error("partial paths should match only at a folder boundary")
	}
	if l, ok := ws.Line("billing/page.tsx", 2); !ok || l != "b" {
		t.Errorf("Line = %q %v", l, ok)
	}
	if n, ok := ws.LineCount("/Users/me/app/web/src/billing/page.tsx"); !ok || n != 3 {
		t.Errorf("LineCount = %d %v", n, ok)
	}
}

func TestRoutesAndFolders(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "web/src/app/account/settings"), 0o755)
	os.WriteFile(filepath.Join(root, "web/src/app/account/settings/page.tsx"), []byte("x\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "ios/App/AcmeApp"), 0o755)
	ws := &Workspace{Root: root, RepoRoot: "/Users/me/app"}
	if !ws.FileExists("/account/settings/page.tsx") {
		t.Error("an app route with a leading slash should resolve inside the repo")
	}
	if !ws.FileExists("AcmeApp/") || !ws.FileExists("App/AcmeApp") {
		t.Error("partial folder paths should resolve")
	}
	if ws.FileExists("/etc/hosts") {
		t.Error("a system path outside the repo shouldn't resolve")
	}
}

func TestOverlayCantEscapeThroughSymlinks(t *testing.T) {
	repo := gitRepo(t)
	repo, _ = filepath.EvalSymlinks(repo)
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim.txt")
	os.WriteFile(victim, []byte("original"), 0o644)
	// A tracked symlink pointing outside the repo.
	os.Symlink(outside, filepath.Join(repo, "link"))
	cmd := exec.Command("git", "-C", repo, "add", "link")
	cmd.Run()
	cmd = exec.Command("git", "-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "link")
	cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE=2026-10-01T12:00:00Z", "GIT_AUTHOR_DATE=2026-10-01T12:00:00Z")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v %s", err, out)
	}
	it := &goldset.Item{Cwd: repo, GitBranch: "--output=/tmp/pwned", Reads: []goldset.FileRead{
		{Path: filepath.Join(repo, "link", "victim.txt"), Full: true, Lines: map[int]string{1: "overwritten"}},
	}}
	ws, err := Prepare(context.Background(), it, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), t.TempDir(), filepath.Join(t.TempDir(), "job"))
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if b, _ := os.ReadFile(victim); string(b) != "original" {
		t.Errorf("an overlay write escaped through a symlink: %q", b)
	}
	if ws.Fidelity.OK() {
		t.Error("a read that can't be restored should make the workspace unfaithful")
	}
	if _, err := os.Stat("/tmp/pwned"); err == nil {
		t.Error("a branch name was run as a git option")
	}
}
