package main

import (
	"path/filepath"

	"github.com/greatliontech/pew/internal/gitblob"
)

func sourceInputsDirty(moduleDir, commit string, sourceFiles []string) (bool, error) {
	repo, err := gitblob.Open(moduleDir)
	if err != nil {
		return false, err
	}
	for _, path := range sourceFiles {
		if !pathWithin(path, repo.Root()) {
			return true, nil
		}
		matches, err := repo.ReproducibleAt(commit, path)
		if err != nil {
			return false, err
		}
		if !matches {
			return true, nil
		}
	}
	return false, nil
}

func pathWithin(path, root string) bool {
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && filepath.IsLocal(rel)
}
