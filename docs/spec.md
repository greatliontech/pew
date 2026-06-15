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
  commit, toolchain, machine fingerprint, build configuration.
- **G2 — Staleness.** For any stored result, decide *valid* (re-use) vs *stale* (re-run) for
  the current tree, with **no false "valid"** (a stale result must never be reported valid).
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
  (data files read at runtime, env-driven branches). pew is sound (never false-`valid`) by
  *widening* bounded blind spots and marking unbounded external dependence `unverifiable`
  (§7.3), not by claiming dynamic precision. See §7.
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
- **Machine fingerprint** — a stable id over the hardware/OS facts that affect benchmark timing.
- **Baseline** — the result set a fresh run is compared against to detect regression (§9).

## 5. Stored artifact format

A stored result is a **canonical Go benchmark-format file** (§1 canonical spec). Provenance
rides in-band as the format's own `key: value` **configuration lines** — the format's sanctioned
extension point — so files stay self-describing and `benchstat`-readable.

Toolchain already emits `goos`, `goarch`, `pkg`, `cpu`. pew adds (uniform per run, therefore
global config lines):

| key            | meaning                                                        | source-of-truth? |
|----------------|---------------------------------------------------------------|------------------|
| `commit`       | full SHA of HEAD at run time                                   | yes              |
| `commit-time`  | committer timestamp (RFC3339) — for ordering baselines        | yes              |
| `branch`       | branch name at run time (advisory)                            | yes              |
| `toolchain`    | `go version` output (compiler/runtime identity)               | yes              |
| `machine`      | machine fingerprint id (§8)                                    | yes              |
| `buildconfig`  | digest of build tags + relevant GOFLAGS/gcflags + cgo + PGO   | yes              |
| `dirty`        | `true` if the working tree had uncommitted changes at run     | yes              |

**Closure hashes are *derived*, not provenance** (recomputable from commit + toolchain +
build-config), so they are never the source of truth and recomputing them never changes a verdict
(INV-5). Whether they live in a sidecar cache index (§6) or as a namespaced in-band config line is
reopened by the overwrite decision (§6) — see §12.1; the original in-band objection (projection
fragmentation) only applied to multi-block files.

A `dirty` run is recorded but flagged: its `commit` does not faithfully describe its source, so
its closure is computed from the *working tree*, and it is never usable as a pinned baseline.

### 5.1 Identity vs validity

Two distinct questions use two distinct keys over a record's fields, and conflating them is the
classic trap: putting the commit sha into the *validity* test makes every commit invalidate every
benchmark — collapsing pew back to "re-run everything every commit" and rendering the closure
analysis dead weight.

- **Identity** — *which* recording is this? The full provenance tuple
  `(benchmark, commit, toolchain, machine, buildconfig)`. Labels the in-band provenance and lets a
  fresh run recognize whether it is the *same point* as a prior recording (same tuple) or a new one.
  The per-commit history of identities lives in git (§6.1), not an in-file log.
- **Validity** — is a stored measurement still usable for HEAD *without re-running*? The predicate
  of §7: `closure ∧ toolchain ∧ machine ∧ buildconfig`, each compared HEAD-vs-record.

The two keys differ in exactly one term — the **commit↔closure swap**:

| term         | identity (which record) | validity (reuse for HEAD) |
|--------------|:-----------------------:|:-------------------------:|
| commit sha   |           ✓             |        ✗ (excluded)       |
| closure hash |           ✗             |             ✓             |
| toolchain    |           ✓             |             ✓             |
| machine      |           ✓             |             ✓             |
| buildconfig  |           ✓             |             ✓             |

Identity **pins** code by commit; validity **tests** code by closure. The commit is a coarse
"some code somewhere changed" signal — correct for *naming a point in history*, fatally over-broad
for *deciding a re-run*. The closure is the precise "code this benchmark exercises changed" signal.
Swapping one for the other is the entire reason closure analysis exists (see INV-6).

## 6. Storage layout

```
<bench-dir>/                      # default: ./benchmarks, configurable
  <pkg-path>/                     # mirrors the package import path under module root
    <BenchmarkName>[.<label>].txt # one file per top-level benchmark (+ optional variant label)
  index.json                      # derived cache + bookkeeping (see below)
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
  where they could diverge under rebase or hand-edit. To add samples, raise `-count` (an explicit
  same-identity sample *merge* is a §12 open question).
- **History axis vs parallel-variant axes.** The path encodes only the history-invariant identity
  `(pkg, benchmark)`; the rest of the identity tuple (§5.1) rides in-band. *Commit* is a **history**
  axis → git holds prior values, the file keeps the latest. *Toolchain / machine / buildconfig* are
  **parallel-variant** axes: if you deliberately benchmark more than one (cgo on/off, a feature build
  tag, two Go versions) and want them retained side by side, `--label <name>` (§12.8) adds the
  filename discriminator (`BenchmarkFoo.cgo.txt`); without it the newer variant overwrites the older.
  Guards 2–4 (§7) still prevent any silent *cross-variant comparison* either way — so omitting a
  label is a retention choice, never a correctness one.
- `index.json` is a **memoization cache + listing index**, never a second source of truth for
  provenance. It maps `(pkg, benchmark, commit, toolchain, buildconfig)` → recorded closure hash
  (the value computed at run time when the tree was present). `toolchain` is in the key because
  version build-constraints (`//go:build go1.27`) change the selected file set, so the closure file
  set is a function of the toolchain too. Because its key is immutable, a cached entry can never
  silently go stale; it can be deleted and recomputed without changing correctness. Under overwrite
  (above) only the current recording's hash per benchmark is retained — the cache tracks HEAD-state
  validity, not history.

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

*(Exact directory naming / whether `index.json` is committed vs `.gitignore`d is an open
question — see §12.)*

## 7. Staleness contract

A stored result `R` for benchmark `B` gets one of **three verdicts** for HEAD, and the governing
rule is **`valid` requires proof**:

- **valid** (reuse `R`) — all four guards below provably hold over a soundly over-approximated
  closure.
- **stale** (re-run) — some guard demonstrably fails: closure, toolchain, machine, or build config
  changed.
- **unverifiable** (re-run, reason recorded) — guards would pass, but `B`'s closure reaches an
  external dependence pew cannot hash (Class B, §7.3), so validity can be neither proven nor
  refuted. Operationally a re-run, but distinct from `stale`: the user can assert purity to override
  (§7.5). Absence of proof never collapses to `valid` (INV-1).

The four guards:

1. **Closure** — `closure-hash(B, HEAD) == closure-hash(B, R.commit)`
2. **Toolchain** — current `go version` == `R.toolchain`
3. **Machine** — current fingerprint == `R.machine`
4. **Build config** — current digest == `R.buildconfig` (build tags, `-gcflags`, cgo on/off, PGO
   profile hash)

Guards 2–4 are exact-equality on recorded provenance — cheap and unambiguous. Guard 1 is the hard
one; the rest of this section defines `closure-hash` and its soundness.

### 7.1 What the closure covers

The closure of `B` is the set of **source declarations** whose change could alter `B`'s runtime
behavior, reachable from `B`'s body via two relations, unioned:

- **Call graph** — functions/methods `B` transitively invokes (`go/ssa` + CHA, §7.4).
- **Reference graph** — the `const`s, types, and package-level vars those functions name. A body can
  be byte-identical while a referenced `const BufSize = 4096` flips to `8192`; hashing call edges
  alone misses it, so referenced declarations are in the closure too (transitively for types/consts).

Plus, for every package contributing a reachable declaration: its **`init` functions and
package-level var initializers** (they set up state `B` reads) and any **`go:embed`-ed files**
(benchmark input that changes behavior with no source change).

**Scope cut — the standard library is excluded from the hash.** stdlib + runtime change *iff* the
toolchain changes, which is Guard 2's job; hashing thousands of constant-per-toolchain std files is
redundant and slow. The call graph is still **traversed through std** (so callbacks into your code —
a user `MarshalJSON` invoked by `encoding/json` — stay reachable), but std declarations contribute
nothing to the hash. Module **dependencies are included** (a `go.mod` bump changes their content,
which Guard 2 does *not* cover). `go list -json` distinguishes them: `Standard: true` → excluded;
everything else → hashed.

### 7.2 Tiers — same model, Tier 1 is the sound floor

- **Tier 1 (package closure):** hash the resolved file sets (`GoFiles`/`CgoFiles`/`SFiles`/
  `EmbedFiles`) of `B`'s package and all transitive **non-std** dependency packages from
  `go list -json`. Over-approximates; never false-`valid`; no SSA.
- **Tier 2 (declaration closure):** §7.1 narrows the hash from whole-package to the reachable
  declaration set. Only ever **shrinks** the hashed set relative to Tier 1; never makes a stale
  result look valid.

**Tier 1 over the whole non-std build is the maximal sound closure** — and the single fallback every
blind spot escalates to (§7.3). Tier 2 + the resolution rules are pure precision: they shrink the
set only where shrinking is provably safe. INV-1 therefore holds *by construction* — the worst case
is always the maximal source set, never less.

### 7.3 Blind spots — resolve, widen, or downgrade

A blind spot is a runtime-reachable code path the static graph does not see. Each gets exactly one
disposition, chosen to never under-cover:

**(A) Resolvable — add the precise edge, no widening.** The missing edge has a statically known
target read directly:

| construct | how it's resolved |
|-----------|-------------------|
| `//go:linkname a b` | target `b` is named in the directive → add edge to `b` (std target → Guard-2-covered, ignore; non-std → include normally) |
| Go-asm symbol refs (`·name(SB)`, `pkg·name(SB)`) | scan the `.s` for symbol references → add those edges; always also hash the `.s` itself |
| generics | built with `ssa.InstantiateGenerics` (§7.4) → every instantiation in the build is materialized and dispatched concretely; not a blind spot |

**(A′) Bounded-but-unresolved — widen to the maximal sound closure (§7.2).** The construct could
reach code we cannot enumerate, but the target is somewhere in the analyzed source:

| construct | why unresolvable |
|-----------|------------------|
| `reflect` dispatch in non-std code (`Value.Call`, `MethodByName().Call`, `MakeFunc`) | target chosen from runtime data |
| `unsafe` function-pointer conversions / computed calls | escapes the type/call model |
| asm with a computed `CALL` (no parseable symbol) | opaque control flow |
| a non-std type converted to an interface (incl. `any`) reachable from `B` | un-analyzable code (e.g. reflection inside an opaque callee) may dispatch *any* of its methods → all its methods are added |

These escalate to hashing the **entire non-std build closure** (Tier 1 maximal). Tighter bounds
(widen-to-package/module) are taken *only* when provably sound; the default is maximal. Because
reflection/asm/unsafe *inside stdlib* are below the §7.1 cut, A′ fires only on such constructs in
**your code or a dependency** — rare, so precision stays high.

**(B) Unhashable external dependence — verdict `unverifiable`, not a widening.** Behavior depends on
state that is not source at all, so no source widening can bound it:

| construct | external state |
|-----------|----------------|
| file I/O on a non-embedded path (`os.Open`, `os.ReadFile`, …) | a file that may change between record and HEAD |
| network I/O | a remote that may change |
| `plugin.Open` / `plugin.Lookup` | code loaded from an arbitrary `.so` at runtime |
| cgo linked against an external library (`#cgo LDFLAGS: -l…`) | C outside the build (in-tree `.c`/`.h` *are* hashed → that is A′, not B) |
| `go:wasmimport` host functions | host code outside the binary |

Any of these reachable in `B`'s closure → `unverifiable`. (Ambient nondeterminism — `time.Now`,
unseeded `rand` — is a benchmark-*quality* issue, out of scope per §3, not a Class-B trigger.)

### 7.4 Analysis requirements

- Build SSA over the **whole program** (`go/packages` `LoadAllSyntax` + `ssautil.AllPackages`) with
  **`ssa.InstantiateGenerics`** set — required for the generics disposition in §7.3.
- Use the **CHA** call graph rooted at `B` (cheapest sound over-approximation of dynamic dispatch).
  RTA/VTA are admissible later *only* as precision improvements that preserve the superset property;
  they never relax soundness.
- Traverse edges through std for reachability, but hash only non-std declarations (§7.1 cut).

### 7.5 Escape hatch

A benchmark flagged `unverifiable` for a Class-B dependence the author knows is perf-irrelevant (a
fixed fixture file, a deterministic seed) can be asserted pure: `pew run --assume-pure <Bench>`
records a `pure: true` provenance line, after which Class-B detection is suppressed for it. This is
the **user taking responsibility**, explicit and recorded — pew never silently assumes purity.

### 7.6 What changes vs what is recomputed

- `closure-hash(B, R.commit)` is **recorded at run time** (working tree present, cheap, reliable)
  and cached in `index.json`. It is *not* recomputed from history — avoiding fragile historical
  checkouts.
- `closure-hash(B, HEAD)` is **recomputed on demand** from the working tree during a staleness
  check.
- Validity = compare the two. No historical build is ever required.

## 8. Machine fingerprint

A stable digest over the facts that affect benchmark timing on this box:

- CPU model / microarchitecture, physical + logical core count, cache sizes
- GOARCH and arch feature level (e.g. GOAMD64) the toolchain targets
- total RAM
- OS / kernel version
- **timing-stability facts**: frequency-scaling governor / driver, turbo-boost state

Fingerprint mismatch ⇒ all results from the old fingerprint are stale (drift guard). Single-machine
users see a match until they change hardware or a timing-relevant setting. An optional calibration
micro-probe (detect silent thermal/throttle drift the static facts miss) is a §12 open question.

## 9. Run hygiene (own the run)

pew drives `go test` rather than ingesting arbitrary output, so results are statistics-grade and
provenance is captured atomically with the run:

- `-run=^$ -bench=<pattern>` (benchmarks only), `-count=N` (default tuned so benchmath has enough
  samples — exact N in §12), fixed `-benchtime`.
- Optional **CPU pinning** (Linux `taskset`/cpuset) for run-to-run stability.
- **Quiesce pre-checks** (advisory, can warn or hard-gate): on-battery, non-`performance`
  governor, high load average, turbo enabled, thermal headroom. The goal is to refuse to silently
  record noisy data.
- Records provenance (§5) and computes + caches the run-commit closure hash (§7) at run time.

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

A **regression** is a statistically significant slowdown beyond a configurable threshold;
comparison must project *away* pew's own provenance keys so differing commit/machine metadata
doesn't fragment the benchstat grouping (with machine guarded separately — never compare across
machine fingerprints silently).

## 11. Architecture & dependencies

**Everything in-process via imported libraries; the only subprocesses are the Go toolchain itself,
where it *is* the API.** No fork of x/perf (importable, Go-team-maintained); **no `git` binary** —
go-git keeps pew self-contained (`go install` works with no external binary beyond `go`).

- **Imported (Go-team / stdlib-tier):** `golang.org/x/perf/{benchfmt,benchproc,benchmath,benchunit}`;
  `golang.org/x/tools/go/{packages,ssa,callgraph,callgraph/cha}`; `go/types`, `go/ast` via
  `go/packages`. (`golang.org/x/perf/benchseries` is a candidate later for across-commit trends.)
- **Imported (third-party, deliberate):** `github.com/go-git/go-git/v5` as the **git access layer**.
  pew is a pure git *reader* (HEAD/commit/commit-time/branch, ref resolution, blob reads for
  baselines, file-scoped log for trends — §6.1, §9). Every `git show <ref>:<path>` /
  `git log -- <path>` in this spec is performed via go-git's object API, **never** by shelling to a
  `git` binary. *Tradeoff accepted:* go-git's `Worktree.Status()` (used only for the informational
  `dirty` flag, §5) is slower than the binary on large repos and, on its weak cases
  (`.gitattributes` filters), errs toward **false-dirty** — the safe direction (a falsely-dirty
  result is only barred from being a pinned baseline, never silently trusted). The
  correctness-bearing "is this result faithful to commit C?" does **not** rely on Status(): it is
  derived from the closure comparison (working-tree closure hash vs C's, §7), so this weakness
  cannot produce a false-`valid`.
- **Subprocesses (the Go toolchain only):** `go test -bench` (run), `go list -json` (Tier-1 file
  sets / build-config resolution); `go/packages` drives the same toolchain for Tier-2 loads.
- Per project policy, go-git is the one deliberately-added third-party dep (user-approved); any
  *further* third-party dependency is flagged and asked before adding.

## 12. Open questions (iterate here)

1. **Closure-hash storage** — sidecar `index.json` cache vs per-benchmark in-band config lines.
   Overwrite (§6) means each file has a single block, so the original objection to in-band closure
   lines (a varying key fragmenting the benchstat projection) no longer applies — in-band becomes
   viable again. Re-evaluate sidecar vs in-band (namespaced, e.g. `pew-closure`, so pew can strip it
   from comparison projections) under overwrite.
2. **`index.json` in git** — committed (shareable cache, reviewable) vs `.gitignore`d (pure local
   cache, always recomputable)? Affects whether the cache is a repo artifact.
3. **Blind-spot resolution depth** — §7.3 fixes the soundness (unresolved ⇒ maximal widening;
   external ⇒ `unverifiable`). Open: how much asm/linkname parsing to implement before falling back
   to maximal widening (precision-only, never affects soundness), and the exact call-signature set
   that flags Class-B I/O (`os`, `net`, `plugin`, cgo-external) without over-flagging.
4. **Machine fingerprint field set** — exact fields, and whether to add a calibration probe.
5. **Run defaults** — concrete `-count` and `-benchtime` values; pinning default on/off; which
   quiesce checks warn vs hard-gate.
6. **Regression criterion** — threshold default; p-value gate vs confidence-interval overlap.
7. **Cross-module closures** — benchmarks whose closure crosses into other modules (replace
   directives, workspace mode); hashing strategy there.
8. **CLI surface** — proposed `pew run` / `pew status` (valid vs stale listing) / `pew stat`
   (compare) / `pew list` / `pew gc`; finalize names and flags.

## 13. Project invariants (spec-tier)

Recorded at the spec because no code exists yet. Each promotes to an enforced test / type / asserted
check when code able to violate it is first written (per project conventions). The chunk-start
triage gate resolves these `Lands:` conditions.

- **INV-1 — Closure soundness (`valid` requires proof).** pew reports `valid` only when all four
  guards (§7) provably hold over a closure that is a *superset* of the source able to affect `B`'s
  performance. Every blind spot is **resolved** to a precise edge, **widened** to the maximal non-std
  closure, or **downgraded** to `unverifiable` — never silently dropped, never narrowing the covered
  set; absence of proof yields `unverifiable`, never `valid`. *Violation (strongest):* a reachable
  `const`/type/embed `B` depends on changes while `B`'s call graph is byte-identical, the closure
  hash is unchanged, and `B` is reported `valid` → silent regression behind a stale baseline (the
  core failure pew exists to prevent). *Kind:* entailed. *Lands:* when closure analysis is first
  implemented.
- **INV-2 — Validity verdict.** `B` is `valid` for HEAD iff *all four* guards hold **and** its
  closure reaches no unhashable external dependence (Class B, §7.3); any guard failing ⇒ `stale`;
  guards holding but a Class-B dependence present ⇒ `unverifiable`. *Violation:* e.g. toolchain
  changed but reported valid, or a benchmark reading an external file reported valid after the file
  changed. *Kind:* clause-explicit (§7). *Lands:* when the staleness check is implemented.
- **INV-3 — Artifact format compatibility.** Every stored `.txt` is a well-formed Go
  benchmark-format file parseable by `benchfmt` and plain `benchstat`. *Violation:* a written file
  that `benchfmt` rejects → ecosystem lock-in, G5 broken. *Kind:* clause-explicit (§5, G5).
  *Lands:* when the storage writer is implemented.
- **INV-4 — Provenance completeness.** Every stored result carries the provenance required to
  evaluate all four guards (commit, toolchain, machine, buildconfig). *Violation:* a result missing
  `commit` → closure guard unevaluable → validity undecidable → must conservatively re-run, defeating
  G1/G2. *Kind:* entailed. *Lands:* when the storage writer is implemented.
- **INV-5 — Derived state is never authoritative.** Persisted closure hashes are a memoization keyed
  *only* by immutable inputs `(commit, toolchain, buildconfig)`; they are never the source of truth
  for provenance and recomputing/discarding them never changes a validity verdict. *Violation:* a
  validity check trusts a cached hash that disagrees with recomputation from source → INV-1 bypassed
  via a stale cache. *Kind:* entailed. *Lands:* when the closure cache is implemented.
- **INV-6 — Validity is commit-sha-independent.** The validity predicate (§7) depends only on
  `closure ∧ toolchain ∧ machine ∧ buildconfig`; it never reads the raw commit sha. *Violation:*
  two records identical in closure/toolchain/machine/buildconfig but differing in commit sha get
  different validity verdicts → every commit invalidates every benchmark → G2 (avoid wasted runs)
  defeated and the closure analysis rendered moot (the §5.1 trap). *Kind:* entailed. *Anchor test:*
  two records differing only in commit sha ⇒ both valid. *Lands:* when the staleness check is
  implemented.
- **INV-7 — Closure includes non-call dependencies.** The closure covers not only call-reachable
  functions but the `const`/type/package-var declarations they reference, the `init`/var-init of
  contributing packages, and `go:embed`-ed files (§7.1). *Violation:* flipping a referenced
  `const BufSize` (4096→8192), changing a referenced struct's field layout, or editing an embedded
  data file leaves the hash unchanged → `B` reported `valid` while its behavior moved. *Kind:*
  entailed. *Anchor tests:* const-flip, struct-field change, embed-file edit ⇒ each reports stale.
  *Lands:* when closure analysis is first implemented.
