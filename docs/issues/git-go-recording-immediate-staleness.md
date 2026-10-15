# git-go recording is stale at the first status inspection

Lands: Gofresh docs/plans/tooling-redesign.md 7.7.

The reader-facing explanation account is a related obligation at 8.3.

## Remaining evidence gap

The matching inspected producer/checker identities in the repeated report show
no known binary-version transition, but the observations below do not retain
both complete compilation environments. Their buildconfig digests cannot
identify the differing input. The retained reduced-case account reports that
a real capture under one executable and one environment stayed current;
changing an otherwise unobserved compiler-environment variable produced the
same `stale (buildconfig)` class. This is not a new verification of the original
run's cause. Compiler wrappers can consume such variables, so dropping them
from the native build description would be unsound.

The diagnostic executable lifetime had a separate concrete cause: the profiler
observed its producing executable and temporary-directory cleanup removed it.
Commit `7683672` implements the retained-executable rule in spec §6.2, with
source pins in `cmd/pew/profile_executable_test.go` and
`internal/store/executable_test.go`. This completed half preserves the dependency
without an exclusion or a grant of outcome support; it is not another pending
producer migration. Historical temporary-path evidence is not rewritten.
The original build-environment cause remains unproved. The remaining work is
the build-input attribution account: record enough safe diagnostic provenance
to distinguish a changed environment
from a producer/checker discrepancy without disclosing environment values or
asserting the cause from guard digests alone. Ordinary unsupported outcomes
and diagnostic identity-only support remain independent, explicit judgments.

## Historical observation

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

The retained-executable rule in §6.2 addresses producer-owned artifact lifetime.
A benchmark or profiler that reads its executable retains that dependency;
historical observations of a deleted temporary path remain stale and require
fresh capture, never a rewritten manifest.

Explain the ordinary measurement's unsupported outcome separately from profile
identity-only support. Use reduced tool-owned regressions with equivalent
consumer workload shapes; another broad consumer measurement is not needed to
file or begin diagnosis. Keep missing/unknown conditions and incomplete evidence
visible rather than replacing them with assertions or a fabricated green verdict.

## Historical repeated observation with matching inspected build identities

The next required git-go evidence run used clean source `9029aa2` and Pew
`v0.9.0`, clean revision `c45212102991752486a19265f5d9c12614cef23e`, built
with `go1.27.2-dst.15` and Gofresh `v0.116.3`. That same executable build
identity was inspected before measurement and after status inspection. This
reduces the earlier installation ambiguity; it is not a claim that a binary
handle was frozen throughout the interval.

```sh
mlock quiet "git-go hashing and affected value evidence" task bench:values bench:hash
mlock run task bench:status -- --json
```

Both benchmark tasks measured required new/stale work rather than replaying a
consumer solely to diagnose the tool: the value subject was stale on closure
after hashing was added, and the hashing subject was unrecorded. The result
contains fourteen value and twelve hashing children, ten samples each, with
four nonempty profiles.

The first status again reports both measurements stale on `buildconfig`; each
allocation profile is stale on `buildconfig`, and each CPU profile is stale on
its removed producer executable:

- value CPU: `/tmp/pew-profile-1977399094/testbin`;
- hashing CPU: `/tmp/pew-profile-1521022194/testbin`.

All four profile objects report verified integrity, unverified relation and
identity-only outcome. Ordinary measurements still report operation-outcome
support unavailable. The new native objects have identities
`5579b6084233ba23eab514a7dfa2511ac19873fad7cd0ffe94d5e90e1e56151e`,
`599f06c8b802952b83c88095361af3242bc02c065bb571349166cec00452c3b1`,
`df8a9b0230ec51f9552b4433c07ec1ba19ac32672a806e8716aa0534e3854573`, and
`e950102dadac184f8d3b446fe84f3b436a84a919344b9d2b65832c091db70dc3`.

With explicit complete/require/compatible comparison policies, `stat` produced
42 numerical comparisons for existing value metrics but zero eligible metrics.
Freshness and unknown throttled/battery observations prevent an enforcing pass;
hashing has no predecessor and is correctly reported separately as missing-side
comparison evidence. The consumer does not treat these observations as a passing
regression judgment or change the guard policy to admit them.
