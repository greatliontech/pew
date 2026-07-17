# Buildconfig Guard Completeness — PGO profile content & CLI build flags

Lands: when PGO-driven benchmarks see real use, or when `pew run` grows build-affecting CLI
pass-throughs (`-tags`/`-gcflags`/`-pgo`)

## Gap

Two build-affecting inputs change generated code without moving the `buildconfig` guard:

- **PGO profile content.** `GOFLAGS=-pgo=/tmp/profile.pprof` is captured only as the flag *string*;
  the profile file's *contents* can change while the digest stays fixed. `-pgo=auto` / `default.pgo`
  needs package-aware profile discovery — raw flag hashing is not a content guard.
- **CLI build-flag pass-throughs.** If `pew run` grows `-tags`/`-gcflags`/`-pgo` flags (not yet
  present), those must feed the digest before they are exposed (spec §9), or a flag change produces a
  false `valid`. The mechanism now exists upstream: gofresh's `WithBuildInputs` folds caller-supplied
  invocation inputs (flag strings, PGO content digests) into the buildconfig guard; pew passes none
  because it exposes none.

## Design tension to resolve on landing

`buildconfig` is a **single global per-run** config line (§5), but PGO profiles are **per-main-
package** (`default.pgo` lives in each package's dir; `go test ./...` can build several). A single
global digest cannot cleanly represent per-package profiles. Options: fold PGO profile content into
the per-benchmark `pew-closure` (it is a codegen input for the reached functions, like `-gcflags`),
or make `buildconfig` per-package. Decide before implementing.

Fail closed for unsupported or unparseable build-affecting inputs rather than emitting a digest that
can remain stable across different generated code.
