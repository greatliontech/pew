# The gofresh bump spans six versions and a recording-format field — integrate once, deliberately

The installed pew binary sat at gofresh v0.70.0 while the module
pinned v0.76.0 and the sibling tools ride v0.82.0 — months of judged
runs on a stale engine (surfaced 2026-08-13 during the tugboat
wal-unverifiable investigation; the go1.26-built binary was
additionally rebuilt on go1.27 on 2026-08-24, which fixed the
toolchain parse skew but left the version pin at v0.76).

A naive bump to current gofresh broke nine tests on first attempt
(reverted): recordings predating the DynamicStateStrategy field fail
to read — the recording format needs the integration to decide
whether the field back-fills (a clean-break re-record is available:
the go1.27 toolchain bump already staled every recording lineage
fleet-wide, so the stores re-record regardless and format backcompat
has no installed base to serve).

The work, one visit: bump to the gofresh carrying the go1.27
generic-method analysis fix (its own filing rides this repo too);
absorb the six versions' behavior deltas against pew's judgement
surface (vouch channel, discharge reasons, verifiability classes);
decide the DynamicStateStrategy recording-format question under the
pre-v1 clean-break rule; then RE-JUDGE the tugboat wal-unverifiable
reading that motivated the investigation — the verdict was reached
on the stale engine.

Lands: with the tool-phase pew visit, after the gofresh go1.27 fix
(the same bump carries both).
