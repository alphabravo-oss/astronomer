# Rancher-Quality Phase 0 Inventory

Date: 2026-06-13
Status: Phase 0 current-state inventory in progress
Parent plan: `docs/plans/2026-06-13-rancher-quality-argo-master-plan.md`

## Purpose

This document starts Phase 0 of the Rancher-quality master plan. It records the current route, proxy, UI, database, CRD, worker, security, duplicate-code, and test posture so later work can be executed against facts instead of memory.

This is not a completion claim for Phase 0. It is the first checked-in inventory snapshot and names the remaining gaps that must be closed before Phase 0 can be considered done.

Concrete file-level surface lists now live in `docs/rancher-quality-phase0-surface-inventory.md`. Reproducible duplicate/dead-code candidate findings live in `docs/rancher-quality-phase0-code-health-inventory.md`. Use those documents as the source lists for UI page audits, frontend API client audits, SQL query ownership, worker task contracts, and high-risk backend file reviews.

## Snapshot Counts

| Area | Current count | Evidence |
|---|---:|---|
| Dashboard `page.tsx` files | 93 | `find frontend/src/app/dashboard -type f -name 'page.tsx'` |
| DB up migrations | 104 | `find internal/db/migrations -name '*.up.sql'` |
| DB down migrations | 104 | `find internal/db/migrations -name '*.down.sql'` |
| SQL query files | 57 | `find internal/db/queries -name '*.sql'` |
| Worker task implementation files | 50 | `find internal/worker/tasks -name '*.go' ! -name '*_test.go'` |
| Worker task test files | 32 | `find internal/worker/tasks -name '*_test.go'` |
| Chart CRD templates | 7 | `deploy/chart/templates/crd-*.yaml` |
| Management CRD Go kinds | 6 | `internal/crd/types.go` registers `Cluster`, `Project`, `ClusterBaseline`, `ComponentBundle`, `AgentProfile`, and `GitOpsTarget` |
| Generated mounted route inventory entries | 405 | `docs/generated-route-inventory.json`, produced by `TestRouteInventoryCanBeGenerated`; every row includes handler owner, surface, auth posture, RBAC posture, CSRF posture, audit posture, and representative test evidence; high-risk registry metadata enriches 262 mounted route rows and classification metadata enriches 3 auth-flow rows |

## Route Inventory

### Public Routes

| Route | Purpose | Expected unauthenticated access | Notes |
|---|---|---:|---|
| `/health`, `/health/` | basic process health | yes | no dependency checks |
| `/readyz`, `/readyz/` | dependency readiness | yes | mounted when `Readyz` handler is wired |
| `/helm-repo/astronomer/index.yaml` | embedded chart index | yes | mounted when platform chart repo handler is wired |
| `/helm-repo/astronomer/<archive>` | embedded chart archive | yes | mounted when platform chart repo handler is wired |
| `/helm-repo/astronomer-v2/index.yaml` | v2 chart index alias | yes | mounted when platform chart repo handler is wired |
| `/helm-repo/astronomer-v2/<archive>` | v2 chart archive alias | yes | mounted when platform chart repo handler is wired |
| `/api/v1/openapi.yaml` | OpenAPI spec | yes | mounted when docs handler is wired |
| `/api/v1/docs`, `/api/v1/docs/` | Swagger UI | yes | mounted when docs handler is wired |
| `/api/v1/auth/login/` | local login | yes | rate-limited |
| `/api/v1/auth/refresh/` | refresh session | yes, token/cookie-gated inside handler | refresh cookie requires CSRF |
| `/api/v1/auth/logout/` | logout | yes, token/cookie-gated inside handler | invalidates session when present |
| `/api/v1/auth/logout-done`, `/api/v1/auth/logout-done/` | SLO landing endpoint | yes | IdP callback after session teardown |
| `/api/v1/auth/password-reset/request/` | request reset email | yes | rate-limited |
| `/api/v1/auth/password-reset/complete/` | complete reset | yes, reset-token gated | rate-limited |
| `/api/v1/auth/totp/verify/` | TOTP challenge verify | yes, challenge-token gated | rate-limited |
| `/api/v1/auth/login/{provider}` | SSO login start | yes | state validation handled by SSO flow |
| `/api/v1/auth/callback/{provider}` | SSO callback | yes | validates state and provider response |
| `/api/v1/settings/sso/presets/` | SSO preset catalog | yes | safe pre-auth catalog data |
| `/api/v1/settings/branding/` | login branding | yes | handler allowlists namespace |
| `/api/v1/settings/banner/` | login banner | yes | handler allowlists namespace |
| `/api/v1/settings/registration/` | registration setting subset | yes | handler allowlists namespace |
| `/api/v1/register/{token}` | one-line agent manifest | yes, token in URL is credential | sensitive, should stay route-tested |
| `/api/v1/register/ca.crt` | CA bundle for manifest install | yes | returns 404 when unset |
| `/api/v1/ws/agent/tunnel/{cluster_id}/` | legacy agent tunnel | yes, agent-token gated by tunnel handler | long-lived WS |
| `/api/v1/connect/{cluster_id}/` | remotedialer agent tunnel | yes, bearer token gated by remotedialer handler | long-lived WS |

### Authenticated Route Groups

Most application routes are mounted under `registerProtectedRoutes`, which is reached through the authenticated subrouter when `JWT` is configured. The route tree includes these major groups:

| Group | Representative routes | Primary authorization model |
|---|---|---|
| clusters | `/clusters`, `/clusters/{id}/`, `/clusters/{id}/register/` | `ResourceClusters` with list/read/create/update/delete |
| registration wizard | `/clusters/{id}/registration/status/`, `/confirm/`, `/retry/` | `ResourceClusters` read/update |
| monitoring | `/monitoring/*`, `/clusters/{id}/monitoring/*` | `ResourceMonitoring` |
| Argo CD | `/argocd/*`, cluster registration and app surfaces | Argo handler RBAC plus JWT/cookie proxy auth |
| projects | `/projects/*` | `ResourceProjects` |
| RBAC | `/rbac/*` | `ResourceRBAC` and write scopes |
| backups/snapshots | `/clusters/{id}/snapshots/*`, backup routes | `ResourceClusters` plus backup handler gates |
| cluster templates | `/cluster-templates/*`, `/clusters/{id}/template/*` | `ResourceClusterTemplates` and `ResourceClusters` |
| network policies | `/admin/network-policy-templates/*`, per-cluster applications | `ResourceNetworkPolicies` and `ResourceClusters` |
| service mesh | `/clusters/{id}/service-mesh/*` | `ResourceClusters` and `ResourceServiceMesh` |
| security | `/security/*`, `/clusters/{id}/security/*`, vulnerabilities | `ResourceSecurity` and cluster read |
| workloads | `/clusters/{id}/workloads/*`, pods/logs/nodes/events | `ResourceWorkloads`, `ResourcePods`, `ResourceClusters` |
| resources | `/clusters/{id}/resources/*`, `/resources/{id}/*` | currently protected by authenticated subrouter; several per-resource routes need explicit RBAC audit |
| service proxy | `/clusters/{id}/proxy/service/*` | auth plus cluster read/update and allowlist |
| admin/settings | `/admin/*`, `/settings/features/` | auth, superuser checks in handlers, and write scopes where configured |
| agents | `/agents/fleet/*` | `agents:read` and lifecycle permissions where implemented |
| extensions | `/extensions/*` | `ResourceSettings` plus admin scope for writes |

### Security-Sensitive Registry

The high-risk route registry now lives in:

```text
docs/security-sensitive-routes.json
```

The generated mounted route inventory and mutating-route classification rules now live in:

```text
docs/generated-route-inventory.json
docs/route-risk-classifications.json
```

The server route security test reads the high-risk registry and verifies every listed sample route is registered and returns its expected unauthenticated status. Most user-facing routes must return `401`; server-internal PSK routes return `403` on missing or wrong PSK. The mutating-route classification test walks the real router, normalizes registered patterns, and fails if any mutating route is neither covered by the high-risk registry nor explicitly classified in `docs/route-risk-classifications.json`.

Current registry entries: 209. The JSON file is the authoritative per-route source. It now covers the long-lived and proxy surfaces, identity/account mutators, credential-bearing configuration, resource/node actions, cluster lifecycle and registration, RBAC, backup/restore, catalog, cluster group, legacy Fleet-named operation aliases, network policy, workload, service mesh, and secondary alias routes discovered by the generated inventory.

Current route-risk classification rules: 1. The only remaining mutating classification is the intentional public auth session flow (`/auth/login`, `/auth/refresh`, `/auth/logout`), which is token/cookie/rate-limit gated by auth-specific handlers and tests rather than the generic high-risk route registry.

Routes that should be reviewed in the next pass:

- non-registry browser-cookie mutating routes, if any are added later, that need explicit CSRF test mapping
- high-risk read routes that need explicit audit decisions, especially privileged previews; user-facing Secret reads now have explicit `cluster.secret.read` audit coverage
- compatibility aliases that should be deprecated or renamed after the UI and API clients stop using them
- legacy Fleet-named operation routes that should be renamed or explicitly documented as compatibility aliases for the Argo-based model

## Proxy Inventory

| Surface | Code owner | Direction | Auth model | RBAC model | Header/target controls | Audit posture | Current tests | Gaps |
|---|---|---|---|---|---|---|---|---|
| Generic Kubernetes proxy | `internal/tunnel/proxy.go`, `internal/agent/k8sproxy.go` | browser/API -> server -> agent -> target K8s API | JWT/session/API token | read/update cluster; pod exec subresources require `pods:exec`; Secret reads require `secrets:read/list/watch` | server strips client-only request headers and unsafe one-shot/watch/cross-pod fallback response headers; agent strips caller auth and injects SA bearer | mutating methods emit `cluster.k8s_proxy.forwarded`; authorized Secret reads emit `cluster.secret.read` | `routes_security_test.go`, `internal/tunnel/proxy_test.go`, `internal/agent/k8sproxy_test.go` | none for current generic proxy slice |
| Argo CD internal K8s proxy | `internal/server/routes.go`, `internal/tunnel/proxy.go`, `internal/agent/k8sproxy.go` | Argo CD -> server -> agent -> target K8s API | cluster-scoped hash-validated Argo proxy token | token must match cluster | same K8s proxy strip/inject path | explicit route audit still missing | `TestArgoCDInternalK8sProxyRequiresClusterScopedToken` | add explicit audit or Argo-specific correlation event |
| Service proxy | `internal/handler/service_proxy.go`, `internal/agent/service_proxy.go` | browser/API -> server -> agent -> cluster service DNS | JWT/session/API token | cluster read/update plus API token write scope for mutations | namespace/name/port validation; sensitive namespace block; tool allowlist; server only forwards content type and accept; agent strips dangerous request headers defensively; server strips unsafe response headers before returning to the browser | mutating methods emit `cluster.service_proxy.forwarded` | `service_proxy_test.go`, `routes_security_test.go`, `internal/agent/service_proxy_test.go` | continue response-header review for Argo UI proxy surface |
| Argo CD UI proxy | `internal/handler/argocd_ui_proxy.go` | browser -> server -> in-cluster Argo CD UI/API | JWT bearer or `astronomer_session` cookie | currently front-door auth; handler token injection | proxy strips Astronomer bearer/session credentials before upstream; rewrites root path; strips unsafe response headers; only returns `argocd.token` cookies scoped host-only to `/argocd`, `HttpOnly`, `SameSite=Lax`, and HTTPS `Secure` | audits document opens and mutating proxied API calls | handler tests and registry-backed unauth route test | add explicit Argo read/API correlation metrics if needed |
| Internal K8s cross-pod fallback | `internal/tunnel/internal_k8s.go` | server pod -> sibling server pod | PSK | server-internal only | intended sibling-only path | not user-audited | registry-backed route test and `internal/tunnel/internal_k8s_test.go` | add internal correlation metrics |
| Internal Helm cross-pod fallback | `internal/tunnel/internal_helm.go` | server pod -> sibling server pod | PSK | server-internal only | intended sibling-only path | operation audit is owned by calling workflow | registry-backed route test and `internal/tunnel/internal_helm_test.go` | add internal correlation metrics |
| Exec WebSocket | `internal/tunnel/exec_consumer.go` | browser/API -> server -> agent -> pod exec | JWT/API token or stream ticket depending path | stream auth helper; Kubernetes path guarded separately | WebSocket only; no query JWT for preferred browser path | should be audited as shell/exec | registry-backed unauth route test | add direct exec audit or keep access through shell/session path |
| Logs WebSocket | `internal/tunnel/logs_consumer.go` | browser/API -> server -> agent -> pod logs | JWT/API token or stream ticket depending path | logs permission should be explicit | WebSocket only | should be audited as logs read when policy requires | registry-backed unauth route test | add log access audit coverage |
| Kubectl shell WebSocket | `internal/handler/kubectl_shell.go`, `internal/tunnel/exec_consumer.go` | browser -> server -> agent -> pod exec | session row plus stream authorization | shell session ownership and cluster access | session-aware route | shell open and commands are audited | registry-backed unauth route test and `kubectl_shell_test.go` | add explicit shell close/end audit review |
| Remotedialer | `internal/tunnel2/server.go`, `internal/handler/remoteproxy` | agent -> server and client-go proxy path | bearer token | cluster registration token model | token extracted from Authorization header | no user audit; agent connection events tracked elsewhere | `internal/tunnel2/server_test.go` | production demo route disabled; remotedialer surface should be added to full proxy inventory |
| Agent service proxy local outbound | `internal/agent/service_proxy.go` | agent -> in-cluster service DNS | trusted tunnel message | server authorizes before message | URL built from namespace/service/port/path payload; agent strips auth, cookie, host, proxy, forwarded, impersonation, and hop-by-hop request headers; browser-facing response headers are sanitized by the server handler | server-side audit | `internal/agent/service_proxy_test.go`, `internal/handler/service_proxy_test.go` | none for current service proxy slice |

## UI Inventory

The dashboard currently has 92 page files. Major information architecture is already present:

| Area | Current route examples | Current posture |
|---|---|---|
| Dashboard home | `/dashboard` | present |
| Clusters | `/dashboard/clusters`, `/dashboard/clusters/[id]` | present; includes resource, workload, tools, apps, nodes, shell, service mesh, snapshots, registries, network pages |
| Cluster registration | `/dashboard/clusters/register`, `/register/[id]/connect`, `/progress` | present |
| Agents | `/dashboard/agents` | present |
| Argo CD | `/dashboard/argocd`, instance/app/ApplicationSet pages | present |
| Projects | `/dashboard/projects`, project detail and policy/quota/catalog/cloud-credential pages | present |
| RBAC | `/dashboard/rbac` | present |
| Monitoring | `/dashboard/monitoring` | present |
| Logging | `/dashboard/logging` | present |
| Alerting | `/dashboard/alerting` | present |
| Security | `/dashboard/security`, scan pages | present |
| Backups | `/dashboard/backups`, run/restore/storage/schedule pages | present |
| Catalog/tools/extensions | `/dashboard/catalog`, `/tools`, `/extensions` | present |
| Settings/admin | auth connectors, operations, quotas, webhooks, SMTP, templates, vault, compliance, GitOps, widgets | broad coverage present |
| Search | `/dashboard/search` | present |

UI gaps to close:

- `docs/rancher-quality-phase0-surface-inventory.md` now lists every dashboard `page.tsx`. The next pass must turn that list into a full UI quality matrix with API clients, RBAC gates, loading/error/empty states, viewport/accessibility evidence, and e2e coverage.
- Resource pages still need a single shared table/detail/action framework.
- Several cluster pages likely contain duplicate data-fetching, action, status-badge, and table patterns.
- The intentionally excluded provisioning surface has been reframed as `/dashboard/clusters/[id]/adoption`. The historical `/dashboard/clusters/[id]/provisioning` redirect alias has been removed so the route map no longer implies infrastructure creation.
- Page-level viewport and accessibility evidence is not yet attached to this inventory.

## Database Inventory

Current database posture:

- 101 up migrations and 101 down migrations are present.
- 57 sqlc query files are present.
- Secret-sensitive columns are tracked separately in `docs/secret-column-inventory.md`.
- Recent migrations include:
  - Argo CD cluster proxy tokens
  - registration token hash-only migration
  - cluster agent token hash migration
  - task outbox
  - operation idempotency keys
  - Rancher-grade role catalog
  - Argo baseline ownership decisions
  - UI extensions
  - agent lifecycle operations

Database gaps to close:

- Use `docs/rancher-quality-phase0-surface-inventory.md` as the current SQL query file source list.
- Attach migration provenance to every table.
- Confirm every sensitive column appears in `docs/secret-column-inventory.md`.
- Confirm every durable operation table has:
  - status enum or equivalent constraint
  - idempotency key where applicable
  - retry/dead-letter semantics
  - audit correlation id
  - index coverage for list/detail UI queries
- Confirm every foreign key has an intentional cascade/restrict behavior.
- Confirm retention policy for audit, events, logs, tokens, sessions, and operation history.

## CRD Inventory

Current CRD posture:

| CRD | Template | Go type | Controller | Status |
|---|---|---|---|---|
| `Cluster.management.astronomer.io/v1alpha1` | `deploy/chart/templates/crd-cluster.yaml` | `internal/crd/types.go` | `internal/crd/controller.go` | present |
| `Project.management.astronomer.io/v1alpha1` | `deploy/chart/templates/crd-project.yaml` | `internal/crd/types.go` | `internal/crd/controller.go` | present |
| CRD RBAC | `deploy/chart/templates/crd-rbac.yaml` | n/a | server pod permissions | present |

Related CRD-mirror posture:

- `internal/crd/ingest_v2.go` mirrors IngressClass, GatewayClass, NetworkPolicy, ResourceQuota, and LimitRange into Postgres summaries.
- `internal/crd/trivy_watcher.go` routes Trivy VulnerabilityReport events into the scanner ingest path.

CRD gaps to close:

- Add planned CRDs from the master plan:
  - `ClusterBaseline`
  - `ComponentBundle`
  - `AgentProfile`
  - `GitOpsTarget`
  - later `AccessPolicy`, `PolicySet`, `BackupPlan`, `ObservabilityProfile`, and `ClusterHealthProfile`
- Add conversion strategy before any API reaches `v1beta1`.
- Add envtest-style controller coverage where practical.
- Add restore/reconcile tests for CRD plus Postgres recovery.

## Worker and Automation Inventory

Current posture:

- 50 task implementation files exist under `internal/worker/tasks`.
- 32 task test files exist under `internal/worker/tasks`.
- `internal/worker/worker.go` registers core task constants and mux handlers.
- `internal/worker/scheduler.go` registers periodic work including health checks, alert evaluation, catalog sync, metrics aggregation, monitoring reconciliation, token cleanup, audit maintenance, scheduled backups, backup retention, project reconciliation, cluster decommission, task outbox dispatch, and template drift checks.
- Argo-related automation exists:
  - `argocd_auto_register_cluster.go`
  - `argocd_refresh_managed_cluster.go`
  - `gitops_sync.go`
- Durable operation models exist in several areas, but not uniformly across every high-risk action.

Worker gaps to close:

- Use `docs/rancher-quality-phase0-surface-inventory.md` as the current worker task source list.
- Produce a task-by-task inventory with:
  - enqueue points
  - idempotency key
  - queue
  - retry policy
  - dead-letter handling
  - metrics
  - audit events
- Confirm every cluster-affecting task is idempotent.
- Confirm periodic Argo cluster Secret reconciliation exists and is not only update-triggered.
- Confirm task outbox coverage for every operation that must survive Redis downtime.

## Security Posture

Already present:

- Email-only login changes are in the current worktree.
- Browser session cookies and CSRF middleware exist.
- API token scope middleware exists.
- High-risk proxy route tests exist.
- K8s proxy mutating requests are audited.
- Service proxy mutating requests are audited.
- Argo cluster proxy tokens are hash-validated and cluster-scoped.
- Registration token hash-only migration exists in the current worktree.
- `TestRouteInventoryCanBeGenerated` uses `chi.Walk` to generate a full mounted route inventory and enriches rows from route-family metadata, `docs/security-sensitive-routes.json`, and `docs/route-risk-classifications.json`. Run `ASTRONOMER_WRITE_ROUTE_INVENTORY=1 go test ./internal/server -run TestRouteInventoryCanBeGenerated -count=1` to write `docs/generated-route-inventory.json`.
- `docs/generated-route-inventory.json` is checked in with 405 mounted route entries from the current router. Every row includes handler owner, surface, auth posture, RBAC posture, CSRF posture, audit posture, and representative test evidence.
- High-risk registry metadata enriches 262 mounted rows, classification metadata enriches 3 auth-flow rows, and `TestRouteInventoryEntriesHaveCompleteSecurityPosture` fails if any row loses required posture metadata.
- `docs/security-sensitive-routes.json` has 209 entries and covers all generated mutating routes except the intentional public auth session flow.
- `docs/route-risk-classifications.json` has one rule for that public auth session flow.
- `TestMutatingRoutesHaveSecurityClassification` fails when a new mutating route is mounted without a high-risk registry entry or an explicit classification rule.
- `TestBrowserCookieMutatingRoutesRequireCSRF` fails when a registry-marked browser-cookie mutating route accepts a session cookie without the double-submit CSRF token.
- `.github/workflows/pr-validation.yaml` validates the checked-in route metadata JSON and runs the focused route security/inventory gate on every pull request to `main`.
- `.github/workflows/pr-validation.yaml` runs `npm run code-health`, which verifies `docs/rancher-quality-phase0-code-health-inventory.md` is current and fails on duplicate frontend dependency declarations or direct frontend HTTP calls outside the API layer.
- `TestUserManagementRoutesRequireUsersRBACAndAdminScope` proves user CRUD is gated by `users:*` RBAC and admin API-token scope.
- `TestLegacySettingsMutatorsRequireRBACAndAdminScope` proves legacy settings/SSO mutators require explicit RBAC and admin API-token scope.
- Agent-side Kubernetes proxy strips caller Authorization/Cookie/Host/X-Forwarded/Impersonate headers before injecting the service account token.
- Server-side Kubernetes proxy strips unsafe response headers from one-shot, watch-stream, and cross-pod fallback responses before returning them to browsers.
- Agent-side service proxy strips auth, cookie, host, proxy, forwarded, impersonation, and hop-by-hop headers before forwarding to in-cluster services.
- NetworkPolicy chart templates exist.
- SBOM/sign/attest release workflow exists.

Security gaps to close:

- Convert compatibility aliases into documented deprecation targets once UI/API clients no longer call them.
- Use `docs/rancher-quality-phase0-surface-inventory.md` as the current high-risk handler/tunnel/agent file review seed.
- Add response-header sanitization review to any future proxy surfaces.
- Extend CSRF route-map coverage if non-registry browser-cookie mutators are intentionally added later.
- Keep every new Secret read surface behind `secrets:*` RBAC and a distinct audit event.
- Add govulncheck and npm audit as required CI gates if not already enforced.

## Duplicate-Code and Dead-Code Initial Findings

Areas that need focused cleanup:

| Area | Why it matters | Initial target |
|---|---|---|
| Frontend API clients | duplicate request shapes hide auth/session bugs | keep page-local API error parsers consolidated under `frontend/src/lib/api/errors.ts`; review any new candidates generated in `docs/rancher-quality-phase0-code-health-inventory.md` |
| React Query keys | inconsistent invalidation creates stale UI | consolidate page-local keys into shared `queryKeys` or feature hook modules; current candidates are generated in `docs/rancher-quality-phase0-code-health-inventory.md` |
| Cluster resource tables | Explorer quality requires one table/action framework | consolidate components under `frontend/src/components/resources` |
| Status badges | inconsistent health/sync wording creates operator confusion | shared status component set |
| Kubernetes object helpers | string parsing appears in server routes and handlers | shared internal Kubernetes helper package |
| Audit event builders | high-risk action details should be consistent | shared audit builder helpers |
| Redaction logic | diagnostics and support bundles must redact identically | shared redaction package |
| Operation state strings | typo-prone state machines are hard to test | typed constants and transition helpers |
| Unused sqlc queries | dead query methods increase generated-surface noise | investigate generated candidates from `docs/rancher-quality-phase0-code-health-inventory.md` before removal |
| Potentially unused components | stale components hide UI drift | verify relative/dynamic imports before removing generated candidates |
| Legacy Python aliases | compatibility routes may keep dead behavior alive | inventory and removal/deprecation plan |
| Legacy provisioning UI route | product scope excludes provisioning | removed; `/dashboard/clusters/[id]/adoption` is the canonical imported-cluster timeline |

No code should be removed from this list without proving it is unused through references, tests, and runtime behavior.

## Test Inventory

Relevant current tests:

| Area | Test files |
|---|---|
| Route security | `internal/server/routes_security_test.go` |
| Browser auth/CSRF | `internal/server/middleware/auth_browser_test.go`, `internal/handler/auth_test.go` |
| Generic tunnel proxy | `internal/tunnel/proxy_test.go` |
| Agent Kubernetes proxy | `internal/agent/k8sproxy_test.go` |
| Service proxy | `internal/handler/service_proxy_test.go` |
| Remotedialer | `internal/tunnel2/server_test.go` |
| Kubectl shell | `internal/handler/kubectl_shell_test.go` |
| CRD controller and mirror | `internal/crd/*_test.go` |
| Worker tasks | `internal/worker/tasks/*_test.go` |
| Migration safety | `internal/db/migrations/*_test.go` |
| Frontend unit tests | `frontend/src/**/__tests__` |
| Frontend e2e | `frontend/tests/e2e` |

Required validation commands for this inventory slice:

```sh
go test ./internal/server ./internal/tunnel ./internal/agent ./internal/handler -run 'TestHighRiskRoutesDenyUnauthenticatedRequests|TestMutatingRoutesHaveSecurityClassification|TestRouteInventoryCanBeGenerated|TestK8sProxy|TestServiceProxy|TestArgoCDInternalK8sProxy|TestHandleK8sProxy|TestK8sProxyExecuteUpstreamStripsClientAuthHeaders'
ASTRONOMER_WRITE_ROUTE_INVENTORY=1 go test ./internal/server -run TestRouteInventoryCanBeGenerated -count=1
node scripts/code-health-inventory.mjs --check --verify-doc
git diff --check
```

## Phase 0 Remaining Work

Phase 0 is not done until these items are complete:

- [x] Add a generator for a full mounted route inventory.
- [x] Enrich every mounted route inventory row with handler owner, auth, RBAC, CSRF, audit, and representative-test posture.
- [x] Compare the generated route inventory against the high-risk registry and classify every remaining mutator.
- [x] Promote every `candidate-high-risk`, secondary, and alias mutating route to `docs/security-sensitive-routes.json` or document why it is intentionally public.
- [x] Add tests proving every current registry entry is registered and denies unauthenticated requests.
- [ ] Add tests proving every proxy/tunnel path strips or rejects dangerous headers.
- [ ] Add a UI page inventory mapping pages to API clients, RBAC gates, states, and e2e tests.
- [ ] Add a database table owner inventory.
- [ ] Add a worker task inventory.
- [x] Add a CRD desired-state roadmap document or update `docs/crd-api.md`.
- [x] Create a duplicate-code cleanup issue list with owners.
- [x] Create a dead-code removal list with evidence.
