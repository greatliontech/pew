# `pew run --stale` discards the user's `--bench` pattern

Lands: when `pew run` benchmark selection or `--stale` handling is next changed

## Fault

`cmd/pew/run.go` (`runPackage`): under `--stale`, the non-valid set is computed from *all* declared
benchmarks — `selectedBenchmarks` parses the test files and never applies `rc.opts.Bench` — and the
resulting alternation then overwrites `opts.Bench` wholesale:

```go
need, err := nonValid(st, h, ..., benches, prov)   // benches = every declared benchmark
...
opts.Bench = "^(" + strings.Join(need, "|") + ")$" // user's --bench value discarded
```

Concrete failure: `pew run --stale --bench '^BenchmarkFoo$'` in a package where `BenchmarkBar` is
also stale reruns **both**, re-recording `BenchmarkBar` (overwriting its stored file) although the
user explicitly excluded it. The cost is a silent expensive rerun plus unintended recording
overwrites.

Spec §12 defines `--stale` as "(re)run only benchmarks currently stale/unverifiable/unrecorded" and
`--bench <pat>` as an independent selection knob; combining them should intersect.

## Resolution

Filter the declared benchmark list by the user's `--bench` pattern before computing the non-valid
set (matching `go test` semantics: the pattern's first slash-separated element applies to the
top-level function name), then build the alternation from the intersection. Regression test:
`--stale --bench` with two stale benchmarks, only the matching one reruns.
