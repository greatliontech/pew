# Two per-verb enumerations of one CLI contract

Spec §12's verb defaults table and the guidance document's per-verb
knob blocks (`docs/guidance.md`, the fleet format gofresh's guidance
spec defines) both enumerate every verb's flags and defaults. Each is
bound to the flag set by its own test — the table's flags, defaults,
and opt-in purposes by `TestSurfaceTableTracksTheCommands`, the
guidance's flag names and stated literal defaults by the same test
and `TestGuidanceCoversTheCLISurface` — so neither can drift from the
code, but the prose is written twice and can drift from itself in
purpose wording.

The collapse: one source. Either the guidance knob blocks are
generated from the table (the table gains the knob text; the guidance
format's `**knobs:**` section is emitted), or the table is derived
from the guidance document plus the flag defaults and the spec keeps
only the behaviour column. Both change what the fleet guidance format
carries, which spans the train's tools.

Invariants preserved: the CLI's flag set and defaults keep one
enforced contract; the served guidance stays the tool's own document.

Lands: user decision
