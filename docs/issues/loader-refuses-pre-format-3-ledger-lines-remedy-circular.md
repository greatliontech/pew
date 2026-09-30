# The store loader refuses pre-format-3 recordings whose ledger line exceeds the scanner bound, and the remedy it names cannot run

Field report from tugboat (filed uncommitted in this tree, the
standing channel), 2026-09-30, the installed pew built 2026-09-29
(fc59d09-era, gofresh v0.107.0).

`pew status` and `pew run` over tugboat's store refuse FIVE of its nine
package stores at load, one line each:

    error  github.com/greatliontech/tugboat/wal  (store: recording .../benchmarks/wal/BenchmarkAppendAsyncPipelined.txt carries a line past benchfmt's scanner bound, written under an earlier format — regenerate it with pew run)

and the same for internal/raft (BenchmarkIdleBeat.txt, longest line
229,992 bytes), lifecycle (BenchmarkDiscoveryProbe.txt, 131,797), node
(BenchmarkBarrier.txt, 356,248), transport/grpctransport
(BenchmarkExchange.txt, 80,095), wal (146,037). The long line is
`pew-test-variant-ledger: <base64 JSON>` in every case — the
pre-format-3 monolithic blob row (the two blob rows were chunked at
format 3, pew c28ac7e); the bound is bufio.MaxScanTokenSize, 64 KiB
(internal/store/store.go:717–732 refuses to WRITE such a line now,
which is right, and the loader refuses to READ the ones an earlier
pew wrote). A recording whose ledger line is under the bound
(transport/tcptransport/BenchmarkExchange.txt, 31,676) loads and
re-records normally.

The remedy the refusal names is circular: `pew run` over the package
loads the store first and hits the same refusal — nothing pew
offers reads or regenerates these recordings. The only way out is
deleting the files by hand, which discards the lineage every
`pew stat` comparison needs: tugboat's chunk 11 wanted its three
touched arms (wal.BenchmarkEntries/cold, node.BenchmarkReplicationFlow,
grpctransport's three) compared against their last recordings and
could not — the "before" curves exist only in files pew no longer
reads, and hand-diffing is forbidden there.

Two further consequences:
- `pew status`'s whole-store verdict is partially blind: a refused
  package contributes no rows, so a store report reads "10 stale"
  while five packages are unreadable; a consumer filtering the
  status output on the stale/unrecorded/unverifiable classes never
  sees the `error` rows (tugboat's chunk-6 close-out did exactly
  that and reported the store healthy).
- The weekly fleet sweep's row counts come from the same output and
  carry the same blindness (filed to gofresh as the gatherer's
  concern).

Expected: the loader reads a pre-format-3 recording whose only
over-bound line is the ledger blob — a reader with a larger token
bound (or a line-length-free reader) for that row, upgrading the
recording to the chunked form on the next write — so `pew run`
regenerates in place and `pew stat`'s lineage survives; and a store
refusal is a store-level verdict on the status face's tally line, not
an easily filtered `error` row.

Lands: awaiting triage.
