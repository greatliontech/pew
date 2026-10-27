package main

import (
	"path/filepath"

	"github.com/greatliontech/pew/internal/gitblob"
)

func sourceInputsDirty(moduleDir, commit string, sourceFiles []string) (bool, error) {
	var c *gitStateCache
	return c.sourceInputsDirty(moduleDir, commit, sourceFiles)
}

func (c *gitStateCache) sourceInputsDirty(moduleDir, commit string, sourceFiles []string) (bool, error) {
	var repo *gitblob.Repo
	repoKey := [2]string{moduleDir, commit}
	if c != nil && c.repos != nil {
		repo = c.repos[repoKey]
	}
	if repo == nil {
		opened, err := gitblob.Open(moduleDir)
		if err != nil {
			return false, err
		}
		if snapped, err := opened.SnapshotRefs(commit); err == nil {
			opened = snapped
		}
		repo = opened
		if c != nil {
			if c.repos == nil {
				c.repos = make(map[[2]string]*gitblob.Repo)
			}
			c.repos[repoKey] = repo
		}
	}
	for _, path := range sourceFiles {
		if !pathWithin(path, repo.Root()) {
			return true, nil
		}
		fileKey := [3]string{moduleDir, commit, path}
		if c != nil && c.reproducible != nil {
			if cached, ok := c.reproducible[fileKey]; ok {
				if cached.err != nil {
					return false, cached.err
				}
				if !cached.matches {
					return true, nil
				}
				continue
			}
		}
		matches, err := repo.ReproducibleAt(commit, path)
		if c != nil {
			if c.reproducible == nil {
				c.reproducible = make(map[[3]string]reproducibleResult)
			}
			c.reproducible[fileKey] = reproducibleResult{matches: matches, err: err}
		}
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
