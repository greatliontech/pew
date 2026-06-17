# Dirty Flag And Runtime Inputs

Lands: when changing dirty baseline checks or runtime-input baseline semantics

## Fault

The `dirty` flag is derived from go-git's worktree status. Ignored or untracked files that are read as
runtime inputs can affect a benchmark while the git tree appears clean or while the commit does not
contain the input content. The runtime manifest records a digest, but a ref-based pinned baseline then
describes code at a commit plus local input bytes that may not be reconstructable from git.

Concrete risk path:

- `.gitignore` excludes `fixture.dat`.
- A benchmark reads `fixture.dat` and records `pew-runtime` for its current bytes.
- The recording is committed with `dirty: false` because git status does not consider the ignored
  input dirty.
- Later `pew stat v1` accepts the recording as a pinned baseline, although the measured input is not
  represented by `v1`.

## Reconciliation

Define the trust model for runtime inputs in pinned baselines:

- mark recordings dirty when a runtime manifest includes ignored/untracked module-local inputs, or
- treat pinned baselines with module-local runtime inputs as usable only when the input path/content is
  present at that ref, or
- explicitly classify such recordings as valid for working-tree reuse but not for ref-pinned baseline
  comparisons.
