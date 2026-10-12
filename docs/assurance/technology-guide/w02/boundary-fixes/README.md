# Numeric, TLS trust and audit sampling repairs

These are three bounded W02 source repairs, qualified on immutable source
`9b75aacf` with a test-only TLS followup at `106e496e`. They do not close the
whole first-party source-security gate. [packet.json](packet.json) binds the
actual before/after logs, receipts and production file bytes.

The scanner now rejects fractional, nonfinite and unrepresentable `NumericField`
values. All five image-report severity aggregates must be nonnegative int32
values before transaction begin, metrics or persistence. Valid upstream counts
remain authoritative when individual CVE rows are absent; the injected,
authenticated cluster scope remains authoritative over payload fields.

The SIEM HTTPS factories now return the custom-CA parsing error instead of
constructing a fallback TLS client. Invalid clients are never cached. Syslog TLS
uses the same CA builder. Existing explicit operator skip mode, system roots,
custom-root verification, pooled reuse and corrected-policy recovery remain.

Read-audit sampling now uses one bounded crypto-random draw in `[0,1)`.
Configured zero/one rates retain their exact behavior without reading entropy.
An entropy failure selects eligible audit emission; existing read-audit delivery
remains best effort. Mandatory mutation auditing is separate and unchanged.

Before production changes, the four named scanner parents produced 36 failing
and 11 passing test events. The two SIEM parents produced six failures, including
an actual TLS request and deletion of a pending row despite a malformed custom
CA. The positive legacy-sampling characterization replayed 64 time-seeded draws;
its passing result demonstrates the old predictable behavior.

After the fixes, the same scanner scope passed 47 cases, SIEM and existing
dispatch/pool scope passed 24, and the new crypto-sampling scope passed nine.
All named scopes had zero skips/failures. The full scanner/tasks/middleware race
run passed 771 test events with zero failures. It skipped two existing database
cases because `CHARLIE_ALERT_POLICY_TEST_DATABASE_URL` was absent:

- `TestCharlieAlertDispatchDistributedFenceBlocksDisableAcrossProcesses`
- `TestDistributedFenceHoldBlocksQueuedCrossReplicaAdmissionUntilTransitionRelease`

This is not a zero-skip whole-package integration receipt. The separate TLS
followup race passed five parents and 21 cases with zero skips/failures, including
invalid DER inside CERTIFICATE/PRIVATE KEY PEM blocks under all three TLS
factories and both verification modes. The actual-peer fixture verifies no send
or deletion under malformed CA, recovery with a valid CA and skip disabled,
rejection of a later malformed policy, and supported explicit skip with no CA.

Vet passed for all three affected packages and again for the test-only followup.
Complexity and dependency boundaries passed without changed ceilings. Commands,
selected Go 1.27.2 runtime, capacity checks, event counts and clean-source state
are recorded in the three receipts. The followup changes only tests; prior
production bindings remain identical.

The SIEM investigation and fresh bypass review used the
`codex-security:fix-finding` skill. Its retained investigation, original failure,
after-fix log and review remain in the target-bound managed artifact collection;
the review path and exact digest are in the packet. The fresh review found no
remaining route through these factories. Standard x509 bundle parsing remains
compatible: a bundle containing a usable certificate is not assigned a new
strict PEM grammar. The local HTTPS fixture disables the destination guard only
within that test; production destination enforcement is unchanged.

Current-source gosec with zero engine errors, native govulncheck across all
first-party commands and remaining source findings still require qualification.
Historical finding counts are not current results. The next combined source
freeze must also refresh exact SQL/JSON ownership metadata; no generated-SQL,
TLS or numeric blanket exception is introduced by these repairs.
