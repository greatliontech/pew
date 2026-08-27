# Installed pew binary lags the repo HEAD (fleet sweep 2026-08-27)

The weekly fleet sweep's binary-provenance check found the installed
`pew` binary's `vcs.revision` (be03654e9e6f) behind the repo HEAD
(d3bf2301731b). The skew guards refuse loudly at use; the sweep's job
is to catch the drift before a session trips on it, and this doc is
that catch made durable.

Undiagnosed whether the lag is a missed install at the last landed
change set's close (the binary is rebuilt from the landed HEAD as part
of every change set that ships) or a deliberate hold at an earlier
revision; either way the installed binary is not the reviewed HEAD,
and every judged run on this machine (tugboat's and protodb's stores,
the sweep itself) runs the older code.

Lands: cross-tool train chunk 134 (its pew leg — pew's stipulator
adoption — is the next pew change set; its close reinstalls the binary
at the landed HEAD, and the sweep's next report confirms `match`).
