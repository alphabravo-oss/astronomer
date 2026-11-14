> **Archived 2026-11-14. Historical record only.** See [the sole current working plan](../../../../../../CURRENT.md). This document does not authorize new work or certify the current candidate.

# Dex registration boundary batch — 2026-10-23

> **Scope correction (2026-10-23): historical Dex work, superseded.** The owner
> requires Dex removal and adoption of the existing AlphaBravo Identity component.
> Follow [Plan 032 §20.13](../../../../032-technology-guide-alignment-and-latest-stable-master-plan.md#2013-shared-identity-adoption-and-dex-retirement--scope-correction-2026-10-23).
> These test results remain evidence of the tested source, not release requirements
> or permission to continue Dex hardening. Preserve uncommitted work for selective
> disposition; do not merge it wholesale.

Implements Plan 032 §20.11 in `/root/work/astronomer-fixes`, based on
`e2223136030fa17541e6be082a511caf444b7067`. Changes are uncommitted and undeployed.

## Findings and changes

The first real PostgreSQL run exposed a production migration defect before any
registration request: migration 82 dropped `dex_settings.public_clients`, but the
JSON registry still required it. All Dex settings writes failed with SQLSTATE 23514.
Migration 86 removes only that obsolete registry row. A real migrated-database
regression verifies that every registry contract references an existing column and
that Dex `expiry`/`extra` remain governed. The initial failure is retained in
`registration-tests.log`; the initial full gate was stopped (exit 143) before the
fix. Task 10 includes this necessary migration repair.

Registration treated every provider lookup failure as though the provider did not
exist. With an explicit replacement secret, it could proceed to stage credentials
and queue an operation despite that read failure. It now permits only `pgx.ErrNoRows`
as absence; other errors return a bounded 503 before mutation. The old test double
now uses the actual no-row sentinel instead of a generic error for missing providers.

The worker previously decrypted its registration payload after Kubernetes runtime
reconciliation. Invalid operation credentials could therefore cause external effects
before being rejected. Payload decryption, strict JSON decoding, required fields,
nested credential decryption, and equality with the staged static client now precede
reconciliation. Decrypted byte buffers are cleared; secret comparison uses constant
time comparison. Unsupported actions are rejected before dependency access. Existing
runtime generation checks still reject superseded operations.

## Ten tasks and evidence

1. Real PostgreSQL admission verifies 202/Location/Retry-After, one encrypted
   operation/outbox/audit, generation increment, provider disablement, preserved old
   provider ciphertext, correct queued credential pair and callback URI.
2. Same-key/same-input replay preserves the full settings/provider/operation records
   and the three durable record counts.
3. Same-key/different-input returns 409 and preserves those exact records/counts.
4. Provider read failure is injected by renaming the provider table in the isolated
   migrated database. The endpoint returns 503; after restoring the table, both
   complete credential-bearing records compare equal to their pre-request values.
5. Platform read failure uses the same real PostgreSQL technique; settings/provider
   records remain equal and no operation, delivery intent or mutation audit appears.
6. A PostgreSQL trigger rejects staging after operation reservation; the transaction
   rolls back the reservation and leaves both credential stores unchanged.
7. A PostgreSQL trigger rejects audit insertion after staging and queueing; rollback
   restores provider enablement, settings, operation and outbox together.
8. Actual durable worker dispatch rejects corrupt envelopes, malformed/unknown-field
   payloads and missing required fields before Kubernetes access.
9. Worker dispatch rejects the wrong client, mismatched secret and corrupt nested
   credential before Kubernetes access, preserving both stored records.
10. Unsupported action and stale generation fail before Kubernetes effects. Updated
    constructor comments describe durable acceptance; plans and final gate results
    are reconciled here.

The database uses testdb's PostgreSQL 17.11 with all production migrations. SQL
fault injection occurs only in disposable test databases. No mock database or
successful simulated Kubernetes response is used. The Kubernetes guard panics on
any request; negative tests prove the boundary is never crossed. Ordinary admission
uses no Kubernetes dependency. This is not a workload rollout test.

## Verification artifacts

- `registration-tests.log`: initial failure exposing the orphan contract.
- `registration-tests-final.log`: **53 handler/migration checks passed**, including real PostgreSQL registration/rollback and migration concurrency/idempotence, with the race detector. The selection also includes existing `TestUpdate*` handler checks.
- `make-check.log`: intentionally stopped initial gate.
- `make-check-final.log`: first completed gate exposed the chart schema bound and a workspace capacity failure.
- `make-check-verified.log`: full gate with only parallel-run capacity failures remaining.
- `make-check-complete.log`: final repository gate after cache-capacity recovery.
- `security-complete.log`, SARIF files and `working-source-secret-scan-complete.log`: final security gate passed, gosec 0 findings, govulncheck 0 reachable vulnerabilities (1 module-only finding), 2,304 history commits secret-scanned clean; changed source also clean. Initial scan logs are retained.
- `source-manifest.json`: frozen hashes including all earlier remediation sources.
- `source.patch`, `new-registration-test.go.txt`: reviewable source evidence.
- `verification-summary.json`: final outcomes once verification completes.

The frozen manifest contains 103 files. The only changes to the prior 93-file
manifest are `dex_operations.go` and the missing-row sentinel in `dex_config_test.go`;
the previous lease fencing and all other batch source hashes remain intact.

## Remaining work and plan reconciliation

The seven legacy Dex rollout/provider scenarios remain open; this batch does not
remove any skipped test. It advances the queued atomic-failure and platform-read
credential-pair subcases with real PostgreSQL, but does not prove a successful real
Dex rollout, provider activation/restoration, retry after rollout failure, or fresh
operation fixed-point behavior. Existing generation/resource-version guards and the
previous attempt/lease fencing remain intact. Invalid durable input currently follows
the bounded retry path; no new terminal-error classification is introduced.

Plan 026 retains Dex rollout, Charlie suspension and pagination timing work. Plan 016
retains immutable release/deployment and external/DR acceptance. Plans 028/029/033
retain independent-host/task-owned-target prerequisites, mutation runner and 174
NOT_RUN lifecycle cases. The last shared-system-UUID finding is unchanged; this
batch performs no live cluster mutation or requalification. Original ui-demo is
preserved.

The first completed full gate exposed two further prerequisites: the chart still
advertised target schema 85, and the artifact capacity guard rejected writes after
the workspace dropped below its 20% reserve. The chart now advertises schema 86.
About 3.18 GB of abandoned Go temp files from the deliberately interrupted gate
were removed only after verifying no live cwd/exe/fd/maps references; the exact
paths and sizes are recorded in `owned-temp-cleanup.json`. Production capacity
guards were not changed. `failure-diagnosis.log` retains the failing reproduction;
`deploy-upgrade-corrected.log` records the now-passing upgrade package and the chart-schema mismatch subsequently fixed. `deploy-corrected-final.log` records the additional retained compatibility-manifest mismatch; `deploy-verified.log` records the final chart checks. Both chart JSON-schema constants and the normalized live-values compatibility fixture now use 86; the fixture comment retains its originally captured schema 81. This changes candidate compatibility input, not live deployment state.

The chart still referenced a removed `scripts/compatibility-contract.py`. Added
`scripts/sync-database-schema.py` derives the target from the contiguous embedded
migrations and regenerates the default, both JSON-schema constraints, retained
compatibility JSON and normalized live fixture. `--check` fails on stale output;
`schema-sync.log` records the successful check. The chart error now names the
available command. Agent/protocol settings are preserved.

The next completed gate confirmed seven remaining upgrade failures came from
artifact capacity pressure during parallel execution. `go clean -cache` removed
only rebuildable cache data and restored about 22 GB above the unchanged reserve
(`cache-capacity-recovery.json`). This is an environmental correction, not a flaky
retry or a lowered safety limit. Admission now also rejects whitespace-only display
names before queueing, aligning HTTP acceptance with worker validation.
`registration-tests-verified.log` covers this final input and the earlier regressions.

Final focused suite: **54 passed / 0 skipped / 0 failed**, with real PostgreSQL and race detection. All 103 source hashes still match.

## Final outcome

All ten local tasks are complete. Full Go gate: **15,755 passed / 14 skipped / 0 failed / 0 errors**; lint, vet, formatting and all four binaries passed. The 54 focused checks and final security scans passed. All 103 hashes match the source verified by the final gate. No cluster mutation, commit, push or deployment occurred.
