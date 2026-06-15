# pew — implementation plan

Roadmap derived from `docs/spec.md`. Each **chunk** lands as one commit (or tight sequence),
coherent end-to-end with tests green and no half-wired state, and runs the adversarial review loop
independently. Spec is authoritative; if a chunk reveals a spec gap, surface it — don't downscope.

## Strategy: Tier-1 first, Tier-2 as a precision swap

The spec makes Tier-1 (package-granularity closure) the **sound safe floor** and Tier-2 (RTA
per-declaration) a precision optimization behind the *same* `closure-hash(benchmark)` interface. So
we ship a complete, sound (coarse) tool through chunk 6, then sharpen precision in chunk 7 by
swapping the closure implementation — no scaffolding retrofit. The hardest chunk (RTA + blind spots)
lands last, on proven scaffolding, with `spikes/closure*` as the working reference.

## Module layout

```
cmd/pew/              # CLI entry (cobra subcommands)
internal/store/       # benchmark-format read/write, paths, overwrite, in-band provenance
internal/provenance/  # git (go-git) + toolchain + machine fingerprint + buildconfig
internal/closure/     # Tier-1 (go list) then Tier-2 (RTA); the pew-closure hash
internal/stale/       # the four guards → valid/stale/unverifiable
internal/run/         # go test orchestration, hygiene, quiesce
internal/compare/     # benchproc/benchmath, baselines, regression
```

Tooling (decided): a **Taskfile** drives dev commands (build/test/lint/run); the **cobra** CLI
library backs the subcommands. Third-party deps are thus go-git + cobra (user-approved); x/perf and
x/tools are Go-team / stdlib-tier.

## Chunks

### 1 — Module skeleton + storage I/O
- **Delivers:** `internal/store` reads/writes a benchmark's `.txt` in canonical benchmark format
  (via `benchfmt`), with in-band provenance config lines, overwrite-in-place, and the
  `<pkg>/<Bench>[.<label>].txt` layout. `cmd/pew` skeleton with subcommand routing.
- **Invariants:** INV-3 (every written file re-parses via `benchfmt`/`benchstat`), INV-4 (provenance
  round-trips) — round-trip + golden tests; mutation-test the writer.
- **Depends on:** —

### 2 — Provenance capture
- **Delivers:** `internal/provenance` produces the config-line set for a run: `commit`+`dirty`
  (go-git, per `spikes/gogit`), `toolchain` (`go version`), `machine` (the §8 stable-identity hash),
  `buildconfig` (tags/gcflags/cgo/PGO digest).
- **Invariants:** completes INV-4's inputs; machine fingerprint excludes transient conditions (§8) —
  test that governor/turbo changes do *not* move the fingerprint.
- **Depends on:** 1

### 3 — Tier-1 closure + cross-module + std-cut
- **Delivers:** `internal/closure` computes a **sound package-granularity** `pew-closure` from
  `go list -json -deps` (per `spikes` go-list findings): non-std reachable package file sets, hashed;
  the cache-vs-mutable-local rule by `Dir`-under-`GOMODCACHE`. A real, coarse closure hash.
- **Invariants:** INV-1 at Tier-1 (over-approximation, never false-valid), INV-8 (mutable-local
  hashed by content — anchor: edit a locally-`replace`d dep without touching `go.mod` ⇒ hash moves).
- **Depends on:** 1

### 4 — Staleness verdicts + `pew status`
- **Delivers:** `internal/stale` combines the four guards (closure from 3, toolchain/machine/
  buildconfig from 2) into `valid`/`stale⟨reason⟩`/`unverifiable⟨reason⟩`/`unrecorded`; records
  `pew-closure` at run time, recomputes HEAD, compares. `pew status` ships. **First working tool**
  (coarse staleness detection).
- **Invariants:** INV-2 (verdict = guard conjunction), INV-5 (derived hash never authoritative),
  INV-6 (sha-independent — anchor: two records differing only in `commit` ⇒ both valid).
- **Depends on:** 2, 3

### 5 — Run hygiene + `pew run` + `--stale`
- **Delivers:** `internal/run` drives `go test` (`-count=10`/`-benchtime=1s`/`-bench`/`-run=^$`),
  demuxes output per-benchmark, stores via 1 with provenance from 2 and closure from 3; quiesce
  checks (warn/`--strict`), `--pin`, `--label`, `--assume-pure`/`--impure`; `--stale` reuses 4.
  **Full run→store→reuse loop** at Tier-1.
- **Invariants:** run defaults configurable (§9/§10.1); `--stale` reruns exactly the non-`valid` set.
- **Depends on:** 1, 2, 3, 4

### 6 — Compare + regression + `pew stat`
- **Delivers:** `internal/compare` — `benchfmt`→`benchproc`→`benchmath`→`benchunit`; baselines auto/
  pinned/A-B via go-git refs; projection strips `pew-*` and matches `machine`; regression =
  Mann-Whitney α=0.05 + worse-direction + ≥3%; `pew stat` + `--fail-on-regression`. **Core
  user-facing feature set complete** (run/store/compare + stale-detection).
- **Invariants:** never compare across `machine` silently (§10); regression criterion as specified.
- **Depends on:** 1, 2

### 7 — Tier-2 closure (precision swap): RTA + reference graph + blind spots
- **Delivers:** swap `internal/closure` to **RTA per-benchmark** (`go/packages` LoadAllSyntax +
  `ssa.InstantiateGenerics`, per `spikes/closurecmp`): call graph ∪ reference graph (consts/types/
  vars), std-cut traversal, blind-spot dispositions (resolve linkname/leaf-asm/generics, A′ maximal
  widening, Class-B → `unverifiable`). Same interface — chunks 4-6 unchanged.
- **Invariants:** INV-1 preserved at full precision (resolve/widen/downgrade, never drop), INV-7
  (closure includes non-call deps — anchors: const-flip, struct-field change, embed-file edit ⇒
  each ⇒ stale). Differential test: Tier-2 hash-set ⊆ Tier-1 set on a fixture corpus.
- **Depends on:** 3 (replaces its impl), 4

### 8 — `pew gc` + polish
- **Delivers:** `pew gc` removes stored results for benchmarks no longer present in code (enumerate
  via `go list`/3); CLI help, README, end-to-end docs; opportunistic test-surface extension.
- **Depends on:** 1, 3

## Process per chunk

Per chunk: run the Project-invariants and Spec-first gates at planning; land a first cut (tests
green); `git add -A`; mutation-test the delta; spawn a fresh-eyes adversarial reviewer scoped to the
delta against `docs/spec.md`; disposition every finding; commit when the loop converges. Issue triage
at each chunk start scans `docs/issues/` for `Lands:` matches (currently: purity-directives is
condition-triggered, no chunk match expected).
