# Observed fingerprints on the recording path

## Problem

Spec §7.8 states "Pew still selects no observability proof": the
recording path is plain `Capture`, so a benchmark whose closure
carries any true external effect (a syscall, a file read outside
declared scratch) is permanently `unverifiable` — it re-records on
every `--stale` campaign even though its per-arm runtime-input
manifest captures identities and guarded values that can contribute to a reuse
verdict. That manifest is not sufficient by itself: completion also needs
operation-outcome support, and gofresh's observed-evidence gate remains conditional
on that premise. Field: 44 of tugboat's 55 arms (everything reaching x/sys via
its wal) sit in this class as the freshness program's terminal
residue.

## Shape

After the shared outcome-support and native-record boundaries are established,
the recording path selects observed capture per arm. Single-subject execution
provides one contributing process per arm, not outcome evidence by itself. The
observed verdict substitutes for the closure's external-effect
unverifiability exactly as gofresh's gate defines, and §7.8's
no-proof sentence retires. Consumer-side residue the proofs surface
(per-arm startup effects — tugboat's bench scaffolding) is each
consumer's to clear; the refusal names them.

Lands: performance-evidence plan chunk 8, after shared outcome-support and canonical encoding are established.
spec-level change (§7.5/§7.8/§10 interactions: purity and external
directives, stat baselines for observed recordings).
