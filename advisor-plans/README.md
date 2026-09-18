# Advisor plans

This directory now has one active execution authority. Earlier audits and plans
are preserved under [`archive/`](./archive/) with a closeout ledger and residual
mapping.

## Active plan

| Plan | Priority | Status | Next gate |
|---|---|---|---|
| [016 — Production and GA closure](./016-production-ga-closure-master-plan.md) | P0/P1 + release qualification | **IN PROGRESS** | The broad local replay and coherent revision-61 deployment are green for exact production candidate `fe4b51eb`; run the retained estate-100 qualification, then the eight external Phase 6 qualifications. Phase 7 remains separately claim-gated and unauthorized. |

Status values: `TODO — READY`, `IN PROGRESS`, `BLOCKED` with a concrete reason,
or `DONE` with retained evidence links.

## Execution order

1. **Phase 0:** source-control and artifact reproducibility.
2. **Phase 1:** critical runtime lifecycle/readiness.
3. **Phase 2:** refresh/JWT/browser-session/ciphertext boundaries.
4. **Phase 3:** production networking, DR, air-gap, and supply chain.
5. **Phase 4:** durable dispatch, bounded data access, scale, and telemetry.
6. **Phase 5:** estate-scale frontend truth, accessibility, and toolchain.
7. **Phase 6:** eight external release-candidate/GA qualifications.
8. **Phase 7:** destructive physical K3s/RKE2/Constellation campaign only when
   that interoperability claim is in release scope.

Do not execute an archived plan directly. If new evidence invalidates Plan 016,
reconcile Plan 016 and record the decision in this index rather than reopening a
historical Argo/Fleet path or duplicating a completed finding.

## Completed outcomes that stay closed

- Vite/React/TanStack frontend migration.
- Flux-native v1 delivery replacement; Flux remains the only delivery engine.
- Approved Charlie P1 platform-pack program.
- Audit archive name backfill and guarded cluster-tombstone retention.
- The completed local portions recorded in archived Plans 008 and 011–015,
  enumerated in Plan 016's reconciliation verdict.
- Plan 016 Phases 1–3 are locally implemented. Phase 4 implementation and
  Phase 5 automated implementation/browser qualification are locally complete;
  their remaining scale and manual/external evidence stays open in Plan 016.

## Archive

The [archive ledger](./archive/README.md) records the closeout status of Plans
000–015 and exactly where each unresolved outcome moved. Archived files remain
the decision/evidence record and should not be deleted.
