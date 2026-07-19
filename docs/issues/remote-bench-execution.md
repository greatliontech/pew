# Remote benchmark execution against a dedicated bench machine

Lands: when a dedicated benchmark machine is provisioned and pew measurements (dev or CI gate)
first need to run on it instead of the invoking host

## Motivation

Laptop/CI-runner measurements are noisy and machine-bound. The intended deployment is a single
quiet homelab machine that both dev measurement and the CI gate run against. Explicitly **not**
a goal: cross-machine comparison — one machine is the measurement substrate; the machine
fingerprint (§8) continues to bind recordings to it.

## Design conclusions (settled in discussion, to be spec'd on landing)

- **Transport & process model.** gRPC from day 1, carried over SSH: the client dials SSH
  (`golang.org/x/crypto/ssh`), starts a per-session `pew agent` on the box (git-upload-pack
  model — no daemon, no listener, dies with the session), and runs gRPC over a forwarded
  unix-socket/stdio channel. A later `pew server` mode is the same gRPC service on a TCP
  listener; nothing else changes.
- **Serialization.** One benchmark session owns the machine at a time via a machine-wide lease
  (flock held by the per-session agent process; kernel releases it on any death). Queueing is
  waiting on the lease.
- **Off-box builds.** The client compiles test binaries locally (`go test -c` per package,
  target GOOS/GOARCH) and ships binary + testdata; the box never checks out source or builds.
  Guard split: toolchain/buildconfig guards evaluate at build time on the client; machine,
  runtime, and quiesce guards evaluate at run time on the box. The shipped binary's digest is
  the bridging identity between the two halves.
- **Stale-set resolution stays client-side.** The client probes box conditions first (machine
  fingerprint, quiesce), resolves staleness locally against the repo store under the *target*
  build configuration, compiles only packages with stale benchmarks, selects via `-test.bench`
  regex, and uploads digest-negotiated (content-addressed cache on the box skips re-uploads).
- **Box state.** Stateless except: the content-addressed upload cache, and a machine-local
  calibration baseline.
- **Calibration as drift vet.** A small fixed calibration workload runs per session; its values
  are compared against the box's stored baseline to vet drift (thermal, firmware, background
  load) and ride the run-conditions provenance of the session's recordings. Calibration values
  are **not** part of guard fingerprints — they gate/annotate, never identify.
- **Comparison model.** Stored-baseline comparison stays the default (reuse of past
  measurements is the point). Interleaved A/B within one session is an escalation only: delta
  inside the noise band, calibration drift observed, or a gate about to fail.
- **Proto layering.** The gRPC schema layers a generic evidence-record envelope separately from
  pew's payload messages, so a future shared evidence library (if gofresh/stipulator/gomutant
  converge on one) can lift the envelope without a wire break. Do not build the shared library
  first — extract only when the same shape has landed in all three stores.

## Open design decisions (settle at spec stage on landing)

- Calibration workloads: pew-owned fixed suite vs repo-declared.
- Exact envelope message shape and its versioning discipline.
