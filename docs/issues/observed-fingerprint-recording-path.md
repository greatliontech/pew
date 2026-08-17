# Observed fingerprints on the recording path

## Problem

Spec §7.8 states "Pew still selects no observability proof": the
recording path is plain `Capture`, so a benchmark whose closure
carries any true external effect (a syscall, a file read outside
declared scratch) is permanently `unverifiable` — it re-records on
every `--stale` campaign even though its per-arm runtime-input
manifest already captures the exact evidence a sound reuse verdict
needs, and gofresh's observed-evidence substitution gate exists and
works. Field: 44 of tugboat's 55 arms (everything reaching x/sys via
its wal) sit in this class as the freshness program's terminal
residue.

## Shape

The recording path adopts `CaptureObserved` per arm (single-subject
execution already gives each arm its own completed observation), the
observed verdict substitutes for the closure's external-effect
unverifiability exactly as gofresh's gate defines, and §7.8's
no-proof sentence retires. Consumer-side residue the proofs surface
(per-arm startup effects — tugboat's bench scaffolding) is each
consumer's to clear; the refusal names them.

Lands: its own train chunk — the recording path's verdict model is a
spec-level change (§7.5/§7.8/§10 interactions: purity assertions,
--impure, stat baselines for observed recordings).
