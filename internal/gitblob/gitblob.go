// Package gitblob reads file content at a git ref via go-git — pew's pure git
// reader for materializing comparison baselines (spec §10, §6.1). It never shells
// to a git binary and never mutates the repository or working tree.
//
// A result file lands in a *child* commit of the code it measures (§6.1), so a
// baseline is resolved by git ref (HEAD, a tag/branch, a sha) and the recording's
// committed blob is read at that ref; the in-band `commit:` line inside the blob
// maps it back to the code it measured.
package gitblob

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// Repo is an opened repository for reading committed blobs.
type Repo struct {
	repo *gogit.Repository
	root string // absolute worktree root, for repo-relative path resolution
}

// Root returns the repository worktree root used for absolute path resolution.
func (r *Repo) Root() string { return r.root }

// Open opens the repository containing dir (walking up to the .git directory).
func Open(dir string) (*Repo, error) {
	repo, err := gogit.PlainOpenWithOptions(dir, &gogit.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, fmt.Errorf("gitblob: open repo at %s: %w", dir, err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("gitblob: worktree: %w", err)
	}
	return &Repo{repo: repo, root: wt.Filesystem.Root()}, nil
}

// ReadAt returns the content of the file at absPath as committed at ref. ok is
// false with a nil error when the file does not exist at ref — i.e. the benchmark
// was not recorded there, which is an expected, non-fatal condition (a benchmark
// added since the baseline). A bad ref or a read error is returned as a non-nil
// error.
func (r *Repo) ReadAt(ref, absPath string) (content []byte, ok bool, err error) {
	rel, err := r.relPath(absPath)
	if err != nil {
		return nil, false, err
	}
	h, err := r.repo.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return nil, false, fmt.Errorf("gitblob: resolve ref %q: %w", ref, err)
	}
	commit, err := r.repo.CommitObject(*h)
	if err != nil {
		return nil, false, fmt.Errorf("gitblob: commit %s: %w", h, err)
	}
	f, err := commit.File(rel)
	if err != nil {
		if errors.Is(err, object.ErrFileNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("gitblob: read %s@%s: %w", rel, ref, err)
	}
	if !isRegularBlob(f.Mode) {
		return nil, false, nil
	}
	s, err := f.Contents()
	if err != nil {
		return nil, false, fmt.Errorf("gitblob: contents %s@%s: %w", rel, ref, err)
	}
	return []byte(s), true, nil
}

// ListAt returns the absolute worktree paths of files committed under absDir at
// ref. A missing directory is an expected empty result; a bad ref is an error.
func (r *Repo) ListAt(ref, absDir string) ([]string, error) {
	relDir, err := r.relPath(absDir)
	if err != nil {
		return nil, err
	}
	h, err := r.repo.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return nil, fmt.Errorf("gitblob: resolve ref %q: %w", ref, err)
	}
	commit, err := r.repo.CommitObject(*h)
	if err != nil {
		return nil, fmt.Errorf("gitblob: commit %s: %w", h, err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("gitblob: tree %s: %w", h, err)
	}
	prefix := filepath.ToSlash(relDir)
	if prefix == "." {
		prefix = ""
	}
	var paths []string
	err = tree.Files().ForEach(func(f *object.File) error {
		name := f.Name
		if prefix != "" && !strings.HasPrefix(name, prefix+"/") {
			return nil
		}
		if !isRegularBlob(f.Mode) {
			return nil
		}
		paths = append(paths, filepath.Join(r.root, filepath.FromSlash(name)))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("gitblob: list %s@%s: %w", relDir, ref, err)
	}
	sort.Strings(paths)
	return paths, nil
}

func isRegularBlob(mode filemode.FileMode) bool {
	return mode == filemode.Regular || mode == filemode.Deprecated || mode == filemode.Executable
}

// relPath converts an absolute filesystem path to a slash-separated path relative
// to the repository root, as git addresses blobs. A path outside the repository
// is an error rather than a silent miss.
func (r *Repo) relPath(absPath string) (string, error) {
	rel, err := filepath.Rel(r.root, absPath)
	if err != nil {
		return "", fmt.Errorf("gitblob: %s relative to repo %s: %w", absPath, r.root, err)
	}
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("gitblob: %s is outside repo %s", absPath, r.root)
	}
	return filepath.ToSlash(rel), nil
}
