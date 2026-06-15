// Spike 4: validate go-git covers pew's pure-reader needs — HEAD/commit/branch,
// ref resolution, blob-at-ref reads (baselines), log-by-path (trends), status.
package main

import (
	"fmt"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func firstLine(s string) string { return strings.SplitN(strings.TrimSpace(s), "\n", 2)[0] }

func main() {
	repo, err := git.PlainOpenWithOptions(".", &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		fmt.Println("open:", err)
		return
	}

	head, err := repo.Head()
	if err != nil {
		fmt.Println("head:", err)
		return
	}
	fmt.Printf("HEAD: %s\nbranch: %s\n", head.Hash(), head.Name().Short())

	c, err := repo.CommitObject(head.Hash())
	if err != nil {
		fmt.Println("commit:", err)
		return
	}
	fmt.Printf("commit-time: %s\nmsg: %s\n",
		c.Committer.When.Format("2006-01-02T15:04:05Z07:00"), firstLine(c.Message))

	// blob-at-ref read (baseline materialization)
	if f, err := c.File("docs/spec.md"); err != nil {
		fmt.Println("file@HEAD:", err)
	} else {
		lines, _ := f.Lines()
		fmt.Printf("docs/spec.md @HEAD: %d lines, %d bytes\n", len(lines), f.Size)
	}

	// revision resolution (HEAD, tags, short shas)
	if h, err := repo.ResolveRevision(plumbing.Revision("HEAD")); err != nil {
		fmt.Println("resolve:", err)
	} else {
		fmt.Println("resolve(HEAD) ->", h)
	}

	// log scoped to a path (trend/series)
	path := "docs/spec.md"
	if it, err := repo.Log(&git.LogOptions{FileName: &path}); err != nil {
		fmt.Println("log:", err)
	} else {
		n := 0
		_ = it.ForEach(func(cm *object.Commit) error { n++; return nil })
		fmt.Printf("log for %s: %d commit(s)\n", path, n)
	}

	// worktree status (the dirty flag)
	if wt, err := repo.Worktree(); err != nil {
		fmt.Println("worktree:", err)
	} else if st, err := wt.Status(); err != nil {
		fmt.Println("status:", err)
	} else {
		fmt.Printf("clean: %v  (changed entries: %d)\n", st.IsClean(), len(st))
	}
}
