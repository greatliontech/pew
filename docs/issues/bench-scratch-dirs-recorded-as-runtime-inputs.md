# Run-created scratch paths are recorded as runtime inputs

Lands: with the next observation-strategy revision (gofresh runtimeinput classification), or
sooner if bench-store verification churn from scratch-path manifests is observed on a second
consumer

## Gap

The observation conjunction records every path the test process opened, classified against
root-based exclusions (toolchain, module cache, build cache, `os.TempDir()` as the ephemeral
root). A benchmark that creates its scratch dirs INSIDE the package directory — a deliberate
shape where the bench must measure the package medium's real fsync behavior, which the system
tempdir (tmpfs) falsifies — defeats the root-based classification: every per-iteration scratch
dir and file lands in the `pew-runtime-inputs` manifest as an input.

Field shape (tugboat `node`, first heavy consumer of the v2 evidence path): thousands of
`nodebench-*` dirs created and deleted within the run produced a ~1.1 MB manifest line. Two
consequences:

- **Store-size and reader pressure.** The manifest line grew past benchfmt's scanner bound; the
  reader now lifts oversized config lines (store.Parse), so this is contained — but the manifest
  content itself is noise at any size.
- **Verification churn.** The recorded paths are run-ephemeral (random names, deleted at
  cleanup); no future verification can ever re-observe them, so the runtime-input comparison for
  such packages degrades permanently.

## Fix direction

Classification, not exclusion: paths under the observation bracket root that were ABSENT from
the pre-run bracket fingerprint and are absent again after the run are the process's own
ephemera under the completed-process assumption (no other writer exists), and belong in the
ephemeral class beside the tempdir root. That is a gofresh `runtimeinput` change (the bracket
already carries the pre-run listing); pew's wiring would not change. A pew-side root exclusion
(e.g. excluding glob-named scratch dirs) is the weaker fallback: it trades soundness policy to
the caller and invites under-observation.
