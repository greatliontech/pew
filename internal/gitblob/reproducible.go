package gitblob

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// ReproducibleAt reports whether absPath's current Git-representable state is
// exactly the state recorded at ref. It compares absence, object type, regular
// file content and executable mode, symlink targets, and complete directory
// trees. Filesystem objects Git cannot represent and submodules are not
// reproducible.
func (r *Repo) ReproducibleAt(ref, absPath string) (bool, error) {
	return r.ReproducibleAtWithin(ref, absPath, r.root)
}

// ReproducibleAtWithin is ReproducibleAt constrained to targets under root.
// A matching symlink that escapes root is not reproducible by that root's commit.
func (r *Repo) ReproducibleAtWithin(ref, absPath, root string) (bool, error) {
	if !withinRoot(root, absPath) {
		return false, fmt.Errorf("gitblob: %s is outside boundary %s", absPath, root)
	}
	return r.reproducibleAt(ref, absPath, filepath.Clean(root), map[string]bool{})
}

func (r *Repo) reproducibleAt(ref, absPath, boundary string, visiting map[string]bool) (bool, error) {
	absPath = filepath.Clean(absPath)
	if visiting[absPath] {
		return false, nil
	}
	visiting[absPath] = true
	defer delete(visiting, absPath)
	rel, err := r.relPath(absPath)
	if err != nil {
		return false, err
	}

	h, err := r.repo.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return false, fmt.Errorf("gitblob: resolve ref %q: %w", ref, err)
	}
	commit, err := r.repo.CommitObject(*h)
	if err != nil {
		return false, fmt.Errorf("gitblob: commit %s: %w", h, err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return false, fmt.Errorf("gitblob: tree %s: %w", h, err)
	}

	rel = filepath.ToSlash(rel)
	if rel == "." || rel == "" {
		return r.reproducibleTree(ref, absPath, rel, tree, boundary, visiting)
	}
	link, suffix, found, err := firstIntermediateSymlink(r.root, rel)
	if err != nil {
		return false, err
	}
	if found {
		matches, err := r.reproducibleAt(ref, link, boundary, visiting)
		if err != nil || !matches {
			return matches, err
		}
		resolved, err := filepath.EvalSymlinks(link)
		if err != nil {
			return false, nil
		}
		resolved = filepath.Join(resolved, filepath.FromSlash(suffix))
		if !withinRoot(boundary, resolved) {
			return false, nil
		}
		return r.reproducibleAt(ref, resolved, boundary, visiting)
	}
	entry, absent, err := r.findTreeEntry(tree, rel)
	if err != nil {
		return false, err
	}
	if entry == nil {
		if !absent {
			return false, nil
		}
		if _, statErr := os.Lstat(absPath); statErr == nil {
			return false, nil
		} else if errors.Is(statErr, os.ErrNotExist) {
			return true, nil
		} else {
			return false, fmt.Errorf("gitblob: lstat %s: %w", absPath, statErr)
		}
	}

	return r.reproducibleEntry(ref, absPath, rel, entry, boundary, visiting)
}

func withinRoot(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && filepath.IsLocal(rel)
}

func firstIntermediateSymlink(root, rel string) (string, string, bool, error) {
	parts := strings.Split(rel, "/")
	path := root
	for i, part := range parts[:len(parts)-1] {
		path = filepath.Join(path, filepath.FromSlash(part))
		info, err := os.Lstat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", "", false, nil
			}
			return "", "", false, fmt.Errorf("gitblob: lstat %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return path, strings.Join(parts[i+1:], "/"), true, nil
		}
	}
	return "", "", false, nil
}

// findTreeEntry distinguishes an absent path from one nested beneath a Git
// object that cannot contain children, such as a blob, symlink, or gitlink.
func (r *Repo) findTreeEntry(tree *object.Tree, rel string) (entry *object.TreeEntry, absent bool, err error) {
	parts := strings.Split(rel, "/")
	for i, name := range parts {
		entry = nil
		for j := range tree.Entries {
			if tree.Entries[j].Name == name {
				entry = &tree.Entries[j]
				break
			}
		}
		if entry == nil {
			return nil, true, nil
		}
		if i == len(parts)-1 {
			return entry, false, nil
		}
		if entry.Mode != filemode.Dir {
			return nil, false, nil
		}
		tree, err = r.repo.TreeObject(entry.Hash)
		if err != nil {
			return nil, false, fmt.Errorf("gitblob: read tree %s: %w", strings.Join(parts[:i+1], "/"), err)
		}
	}
	return nil, true, nil
}

func (r *Repo) reproducibleEntry(ref, path, rel string, entry *object.TreeEntry, boundary string, visiting map[string]bool) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("gitblob: lstat %s: %w", path, err)
	}

	worktreeMode, err := filemode.NewFromOSFileMode(info.Mode())
	if err != nil || entry.Mode == filemode.Submodule || entry.Mode.IsMalformed() {
		return false, nil
	}
	committedMode := entry.Mode
	if committedMode == filemode.Deprecated {
		committedMode = filemode.Regular
	}
	if worktreeMode != committedMode {
		return false, nil
	}

	switch committedMode {
	case filemode.Regular, filemode.Executable:
		content, err := os.ReadFile(path)
		if err != nil {
			return false, fmt.Errorf("gitblob: read %s: %w", path, err)
		}
		return r.reproducibleBlob(content, entry.Hash, rel)
	case filemode.Symlink:
		target, err := os.Readlink(path)
		if err != nil {
			return false, fmt.Errorf("gitblob: readlink %s: %w", path, err)
		}
		matches, err := r.reproducibleBlob([]byte(target), entry.Hash, rel)
		if err != nil || !matches {
			return matches, err
		}
		targetPath := target
		if !filepath.IsAbs(targetPath) {
			targetPath = filepath.Join(filepath.Dir(path), targetPath)
		}
		targetPath = filepath.Clean(targetPath)
		if !withinRoot(boundary, targetPath) {
			return false, nil
		}
		return r.reproducibleAt(ref, targetPath, boundary, visiting)
	case filemode.Dir:
		tree, err := r.repo.TreeObject(entry.Hash)
		if err != nil {
			return false, fmt.Errorf("gitblob: read tree %s: %w", rel, err)
		}
		return r.reproducibleTree(ref, path, rel, tree, boundary, visiting)
	default:
		return false, nil
	}
}

func (r *Repo) reproducibleTree(ref, path, rel string, tree *object.Tree, boundary string, visiting map[string]bool) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("gitblob: lstat %s: %w", path, err)
	}
	if !info.IsDir() {
		return false, nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return false, fmt.Errorf("gitblob: read directory %s: %w", path, err)
	}
	if filepath.Clean(path) == filepath.Clean(r.root) {
		for i, entry := range entries {
			if entry.Name() == ".git" {
				entries = append(entries[:i], entries[i+1:]...)
				break
			}
		}
	}
	if len(entries) != len(tree.Entries) {
		return false, nil
	}

	committed := make(map[string]*object.TreeEntry, len(tree.Entries))
	for i := range tree.Entries {
		entry := &tree.Entries[i]
		committed[entry.Name] = entry
	}
	for _, entry := range entries {
		gitEntry, ok := committed[entry.Name()]
		if !ok {
			return false, nil
		}
		childRel := entry.Name()
		if rel != "." && rel != "" {
			childRel = rel + "/" + childRel
		}
		ok, err := r.reproducibleEntry(ref, filepath.Join(path, entry.Name()), childRel, gitEntry, boundary, visiting)
		if err != nil || !ok {
			return ok, err
		}
	}
	return true, nil
}

func (r *Repo) reproducibleBlob(content []byte, hash plumbing.Hash, rel string) (bool, error) {
	blob, err := r.repo.BlobObject(hash)
	if err != nil {
		return false, fmt.Errorf("gitblob: read blob %s: %w", rel, err)
	}
	reader, err := blob.Reader()
	if err != nil {
		return false, fmt.Errorf("gitblob: read blob %s: %w", rel, err)
	}
	committed, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		return false, fmt.Errorf("gitblob: read blob %s: %w", rel, readErr)
	}
	if closeErr != nil {
		return false, fmt.Errorf("gitblob: close blob %s: %w", rel, closeErr)
	}
	return bytes.Equal(content, committed), nil
}
