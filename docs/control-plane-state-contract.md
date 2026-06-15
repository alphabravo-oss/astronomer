# Control-Plane State Contract

Date: 2026-06-12

Astronomer uses Postgres as the durable product database and Kubernetes/etcd as the reconciliation substrate. That split is intentional: Postgres is better for relational product state, audit history, identity, credentials, and cross-cluster inventory; Kubernetes is better for declarative desired state, controller-owned status, and GitOps fan-out.

This contract defines which system owns each class of state and how conflicts must be handled.

## Ownership Classes

| Class | Owner | Examples | Contract |
| --- | --- | --- | --- |
| Product state | Postgres | users, teams, API tokens, RBAC bindings, projects, cluster inventory, operation rows, audit logs | Authoritative. Kubernetes objects may reference or mirror this state, but must not replace the DB as the system of record. |
| Secret material | Postgres encrypted columns or Kubernetes Secrets, depending on consumer | ArgoCD tokens, registry credentials, backup credentials, Dex client secrets | Store only where the consuming controller requires it. DB values must be encrypted or hashed; Kubernetes Secrets must be reconciled from encrypted source material or explicitly marked externally managed. |
| Operator intent | CRDs when enabled, otherwise REST/UI-backed Postgres rows | `Cluster`, `Project`, future `ClusterBaseline`, `ComponentBundle`, `AgentProfile`, and `GitOpsTarget` | CRD-owned rows carry `managed_by`, `external_ref_*`, and `observed_generation` metadata in Postgres when mirrored to product tables so REST/UI can reject or transfer ownership safely. |
| Kubernetes desired state | Kubernetes API and ArgoCD CRDs | ArgoCD `ApplicationSet`, `Application`, `AppProject`, cluster Secret labels, generated workload manifests | Kubernetes controllers own reconciliation. Astronomer writes desired state and observes status; it should not continuously overwrite controller-owned status fields. |
| Cached status | Postgres cache tables and CRD status | health summaries, vulnerability reports, discovered resources, ArgoCD adoption phase | Rebuildable. Losing cached status must not lose product intent or credentials. |

## Current Source Of Truth

| Domain | Source of Truth | Notes |
| --- | --- | --- |
| Users, sessions, API tokens | Postgres plus HttpOnly browser cookies | API-token secrets are hashed; browser sessions use HttpOnly cookies with CSRF protection for unsafe requests. |
| RBAC roles and bindings | Postgres | Middleware reads DB-backed role bindings. CRDs should not manage user identity or RBAC until an explicit identity API exists. |
| Cluster inventory | Postgres | CRD `Cluster` can create/update rows when CRDs are enabled. `clusters.managed_by`, `external_ref_*`, and `observed_generation` identify CRD-owned rows and support conflict handling. |
| Cluster connectivity and health | Agent tunnel plus Postgres status/cache | Heartbeats and health are observed status, not CRD spec. |
| Projects | Postgres | CRD `Project` can create/update rows when enabled. `projects.managed_by`, `external_ref_*`, and `observed_generation` identify CRD-owned rows and support conflict handling. |
| ArgoCD managed cluster registration | Postgres plus ArgoCD cluster Secret | Postgres tracks the product relationship; ArgoCD Secret is the Kubernetes credential surface consumed by ArgoCD. Repair jobs must be able to recreate either side. |
| Baseline component deployment | ArgoCD `ApplicationSet` when `argocd.manage_platform_baseline=true` | Legacy cluster-template install skips ArgoCD-owned baseline tools on adopted clusters when this setting is enabled. |
| Tool/catalog installs outside platform baseline | Postgres operation rows plus Helm release state | Existing Helm-over-tunnel path remains authoritative until a tool explicitly moves to ArgoCD ownership. |
| Audit history | Postgres | Kubernetes events/logs are supplemental and do not replace the audit table. |

`ClusterBaseline` override state follows the same split. Literal
`spec.bundles[].values` entries are product intent and may be mirrored or
audited as non-secret Helm parameters. Git `valuesFrom` entries are source
references and become Argo CD Helm `valueFiles`. Secret and ConfigMap
`valuesFrom` entries are same-namespace governance references only; the
controller must not persist rendered Secret/ConfigMap contents into Postgres or
inline them into generated Argo ApplicationSets.

## ArgoCD Cluster Secret Label Contract

Astronomer-owned ArgoCD cluster Secrets must carry the following labels so
ApplicationSet cluster generators and repair jobs can target the same clusters
without relying on Argo's generated Secret names:

- Always: `astronomer.io/managed-by=astronomer`,
  `astronomer.io/cluster-id`, `astronomer.io/cluster-name`, and
  `astronomer.io/is-local`.
- When set on the cluster row: `astronomer.io/environment`,
  `astronomer.io/region`, `astronomer.io/provider`, and
  `astronomer.io/distribution`.
- Always, from the normalized reserved cluster annotation:
  `astronomer.io/agent-privilege-profile`.
- When set on the cluster row, with Kubernetes-label-safe sanitized values:
  `astronomer.io/agent-version` and `astronomer.io/kubernetes-version`.
- For project membership: `astronomer.io/project-id.<uuid>=true` for every
  project on the cluster, `astronomer.io/project.<sanitized-name>=true` for
  every project with a usable name, and the singular `astronomer.io/project-id`
  / `astronomer.io/project` labels only when the cluster belongs to exactly one
  project.
- For user-managed cluster labels: sanitized
  `astronomer.io/label-<key>` projections.

User-provided registration labels must not use the `astronomer.io` or
`argocd.argoproj.io` prefixes.

## ArgoCD Baseline Sync-Wave Contract

Built-in platform baseline ApplicationSets stamp
`argocd.argoproj.io/sync-wave` on the generated Application template and
`astronomer.io/sync-phase` on both the ApplicationSet and generated
Applications. The current phase order is:

| Phase | Wave | Purpose |
| --- | ---: | --- |
| `namespaces` | -40 | Namespace and prerequisite tenancy objects. |
| `crds` | -30 | CRD installers and CRD-owning platform packages such as cert-manager. |
| `operators` | -20 | Controllers/operators that reconcile later resources. |
| `policies` | -10 | Admission, network, image, and platform policy bundles. |
| `workloads` | 10 | Normal baseline workloads such as metrics and logging agents. |
| `health-checks` | 30 | Post-deploy scanners, health checks, and validators. |

The policy phase is intentionally reserved even when no default policy bundle
is installed. Future policy-stack bundles should use that phase so they run
after CRDs/operators are available but before general workloads depend on the
policy posture.

## Write Precedence

1. CRD-owned `clusters` and `projects` rows must set `managed_by=crd`, `external_ref_*`, and `observed_generation` metadata in Postgres.
2. REST/UI updates to CRD-owned `Cluster` and `Project` spec fields are rejected with `409 conflict`; takeover is explicit through `POST /api/v1/clusters/{id}/ownership/takeover/` or `POST /api/v1/projects/{id}/ownership/takeover/`.
3. REST/UI updates to non-CRD-owned fields may continue if they do not mutate CRD-owned spec fields.
4. ArgoCD-owned deployment fields must not be mutated by the legacy Helm-over-tunnel reconciler.
5. Controller-owned status must be written only to status fields or cache tables, never back into declarative spec without an explicit reconcile decision.

## Restore And Repair Rules

| Scenario | Required Behavior |
| --- | --- |
| Postgres restored, CRDs still exist | Reconcile CRDs against DB rows by `external_ref`; recreate missing rows only when ownership metadata proves CRD ownership or operator opts into import. |
| CRDs restored, Postgres rows already exist | Match by `external_ref` first. Refuse same-name rows that are not already owned by the same CRD until an operator runs an explicit ownership-transfer/import operation. |
| ArgoCD cluster Secret missing, DB row exists | Recreate the Secret from encrypted proxy token material or mark adoption degraded if token material is unavailable. |
| ArgoCD Secret exists, DB row missing | Import only if labels prove Astronomer ownership; otherwise leave unmanaged and surface drift. |
| Worker operation stuck `running` | Recovery sweeps should move stale operations to retryable/failed state using durable idempotency keys. |

## CRD Expansion Rules

Add a new Astronomer CRD only when it gives operators a useful declarative workflow. Do not mirror every Postgres row into Kubernetes.

New CRDs must include:

- OpenAPI validation for required fields, enums, min/max values, and defaults.
- `status` subresource.
- Standard Kubernetes conditions with `type`, `status`, `reason`, `message`, `observedGeneration`, and `lastTransitionTime`.
- Finalizers when external cleanup is required.
- Printer columns for phase/readiness and owning DB row when applicable.
- Versioning and conversion strategy before a breaking schema change.

## Planned CRD Ownership

These CRDs are the planned Kubernetes-facing desired-state surfaces. They do not make Postgres behave like etcd; they expose operator intent while Postgres keeps product history, auditability, and relational views.

| CRD | Owns | Does Not Own | Backing Product State |
| --- | --- | --- | --- |
| `Cluster` | Adopted cluster declarative metadata and adoption intent | Raw workload objects, live health, or Kubernetes node/pod state | `clusters`, ownership metadata, registration/adoption operation rows |
| `Project` | Project policy intent and cluster membership references | User identity or global RBAC by itself | `projects`, ownership metadata, project operation rows |
| `ClusterBaseline` | Desired baseline profile targeting and sync policy for clusters/groups | Individual Argo Application health or raw Helm release state | baseline operation rows, Argo ApplicationSet ownership references, status caches |
| `ComponentBundle` | Reusable component source, default values contract, capability requirements, and upgrade policy | Secret values or rendered Kubernetes manifests as product state | bundle catalog rows or Git-backed bundle references, audit/operation history |
| `AgentProfile` | Agent privilege mode, allowed feature surface, namespace scope, and install posture | Per-request user authorization or standalone agent decisions | cluster annotations/profile references, agent status/compatibility rows |
| `GitOpsTarget` | Argo ApplicationSet targeting policy, selectors, sync windows, prune/self-heal intent | Argo controller status or target-cluster workload state | Argo operation rows, ApplicationSet references, drift summary cache |

Every planned CRD must use `secretRef` for secret material, write status without leaking credentials, and record durable product operations in Postgres when reconciliation creates, updates, or deletes external state.

## Required Follow-Up Work

- Add an explicit ownership-transfer operation for taking a CRD-owned `Cluster` or `Project` back under UI/API ownership.
- Add drift repair jobs for Postgres, CRDs, ArgoCD Secrets, and ArgoCD Applications.
- Keep the Postgres restore runbook and restore drill current with every schema release, including mismatched Postgres and Kubernetes/etcd snapshot scenarios.
- Enforce the production Postgres contract from `deploy/chart/README.md`: external managed/HA Postgres, TLS, backups, PITR, restore drills, and pool sizing.
