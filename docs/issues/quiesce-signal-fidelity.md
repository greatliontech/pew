# Quiesce signals mis-state some real machine states

Lands: when a recorded `pew-runconditions` value or quiesce warning is observed wrong on a real
machine (mixed-policy governor, boot-stale throttle count, or conflicting turbo signals)

## Gap

The Linux quiesce observation (`internal/run/quiesce_linux.go`) feeds both the §9 warnings and the
recorded `pew-runconditions` provenance, and three of its signals can mis-state the machine:

- **Governor is read from `cpu0` only.** cpufreq policies are per-core (or per-cluster); a box with
  `performance` on policy0 and `powersave` on the cores the benchmark lands on records
  `governor=performance` and warns nothing. Symptom: a noisy run recorded as quiet.
- **`thermal_throttle/*_throttle_count` counters are cumulative since boot.** One throttle event at
  boot makes every later run warn "thermal throttling observed" and record `throttled=true`
  indefinitely, even on a cold quiet machine. The signal conflates "has throttled since boot" with
  "throttling around this run"; distinguishing them needs a pre/post-run counter delta.
- **Conflicting turbo signals resolve toward enabled.** With `intel_pstate/no_turbo = 1` (turbo off)
  and `cpufreq/boost = 1` both exposed, the observation reports turbo enabled — either-signal-on
  wins. On intel_pstate systems `no_turbo` is authoritative; the current rule can warn and record
  `turbo=on` on a machine whose turbo is actually off.

All three predate the recording feature (they shaped only warnings before); recording makes the
values durable, which raises the cost of the inaccuracy.

## Resolution sketch

Walk every `cpufreq/policy*/scaling_governor` (record a single value only when uniform, else a
mixed marker), snapshot throttle counters before and after the run and report the delta, and give
`intel_pstate/no_turbo` precedence over `cpufreq/boost` when both are exposed. Each changes
warn/record semantics, so it needs its own spec §9 wording and test fixtures.
