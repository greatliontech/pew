# Buildconfig Guard Completeness — PGO profile content & CLI build flags

Lands: when PGO-driven benchmarks see real use, or when `pew run` grows build-affecting CLI
pass-throughs (`-tags`/`-gcflags`/`-pgo`)

## Resolved (landed)

The environment-input half of this gap is closed: `buildconfig` now digests the full codegen
feature level and cgo toolchain environment (`CGO_*FLAGS`, `CC`/`CXX`, `PKG_CONFIG*`) alongside
`GOFLAGS`, with host `GOOS`/`GOARCH` deliberately left to the machine guard (§8). The canonical input
contract is stated at spec §7 guard 5 and pinned by `TestBuildConfigCapturesBuildAffectingEnv`
(each hashed key moves the digest). The `CGO_CFLAGS=-DFAST=0 → -DFAST=1` false-valid no longer
reproduces.

## Remaining fault

Two build-affecting inputs still change generated code without moving the guard:

- **PGO profile content.** `GOFLAGS=-pgo=/tmp/profile.pprof` is captured only as the flag *string*;
  the profile file's *contents* can change while the digest stays fixed. `-pgo=auto` / `default.pgo`
  needs package-aware profile discovery — raw flag hashing is not a content guard.
- **CLI build-flag pass-throughs.** If `pew run` grows `-tags`/`-gcflags`/`-pgo` flags (not yet
  present), those must feed the digest before they are exposed (spec §9), or a flag change produces a
  false `valid`.

## Design tension to resolve on landing

`buildconfig` is a **single global per-run** config line (§5), but PGO profiles are **per-main-
package** (`default.pgo` lives in each package's dir; `go test ./...` can build several). A single
global digest cannot cleanly represent per-package profiles. Options: fold PGO profile content into
the per-benchmark `pew-closure` (it is a codegen input for the reached functions, like `-gcflags`),
or make `buildconfig` per-package. Decide before implementing.

Fail closed for unsupported or unparseable build-affecting inputs rather than emitting a digest that
can remain stable across different generated code.
