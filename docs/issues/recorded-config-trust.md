# Recorded-configuration trust residuals: whitelisted-value spoofing, historical foreign keys

Lands: when a spoofed toolchain key value or foreign-key grouping fragmentation is observed in a
real recording, or when read-side recording validation is next designed

## Gap (two residuals left by the closed recording key set, spec §5 / INV-12)

The closed-key-set enforcement is write-side and key-based; two adjacent trust gaps remain:

- **Whitelisted-key value spoofing.** A dependency logging `cpu: spoofed` (or `goos:`/`goarch:`/
  `pkg:`) has the same lowercase-colon shape benchfmt reads as configuration; the key survives the
  whitelist and benchfmt's same-key overwrite makes the spoofed value the recorded one for every
  subsequent result. `cpu` is not projected away in comparison grouping, so §10.1 fragments exactly
  as in the resolved foreign-key fault — a one-sided silent skip — while INV-12 holds. In-stream
  the toolchain's own emission is indistinguishable from a logger's; ordering rules cannot fix it
  (init output can precede the toolchain header). Real fix needs out-of-band truth: pew knows
  `goos`/`goarch` (build target) and `pkg` (the import path it ran) independently and could refuse
  or override mismatches; `cpu` has no in-run source of truth and needs its own disposition.
- **Historical foreign keys at read time.** Read paths do not police stored recordings: a
  recording written before the closed-set enforcement (or hand-edited) carrying a junk key passes
  the shape check, reads as valid, and fragments stat grouping with no warning. Regeneration is
  the remediation path; detection is the gap.
