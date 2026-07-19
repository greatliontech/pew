# Plan: issue burn-down

Spec: `docs/specs/spec.md`. Executes every open issue doc; remote execution is tracked as a
future improvement (`docs/issues/remote-bench-execution.md`), not part of this plan.

- [x] 1. Triage gate; gofresh v0.11.1 bump; file the remote-bench-execution issue doc
- [x] 2. Stream configuration key poisoning: spec §5 wording for recordable stream keys + enforcement
- [x] 3. Stale-shape recording visibility: surface shape-failing recordings in `stat`; report/remove in `gc`
- [ ] 4. Quiesce signal fidelity: all-policy governor scan, throttle-counter delta, `no_turbo` precedence (§9)
- [ ] 5. Buildconfig completeness: PGO profile content guarded; per-package tension resolved
- [ ] 6. Durable impure directive: gofresh grammar + engine/caller decision, pew adoption
- [ ] 7. `pew stat --explain`: recorded-vs-current guard view, manifest-decoded input naming
- [ ] 8. `-json` output for `status`/`stat`
- [ ] 9. Close-out gate
