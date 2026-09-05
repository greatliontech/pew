# pew — tool-resident guidance

## verbs

### ab
**surfaces:** cli
**does:** A/B-compare the working tree against a ref without touching either.
**knobs:**
- `bench` — benchmark pattern (go test -bench syntax).
- `count` — interleaved iterations per side (default 6).
- `benchtime` — per-benchmark time or iteration budget (go test -benchtime).
- `ref` — B side: any git rev the repository resolves (default HEAD).
- `pin` — pin both sides to one CPU set derived from the host's topology (taskset): the isolated set when the kernel has one, else one whole physical core, the fastest the kernel ranks, outside CPU 0's; the set and its derivation are reported first, and a host it cannot be derived on, or without taskset to apply it, refuses.
- `strict` — refuse to measure under noisy machine conditions.
- `out` — also write both sides' raw benchmark streams to this file, marked pew-ab/dirty — a derivation artifact, by shape never a stat baseline.
**when:** use ab while a design or curve is still moving — the
uncommitted working tree (side A) measures against the ref (side B)
materialized in a disposable detached worktree beside the
repository on the same filesystem; both sides build before either
measures, executions interleave A/B per iteration so slow machine
drift cancels instead of folding into the delta (A leads each pair,
so only one side's first sample carries the cold-start boundary),
and the repository stays writable throughout — no stash cycle,
crash-safe by construction. Each side runs from its own tree so
cwd-sensitive benchmarks resolve correctly; each side's guard
values are captured in its own tree at build time, and a ref
pinning a different toolchain or PGO bytes refuses with the
mismatch named. Machine hygiene is run's regime per side — the pin and
strict knobs and the throttle bracket. The verdict uses stat's
significance machinery with side B as base. Nothing is written to
the recording store.
**example:** an interleaved comparison of ./internal/wal against
HEAD while tuning a hot path.

### run
**surfaces:** cli
**does:** Run benchmarks with hygiene and store results.
**knobs:**
- `bench-dir` — stored-recordings directory (default <module>/benchmarks).
- `count` — measurement runs per benchmark (default 10).
- `benchtime` — duration or iterations per measurement (default 1s).
- `bench` — benchmark name pattern (default .).
- `pin` — pin the measurement to one CPU set derived from the host's topology (taskset): the isolated set when the kernel has one, else one whole physical core, the fastest the kernel ranks, outside CPU 0's; the set and its derivation are reported first, and a host it cannot be derived on, or without taskset to apply it, refuses. The pin's width is the measured process's GOMAXPROCS, a guarded runtime configuration: a pinned recording and an unpinned one stale each other and share a destination, so keep both under distinct labels. A run minting a new GOMAXPROCS variant lineage for a benchmark already on record warns at record time — grouping never bridges the suffix, and the operator must not learn that from a later comparison after the measurement time is spent.
- `strict` — treat quiesce warnings as fatal.
- `label` — variant label for the recording filename.
- `all` — measure every selected benchmark, a valid recording included; the default serves what is proven and measures the rest (status's closure-analysis path, over the run's own typed view, intersecting the independent benchmark selection and never adding or recording an excluded benchmark).
- `vouch` — dynamic-state vouch IMPORT-PATH:VARIABLE (repeatable): a version-pinned dependency variable accepted as stable after initialization; discharges exactly that variable's shared-dynamic-state downgrade, the load-bearing set recorded on the recording.
**when:** use run to measure and store — one pre-run observation
both drives the quiesce gate and is recorded as the run-conditions
provenance line, so the recording states exactly the conditions the
gate evaluated; storage overwrites with in-band provenance. After
edits only non-valid benchmarks re-measure; `all` re-measures every
selected one.
**example:** a run over ./... on a prepped machine after landing a
change — the unchanged benchmarks serve, the changed ones measure.

### status
**surfaces:** cli
**does:** Report each benchmark as valid, stale, unverifiable, or unrecorded.
**knobs:**
- `bench-dir` — stored-recordings directory (default <module>/benchmarks); an explicit value applies to every package.
- `label` — variant label to check; empty means the unlabeled recording.
- `stale` — show only benchmarks that need re-running (non-valid); scriptable, the set run measures by default.
- `explain` — explain each non-valid verdict: every guard's recorded vs current value, the closure hash, the runtime-input digest, and the manifest's watched identities — environment inputs disclosed as names with digest equality only, never values; a digest mismatch additionally names the moved watched inputs. Mutually exclusive with the JSON view (the explanation is a human view).
- `json` — one JSON object per row; the field names are public surface and stable (package, benchmark, label, verdict, reason; a per-package failure emits package and error).
- `vouch` — dynamic-state vouch IMPORT-PATH:VARIABLE (repeatable), the same acceptance set run records.
**when:** use status as the inventory-plus-verdict view before
measuring or comparing — the stale filter names what run will
measure, and the explanation view answers why a verdict is
non-valid without re-deriving anything by hand.
**example:** a stale-filtered status over ./... before deciding what
to re-measure.

### stat
**surfaces:** cli
**does:** Compare recorded benchmarks across git refs and flag regressions.
**knobs:**
- `bench-dir` — stored-recordings directory (default <module>/benchmarks).
- `label` — variant label to compare; empty means the unlabeled recording.
- `alpha` — significance level for the Mann-Whitney U test (default 0.05); outside (0,1) refuses.
- `threshold` — regression magnitude floor, in percent (default 3); negative refuses, zero means any significant worse change regresses — legitimate, noisier.
- `confidence` — confidence level for summary intervals; outside (0,1) refuses.
- `fail-on-regression` — exit non-zero if a gated metric regresses; an empty comparison then exits 2, so a CI consumer can tell measured-and-regressed from measured-nothing.
- `explain` — lay out the values behind a one-word skip or warning: a comparison key whose two sides disagree on a guard prints both sides' recorded values naming the moving guard, and a working-tree recording warned non-valid prints its recorded-vs-current explanation. Mutually exclusive with the JSON view.
- `json` — one JSON object per comparison row, note, or empty-comparison marker; the field names are public surface and stable, and internal values (guard digests, closure hashes) are deliberately excluded — they belong to the explanation view.
- `gate` — comma-separated units whose regression fails the build (sec/op, B/op, allocs/op; default sec/op).
- `vouch` — dynamic-state vouch IMPORT-PATH:VARIABLE (repeatable), the same acceptance set run records.
**when:** use stat as the comparison of record — it runs nothing,
comparing already-stored results, and the baseline mode follows the
argument count: no ref is auto (working-tree recording vs the
HEAD-committed one), one ref is pinned, two refs is A/B across
them. Run first; the text renderer is the default.
**example:** an auto comparison after a run, gated on sec/op, in CI
with the regression exit armed.

### gc
**surfaces:** cli
**does:** Remove stored results for benchmarks no longer in the code.
**knobs:**
- `bench-dir` — stored-recordings directory (default <module>/benchmarks).
**when:** use gc after deleting or renaming benchmarks — it scans
the module's benchmark declarations (build-tagged declarations
count as present, so a variant hidden by the current build config
keeps its recordings) and deletes recordings for what disappeared.
A recording failing the current format is never silently skipped:
gone-from-source removes it like any orphan, still-present keeps it
reported as format-stale pointing at a re-run; an unreadable file
is kept and reported with its error — removal never acts on unread
content; a package whose benchmark-source scan fails keeps all its
recordings behind the reported scan error. Foreign layout-matching
files without a pew marker are ignored.
**example:** a gc pass at a chunk close after a benchmark rename,
then a re-run for the renamed lineage.

### guidance
**surfaces:** cli
**does:** Serve this guidance: a verb's full section, or the decision map.
**knobs:** none
**when:** use guidance to learn what a verb does, what a knob
controls, and when to use which — the tool answers from its own
embedded document, so served prose and repository documentation are
the same bytes; the verb is the positional argument, and no verb
serves the decision map.
**example:** guidance stat before choosing comparison tunables.

## decision map

pew manages Go benchmark provenance, staleness, and comparison.
The loop: run measures with hygiene and stores with in-band
provenance — one pre-run observation drives the quiesce gate and is
recorded, so the recording states the conditions the gate
evaluated; status reports each benchmark valid, stale,
unverifiable, or unrecorded, its stale filter feeding run so only
non-valid benchmarks re-measure; stat compares already-stored
results across refs (auto, pinned, or A/B by argument count) and
flags regressions with the Mann-Whitney machinery — it never runs
anything; ab is the derivation loop's interleaved working-tree
comparison against a ref, writable-repository and crash-safe, whose
output is never a stat baseline; gc removes recordings for
benchmarks that left the code, refusing to act on anything unread.
Errors — a detected regression included — exit 1; the
fail-on-regression empty-comparison case exits 2, so a CI consumer
can tell measured-and-regressed from measured-nothing. The guidance
verb serves any verb's full section — knobs, when-to-use, example —
from the tool's own embedded document.
