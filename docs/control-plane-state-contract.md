# Control-Plane State Contract

Date: 2026-06-12

Astronomer uses Postgres as the durable product database and Kubernetes/etcd as the reconciliation substrate. That split is intentional: Postgres is better for relational product state, audit history, identity, credentials, and cross-cluster inventory; Kubernetes is better for declarative desired state, controller-owned status, and GitOps fan-out.

This contract defines which system owns each class of state and how conflicts must be handled.

## Ownership Classes

| Class | Owner | Examples | Contract |
| --- | --- | --- | --- |
| Product state | Postgres | users, teams, API tokens, RBAC bindings, projects, cluster inventory, operation rows, audit logs | Authoritative. Kubernetes objects may reference or mirror this state, but must not replace the DB as the system of record. |
| Secret material | Postgres encrypted columns or Kubernetes Secrets, depending on consumer | ArgoCD tokens, registry credentials, backup credentials, Dex client secrets | Store only where the consuming controller requires it. DB values must be encrypted or hashed; Kubernetes Secrets must be reconciled from encrypted source material or explicitly marked externally managed. |
| Operator intent | CRDs when enabled, otherwise REST/UI-backed Postgres rows | `Cluster`, `Project`, future `AdoptionPolicy`, future `BaselineProfile` | CRD-owned `clusters` and `projects` rows carry `managed_by`, `external_ref_*`, and `observed_generation` metadata in Postgres so REST/UI can reject or transfer ownership safely. |
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

## Required Follow-Up Work

- Add an explicit ownership-transfer operation for taking a CRD-owned `Cluster` or `Project` back under UI/API ownership.
- Add drift repair jobs for Postgres, CRDs, ArgoCD Secrets, and ArgoCD Applications.
- Keep the Postgres restore runbook and restore drill current with every schema release, including mismatched Postgres and Kubernetes/etcd snapshot scenarios.
- Enforce the production Postgres contract from `deploy/chart/README.md`: external managed/HA Postgres, TLS, backups, PITR, restore drills, and pool sizing.
