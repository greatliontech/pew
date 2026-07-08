# Machine-readable output (`-json`) for `status` and `stat`

Lands: when pew is first wired into CI/scripting beyond exit-code gating

## Gap

`pew status` and `pew stat` emit only aligned-column human text. Scripting today means parsing that
text; the sole machine-readable signal is `stat --fail-on-regression`'s exit code. The spec already
anticipates scriptability (`status --stale` "scriptable; feeds `run --stale`", §12), but the feed is
line-format-fragile.

## Resolution

Add a `-json` flag emitting one JSON object per row: for `status`, {package, benchmark, label,
verdict, reason}; for `stat`, the comparison rows (name, unit, base/new center + CI, delta, p,
regression, gated) plus the not-compared notes. Keep the text renderer the default; the JSON shape
becomes public surface once shipped, so define it deliberately (stable field names, omit internal
values like raw guard hashes unless `--explain`-tier detail is requested).
