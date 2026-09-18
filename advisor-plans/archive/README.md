# Archived advisor plans

These files preserve the audit and implementation history through 2026-09-16.
They are not active execution authority. Current residual work is consolidated
in [`../016-production-ga-closure-master-plan.md`](../016-production-ga-closure-master-plan.md).

Do not delete archived plans: their evidence, rejected approaches, and product
boundary decisions prevent regressions and duplicate work.

| Plan | Closeout status | Residual disposition |
|---|---|---|
| [000](./000-enterprise-quality-rancher-parity-master-plan.md) | HISTORICAL / superseded | Later plans replaced the July 2026 master; retired Argo findings are not carried. |
| [001](./001-residual-enterprise-ha-ssrf-parity-master-plan.md) | HISTORICAL / superseded | Replaced by 002 and then the Flux-native v1 architecture. |
| [002](./002-enterprise-grade-closure-and-rancher-day2-assurance-plan.md) | HISTORICAL v0.x | Argo-era evidence only; do not apply to v1. |
| [003](./003-self-managed-argo-operation-write-barrier-and-live-acceptance-plan.md) | HISTORICAL v0.3 | Retired Argo-only acceptance blocker. |
| [004](./004-vite-tanstack-frontend-migration-plan.md) | VERIFIED DONE | Current tree is Vite/React/TanStack; scale/accessibility residuals are in 016 Phase 5. |
| [005](./005-audit-archive-backfill-and-tombstone-retention-plan.md) | VERIFIED DONE ON CURRENT TREE | Archive name, guarded retention task, and focused tests exist; invariant retained in 016 Phase 4. |
| [006](./006-charlie-platform-pack-expansion-plan.md) | DONE for approved P1 | Demand-gated P2 packs are optional and not release blockers. |
| [007](./007-flux-native-continuous-delivery-replacement-plan.md) | VERIFIED DONE for v1 architecture | Flux-only boundary retained; current RC evidence is in 016 Phase 6. |
| [008](./008-enterprise-grade-api-and-rancher-parity-implementation-plan.md) | LOCAL IMPLEMENTATION COMPLETE (352/360) | Eight external executions moved to 016 Phase 6. |
| [009](./009-three-host-k3s-rke2-astronomer-constellation-e2e-qualification-plan.md) | NOT EXECUTED / superseded | Claim-gated destructive campaign condensed into 016 Phase 7. |
| [010](./010-full-platform-review-2026-09-10.md) | REVIEW COMPLETE | Findings were decomposed into 011–015; verified residuals moved to 016. |
| [011](./011-release-green-and-runtime-lifecycle-safety-plan.md) | PARTIAL / superseded | Reproducibility and critical-loop lifecycle moved to 016 Phases 0–1. |
| [012](./012-authentication-cryptography-and-api-boundary-plan.md) | PARTIAL / superseded | Refresh families, JWT context, cookie-only browser sessions, and ciphertext envelopes moved to 016 Phase 2. |
| [013](./013-production-security-dr-and-supply-chain-plan.md) | PARTIAL / superseded | Remaining networking, DR, air-gap, image, CI, dependency, and FIPS decisions moved to 016 Phase 3. |
| [014](./014-data-durability-scale-and-observability-plan.md) | PARTIAL / superseded | Remaining durable dispatch, pagination/data ownership, scale, and telemetry moved to 016 Phase 4. |
| [015](./015-frontend-scale-accessibility-and-guide-alignment-plan.md) | PARTIAL / superseded | Remaining estate UI, accessibility, toolchain, Playwright, and ADR work moved to 016 Phase 5. |

## Reconciliation evidence

- Baseline commit: `5567da3bbba8c72bb95924f1824f5c100d147058` plus the preserved dirty integration tree.
- Current frontend migration spot check: Vite and generated TanStack route tree
  present, Next app tree absent, `npm run type-check` and `npm run lint` pass.
- Tombstone spot check: `archived_cluster_name` is in the current schema/query
  path; the scheduled guarded retention task is registered; focused worker tests
  pass.
- Local runtime spot check: Helm revision 23 and app/chart 1.1.0 were deployed;
  intended deployments/statefulsets were Ready and `/health/` plus `/readyz`
  returned 200 at reconciliation time.

Archived plan checkboxes reflect the historical moment in which they were
written. This README's closeout status and Plan 016 are authoritative for future
execution.
