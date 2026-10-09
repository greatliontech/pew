# pew — tool-resident guidance

## verbs

### ab
**surfaces:** cli
**does:** A/B-compare the working tree against a ref without touching either.
**knobs:**
- `bench` — benchmark pattern (go test -bench syntax) (default .).
- `count` — interleaved iterations per side (default 6).
- `profile` — capture cpu, alloc or cpu,alloc in independent single-subject diagnostic processes on each side after statistical pairs; bare selects both. Diagnostics carry identity-only evidence, never per-operation or causal estimates. Completed sides survive later failures. Successfully captured but incompatible profiles leave the request unfulfilled (exit 2); capture/publication failures exit 1, and interruption takes precedence with exit 130.
- `profile-benchtime` — positive duration or iteration budget per diagnostic process (default 1s).
- `json` — emit typed statistical and per-side diagnostic comparison reports.
- `benchtime` — per-benchmark time or iteration budget (go test -benchtime).
- `ref` — B side: any git rev the repository resolves (default HEAD).
- `pin` — pin both sides to one CPU set derived from the host's topology (taskset): the isolated set when the kernel has one, else one whole physical core, the fastest the kernel ranks, outside CPU 0's; the set and its derivation are reported first, and a host it cannot be derived on, or without taskset to apply it, refuses.
- `strict` — refuse to measure under noisy machine conditions.
- `worktree-dir` — directory for side B's worktree and both binaries, the repository's parent unless given; a placement on another device or inside the repository is refused. Residue is swept only with validated ownership, a dead owner and no Git registration. Cleanup is bounded and failures report retained paths.
- `out` — retain every completed package/pair in a versioned Go benchmark-format derivation artifact marked pew-ab/dirty, never a stat baseline. Before either side builds, refuse overlap with either side's selected recording stores (including permanent locks) or source/build inputs, and unsafe symlink destinations. This protection also applies without profiling and includes dependencies selected only by the ref. Diagnostic objects use the shared confined store rooted at <out>.profiles (objects beneath its .profiles/sha256); without out diagnostics are reported but not retained.
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
- `bench-dir` — stored-recordings directory, `<module>/benchmarks` unless given.
- `count` — measurement runs per benchmark (default 10).
- `benchtime` — duration or iterations per measurement (default 1s).
- `profile` — capture cpu, alloc or cpu,alloc diagnostics separately from statistical samples; bare --profile selects both. A served measurement can receive a missing or stale requested diagnostic without remeasurement. Profile failures retain published measurements and make the request fail; nonempty identity-only captures retain explicitly unverified evidence.
- `profile-benchtime` — positive duration or iteration budget for each single-kind diagnostic process (default 1s).
- `bench` — benchmark name pattern (default .).
- `pin` — pin the measurement to one CPU set derived from the host's topology (taskset): the isolated set when the kernel has one, else one whole physical core, the fastest the kernel ranks, outside CPU 0's; the set and its derivation are reported first, and a host it cannot be derived on, or without taskset to apply it, refuses. The pin's width is the measured process's GOMAXPROCS, a guarded runtime configuration: a pinned recording and an unpinned one stale each other and share a destination, so keep both under distinct labels. A run minting a new GOMAXPROCS variant lineage for a benchmark already on record warns at record time — grouping never bridges the suffix, and the operator must not learn that from a later comparison after the measurement time is spent.
- `strict` — treat quiesce warnings as fatal.
- `label` — variant label for the recording filename.
- `all` — measure every selected benchmark, a valid recording included; the default serves what is proven and measures the rest (status's closure-analysis path, over the run's own typed view, intersecting the independent benchmark selection and never adding or recording an excluded benchmark).
- `vouch` — dynamic-state vouch IMPORT-PATH:VARIABLE (repeatable): a version-pinned dependency variable accepted as stable after initialization; discharges exactly that variable's shared-dynamic-state downgrade, the load-bearing set recorded on the recording. A one-off acceptance extending the store's reviewed `vouches` file — one IMPORT-PATH:VARIABLE per line at the store root, `#` comments — the standing set every judged verb (run, status, stat) reads; a flag adds, never removes.
**when:** use run to measure and store — one pre-run observation
both drives the quiesce gate and is recorded as the run-conditions
provenance line, so the recording states exactly the conditions the
gate evaluated; storage overwrites with in-band provenance. After
edits only non-valid benchmarks re-measure, and each package's
served line counts the valid recordings the run did not re-measure;
`all` re-measures every selected one. Readable Pew-marked recordings with
unusable format or metadata re-measure in place, including oversized legacy
metadata; the old file stays intact until the fresh measurement commits.
**example:** a run over ./... on a prepped machine after landing a
change — the unchanged benchmarks serve, the changed ones measure.

### status
**surfaces:** cli
**does:** Report each benchmark as valid, stale, unverifiable, or unrecorded.
**knobs:**
- `bench-dir` — stored-recordings directory, `<module>/benchmarks` unless given; an explicit value applies to every package.
- `label` — variant label to check; empty means the unlabeled recording.
- `stale` — show only benchmarks that need re-running (non-valid); scriptable, the set run measures by default.
- `explain` — explain each non-valid verdict: every guard's recorded vs current value, the closure hash, the runtime-input digest, and the manifest's watched identities — environment inputs disclosed as names with digest equality only, never values; a digest mismatch additionally names the moved watched inputs. Mutually exclusive with the JSON view (the explanation is a human view).
- `json` — one JSON object per row; the field names are public surface and stable (package, benchmark, label, verdict, reason; a per-package failure emits package and error, and a recording-specific failure also names its benchmark and optional label).
- `vouch` — dynamic-state vouch IMPORT-PATH:VARIABLE (repeatable), a one-off acceptance extending the store's reviewed `vouches` file (one entry per line at the store root; the standing set every judged verb reads); the same acceptance set run records.
**when:** use status as the inventory-plus-verdict view before
measuring or comparing — the stale filter names what run will
measure, and the explanation view answers why a verdict is
non-valid without re-deriving anything by hand. Read or analysis failures leave
independent rows visible and make the command exit nonzero; stale, unrecorded
and unverifiable verdicts alone are successful reporting. Check the exit status
before treating a filtered report as complete.
Indexed profiles have independent integrity, freshness, relation and identity-only
outcome fields in text and JSON. Explain includes symbol weights and exact-source
attribution where supported; cumulative weights overlap, and unresolved weights
are retained. Empty profiles prove no absence of work. Profile read/integrity
failures make reporting incomplete while leaving the measurement verdict visible.
**example:** a stale-filtered status over ./... before deciding what
to re-measure.

### stat
**surfaces:** cli
**does:** Compare recorded benchmarks across git refs and flag regressions.
**knobs:**
- `profile` — compare requested cpu, alloc or cpu,alloc diagnostics; bare selects both. Each historical index, source snapshot and object is read at its recording ref, without current-tree fallback; package contexts are ref-local. Raw totals and normalized shares remain separate. Missing, empty or incompatible requested profiles exit 2 unless interruption or a statistical regression takes precedence; malformed indexes, corrupt or unparseable profiles, and operational read failures exit 1 while completed statistical reports remain visible.
- `bench-dir` — stored-recordings directory, `<module>/benchmarks` unless given.
- `label` — variant label to compare; empty means the unlabeled recording.
- `alpha` — finite significance level for the Mann-Whitney U test (default 0.05); outside (0,1) refuses.
- `threshold` — finite regression magnitude floor, in percent (default 3); negative refuses, zero means any significant worse change regresses — legitimate, noisier; a significant zero-to-positive cost clears every finite floor while its percentage remains undefined.
- `confidence` — finite confidence level for summary intervals (default 0.95); outside (0,1) refuses.
- `fail-on-regression` — exit 1 if an eligible gated metric regresses; otherwise empty eligible evidence or unsatisfied complete coverage exits 2. Interruption exits 130 ahead of either. Partial clean coverage still passes by default.
- `coverage` — partial accepts a nonempty clean eligible subset; complete requires every requested benchmark/unit obligation (default partial). Inventory includes current declarations for working-tree comparisons and recorded identities from both sides; historical comparisons claim only recorded coverage, never unrecorded historical declarations.
- `freshness` — report ordinary working-tree freshness warnings, or require proven valid working-tree evidence for gate eligibility; historical sides are not applicable (default report). Admitted numerical comparisons remain visible.
- `conditions` — report conditions, or require compatible known equal governor, turbo, throttled and battery values for gate eligibility; unknown is not proven compatible and load1 remains context (default report). Neither fingerprint nor grouping changes.
- `explain` — lay out the values behind a one-word skip or warning: a comparison key whose two sides disagree on a guard prints both sides' recorded values naming the moving guard, and a working-tree recording warned non-valid prints its recorded-vs-current explanation; mutually exclusive with the JSON view.
- `json` — one JSON object per comparison row, note, or empty-comparison marker; notes include additive code/details for dispositions and coverage. Both views report the same omissions and policies; internal values (guard digests, closure hashes) are deliberately excluded — they belong to the explanation view.
- `gate` — comma-separated units whose regression fails the build: sec/op, B/op, allocs/op (default sec/op).
- `vouch` — dynamic-state vouch IMPORT-PATH:VARIABLE (repeatable), a one-off acceptance extending the store's reviewed `vouches` file (one entry per line at the store root; the standing set every judged verb reads); the same acceptance set run records.
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
- `bench-dir` — stored-recordings directory, `<module>/benchmarks` unless given.
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
fail-on-regression empty-eligible or incomplete-required-coverage case exits 2, so a CI consumer
can tell measured-and-regressed from measured-nothing. The guidance
verb serves any verb's full section — knobs, when-to-use, example —
from the tool's own embedded document.
