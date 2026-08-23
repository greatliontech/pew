# go1.27 generic-method shape breaks closure analysis ("unsupported analysis shape: Int")

Filed from a protodb baseline re-record attempt, 2026-08-24. Sibling
filing with the same signature: gomutant
docs/issues/go127-generic-method-closure-analysis.md — the shared
root is almost certainly the gofresh analysis layer both tools ride
(pew go.mod: gofresh v0.76.0; gomutant recently bumped to v0.82.0 and
fails identically), not either tool's own code.

## Symptom

On a go1.27 toolchain (go1.27.0-X:nodwarf5), `pew run --label
pebble-tugboat ./internal/db ./internal/storage/kv/pebblekv` fails
both packages with:

    error  <pkg>  (closure: attributed reachability: unsupported
    analysis shape: Int)

The shape is go1.27's new generic method in `math/rand/v2`
(rand.go:213, `Int[T]`) — any package whose closure reaches it is
unanalyzable, which for protodb is the whole benchmark surface.

## Impact

The substrate-swap baseline re-record (pebble-dragonboat →
pebble-tugboat) is blocked: no recording, so no cross-label swap
delta and no regression gate under the new label. protodb has
reverted its Taskfile label flip and redeferred the re-record on this
issue (protodb docs/issues/2026-08-23-bench-stack-label-flip.md).

## Ask

Same as the gomutant filing: teach the analysis the generic-method
shape, or degrade per-edge with the imprecision named, rather than
refusing the package. If the fix lands in gofresh, both tools need
the bump.

Lands: with the gofresh generic-method analysis fix (the owning
filing is gofresh docs/issues/go127-generic-method-closure-analysis.md;
the shared engine owns the failure), then a pew bump to the fixed
gofresh — the same bump as the six-version integration's.
