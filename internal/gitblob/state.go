package gitblob

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/go-git/go-git/v5/plumbing/format/index"
)

// RepositoryState pins the HEAD, index, and dirty worktree objects at one point
// in time. Worktree signatures are keyed by repository-relative path.
type RepositoryState struct {
	Commit   string
	Dirty    bool
	root     string
	index    string
	worktree map[string]string
}

// State reports the HEAD commit and worktree-dirty flag for the repository
// containing dir. Commit and dirty are recorded in-band with each recording
// (spec §5): the commit names the code measured — not the recording's later git
// position (§6.1) — and dirty marks a tree whose recording cannot serve as a
// baseline (§10). Neither is a validity guard: freshness is commit-independent.
// Worktree status is the documented slow path on large repos (§11).
// excludeDirs names absolute directories whose contents are pew's own
// outputs — the recording store — and never part of any measured subject:
// status entries under them contribute neither to dirty nor to the pinned
// worktree signatures (spec §5).
func State(dir string, excludeDirs ...string) (commit string, dirty bool, err error) {
	state, err := Snapshot(dir, excludeDirs...)
	if err != nil {
		return "", false, err
	}
	return state.Commit, state.Dirty, nil
}

// Snapshot returns the exact repository state relevant to dirty provenance.
func Snapshot(dir string, excludeDirs ...string) (RepositoryState, error) {
	r, err := Open(dir)
	if err != nil {
		return RepositoryState{}, err
	}
	head, err := r.repo.Head()
	if err != nil {
		return RepositoryState{}, fmt.Errorf("gitblob: resolve HEAD: %w", err)
	}
	wt, err := r.repo.Worktree()
	if err != nil {
		return RepositoryState{}, fmt.Errorf("gitblob: worktree: %w", err)
	}
	st, err := wt.Status()
	if err != nil {
		return RepositoryState{}, fmt.Errorf("gitblob: worktree status: %w", err)
	}
	idx, err := r.repo.Storer.Index()
	if err != nil {
		return RepositoryState{}, fmt.Errorf("gitblob: index: %w", err)
	}
	indexHash := sha256.New()
	entries := append([]*index.Entry(nil), idx.Entries...)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Name != entries[j].Name {
			return entries[i].Name < entries[j].Name
		}
		return entries[i].Stage < entries[j].Stage
	})
	for _, entry := range entries {
		fmt.Fprintf(indexHash, "%s\x00%s\x00%d\x00%d\x00%t\x00%t\n", entry.Name, entry.Hash, entry.Mode, entry.Stage, entry.SkipWorktree, entry.IntentToAdd)
	}

	// The exclusion covers dirty and the worktree signatures only; the
	// index hash deliberately keeps store entries — staging a recording is
	// an operator act, and mid-run index motion must still abort.
	excluded := func(path string) bool {
		abs := filepath.Join(r.root, filepath.FromSlash(path))
		for _, dir := range excludeDirs {
			rel, err := filepath.Rel(dir, abs)
			if err == nil && filepath.IsLocal(rel) {
				return true
			}
		}
		return false
	}
	paths := make([]string, 0, len(st))
	dirty := false
	for path := range st {
		if excluded(path) {
			continue
		}
		paths = append(paths, path)
		dirty = true
	}
	sort.Strings(paths)
	worktree := make(map[string]string, len(paths))
	for _, path := range paths {
		sig, err := worktreeSignature(filepath.Join(r.root, filepath.FromSlash(path)))
		if err != nil {
			return RepositoryState{}, err
		}
		status := st[path]
		worktree[path] = fmt.Sprintf("%d:%d:%s", status.Staging, status.Worktree, sig)
	}
	return RepositoryState{
		Commit:   head.Hash().String(),
		Dirty:    dirty,
		root:     r.root,
		index:    fmt.Sprintf("%x", indexHash.Sum(nil)),
		worktree: worktree,
	}, nil
}

// Equal reports whether two snapshots describe the same repository state.
func (s RepositoryState) Equal(other RepositoryState) bool {
	return s.equalExcept(other, nil)
}

// Root returns the absolute worktree root represented by the snapshot.
func (s RepositoryState) Root() string { return s.root }

func (s RepositoryState) equalExcept(other RepositoryState, excluded map[string]bool) bool {
	if s.Commit != other.Commit || s.index != other.index || filepath.Clean(s.root) != filepath.Clean(other.root) {
		return false
	}
	for path, sig := range s.worktree {
		if excluded[path] {
			continue
		}
		if other.worktree[path] != sig {
			return false
		}
	}
	for path, sig := range other.worktree {
		if excluded[path] {
			continue
		}
		if s.worktree[path] != sig {
			return false
		}
	}
	return true
}

func worktreeSignature(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "missing", nil
	}
	if err != nil {
		return "", fmt.Errorf("gitblob: lstat %s: %w", path, err)
	}
	h := sha256.New()
	fmt.Fprintf(h, "%d\x00", info.Mode())
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return "", fmt.Errorf("gitblob: readlink %s: %w", path, err)
		}
		_, _ = io.WriteString(h, target)
	case info.Mode().IsRegular():
		f, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("gitblob: open %s: %w", path, err)
		}
		_, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil {
			return "", fmt.Errorf("gitblob: read %s: %w", path, copyErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("gitblob: close %s: %w", path, closeErr)
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
