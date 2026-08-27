# Standing vouch sets have no repo-level source

A store's reviewed dynamic-state vouch set exists only as command-line
flags: tugboat's audited six-entry set lives in prose (its CLAUDE.md)
and is mirrored by hand into the fleet sweep's gatherer (gofresh
scripts/fleet-sweep.sh) — two copies that must agree. Drift's safe
direction self-announces (a dropped vouch turns rows unverifiable);
the unsafe direction — an extra vouch suppressing a real unverifiable
— does not. A repo-level vouch source (a reviewed file beside the
store that pew/gomutant judged runs read, with the flags as override)
gives the set one home and makes the sweep's copy unnecessary.

Lands: cross-tool train chunk 115 (rides the pew verdict-surface
batch).
