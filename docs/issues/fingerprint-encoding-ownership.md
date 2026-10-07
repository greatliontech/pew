# Fingerprint encoding has two owners

Pew's recording table and `FingerprintConfigs`/`fingerprintFromConfig` independently
encode gofresh fingerprint fields. Gofresh's `REQ-fresh-fingerprint-data` requires
callers to store the library's native record form, not a second fingerprint encoding.

Adding observed-proof fields to the existing parallel mapping would extend this
contract conflict. The proposed repair is a canonical gofresh fingerprint payload
inside pew's artifact envelope, with pew-owned facts alongside it and display or
comparison fields projected from the admitted payload.

The wire contract, read admission, comparison projection and producer write must
change coherently. A clean format cutover is appropriate for the fleet-owned
deployment; historical records regenerate rather than gaining inferred evidence.
The schema must be settled before the proposed final reset to v1.

Lands: performance-evidence plan chunk 7 (the canonical persisted fingerprint boundary).
