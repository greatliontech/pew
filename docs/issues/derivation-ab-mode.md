# Derivation A/B: uncommitted-delta comparison as an owned mode

**Lands: cross-tool train chunk 44.
pew owns recorded-curve comparison (`pew stat`: working tree vs HEAD,
A/B across refs) but leans on the recordings store, and the dirty-tree
discipline rightly refuses uncommitted recordings as stat baselines.
The gap: the *derivation loop's* A/B — an uncommitted change set vs
its base ref, n≥6, sometimes `-benchmem` or a profile per side —
has no owned path. The consumer's current protocol is a hand stash
cycle inside one quiet window:

1. bench the working tree (A side);
2. `git stash push -u`, bench HEAD (B side);
3. `git stash pop`, benchstat by hand.

Observed failure modes across three sessions: the tree is mutated
mid-measurement (nothing else may edit or commit for the ~40-minute
window, so review/probe pipelines queue behind it); a script failure
between push and pop strands the change set in the stash (recoverable,
manual); block-A-then-block-B ordering folds slow machine drift
(thermal, page cache) into the measured delta.

Sketch — build both sides first, never touch the tree:

- `pew ab [--bench <pattern>] [--pkg <dir>] [--count N] [--ref HEAD]`
- A side compiles from the working tree in place; B side from the ref
  materialized in a temporary worktree (`git worktree add`, or a
  `git stash create` snapshot — no working-tree mutation either way),
  sharing the module/build cache so the second build is cheap.
- Runs interleaved (A/B/A/B, not block-A-block-B): two standing
  binaries make interleaving free, and it cancels the drift that
  block ordering cannot — statistically stronger than the stash
  cycle, not merely safer.
- Machine-prep enforced per the recording protocol (governor, turbo,
  pin, quiet); each binary runs from its own tree so cwd-sensitive
  arms (disk media, testdata) resolve correctly.
- Verdict via the existing significance machinery; output storable as
  a dirty-marked derivation artifact — never a stat baseline,
  consistent with the standing discipline.
- Crash-safe by construction: a killed run leaves a disposable
  worktree, never a stashed tree; the repo stays writable throughout.

Two composition notes: (a) per-side `-benchmem`/`-cpuprofile` capture
belongs in the same mode — profile attribution is part of the same
derivation loop pew half-owns via the profile-sanity rule (see
profile-capture-attribution.md); (b) `--ref` generalizes beyond HEAD:
the same machinery gives A/B across any two refs without the
recordings store — exactly the seam between "derivation loop" and
"artifact of record" the consumer's bench discipline already names.
