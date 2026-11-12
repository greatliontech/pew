# Stipulator Witness Policy & Test Cache Isolation Plan

## Chunk 1: `cmd/pew` Test Cache Isolation & Core/Leaf `.stipulator/policy.textproto`
- [ ] Isolate `XDG_CACHE_HOME` to `t.TempDir()` (resolving and preserving `GOCACHE` via `go env GOCACHE` beforehand so `GOENV=off` child `go` commands do not cold-rebuild stdlib) across `cmd/pew` tests that invoke `pew` or `gofresh`
- [ ] Add `.stipulator/policy.textproto` splitting `race:core` (`./cmd/pew` at wide `GOMAXPROCS`) from `race:leaf` (`.` and `./internal/...`) with reviewed `bracket_paths` (`/bin/sh`, `/usr/bin/sh`, `/usr/bin/git`, `internal`)
- [ ] Verify cold and warm `stipulator check` witness caching across all `pew` packages after `gofresh` and `stipulator` freshness fixes land, adding explicit vouches or pure directives only where genuine non-derivable external blindspots remain
