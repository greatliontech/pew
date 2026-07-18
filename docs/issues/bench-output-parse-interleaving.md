# Benchmark-output parse hard-fails the package on one corrupted line

Lands: when `pew run` over a real benchmark-heavy package next fails with a `benchfmt` syntax
error, or before pew is pointed at a package whose benchmarks (or their dependencies) write to
stdout during measurement

## Fault (observed)

`pew run ./internal/db` in `github.com/greatliontech/protodb` (pew at e1e6a22, gofresh with the
declaration-key fix) fails the whole package after benchmarks have executed for ~30 minutes:

```
error        github.com/greatliontech/protodb/internal/db  (run: parse benchmark output: go test:1770: parsing iteration count: invalid syntax)
pew: run: 1 package(s) failed: github.com/greatliontech/protodb/internal/db
```

`internal/run/run.go` `Parse` surfaces any `benchfmt.SyntaxError` as fatal for the package
(run.go:101). Line 1770 of the `go test` stream held a line recognized as a benchmark result whose
iteration-count field did not parse — the shape produced when something writes to stdout between
`go test`'s un-newlined `BenchmarkX   ` name print and the result fields, splicing foreign bytes
into the result line.

A one-iteration control (`go test -run '^$' -bench . -benchtime 1x ./internal/db`) produced 182
well-formed benchmark lines and no stray output — the corruption appears only at full benchtime
(the failing stream was past line 1770 vs the control's 734 lines), consistent with occasional
mid-measurement stdout (a store/compaction log line under sustained load) rather than a
systematically malformed benchmark. The run above also executed alongside a heavily loaded machine
(load ~15, thermal-throttling warnings), which lengthens measurement windows and widens the
interleaving exposure.

This failure was previously unreachable over protodb: gofresh v0.10.4's build-variant-dependent
declaration keys failed the package at analysis, minutes before any benchmark ran (gofresh
`docs/issues/positionless-method-declaration-key-variant-ambiguity.md`, since fixed). The fix
unmasked it.

## Suspicion surface

`internal/run/run.go` `Parse` — one corrupted line discards an entire package's completed
measurements. `benchfmt` itself distinguishes a syntax error from a fatal read error; pew promotes
the former to fatal. The robustness call (skip-and-warn per corrupted line, quarantine the
affected benchmark, or fail as today) changes recorded-provenance semantics, so it needs its own
spec wording — filed rather than patched.

## Reproducer

`pew run ./internal/db` in the protodb checkout; intermittent by nature (depends on when the
benchmark's dependencies log during measurement), observed on the run recorded at
`~/.cache/gofresh-fix-pew-protodb-run1.txt`.
