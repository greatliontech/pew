# Buildconfig Guard Completeness

Lands: when changing buildconfig guard inputs or build-flag handling

## Fault

`buildconfig` currently hashes a small `go env` subset plus raw `GOFLAGS`. That misses build inputs
that can change generated code without changing source, toolchain, or machine identity.

Concrete false-valid paths:

- `CGO_CFLAGS=-DFAST=0` at record time, then `CGO_CFLAGS=-DFAST=1` at status time. The benchmark can
  compile different C paths while the recorded `buildconfig` still matches.
- `GOFLAGS=-pgo=/tmp/profile.pprof` and the profile file contents change without the flag string
  changing. The generated Go code can change while the guard stays fixed.
- `-pgo=auto` / `default.pgo` requires package-aware profile discovery; raw flag hashing is not a
  content guard.

This violates the §7 buildconfig guard: a code-generation input changed but `valid` can still be
reported.

## Reconciliation

Define and implement the exact canonical buildconfig input set. At minimum:

- architecture/codegen env: `GOOS`, `GOARCH`, `GOAMD64`, `GOARM`, `GOARM64`, `GO386`, `GOEXPERIMENT`
- cgo and external compiler/linker env: `CGO_ENABLED`, `CGO_CFLAGS`, `CGO_CPPFLAGS`, `CGO_CXXFLAGS`,
  `CGO_FFLAGS`, `CGO_LDFLAGS`, `CC`, `CXX`, `PKG_CONFIG`, `PKG_CONFIG_PATH`, `PKG_CONFIG_LIBDIR`,
  `PKG_CONFIG_SYSROOT_DIR`
- build flags from `GOFLAGS` and pew CLI pass-throughs that affect compilation
- PGO profile content, including explicit `-pgo=<path>` and `auto`/`default.pgo` behavior

Fail closed for unsupported or unparseable build-affecting inputs rather than emitting a digest that
can remain stable across different generated code.
