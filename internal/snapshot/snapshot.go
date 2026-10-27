// Package snapshot saves a repo's uncommitted files when Claude starts a
// subagent, so a replay can rebuild what the job saw. Without one, a replay
// has the last commit plus the files the job read in full, and a job that
// touched other uncommitted files can't be graded fairly.
//
// A Claude Code hook runs `htd snapshot` just before each subagent starts,
// so the job can't have changed anything yet. Contents are stored once, by
// hash; each snapshot is a small list of paths. Files that look like they
// hold secrets, binary files and big files are left out, and snapshots are
// deleted after MaxAge.
package snapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/secrets"
)

// Limits on what a snapshot keeps.
const (
	MaxFileBytes  = 1 << 20  // bigger files are left out
	MaxTotalBytes = 50 << 20 // the rest of a snapshot past this is left out
	MaxAge        = 60 * 24 * time.Hour
	// MaxGap is how long before a job's first transcript line its snapshot
	// may be taken. The hook runs just before the subagent starts.
	MaxGap = 2 * time.Minute
)

// Manifest is one snapshot.
type Manifest struct {
	At      time.Time         `json:"at"`
	Session string            `json:"session,omitempty"`
	Repo    string            `json:"repo"`              // the repo's top folder
	Head    string            `json:"head"`              // the commit checked out
	Files   map[string]string `json:"files"`             // repo-relative path -> content hash
	Deleted []string          `json:"deleted,omitempty"` // committed files deleted in the working tree
}

// Store keeps snapshots in a folder, normally ~/.htd/snapshots.
type Store struct{ dir string }

// Open returns the store in dir. Nothing is created until a snapshot is
// taken.
func Open(dir string) *Store { return &Store{dir: dir} }

var sha = regexp.MustCompile(`^[0-9a-f]{40,64}$`)

// Take saves the uncommitted files of the repo cwd is in. A folder that
// isn't in a git repo saves nothing and returns nil, nil.
func (s *Store) Take(ctx context.Context, cwd, session string, now time.Time) (*Manifest, error) {
	repo, err := git(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, nil
	}
	head, err := git(ctx, repo, "rev-parse", "HEAD")
	if err != nil || !sha.MatchString(head) {
		return nil, nil // no commits yet: nothing a replay could start from
	}
	// -z: paths come raw, NUL-separated; ignored files aren't listed.
	out, err := exec.CommandContext(ctx, "git", "-C", repo, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames").Output()
	if err != nil {
		return nil, err
	}
	m := &Manifest{At: now.UTC(), Session: session, Repo: repo, Head: head, Files: map[string]string{}}
	var total int64
	for _, entry := range strings.Split(string(out), "\x00") {
		if len(entry) < 4 {
			continue
		}
		code, rel := entry[:2], entry[3:]
		if secrets.Sensitive(rel) {
			continue
		}
		if strings.Contains(code, "D") {
			m.Deleted = append(m.Deleted, rel)
			continue
		}
		data, ok := readText(filepath.Join(repo, rel))
		if !ok || total+int64(len(data)) > MaxTotalBytes {
			continue
		}
		total += int64(len(data))
		h, err := s.putBlob(data)
		if err != nil {
			return nil, err
		}
		m.Files[rel] = h
	}
	if err := s.save(m); err != nil {
		return nil, err
	}
	return m, nil
}

// readText returns a regular file's contents when it's text and not too big.
func readText(path string) ([]byte, bool) {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > MaxFileBytes {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil || bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
		return nil, false
	}
	return data, true
}

func (s *Store) blobPath(h string) string { return filepath.Join(s.dir, "blobs", h[:2], h) }

func (s *Store) putBlob(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	h := hex.EncodeToString(sum[:])
	p := s.blobPath(h)
	if _, err := os.Stat(p); err == nil {
		now := time.Now()
		os.Chtimes(p, now, now) // in use again: Prune leaves fresh contents alone
		return h, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	return h, writeAtomic(p, data)
}

// repoDir is where one repo's manifests live, named by a hash of its path.
func (s *Store) repoDir(repo string) string {
	sum := sha256.Sum256([]byte(repo))
	return filepath.Join(s.dir, "manifests", hex.EncodeToString(sum[:8]))
}

func (s *Store) save(m *Manifest) error {
	d := s.repoDir(m.Repo)
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	name := strconv.FormatInt(m.At.UnixNano(), 10) + ".json"
	return writeAtomic(filepath.Join(d, name), b)
}

// Find returns the snapshot taken for a job: the last one in its session
// and repo at most MaxGap before it started.
func (s *Store) Find(repo, session string, start time.Time) (*Manifest, bool) {
	d := s.repoDir(repo)
	entries, err := os.ReadDir(d)
	if err != nil {
		return nil, false
	}
	var names []string
	for _, e := range entries {
		n, ok := nanos(e.Name())
		// a second of slack for the transcript's own clock rounding
		if ok && !n.After(start.Add(time.Second)) && start.Sub(n) <= MaxGap {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(d, name))
		if err != nil {
			continue
		}
		var m Manifest
		if json.Unmarshal(b, &m) == nil && m.Repo == repo && (session == "" || m.Session == session) {
			return &m, true
		}
	}
	return nil, false
}

func nanos(name string) (time.Time, bool) {
	n, err := strconv.ParseInt(strings.TrimSuffix(name, ".json"), 10, 64)
	if err != nil || !strings.HasSuffix(name, ".json") {
		return time.Time{}, false
	}
	return time.Unix(0, n), true
}

// Restore writes a snapshot over a copy of its commit in dir: changed and
// new files are written, deleted ones removed. Paths can't leave dir.
func (s *Store) Restore(m *Manifest, dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	for rel, h := range m.Files {
		if !sha.MatchString(h) {
			return fmt.Errorf("snapshot of %s: bad hash for %s", m.Repo, rel)
		}
		data, err := os.ReadFile(s.blobPath(h))
		if err != nil {
			return fmt.Errorf("snapshot of %s: %w", m.Repo, err)
		}
		if d := filepath.Dir(rel); d != "." {
			if err := root.MkdirAll(d, 0o755); err != nil {
				return err
			}
		}
		if err := root.WriteFile(rel, data, 0o644); err != nil {
			return err
		}
	}
	for _, rel := range m.Deleted {
		if err := root.Remove(rel); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Prune deletes snapshots older than MaxAge, then stored contents no
// snapshot uses. Contents stored in the last hour are kept: a snapshot being
// taken right now may not have saved the list that uses them yet.
func (s *Store) Prune(now time.Time) error {
	used := map[string]bool{}
	repos, _ := os.ReadDir(filepath.Join(s.dir, "manifests"))
	for _, r := range repos {
		d := filepath.Join(s.dir, "manifests", r.Name())
		entries, _ := os.ReadDir(d)
		for _, e := range entries {
			p := filepath.Join(d, e.Name())
			if n, ok := nanos(e.Name()); !ok || now.Sub(n) > MaxAge {
				os.Remove(p)
				continue
			}
			var m Manifest
			if b, err := os.ReadFile(p); err == nil && json.Unmarshal(b, &m) == nil {
				for _, h := range m.Files {
					used[h] = true
				}
			}
		}
	}
	return filepath.WalkDir(filepath.Join(s.dir, "blobs"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || used[d.Name()] {
			return nil
		}
		if info, err := d.Info(); err == nil && now.Sub(info.ModTime()) > time.Hour {
			os.Remove(p)
		}
		return nil
	})
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}
