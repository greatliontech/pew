# ab worktree placement: operator escape and crash-residue sweep

## Problem

`pew ab` creates side B's worktree as a hidden sibling of the
repository and hard-errors when that placement is unavailable —
unwritable parent (home-directory repos whose parent is root-owned
`/home`, container repos with a read-only parent) or a parent on a
different filesystem device (repo at a mount boundary). The hard
error is the correct default: a silent fallback to another medium
recreates the invalid experiment the placement exists to prevent
(side B fsyncing tmpfs while side A pays the disk). But it leaves
those configurations with no way to run `pew ab` at all.

Secondary: a killed run now leaves durable residue — a
`.pew-ab-worktree-*` directory beside the repository plus a stale
`.git/worktrees` entry — where the old OS-temp placement's residue
self-expired with the host. If the repository's parent is itself
under version control or a sync tool, crashed-run residue becomes
visible pollution.

## Shape

- `--worktree-dir <path>`: operator-chosen side-B placement,
  validated by the same device-identity check (`sameDevice`) before
  use — the escape restores usability without reopening the
  different-medium hole. An operator with a same-device scratch
  location names it; one without cannot silently degrade.
- Startup sweep: on `ab` start, remove stale `.pew-ab-worktree-*`
  siblings not listed by `git worktree list` (the same cross-check
  `git worktree prune` trusts), so crash residue self-heals on the
  next run instead of accumulating.

Lands: when a pew ab consumer first hits the unwritable-parent or
mount-boundary refusal in practice, or with the next ab-surface
train chunk, whichever first.
