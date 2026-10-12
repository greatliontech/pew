# git-go recording is stale at the first status inspection

Lands: Gofresh docs/plans/tooling-redesign.md 5.1u for fixed-identity diagnosis,
with measurement attribution at 7.7 and the explanation account at 8.3.

## Observation

On the clean git-go commit `aa61bc6f7aab38f0904da9cd7acbadb979cab01f`, one
native Pew run recorded all fourteen `BenchmarkIDValues` children, ten samples
each, plus nonempty CPU and allocation profiles. No consumer code changed
between recording and the first status inspection; only the native recording
store appeared as untracked output.

```sh
mlock quiet "git-go object-value baseline and profiles" \
  pew run --bench='^BenchmarkIDValues$' --count=10 --benchtime=1s \
  --profile=cpu,alloc --profile-benchtime=1s ./object
mlock run pew status --explain ./object
```

The recording names that clean commit, `dirty: false`, format 6, and producing
toolchain `go1.27.2-dst.15 linux/amd64`. Run conditions were
`governor=performance turbo=off load1=3.96 throttled=unknown battery=unknown`.
The status inspection reported:

- Measurement `stale (buildconfig)`, with recorded guard beginning
  `17b61e6bf76d1ee50b0e22f3` and current guard beginning
  `dc25c0bb213712034f2cd99d`.
- Toolchain, machine, runtime configuration, dynamic-state strategy, closure,
  producing/test-variant ledgers, purity attribution and runtime digest matched.
- Allocation profile: `integrity=verified freshness=stale relation=unverified
  outcome=identity-only empty=false`, naming `buildconfig`.
- CPU profile: the same integrity/relation/outcome/empty states, with staleness
  naming `runtimeinputs (moved: path /tmp/pew-profile-3254321371/testbin)`.

The run also reported measurement outcome support unavailable for the benchmark.
The recorded observation reason is `startup effect: reaches testing.Run (test
runtime execution)`, strategy `gofresh/observation-rta@44`. No source purity
directive or dynamic-state vouch was supplied by the consumer. Diagnostic
identity-only support is separately expected by the profiling model; it is not
itself the immediate-staleness complaint.

## Provenance boundary

The installed executable inspected **after** these calls was clean revision
`a22c3333c987c53b66dbd263e7bd4bbaa96f483f`
(`v0.8.6-0.20261012170319-a22c3333c987`), using Gofresh `v0.116.3`, built with
`go1.27.2-dst.15`. The tooling owner installs updates on this shared machine.
No separate executable snapshot was taken immediately before the measuring
call, so this report does not assume that every call used that same Pew binary.
Rule out an intervening producer/checker version change before diagnosing the
build-config mismatch as a same-version defect.

The retained consumer artifacts are:

- `benchmarks/object/BenchmarkIDValues.txt`, including both diagnostic indexes
  and their source snapshots.
- Profile object `96895effb5248839bff121a5492124bb47fc2ac04d6ec572b0224b1770d9cce2`.
- Profile object `2c7a0afc12ff7346c9dd8d7485f572f603bfa0bcf6342e3d410fef0f283a64f0`.

Objects live under the store's `.profiles/sha256`. The producing source is the
named commit; subsequent source changes are not part of this observation. The
consumer retains these as measured observations, without claiming a complete
freshness-enforcing acceptance or a regression comparison.

## Required derivation

With a fixed identified producer/checker pair, derive which build-description
field differs across run, profile attachment and status. Preserve actual
compiler/build/runtime/PGO semantics; do not remove a guard simply to make this
record valid. If an installed-version change explains the refusal, report the
appropriate compatibility boundary and needed remeasurement explicitly.

Separately determine whether the recorded temporary profile executable is an
actual external input or a producer-owned artifact whose identity/retention
belongs to the capture. A successful capture's normal scratch cleanup should
not silently invalidate otherwise reusable evidence, but a benchmark that
actually reads its executable must retain that dependency correctly.

Explain the ordinary measurement's unsupported outcome separately from profile
identity-only support. Use reduced tool-owned regressions with equivalent
consumer workload shapes; another broad consumer measurement is not needed to
file or begin diagnosis. Keep missing/unknown conditions and incomplete evidence
visible rather than replacing them with assertions or a fabricated green verdict.
