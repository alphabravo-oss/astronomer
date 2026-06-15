# Rancher-Quality Phase 0 Surface Inventory

Date: 2026-06-13
Status: Initial concrete surface inventory plus generated route inventory
Parent plan: `docs/plans/2026-06-13-rancher-quality-argo-master-plan.md`

## Purpose

This file is the concrete Phase 0 audit surface list for the Rancher-quality plan. The prose inventory names broad areas; this document pins down the actual files that need ownership, quality checks, deduplication review, security review, and test mapping.

This is not a claim that each surface is fully audited. It is the working list that future Phase 0 tasks should burn down.

## Refresh Commands

Run these from the repository root when updating this file:

```bash
find frontend/src/app/dashboard -type f -name 'page.tsx' | sort
find frontend/src/lib/api -type f | sort
find internal/db/queries -type f -name '*.sql' | sort
find internal/worker/tasks -type f -name '*.go' ! -name '*_test.go' | sort
find internal/handler internal/server internal/tunnel internal/agent -type f -name '*.go' ! -name '*_test.go' | sort
ASTRONOMER_WRITE_ROUTE_INVENTORY=1 go test ./internal/server -run TestRouteInventoryCanBeGenerated -count=1
node scripts/code-health-inventory.mjs --write
```

## Generated Route Security Artifacts

| Artifact | Purpose | Refresh or validation command |
|---|---|---|
| `docs/generated-route-inventory.json` | Full `chi.Walk` mounted route inventory. Current count: 405 entries. Every row includes handler owner, surface, auth posture, RBAC posture, CSRF posture, audit posture, and representative test evidence; high-risk registry metadata enriches 262 mounted rows, and classification metadata enriches 3 auth-flow rows. | `ASTRONOMER_WRITE_ROUTE_INVENTORY=1 go test ./internal/server -run TestRouteInventoryCanBeGenerated -count=1` |
| `docs/security-sensitive-routes.json` | Per-route high-risk registry with sample path, expected unauthenticated status, auth model, RBAC, CSRF, audit notes, and tests. Current count: 209 entries. | `go test ./internal/server -run TestHighRiskRoutesDenyUnauthenticatedRequests -count=1` |
| `docs/route-risk-classifications.json` | Narrow classification file for mutating routes intentionally outside the registry. Current count: 1 public auth session-flow rule. | `go test ./internal/server -run TestMutatingRoutesHaveSecurityClassification -count=1` |
| `docs/rancher-quality-phase0-code-health-inventory.md` | Duplicate/dead-code candidate inventory with remove, keep, and needs-investigation classifications. Hard gates currently enforce no duplicate direct frontend dependency declarations and no direct frontend HTTP calls outside the API layer. | `node scripts/code-health-inventory.mjs --check --verify-doc` |

The route classification file is not a substitute for high-risk route coverage. New mutating routes should normally be added to `docs/security-sensitive-routes.json`; classification is reserved for intentionally public auth-token flows or documented lower-risk exceptions.

## Required Per-Surface Checks

### Dashboard Page Checks

Every dashboard page must have:

- Authentication handled by the dashboard shell or explicit page guard.
- Permission-aware empty, denied, loading, and error states.
- No hidden write action without a matching server-side RBAC check.
- No local copy of common table, status, confirmation, toast, polling, or mutation patterns when a shared component exists.
- Responsive layout at desktop and mobile widths.
- Accessible labels for form controls and icon-only actions.
- Stable route names that match product scope; no creation/provisioning UI for clusters.
- At least smoke coverage for critical page families and targeted tests for high-risk writes.

### Frontend API Client Checks

Every API client module must have:

- Centralized fetch/auth/CSRF behavior through the shared API layer.
- Typed request and response shapes.
- Predictable error handling.
- No duplicate endpoint wrappers in unrelated files.
- No client-side permission assumptions that are not enforced server-side.
- Tests or e2e coverage for high-risk mutations.

### Database Query and Migration Checks

Every query file and related migration must have:

- Intentional table owner and feature owner.
- Index coverage for list/detail routes and worker polling.
- Foreign keys with explicit cascade/restrict semantics.
- Status constraints or typed enums for durable operations.
- Idempotency keys where retries can create duplicate work.
- Secret columns recorded in `docs/secret-column-inventory.md`.
- Retention policy for audit, event, session, token, and operation-history tables.
- Up/down migration coverage and migration tests for risky data moves.

### Worker Task Checks

Every worker task must have:

- Idempotent execution.
- Retry, timeout, and dead-letter behavior.
- Operation or audit correlation id where the task is user-initiated.
- Structured logs and metrics.
- Clear lock/concurrency semantics.
- Cancellation or stale-job handling where applicable.
- Tests for success, retryable failure, permanent failure, and duplicate dispatch.

### Backend Handler, Tunnel, and Agent Checks

Every handler, tunnel, and agent surface must have:

- Authentication and authorization stated at the route boundary.
- Explicit audit behavior for high-risk reads and writes.
- CSRF behavior documented for browser-cookie mutations.
- Request and response header sanitization where proxying is involved.
- No caller credential forwarding to adopted clusters unless intentionally transformed into a scoped service account token.
- Per-cluster scoping for machine-to-machine credentials.
- Tests proving unauthenticated and unauthorized callers are denied.

## Dashboard Page Inventory

Current dashboard page count: 92.

```text
frontend/src/app/dashboard/account/security/page.tsx
frontend/src/app/dashboard/admin/users/[id]/page.tsx
frontend/src/app/dashboard/audit/page.tsx
frontend/src/app/dashboard/agents/page.tsx
frontend/src/app/dashboard/alerting/baselines/page.tsx
frontend/src/app/dashboard/alerting/page.tsx
frontend/src/app/dashboard/argocd/[instanceId]/applications/[appId]/page.tsx
frontend/src/app/dashboard/argocd/[instanceId]/applicationsets/new/page.tsx
frontend/src/app/dashboard/argocd/[instanceId]/page.tsx
frontend/src/app/dashboard/argocd/page.tsx
frontend/src/app/dashboard/backups/page.tsx
frontend/src/app/dashboard/backups/restores/[restoreId]/page.tsx
frontend/src/app/dashboard/backups/runs/[runId]/page.tsx
frontend/src/app/dashboard/backups/schedules/new/page.tsx
frontend/src/app/dashboard/backups/storage/new/page.tsx
frontend/src/app/dashboard/catalog/page.tsx
frontend/src/app/dashboard/cluster-templates/[id]/edit/page.tsx
frontend/src/app/dashboard/cluster-templates/[id]/page.tsx
frontend/src/app/dashboard/cluster-templates/new/page.tsx
frontend/src/app/dashboard/cluster-templates/page.tsx
frontend/src/app/dashboard/clusters/[id]/[resource]/page.tsx
frontend/src/app/dashboard/clusters/[id]/adoption/page.tsx
frontend/src/app/dashboard/clusters/[id]/apps/page.tsx
frontend/src/app/dashboard/clusters/[id]/image-scans/page.tsx
frontend/src/app/dashboard/clusters/[id]/network-access/page.tsx
frontend/src/app/dashboard/clusters/[id]/network-policies/page.tsx
frontend/src/app/dashboard/clusters/[id]/nodes/[nodeName]/page.tsx
frontend/src/app/dashboard/clusters/[id]/page.tsx
frontend/src/app/dashboard/clusters/[id]/registries/page.tsx
frontend/src/app/dashboard/clusters/[id]/resources/page.tsx
frontend/src/app/dashboard/clusters/[id]/service-mesh/mtls/page.tsx
frontend/src/app/dashboard/clusters/[id]/service-mesh/page.tsx
frontend/src/app/dashboard/clusters/[id]/shell/page.tsx
frontend/src/app/dashboard/clusters/[id]/snapshots/page.tsx
frontend/src/app/dashboard/clusters/[id]/template/page.tsx
frontend/src/app/dashboard/clusters/[id]/tools/page.tsx
frontend/src/app/dashboard/clusters/[id]/workloads/[kind]/[namespace]/[name]/page.tsx
frontend/src/app/dashboard/clusters/[id]/workloads/page.tsx
frontend/src/app/dashboard/clusters/page.tsx
frontend/src/app/dashboard/clusters/register/[id]/connect/page.tsx
frontend/src/app/dashboard/clusters/register/[id]/progress/page.tsx
frontend/src/app/dashboard/clusters/register/page.tsx
frontend/src/app/dashboard/extensions/page.tsx
frontend/src/app/dashboard/logging/page.tsx
frontend/src/app/dashboard/monitoring/page.tsx
frontend/src/app/dashboard/page.tsx
frontend/src/app/dashboard/projects/[id]/catalogs/page.tsx
frontend/src/app/dashboard/projects/[id]/cloud-credentials/[credId]/edit/page.tsx
frontend/src/app/dashboard/projects/[id]/cloud-credentials/new/page.tsx
frontend/src/app/dashboard/projects/[id]/cloud-credentials/page.tsx
frontend/src/app/dashboard/projects/[id]/page.tsx
frontend/src/app/dashboard/projects/[id]/policy/page.tsx
frontend/src/app/dashboard/projects/[id]/quota/page.tsx
frontend/src/app/dashboard/projects/page.tsx
frontend/src/app/dashboard/rbac/page.tsx
frontend/src/app/dashboard/search/page.tsx
frontend/src/app/dashboard/security/page.tsx
frontend/src/app/dashboard/security/scans/[scanId]/page.tsx
frontend/src/app/dashboard/security/scans/new/page.tsx
frontend/src/app/dashboard/settings/auth/connectors/[id]/page.tsx
frontend/src/app/dashboard/settings/auth/connectors/new/page.tsx
frontend/src/app/dashboard/settings/auth/install/page.tsx
frontend/src/app/dashboard/settings/auth/page.tsx
frontend/src/app/dashboard/settings/auth/register-sso/page.tsx
frontend/src/app/dashboard/settings/auth/settings/page.tsx
frontend/src/app/dashboard/settings/backup-drill/page.tsx
frontend/src/app/dashboard/settings/cluster-groups/page.tsx
frontend/src/app/dashboard/settings/compliance/baselines/page.tsx
frontend/src/app/dashboard/settings/compliance/page.tsx
frontend/src/app/dashboard/settings/general/page.tsx
frontend/src/app/dashboard/settings/gitops/[id]/page.tsx
frontend/src/app/dashboard/settings/gitops/new/page.tsx
frontend/src/app/dashboard/settings/gitops/page.tsx
frontend/src/app/dashboard/settings/group-mappings/page.tsx
frontend/src/app/dashboard/settings/network-policies/page.tsx
frontend/src/app/dashboard/settings/operations/page.tsx
frontend/src/app/dashboard/settings/page.tsx
frontend/src/app/dashboard/settings/platform/page.tsx
frontend/src/app/dashboard/settings/quotas/[name]/page.tsx
frontend/src/app/dashboard/settings/quotas/new/page.tsx
frontend/src/app/dashboard/settings/quotas/page.tsx
frontend/src/app/dashboard/settings/quotas/usage/page.tsx
frontend/src/app/dashboard/settings/read-audit/page.tsx
frontend/src/app/dashboard/settings/smtp/page.tsx
frontend/src/app/dashboard/settings/templates/[key]/page.tsx
frontend/src/app/dashboard/settings/templates/page.tsx
frontend/src/app/dashboard/settings/vault/page.tsx
frontend/src/app/dashboard/settings/webhooks/[id]/page.tsx
frontend/src/app/dashboard/settings/webhooks/new/page.tsx
frontend/src/app/dashboard/settings/webhooks/page.tsx
frontend/src/app/dashboard/settings/widgets/page.tsx
frontend/src/app/dashboard/tools/page.tsx
```

Immediate UI findings:

- `/dashboard/clusters/[id]/adoption` is the canonical cluster adoption timeline.
- The legacy `/dashboard/clusters/[id]/provisioning` redirect alias has been removed; `/dashboard/clusters/[id]/adoption` is the canonical cluster adoption timeline.
- The cluster detail area has enough pages that shared table/action/status components should be mandatory before adding more feature-specific implementations.
- Argo CD pages should become the deployment-management center for adopted clusters; avoid adding any Fleet-branded product surface.

## Frontend API Client Inventory

```text
frontend/src/lib/api/account-security.ts
frontend/src/lib/api/admin-operations.ts
frontend/src/lib/api/cluster-detail.ts
frontend/src/lib/api/cluster-groups.ts
frontend/src/lib/api/dashboards.ts
frontend/src/lib/api/extensions.ts
frontend/src/lib/api/kubectl-shell.ts
frontend/src/lib/api/project-detail.ts
frontend/src/lib/api/settings.ts
frontend/src/lib/api/vault.ts
```

Immediate API-client findings:

- `frontend/src/lib/api.ts` remains the central compatibility module and must be audited for duplicate endpoint wrappers against the split modules above.
- Account security, admin operations, kubectl shell, vault, and settings clients are high-risk because they touch identity, privileged operations, exec, or secrets.
- Every client-side mutation should use the same error shape and permission-denied handling so UI behavior is consistent across the dashboard.

## Database Query Inventory

Current SQL query file count: 57.

```text
internal/db/queries/agents.sql
internal/db/queries/alerting.sql
internal/db/queries/anomaly_baselines.sql
internal/db/queries/apiserver_allowlists.sql
internal/db/queries/argocd.sql
internal/db/queries/argocd_operation_events.sql
internal/db/queries/argocd_operations.sql
internal/db/queries/auth.sql
internal/db/queries/backup_drill.sql
internal/db/queries/backups.sql
internal/db/queries/catalog.sql
internal/db/queries/catalog_operation_events.sql
internal/db/queries/catalog_operations.sql
internal/db/queries/cloud_credentials.sql
internal/db/queries/cluster_condition_remediation.sql
internal/db/queries/cluster_decommission.sql
internal/db/queries/cluster_groups.sql
internal/db/queries/cluster_registration.sql
internal/db/queries/cluster_snapshots.sql
internal/db/queries/cluster_templates.sql
internal/db/queries/clusters.sql
internal/db/queries/compliance.sql
internal/db/queries/control_plane.sql
internal/db/queries/crd_mirror_v2.sql
internal/db/queries/dashboards.sql
internal/db/queries/fleet.sql
internal/db/queries/fleet_ownership.sql
internal/db/queries/gitops_registration.sql
internal/db/queries/group_sync.sql
internal/db/queries/image_vulns.sql
internal/db/queries/kubectl_sessions.sql
internal/db/queries/logging.sql
internal/db/queries/logging_operations.sql
internal/db/queries/maintenance_windows.sql
internal/db/queries/monitoring.sql
internal/db/queries/monitoring_operation_events.sql
internal/db/queries/monitoring_operations.sql
internal/db/queries/network_policies.sql
internal/db/queries/platform.sql
internal/db/queries/platform_settings.sql
internal/db/queries/project_catalogs.sql
internal/db/queries/projects.sql
internal/db/queries/quotas.sql
internal/db/queries/rbac.sql
internal/db/queries/security.sql
internal/db/queries/siem.sql
internal/db/queries/smtp.sql
internal/db/queries/sso.sql
internal/db/queries/sso_sessions.sql
internal/db/queries/tool_operation_events.sql
internal/db/queries/tool_operations.sql
internal/db/queries/tools.sql
internal/db/queries/totp.sql
internal/db/queries/users.sql
internal/db/queries/vault_connections.sql
internal/db/queries/webhooks.sql
internal/db/queries/workload_operations.sql
```

Immediate database findings:

- `argocd*`, `gitops_registration`, `cluster_registration`, `cluster_templates`, and `tool_operations` are the core Argo-managed adoption path and need the strictest idempotency and operation-history review.
- `fleet.sql` and `fleet_ownership.sql` must be reviewed. If these are internal historical names for Argo-oriented ownership logic, rename them or document the alias. The product target explicitly excludes Rancher Fleet.
- `users`, `auth`, `totp`, `sso`, `sso_sessions`, and `vault_connections` must be mapped to secret inventory, audit behavior, and retention policy.
- Operation-event query files should be normalized so every long-running operation has consistent list/detail/event APIs.

## Worker Task Inventory

Current worker task implementation count: 50.

```text
internal/worker/tasks/agent_manifest.go
internal/worker/tasks/alert_evaluation.go
internal/worker/tasks/alert_evaluation_anomaly.go
internal/worker/tasks/anomaly_baseline_recompute.go
internal/worker/tasks/apiserver_allowlist_reconcile.go
internal/worker/tasks/argocd_auto_register_cluster.go
internal/worker/tasks/argocd_refresh_managed_cluster.go
internal/worker/tasks/audit_partition_maintenance.go
internal/worker/tasks/audit_retention.go
internal/worker/tasks/backup_execution.go
internal/worker/tasks/catalog_sync.go
internal/worker/tasks/chart_recommendations_recompute.go
internal/worker/tasks/cleanup_alert_events.go
internal/worker/tasks/cleanup_registration_tokens.go
internal/worker/tasks/cloud_credentials_materialize.go
internal/worker/tasks/cluster_condition_reconcile.go
internal/worker/tasks/cluster_decommission.go
internal/worker/tasks/cluster_group_metrics.go
internal/worker/tasks/cluster_registry_apply.go
internal/worker/tasks/cluster_snapshot_poll.go
internal/worker/tasks/cluster_template_apply.go
internal/worker/tasks/crd_mirror_gauge.go
internal/worker/tasks/crd_mirror_prune.go
internal/worker/tasks/crd_ownership_drift.go
internal/worker/tasks/deferred_dispatch.go
internal/worker/tasks/email_dispatch.go
internal/worker/tasks/enforce_backup_retention.go
internal/worker/tasks/fleet_orchestrate.go
internal/worker/tasks/fleet_selector.go
internal/worker/tasks/gitops_sync.go
internal/worker/tasks/health_check.go
internal/worker/tasks/kubectl_session_reap.go
internal/worker/tasks/mesh_detect.go
internal/worker/tasks/metrics_collection.go
internal/worker/tasks/monitoring_reconcile.go
internal/worker/tasks/network_policy_apply.go
internal/worker/tasks/notification_dispatch.go
internal/worker/tasks/plaintext_credential_migration.go
internal/worker/tasks/project_reconcile.go
internal/worker/tasks/refresh_group_sync_metrics.go
internal/worker/tasks/repair_job_state.go
internal/worker/tasks/run_restore.go
internal/worker/tasks/run_scheduled_backups.go
internal/worker/tasks/runtime.go
internal/worker/tasks/security_scan.go
internal/worker/tasks/siem_dispatch.go
internal/worker/tasks/task_outbox_dispatch.go
internal/worker/tasks/task_outbox_enqueue.go
internal/worker/tasks/telemetry.go
internal/worker/tasks/webhook_dispatch.go
```

Immediate worker findings:

- `argocd_auto_register_cluster.go` and `argocd_refresh_managed_cluster.go` are central to the requested automatic Argo registration model and must have idempotency, token-rotation, retry, and partial-failure tests.
- `fleet_orchestrate.go` and `fleet_selector.go` must be renamed, deleted, or documented as legacy internal naming. They should not imply Rancher Fleet ownership in the product.
- `cluster_template_apply.go`, `gitops_sync.go`, `catalog_sync.go`, `tool_operations`, and Argo tasks need a unified operation state machine so deployment progress is consistent in the UI.
- Cleanup, retention, and migration tasks must emit enough metrics to prove they are running in production.

## High-Risk Backend Surface Families

These are the highest-priority backend file families for the next Phase 0 passes:

```text
internal/handler/auth.go
internal/handler/password_reset.go
internal/handler/totp.go
internal/handler/users.go
internal/handler/sso.go
internal/handler/vault.go
internal/handler/kubectl_shell.go
internal/handler/service_proxy.go
internal/handler/argocd_ui_proxy.go
internal/handler/argocd/*.go
internal/handler/cluster_registration.go
internal/handler/cluster_template_apply_enqueue.go
internal/handler/resources.go
internal/handler/workloads.go
internal/server/routes.go
internal/server/middleware/auth.go
internal/server/middleware/auth_browser.go
internal/server/middleware/rbac.go
internal/server/middleware/read_audit.go
internal/tunnel/proxy.go
internal/tunnel/exec_consumer.go
internal/tunnel/logs_consumer.go
internal/tunnel/internal_k8s.go
internal/tunnel/internal_helm.go
internal/agent/k8sproxy.go
internal/agent/service_proxy.go
internal/agent/exec.go
internal/agent/logs.go
internal/agent/helm.go
```

Required next checks:

- Keep `docs/generated-route-inventory.json`, `docs/security-sensitive-routes.json`, and `docs/route-risk-classifications.json` in sync so any new mutator is either registered or intentionally documented.
- Keep `TestRouteInventoryEntriesHaveCompleteSecurityPosture` passing so every mounted route row keeps handler owner, auth, RBAC, CSRF, audit, and representative-test metadata.
- Keep `TestBrowserCookieMutatingRoutesRequireCSRF` passing for every registry-marked browser-cookie write path.
- Keep explicit audit tests for Argo UI/API proxy access, generic Kubernetes proxy mutations, shell lifecycle, TOTP/admin security changes, and Secret reads current as new surfaces are added.
- Keep service proxy response-header sanitization covered by `TestServiceProxySanitizesResponseHeaders`, keep generic Kubernetes proxy response-header sanitization covered by `TestWriteK8sResponseSanitizesUnsafeHeaders`, `TestHandleK8sProxyStreamingWatchSanitizesResponseHeaders`, and `TestForwardToOwnerPodSanitizesResponseHeaders`, keep Argo UI proxy response-header/cookie sanitization covered by `TestArgoCDUIProxySanitizesResponseHeadersAndCookies`, and add the same explicit review for any future proxy surfaces.
- Verify no handler forwards browser credentials to adopted clusters.

## Definition of Done for Phase 0 Inventory

Phase 0 inventory is done when:

- This file is refreshed and reviewed against the current tree.
- Every dashboard page has a row in a UI quality matrix with owner, API clients, permission gate, state coverage, and test coverage.
- Every high-risk backend route is in `docs/security-sensitive-routes.json` or explicitly documented as intentionally public.
- Every mounted backend route row has security posture metadata in `docs/generated-route-inventory.json`.
- Every worker task has a task contract covering idempotency, retries, metrics, audit correlation, and tests.
- Every SQL query file has a table/feature owner and migration provenance.
- Fleet/provisioning legacy naming is either removed, renamed, or explicitly documented as compatibility-only.
- The master plan links to this inventory and tracks outstanding gaps as tasks, not general concerns.
