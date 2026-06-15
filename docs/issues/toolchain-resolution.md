# Capture and run must share one resolved toolchain

Lands: 5 — when `pew run` selects the `go` toolchain that runs benchmarks.

## Context

`internal/provenance` (chunk 2) captures `toolchain` (`go version`) and `buildconfig` (`go env`)
from the `go` on PATH at capture time. Chunk 5 will run benchmarks via `go test` — using some `go`.

## The risk

If chunk 5 runs benchmarks with a different toolchain than the one captured here — a `go1.X` shim, a
`GOTOOLCHAIN` redirect, an explicit `--go` flag — the recorded `toolchain`/`buildconfig` describe a
different binary than the one that built the benchmark, opening a guard-2/guard-4 hole (a stale
result could read `valid`). Capture is self-consistent within chunk 2; the hazard is only at the
chunk-5 seam.

## Resolution

Chunk 5 resolves the toolchain **once** (the `go` it will invoke) and feeds that same binary into
provenance capture, so `toolchain`/`buildconfig` always describe the toolchain that actually ran the
benchmark. Promote this into the run/provenance wiring with a test, then delete this doc.
