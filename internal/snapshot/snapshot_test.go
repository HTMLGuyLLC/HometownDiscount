package snapshot

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if r, err := filepath.EvalSymlinks(repo); err == nil {
		repo = r
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "old.md"), []byte("old\n"), 0o644)
	os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("build/\n"), 0o644)
	run("add", ".")
	run("commit", "-qm", "base")
	return repo
}

// A snapshot keeps changed and new text files and deletions, and leaves out
// secrets, ignored, binary and big files. Restoring it over the commit
// gives the working tree back.
func TestTakeAndRestore(t *testing.T) {
	repo := gitRepo(t)
	write := func(rel, s string) {
		os.MkdirAll(filepath.Dir(filepath.Join(repo, rel)), 0o755)
		os.WriteFile(filepath.Join(repo, rel), []byte(s), 0o644)
	}
	write("app.go", "package app\n\nfunc New() {}\n")
	write("docs/plan.md", "# Plan\n")
	write(".env", "TOKEN=abc\n")
	write("build/out.txt", "ignored\n")
	write("logo.png", "\x89PNG\x00\x00")
	write("huge.txt", strings.Repeat("x", MaxFileBytes+1))
	os.Remove(filepath.Join(repo, "old.md"))

	s := Open(filepath.Join(t.TempDir(), "snapshots"))
	at := time.Now()
	m, err := s.Take(context.Background(), filepath.Join(repo, "docs"), "sess-1", at)
	if err != nil || m == nil {
		t.Fatalf("take: %v", err)
	}
	if m.Repo != repo || len(m.Head) != 40 {
		t.Errorf("repo %q head %q", m.Repo, m.Head)
	}
	var kept []string
	for rel := range m.Files {
		kept = append(kept, rel)
	}
	if len(kept) != 2 || m.Files["app.go"] == "" || m.Files["docs/plan.md"] == "" {
		t.Errorf("want app.go and docs/plan.md kept, got %v", kept)
	}
	if len(m.Deleted) != 1 || m.Deleted[0] != "old.md" {
		t.Errorf("want old.md deleted, got %v", m.Deleted)
	}

	got, ok := s.Find(repo, "sess-1", at.Add(3*time.Second))
	if !ok || got.Head != m.Head {
		t.Fatalf("find: %v %+v", ok, got)
	}
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "app.go"), []byte("package app\n"), 0o644) // the commit's copy
	os.WriteFile(filepath.Join(ws, "old.md"), []byte("old\n"), 0o644)
	if err := s.Restore(got, ws); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "app.go")); !strings.Contains(string(b), "func New") {
		t.Errorf("app.go not restored: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "docs/plan.md")); string(b) != "# Plan\n" {
		t.Errorf("new file not restored: %q", b)
	}
	if _, err := os.Stat(filepath.Join(ws, "old.md")); err == nil {
		t.Error("deleted file still there")
	}

	// Taking it again stores no new contents.
	blobs := func() int {
		n := 0
		filepath.WalkDir(filepath.Join(s.dir, "blobs"), func(_ string, d os.DirEntry, _ error) error {
			if d != nil && !d.IsDir() {
				n++
			}
			return nil
		})
		return n
	}
	before := blobs()
	s.Take(context.Background(), repo, "sess-1", at.Add(time.Minute))
	if blobs() != before {
		t.Errorf("unchanged files stored twice: %d -> %d", before, blobs())
	}
}

// Find takes the last snapshot of the job's own session shortly before it
// started, and nothing else.
func TestFind(t *testing.T) {
	repo := gitRepo(t)
	s := Open(t.TempDir())
	start := time.Now()
	for _, c := range []struct {
		at   time.Time
		sess string
	}{
		{start.Add(-time.Hour), "s1"},        // too early
		{start.Add(-30 * time.Second), "s1"}, // the one
		{start.Add(-10 * time.Second), "s2"}, // another session
		{start.Add(30 * time.Second), "s1"},  // after the job started
	} {
		if _, err := s.Take(context.Background(), repo, c.sess, c.at); err != nil {
			t.Fatal(err)
		}
	}
	m, ok := s.Find(repo, "s1", start)
	if !ok || !m.At.Equal(start.Add(-30*time.Second).UTC()) {
		t.Errorf("want the s1 snapshot 30s before, got %v %+v", ok, m)
	}
	if _, ok := s.Find(repo, "s3", start); ok {
		t.Error("no snapshot of another session should match")
	}
	if _, ok := s.Find("/elsewhere", "s1", start); ok {
		t.Error("no snapshot of another repo should match")
	}
}

// Prune deletes old snapshots and contents nothing uses any more, but not
// contents stored in the last hour.
func TestPrune(t *testing.T) {
	repo := gitRepo(t)
	s := Open(t.TempDir())
	os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app // v1\n"), 0o644)
	old, _ := s.Take(context.Background(), repo, "s", time.Now().Add(-MaxAge-time.Hour))
	os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app // v2\n"), 0o644)
	cur, _ := s.Take(context.Background(), repo, "s", time.Now())
	oldBlob := s.blobPath(old.Files["app.go"])

	s.Prune(time.Now())
	if _, err := os.Stat(oldBlob); err != nil {
		t.Error("contents stored in the last hour must survive a prune")
	}
	s.Prune(time.Now().Add(2 * time.Hour))
	if _, err := os.Stat(oldBlob); err == nil {
		t.Error("contents of an expired snapshot should be deleted")
	}
	if _, err := os.Stat(s.blobPath(cur.Files["app.go"])); err != nil {
		t.Error("contents of a live snapshot must stay")
	}
}

// Outside a git repo there's nothing to save.
func TestNotARepo(t *testing.T) {
	s := Open(t.TempDir())
	if m, err := s.Take(context.Background(), t.TempDir(), "s", time.Now()); m != nil || err != nil {
		t.Errorf("want nothing, got %+v %v", m, err)
	}
}
