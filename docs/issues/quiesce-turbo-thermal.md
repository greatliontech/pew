# Turbo And Thermal Quiesce Checks

Lands: when changing quiesce checks, strict-mode run hygiene, or §9 quiesce documentation

## Fault

The spec lists turbo and thermal throttling among quiesce checks, but the Linux implementation covers
governor, battery, and load only. Under `--strict`, pew can still record while turbo or thermal state
violates the documented hygiene gate.

## Reconciliation

Either implement platform-specific turbo/thermal checks or narrow §9 to the checks currently enforced
and track the omitted checks as explicit additions.
