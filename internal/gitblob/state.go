package gitblob

import "fmt"

// State reports the HEAD commit and worktree-dirty flag for the repository
// containing dir. Commit and dirty are recorded in-band with each recording
// (spec §5): the commit names the code measured — not the recording's later git
// position (§6.1) — and dirty marks a tree whose recording cannot serve as a
// baseline (§10). Neither is a validity guard: freshness is commit-independent.
// Worktree status is the documented slow path on large repos (§11).
func State(dir string) (commit string, dirty bool, err error) {
	r, err := Open(dir)
	if err != nil {
		return "", false, err
	}
	head, err := r.repo.Head()
	if err != nil {
		return "", false, fmt.Errorf("gitblob: resolve HEAD: %w", err)
	}
	wt, err := r.repo.Worktree()
	if err != nil {
		return "", false, fmt.Errorf("gitblob: worktree: %w", err)
	}
	st, err := wt.Status()
	if err != nil {
		return "", false, fmt.Errorf("gitblob: worktree status: %w", err)
	}
	return head.Hash().String(), !st.IsClean(), nil
}
