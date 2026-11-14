> **Archived 2026-11-14. Historical record only.** See [the sole current working plan](../../../../../../CURRENT.md). This document does not authorize new work or certify the current candidate.

# Dex durability batch — 2026-10-23

> **Scope correction (2026-10-23): historical Dex work, superseded.** The owner
> requires Dex removal and adoption of the existing AlphaBravo Identity component.
> Follow [Plan 032 §20.13](../../../../032-technology-guide-alignment-and-latest-stable-master-plan.md#2013-shared-identity-adoption-and-dex-retirement--scope-correction-2026-10-23).
> These test results remain evidence of the tested source, not release requirements
> or permission to continue Dex hardening. Preserve uncommitted work for selective
> disposition; do not merge it wholesale.

This batch addresses ten tasks in Plan 032 §20.10. Source is uncommitted in
`/root/work/astronomer-fixes`, based on `e2223136030fa17541e6be082a511caf444b7067`.
No runtime deployment or cluster change was performed.

## Implemented tasks

1. Real PostgreSQL claim exclusivity: sixteen concurrent claimants, exactly one owner.
2. Lease takeover: expire the first claim in PostgreSQL, reclaim, verify attempt 2.
3. Success fencing: expired and superseded claims affect zero rows; current owner succeeds.
4. Retry fencing: late claims cannot release a successor's lease or change its error.
5. Failure fencing: late claims cannot mark a successor terminal.
6. Phase fencing: late claims cannot advance the successor to finalization. The executor
   checks affected rows and stops before SSO restoration/enablement on lost ownership.
7. Recovery selection: stale pending/retrying and expired running operations reopen
   delivered outbox intents; fresh, actively leased and terminal operations do not.
   Recovery resets delivery metadata and a repeated recovery changes zero rows.
8. Queue validation: bounded 1 KiB, single JSON document, exact field name, no duplicate
   or unknown keys, canonical nonzero UUID; malformed tasks stop before any storage call.
9. Durable worker outcomes: success/failure replay executes once; late success/retry/
   terminal failure leaves the successor running and returns a safe retryable error;
   ordinary retry clears its lease, then converges to success on attempt 2.
10. Retired `TestApply_503WhenNoK8sRequester`, which asserted an obsolete synchronous
    HTTP contract. Its active replacement is
    `TestDexApplyQueuesDurablyWithoutKubernetesAndReplaysExactlyOnce` (202, Location,
    Retry-After, exactly one queue/audit, conflict for changed request input).

## Design and scope

The existing `attempt_count` increments atomically during claim, so it is a fencing
value without a migration. All four progress/terminal queries now match operation ID,
`running` status, the claimed attempt, and an unexpired lease. Generated interfaces
return affected rows; zero is ownership loss, not successful completion. SQL was
regenerated with `make sqlc`; generated files were not edited manually.

This prevents stale durable status writes. It is not an exactly-once guarantee for
external Kubernetes effects: those retain their existing generation/resource-version
checks. There is no distributed transaction spanning the phase check and SSO writes.
The existing generation guards remain necessary. End-to-end queued rollout, credential
pair failure, provider restoration, and fresh-operation fixed-point coverage remain
open in the earlier Dex coverage map. Seven legacy skipped tests remain.

## Verification

- `durable-tests.log`: first real PostgreSQL/race run passed.
- `durable-tests-final.log`: expanded focused PostgreSQL, worker, handler and lifecycle tests: **43 passed, 0 skipped, 0 failed**, with the race detector.
- `make-check.log`: full repository lint, vet, format, four binary builds and race tests passed: **15,755 passed / 14 skipped / 0 failures / 0 errors**.
- `security.log`: repository security gate passed: gosec 0 results, govulncheck 0 reachable vulnerabilities (1 module-only finding), 2,304 commits secret-scanned clean. Changed working source also passed `working-source-secret-scan.log`.
- `source-manifest.json`: 93 source file hashes, including all 86 prior qualification
  files. Only `internal/db/sqlc/querier.go` changed among those earlier hashes, to
  regenerate the four Dex interfaces; prior Apps projection methods are preserved.
- `source.patch` and `new-durability-test.go.txt`: retained reviewable implementation.

Final gate outcomes are recorded in `verification-summary.json` when complete.

## Open-plan review

Plan 026 owns remaining Dex rollout/provider coverage, Charlie suspension and the
unreproduced pagination timing cause. Plan 016 still needs immutable candidate/live
acceptance and its external/DR evidence. Plans 028/029/033 still require independent
hosts, dedicated qualification targets, a mutation runner and all 174 lifecycle cases.
The last observed replacement/site-demo nodes report the same system UUID. This batch
does not re-observe or resolve that estate blocker. Original ui-demo is preserved.

The full gate used `TMPDIR=/root/work/astronomer-test-tmp` and
`ASTRONOMER_HELM_HTTP_PRIVATE_FIXTURE_IP=172.17.0.1`. The skip count fell from
31 to 14: one obsolete Dex test was removed and sixteen private Helm/proxy and
upgrade cases ran successfully with the owned fixture enabled. No skips were
blanket-deleted or relabeled as passes.
