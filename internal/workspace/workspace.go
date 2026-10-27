// Package workspace rebuilds the repo a past job saw, so a cheaper model can
// replay the job on the same files.
//
// The base is the commit checked out when the job started, with the
// uncommitted files saved then (see package snapshot), or without a
// snapshot the last commit before the job. Every file the job read in full
// is restored from the transcript, which brings back uncommitted changes.
// Partial reads must match the base on the lines seen.
package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/goldset"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/snapshot"
)

// Workspace is a replay copy of a repo.
type Workspace struct {
	Root     string // folder the replay runs in
	RepoRoot string // the original repo root, used to map absolute paths
	Commit   string
	Fidelity Fidelity

	contents map[string]string // rel path -> text, loaded lazily by Contains
	files    []string          // every file's rel path, loaded lazily by resolve
}

// Fidelity says how faithfully the workspace matches what the job saw.
type Fidelity struct {
	Snapshot   bool     // uncommitted files saved when the job started were restored
	Restored   int      // full reads written over the base
	Mismatched []string // partial reads that differ from the base
	Outside    []string // reads outside the repo (can't be restored)
	Missing    []string // files the job's commands named that the workspace lacks
}

// OK reports whether the job can be graded on this workspace. A job that
// read files outside its repo (instructions in a temp folder, another
// project), or read files through the shell that weren't committed,
// depended on things a replay doesn't have.
func (f Fidelity) OK() bool {
	return len(f.Mismatched) == 0 && len(f.Outside) == 0 && len(f.Missing) == 0
}

// ErrNoRepo means the job's folder is gone or isn't a git repo.
var ErrNoRepo = errors.New("job folder is not an available git repo")

// From is what's known about a job beyond its transcript's contents.
type From struct {
	Start     time.Time       // when the job started
	Session   string          // its Claude Code session
	Snapshots *snapshot.Store // uncommitted files saved as jobs started; nil for none
}

// Prepare builds a workspace for one job under dir. The base commit is
// checked out once per repo and commit into cacheDir and cloned from there.
func Prepare(ctx context.Context, it *goldset.Item, from From, cacheDir, dir string) (*Workspace, error) {
	start := from.Start
	repoRoot, err := git(ctx, it.Cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, ErrNoRepo
	}
	var snap *snapshot.Manifest
	if from.Snapshots != nil {
		if m, ok := from.Snapshots.Find(repoRoot, from.Session, start); ok {
			if _, err := git(ctx, repoRoot, "cat-file", "-e", m.Head+"^{commit}"); err == nil {
				snap = m // its commit may be gone, e.g. after a rebase
			}
		}
	}
	// The branch name comes from a transcript: never let it be read as a
	// git option.
	ref := it.GitBranch
	if !safeRef.MatchString(ref) {
		ref = "HEAD"
	}
	before := "--before=" + start.UTC().Format(time.RFC3339)
	commit, err := git(ctx, repoRoot, "rev-list", "-1", before, "--end-of-options", ref)
	if snap != nil {
		commit, err = snap.Head, nil
	}
	if err != nil || !sha.MatchString(commit) {
		// The branch may be gone; fall back to any commit before the job.
		commit, err = git(ctx, repoRoot, "rev-list", "-1", before, "--all")
		if err != nil || !sha.MatchString(commit) {
			return nil, fmt.Errorf("no commit before %s in %s", start.Format(time.RFC3339), repoRoot)
		}
	}

	base := filepath.Join(cacheDir, safeName(repoRoot), commit)
	if _, err := os.Stat(base); err != nil {
		if err := exportCommit(ctx, repoRoot, commit, base); err != nil {
			return nil, err
		}
	}
	if err := cloneTree(base, dir); err != nil {
		return nil, err
	}

	ws := &Workspace{Root: dir, RepoRoot: repoRoot, Commit: commit}
	if snap != nil {
		if err := from.Snapshots.Restore(snap, dir); err != nil {
			return nil, err
		}
		ws.Fidelity.Snapshot = true
	}
	for _, r := range it.Reads {
		rel, ok := ws.repoRel(r.Path)
		if !ok {
			ws.Fidelity.Outside = append(ws.Fidelity.Outside, r.Path)
			continue
		}
		if r.Full {
			if err := writeLines(dir, rel, r.Lines); err != nil {
				// e.g. the path runs through a symlink that leaves the copy
				ws.Fidelity.Mismatched = append(ws.Fidelity.Mismatched, rel)
				continue
			}
			ws.Fidelity.Restored++
			continue
		}
		for n, want := range r.Lines {
			if got, ok := ws.Line(rel, n); !ok || got != want {
				ws.Fidelity.Mismatched = append(ws.Fidelity.Mismatched, rel)
				break
			}
		}
	}
	// Files read through the shell, Grep or Glob have no recorded content;
	// at least they must exist (e.g. not uncommitted docs).
	for _, p := range it.Named {
		rel, ok := ws.repoRel(p)
		if !ok {
			ws.Fidelity.Outside = append(ws.Fidelity.Outside, p)
			continue
		}
		if _, err := os.Lstat(filepath.Join(dir, rel)); err != nil {
			ws.Fidelity.Missing = append(ws.Fidelity.Missing, rel)
		}
	}
	return ws, nil
}

var (
	safeRef = regexp.MustCompile(`^[A-Za-z0-9._/][A-Za-z0-9._/-]{0,250}$`)
	sha     = regexp.MustCompile(`^[0-9a-f]{40,64}$`)
)

// repoRel maps a file the original job read to the workspace, strictly: an
// absolute path must be inside the repo. Files read elsewhere aren't restored.
func (w *Workspace) repoRel(path string) (string, bool) {
	p := filepath.Clean(path)
	if !filepath.IsAbs(p) {
		return w.Rel(p)
	}
	rel, err := filepath.Rel(w.RepoRoot, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return rel, true
}

// Rel maps a path from the original job (absolute or repo-relative) to a
// path inside the workspace.
func (w *Workspace) Rel(path string) (string, bool) {
	p := filepath.Clean(path)
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(w.RepoRoot, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			// Not under the repo: answers also write repo paths and app
			// routes with a leading slash ("/web/src/a.ts", "/account/page.tsx").
			rel = strings.TrimLeft(p, "/")
		}
		p = rel
	}
	if strings.HasPrefix(p, "..") {
		return "", false
	}
	return p, true
}

// FileExists reports whether a file or folder exists in the workspace. A
// partial path ("billing/page.tsx") counts when some file ends with it.
func (w *Workspace) FileExists(path string) bool {
	rel, ok := w.Rel(path)
	if !ok {
		return false
	}
	if _, err := os.Stat(filepath.Join(w.Root, rel)); err == nil {
		return true
	}
	return len(w.resolve(rel)) > 0
}

// Line returns the 1-based line n of a file. For a partial path that several
// files end with, the first file that has the line answers.
func (w *Workspace) Line(path string, n int) (string, bool) {
	for _, rel := range w.candidates(path) {
		if lines, ok := w.lines(rel); ok && n >= 1 && n <= len(lines) {
			return lines[n-1], true
		}
	}
	return "", false
}

// LineCount returns a file's number of lines (the longest, for a partial
// path that several files end with).
func (w *Workspace) LineCount(path string) (int, bool) {
	best, found := 0, false
	for _, rel := range w.candidates(path) {
		if lines, ok := w.lines(rel); ok {
			best, found = max(best, len(lines)), true
		}
	}
	return best, found
}

// candidates is the exact file if it exists, else every file the path ends.
func (w *Workspace) candidates(path string) []string {
	rel, ok := w.Rel(path)
	if !ok {
		return nil
	}
	if st, err := os.Stat(filepath.Join(w.Root, rel)); err == nil && !st.IsDir() {
		return []string{rel}
	}
	return w.resolve(rel)
}

// resolve lists files and folders whose path ends with rel at a folder boundary.
func (w *Workspace) resolve(rel string) []string {
	if w.files == nil {
		w.files = []string{}
		_ = filepath.WalkDir(w.Root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			if r, _ := filepath.Rel(w.Root, p); r != "." {
				w.files = append(w.files, filepath.ToSlash(r)) // files and folders
			}
			return nil
		})
	}
	rel = strings.TrimSuffix(filepath.ToSlash(rel), "/")
	suffix := "/" + rel
	var out []string
	for _, f := range w.files {
		if f == rel || strings.HasSuffix(f, suffix) {
			out = append(out, f)
		}
	}
	return out
}

// Contains reports whether text appears in any text file of the workspace.
func (w *Workspace) Contains(text string) bool {
	if text == "" {
		return false
	}
	if w.contents == nil {
		w.loadContents()
	}
	for _, c := range w.contents {
		if strings.Contains(c, text) {
			return true
		}
	}
	return false
}

// Close deletes the workspace copy (not the cached base).
func (w *Workspace) Close() error { return os.RemoveAll(w.Root) }

func (w *Workspace) lines(path string) ([]string, bool) {
	rel, ok := w.Rel(path)
	if !ok {
		return nil, false
	}
	// Through os.Root: a repo symlink can't point the grader outside the copy.
	root, err := os.OpenRoot(w.Root)
	if err != nil {
		return nil, false
	}
	defer root.Close()
	if st, err := root.Lstat(rel); err != nil || !st.Mode().IsRegular() || st.Size() > 4<<20 {
		return nil, false
	}
	b, err := root.ReadFile(rel)
	if err != nil {
		return nil, false
	}
	s := strings.TrimSuffix(string(b), "\n")
	if s == "" {
		return nil, true
	}
	return strings.Split(s, "\n"), true
}

// Files bigger than this, binary files and dependency folders are skipped by Contains.
const maxContentsFile = 1 << 20

var skipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, "dist": true, "build": true, ".next": true}

func (w *Workspace) loadContents() {
	w.contents = map[string]string{}
	_ = filepath.WalkDir(w.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() { // symlinks, devices and pipes could hang or leak
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxContentsFile {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil || bytes.IndexByte(b, 0) >= 0 {
			return nil
		}
		rel, _ := filepath.Rel(w.Root, p)
		w.contents[rel] = string(b)
		return nil
	})
}

// maxRestoreLines caps the line numbers a transcript can make a restored
// file reach: a made-up line number in the billions would fill memory.
const maxRestoreLines = 200_000

// writeLines restores a file inside the workspace through os.Root, so a
// symlink in the repo can't redirect the write outside the copy.
func writeLines(dir, rel string, lines map[int]string) error {
	maxN := 0
	for n := range lines {
		maxN = max(maxN, n)
	}
	if maxN > maxRestoreLines {
		return fmt.Errorf("%s: line %d is past the %d lines a restored file may have", rel, maxN, maxRestoreLines)
	}
	var b strings.Builder
	for n := 1; n <= maxN; n++ {
		b.WriteString(lines[n])
		b.WriteByte('\n')
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	if d := filepath.Dir(rel); d != "." {
		if err := root.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return root.WriteFile(rel, []byte(b.String()), 0o644)
}

// exportCommit writes a commit's tree to dir (no .git folder).
func exportCommit(ctx context.Context, repoRoot, commit, dir string) error {
	tmp := dir + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	archive := exec.CommandContext(ctx, "git", "-C", repoRoot, "archive", "--format=tar", commit)
	untar := exec.CommandContext(ctx, "tar", "-x", "-C", tmp)
	pipe, err := archive.StdoutPipe()
	if err != nil {
		return err
	}
	untar.Stdin = pipe
	if err := untar.Start(); err != nil {
		return err
	}
	if err := archive.Run(); err != nil {
		_ = untar.Process.Kill()
		_ = untar.Wait()
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("git archive %s: %w", commit, err)
	}
	if err := untar.Wait(); err != nil {
		return fmt.Errorf("untar %s: %w", commit, err)
	}
	return os.Rename(tmp, dir)
}

// cloneTree copies a folder, using copy-on-write clones where the file
// system supports them (APFS, Btrfs, XFS), so a workspace costs almost no disk.
func cloneTree(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("cp", "-c", "-R", src, dst)
	case "linux":
		cmd = exec.Command("cp", "--reflink=auto", "-R", src, dst)
	default:
		cmd = exec.Command("cp", "-R", src, dst)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("copy workspace: %v: %s", err, out)
	}
	return nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	if dir == "" {
		return "", ErrNoRepo
	}
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

func safeName(path string) string {
	return strings.Trim(strings.NewReplacer("/", "-", "\\", "-", ":", "").Replace(path), "-")
}
