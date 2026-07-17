# Advisor plans

Plans produced by deep-dive advisory audits. Product/feature plans remain under `../plans/`.

| Plan | Status | Notes |
|---|---|---|
| [000-enterprise-quality-rancher-parity-master-plan.md](./000-enterprise-quality-rancher-parity-master-plan.md) | **SUPERSEDED residual** | Full 2026-07-08 multiagent assessment. Many Wave A–C items landed on disk (often uncommitted). Do not re-open §4 closed items without new evidence. |
| [001-residual-enterprise-ha-ssrf-parity-master-plan.md](./001-residual-enterprise-ha-ssrf-parity-master-plan.md) | **SUPERSEDED by 002** | Historical residual program against 2991f9d. Most Wave A–C work landed in commit 9256f0d. Retain for decision history; do not reimplement its closed findings. |
| [002-enterprise-grade-closure-and-rancher-day2-assurance-plan.md](./002-enterprise-grade-closure-and-rancher-day2-assurance-plan.md) | **BLOCKED — P0 LIVE ARGO ACCEPTANCE DEFECTS** | **Active enterprise closure program (2026-07-10).** Static code and release artifacts at integration commit `6783004f97b270d3b1e3fa1ac105dd234a534e08` pass the enterprise gate, and six 0.2.2 candidate images/SBOMs were produced. Live acceptance rejected promotion: one durable async Argo sync was reclaimed and submitted upstream nine times because running rows reuse a one-minute dispatch lease; multi-replica polling lacks a durable claim/CAS; and referenced external Secrets remain prune candidates, deadlocking the Synced acceptance requirement against safe non-pruning adoption. The controller is contained at zero and no Astronomer Argo operation remains running. Section 26 is the mandatory implementation, HA/concurrency, retained-resource, rollback, and three-run live-validation addendum. ARGO-03 is reopened until every Section 26 gate passes; do not call this release enterprise-ready. |
| [003-self-managed-argo-operation-write-barrier-and-live-acceptance-plan.md](./003-self-managed-argo-operation-write-barrier-and-live-acceptance-plan.md) | **TODO — P0 0.3.0 BLOCKER** | Focused child plan against integration commit `5915bc6`. Establishes an Application single-writer barrier while Argo acceptance is Running/Terminating, adds status-only operation characterization and zero-write race tests, commits a redacting rollout-resilient live harness, and requires three clean live runs before 0.3.0 promotion. Depends on Plan 002 Section 26. |
| [005-audit-archive-backfill-and-tombstone-retention-plan.md](./005-audit-archive-backfill-and-tombstone-retention-plan.md) | **TODO — not started** | Cluster decommission tombstones are retained forever by omission: no retention job exists and `DeleteCluster` (the hard delete) has zero callers. The retention itself is deliberate and still load-bearing — `audit_archive` denormalizes `resource_name`, but only 699 of 1250 cluster rows have it populated, so for ~44% the tombstone is the only way to name the cluster. Plan denormalizes `archived_cluster_name` onto `audit_archive` first, then purges on a window. **Ordering is mandatory: backfill before purge, never the reverse** — purging first silently orphans 551 archived audit rows. Raised 2026-07-17. |

## Recommended execution order (from 002)

1. **Phase 0** — restore all Go/frontend/Helm gates and establish one enterprise verification command.
2. **Phase 1** — authenticate the production Argo listener and align session/agent defaults.
3. **Phase 2** — distributed JWT/RBAC invalidation and bounded asynchronous event fan-out.
4. **Phase 3** — split bootstrap/durable agent credentials and restrict bundled Argo networking.
5. **Phase 4** — real load identities, tunnel-correlated workloads, HA/failure drills, measured baselines.
6. **Phase 5** — authoritative fleet preview and schema-driven common-resource forms.
7. **Phase 6** — tunnel convergence, hotspot decomposition, immutable CI/images.
8. **Phase 7** — documentation reconciliation, evidence bundle, and GA sign-off.

Child executor plans 003–018 are written only after master-plan **002** approval; see Section 23 of 002.

Plan 003 is now the first approved implementation unit because live acceptance
isolated the remaining self-managed Application write race. Complete it before
splitting or executing any lower-priority 004–018 work.

## Waivers

| ID | Owner | Date | Reason |
|---|---|---|---|
| BASE-04 | deploy/chart maintainers | 2026-07-09; review by 2026-10-09 or next Helm/Argo dependency change | Helm v3.21.0 recursively mis-coalesces Astronomer's map-shaped server.env against argo-cd 9.5.21's list-shaped server.env while evaluating nested redis-ha. Rendered values are isolated and protected by TestServerEnvironmentValuesRemainIsolatedFromArgoCD. Do not filter the warning or rename the public value merely to silence it. |
