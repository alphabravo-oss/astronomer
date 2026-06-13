# Rancher-Grade Day-2 Management Plan

Date: 2026-06-13
Status: In progress
Scope: Cluster Explorer, RBAC, agent fleet lifecycle, automation/GitOps, UI extensibility, auth/settings, observability, service mesh, controller consistency, and scale/HA validation.

## Implementation Progress

Implemented on 2026-06-13:

- Expanded the embedded RBAC role-template catalog from the original eight templates to a Rancher-grade day-2 catalog covering platform operations, audit, security, compliance, GitOps, observability, catalog, backup/restore, support bundles, node operations, service mesh, storage, config, secrets, services/ingress, and workload deployment/viewing.
- Added role-template metadata for risk level, inherited template references, and system-managed status; the loader now validates risk levels and infers risk when a template omits it.
- Added first-class RBAC resources for narrower future guards: `secrets`, `configmaps`, `services`, `ingresses`, `storage`, `nodes`, `service_mesh`, and `support_bundles`.
- Added migration `098_rancher_grade_role_catalog` to seed missing built-in database roles for upgraded installs while leaving existing custom and built-in roles untouched.
- Strengthened RBAC contract tests so the embedded template catalog and database seed catalog cannot drift silently.
- Added a read-only Agent Fleet backend endpoint at `/api/v1/agents/fleet/`, protected by `agents:read`, with per-cluster agent status, version, active session, privilege profile, inferred capabilities, degraded reasons, and summary counts.
- Added an Agent Fleet dashboard page at `/dashboard/agents` and sidebar navigation entry gated by `agents:read`.
- Added Agent Fleet diagnostics and upgrade-plan endpoints, plus UI actions for per-cluster diagnostics and safe upgrade recommendation display.
- Added redacted Agent Fleet diagnostics bundle download at `/api/v1/agents/fleet/{cluster_id}/diagnostics/bundle/` and a drawer action to download the JSON attachment for support triage.
- Added live Agent Fleet diagnostics collection through the existing Kubernetes tunnel: agent Deployment summary, agent pod status, recent events, tail-limited redacted logs, `/version`, and `/apis` discovery are included when the cluster agent is connected.
- Added durable agent lifecycle operation intent storage with migration `101_agent_lifecycle_operations`, plus `/operations/` history and `/upgrade/` queue endpoints. The UI can now queue an agent upgrade intent and show recent lifecycle operation state.
- Added agent-side upgrade convergence over the existing tunnel: heartbeats claim pending or stale lifecycle operations, the server sends an `AGENT_UPGRADE` command, the connected agent patches its own `astronomer-system/astronomer-agent` Deployment image, and `AGENT_UPGRADE_RESULT` marks the operation succeeded or failed. Running operations are also reconciled as succeeded when a later heartbeat reports the target version.
- Added Kubernetes YAML dry-run support to the shared YAML dialog. The UI now exposes a `Dry run` action that sends `dryRun=All`, `fieldManager=astronomer`, and `fieldValidation=Warn` through the existing authenticated Kubernetes proxy before save.
- Added Kubernetes YAML diff/conflict preview in the shared YAML dialog, including stale object warnings when server-side `resourceVersion` changes after the editor opens.
- Added effective-permission APIs for current user inspection, target user inspection, and binding preview before saving assignments.
- Added ArgoCD baseline ownership migration APIs and cluster-detail UI for component ownership decisions: adopt, leave local, and replace.
- Added service mesh inventory and policy validation endpoints. Inventory lists Istio `Gateway`, `VirtualService`, `DestinationRule`, `AuthorizationPolicy`, `PeerAuthentication`, `Sidecar`, and `ServiceEntry` resources through the agent tunnel and marks ArgoCD-owned policies read-only. Validation catches required object fields, unsupported kinds, bad VirtualService route-weight totals, mTLS disable warnings, and GitOps ownership warnings.
- Added service mesh UI panels for resource inventory, GitOps ownership badges, and policy validation.
- Added UI extension registry support with migration `100_ui_extensions`, manifest validation, install/upsert, enable/disable, compatibility checks, audit hooks, OpenAPI docs, a sample manifest, and `/dashboard/extensions`.
- Added scale-profile support to `scripts/loadtest`, including small/medium/large/extreme lab YAML profiles, resource-cardinality settings, reconnect-storm scheduling, profile parsing tests, and report sections for day-2 failure drills.
- Replaced the client-side node drain sequence with a server-side node drain workflow: cordon, list pods on the node, skip DaemonSet/static/terminal pods, block `emptyDir` pods unless explicitly allowed, submit `policy/v1` evictions, return evicted/skipped/failed details, and document the node action API.
- Added RBAC-gated server-side node metadata and taint action APIs for label, annotation, taint, and untaint workflows; the node detail UI now uses those APIs instead of generic browser-side Kubernetes patch calls and exposes annotation management alongside labels.
- Validation passed for the implemented slice:
  - `go test ./...`
  - `npm run type-check`
  - `npm test -- --runInBand`
  - `git diff --check`

## Explicit Non-Goal

Astronomer will not implement Rancher-style cluster provisioning in this plan.

Out of scope:

- Creating EKS, GKE, AKS, RKE2, k3s, Harvester, vSphere, or custom-node clusters.
- Node drivers, cloud node pools, machine configs, cloud credential provisioning, or cluster upgrade orchestration for infrastructure-owned clusters.
- Replacing cloud-provider consoles, Cluster API providers, Terraform, Crossplane, or existing infrastructure pipelines.

Product position:

- Astronomer manages clusters that already exist.
- The quality bar is Rancher-grade day-2 operations for adopted clusters, not Rancher-grade day-0 infrastructure lifecycle.
- Imported cluster management must be deep enough that users do not feel the lack of provisioning once a cluster is registered.

## Guiding Principles

- Keep Postgres as the management-plane source of truth for users, RBAC, settings, operations, audit, cluster registration state, and durable workflow intent.
- Use Kubernetes CRDs as declarative integration surfaces and reconciliation targets, not as the only source of truth for every control-plane concern.
- Use ArgoCD as the desired-state engine for baseline and application deployment to adopted clusters.
- Use the agent for secure, least-privilege, cluster-local execution, watching, proxying, diagnostics, and capability reporting.
- Prefer durable operations and reconcilers over request-time imperative mutations.
- Every high-impact action must be auditable, retryable, idempotent, and visible in the UI.
- UI surfaces should be operational tools, not marketing pages: dense, fast, filterable, live, and safe for repeated daily use.

## Current Posture

The current system has a strong foundation:

- Adopted-cluster registration and agent tunnel exist.
- ArgoCD auto-adoption and baseline ApplicationSet management exist.
- The management plane has hardened auth, route guards, stream tickets, CSRF protection, network policies, task outbox, repair jobs, and production runtime validation.
- Monitoring, logging, backups, projects, GitOps, quotas, compliance, security, and operations pages exist.
- Cluster CRDs and ownership protections exist.

The remaining gap is mostly product depth and consistency:

- Some pages still poll instead of feeling live.
- Cluster resource workflows are not yet as complete as Rancher's Explorer.
- RBAC templates are too sparse.
- Agent lifecycle management is not yet a first-class fleet feature.
- Automation needs stronger migration/adoption/override behavior.
- Extensibility, auth/settings breadth, observability depth, and service mesh UX trail Rancher's mature surfaces.
- Scale and HA behavior needs continuous validation under failure and high-cardinality conditions.

## Workstream 1: Rancher-Grade Cluster Explorer

### Reasoning

Cluster Explorer is the daily cockpit. If users manage imported clusters, they need to inspect, edit, debug, and recover workloads without leaving Astronomer. Rancher's value is not just listing resources; it is the depth of workflows around resources.

Astronomer already has tunnel-backed Kubernetes APIs, YAML editor components, live-event plumbing, and resource pages. The next step is turning those pieces into a consistent Explorer experience across common resource kinds.

### Target Capabilities

- Live resource lists with watch-driven invalidation.
- Resource detail drawers for common kinds.
- Schema-aware YAML editor with dry-run, diff, conflict detection, and apply.
- Safe delete, scale, restart, rollout undo, and bulk actions.
- Events, conditions, owner references, labels, annotations, and managed fields in every detail view.
- Integrated logs, exec, shell, and service proxy entry points where permissions allow them.
- Node operations for imported clusters: cordon, uncordon, drain, taint, label, annotate, and view allocatable pressure.
- Namespace/project context switching.
- Saved filters and quick search across clusters.

### Tasks

- [ ] Build a shared `ResourceTable` contract for Kubernetes resources:
  - cluster id
  - namespace
  - kind
  - name
  - labels
  - age
  - health summary
  - warnings count
  - selected rows
  - bulk action support
- [ ] Add live watch integration to resource list hooks:
  - replace short polling where an agent informer exists
  - keep polling fallback when the agent does not support a kind
  - surface "live", "degraded", and "polling fallback" states
- [ ] Add a shared resource detail drawer:
  - summary tab
  - YAML tab
  - events tab
  - conditions tab
  - related resources tab
  - access/audit tab for high-risk objects
- [x] Add dry-run apply support for generic Kubernetes resources:
  - server-side dry run
  - field manager set to `astronomer`
  - RBAC enforced by the authenticated Kubernetes proxy
  - mutation audit emitted by the proxy path; diff summaries remain part of the diff workflow below
- [x] Add diff support before apply:
  - original object
  - edited object
  - server-side normalized object
  - conflict warnings for resourceVersion mismatch
- [ ] Add safe delete flow:
  - foreground/background/orphan propagation policy
  - force delete disabled by default
  - finalizer warning
  - owner-reference impact preview
- [ ] Add workload actions:
  - scale Deployment/StatefulSet/ReplicaSet
  - restart Deployment
  - pause/resume Deployment rollout
  - undo rollout where history exists
- [ ] Add pod actions:
  - logs
  - previous logs
  - exec shell when authorized
  - delete pod
  - view mounts, probes, env sources, image pull status
- [x] Add node actions:
  - [x] cordon/uncordon
  - [x] drain with options for daemonsets and local data
  - [x] taint/untaint
  - [x] label/annotate
  - [x] pressure and allocatable summary
- [ ] Add bulk actions:
  - delete selected resources
  - label selected resources
  - annotate selected resources
  - restart selected Deployments
  - export selected YAML
- [ ] Add UI affordances:
  - stable table density
  - column picker
  - namespace selector
  - kind selector
  - saved filters
  - keyboard-safe modals with explicit submit/cancel states

### Examples

Dry-run apply response shape:

```json
{
  "dry_run": true,
  "field_manager": "astronomer",
  "changed": true,
  "warnings": [
    "resourceVersion is stale; server object changed after this editor opened"
  ],
  "diff": {
    "format": "unified",
    "lines_added": 4,
    "lines_removed": 1
  },
  "normalized_object": {
    "apiVersion": "apps/v1",
    "kind": "Deployment",
    "metadata": {
      "name": "api",
      "namespace": "default"
    }
  }
}
```

Node drain options:

```json
{
  "delete_empty_dir_data": false,
  "ignore_daemonsets": true,
  "grace_period_seconds": 60,
  "timeout_seconds": 900
}
```

### Tests

- Unit tests for resource path parsing and verb/resource RBAC mapping.
- Handler tests for dry-run, apply, delete, scale, restart, and node operations.
- Frontend tests for:
  - stale object conflict warning
  - diff render
  - bulk action disabled states
  - read-only user restrictions
  - failed mutation error display with request id
- Playwright flows for:
  - edit YAML and dry-run
  - scale Deployment
  - delete resource
  - view pod logs
  - open node drain dialog without submitting
- Agent integration tests with a kind cluster:
  - informer event causes UI invalidation
  - polling fallback works when live events are disabled

### Validation

- `go test ./...`
- `npm run type-check`
- `npm test -- --runInBand`
- Playwright smoke tests for Explorer workflows.
- Manual validation against a kind cluster and one real adopted cluster.
- Audit log inspection for every mutating Explorer action.

### Definition of Done

- A read-only user can inspect resources but cannot mutate them.
- An operator can edit a Deployment through YAML, see a diff, dry-run, apply, and observe live status changes.
- A cluster admin can cordon, uncordon, drain, taint, label, and annotate a node through the UI.
- Resource pages stay responsive with at least 5,000 resources in a cluster.
- Every mutating action has an audit row, operation status where needed, and a visible error path.

## Workstream 2: Expanded RBAC and Tenancy Templates

### Reasoning

Rancher's role templates are a major part of its enterprise value. Astronomer has core authorization, but the built-in role catalog needs enough breadth that admins can delegate real operational work without handing out broad admin privileges.

### Target Capabilities

- Built-in global, cluster, project, namespace, GitOps, security, backup, observability, and catalog roles.
- Role template inheritance.
- Custom role templates through UI and CRD/API.
- Permission preview before assignment.
- Effective permission inspection for a user/group.
- Deny unsafe combinations where a role would accidentally escalate privileges.

### Tasks

- [x] Expand built-in role templates:
  - global admin
  - platform operator
  - security auditor
  - compliance manager
  - cluster owner
  - cluster member
  - cluster viewer
  - project owner
  - project member
  - project viewer
  - namespace operator
  - workload deployer
  - workload viewer
  - secret manager
  - config manager
  - service/ingress manager
  - storage manager
  - node operator
  - backup operator
  - restore operator
  - GitOps admin
  - GitOps deployer
  - GitOps viewer
  - catalog admin
  - catalog installer
  - monitoring admin
  - monitoring viewer
  - logging viewer
  - service mesh operator
  - audit viewer
  - support bundle operator
- [x] Add role template metadata:
  - display name
  - category
  - scope
  - risk level
  - included permissions
  - inherited templates
  - system-managed flag
- [x] Add effective-permission API:
  - by user
  - by group
  - by cluster/project scope
  - include source binding and inherited role template
- [x] Add permission preview API before saving a binding.
- [ ] Add role-template UI:
  - built-in templates
  - custom templates
  - compare roles
  - clone built-in role to custom role
  - assignment preview
- [ ] Add guardrails:
  - prevent project roles from granting global permissions
  - flag roles that can read secrets
  - flag roles that can exec into pods
  - require confirmation for roles that can mutate RBAC or service accounts
- [ ] Add CRD/API support if declarative role templates are desired:
  - `RoleTemplate`
  - `RoleTemplateBinding`
  - status conditions for invalid or unsafe templates

### Examples

Role template shape:

```yaml
apiVersion: management.astronomer.io/v1alpha1
kind: RoleTemplate
metadata:
  name: workload-deployer
spec:
  scope: project
  displayName: Workload Deployer
  permissions:
    - resource: workloads
      verbs: ["read", "create", "update", "delete"]
    - resource: k8s.deployments
      verbs: ["get", "list", "watch", "patch"]
    - resource: k8s.pods.exec
      verbs: []
  riskLevel: medium
```

Effective permission response:

```json
{
  "subject": "user:alice",
  "scope": "project:payments",
  "permissions": [
    {
      "resource": "workloads",
      "verb": "update",
      "source": "role_template:workload-deployer"
    }
  ]
}
```

### Tests

- Unit tests for role inheritance and scope validation.
- Authorization matrix tests for every built-in role.
- Handler tests for permission preview and effective-permission APIs.
- UI tests proving hidden/disabled controls align with backend authorization.
- Regression tests for sensitive actions:
  - secret read
  - pod exec
  - service proxy mutation
  - backup restore
  - ArgoCD sync/prune
  - Kubernetes YAML apply

### Validation

- Generate a permission matrix artifact in CI.
- Run route guard tests with representative roles.
- Manually test a viewer, project operator, cluster owner, and platform operator.
- Confirm every UI-denied action is also backend-denied.

### Definition of Done

- Admins can delegate common operations without using superuser access.
- Every high-risk permission is documented and visible in the UI.
- Effective permission inspection explains why a user can or cannot perform an action.
- Built-in roles cover the main imported-cluster workflows.

## Workstream 3: Agent Fleet Lifecycle

### Reasoning

The agent is the trust bridge into adopted clusters. It needs to be managed as a fleet, not as a hidden implementation detail. Rancher exposes agent health, version skew, cluster connectivity, and remediation workflows. Astronomer needs the same operational clarity.

### Target Capabilities

- Fleet-wide agent inventory.
- Agent version, build SHA, protocol version, privilege profile, and capability reporting.
- Agent self-upgrade orchestration.
- Health timeline and reconnect history.
- Diagnostics bundle collection.
- Token and CA rotation workflows.
- Agent degraded-state reporting for informer lag, send-buffer pressure, Kubernetes API errors, and permission denials.
- Per-cluster agent privilege profile visibility and upgrade recommendations.

### Tasks

- [ ] Add agent heartbeat payload fields:
  - agent version
  - build SHA
  - protocol version
  - Kubernetes version
  - agent namespace
  - service account name
  - privilege profile
  - enabled capabilities
  - informer sync status
  - queue depth and dropped event counters
  - last Kubernetes API error
- [ ] Persist agent inventory and history:
  - current state table
  - heartbeat history table with retention
  - reconnect event table
  - capability snapshot table
- [x] Add read-only Agent Fleet inventory page:
  - all agents
  - version skew
  - disconnected agents
  - degraded agents
  - privilege profile distribution
- [x] Add full Agent Fleet UI:
  - all agents
  - version skew
  - disconnected agents
  - degraded agents
  - privilege profile distribution
  - upgrade available
  - last error
- [ ] Add agent diagnostics *(partial: live logs/events/discovery and support bundle exist; informer sync internals remain)*:
  - [x] collect agent logs
  - [x] collect recent tunnel events
  - [ ] collect informer sync state
  - [x] collect Kubernetes discovery output
  - [x] redact secrets and tokens
  - [x] package as support bundle artifact
- [x] Add agent self-upgrade operation:
  - [x] desired version set by management plane
  - [x] connected agent receives desired version/image through the tunnel
  - [x] upgrade delivered by patching the agent Deployment image in the adopted cluster
  - [x] operation tracks pending/running/succeeded/failed
  - [x] stale running operations are retryable and target-version heartbeats reconcile success
  - [x] rollback recommendation on failure
- [ ] Add CA/token rotation flows:
  - generate next token
  - overlap current and next token
  - agent switches to next token
  - revoke old token after confirmation
  - alert if rotation stalls
- [ ] Add degraded-state alerts:
  - disconnected beyond threshold
  - informer not synced
  - event drops
  - tunnel reconnect storm
  - agent version unsupported
  - privilege profile broader than recommended
- [ ] Add stale credential cleanup:
  - remove abandoned registration secrets
  - remove stale pull secrets created by Astronomer
  - clean old bootstrap tokens after adoption completes

### Examples

Agent heartbeat payload:

```json
{
  "agent_version": "0.9.4",
  "build_sha": "7180411",
  "protocol_version": "2026-06-13",
  "kubernetes_version": "v1.30.2",
  "privilege_profile": "operator",
  "capabilities": {
    "watch": true,
    "logs": true,
    "exec": false,
    "helm": true,
    "service_proxy": true
  },
  "health": {
    "informer_synced": true,
    "send_queue_depth": 3,
    "dropped_events_total": 0,
    "last_error": ""
  }
}
```

### Tests

- Agent unit tests for heartbeat payload construction.
- Server handler tests for ingesting heartbeat and preserving history.
- Upgrade operation tests for idempotency and retry.
- Handler tests for upgrade plan, blocked queue, queued lifecycle operation, operation history, and diagnostics bundle redaction.
- Diagnostics redaction tests to prevent token/secret leakage.
- Integration tests for reconnect and capability changes.
- UI tests for fleet filtering and degraded status rendering.

### Validation

- Run two agents with different versions and confirm version-skew UI.
- Disable a permission in the agent service account and confirm capability degradation is visible.
- Force reconnect loops and confirm alerts/timeline.
- Generate diagnostics bundle and inspect redaction.

### Definition of Done

- Operators can answer "which agents are unhealthy and why?" from one screen.
- Agent upgrades are tracked as operations, not manual guesswork.
- Token/CA rotation can be performed without losing the cluster.
- Diagnostics bundles are useful and safe to share.

## Workstream 4: Automation and GitOps Hardening

### Reasoning

ArgoCD is the chosen deployment automation engine. The auto-adoption path is now present, but production-grade GitOps requires migration safety, ownership clarity, override controls, and drift visibility.

### Target Capabilities

- Existing baseline components can be adopted into Argo ownership or intentionally left local.
- Every managed baseline component has clear ownership metadata.
- Operators can opt clusters or components out of auto-management.
- ArgoCD cluster registration has least-privilege service accounts.
- ApplicationSet fan-out is validated continuously.
- Drift, prune, sync, and health are visible from Astronomer.
- GitOps actions are auditable and retryable.

### Tasks

- [x] Add baseline ownership migration flow:
  - detect existing Helm-managed baseline components
  - detect existing Argo-managed components
  - compare desired ApplicationSet target
  - present adopt, leave local, or replace options
  - record decision per cluster/component
- [x] Add explicit ownership states:
  - `argocd_owned`
  - `legacy_helm`
  - `local_manual`
  - `external_argocd`
  - `unmanaged`
  - `migration_required`
- [ ] Add cluster/component opt-outs:
  - cluster-level auto-adoption disabled
  - component-level baseline disabled
  - temporary maintenance suppression
  - reason and expiry fields
- [ ] Harden ArgoCD service account permissions:
  - baseline read/write by namespace
  - cluster Secret management only in Argo namespace
  - no broad cluster-admin unless explicitly configured
  - profile-specific permission templates
- [ ] Add ArgoCD health mirror:
  - Application sync status
  - Application health
  - operation phase
  - drift summary
  - last sync error
  - prune pending warning
- [ ] Add adoption repair jobs:
  - missing cluster Secret
  - stale server URL
  - stale labels
  - missing ApplicationSet target
  - Argo app orphaned after cluster decommission
- [ ] Add release-drill validation:
  - create cluster
  - register agent
  - auto-register with ArgoCD
  - fan out baseline ApplicationSets
  - delete cluster
  - verify Argo Secret and apps are cleaned up
- [ ] Add UI:
  - GitOps ownership panel per cluster
  - baseline component migration panel
  - Argo health and drift badges
  - retry adoption action
  - opt-out editor with required reason

### Examples

Baseline ownership state:

```json
{
  "cluster_id": "clu_123",
  "component": "monitoring",
  "desired_owner": "argocd",
  "observed_owner": "legacy_helm",
  "state": "migration_required",
  "options": ["adopt", "leave_local", "replace"],
  "last_checked_at": "2026-06-13T12:00:00Z"
}
```

Opt-out:

```yaml
argocd:
  autoAdopt: false
  reason: "Cluster is managed by a regulated external ArgoCD instance"
  expiresAt: "2026-09-01T00:00:00Z"
```

### Tests

- Unit tests for ownership state calculation.
- Worker tests for adoption repair jobs.
- Handler tests for opt-out authorization and audit.
- Kubernetes integration tests for Argo Secret create/update/delete.
- Helm render tests for least-privilege Argo service accounts.
- UI tests for migration decision flows.

### Validation

- Run the live ArgoCD auto-adoption validation script against a managed cluster.
- Simulate an existing Helm baseline and verify the UI reports migration required.
- Remove the Argo cluster Secret and verify repair recreates it.
- Delete a cluster and verify Argo cleanup completes.

### Definition of Done

- Baseline ownership is never ambiguous.
- Operators can safely migrate existing components to ArgoCD management.
- Auto-adoption can be disabled with an explicit, auditable reason.
- Argo drift and health are visible without opening ArgoCD directly.

## Workstream 5: UI Extension and Plugin Model

### Reasoning

Rancher has a UI extension model and plugin marketplace. Astronomer does not need to clone Rancher's exact extension architecture, but it does need a safe way to add organization-specific or ecosystem-specific UI surfaces without forking the main frontend.

### Target Capabilities

- Extension manifest format.
- Admin install/enable/disable controls.
- Navigation injection.
- Dashboard widget injection.
- Cluster detail tab injection.
- Settings page injection.
- Permissions declared up front.
- Extension assets served safely.
- Version compatibility checks.

### Tasks

- [x] Define extension manifest:
  - name
  - version
  - compatible Astronomer range
  - routes
  - nav items
  - widgets
  - required permissions
  - backend API scopes
  - content security policy requirements
- [x] Add extension registry table:
  - installed version
  - enabled state
  - source
  - checksum
  - compatibility status
- [ ] Add extension loader:
  - signed or checksum-verified bundle
  - static asset serving
  - isolated route namespace
  - frontend runtime registration
- [ ] Add safety controls *(partial: compatibility, admin install, audit, CSP validation, and fail-closed enablement exist; signed bundle isolation remains)*:
  - disable extension on compatibility failure
  - admin-only install
  - audit install/enable/disable
  - CSP restrictions
  - no token access from extension code
- [ ] Add extension points:
  - sidebar section
  - dashboard widget
  - cluster detail tab
  - resource action
  - settings page
- [ ] Add starter SDK:
  - TypeScript types
  - manifest schema
  - sample extension
  - local development command
- [x] Add UI:
  - Extensions page
  - installed extensions
  - available extensions if marketplace source exists
  - compatibility warnings
  - permissions review

### Examples

Extension manifest:

```json
{
  "apiVersion": "extensions.astronomer.io/v1alpha1",
  "name": "cost-insights",
  "version": "0.1.0",
  "compatibleAstronomer": ">=0.9.0 <1.0.0",
  "entry": "index.js",
  "permissions": [
    "clusters:read",
    "metrics:read"
  ],
  "extensionPoints": {
    "sidebar": [
      {
        "label": "Cost",
        "path": "/dashboard/extensions/cost-insights"
      }
    ],
    "clusterTabs": [
      {
        "label": "Cost",
        "component": "ClusterCostTab"
      }
    ]
  }
}
```

### Tests

- Manifest schema tests.
- Backend install/enable/disable authorization tests.
- Checksum/signature validation tests.
- Frontend loader tests for route/nav injection.
- CSP tests to confirm extensions cannot relax global security headers.
- Playwright test for installing sample extension and seeing injected nav.

### Validation

- Install sample extension in dev.
- Disable sample extension and verify routes/nav disappear.
- Attempt incompatible extension and verify it is blocked.
- Attempt extension requiring permissions the user lacks and verify UI hides it.

### Definition of Done

- A sample extension can add a page, nav item, and cluster tab without modifying core frontend files.
- Admins see exactly what permissions an extension needs.
- Extensions cannot access browser session tokens.
- Incompatible extensions fail closed.

## Workstream 6: Auth, Settings, and Admin Breadth

### Reasoning

Enterprise platforms are judged by setup and administration quality. Astronomer already has auth and settings surfaces, but Rancher-grade maturity requires richer provider configuration, safer settings rollout, better auditability, and clearer operational feedback.

### Target Capabilities

- OIDC, SAML-via-Dex, LDAP/AD-via-Dex, and local auth settings with guided validation.
- Group-to-role mapping preview.
- SCIM or external identity sync option.
- Settings change audit and rollback.
- Readiness checks before enabling risky auth changes.
- Platform settings organized by operational domain.

### Tasks

- [ ] Expand auth provider UI:
  - OIDC generic
  - Dex-backed SAML
  - Dex-backed LDAP/AD
  - Okta/Azure AD presets
  - GitHub/Google presets where already supported
- [ ] Add provider validation:
  - discovery URL reachable
  - JWKS available
  - redirect URI matches
  - test login flow
  - test group claims
- [ ] Add group mapping preview:
  - sample claims input
  - resolved groups
  - resolved role bindings
  - missing/default role warnings
- [ ] Add SCIM/external identity sync design:
  - user create/update/disable
  - group membership sync
  - audit trail
  - dry-run mode
- [ ] Add settings versioning:
  - previous value hash
  - changed by
  - changed at
  - rollout status
  - rollback action where safe
- [ ] Add risky setting guardrails:
  - disabling all auth providers blocked unless local break-glass is configured
  - changing issuer requires validation
  - changing session/security settings shows impact
  - production-only warnings for insecure options
- [ ] Add admin status page:
  - auth provider health
  - session/cookie security status
  - encryption key status
  - outbound SMTP/webhook status
  - backup destination status
  - license status if applicable

### Examples

Group mapping preview:

```json
{
  "claims": {
    "email": "alice@example.com",
    "groups": ["platform-admins", "payments-dev"]
  },
  "resolved_bindings": [
    {
      "group": "platform-admins",
      "role": "platform-operator",
      "scope": "global"
    },
    {
      "group": "payments-dev",
      "role": "project-member",
      "scope": "project:payments"
    }
  ]
}
```

### Tests

- Provider validation handler tests.
- Group mapping resolution tests.
- Settings audit/versioning tests.
- UI tests for auth preset forms and validation failures.
- Production configuration tests for unsafe auth states.

### Validation

- Configure generic OIDC in dev and verify login.
- Validate a Dex-backed LDAP config with test connection.
- Attempt to disable the last working provider and verify it is blocked.
- Change a setting and confirm audit/version history.

### Definition of Done

- Operators can configure and validate enterprise auth without editing raw config.
- Every auth/settings change is auditable.
- Dangerous settings fail closed in production.
- Group-to-role behavior can be previewed before rollout.

## Workstream 7: Observability Dashboard Depth

### Reasoning

Imported-cluster management needs fast operational diagnosis. Astronomer has monitoring primitives, but Rancher-grade operations need broad dashboards, drilldowns, and consistent links from resources to metrics and logs.

### Target Capabilities

- Kubernetes dashboard library comparable to kube-prometheus-stack coverage.
- Cluster, node, namespace, workload, pod, container, control-plane, ingress, storage, and network dashboards.
- Project-scoped dashboards.
- Metrics/logs/event drilldowns from resource detail pages.
- Alert correlation.
- Dashboard-as-code import/export.
- Performance-safe rendering with high-cardinality data.

### Tasks

- [ ] Add dashboard catalog:
  - cluster overview
  - nodes
  - namespaces
  - workloads
  - pods
  - containers
  - deployments
  - statefulsets
  - daemonsets
  - ingress
  - services
  - persistent volumes
  - control plane
  - etcd when available
  - API server
  - scheduler
  - controller manager
  - network
  - service mesh
  - GitOps
  - backup
- [ ] Add resource-to-observability links:
  - pod to logs
  - pod to metrics
  - workload to rollout metrics
  - node to pressure metrics
  - service to request metrics where available
- [ ] Add project-scoped views:
  - namespace set filter
  - quota usage
  - alert summary
  - workload health
  - top pods by CPU/memory/restarts
- [ ] Add dashboard import/export:
  - JSON schema
  - versioned dashboard definitions
  - validation before import
  - RBAC around edit/publish
- [ ] Add dashboard health:
  - missing datasource warning
  - query failure warning
  - stale data warning
  - expensive query warning
- [ ] Add frontend performance guardrails:
  - query cancellation
  - pagination where needed
  - downsampling
  - loading skeletons
  - empty states

### Examples

Dashboard definition:

```json
{
  "apiVersion": "observability.astronomer.io/v1alpha1",
  "kind": "Dashboard",
  "metadata": {
    "name": "kubernetes-pods"
  },
  "spec": {
    "scope": "cluster",
    "panels": [
      {
        "title": "CPU Usage",
        "query": "sum(rate(container_cpu_usage_seconds_total{pod=~\"$pod\"}[5m])) by (pod)"
      }
    ]
  }
}
```

### Tests

- Query builder tests for label escaping and tenant filters.
- Dashboard schema validation tests.
- Frontend chart rendering tests.
- Playwright tests for dashboard navigation and resource drilldowns.
- Performance tests with large result sets.

### Validation

- Deploy monitoring stack and confirm every built-in dashboard has data or a clear missing-data reason.
- Verify project-scoped dashboards cannot escape namespace scope.
- Confirm dashboard queries are canceled when navigating away.
- Confirm logs/metrics links from pod detail work.

### Definition of Done

- Operators can debug common cluster, node, workload, and pod issues without leaving Astronomer.
- Dashboards are scoped by RBAC and project membership.
- Missing data is explained clearly.
- Large clusters do not freeze the browser.

## Workstream 8: Service Mesh Operations

### Reasoning

Rancher exposes operational workflows for Istio/service mesh. Astronomer currently trends closer to detection and installation. To be useful for day-2 management, service mesh needs traffic policy, mTLS, and routing workflows.

### Target Capabilities

- Detect Istio and supported mesh components.
- Install or link to existing mesh.
- Manage core Istio resources:
  - Gateway
  - VirtualService
  - DestinationRule
  - PeerAuthentication
  - AuthorizationPolicy
  - Sidecar
  - ServiceEntry
- Visualize traffic health.
- Configure canary, blue/green, retries, timeouts, circuit breaking, and mTLS.
- Validate policies before apply.

### Tasks

- [ ] Add mesh capability detection:
  - installed CRDs
  - control plane namespace
  - data plane injection labels
  - sidecar coverage
  - version
- [ ] Add mesh inventory pages *(partial: core Istio policy inventory exists; sidecar workload coverage remains)*:
  - gateways
  - virtual services
  - destination rules
  - authorization policies
  - workloads with sidecars
  - workloads missing sidecars
- [ ] Add traffic rule editor:
  - host selection
  - route weights
  - retries
  - timeout
  - fault injection if enabled
  - mirror traffic if enabled
- [ ] Add mTLS editor:
  - namespace mode
  - workload exceptions
  - strict/permissive/disabled
  - warning before disabling strict mode
- [ ] Add policy validation *(partial: schema-shape, kind, route-weight, mTLS, CRD availability, and GitOps ownership checks exist; host/service/gateway reference checks remain)*:
  - CRD schema validation
  - host/service existence checks
  - gateway reference checks
  - conflicting route warnings
- [ ] Add mesh observability:
  - request rate
  - error rate
  - latency
  - top services
  - mTLS status
  - sidecar injection coverage
- [ ] Add GitOps integration:
  - export mesh policy YAML
  - optionally commit to GitOps repo
  - mark Argo-owned mesh policies read-only unless user chooses GitOps edit flow

### Examples

Canary traffic policy:

```yaml
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: payments
  namespace: payments
spec:
  hosts:
    - payments.example.com
  http:
    - route:
        - destination:
            host: payments
            subset: stable
          weight: 90
        - destination:
            host: payments
            subset: canary
          weight: 10
      retries:
        attempts: 2
        perTryTimeout: 2s
      timeout: 10s
```

### Tests

- CRD detection tests.
- Validation tests for invalid host/gateway/subset references.
- Handler tests for RBAC and dry-run apply.
- UI tests for route weight editor totaling 100%.
- Integration tests against kind with Istio installed where feasible.

### Validation

- Detect existing Istio install.
- Create a VirtualService through dry-run and apply.
- Confirm read-only behavior for Argo-owned mesh policy.
- Validate traffic dashboard with Prometheus metrics where available.

### Definition of Done

- Operators can inspect mesh health and traffic policy in Astronomer.
- Common routing and mTLS changes are safe, validated, and auditable.
- Argo-owned mesh resources are not silently overwritten by UI mutations.

## Workstream 9: Controller and Reconciler Consistency

### Reasoning

Rancher's durability comes from controllers. Astronomer has moved several paths toward durable operations, task outbox, idempotency keys, and repair jobs. The remaining work is consistency: every important subsystem should use the same operation/reconciler lifecycle.

### Target Capabilities

- Mutating requests create durable intent.
- Workers/reconcilers perform external side effects.
- Operations are idempotent and retryable.
- Repair jobs detect drift.
- UI shows queue depth, latest failure, stale running operations, and retry actions.
- Handlers do not perform long-running cluster mutations directly.

### Tasks

- [ ] Inventory all mutating handlers:
  - cluster templates
  - tools
  - catalog apps
  - ArgoCD
  - logging
  - monitoring
  - workloads
  - backups
  - restore
  - service mesh
  - security scans
  - GitOps
  - cloud credentials
  - support bundles
- [ ] Classify each path:
  - already durable
  - partially durable
  - request-driven
  - missing idempotency
  - missing repair
  - missing operation visibility
- [ ] Standardize operation tables:
  - target scope
  - requested by
  - idempotency key
  - desired spec hash
  - status
  - attempts
  - last error
  - operation events
  - superseded by
- [ ] Extend shared operation runner:
  - claim
  - mark running
  - mark succeeded
  - mark failed
  - mark superseded
  - retry eligibility
  - stale-running repair
- [ ] Move remaining request-driven paths to durable operations.
- [ ] Add repair jobs:
  - cluster desired state drift
  - tool install drift
  - catalog app drift
  - backup schedule drift
  - GitOps ownership drift
  - service mesh policy drift
- [ ] Add admin operation dashboards:
  - per subsystem queue
  - stuck operations
  - retry
  - cancel/supersede where safe
  - latest reconcile result
- [ ] Add code guardrails:
  - tests or static checks for high-risk handlers that call external clients directly
  - route review checklist for new mutating routes

### Examples

Operation state lifecycle:

```text
queued -> running -> succeeded
queued -> running -> failed -> queued
queued -> superseded
running -> stale -> failed
```

Operation event:

```json
{
  "operation_id": "op_123",
  "phase": "apply",
  "message": "Applied Helm release monitoring/prometheus",
  "severity": "info",
  "at": "2026-06-13T12:00:00Z"
}
```

### Tests

- Shared operation runner table tests.
- Idempotency race tests.
- Worker retry tests.
- Repair job tests.
- Handler tests verifying mutating requests enqueue intent rather than performing side effects directly.
- UI tests for retry, stale, and failure states.

### Validation

- Kill Redis and confirm task outbox repairs delivery.
- Kill worker during an operation and confirm stale-running recovery.
- Repeat the same request with the same idempotency key and confirm one operation.
- Manually drift a managed resource and confirm repair detects it.

### Definition of Done

- All major mutating workflows are durable, idempotent, auditable, and visible.
- Operators can understand and repair failed automation from the UI.
- Direct request-time side effects are limited to trivial local DB changes or explicit exceptions.

## Workstream 10: Scale, HA, and Failure Validation

### Reasoning

The platform is only credible if it survives realistic failure modes. Rancher is used in large, long-lived environments. Astronomer must prove behavior under many clusters, agent reconnect storms, database pressure, Redis outages, Argo fan-out, and frontend high-cardinality data.

### Target Capabilities

- Repeatable scale test suite.
- HA management-plane validation.
- Agent reconnect storm testing.
- ArgoCD fan-out testing.
- Database and Redis failure drills.
- Browser performance budgets.
- Production readiness scorecard.

### Tasks

- [x] Define scale tiers:
  - small: 5 clusters, 500 resources
  - medium: 50 clusters, 25,000 resources
  - large: 250 clusters, 250,000 resources
  - extreme lab: 1,000 clusters or simulated agents
- [ ] Build simulated agent harness *(partial: tunnel, heartbeat, K8s response, profile cardinality, and reconnect simulation exist; watch-event and dropped-message simulation remain)*:
  - register many clusters
  - open tunnels
  - send heartbeats
  - send watch events
  - simulate disconnect/reconnect
  - simulate dropped messages
- [x] Add reconnect storm test:
  - all agents disconnect
  - all reconnect with jitter
  - verify server CPU/memory/tunnel ownership
  - verify no event bus collapse
- [ ] Add Argo fan-out test:
  - many clusters auto-adopted
  - ApplicationSet targets all clusters
  - drift one cluster
  - decommission one cluster
  - verify cleanup and health summary
- [ ] Add Postgres drills:
  - failover
  - connection pool exhaustion
  - long transaction
  - deadlock
  - PITR restore validation
  - mismatched Postgres/Kubernetes state drill
- [ ] Add Redis drills:
  - Redis down during enqueue
  - Redis returns after outage
  - task outbox drains
  - duplicate delivery avoided
- [ ] Add management-plane HA test:
  - multiple server pods
  - multiple worker pods
  - leader election
  - route traffic across pods
  - kill current leader
  - verify reconcilers resume
- [ ] Add frontend performance tests:
  - large cluster list
  - large resource list
  - dashboard with many series
  - live updates under load
  - memory leak scan after navigation loop
- [ ] Add production readiness scorecard:
  - security
  - HA
  - backup/restore
  - observability
  - agent fleet
  - GitOps
  - RBAC
  - scale
  - UI performance

### Examples

Scale tier config:

```yaml
name: medium
clusters: 50
agents:
  mode: simulated
resources:
  podsPerCluster: 250
  deploymentsPerCluster: 50
  servicesPerCluster: 75
eventsPerSecond: 500
duration: 30m
```

Readiness scorecard:

```json
{
  "tier": "medium",
  "result": "pass",
  "checks": {
    "agent_reconnect_storm": "pass",
    "task_outbox_redis_outage": "pass",
    "frontend_resource_list_p95_ms": 420,
    "api_error_rate": 0.001
  }
}
```

### Tests

- Load-test harness unit tests.
- Simulated agent protocol tests.
- HA integration tests in kind or k3d.
- Database failure drill tests where possible.
- Frontend performance regression tests.
- CI nightly scale profile for small tier.

### Validation

- Run small scale profile in CI.
- Run medium profile before release.
- Run large profile before declaring Rancher-grade day-2 readiness.
- Capture CPU, memory, DB pool, Redis queue, tunnel count, event lag, browser p95 render time, and API p95 latency.

### Definition of Done

- The small profile runs automatically in CI.
- The medium profile is repeatable from a documented command.
- Large-profile results are published before production claims.
- Failure drills have runbooks and pass/fail evidence.
- Performance budgets are documented and enforced.

## Cross-Cutting Requirements

### Security

- All new routes require explicit auth and RBAC.
- Mutating routes require CSRF protection for browser-cookie callers.
- API-token callers require appropriate write scopes.
- High-risk actions must be audited.
- Sensitive payloads must be redacted in diagnostics, logs, and support bundles.
- Browser session tokens must remain inaccessible to JavaScript.
- Service proxy and Kubernetes proxy must preserve header stripping and allowlists.

### UI Quality

- No feature should rely on hidden raw JSON when a domain-specific form is expected.
- Every destructive action requires clear target identity and confirmation.
- Empty, loading, forbidden, degraded, and failed states must be implemented.
- Long tables require search/filter/sort/pagination or virtualization.
- UI controls must reflect backend permissions but never rely only on frontend hiding.
- Dense operational layouts are preferred over marketing-style panels.

### API and Compatibility

- New APIs must be documented in `docs/openapi.yaml` or the current API docs path.
- New CRDs require schema, status conditions, printer columns, and versioning policy review.
- Breaking CRD changes require conversion planning.
- Every operation API should support idempotency keys where retry is likely.

### Observability

- Every reconciler exposes:
  - queue depth
  - running count
  - failed count
  - oldest queued age
  - last successful reconcile
  - last error
- Every critical worker path emits structured logs with request/operation ids.
- Prometheus metrics and alert rules must accompany new durable queues or long-running controllers.

### Documentation

- Add or update runbooks for:
  - agent upgrade failure
  - Argo adoption drift
  - extension install rollback
  - auth provider lockout
  - service mesh bad route
  - scale test failure
- Add admin-facing docs for:
  - RBAC role templates
  - agent privilege profiles
  - GitOps ownership states
  - extension permissions
  - dashboard import/export

## Suggested Sequencing

### Phase 1: Operator Trust

- Expanded RBAC role templates.
- Agent Fleet page and heartbeat/capability inventory.
- ArgoCD ownership migration and opt-out controls.
- Controller inventory for remaining request-driven paths.

Reasoning: these make the platform safer to delegate and operate before adding more surface area.

### Phase 2: Daily Workflow Depth

- Cluster Explorer resource detail drawers.
- YAML dry-run/diff/apply.
- Bulk actions.
- Resource-to-logs/metrics/events drilldowns.
- Observability dashboard expansion.

Reasoning: this is the core Rancher-like imported-cluster management experience.

### Phase 3: Advanced Operations

- Agent self-upgrade and diagnostics.
- Service mesh traffic policy UI.
- Extension/plugin model.
- More durable reconcilers and repair jobs.

Reasoning: these deepen the platform after the core workflow is reliable.

### Phase 4: Proof at Scale

- Simulated agent harness.
- Reconnect storm tests.
- HA management-plane tests.
- Argo fan-out tests.
- Medium and large scale reports.

Reasoning: do not claim Rancher-grade readiness without repeated evidence.

## Overall Definition of Done

This plan is complete when:

- Astronomer remains explicitly BYO/adopted-cluster only.
- A non-admin operator can manage day-2 workflows through delegated roles.
- Cluster Explorer supports live inspection, safe YAML edit/apply, common workload actions, node operations, logs, events, and metrics.
- Agent health, version skew, capabilities, diagnostics, and upgrade state are visible and actionable.
- ArgoCD ownership of baseline and app deployment is clear, migratable, repairable, and auditable.
- Extensions can add UI surface safely without forking the frontend.
- Enterprise auth and settings can be configured and validated from the UI.
- Observability dashboards cover common Kubernetes troubleshooting paths.
- Service mesh policy can be inspected and edited safely when mesh CRDs exist.
- Major mutation paths use durable operations/reconcilers rather than hidden request-time side effects.
- Small scale tests run in CI, medium scale tests are repeatable, and large scale results exist before production readiness claims.

## Immediate Next Tasks

- [ ] Review this plan against the current implementation and tag each task as `new`, `partial`, or `already implemented`.
- [ ] Create issues or implementation plan sections for Phase 1.
- [ ] Start with RBAC role-template expansion and Agent Fleet inventory because both improve safety before expanding power-user workflows.
- [ ] Add Cluster Explorer dry-run/diff/apply as the first major UI workflow.
- [ ] Add the simulated-agent harness before claiming medium or large scale readiness.
