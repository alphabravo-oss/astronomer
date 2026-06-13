# ArgoCD Auto-Adoption Plan

**Date:** 2026-06-12  
**Status:** In progress; auto-adoption and built-in baseline ApplicationSets implemented  
**Scope:** Automatically register newly adopted clusters into the built-in ArgoCD instance, then use ArgoCD to deploy and reconcile baseline platform components on those clusters.

---

## Goal

When an operator adds a new cluster to Astronomer and the agent connects, the cluster should become an ArgoCD managed cluster without a manual ArgoCD registration step. Baseline components should then be reconciled by ArgoCD ApplicationSets instead of being installed directly by Astronomer's Helm-over-tunnel template worker.

The target user experience:

1. Operator registers a BYO Kubernetes cluster in Astronomer.
2. Agent connects back to the management plane.
3. Astronomer automatically registers that cluster into the built-in `local` ArgoCD instance.
4. ArgoCD cluster Secrets receive Astronomer-owned labels.
5. Built-in ApplicationSets target `astronomer.io/managed-by=astronomer` or narrower label selectors.
6. ArgoCD deploys and continuously reconciles baseline components on the adopted cluster.

---

## Current State

Pieces that already exist:

- Local/built-in ArgoCD self-management exists in `internal/server/self_manage_argocd.go`.
- The local management cluster is registered into ArgoCD by the self-management loop.
- Manual registration of an Astronomer-managed cluster into ArgoCD exists via `ArgoCDHandler.RegisterManagedCluster`.
- ArgoCD managed-cluster rows are tracked in `argocd_managed_clusters`.
- Cluster labels are projected to ArgoCD cluster Secret labels by `managedClusterLabels`.
- Label refresh after cluster updates exists in `internal/worker/tasks/argocd_refresh_managed_cluster.go`.
- ApplicationSet CRUD exists and can target those labels.
- New clusters currently auto-attach the `Platform baseline` cluster template.
- The baseline template currently installs tools through `ToolHandler.EnsureInstalled`, which means Helm-over-tunnel, not ArgoCD.

Missing behavior:

- Remote/adopted clusters now have an automatic registration worker path and built-in ApplicationSet baseline definitions.
- Remote cluster registration no longer needs an operator-pasted target-cluster bearer token when using the built-in proxy path; Astronomer mints a cluster-scoped ArgoCD proxy token.
- The legacy baseline component model is still cluster-template/tool-install driven, but built-in ArgoCD ApplicationSets now exist for Argo-owned reconciliation.
- The `Cluster` CRD now has a durable ArgoCD adoption status surface; the cluster overview shows backend adoption/ownership state, and ArgoCD auto-adoption now writes registration timeline steps plus a cluster condition.

Implemented on 2026-06-12:

- Added `argocd:auto_register_cluster`.
- Enqueued it after agent `CONNECT_ACK`; the immediate enqueue now writes through the durable `task_outbox` first so Redis outages do not strand the fast adoption path.
- Added a 5-minute periodic recovery sweep.
- Added `argocd_cluster_proxy_tokens` with encrypted token storage and hash-based validation.
- Added `/api/v1/internal/argocd/clusters/{cluster_id}/k8s/*` as a dedicated ArgoCD-only proxy route.
- Decommission now deletes ArgoCD cluster-proxy tokens with other cluster credentials.
- Added built-in ArgoCD ApplicationSets for the five platform baseline components.
- Added `argocd.manage_platform_baseline` platform setting, default `true`.
- Added `argocd.auto_adopt_clusters` platform setting, default `true`; the auto-registration worker skips all adoption work when disabled.
- Cluster-template apply now skips ArgoCD-owned baseline component installs for adopted clusters when `argocd.manage_platform_baseline=true`, preventing Helm-over-tunnel and ArgoCD from managing the same baseline tools.
- Added `Cluster` CRD fields for declarative ArgoCD adoption, baseline profile, agent privilege profile, and ArgoCD adoption status.
- Added `scripts/validate-live-argocd-auto-adoption.sh` and `make validate-live-argocd-auto-adoption` to validate managed-cluster auto-adoption, baseline ApplicationSet fan-out, and ArgoCD cluster Secret label refresh.
- Cluster detail now surfaces GitOps ownership: ArgoCD registration count, ArgoCD cluster Secret names, and whether the platform baseline is ArgoCD-owned, pending, local, or legacy Helm-over-tunnel.
- Cluster detail now also shows per-component ownership for the five built-in baseline ApplicationSets, including component slug/name, namespace, ApplicationSet name, and whether that component is ArgoCD-owned, pending, local, or Helm-over-tunnel.
- ArgoCD auto-adoption now writes `argocd_registering`, `argocd_registered`, `argocd_registration_failed`, and `baseline_appsets_matched` registration timeline steps, plus an `ArgoCDAdopted` cluster condition. Failed ArgoCD adoption steps retry the `argocd:auto_register_cluster` worker instead of the platform-baseline template worker.
- The ArgoCD instance cluster tab now exposes operator actions to refresh ArgoCD cluster Secret labels from the current cluster row and to reopen the registration flow for an existing registration.
- The label refresh path now removes stale Astronomer-owned labels from the ArgoCD cluster Secret, so deleted or changed cluster labels stop targeting old ApplicationSets.
- Cluster DELETE and CRD finalizer deletion now persist the decommission row and durable task-outbox row atomically before Redis delivery, so the cleanup worker is retried after Redis/server outages.
- GitOps-driven decommission now uses the same durable task-outbox delivery path before Redis.
- Decommission now unregisters adopted clusters from built-in ArgoCD by deleting the ArgoCD cluster Secret when the management-plane Kubernetes client is available, then deleting `argocd_managed_clusters`; without a K8s client it emits an orphan audit event for operator cleanup.
- The ArgoCD auto-adoption sweep now also repairs the reverse drift case for the built-in single-instance topology: if an Astronomer-owned ArgoCD cluster Secret exists but the `argocd_managed_clusters` index row is missing after a Postgres restore or manual DB edit, the sweep rebuilds the row and refreshes owned labels.
- Legacy cluster-template baseline apply/reapply triggers now use durable task-outbox delivery before Redis, so the Helm-over-tunnel fallback remains reliable during the ArgoCD rollout.
- ApplicationSets target adopted clusters only via:
  - `astronomer.io/managed-by=astronomer`
  - `astronomer.io/is-local=false`

---

## Design Principles

- ArgoCD should become the owner of long-lived baseline component reconciliation.
- Astronomer should still own cluster registration, credentials, labels, audit, and operator UX.
- The migration should be additive. Existing cluster-template baseline installs should keep working during rollout.
- Remote cluster credentials must be minted or provisioned by the agent path, not pasted by an operator.
- ApplicationSets should target cluster labels, not hard-coded cluster names.
- Failures must surface in the cluster registration timeline and cluster conditions.

---

## Phase 1: Automatic ArgoCD Registration

### 1.1 Add An Adoption Task

Create a new Asynq task:

```text
argocd:auto_register_cluster
```

Suggested files:

- `internal/worker/tasks/argocd_auto_register_cluster.go`
- `internal/worker/worker.go`
- `internal/worker/scheduler.go` if a recovery sweep is added

Payload:

```json
{
  "cluster_id": "<uuid>"
}
```

Behavior:

- Load the cluster row.
- Load configured ArgoCD instance rows.
- Register/upsert the cluster into ArgoCD.
- Persist or update `argocd_managed_clusters`.
- Emit audit events and registration timeline steps.

Current implementation registers into every configured ArgoCD instance. If product semantics should mean only a single built-in instance, add a durable `is_builtin` or `managed_by_astronomer` marker to `argocd_instances` and filter the worker.

### 1.2 Enqueue On Agent Connection

The safest trigger is first successful agent connection, not cluster row creation. At creation time the cluster may not yet have a live tunnel or a usable API proxy.

Hook candidates:

- `registration.Service.OnAgentConnected`
- tunnel hub after successful `CONNECT_ACK`

Recommended:

- Enqueue the adoption task after `OnAgentConnected` advances the cluster.
- Keep the task idempotent so repeated heartbeats or reconnects are harmless.

### 1.3 Add A Recovery Sweep

Add a periodic sweep that finds active clusters and re-runs adoption.

This covers:

- Redis outage during first connection.
- Server restart during registration.
- Clusters added before the feature shipped.
- Manual deletion of `argocd_managed_clusters` rows.

Cadence: every 5 minutes.

### 1.4 Decide The Credential Model

Current manual remote registration requires `bearer_token`. Automatic registration needs a non-interactive credential.

Preferred approach:

- Extend the agent to mint or ensure a dedicated ServiceAccount in the adopted cluster for ArgoCD.
- The ServiceAccount should have only the permissions required for the baseline/app deployment model.
- Agent returns a short-lived TokenRequest token, CA data, and server/proxy metadata to the server through a tunnel RPC.
- Server uses that credential to create the ArgoCD cluster Secret.

Potential protocol addition:

```text
ARGOCD_CREDENTIAL_REQUEST
ARGOCD_CREDENTIAL_RESULT
```

Credential payload:

```json
{
  "server": "https://...",
  "bearer_token": "...",
  "ca_data": "...",
  "expires_at": "..."
}
```

Open decision:

- Use direct cluster API server URL when reachable, or always use Astronomer's `/api/v1/clusters/{id}/k8s` proxy.

Recommended default:

- Register remote clusters through Astronomer's cluster proxy URL.
- Keep direct API server registration as an advanced override.

Reason:

- The product premise is no inbound access to adopted clusters.
- ArgoCD running in the management cluster can reach the Astronomer API service.
- The agent already provides the reverse tunnel to the target API.

### 1.5 Secure The ArgoCD Proxy Path

If ArgoCD talks to remote clusters through:

```text
/api/v1/clusters/{id}/k8s
```

then the proxy must authenticate ArgoCD using a server-issued service credential rather than a human JWT.

Options:

- Dedicated API token scoped to `clusters:k8s_proxy` for one cluster.
- Dedicated internal service token stored in the ArgoCD cluster Secret.
- Separate ArgoCD-only proxy route that validates a signed cluster-scoped token.

Recommended:

- Add a cluster-scoped API token/service token with minimal scope.
- Store its hash for validation and encrypted plaintext for idempotent ArgoCD re-registration.
- Put the plaintext token in the ArgoCD cluster Secret config as bearer token.
- Make the proxy middleware authorize that token for only the target cluster.

Implemented route:

```text
/api/v1/internal/argocd/clusters/{id}/k8s
```

The public `/api/v1/clusters/{id}/k8s` path remains human/API-token auth plus RBAC. The ArgoCD path accepts only `astro_argocd_*` cluster-proxy tokens.

Acceptance:

- ArgoCD can list namespaces/pods through the Astronomer proxy.
- The token cannot call unrelated Astronomer API endpoints.
- The token cannot access other clusters.

---

## Phase 2: ArgoCD-Owned Platform Baseline

### 2.1 Define Built-In Baseline ApplicationSets

Create built-in ApplicationSets for platform baseline components:

- [x] `trivy-operator`
- [x] `kube-state-metrics`
- [x] `prometheus-node-exporter` with stable ApplicationSet name `astronomer-baseline-node-exporter`
- [x] `fluent-bit`
- [x] `cert-manager`

Target selector:

```yaml
matchLabels:
  astronomer.io/managed-by: astronomer
  astronomer.io/is-local: "false"
```

Each ApplicationSet should render one Application per adopted cluster.

Use stable names:

```text
astronomer-baseline-trivy
astronomer-baseline-kube-state-metrics
astronomer-baseline-node-exporter
astronomer-baseline-fluent-bit
astronomer-baseline-cert-manager
```

### 2.2 Source Of Truth For Component Specs

Choose where ApplicationSet definitions live.

Recommended:

- [x] Render them from the management server into the built-in ArgoCD namespace.
- Store desired config in DB/platform settings later if operators need customization.
- [x] Keep initial version as code-defined defaults, resolved from `cluster_tools` with built-in fallbacks.

Suggested files:

- `internal/server/baseline_appsets.go`
- `internal/server/self_manage_argocd.go`
- `deploy/chart/templates/*` only if we want chart-level static bootstrapping

### 2.3 Map Current Tool Presets To ArgoCD Sources

The current cluster-template baseline references tool slugs. ArgoCD ApplicationSets need concrete chart source fields:

```yaml
source:
  repoURL: https://...
  chart: ...
  targetRevision: ...
  helm:
    values: |
      ...
destination:
  server: '{{server}}'
  namespace: ...
syncPolicy:
  automated:
    prune: true
    selfHeal: true
  syncOptions:
    - CreateNamespace=true
```

Implementation choices:

- Reuse `cluster_tools.charts` and `presets` to render ApplicationSets.
- Or create a separate `platform_baseline_components` registry.

Recommended:

- [x] Start by rendering from the existing `cluster_tools` catalog so one catalog entry feeds both manual tool installs and ArgoCD baseline installs.
- [x] Add a small resolver that converts a tool slug into ArgoCD Application source/destination config.

### 2.4 Gate Rollout

Add a platform setting:

```text
argocd.auto_adopt_clusters = true
argocd.manage_platform_baseline = true
```

Implemented:

- `argocd.manage_platform_baseline`, default `true`.
- `argocd.auto_adopt_clusters`, default `true`.

Default recommendation:

- `auto_adopt_clusters`: true for new installs.
- `manage_platform_baseline`: false until the migration is validated, then true.

Reason:

- Auto-registration is mostly additive.
- Moving baseline ownership from Helm-over-tunnel to ArgoCD changes reconciliation ownership and uninstall behavior.

---

## Phase 3: Replace Helm-Over-Tunnel Baseline Ownership

### 3.1 Keep Cluster Templates For Policy And Metadata

Cluster templates should still manage:

- Environment.
- Labels.
- Default project.
- Registration policy.

But baseline tools should be skipped when ArgoCD baseline management is enabled.

Implementation:

- In `cluster_template:apply`, if `argocd.manage_platform_baseline=true`, skip `spec.tools` entries that are part of the platform baseline.
- Write registration timeline steps saying "managed by ArgoCD" rather than "installed by Helm".

### 3.2 Migration For Existing Clusters

For clusters that already have baseline tools installed directly:

1. Auto-register them into ArgoCD.
2. Create/enable baseline ApplicationSets.
3. Let ArgoCD adopt or reconcile the resources.
4. Mark the old `installed_charts` rows as `managed_by=argocd` or equivalent.

Schema option:

```sql
ALTER TABLE installed_charts ADD COLUMN managed_by text NOT NULL DEFAULT 'astronomer';
```

Valid values:

- `astronomer`
- `argocd`

If adoption is too risky initially, leave existing clusters on Helm-over-tunnel and use ArgoCD only for newly adopted clusters.

### 3.3 Decommission Semantics

When a cluster is decommissioned:

- [x] Remove or disable the ArgoCD cluster Secret when the management-plane Kubernetes client is available.
- [x] Allow ApplicationSet-generated Applications to stop targeting the cluster by removing the cluster Secret.
- [x] Delete the `argocd_managed_clusters` row.
- [x] Emit audit events for orphaned upstream resources if cleanup fails or the Kubernetes client is unavailable.

Current implementation deletes the upstream cluster Secret by name, with a server-URL scan fallback for older rows that do not have `cluster_secret_name` populated.

Current implementation note:

- ApplicationSet pruning is controller-setting dependent. The live validation script includes `VALIDATE_PRUNING=true` to prove selector-based pruning in the target ArgoCD environment; exact decommission-secret deletion should also be included in staging release drills because it intentionally unregisters a cluster.

---

## Phase 4: UI And Operator Feedback

### 4.1 Cluster Registration Timeline

Add steps:

- [x] `argocd_registering`
- [x] `argocd_registered`
- [x] `argocd_registration_failed`
- [x] `baseline_appsets_matched`

Display these in the existing registration timeline component.

### 4.2 Cluster Detail

Add an ArgoCD adoption panel:

- Built-in ArgoCD instance.
- Registered/unregistered status.
- Cluster Secret name.
- Last label sync or refreshed labels when available from the ArgoCD cluster Secret refresh path.
- Baseline ApplicationSet match state through `baseline_appsets_matched` and GitOps ownership summary.
- Last error through the `ArgoCDAdopted=False` condition and failed timeline step.

### 4.3 ArgoCD Page

On the ArgoCD instance cluster tab:

- Show auto-adopted clusters separately from manually registered clusters if useful.
- [x] Add "Re-register" action.
- [x] Add "Refresh labels" action.
- Link to matching ApplicationSets.

---

## Phase 5: Tests And Validation

### Unit Tests

Add tests for:

- [x] Auto-register task idempotency and no-spam behavior for already managed clusters.
- Missing-cluster sweep.
- Credential request/response handling.
- Cluster proxy service-token authorization.
- Label projection parity between registration and refresh task.
- Cluster-template apply skipping baseline tools when ArgoCD owns baseline.
- [x] ArgoCD adoption timeline failure and `ArgoCDAdopted` condition writes.
- [x] Failed ArgoCD adoption retry queues `argocd:auto_register_cluster`.

### Integration Tests

Add validation script:

```text
scripts/validate-live-argocd-auto-adoption.sh
make validate-live-argocd-auto-adoption
```

Scenario:

1. Start a fresh management plane.
2. Register a fresh downstream k3d cluster.
3. Wait for agent connection.
4. Assert `argocd_managed_clusters` contains the new cluster.
5. Assert ArgoCD has a cluster Secret labeled `astronomer.io/managed-by=astronomer`.
6. Assert baseline ApplicationSets generate Applications for the cluster.
7. Assert at least one baseline component reaches Healthy/Synced.
8. Update cluster labels in Astronomer.
9. Assert ArgoCD Secret labels update and ApplicationSet targeting reacts.

Optional live pruning extension:

```text
VALIDATE_PRUNING=true scripts/validate-live-argocd-auto-adoption.sh
```

This temporarily changes the selected ArgoCD cluster Secret's selector labels so built-in baseline ApplicationSets no longer match, waits for generated baseline Applications to be pruned, restores the labels, and waits for fan-out again. Keep it opt-in because it intentionally perturbs a live ArgoCD registration.

### Acceptance Criteria

- New adopted clusters appear under built-in ArgoCD without manual token paste.
- ArgoCD can reconcile against adopted clusters through the Astronomer proxy.
- Built-in baseline ApplicationSets deploy components to adopted clusters automatically.
- Removing or changing cluster labels affects ApplicationSet membership.
- Registration timeline clearly shows ArgoCD adoption status.
- Failure to register into ArgoCD does not block core cluster connectivity, but it does surface a condition and retry.
- Existing manual registration endpoints continue to work.

---

## Proposed Implementation Order

1. [x] Add cluster-scoped service token support for ArgoCD proxy access.
2. [x] Add agent/server credential path or proxy-token registration path.
3. [x] Add `argocd:auto_register_cluster` task.
4. [x] Enqueue auto-registration on agent connected.
5. [x] Add recovery sweep for missing ArgoCD registrations.
6. [x] Add built-in baseline ApplicationSet renderer.
7. [x] Add platform settings to gate auto-adoption and ArgoCD-managed baseline.
8. [x] Update cluster-template apply to skip ArgoCD-owned baseline tools.
9. [x] Add CRD/status fields for ArgoCD adoption phase and baseline profile.
10. [x] Add UI status surfaces.
11. [x] Add live validation script and docs.

---

## Open Questions

- Should ArgoCD always use the Astronomer proxy URL for adopted clusters, or prefer direct `api_server_url` when present?
- Answered: baseline ownership is modeled per component for the five built-in ApplicationSets, and the cluster API/UI now expose each component's owner, namespace, and ApplicationSet name.
- Should existing Helm-over-tunnel baseline installs be adopted by ArgoCD or left alone until re-registration?
- How narrow can the ArgoCD cluster ServiceAccount permissions be while still supporting the baseline components?
- Should the operator be allowed to disable auto-adoption per cluster during registration?
- Should decommission always unregister from ArgoCD, or only for auto-adopted clusters?

---

## Non-Goals

- Day-0 cluster provisioning.
- Replacing ArgoCD's ApplicationSet controller.
- Building a Fleet-compatible API.
- Migrating all catalog installs to ArgoCD in this phase.
- Removing the Helm-over-tunnel install path.
