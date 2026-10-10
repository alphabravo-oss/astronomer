# Verified database base binding

This bounded source slice adds an optional frozen update binding to the existing
distribution loader. Ordinary loaders without it continue their existing signed
distribution behavior and cannot return an update base. Development mode cannot
claim that binding. The base accessor first runs the same canonical parent/child
verification and closed inventory checks, then matches the constructor's exact
release/distribution/catalog/profile intent and the actual reviewed scanner
version/digest against the loaded values and image inventory.

`VerifiedDatabaseBase` has private fields and returns scalar copies. Neither a
zero object, a diagnostic `Loader.Status`, edited caller options after construction
nor an edited output snapshot can replace this provenance. The policy fingerprint
is a domain-separated, length-framed digest of the public verification constraints;
it is not a fingerprint of physical key/TUF root files. Candidate source commit
is independently injected build intent; the existing catalog BOM does not sign
a candidate source claim. The durable repository must bind the deployed build,
actual immutable trust snapshot/epoch and current source/release revocations.

The accessor records effective scanner enablement without turning it into a
caller authorization boolean. A verified profile with scanning disabled can be
described, but the later opaque-proof acceptance repository must refuse refresh
projection for that profile. This slice does not implement that repository,
generation reservation, update acceptance, scan-use measurement or API/UI.

The new tests use the existing real in-process keyed QA registry and canonical
signature consumer for a descriptor-only profile whose scanner is disabled.
They cover exact loaded base binding, missing/unsigned inputs, different scanner,
constructor mismatch, snapshot isolation, cancellation and framed public policy
identity. That controlled QA identity does not qualify production OIDC, payload
freshness or a running scan. Tests are source-prepared and have not executed yet;
the compiler lease is owned by the separate W07 qualification.

Static Node 26 checks pass for dependency boundaries and unchanged complexity
ceilings after checking out the required frontend source paths. The initial sparse
checkout lacked those paths and produced missing-root/stale-baseline failures;
their reproduced raw logs are retained in the private reviewer packet. No
baseline was regenerated and no exception or gate was weakened.

Migration 079 remains reserved and unapplied. Next source work is relational
current-base/attempt/update authority, sqlc statements and atomic acceptance/
revocation with separate report receipt/upstream timestamps. Actual PG17/18
transactions and selected scanner/no-egress evidence remain required separately.
