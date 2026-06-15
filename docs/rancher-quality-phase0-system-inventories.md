# Rancher-Quality Phase 0 System Inventories

Date: 2026-06-14
Scope: frontend pages, Postgres/query surface, management CRDs, and background automation.

This inventory complements `docs/rancher-quality-phase0-inventory.md` and
`docs/rancher-quality-phase0-surface-inventory.md`. It is intentionally factual:
counts, owners, and risk notes that should guide Rancher-grade cleanup.

## Frontend Page Inventory

Source scan:

```bash
find frontend/src/app -type f -name 'page.tsx' | wc -l
find frontend/src/app -type f -name 'layout.tsx' | wc -l
```

Current count:

- `98` Next.js `page.tsx` routes.
- `3` Next.js `layout.tsx` files.

Largest dashboard route families:

- `settings`: 32 pages.
- `clusters`: 23 pages.
- `projects`: 8 pages.
- `backups`: 5 pages.
- `argocd`: 4 pages.
- `cluster-templates`: 4 pages.
- `security`: 3 pages.

Ownership notes:

- `frontend/src/app/dashboard/clusters/**` owns adopted-cluster day-2 operations.
- `frontend/src/app/dashboard/argocd/**` owns GitOps and Argo visibility.
- `frontend/src/app/dashboard/settings/**` is the largest administrative surface and should be the first UI area checked for duplicate forms, one-off table behavior, inconsistent loading states, and RBAC visibility gaps.
- `frontend/src/app/dashboard/audit/page.tsx` is the first-class audit search surface and should remain distinct from the legacy settings audit tab.

Quality risks:

- The large `settings` and `clusters` families are the highest-risk areas for duplicated table/action/drawer patterns.
- Cluster detail has many sibling pages; shared cluster header, stale-state indicators, and permission-aware action affordances should be enforced there first.
- Routes that perform high-risk operations must map to explicit backend RBAC and audit entries before new UI actions are added.

Required follow-up:

- Produce an automated frontend route inventory with page path, API hooks used, RBAC requirements, loading/error/empty states, and e2e coverage.
- Add viewport checks for the highest-use cluster, Argo, audit, RBAC, settings, and backup pages.

## Code Health Inventory

Source scan:

```bash
node scripts/code-health-inventory.mjs --check --verify-doc
```

Current generated evidence:

- `docs/rancher-quality-phase0-code-health-inventory.md`
- `npm run code-health` from `frontend/`

Hard gates:

- No duplicate direct package declarations across frontend dependency sections.
- No direct `fetch` or `axios` calls outside `frontend/src/lib/api*`.

Current cleanup candidate families:

- Page-local API error `ResponseShape` types are currently consolidated into
  `frontend/src/lib/api/errors.ts`; the generated inventory should stay empty
  for this class.
- Argo React Query keys are consolidated under `queryKeys.argocd`; logging,
  cluster-group, Vault connection, Agent Fleet, Extensions, and Settings
  Operations, cluster overview, and core cluster tab keys have also moved to
  shared query keys. Remaining inline/page-local keys should move to shared
  query key or feature hook modules.
- Duplicated unexported Go helper names that need package-owner review before consolidation.
- sqlc query declarations without references from any non-generated Go file
  across the repo, including CLI and tests.
- No frontend component dead-code candidates remain in the generated inventory;
  the scanner now resolves absolute, relative, and dynamic component imports.

## Database Inventory

Source scan:

```bash
find internal/db/migrations -name '*.up.sql' | wc -l
find internal/db/migrations -name '*.down.sql' | wc -l
find internal/db/migrations -name '*_test.go' | wc -l
find internal/db/queries -name '*.sql' | wc -l
```

Current count:

- `104` up migrations.
- `104` down migrations.
- `34` migration-focused Go tests.
- `57` sqlc query files.
- Latest observed migration: `105_seed_policy_and_ingress_baseline_tools`.

Recent state-bearing migrations:

- `096_repair_job_states`
- `097_operation_idempotency_keys`
- `098_rancher_grade_role_catalog`
- `099_argocd_baseline_ownership_decisions`
- `100_ui_extensions`
- `101_agent_lifecycle_operations`
- `102_cluster_registration_token_hash_only`
- `103_cluster_agent_token_revocation`
- `104_argocd_application_resource_drift_counts`
- `105_seed_policy_and_ingress_baseline_tools`

Ownership notes:

- Postgres owns durable product state, identities, RBAC, audit, operation history, cached GitOps status, and automation records.
- Redis/asynq owns ephemeral work delivery and retry state.
- Kubernetes/Argo own live workload state and desired deployment convergence.
- CRDs own declarative management intent where Kubernetes-native reconciliation is desired.

Quality risks:

- Migration count is high enough that every new migration needs a rollback, idempotency, and data-backfill review.
- Several systems have both operation rows and task queue delivery; new work should use idempotency keys and durable task-outbox records for high-risk operations.
- Generated SQL should remain the only normal access path; manual SQL helpers need tests and a clear reason.

Required follow-up:

- Add an automated table/index/query inventory in CI.
- Flag tables that copy raw Kubernetes object state instead of storing product state or bounded cache summaries.
- Verify every operation table has status constants, retry semantics, timestamps, actor/correlation fields, and audit linkage where applicable.

## CRD Inventory

Source scan:

```bash
ls deploy/chart/templates/crd-*.yaml
rg -n 'GroupVersion|type .*Spec struct' internal/crd/types.go
```

Shipped chart CRD templates:

- `crd-cluster.yaml`
- `crd-project.yaml`
- `crd-clusterbaseline.yaml`
- `crd-componentbundle.yaml`
- `crd-agentprofile.yaml`
- `crd-gitopstarget.yaml`
- `crd-rbac.yaml`

Primary management API group:

- `management.astronomer.io/v1alpha1`

Primary management CRDs:

- `Cluster`
- `Project`
- `ClusterBaseline`
- `ComponentBundle`
- `AgentProfile`
- `GitOpsTarget`

Related watched API groups:

- Argo CD `argoproj.io` resources for generated `ApplicationSet` and child `Application` state.
- Trivy Operator `aquasecurity.github.io/v1alpha1` image scan resources.

Ownership notes:

- `Cluster` and `Project` CRDs mirror adoption/project intent into durable Postgres records.
- `ClusterBaseline` and `GitOpsTarget` reconcile desired GitOps intent into Argo `ApplicationSet` resources.
- `ComponentBundle` is the governed reusable source catalog for baseline and GitOps targets.
- `AgentProfile` controls install-time agent privilege and capability projection.

Quality risks:

- CRDs are still `v1alpha1`; conversion strategy must be finalized before serving a beta API.
- Generated Argo resources must never overwrite objects without Astronomer ownership labels/annotations.
- Status must remain bounded and must not leak secret refs or rendered secret values.

Required follow-up:

- Add an automated CRD inventory check that verifies every chart CRD has a runtime type, status subresource, schema test, RBAC rule, controller coverage, and documented ownership policy.
- Keep backup/restore tests aligned with CRD plus Postgres reconciliation expectations.

## Background Job Inventory

Generated inventory:

```bash
node scripts/operation-task-inventory.mjs --write
npm run code-health
```

Current count:

- `docs/rancher-quality-phase0-operation-task-inventory.md` scans worker,
  handler, and command production Go files.
- Worker handlers are registered in `internal/worker/worker.go`, including
  tunnel-queue handlers for cluster-template apply/drift, service-mesh detect,
  and cluster-group metric refresh.
- Periodic tasks are registered in `internal/worker/scheduler.go`; tunnel-bound
  tasks are scheduled onto `tasks.ClusterTemplateApplyQueueName` so the
  server-embedded tunnel worker drains work that needs server-only runtime
  dependencies.
- Production wiring is split between `cmd/worker/main.go` for standalone worker
  tasks and `internal/server/server.go` for tunnel/server-only tasks.

Major task families:

- Cluster health, registration cleanup, decommission, registry apply, and condition remediation.
- Argo managed-cluster label refresh and auto-registration.
- GitOps sync, Fleet-named legacy orchestration, and baseline drift checks.
- Project reconcile and cleanup.
- Backup, restore, retention, snapshot polling, and scheduled backup dispatch.
- NetworkPolicy apply and drift checks.
- Cloud credential materialization and plaintext credential migration.
- Audit partition maintenance and audit retention.
- SIEM, webhook, email, notification, alert, telemetry, and recommendation dispatch.
- CRD mirror gauges, stale mirror pruning, and ownership drift checks.
- Task outbox dispatch and deferred work dispatch.

Ownership notes:

- Asynq is delivery machinery; durable intent belongs in Postgres.
- Periodic re-evaluators are required for Argo adoption repair, credential migration, drift repair, and stale-state cleanup.
- High-risk tasks should be idempotent and traceable to an operation/audit row.

Quality risks:

- Task type constants are split across worker and task packages; new tasks should avoid anonymous string literals.
- Some old task names still use Fleet terminology; UI/API copy should prefer Argo/GitOps naming while preserving compatibility where needed.
- Every external task path needs bounded timeouts, retry policy, metrics, and dead-letter visibility.
- Async compliance exports are disabled after removing the unhandled
  `compliance:export` producer; reintroducing them requires a registered worker,
  durable job table, durable output storage, and polling API.
- Agent lifecycle operation creation now accepts `Idempotency-Key` and has an
  idempotent SQL helper; keep new durable operation tables in the generated
  inventory with matching idempotent create helpers.
- Direct enqueue call sites that are not statically proven outbox-backed are
  classified in `docs/direct-enqueue-classifications.json`; new unclassified
  rows fail `npm run code-health`.
- Cleanup-only fallback exceptions have been promoted to durable delivery:
  registry unapply delete cleanup uses the atomic task-outbox path, and project
  namespace apply/cleanup fallback paths write task_outbox before direct queue
  fallback.

Required follow-up:

- Keep `docs/rancher-quality-phase0-operation-task-inventory.md` current through
  `npm run code-health`.
- Keep `docs/direct-enqueue-classifications.json` current when a direct enqueue
  call site is added, removed, or promoted to durable outbox delivery.
- Fail CI when a task is registered without a test or when a task constructor uses an inline string without a typed constant.
- Link support-bundle queue/DLQ sections to the task inventory for support triage.
