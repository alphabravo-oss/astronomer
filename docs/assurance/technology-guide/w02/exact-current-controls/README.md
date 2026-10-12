# Exact current source controls

The source gate remains **FAIL**. Replaying the qualified native report retains
all 566 findings, accepts exactly 66 reviewed controls, and leaves 408 HIGH
findings unaccepted. MED findings still require exact owned reviews. This slice
does not close W02 or approve a release.

The 63 added controls cover 62 exact SQL constants and one synthetic native
AES-GCM probe. Each binds the tool, rule, path, line, column, whole-file digest,
finding fingerprint, scope, and expiration. SQL credential values remain bound
parameters or database columns. The probe uses Go 1.27.2's
`NewGCMWithRandomNonce`, whose empty external nonce argument is required while
the wrapper generates and prepends a random encryption nonce. The root reviewed
these precise static dispositions. Three unchanged reconnect-jitter controls
remain; no generated-file or rule exemption was added.

The obsolete sampler exception was removed after the crypto sampling fix. The
stale audit-filter G109 binding was also removed. Static review confirms its
current 0–599 guard before narrowing, but the new binding remains unaccepted
until its exact review is completed.

The named missing-control regression failed before the change. All 20 policy
fixtures now pass, including new credential sites within the same generated
file, a new generated file, changed SQL/probe source, a different fixed-nonce
site, expiration, and wrong scope. These fixtures test policy enforcement; they
do not substitute for native collection or product behavior tests.

[Receipt](receipt.json) binds the source inputs, commands, results, and artifact
hashes. [Retained policy result](retained-policy-result.json) records the exact
accepted set and remaining failures. The native report was collected at
`927151be`; its affected source bytes remain unchanged. No compiler or native
scanner ran for this policy metadata slice. The original
[qualified collection](../current-source-927151be/README.md) remains immutable.
