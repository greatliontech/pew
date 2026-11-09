# Consolidate internalized CLI, MCP, and child-process support packages

**Lands:** User-authorized deferral until triggered during dedicated cross-tool CLI/MCP substrate consolidation (authorized during `gofresh` single-package surface purification).

## Context & Duplicated Surface

When `github.com/greatliontech/gofresh` was purified into a single-package freshness analysis library with zero public subpackages, non-freshness CLI/MCP presentation and child-process support packages were evicted from `gofresh` and internalized into `internal/` of `pew`, `stipulator`, and `gomutant`:

- `internal/guidance`: Markdown verb/knob guidance parser and formatter for CLI help and MCP tool descriptions (`pew`, `stipulator`, `gomutant`).
- `internal/toolversion`: Build-info version and `gofresh` dependency provenance formatter for `--version` and MCP server metadata (`pew`, `stipulator`, `gomutant`).
- `internal/gotool`: Child `go` command execution, process-group containment (`SIGQUIT` stack dump + grace period before `SIGKILL`), `go env -json` snapshot memoization, and environment normalization (`pew`, `stipulator`, `gomutant`).
- `internal/shortgates`: AST verifier ensuring `testing.Short()` gates live directly in top-level `Test*`/`Fuzz*` bodies (`pew`, `stipulator`, `gomutant`).
- `internal/shapecorpus`: Shared Go language-shape canary corpus (`pew`, `stipulator`).
- `internal/trace`: Zero-disabled-cost structured diagnostic tracing (`GOFRESH_TRACE` / `GOFRESH_TRACE_FILE`) interleaved with `gofresh` (`pew`, `stipulator`, `gomutant`).

Additionally, `cmd/pew` owns small CLI helpers evicted from root `gofresh` (`readVouchFile` in `cmd/pew/vouchfile.go`, `testCompileArgs` in `cmd/pew/built_binary.go`, and `progressDiagnostic` / `diagnosticsTo` / `progressIsUnit` in `cmd/pew/status.go`).

## Consolidation Path

When triggered, extract the shared non-freshness CLI/MCP/process utilities (`guidance`, `resident`, `toolversion`, `gotool` process-group supervisor, `shortgates`, `shapecorpus`, and `trace`) into a dedicated shared support module outside `gofresh`, switch `pew`, `stipulator`, and `gomutant` to import that module, and delete the duplicated `internal/` copies across all three tools.
