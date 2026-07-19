# pew — Go benchmark provenance & staleness manager

**Status:** draft (iterating). Authoritative requirements contract. Code conforms to this
spec, not the reverse.

**Canonical external spec:** the Go benchmark output format —
[golang.org/design/14313-benchmark-format](https://go.dev/design/14313-benchmark-format)
and the `testing` package's benchmark output contract. Stored artifacts are valid files of
that format; pew never invents a competing format for the result data itself.

---

## 1. Problem

Go benchmarks are expensive to run, and a result is only meaningful relative to **the code it
exercised**, **the toolchain that built it**, and **the machine it ran on**. Today the workflow
is manual: run `go test -bench`, paste the text into a file in the repo, eyeball `benchstat`
output for regressions, and *guess* whether a saved result is still trustworthy or needs a
re-run. Two failure modes recur:

- **Wasted runs** — re-running a benchmark whose exercised code, toolchain, and machine are all
  unchanged. The old result is still valid; the run was money burned.
- **Silent staleness** — trusting a saved result after the code it exercises changed. The
  comparison is now lying, and a regression can hide behind a stale baseline.

pew streamlines run → store → compare and, crucially, answers **"is this saved result still
valid for HEAD, or must I re-run it?"** mechanically rather than by guesswork.

## 2. Goals

- **G1 — Provenance.** Every stored result records enough to decide its own validity later:
  commit, toolchain, machine fingerprint, build configuration, and runtime-input evidence or an
  explicit incomplete-observation disposition.
- **G2 — Staleness.** For any stored result, decide *valid* (re-use) vs *stale* (re-run) for
  the current tree, with **no false "valid" within pew's specified guard model**: source closure,
  runtime-input evidence, toolchain, machine, and build configuration. Class-B external-dependence
  detection has the documented best-effort boundary in §7.3; known external state outside that
  detection is declared with `--impure`.
- **G3 — Run hygiene.** Drive `go test -bench` with statistics-grade defaults (multiple counts,
  fixed benchtime, optional CPU pinning, environment quiesce checks).
- **G4 — Comparison.** Detect regressions with proper statistics across three baseline modes
  (auto / pinned / on-demand), re-using benchstat's math, not re-implementing it.
- **G5 — Ecosystem compatibility.** Stored files remain readable by plain `benchstat` and any
  `benchfmt` consumer. No lock-in.

## 3. Non-goals

- **Multi-machine result federation.** Primary target is solo, single machine. Machine identity
  is a *drift guard* (detect "you changed machines / config"), not a cross-machine normalization
  or a results server. (Forward-compatible, not built now.)
- **A benchmark results web UI / upload server.** Out of scope.
- **Perfect dynamic-coverage staleness.** Static analysis cannot see all runtime behavior
  (data files read at runtime, env-driven branches). pew's no-false-`valid` guarantee applies to
  the specified source/provenance/runtime-input guard model: bounded source blind spots are
  *widened*, and recognized unbounded external dependence is marked `unverifiable` (§7.3), but
  Class-B detection is explicitly best-effort for exotic unrecognized external state. See §7.
- **Replacing the Go toolchain.** pew orchestrates `go test`/`go list`; it does not compile.

## 4. Vocabulary

- **Benchmark `B`** — a top-level `BenchmarkXxx` function. Sub-benchmarks (`b.Run`, table cases)
  share `B`'s source closure; they are distinct *result rows* but one staleness unit.
- **Result** — one stored measurement set for `B`: the benchmark-format lines plus provenance.
- **Sample** — one `-count` iteration's measurement; statistics need several per result.
- **Provenance** — facts about *how a result was produced*: commit, toolchain, machine,
  build config. Source-of-truth; not recomputable (you can't re-derive "what commit was this").
- **Closure** — the set of source that can affect `B`'s runtime performance, reachable by
   static analysis from `B`. Hashed to detect code change. *Derived* from (commit, build-config),
   not provenance.
- **Runtime input** — a non-source input that the benchmark process actually observed during the
  recorded run, such as an environment variable or local file opened via Go's testlog-observable
  runtime paths. Hashed separately from the source closure.
- **Machine fingerprint** — a stable id over the hardware/OS facts that affect benchmark timing.
- **Baseline** — the result set a fresh run is compared against to detect regression (§9).

## 5. Stored artifact format

A stored result is a **canonical Go benchmark-format file** (§1 canonical spec). Provenance
rides in-band as the format's own `key: value` **configuration lines** — the format's sanctioned
extension point — so files stay self-describing and `benchstat`-readable.

Toolchain already emits `goos`, `goarch`, `pkg`, `cpu`. pew adds, as global config lines —
uniform per run except `buildconfig`, whose value is per-package wherever the applicable PGO
profile differs between packages (§9):

| key                  | meaning                                                       | source-of-truth? |
|----------------------|---------------------------------------------------------------|------------------|
| `pew-format`         | exact Pew recording format version, currently `1`            | yes              |
| `commit`             | full SHA of HEAD at run time                                  | yes              |
| `toolchain`          | `go version` output (compiler/runtime identity)               | yes              |
| `machine`            | machine fingerprint id (§8)                                   | yes              |
| `buildconfig`        | digest of build tags + relevant GOFLAGS/gcflags + cgo + PGO profile **content** | yes |
| `runtimeconfig`      | digest of Go runtime-config env (GOGC/GODEBUG/GOMEMLIMIT/GOMAXPROCS), §7 | yes  |
| `dirty`              | `true` if the working tree had uncommitted changes at run    | yes              |
| `pew-runconditions`  | observed transient run conditions at run time (§9)           | yes              |
| `pew-runtime`        | digest of runtime-input evidence (§7.8)                      | derived          |
| `pew-runtime-inputs` | encoded runtime-input manifest or incomplete disposition (§7.8) | yes            |
| `pew-purity`         | attributable Gofresh purity evidence used for this fingerprint | yes            |

`pew-format` occurs exactly once as the byte-exact LF-terminated line `pew-format: 1`. A recording
with no discriminator, a duplicate, alternate whitespace or line endings, or another value is
`stale (format)` and MUST be regenerated; Pew never interprets it as an earlier shape. Benchmark
output that attempts to define `pew-*` or any other Pew-owned provenance, guard, or purity key is
refused before storage. A format-1 recording missing any mandatory field is likewise `stale (format)`
before guard or purity interpretation. Duplicate rejection applies to every recording key — the
table above plus `pew-closure` and the per-benchmark `pure` line (§7.5) — not only `pew-format`:
a recording that repeats any of them is `stale (format)`.
Format governs interpretation rather than measurement identity and is projected from comparisons.

**The recording key set is closed** (INV-12). Stream-derived configuration keys other than the four
the toolchain itself emits (`goos`, `goarch`, `pkg`, `cpu`) are dropped before storage, each
distinct dropped key reported with a warning naming the key and its first observed value. The
benchmark-format reader treats any stdout line whose first word is lowercase, space-free, and
colon-terminated as configuration, so a dependency's logger (`raft: appending entries`) would
otherwise record transient log text as durable configuration and fragment comparison grouping
(§10.1) — a benchmark silently falling out of comparison because a dependency logged once.
Deliberately emitted custom benchmark configuration is therefore **not** a supported recording
input; supporting it is a spec change, not a pass-through. `pew run` stores recordings whose
configuration keys are drawn only from the closed set: the toolchain's four, the §5
provenance/guard keys, `pew-closure`, and the per-benchmark `pure` line. This is a producer
contract — read paths do not police historical recordings for foreign keys.
`pew-purity` is benchmark-specific despite the surrounding uniform provenance keys and is omitted
when capture used no purity assertion; omission is the canonical no-attribution encoding.

**The closure hash rides in-band** as a namespaced `pew-closure` config line (no sidecar index). It
is *derived*, not provenance (recomputable from commit + toolchain + build-config), so it is never
the source of truth and recomputing it never changes a verdict (INV-5). In-band is sound because
overwrite makes each file single-block, so the key cannot fragment a benchstat projection; pew
additionally strips `pew-*` keys from its own comparison projections (§10). The `.txt` is therefore
fully self-describing — everything needed to evaluate its own validity lives in one file.

Runtime-input evidence uses the same in-band rule: `pew-runtime-inputs` is the recorded manifest and
`pew-runtime` is its digest. Format-1 recordings carry a canonical incomplete disposition rather than
observed identities (§7.8).

A `dirty` run is recorded but flagged: its `commit` does not faithfully describe its source, so
its closure is computed from the *working tree*, and it is never usable as a pinned baseline.

### 5.1 Identity vs validity

Two distinct questions use two distinct keys over a record's fields, and conflating them is the
classic trap: putting the commit sha into the *validity* test makes every commit invalidate every
benchmark — collapsing pew back to "re-run everything every commit" and rendering the closure
analysis dead weight.

- **Identity** — *which* recording is this? The full provenance tuple
  `(benchmark, commit, toolchain, machine, buildconfig, runtimeconfig)`. Labels the in-band
  provenance and lets a fresh run recognize whether it is the *same point* as a prior recording (same
  tuple) or a new one. The per-commit history of identities lives in git (§6.1), not an in-file log.
- **Validity** — is a stored measurement still usable for HEAD *without re-running*? The predicate
  of §7: `closure ∧ runtime-inputs ∧ toolchain ∧ machine ∧ buildconfig ∧ runtimeconfig`, each
  compared HEAD/current-vs-record.

The two keys differ in which facts answer each question: commit identifies the recording point, while
closure and runtime-input evidence prove reuse validity:

| term         | identity (which record) | validity (reuse for HEAD) |
|--------------|:-----------------------:|:-------------------------:|
| commit sha   |           ✓             |        ✗ (excluded)       |
| closure hash |           ✗             |             ✓             |
| runtime inputs |         ✗             |             ✓             |
| toolchain    |           ✓             |             ✓             |
| machine      |           ✓             |             ✓             |
| buildconfig  |           ✓             |             ✓             |
| runtimeconfig |          ✓             |             ✓             |

Run conditions (`pew-runconditions`, §9) appear in **neither** key: they neither name a recording
point nor gate reuse. They are audit provenance only — recorded so "under what machine conditions
was this number taken" is answerable from the recording alone — and surface solely as a
comparison note (§10.1). Putting them in identity or validity would recreate the §8 spurious-stale
trap (the governor at *check* time mismatching the governor at *run* time).

Identity **pins** code by commit; validity **tests** code by closure plus runtime-input evidence. The
commit is a coarse "some code somewhere changed" signal — correct for *naming a point in history*,
fatally over-broad for *deciding a re-run*. The closure is the precise "code this benchmark exercises
changed" signal, and runtime inputs are the precise "observed non-source input changed" signal.
Swapping commit for closure in the validity predicate is the classic trap closure analysis exists to
avoid (see INV-6).

## 6. Storage layout

```
<bench-dir>/                      # default: ./benchmarks, configurable
  <pkg-path>/                     # mirrors the package import path under module root
    <BenchmarkName>[.<label>].txt # one file per top-level benchmark (+ optional variant label)
```

- **File key = `(package import path, top-level benchmark function)`.** So the file count is simply
  the number of `BenchmarkXxx` functions you have run and stored — *not* fanned out per sub-benchmark
  (`b.Run` rows share `B`'s closure → same file), per `-cpu`/`GOMAXPROCS` suffix, per commit (history
  is git, §6.1), or per machine (single-machine; the fingerprint rides in-band). Per-function (not
  per-package) keeps the staleness unit and the storage unit aligned: re-running one stale benchmark
  touches exactly one file, and `pew status` is a directory listing. The `go test` stream is demuxed
  into per-function files via `benchfmt` (each result carries its `.Name`).
- Each `pew run` **overwrites** the file with that run's latest result block — provenance config
  lines (§5) plus sample lines. The file holds only the most recent recording; **history is git's
  job** (§6.1). Keeps files small, makes commit-to-commit diffs show the actual perf delta, and — per
  the single-source-of-truth discipline — avoids storing history twice (git *and* an in-file log)
  where they could diverge under rebase or hand-edit. To add samples, raise `--count` (an explicit
  same-identity sample *merge* is a deferred option, tracked in `docs/issues/`).
- **History axis vs parallel-variant axes.** The path encodes only the history-invariant identity
  `(pkg, benchmark)`; the rest of the identity tuple (§5.1) rides in-band. *Commit* is a **history**
  axis → git holds prior values, the file keeps the latest. *Toolchain / machine / buildconfig* are
  **parallel-variant** axes: if you deliberately benchmark more than one (cgo on/off, a feature build
  tag, two Go versions) and want them retained side by side, `--label <name>` (§12, CLI surface) adds the
  filename discriminator (`BenchmarkFoo.cgo.txt`); without it the newer variant overwrites the older.
  The toolchain/machine/buildconfig guards (§7, §10) still prevent any silent *cross-variant comparison* either way — so omitting a
  label is a retention choice, never a correctness one.
- **No sidecar index.** The only derived datum — the closure hash — rides in-band as `pew-closure`
  (§5), so each benchmark's `.txt` is self-describing and there is no second artifact to keep in
  sync. The recorded side of the validity check is thus in-band; `pew status` recomputes only the
  HEAD closure. If repeated `status` on an unchanged tree ever proves slow, a *gitignored* memo can
  be added then — not now.
- **Path confinement.** Store operations never follow symlink directory components under
  `<bench-dir>` and operate only on regular recording files. A lexical recording path must not resolve
  outside the store through filesystem topology; reads and removals use the same boundary as writes.

### 6.1 History lives in git

Result files are committed artifacts, so their history *is* the git history of those files — pew
does not re-implement it in-band. One wrinkle makes in-band provenance (§5) non-optional, though:

**The result file lands in a *child* commit of the code it measures.** You benchmark code at commit
`C`; the fresh `.txt` is then committed as `C'` (child of `C`). The result *describes* `C` but
*lives in* `C'`, so `git show C:Foo.txt` does **not** contain `C`'s result — `git show C':Foo.txt`
does, and its in-band `commit: C` line is what maps the recording back to the code it measured.

So the two axes are distinct and both needed: git history gives the *sequence of recordings*; the
in-band `commit` field gives *which code each recording measured*. Baselines (§9) therefore resolve
a recording by **git ref**, then read its measured commit from the file's `commit` line. No in-file
log is required, and provenance-in-band is mandatory regardless of append-vs-overwrite.

## 7. Staleness contract

A stored result `R` for benchmark `B` gets one of **three verdicts** for HEAD, and the governing
rule is **`valid` requires proof**. `unrecorded` is a `status` inventory state for a missing stored
result, not a verdict about an existing result.

Exact format validation is a prerequisite to the six-guard predicate below. A format failure is
`stale (format)` without interpreting any guard or purity field.

- **valid** (reuse `R`) — all six guards below provably hold over a soundly over-approximated
  closure, and either neither the closure nor runtime-input manifest carries an unverifiable
  disposition or an applicable purity assertion explicitly overrides those dispositions (§7.5).
- **stale** (re-run) — some guard demonstrably fails: closure, runtime input, toolchain, machine,
   build config, or runtime config changed.
- **unverifiable** (re-run, reason recorded) — guards would pass, but `B`'s closure reaches an
  external dependence pew cannot hash (Class B, §7.3), or the runtime-input manifest carries an
  unverifiable disposition such as explicit incomplete outcome evidence. Operationally a re-run,
  but distinct from `stale`: the user can assert purity to override (§7.5). Absence of proof never
  collapses to `valid` (INV-1).

The six guards:

1. **Closure** — `closure-hash(B, HEAD) == closure-hash(B, R.commit)`
2. **Runtime inputs** — recomputed digest from `R.pew-runtime-inputs` == `R.pew-runtime` (§7.8)
3. **Toolchain** — current `go version` == `R.toolchain`
4. **Machine** — current fingerprint == `R.machine`
5. **Build config** — current digest == `R.buildconfig`. The digest covers every build-affecting
   input that can change generated code without moving the toolchain, machine, or source guards:
   the codegen **feature level** (`GOAMD64`/`GOARM`/`GOARM64`/`GO386`, `GOEXPERIMENT`), the **cgo
   toolchain environment** (`CGO_ENABLED`, the `CGO_*FLAGS`, `CC`/`CXX`, the `PKG_CONFIG*`
   variables), **build flags** (`GOFLAGS` plus any build-affecting CLI pass-through), and **PGO
   profile content**, and the **target platform** `GOOS`/`GOARCH`. The target platform lives here —
   not in the machine fingerprint (§8) — because it is code-determining: a cross-compile changes the
   binary and thus any result, so it must be checked even by a consumer that applies only the code
   guards. A build-affecting input pew cannot parse or bound **fails closed** (the
   recording is refused) rather than digesting to a value that could stay stable across different
   generated code.
6. **Runtime config** — current digest == `R.runtimeconfig`. A digest of the Go runtime-configuration
   environment the benchmark process inherits — `GOGC`, `GODEBUG`, `GOMEMLIMIT`, `GOMAXPROCS` — read
   by the runtime during init, *before* the testlog stream starts, so they are invisible to the
   runtime-input guard (§7.8) and are transient, so deliberately excluded from the machine fingerprint
   (§8). A change (e.g. `GOGC=off`, a `GODEBUG` setting) can materially move allocation/scheduling
   behavior with no other guard moving, so it is captured here as a distinct guard. Values are digested,
   not stored in clear text (§7.8). Only variables explicitly set in the environment are captured — an
   unset `GOMAXPROCS` defaults to the machine's CPU count, already covered by the machine guard (§8).

Guards 3–6 are exact-equality on recorded provenance — cheap and unambiguous. Guards 1–2 are derived
digests over source and runtime-input evidence; the rest of this section defines their soundness.

### 7.1 What the closure covers

The closure of `B` is the set of **source declarations** whose change could alter `B`'s runtime
behavior. Tier 2 starts from every source execution root whose effects can reach `B`, then walks two
relations, unioned:

- **Roots** — `B`'s body, plus linked package `init` functions and package-level var initializers
  whose startup side effects can affect state `B` observes. The sound default is all linked package
  startup roots; a tighter subset is allowed only when the analysis proves the omitted startup state
  cannot flow to `B`.
- **Call graph** — functions/methods those roots transitively invoke (`go/ssa` + RTA/VTA, §7.4).
- **Reference graph** — the `const`s, types, and package-level vars those functions name. A body can
  be byte-identical while a referenced `const BufSize = 4096` flips to `8192`; hashing call edges
  alone misses it, so referenced declarations are in the closure too (transitively for types/consts).

Plus, for every package contributing a reached declaration or startup root: any **`go:embed`-ed
files** read through that declaration/root (benchmark input that changes behavior with no source
change). Startup side effects are part of the source closure even when `B` never names their package:
registry patterns (`database/sql` drivers, image decoders, codecs) can allocate and register a
concrete type in `init`, then `B` observes it later through package-level state and interface
dispatch.

**Scope cut — the standard library is excluded from the hash.** stdlib + runtime change *iff* the
toolchain changes; hashing thousands of constant-per-toolchain std files is
redundant and slow. The call graph is still **traversed through std** (so callbacks into your code —
a user `MarshalJSON` invoked by `encoding/json` — stay reachable), but std declarations contribute
nothing to the hash. Module **dependencies are included** (a `go.mod` bump changes their content,
which the toolchain guard does *not* cover). `go list -json` distinguishes them: `Standard: true` → excluded;
everything else → hashed.

**System C headers are build environment, not hashed.** A cgo package's `#include` of a
toolchain/system header (`<stdio.h>`, an `openssl` header) that the C compiler finds on its
**default search path** — one pew does not resolve under the package or a module-cache dependency —
is cut like stdlib: covered by the toolchain and machine guards within pew's single-machine scope
(§3, §8), not hashed. This is a **weaker** cut than the Go stdlib one — C system headers are not
Go-toolchain-versioned, so a libc/system-header update between record and status is caught only
insofar as it moves the machine fingerprint (OS/kernel version, §8), and cross-machine reuse is
already barred (§10); hashing per-machine-varying system paths is impractical. **In-tree C headers
are still hashed:** an include resolving under the package dir rides the package file-set hash. A
`-I` root — or an include reached via `..`/an absolute path — that leaves the package dir is safe
only when it lands under the **module cache** (a version-pinned dependency, §7.7); anything else — an
in-module sibling, a local `replace`/`go.work` sibling module, or a directory pew cannot prove is a
pinned dependency — is mutable first-party source and **fails closed**. A genuine system `-I` root
is indistinguishable from a mutable local one, so it fails closed too; only the default-search
(not-found) system header is skipped. An opaque macro/computed `#include`, whose expansion could
reach in-tree source, also fails closed.

### 7.2 Tiers — same model, Tier 1 is the sound floor

- **Tier 1 (package closure):** hash the resolved file sets (`GoFiles`/`CgoFiles`/`SFiles`/
  `EmbedFiles`) of `B`'s package and all transitive **non-std** dependency packages from
  `go list -json`. Over-approximates; never false-`valid`; no SSA.
- **Tier 2 (declaration closure):** §7.1 narrows the hash from whole-package to the declaration set
  reached from the sound root set, including startup/global side effects. It may **shrink** the
  hashed set relative to Tier 1 only where shrinking is proven safe; otherwise it widens to Tier 1 and
  never makes a stale result look valid.

**Tier 1 over the whole non-std build is the maximal sound closure** — and the single fallback every
blind spot escalates to (§7.3). Tier 2 + the resolution rules are pure precision: they shrink the
set only where shrinking is provably safe. INV-1 therefore holds *by construction* — the worst case
is always the maximal source set, never less.
Pew records this maximal closure by default. Declaration refinement is admissible only as an explicit
precision policy after it demonstrates bounded completion and useful false-stale recovery; a recording
never infers or silently selects it from benchmark cost.

### 7.3 Blind spots — resolve, widen, or downgrade

A blind spot is a runtime-reachable code path the static graph does not see. Each gets exactly one
disposition, chosen to never under-cover:

**(A) Resolvable — add the precise edge, no widening.** The missing edge has a statically known
target read directly:

| construct | how it's resolved |
|-----------|-------------------|
| `//go:linkname a b` | target `b` is named in the directive → add edge to `b` (std target → toolchain-guard-covered, ignore; non-std → include normally) |
| Go-asm functions | always hash the `.s` (it is in the file set). Almost all asm is a **leaf** (no Go call-outs) ⇒ no missing callees ⇒ no widening — hashing the `.s` already catches any change. A cheap scan for Go-symbol call-refs (`·name(SB)`, `pkg·name(SB)`) resolves the rare call-out; only a *computed* call falls to A′. |
| generics | built with `ssa.InstantiateGenerics` (§7.4) → every instantiation in the build is materialized and dispatched concretely; not a blind spot |

**(A′) Bounded-but-unresolved — widen to the maximal sound closure (§7.2).** The construct could
reach code we cannot enumerate, but the target is somewhere in the analyzed source:

| construct | why unresolvable |
|-----------|------------------|
| `reflect` dispatch in non-std code (`Value.Call`, `MethodByName().Call`, `MakeFunc`) | target chosen from runtime data |
| `unsafe` function-pointer conversions / computed calls | escapes the type/call model |
| asm with a computed `CALL` (no parseable symbol) | opaque control flow |
| a non-std type converted to an interface (incl. `any`) reachable from `B` | un-analyzable code (e.g. reflection inside an opaque callee) may dispatch *any* of its methods → all its methods are added |
| startup/global side-effect flow not proven complete | linked `init`/var-init code may mutate package-level state that `B` later reads or dispatches through without naming the registering package |

These escalate to hashing the **entire non-std build closure** (Tier 1 maximal). Tighter bounds
(widen-to-package/module) are taken *only* when provably sound; the default is maximal. Because
reflection/asm/unsafe *inside stdlib* are below the §7.1 cut, A′ fires only on such constructs in
**your code or a dependency** — rare, so precision stays high.

**(B) External dependence — observe, or verdict `unverifiable`.** Behavior depends on state that is
not source at all, so no source widening can bound it:

| construct | external state |
|-----------|----------------|
| file I/O on a non-embedded path (`os.Open`, `os.ReadFile`, …) | requires the complete observation conjunction in §7.8; Pew's current producer records incompleteness, so it remains `unverifiable` |
| network I/O | a remote that may change |
| `plugin.Open` / `plugin.Lookup` | code loaded from an arbitrary `.so` at runtime |
| cgo linked against an external library (`#cgo LDFLAGS: -l…`) | C outside the build (in-tree `.c`/`.h` *are* hashed → that is A′, not B) |
| `go:wasmimport` host functions | host code outside the binary |

Any non-file Class-B dependence reachable in `B`'s closure → `unverifiable`. File I/O reached by
the closure also remains `unverifiable` unless the full upstream observation conjunction holds
(§7.8). Testlog identities alone prove neither complete path coverage nor operation outcomes, so
they cannot suppress the Class-B marker. (Ambient nondeterminism — `time.Now`, unseeded `rand` — is a
benchmark-*quality* issue, out of scope per §3, not a Class-B trigger.)

Class-B detection is **best-effort coverage**, not a hard guarantee — perfect external-dependence
detection is impossible (§3 non-goal), so unlike INV-1's source-soundness it can miss exotic cases.
The set above is deliberately **small and high-confidence**: under-flagging is the documented
boundary, while *over*-flagging is the real cost (a benchmark reading a fixed fixture in setup would
be marked `unverifiable` → forced rerun), recovered by `--assume-pure` (§7.5). The complement for
benchmarks the author *knows* read external state has two channels with the same semantics as the
pure side: the per-invocation `--impure` marker (folds into the CLI surface, §12) and the durable
`//gofresh:external` directive on the declaration, honored inside the shared engine (gofresh
`REQ-external-directive`) — either always re-runs the benchmark while a failing hashable guard
still reports `stale`.

### 7.4 Analysis requirements

- Build SSA over the **whole program** (`go/packages` with all-dependency syntax +
  `ssautil.AllPackages`) with **`ssa.InstantiateGenerics`** set. Both the generics disposition
  (§7.3) and the std-callback case (a user method dispatched *inside* a stdlib function — e.g.
  `byLen.Less` invoked by `sort.Sort`) require stdlib **bodies**, not just export data. *Spike-
  validated:* bodies absent → the user callback is missed (unsound); bodies present → captured. This
  whole-program load is the dominant cost, amortized by the closure cache (§7.6) and by recomputing
  only HEAD per check.
- **Call graph: RTA over the sound root set** (`callgraph/rta`), **not CHA.** *Spike-validated:*
  CHA's program-wide dynamic-dispatch over-approximation exploded a trivial benchmark to **6564**
  reachable functions and pulled in *sibling* benchmarks through a single `error`/`func()`/runtime
  bridge — so Tier 2 over CHA collapses to ≈ Tier 1 (no precision gained). RTA rooted at the
  benchmark is precise for direct benchmark-local flows, but it is **not** by itself sound for linked
  startup side effects: a package can register a concrete implementation in `init`, and `B` can later
  observe it through global state and interface dispatch without naming that package. Therefore RTA
  must be rooted at `B` plus startup roots, or a proven tighter startup/global-flow set; if that proof
  is unavailable, §7.3-A′ widens to Tier 1. VTA is an admissible precision alternative if it proves
  the same startup/global-flow coverage and is whole-program (so it amortizes across a package's
  benchmarks). Both RTA and VTA are implementation choices behind the same soundness rule: the hashed
  set is a source-effect superset, never a narrower `B`-reachable-only graph.
- Traverse edges through std for reachability, but hash only non-std declarations (§7.1 cut). The
  std-callback soundness above can *alternatively* be bought **without** loading stdlib bodies via
  the §7.3-A′ escape rule (non-std type converted to an interface ⇒ all its methods reachable),
  trading stdlib-load cost for coarser-but-sound inclusion. **Decided: load stdlib bodies** —
  soundness then rides on mature go/ssa+RTA traversal of real edges, not on our own escape
  enumeration being exhaustive (an incomplete escape set is a false-`valid`, the one failure INV-1
  forbids). The escape rule stays a documented optimization, used only if analysis time becomes a
  *measured* bottleneck and escape-completeness can be shown.

### 7.5 Escape hatch

A benchmark flagged `unverifiable` for a dependence the author knows is perf-irrelevant (a fixed
fixture file, a deterministic seed) can be asserted pure: `pew run --assume-pure <Bench>` records a
`pure: true` provenance line. It suppresses **all** of `B`'s unverifiability — both the closure's
Class-B marker (§7.3) *and* the runtime-input manifest's `unverifiable` flag (§7.8), so a benchmark
that only `stat`s a fixed fixture can reach `valid`. This is a **full "trust me"**, not "the manifest
keeps guarding": `--assume-pure` also waives the manifest's own blind spots (§7.8) — testlog-invisible
reads (`os.Root`/`openat`), a pre-stream `syscall.Chdir` that desyncs a relative path — because those
are exactly the unverifiabilities the author is asserting away. The **hashable** guards are *not*
waived: the closure hash, the `pew-runtime` digest over whatever inputs the manifest *did* observe,
and the toolchain/machine/build/runtime-config guards all still apply, so a change to an observed
input still moves the recording to `stale`. This is the **user taking responsibility**, explicit and
recorded — pew never silently assumes purity.

The assertion has two channels with the same semantics. The **CLI flag** (`--assume-pure <Bench>`)
is per-invocation, recorded as the `pure: true` line. The **durable directive** `//gofresh:pure` on
the benchmark declaration travels with the code — written once, reviewed in code review, honored by
every consumer of the shared gofresh engine (gofresh `REQ-purity-directive`). The exact Gofresh
attribution used by capture is recorded as `pew-purity`; source equality can prove it unchanged, but
an old recording with no attribution re-runs rather than acquiring historical evidence. Precedence:
an external-state declaration beats every purity assertion. `--impure` (§7.3) is applied after the
engine verdict and forces a re-run even for a directive-pure benchmark; the `//gofresh:external`
directive is enforced inside the engine (a declaration carrying both directives is refused there,
gofresh `REQ-external-precedence`), and `--assume-pure` never upgrades its verdict — the in-code
external declaration is not a blind spot the caller may vouch away.

### 7.6 What changes vs what is recomputed

- `closure-hash(B, R.commit)` is **recorded at run time** (working tree present, cheap, reliable)
  in-band as the `pew-closure` config line (§5). It is *not* recomputed from history — avoiding
  fragile historical checkouts.
- `closure-hash(B, HEAD)` is **recomputed on demand** from the working tree during a staleness
  check.
- Validity = compare the two. No historical build is ever required.

### 7.7 Cross-module closures

Dependencies are in the closure (§7.1) — a dep change can move `B`'s performance and, unlike
stdlib, is *not* covered by the toolchain guard. But *where a dependency's source lives* and
*whether a change to it leaves a signal* differ, and getting this wrong is a false-`valid` hole. The
dividing line is **immutable-pinned vs mutable-local**, decided per reachable package by one test:
**is its resolved `Dir` under `GOMODCACHE`?** (`go list -json` exposes `Standard`, `Dir`, and
`Module`{`Path`,`Version`,`Main`,`Dir`} — spike-confirmed: std → `Module=null`; main → `Main:true`
+ repo dir; cache deps → `Version` set + dir under `GOMODCACHE`; 126/1/33/0 on the spike's own build.)

| package class | identified by | closure contribution |
|---|---|---|
| **stdlib** | `Standard: true` | none — toolchain guard (§7.1) |
| **module-cache dep** | `Dir` under `GOMODCACHE` | the cache-relative module id (`modpath@version`, straight from `Module.Dir`) for every linked cache module. Immutable + version-locked ⇒ content ⟺ version, so this captures every possible change via the one event that causes it — a `go.mod`/`go.sum` bump, including init-registered drivers/codecs not reached from `B`'s declarations. Replace-to-version is automatic (`Module.Dir` points at the *replacement's* `@version`). Dep source is **traversed** for further reachability (like std) but **not** hashed per-declaration. |
| **mutable-local** — main module, local `replace => ./path`, `go.work use`, `vendor/` | `Dir` *not* under `GOMODCACHE` (and not std) | hash the reachable-declaration **source** (§7.1 first-party treatment). These resolve to working directories with **no version/go.sum signal** — content can change silently, so they must be hashed by content, never pinned by version. |

Two consequences:

- **Classification is per-package, not transitive.** A package is mutable-local iff its *own*
  resolved `Dir` is outside the cache. The build's replaces/workspace/vendor (all main-module level —
  dependency-declared replaces are ignored by Go) are already baked into where `go list` resolves
  each package, so no graph reasoning is needed.
- **Cache deps are pinned at module-version granularity** (coarser than per-declaration): bumping a
  linked cache dep marks `B` stale even if the bump didn't touch the reached function. Deliberate and
  sound — version bumps are coarse, infrequent events you'd re-benchmark on anyway, and linked init
  side effects can affect `B` without a declaration edge. Per-declaration hashing *into* cache deps is
  an available precision upgrade (like VTA), not a default.

### 7.8 Runtime-input evidence

Go's testlog stream records operation identities but omits behavior-affecting return values, byte
counts, and errors. A zero exit from the `go test` driver proves neither those outcomes nor normal
completion of an observation protocol by itself. Pew has no separate per-operation outcome evidence,
so `pew run` does not call the completed-observation constructor, does not select an observability
proof, and never promotes a file-reading benchmark to `valid` from testlog identities.

Every new run instead records one canonical incomplete observation for the result-contributing
package test-binary process, with reason `testlog lacks operation outcome evidence`. Its
`pew-runtime-inputs` manifest carries that disposition and `pew-runtime` carries the corresponding
digest. Ordinary checking therefore returns `unverifiable` while the other guards hold. In
particular, a transient read error followed by a successful process exit cannot become `valid` merely
because the process exited zero. An explicit purity assertion retains its separate full-trust
semantics from §7.5.

Pew defines and reads no positive observation-proof metadata. Format-1 recordings are checked through
Gofresh's ordinary fingerprint path. Unversioned recordings are rejected at the Pew format boundary
before their runtime manifest or any other fingerprint field is interpreted.

## 8. Machine fingerprint

The machine guard is a **drift guard**, compared by **exact equality**: "same machine config as when this was
recorded?" — not cross-machine normalization (§3). So the fingerprint hashes **stable identity facts
whose change plausibly shifts timing**, and *nothing transient* (or it fires spuriously):

- **CPU model / microarchitecture** (implies cache sizes, base clock, SIMD width) — taken from the
  arch-appropriate `/proc/cpuinfo` identity: `model name` on x86, the composed
  implementer/part/variant/revision on ARM. An empty identity is a hard capture error, never a
  silent collision.
- **physical + logical core counts** (SMT falls out of the ratio; matters for `b.RunParallel`)
- **total RAM**
- **OS + kernel version** — in deliberately: a kernel bump can move scheduler/syscall/mitigation
  costs, so reusing pre-bump numbers would be a quiet guard hole. The cost (rerun after a kernel
  update) is the price of soundness, like the toolchain guard.
The architecture is deliberately *not* a machine fact: the target platform `GOOS`/`GOARCH` and
the codegen feature level (GOAMD64/GOARM/…) are build-determined, so they live in the
**buildconfig** digest (§5, §7 guard 5) — code-determining inputs must hold even for a consumer
that checks only code guards.

The hash of these is the `machine` config line; a mismatch makes every result from the old
fingerprint stale. The fingerprint reflects **host** topology (cores/RAM as the OS sees the
hardware), not cgroup/container-effective limits — consistent with the single-machine scope (§3).
An OS without a stable implementation of these identity facts fails provenance capture rather than
emitting a weak best-effort fingerprint that could collide across machines.

**Transient run conditions are excluded** (governor/scaling-driver, turbo/boost, CPU pinning,
thermal/load). They are not machine *identity*: you set `performance` governor *for benchmarking*
while the box idles in `powersave`, so a fingerprint containing the governor would read `powersave`
at `pew status` time, mismatch the recording, and mark everything stale — spuriously, since a rerun
reproduces the numbers. The governor at *check* time is irrelevant; only the governor at *run* time
matters, and pew **owns the run** (§9). Run conditions are therefore enforced/recorded by run
hygiene (§9), never baked into identity.

**No calibration probe.** A reference micro-benchmark would turn the fingerprint from exact-equality
identity into a fuzzy measurement-with-threshold, conflating "same machine" with "same performance
right now" — which is exactly what running the benchmark already tells you. Drift (thermal,
noisy-neighbor VM) is caught by quiesce checks and the statistics (high variance surfaces it), not
by identity.

## 9. Run hygiene (own the run)

pew drives `go test` rather than ingesting arbitrary output, so results are statistics-grade and
provenance is captured atomically with the run:

- `-run=^$ -bench=<pattern>` (benchmarks only), **`-count=10`** (enough for a real benchmath CI;
  n=3 gives a degenerate interval), **`-benchtime=1s`** time-based (works with Go 1.24+ `b.Loop()`;
  per-op comparison makes the auto-scaled iteration count fine). The benchmark pattern, count, and
  benchtime are configurable run-selection/statistical knobs.
- **`-benchmem` is always on** for pew-recorded runs, so allocation metrics (`B/op`, `allocs/op`)
  are captured alongside timing. It is cheap, keeps stored results comparable, and lets `pew stat`
  flag allocation regressions without requiring a second run.
- **Build-affecting `go test` inputs are not generic run knobs.** Flags and environment that can
  change generated code (`-tags`, `-gcflags`, `-ldflags`, PGO, cgo/compiler env, architecture
  feature env) must be represented in the `buildconfig` guard (§7) before pew exposes or changes
  their handling; otherwise a build-input change could produce a false `valid` recording.
  **PGO is guarded by profile content, not flag string.** Two channels put a profile into a pew
  measurement, and both are content-guarded per package: an explicit `-pgo=<path>` in the
  **effective** GOFLAGS — resolved through `go env`, so a value written to the go env file
  (`go env -w`) counts exactly like the process variable — and, under `-pgo=auto` or an absent
  flag (the default), a tested **main** package's `default.pgo`: `go test` synthesizes the test
  main from the package under test, and a package-main's profile is consumed while a library
  package's never is. The applicable profile's content digest feeds the guard — the digest moves
  when the profile's bytes change, not merely when the flag string does — so `buildconfig` may
  differ across the packages of one run wherever the applicable profile does: a tested main
  package's `default.pgo`, or one relative `-pgo` path resolving against different module roots
  in a multi-module run; each recording self-describes either way. A profile the compile will consume but pew cannot read
  fails the operation closed (as does the empty `-pgo=` path, which fails every build): a guessed
  or partial value could hold `buildconfig` still while generated code moves. Relative profile
  paths resolve against the module root, where pew runs the go command. The profile is a build
  input outside the git-tracked source snapshots, so the producer revalidates its digest
  immediately before writing a recording and refuses the package on drift.
- **CPU pinning is opt-in** (`--pin`, Linux `taskset`/cpuset), **off by default** — it cuts
  scheduler-migration variance but is platform-specific and footgun-prone (core choice, SMT
  siblings, containers/VMs), so forcing it on would be the surprising default. The first knob to
  enable for serious runs.
- **Quiesce pre-checks WARN by default; `--strict` promotes them to hard-gates.** Pre-run checks:
  on-battery, non-`performance` governor, high load average, turbo. Warn-not-gate
  because hard-gating blocks legitimate quick runs and pew often can't fix the condition anyway
  (setting the governor needs root); the warning fires at the right moment — about to record a bad
  run.
  On Linux: the governor is read across **every** exposed cpufreq policy — a uniform box records
  the value, differing policies record (and warn as) the explicit `mixed` marker, never one
  policy's value standing in for cores it doesn't govern; an exposed policy whose governor cannot
  be read, or reads blank, leaves the whole signal unobserved (with one policy dark, neither
  uniformity nor a mix is provable); with no policy exposed the legacy per-cpu path is the fallback. Turbo is read
  with driver precedence: an exposed parseable `intel_pstate/no_turbo` is authoritative (it
  belongs to the platform driver actually governing the CPU) and `cpufreq/boost` is consulted
  only in its absence — conflicting signals never resolve toward enabled.
  **Thermal throttling is run-scoped evidence, not a pre-check.** The CPU
  `thermal_throttle/*_throttle_count` counters are cumulative since boot, so their standing value
  says nothing about this run; pew compiles the package's test binary **before** the bracket
  opens (compilation is a thermal-event source of its own — the discarded artifact warms the
  build cache the measurement run reuses; the measured invocation still performs its own link of
  the cached objects, the far smaller residual inside the bracket), then snapshots the counters
  bracketing the measurement run and records the delta verdict: `throttled=true` iff a counter increased within the bracket,
  `false` when counters were comparable and still, `unknown` when none were. A throttled package
  warns right after its measurement — the only moment the evidence exists — and under `--strict`
  is **refused**: not recorded, its prior recording untouched, the run exits non-zero.
- **Run conditions are recorded as provenance, never identity.** The observation that produces the
  quiesce warnings is also recorded in-band as the mandatory `pew-runconditions` config line — one
  per recording, uniform per invocation:
  `pew-runconditions: governor=<name> turbo=<on|off> load1=<1-min load> throttled=<true|false> battery=<true|false>`
  with any unobservable signal recorded as the explicit field value `unknown` (a platform with no
  quiesce signals — non-Linux today — records every field `unknown` rather than omitting the line:
  an unobserved condition is stated, never implied, so a missing line always means a
  pre-run-conditions producer, not a quiet run). Every field except `throttled` derives, with the
  warn/`--strict` gate, from one pre-run observation taken once per `pew run` invocation before
  execution — uniform per invocation, so the gate and the recording can never disagree about what
  was observed, and mid-run drift is not re-observed (drift is caught by the statistics, §8).
  `throttled` is the exception by construction: it is the per-package measurement-bracket delta
  above, so it may differ between packages of one invocation and is exactly as run-scoped as the
  measurement it annotates. A governor value that is not a plain token
  (`[A-Za-z0-9_.-]+`) is *recorded* as `unknown` — field values never carry separators that could
  corrupt the line's `key=value` structure — while the advisory warning may still name the raw
  value (warnings are stderr text, not persisted structure). The line answers the audit question "was this number
  taken with the performance governor on a quiet machine" from the recording alone. It is excluded
  from the machine fingerprint (§8), from every validity guard (§7), and from comparison grouping
  (§10.1): recordings differing only in run conditions validate and compare identically, with a
  differing-conditions note (§10.1) instead of a verdict.
- **The `go test` stream is transient input, not a recording.** Benchmarks and their dependencies
  may write to stdout at any point — including between the framework's un-newlined benchmark-name
  print and its result fields — so the stream pew parses can carry foreign bytes spliced into a
  result line, leaving the line's measurement fields orphaned on a line of their own. pew reads
  the stream tolerantly and fails closed **per benchmark**, never per line:
  - A line claiming a benchmark result that the parser cannot read (a result line with foreign
    bytes spliced into it) and any orphaned measurement-fields line (an iteration count plus
    value/unit pairs with no benchmark name — the detached tail of a spliced result line) is
    never recorded and never silently dropped: each is surfaced verbatim with its stream line
    number. Foreign lines of any other shape are outside this corruption surface and take the
    text format's own reading (unrecognized lines are skipped; `key: value`-shaped lines read as
    configuration).
  - **Sample floor.** A recorded benchmark carries, for every result row it produced, exactly the
    demanded `--count` samples. A top-level benchmark with any row above or below that count, or
    with corruption evidence attributed to it (an unparseable line naming it, an orphaned
    measurement-fields line following its output), is **refused**: not recorded, its prior
    recording left untouched, the refusal reported with the offending lines — while the package's
    other benchmarks record normally wherever the corruption is attributable, and the run still
    exits non-zero. An orphaned measurement-fields line attributable to no benchmark of the run
    refuses the whole package (a sample was destroyed or replaced somewhere pew cannot localize),
    and a selected benchmark left with no parseable rows and no attributed evidence fails the
    package (indistinguishable from a benchmark that never ran).
  - A `go test` exit failure still records nothing for the package — the process is suspect, not
    merely its transcript. The reserved-key refusal (§5) is unchanged and fail-closed.
  - Recordings carry no salvage artifacts: corrupt-line reports, counts, and refusal reasons are
    command output only, never persisted.
  - *Detection boundary:* a spliced line that still parses as a well-formed result row is
    textually indistinguishable from a genuine one, and a foreign unterminated write can prepend
    bytes to a detached tail so it evades orphan detection. Detection is therefore best-effort
    over a text stream: the sample floor catches every corruption that changes a row's count and
    orphan evidence catches tail-preserving fabrication, but a splice that both fabricates a
    parseable row and masks its detached tail is undetectable — the same exposure every consumer
    of the text format has.
- Records provenance (§5), computes the run-commit closure hash (§7), and records explicit incomplete
  runtime-input evidence (`pew-runtime*`, §7.8) at run time.
- Captures one ordinary measurement view and every benchmark fingerprint before execution. The
  result-contributing process is the package test binary launched by the `go test` driver. Because
  Pew has no per-operation outcome evidence, it finalizes exactly one incomplete observation for
  that package test binary. It validates the view and the sealed incomplete runtime state before
  writing any result. One immutable complete process-environment snapshot configures the view, the
  driver and inherited package test binary, and incomplete-observation construction. Source, guard,
  purity, commit, or worktree-state drift aborts the package write. Every destination is
  staged before replacement and every returned commit failure restores the prior complete set, so one
  package run is one producer transaction across ordinary filesystem errors. Each file is individually
  temp-and-rename safe; sudden process death during a multi-file commit may leave a recording absent,
  which safely forces regeneration rather than exposing a torn recording.
- The caller excludes source and repository mutation during final validation and commit.
  Pew double-checks the view, HEAD, and dirty state immediately before the write batch; as with
  Gofresh producer views, an externally allowed change-and-restore interval cannot be proven absent.
- A package write is refused when any recording destination overlaps a selected maximal-source path,
  because storage must not invalidate the completed source evidence it is about to persist.

## 10. Comparison & regression

Statistics are **imported, not re-implemented**: `benchfmt` (parse) → `benchproc` (project/group)
→ `benchmath` (distribution estimate, confidence interval, Mann–Whitney U significance) →
`benchunit` (units). This is exactly benchstat's pipeline, in-process.

Three baseline-selection modes (all requested):

- **Auto** — fresh run in the working tree vs the prior committed recording (`git show HEAD:<path>`,
  §6.1). `git diff` semantics for benchmark numbers: zero-config "did HEAD regress vs last time on
  this box?"
- **Pinned** — `git show <ref>:<path>` for a tag/branch (e.g. a release tag, or `main`). Stable
  "regressed since X?" Pinned refs must resolve to non-`dirty` recordings.
- **On-demand A/B** — any two refs (or a ref vs the working tree), each materialized via `git show`
  (§6.1). Manual investigation.

For historical sides, `pew stat` inventories recording paths from the selected ref trees (plus the
working-tree store for a working-tree side). Current benchmark declarations may inform working-tree
staleness warnings, but they are not the authority for which historical recordings exist.

### 10.1 Regression criterion

A **regression** on a metric requires all three: (1) it moved the **worse** direction (higher
sec/op, B/op, or allocs/op); (2) the change is **statistically significant** — Mann–Whitney U via
benchmath, default α = 0.05, *not* CI-overlap; and (3) its magnitude clears a **threshold** (default
3%, just above good-hygiene noise). All three are needed: significance without a magnitude floor
flags real-but-trivial changes; a floor without significance flags noise.

- **`pew stat` (default)** reports the benchstat-style table and marks `⚠ regression` on any metric
  meeting the three conditions. **`--fail-on-regression`** drives a non-zero exit on the same
  criterion for CI. `sec/op` gates by default; `allocs/op` and `B/op` are flagged but failing on
  them is opt-in.
- **The gate never passes vacuously.** `--fail-on-regression` asserts "compared and clean", not
  merely "no regression seen": exit status `0` requires at least one gated-unit comparison to have
  actually been performed with no gated regression. When zero benchmarks are statistically compared
  on any gated unit — nothing recorded on either side, every candidate skipped (stale format, dirty
  baseline, one-sided, provenance guard mismatch, no shared metric unit), or metrics compared but
  none among the gated units — the exit is non-zero and **distinct from the regression exit**: a
  detected regression exits `1`, an empty gated comparison exits `2`, each with a diagnostic on
  stderr naming its cause. In a partial comparison the compared subset alone governs the exit;
  skipped benchmarks surface as warnings/notes, never silently. Without `--fail-on-regression` an
  empty comparison stays informational (exit `0`), and the "no recorded benchmarks to compare" line
  names why when the cause is determinable from the inventoried recordings. A recording that fails
  the format shape check (§5) is still **inventoried**: it surfaces as a per-side stale-format
  warning and counts in the empty-comparison cause, never as an absent recording — immediately
  after a format change, "everything stale (format)" and "nothing recorded" are different
  diagnoses and must read differently. Only pew-marked recordings are inventoried (a pew-owned
  `pew-` key, §12 gc's same discrimination): a foreign benchmark file at a layout path never
  inventories a comparison key on its own; one sitting at a key another side's genuine recording
  inventoried is excluded from comparison by the per-side check and surfaces in its warning.
- Comparison projects *away* pew's own provenance keys (`commit`, `pew-closure`, …) so differing
  metadata doesn't fragment the benchstat grouping, and separately requires non-empty equal
  `machine`, `toolchain`, `buildconfig`, and `runtimeconfig` — never comparing across machine
  fingerprints, toolchains, build variants, or runtime-configuration variants silently (§6, §8).
- **Run conditions are surfaced, not gated.** When the two compared sides' recorded
  `pew-runconditions` (§9) differ in a categorical field — `governor`, `turbo`, `throttled`,
  `battery`, with a missing, malformed, or repeated field read as `unknown` — `pew stat` emits a
  note naming both sides' recorded conditions; when one side lacks the line, or a side mixes
  distinct values, the note names the affected side. In every case the comparison **still
  proceeds**. Unlike the guard mismatches above, differing run conditions never block: the numbers
  may still be wanted, the note keeps the mixing visible. Two sides that both lack the line are
  silent — there is nothing recorded to disagree about. `load1` is continuous measurement context:
  recorded and shown in the differing-conditions note, never itself a trigger.

Every tunable across pew — α, threshold, `--count`, `--benchtime`, pinning, strictness, gating
metrics — is **configurable with the stated values as defaults**; the correctness guards (§7) are
*not* knobs. Deliberately un-optimized (no per-metric thresholds, adaptive noise floors, persistent
caches) until real use asks for it.

## 11. Architecture & dependencies

**Everything in-process via imported libraries; the only subprocesses are the Go toolchain itself,
where it *is* the API.** No fork of x/perf (importable, Go-team-maintained); **no `git` binary** —
go-git keeps pew self-contained (`go install` works with no external binary beyond `go`).

- **Imported (Go-team / stdlib-tier):** `golang.org/x/perf/{benchfmt,benchproc,benchmath,benchunit}`;
  `golang.org/x/tools/go/{packages,ssa,callgraph,callgraph/rta}` (RTA — CHA rejected as too imprecise,
  §7.4; VTA a documented upgrade path, §7.4); `go/types`, `go/ast` via `go/packages`.
  (`golang.org/x/perf/benchseries` is a candidate later for across-commit trends.)
- **Imported (third-party, deliberate):** `github.com/go-git/go-git/v5` as the **git access layer**,
  and `github.com/spf13/cobra` as the CLI command/flag layer.
  pew is a pure git *reader* (HEAD, commit metadata, ref resolution, blob reads for
  baselines, file-scoped log for trends — §6.1, §9). Every `git show <ref>:<path>` /
  `git log -- <path>` in this spec is performed via go-git's object API, **never** by shelling to a
  `git` binary. *Tradeoff accepted:* go-git's `Worktree.Status()` (used for the informational
  `dirty` flag, §5, and to pin repository state across producer validation, §9) is slower than the binary on large repos and, on its weak cases
  (`.gitattributes` filters), errs toward **false-dirty** — the safe direction (a falsely-dirty
  result is only barred from being a pinned baseline, never silently trusted). The
  correctness-bearing "is this result faithful to commit C?" does **not** rely on Status(): it is
  derived from the closure comparison (working-tree closure hash vs C's, §7), so this weakness
  cannot produce a false-`valid`.
- **Subprocesses (the Go toolchain only):** `go test -bench` (run), `go list -json` (Tier-1 file
  sets / build-config resolution); `go/packages` drives the same toolchain for Tier-2 loads.
- Per project policy, go-git and cobra are the deliberately-added third-party deps (user-approved);
  any *further* third-party dependency is flagged and asked before adding.

## 12. CLI surface

Four commands; names follow the `go test` / benchstat idiom.

- **`pew run [packages] [flags]`** — run with hygiene (§9), store (overwrite, in-band provenance §5).
  - selection: `[packages]` (default `./...`), `--bench <pat>` (default `.`)
  - **`--stale`** — (re)run only benchmarks currently `stale` / `unverifiable` / `unrecorded`; skip
    `valid` ones (the reuse-don't-rerun win; shares the `status` closure-analysis path). This filter
    intersects the independent `--bench` selection and never adds or records an excluded benchmark.
  - hygiene: `--count` (10), `--benchtime` (1s), `--pin`, `--strict` (§9)
  - storage: `--bench-dir <dir>` (default `<module>/benchmarks`), `--label <name>` (§6);
    purity overrides: `--assume-pure <bench>` (§7.5), `--impure <bench>` (§7.3). Both assertions
    also have durable in-code forms honored by the shared engine: `//gofresh:pure` and
    `//gofresh:external` (§7.3, §7.5).
- **`pew status [packages]`** — per-benchmark verdict: `valid` / `stale ⟨reason⟩` /
  `unverifiable ⟨reason⟩` / `unrecorded`. `--stale` filters to non-valid (scriptable; feeds
  `run --stale`). Supports `--bench-dir <dir>` and `--label <name>` (§6).
- **`pew stat [ref | refA refB] [flags]`** — compare; the three baselines (§10) fall out of arg
  count (none → auto, one → pinned, two → A/B). `--fail-on-regression`, `--threshold` (3%),
  `--alpha` (0.05), metric selection (§10.1). `--explain` is reserved for a detailed guard/input
  explanation view over `pew-closure` and `pew-runtime*`. Supports `--bench-dir <dir>` and
  `--label <name>`.
- **`pew gc`** — remove stored results for benchmarks no longer present in the code. Supports
  `--bench-dir <dir>`. A pew recording that fails the current format (§5) is never silently
  skipped: when its benchmark is also gone from the source it is removed like any other orphan (an
  old-shape recording can never be reused — regeneration is the only path, and there is nothing
  left to regenerate); when its benchmark still exists it is kept and reported as stale (format),
  pointing at `pew run`. What counts as a pew recording here is the pew marker: any pew-owned
  (`pew-`-prefixed) configuration key, which benchmark output can never define (§5) — a
  layout-matching file without one is foreign and ignored, whatever its shape. A recording file
  that cannot be read or parsed at all is kept and reported with its error — removal never acts
  on unread content. A package whose benchmark-source scan fails keeps all its recordings behind
  the reported scan error: the error line is those recordings' report, and per-recording
  dispositions resume once the scan succeeds.

(`pew list` dropped — `status` is the inventory-plus-verdict view.) Every flag value is a **default,
configurable** (§10.1); the correctness guards (§7) are not flags.

*Design log — resolved & folded in:* closure-hash storage → in-band `pew-closure`, no sidecar
(§5/§6); call-graph → RTA (§7.4); std-callback → load stdlib bodies (§7.4); cross-module →
cache-vs-mutable-local (§7.7); blind-spot resolution → leaf-asm + resolve-linkname, best-effort
Class-B (§7.3); machine fingerprint → stable-identity, no transient conditions, no probe (§8); run
defaults → `--count=10`/`--benchtime=1s`, pinning opt-in, quiesce=warn+`--strict` (§9); regression →
Mann–Whitney α=0.05 + worse-direction + ≥3% (§10); CLI → above. Deferred explorations live in
`docs/issues/`.

## 13. Project invariants

- **INV-1 — Closure soundness (`valid` requires proof).** pew reports `valid` only when all six
  guards (§7) provably hold over a closure that is a *superset* of the source able to affect `B`'s
  performance. Every blind spot is **resolved** to a precise edge, **widened** to the maximal non-std
  closure, or **downgraded** to `unverifiable` — never silently dropped, never narrowing the covered
  set. An unresolved blind spot yields `unverifiable` unless an applicable explicit purity assertion
  accepts responsibility for that disposition; purity never waives the six guards. *Violation (strongest):* a reachable
  `const`/type/embed `B` depends on changes while `B`'s call graph is byte-identical, the closure
  hash is unchanged, and `B` is reported `valid` → silent regression behind a stale baseline (the
  core failure pew exists to prevent). *Kind:* entailed.
- **INV-2 — Validity verdict.** `B` is `valid` for HEAD iff *all six* guards hold and either its
  closure reaches no unhashable external dependence (Class B, §7.3) and its runtime-input manifest
  has no unverifiable disposition, or an applicable purity assertion overrides those dispositions
  (§7.5). Any guard failing ⇒ `stale`; absent a purity assertion, guards holding but either
  unverifiability source present ⇒ `unverifiable`. *Violation:* e.g. toolchain changed but reported valid, a
  benchmark reading an external file reported valid after the file changed, or a no-I/O benchmark
  carrying explicit incomplete outcome evidence reported valid. *Kind:* clause-explicit (§7).
- **INV-3 — Artifact format compatibility.** Every stored `.txt` is a well-formed Go
  benchmark-format file parseable by `benchfmt` and plain `benchstat`. *Violation:* a written file
  that `benchfmt` rejects → ecosystem lock-in, G5 broken. *Kind:* clause-explicit (§5, G5).
- **INV-4 — Provenance completeness.** Every produced result carries format `1` and the provenance
  and manifests required to evaluate all six guards: `pew run` always writes the commit, the runtime-input
  manifest (a canonical incomplete disposition for every run, §7.8), the four environment
  guard lines, and the run-conditions line (§9, explicit `unknown` fields when unobserved).
  *Violation:* a result missing `commit` or a guard value → the guard is unevaluable → validity
  undecidable → must conservatively re-run, defeating G1/G2. A missing or unknown format is rejected
  without interpretation. A recorded `pew-runtime` digest without its manifest is corruption and stale;
  a recording without the incomplete disposition violates the producer contract. *Kind:*
  entailed.
- **INV-5 — Derived state is never authoritative.** Persisted closure hashes are a memoization keyed
  *only* by immutable inputs `(commit, toolchain, buildconfig)`; they are never the source of truth
  for provenance and recomputing/discarding them never changes a validity verdict. *Violation:* a
  validity check trusts a cached hash that disagrees with recomputation from source → INV-1 bypassed
  via a stale cache. *Kind:* entailed.
- **INV-6 — Validity is commit-sha-independent.** The validity predicate (§7) depends only on
  `closure ∧ runtime-inputs ∧ toolchain ∧ machine ∧ buildconfig ∧ runtimeconfig`; it never reads the raw commit sha. *Violation:*
  two records identical in closure/toolchain/machine/buildconfig but differing in commit sha get
  different validity verdicts → every commit invalidates every benchmark → G2 (avoid wasted runs)
  defeated and the closure analysis rendered moot (the §5.1 trap). *Kind:* entailed. *Anchor test:*
  two records differing only in commit sha ⇒ both valid.
- **INV-7 — Closure includes non-call dependencies.** The closure covers not only call-reachable
  functions but the `const`/type/package-var declarations they reference, the `init`/var-init of
  contributing packages, and `go:embed`-ed files (§7.1). *Violation:* flipping a referenced
  `const BufSize` (4096→8192), changing a referenced struct's field layout, or editing an embedded
  data file leaves the hash unchanged → `B` reported `valid` while its behavior moved. *Kind:*
  entailed. *Anchor tests:* const-flip, struct-field change, embed-file edit ⇒ each reports stale.
- **INV-8 — Mutable-local deps are hashed by content.** Any reachable dependency whose resolved
  source is *not* under `GOMODCACHE` (local `replace => ./path`, `go.work use`, `vendor/`) is hashed
  by its source content, never pinned by `(module, version)` (§7.7). *Violation:* `B` reaches a
  locally-replaced dep; the dep's reachable source changes; `go.mod`/`go.sum` untouched; version-
  pinning → hash unchanged → `B` reported `valid` while its dependency moved → false-`valid`.
  *Kind:* entailed. *Anchor test:* edit a locally-replaced dep's reachable code without touching
  `go.mod` ⇒ `B` reports stale.
- **INV-9 — Run conditions are provenance-only.** The `pew-runconditions` line (§9) never enters
  the machine fingerprint (§8), any validity guard (§7), or the comparison grouping / required-equal
  guard set (§10.1). *Violation:* the `performance` governor is set for a recording run; at check
  time the box idles in `powersave`; a conditions-bearing fingerprint or guard marks every
  recording stale → spurious re-run of a still-valid result — G2 defeated by the exact
  transient-mismatch trap §8 excludes by construction. *Kind:* entailed (from §8's exclusion and
  the §5.1 identity/validity keys). *Anchor tests:* two recordings differing only in
  `pew-runconditions` ⇒ both valid; ⇒ compared (with a note), never fragmented or blocked.
- **INV-10 — The regression gate is never vacuously green.** Under `--fail-on-regression`, exit
  status `0` requires at least one gated-unit comparison to have been performed and found clean
  (§10.1); an empty gated comparison set exits non-zero with a status distinct from the regression
  exit and a cause-naming diagnostic. *Violation:* a repo with no recordings yet (or every recording
  skipped) wires `pew stat --fail-on-regression` as a CI gate; the gate exits `0` — passing
  precisely when it measured nothing — and a regression lands behind a green check: the silent-
  staleness failure (§1) reproduced at the gate itself. *Kind:* clause-explicit (§10.1). *Anchor
  tests:* empty store under the flag ⇒ exit `2` with diagnostic; all candidates skipped ⇒ exit `2`
  with per-cause tally; partial skip with a clean compared subset ⇒ exit `0`.
- **INV-11 — Recording sample completeness.** A recording produced by `pew run` carries exactly
  the demanded `--count` samples for every result row; a benchmark whose stream output shows
  corruption evidence (unparseable lines naming it, orphaned measurement fields, sample-count
  deviation) is refused rather than recorded (§9 sample floor); localizable corruption in one
  benchmark's output never discards another benchmark's completed measurements — only the §5
  reserved-key refusal and §9's unattributable-corruption cases refuse a whole package — and no
  corrupt line's content is ever recorded, as measurement data or as salvage artifact.
  *Violation:* a dependency's logger splices one line into one result row and either (a) the whole
  package's completed run — ~30 minutes of untouched benchmarks — is discarded, or (b) the
  affected benchmark records silently with fewer samples than demanded, and `pew stat` later
  compares a degenerate sample set as statistics-grade while every guard holds. *Kind:* entailed
  (§9 statistics-grade defaults + §5 provenance honesty). *Anchor tests:* a stream captured from a
  real consensus-node-logging run ⇒ the affected benchmark refused with the spliced line reported
  verbatim, the clean benchmark recorded with its full sample set; an orphaned-fields line with no
  attributable benchmark ⇒ the package refused.
- **INV-12 — The recording key set is closed.** Every recording `pew run` stores carries
  file-configuration keys drawn only from the closed set of §5: the toolchain's four stream keys,
  pew's provenance/guard/manifest keys, `pew-closure`, and `pure`; every other stream-derived
  configuration key is dropped before storage with a warning. *Violation:* a benchmark dependency logs one `key: value`-shaped line to
  stdout; the key is recorded as durable configuration in this run but is absent from the baseline;
  §10.1's config grouping fragments and the benchmark drops out of comparison one-sided — a
  regression hides behind a log line while every other invariant holds. *Kind:* entailed (§5
  self-describing artifacts + §10.1 grouping). *Anchor tests:* a stream carrying `raft: appending
  entries` ⇒ the key is stripped from every result and reported once; the composed run-path config
  serializes only closed-set keys.
