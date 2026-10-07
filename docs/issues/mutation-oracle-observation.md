# Mutation campaign observation and close-out

Lands: performance-evidence plan chunk 8 (the cwd-relative refusal). The campaign's other half — it stopped reporting about two minutes short of its deadline and its close-out never returned — is gomutant docs/issues/campaign-silent-past-its-deadline.md, landing at cross-tool train chunk 267 (audit 335).

The changed-code campaign over newStatCmd, validateOptions and Compare measured
24 candidates and banked three machine-local records (22 killed, no open survivors),
but reached its command deadline during final evidence work. Its derived oracle
includes CLI integration tests which change working directory; observation reports
`relative runtime input after working-directory change: cmd/pew`. Bounded freshness
proof passes also explicitly reported unproven subjects.

The execution evidence does not establish reusable freshness evidence or a successful
campaign close-out. Keep those facts distinct. The existing targeted ephemeral
probes remain specific kill evidence, not a substitute claim of exhaustive campaign
coverage.

At observed-capture integration, determine which refusal belongs to the test's
actual runtime-input contract and which belongs to shared observation/containment
or campaign close-out. Fix shared faults in their owning tool. Do not exclude
integration tests or add purity vouches merely to obtain a reusable verdict.

A subsequent JSON-rendering campaign banked its sole target with both mutants
killed, then stopped reporting progress after roughly eight minutes despite a
ten-minute command deadline; the enclosing thirty-minute shell deadline terminated
it. No campaign process remained afterward. Investigate deadline propagation and
final evidence work in gomutant/gofresh before treating this as ordinary bounded
non-reuse. The result is measured execution evidence with incomplete close-out,
not a passing campaign.
