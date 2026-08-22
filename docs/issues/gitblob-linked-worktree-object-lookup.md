# gitblob fails in linked worktrees — "worktree status: object not found"

Observed 2026-08-22 (tugboat, chunk-2 regression attribution): a
`pew run` inside a `git worktree add --detach` checkout failed the
whole package with

    error  github.com/greatliontech/tugboat/node  (gitblob: worktree status: object not found)

while the identical invocation in a plain clone of the same commit
records cleanly. The gitblob/worktree-status layer evidently resolves
git metadata against the checkout path directly and breaks on the
linked-worktree layout (`.git` as a file pointing at
`<main>/.git/worktrees/<name>`, refs and objects living in the main
repository's directory).

Why it matters: recording a baseline at an older ref for an A/B is
exactly the worktree use case (a clone is the workaround, at the cost
of a full checkout copy), and pew's own ab machinery places worktrees
(see ab-worktree-placement-escape.md — this may be one shared
resolution defect with that issue's neighborhood).

Fix: resolve the git common directory properly (gitdir indirection +
commondir, or rev-parse --git-common-dir semantics) in the gitblob
layer; a regression test runs a recording from a linked worktree.

Lands: user decision.
