# System Hardening and Remediation Plan

Date: 2026-06-12
Status: In progress; P0 route/auth hardening, ArgoCD proxy service identity, baseline ApplicationSets, service proxy allowlisting, stream tickets, and HttpOnly browser sessions implemented
Scope: Astronomer Go backend, agent tunnel, Next.js frontend, Helm/Kubernetes deployment assets, and docs/process.

## Review Summary

Astronomer is a high-privilege Kubernetes management plane. Its largest risk is not a single feature; it is that several routes and helper patterns can accidentally expose cluster-admin-like capabilities when auth/RBAC wiring is missing or mounted in the wrong place.

This plan prioritizes the fixes that reduce blast radius first, then UI consistency and maintainability work.

Implemented on 2026-06-12:

- Public Kubernetes passthrough now requires auth, cluster RBAC, API-token write scope for mutations, and `pods:exec` for exec/attach/port-forward subresources; tunnel forwarding tests cover `GET pods`, `PATCH deployment`, `DELETE pod`, and `WATCH pods`.
- Registration SSE and the remotedialer pod demo route now require auth and cluster read permission.
- Production boot now fails closed on missing/dev JWT secret, encryption key, DB TLS, JWT manager, auth queries, RBAC engine/queries, or encryptor.
- K8s proxy strips inbound `Authorization`, cookies, forwarding headers, and all `Impersonate-*` headers before forwarding to adopted clusters.
- CRD controller supports leader election and server wiring enables it.
- ArgoCD auto-adoption has a dedicated cluster-scoped service token and internal proxy route.
- Built-in ArgoCD ApplicationSets now reconcile the platform baseline on adopted clusters when `argocd.manage_platform_baseline=true`.
- ArgoCD auto-adoption now has an `argocd.auto_adopt_clusters` rollout setting, default `true`, which the worker honors before any registration work.
- Legacy cluster-template apply now skips ArgoCD-owned baseline tool installs on adopted clusters when the ArgoCD baseline setting is enabled, avoiding duplicate Helm-vs-Argo ownership.
- Service proxy access now requires cluster RBAC, API-token write scope for mutating methods, valid Kubernetes service targets, and an enabled-tool allowlist before forwarding to adopted clusters.
- Service proxy allowlisting now honors `cluster_tools.presets.service_proxy_allowed=false` and per-subservice `service_proxy_allowed=false`, so enabled tools can still opt out of browser proxy exposure.
- API and proxied UI responses now include browser security headers: CSP, `X-Frame-Options`, `X-Content-Type-Options`, `Referrer-Policy`, and HTTPS-gated HSTS.
- Mutating generic Kubernetes passthrough requests now emit best-effort audit rows after auth/RBAC/scope checks, including parsed namespace/resource/name for common Kubernetes API paths.
- Non-read service proxy requests now emit best-effort audit rows with cluster, namespace, service, port, path suffix, and HTTP method.
- Service proxy route tests now cover API-token write-scope denial and allow behavior.
- When `CRD_ENABLED=true`, CRD controller bootstrap failures now fail startup in production while preserving warn-and-disable behavior for development.
- The remotedialer `/v2/pods/` demo route is now disabled entirely in production.
- Production runtime validation now rejects missing or non-HTTPS `server_url`.
- Production runtime validation now rejects Dex-disabled installs unless local-password-only auth is explicitly acknowledged, matching the Helm render-time guard.
- Helm chart workloads for frontend, server, worker, Dex, bundled Postgres, and bundled Redis now render restricted pod/container security contexts by default. Postgres and Redis keep writable root filesystems because their upstream images need runtime/data writes.
- The Helm chart now renders management-plane NetworkPolicies by default for frontend, server, worker, Dex, bundled Postgres, and bundled Redis, with configurable public ingress, metrics ingress, and external egress CIDRs.
- Added `docs/control-plane-state-contract.md` documenting Postgres, CRD, ArgoCD, Kubernetes, and cache ownership boundaries.
- Added Postgres ownership metadata for `clusters` and `projects`: `managed_by`, `external_ref_*`, `observed_generation`, validation constraints, uniqueness for external refs, and typed query helpers.
- The CRD controller now stamps those ownership fields after successful `Cluster` and `Project` CR reconciliation.
- The CRD controller now refuses to reconcile a same-name `Cluster` or `Project` CR over an existing UI/API-owned row, preventing implicit ownership takeover.
- REST cluster/project mutation handlers now reject CRD-owned rows with `409 ownership_conflict` instead of silently overwriting CRD-owned fields.
- Added `docs/secret-column-inventory.md` and a migration guard test requiring every new secret-like column to be explicitly classified.
- The `Cluster` CRD now exposes ArgoCD adoption intent and status fields: `spec.argocd`, `spec.baseline.profile`, `spec.agent.privilegeProfile`, `status.argocd.phase`, `status.argocd.clusterSecretName`, and standard adoption conditions.
- `Cluster` and `Project` CRD condition schemas now use standard Kubernetes condition fields, and CRD printer columns include the owning DB row IDs.
- The adopted-cluster agent install manifest now supports `viewer`, `operator`, and `admin` RBAC profiles; `Cluster.spec.agent.privilegeProfile` is persisted as a reserved annotation and used when rendering the manifest.
- Cluster API responses now expose the normalized agent privilege profile, the cluster overview warns when a cluster uses full-admin agent access, and `docs/agent-privilege-profiles.md` documents profile capabilities and limits.
- Helm values and chart docs now define the production Postgres contract: external managed/HA Postgres, TLS, PITR, logical backups, restore drills, retention minimums, and pool-sizing guidance.
- The management-plane DR runbook now documents restore-order rules for mismatched Postgres and Kubernetes/etcd snapshots.
- Added `docs/migration-safety.md` with expand/migrate/contract rollout rules, online backfill requirements, rollback/forward-fix guidance, and validation commands.
- Added explicit cluster/project ownership takeover endpoints so operators can intentionally transfer CRD-owned rows back to API ownership instead of silently overwriting or being permanently blocked.
- Registration SSE route tests now cover invalid bearer tokens, authenticated callers without cluster access, and authorized cluster readers.
- Handler-level authorization support now fails closed for authenticated requests when RBAC wiring is missing, while preserving direct unauthenticated unit-test passthrough.
- Added `.github/pull_request_template.md` with explicit route, secret, proxy, external-client, Kubernetes-RBAC, browser-session, and ownership review checks.
- Added `docs/threat-model.md` covering browser sessions, the agent tunnel, ArgoCD cluster proxy, service proxy, and in-browser kubectl shell.
- Added a chi route-table guard for high-risk routes that verifies registration-event SSE, remotedialer pods, generic k8s proxy, ArgoCD k8s proxy, and service proxy routes all reject unauthenticated callers.
- Added an admin route registration guard to CI so `/admin/*` routes must either declare route-local auth or live inside the authenticated protected-route registrar.
- Added `.github/workflows/pr-validation.yaml` with backend tests, route-security tests, frontend typecheck/unit/audit, Helm dev/prod renders, Trivy image scanning, and syft SBOM artifacts.
- Removed the last stale Django/Python references from current frontend API comments and kept the CI workflow at least-privilege repository permissions.
- Corrected the DB pool exhaustion PrometheusRule and runbook examples to use the emitted `astronomer_db_*` metric names.
- Added Postgres runtime contention metrics and alerting for deadlock deltas and longest open transaction age, with an operator runbook.
- Added a live ArgoCD auto-adoption validation script and Make target for managed-cluster registration, baseline ApplicationSet fan-out, and label refresh.
- Added one-use stream tickets at `POST /api/v1/streams/tickets/` and migrated browser EventSource/WebSocket clients to pass `?ticket=` instead of long-lived JWTs in stream URLs.
- Browser login, refresh, TOTP verify, SSO callback, and logout now issue/clear HttpOnly `astronomer_session` and `astronomer_refresh` cookies from the Go server.
- Standard API auth middleware now accepts the HttpOnly browser session cookie when no `Authorization` header is present, while preserving bearer JWT/API-token support for headless callers.
- The frontend no longer persists session JWTs or refresh tokens in `localStorage`, removes legacy token keys on API startup/logout, and gates `/dashboard/*` routes in Next middleware when no session cookie is present.
- The dashboard layout now refreshes `/auth/me` once for current role data, exposes a shared client-side `can(resource, verb, scope)` helper, and hides sidebar navigation entries that the current user cannot read.
- The frontend now reads authenticated `feature.*` flags from `/api/v1/settings/features/`, hides disabled feature navigation after flags load, and shows a disabled state for direct links to disabled sections.
- Added a shared frontend `EmptyState` component and migrated the settings admin gate, feature-disabled dashboard view, and backups overview empty states to it so high-visibility forbidden/disabled/no-data views render consistently.
- Cookie-authenticated unsafe requests now require a double-submit CSRF token: the Go server issues a readable `astronomer_csrf` cookie, the frontend sends it as `X-CSRF-Token`, and bearer/API-token callers remain exempt.
- Added a durable Postgres `task_outbox` table, a leader-elected dispatcher, worker/server wiring, and a producer helper so critical state changes can commit task intent transactionally and retry Redis/Asynq delivery after outages.
- The cluster-registration confirm/retry path now writes the platform-baseline `cluster_template:apply` task through `task_outbox` before Redis delivery, with the prior direct Asynq enqueue kept as a compatibility fallback when the outbox writer is not wired.
- The agent-connect ArgoCD auto-adoption fast path now writes `argocd:auto_register_cluster` through `task_outbox` before Redis delivery, while retaining the 5-minute sweep as a repair path.
- Cluster DELETE/decommission now writes `cluster:decommission` through `task_outbox` before Redis delivery, with dedupe by decommission row ID and the periodic decommission sweep retained as a repair path. The REST and CRD finalizer paths now use an atomic SQL helper that creates the decommission row and durable task-outbox row together.
- GitOps `on_delete=decommission` and tombstone reaping now write `cluster:decommission` through `task_outbox` before Redis delivery.
- Cloud credential materialization and delete fan-out now write `cloud_credential:materialize` through `task_outbox` before Redis delivery.
- Cloud credential per-target materialization apply/delete now commits the materialization row change and durable task-outbox row in one SQL helper when production sqlc is wired.
- Cluster-template apply producers now write `cluster_template:apply` through `task_outbox` before Redis delivery, covering platform-default auto-attach on cluster create/adopt, explicit template apply/reapply, and platform-default reapply.
- Registration confirm plus explicit cluster-template apply/reapply now use an atomic SQL helper for `cluster_template_applications` plus `task_outbox` when production sqlc is wired, with the old separate writes retained only as a fallback for tests or partial wiring. Registration retry now also resets the failed timeline step and writes its retry task-outbox row in one SQL helper. If baseline attachment fails after the confirm phase advances, the handler records a failed Platform Baseline step so the mismatch is visible and retryable instead of silent.
- Cluster decommission now unregisters ArgoCD managed clusters by deleting the ArgoCD cluster Secret when the management-plane Kubernetes client is available, then deleting local `argocd_managed_clusters` rows; audit fallback remains for orphan cleanup when the client is unavailable or deletion fails.
- Task-outbox delivery health now has Prometheus gauges for rows by status and oldest due age, chart alerts for stalled/dead rows, and a runbook for inspection and requeue decisions.
- Added a superuser-gated admin task-outbox API to list rows by status and retry non-delivered rows without exposing raw task payloads.
- The settings Operations page now includes a superuser task-outbox panel with status filtering and retry for non-delivered rows.
- Added durable `repair_job_states` rows with last-successful-reconcile and last-error fields for periodic repair jobs, and wired the CRD ownership drift check plus ArgoCD auto-adoption sweep to record repair health.
- Added a generic `operation_idempotency_keys` ledger plus atomic SQL helpers for ArgoCD, tool, catalog, logging, monitoring, workload, fleet, restore, and deferred-operation rows so those operation-create paths can reserve `Idempotency-Key` values and return the same operation on client retry.
- Operation idempotency helpers now use a single atomic upsert/claim CTE so first-use keys cannot race the operation insert.
- Added the first shared operation-runner helper for claim/supersede/fresh-running-skip/mark-running dispatch and migrated the ArgoCD, Tools, Catalog, Logging, Monitoring, and Workloads reconcilers to it, with table-driven runner tests.
- Added a shared operation status-summary helper and migrated ArgoCD, Tools, Catalog, Logging, Monitoring, and Workloads controller status endpoints to it for queue depth, stale-running, recent operations, and latest-failure accounting.
- Added a shared retry eligibility guard for operation retry endpoints and fixed Workloads and Monitoring retry handlers to require target-scope update permission before requeueing failed/superseded rows.
- Added a shared superuser gate helper, central tests for authenticated-user/store/non-superuser behavior, and migrated admin drill, admin queues, task outbox, dashboards, platform settings, maintenance windows, platform default template, platform-baseline coverage, quotas, Vault, compliance export, compliance baselines, GitOps, support bundle, and management logs gates to it.
- Added `RespondRequestError`, which includes `request_id` in JSON error bodies when request ID middleware is present, and migrated shared authorization/superuser helper failures to it.
- Migrated the service proxy, stream ticket issuance, shared operation retry-state guard, operation list/get/retry/status endpoints for ArgoCD, Tools, Catalog, Logging, Monitoring, and Workloads, plus monitoring operation authorization failures, to request-aware error responses.
- Migrated SSO login/callback failures and admin queue, task-outbox, and backup-drill operational errors to request-aware error responses.
- Migrated AuthHandler login/refresh/password/API-token flows and TOTP enrollment/verify/admin flows to request-aware error responses.
- Migrated remaining production Go `RespondError(w, ...)` call sites in handlers and route guards to request-aware responses; `RespondError` remains only as the compatibility helper and in tests.
- Centralized the remaining bespoke admin gate paths by routing TOTP admin disable, registration cancel, and route-level key-status through the shared superuser gate, and by making the legacy error-returning superuser helper delegate authenticated DB-user lookup to the shared authorization helper. Remaining authenticated-user reads are middleware, self-service identity, stream ticket, or attribution paths.
- Cluster API responses now include per-baseline-component ArgoCD ownership entries for the five built-in baseline ApplicationSets, and the cluster GitOps ownership panel renders each component's owner, namespace, and ApplicationSet name.
- Documented the CRD versioning and conversion policy for `management.astronomer.io/v1alpha1`, including the rules that require a `v1beta1` conversion webhook before breaking schema or ownership changes.
- Frontend dependency hygiene now supports clean `npm ci` without peer-resolution flags, removes unused `next-auth`, updates direct vulnerable packages, and passes `npm audit --audit-level=moderate` with zero vulnerabilities.
- Recharts has been upgraded to the active 3.x line and the existing metrics chart passes the v3 TypeScript surface.
- Cluster API responses now include an ArgoCD ownership summary, and the cluster detail overview shows GitOps registration, ArgoCD cluster Secret names, and whether baseline components are ArgoCD-owned, pending, local, or legacy Helm-over-tunnel.
- ArgoCD auto-adoption now writes adoption timeline steps and an `ArgoCDAdopted` cluster condition; failed adoption steps retry `argocd:auto_register_cluster` through the durable task outbox.

## P0: Close Public High-Privilege Routes

### Finding

The Kubernetes passthrough route is mounted outside the authenticated `/api/v1` router:

- `internal/server/routes.go`: `/api/v1/clusters/{cluster_id}/k8s/*`
- `internal/tunnel/proxy.go`: `HandleK8sProxy` does not authenticate or authorize; it forwards the request to the agent.
- `deploy/agent/install.yaml.template`: the agent ServiceAccount historically rendered `apiGroups: ["*"], resources: ["*"], verbs: ["*"]`; that remains only the explicit/default `admin` profile.

If reachable, this is effectively an unauthenticated Kubernetes API proxy to any connected adopted cluster.

### Tasks

- [x] Move `/api/v1/clusters/{cluster_id}/k8s/*` behind `RequireAuthWithQueries`.
- [x] Add cluster-scoped RBAC enforcement before forwarding:
  - `GET`, `LIST`, `WATCH`: `clusters:read` or more specific resource verbs.
  - mutating verbs: require explicit `clusters:update` initially, then narrow by resource family.
- [x] Add API-token scope enforcement for all mutating passthrough calls.
- [x] Add route tests proving unauthenticated requests get `401` and unauthorized users get `403`.
- [x] Add integration tests for `GET pods`, `PATCH deployment`, `DELETE pod`, and `WATCH` paths.
- [x] Add audit records for every mutating passthrough request with method, k8s path, cluster id, and namespace/name when parseable.
- [x] Strip user-controlled Kubernetes impersonation headers before forwarding to adopted clusters.
- [x] Add explicit classification for high-risk subresources such as `pods/exec`, `pods/attach`, and `pods/portforward`; gate them separately from ordinary read/write proxy calls.

### Acceptance Criteria

- A request to `/api/v1/clusters/{id}/k8s/api/v1/pods` without auth returns `401`.
- A user without access to the cluster gets `403`.
- A read-only user cannot mutate Kubernetes resources through the passthrough.
- Client-supplied `Impersonate-*` headers are never forwarded to adopted clusters.
- Existing UI resource reads still work after auth/RBAC wrapping.

## P0: Remove or Protect Remotedialer Demo Endpoint

### Finding

`/api/v1/clusters/{id}/v2/pods/` is mounted outside the authenticated router as a remotedialer demonstration endpoint.

### Tasks

- [x] Remove the endpoint if it is no longer needed.
- [x] If kept, mount it inside protected routes with `clusters:read`.
- [x] Add a test asserting unauthenticated access returns `401`.
- [x] Mark any future demo/migration endpoints behind a development-only feature flag.

### Acceptance Criteria

- No unauthenticated caller can list pods through the remotedialer path.
- Production builds do not expose demo-only cluster data endpoints.

## P0: Protect Registration Event Streams

### Finding

`/api/v1/clusters/{id}/registration/events/` is mounted outside the authenticated router, and `StreamEvents` assumes route middleware already enforced auth.

### Tasks

- [x] Authenticate registration SSE requests.
- [x] Enforce `clusters:read` for the target cluster.
- [x] Prefer short-lived stream tickets over long-lived JWT query parameters.
- [x] Add tests for missing auth.
- [x] Add tests for invalid token, no cluster access, and authorized cluster access.

### Acceptance Criteria

- Registration events for a cluster are visible only to users who can read that cluster.

## P0: Fail Closed on Missing Auth and RBAC Wiring

### Finding

Several helpers intentionally pass through when core security dependencies are nil:

- `requireAuth` returns pass-through if JWT is nil.
- `requirePermission` returns pass-through if RBAC engine or querier is nil.
- `authorizationSupport` treats missing engine/querier as unrestricted.
- Stream auth accepts requests when JWT manager is nil.

This is useful in tests but dangerous if production boot ever misses a dependency.

### Tasks

- [x] Add a production boot invariant check after wiring:
  - JWT manager must have a non-empty, non-dev signing key.
  - RBAC engine and querier must be wired.
  - encryption key must be valid in production.
  - stream auth must be wired for all WS/SSE handlers.
- Split helpers into explicit modes:
  - `RequireAuthStrict` for production routes.
  - `RequireAuthOptionalForTest` only in tests.
- [x] Make `authorizationSupport` fail closed unless explicitly configured for test mode.
- [x] Add a route-table security test that walks registered routes and verifies protected prefixes have auth/RBAC middleware or self-auth handlers.

### Acceptance Criteria

- A production server cannot start with missing JWT/RBAC/encryption dependencies.
- Tests that need no-auth behavior opt in explicitly.

## P1: Replace URL Tokens and LocalStorage Sessions

### Finding

The frontend stores JWTs in `localStorage` and mirrors the access token into a JS-readable `astronomer_session` cookie for ArgoCD proxy navigation. WS/SSE paths previously passed those tokens in `?token=`; browser stream clients now use one-use `?ticket=` values instead.

This increases exposure through XSS, browser extensions, proxy logs, access logs, referrers, and screenshots.

### Tasks

- [x] Move browser auth to `HttpOnly; Secure; SameSite=Lax` cookies issued by the Go server.
- [x] Keep API tokens only for headless callers, not browser session storage.
- [x] Add short-lived stream tickets:
  - [x] `POST /api/v1/streams/tickets/` returns a one-use, 30-60 second ticket.
  - [x] WS/SSE endpoints accept only stream tickets in the query string.
  - [x] Tickets are bound to user id, route type, cluster id, and expiry.
- [x] Update browser EventSource/WebSocket clients to request stream tickets instead of appending JWTs to stream URLs.
- [x] Remove JWT mirroring from `frontend/src/lib/api.ts`.
- [x] Add CSP headers to reduce XSS token-theft risk while migration is underway.
- [x] Add CSRF protection for browser cookie-authenticated unsafe methods.

### Acceptance Criteria

- [x] No long-lived JWT or refresh token is stored in `localStorage`.
- [x] WS/SSE URLs never contain session JWTs or API tokens.
- ArgoCD UI proxy still supports top-level browser navigation.

## P1: Tighten Service Proxy Access

### Finding

`/api/v1/clusters/{cluster_id}/proxy/service/{namespace}/{service_port}/*` is authenticated by router placement, but the handler has no resource-specific RBAC check, service allowlist, or audit.

### Tasks

- [x] Add `clusters:read` for GET/HEAD and `clusters:update` for mutating methods.
- [x] Restrict proxy targets to known enabled tool services by default.
- [x] Add optional per-tool `service_proxy_allowed` metadata.
- [x] Block proxying to Kubernetes control-plane namespaces unless explicitly enabled.
- [x] Add audit records for non-GET service proxy use.
- [x] Add route/table tests for API-token scope behavior in addition to JWT/RBAC behavior.

### Acceptance Criteria

- Users cannot use the service proxy as a generic in-cluster SSRF primitive.
- Tool UI links still work for approved tool services.

## P1: Reduce Agent and Management-Plane Kubernetes Privileges

### Finding

Both adopted-cluster agents and the management-plane ServiceAccount use cluster-wide `*/*/*` privileges. That simplifies feature parity but creates a large blast radius.

### Tasks

- [x] Define privilege profiles:
  - [x] `viewer`: list/watch/get resources, logs.
  - [x] `operator`: common workload mutations without cluster-admin or ClusterRole escalation.
  - [x] `admin`: explicit break-glass full cluster admin.
- [x] Render agent RBAC from selected profile.
- [x] Introduce server-side admission checks where feasible: the proxy strips caller-controlled `Impersonate-*` headers and separately gates high-risk Kubernetes subresources such as `pods/exec`, `pods/attach`, and `pods/portforward`.
- [x] Add a UI warning when a cluster is adopted with full-admin agent privileges.
- [x] Document which features require which privilege profile.

### Acceptance Criteria

- A newly adopted cluster can run in a non-admin profile.
- Full-admin access is explicit and visible.

## P1: Encrypt or Retire Legacy Plaintext Secret Columns

### Finding

Most newer secret-bearing tables use Fernet-encrypted columns, but older schema areas still have plaintext-looking fields such as registry and backup credentials.

### Tasks

- [x] Inventory every `password`, `secret`, `token`, and credential column.
- [x] Classify each as plaintext, hashed, encrypted, or non-sensitive.
- Add migrations for plaintext credential columns:
  - [x] cluster registration tokens: add `token_hash`, backfill existing rows, update new writes and lookup to use hashes while leaving the deprecated plaintext column for one expiry/release window;
  - [x] cluster agent tokens: add `token_hash`, backfill existing rows, update new durable-token writes and lookup to use hashes while leaving the deprecated plaintext column for one rotation/release window;
  - [x] backup storage credentials: update new handler writes to store access and secret keys only in `encrypted_credentials` when Fernet encryption is configured, while preserving legacy fallback for installations without `ASTRONOMER_ENCRYPTION_KEY`;
  - [x] cluster registry passwords: add `registry_password_encrypted`, update multi-registry and legacy single-registry writes to use it when Fernet encryption is configured, and update registry apply/project reconcile/test paths to decrypt before materializing pull secrets;
  - add encrypted replacement column for any future plaintext credential columns;
  - [x] backfill by encrypting existing values: add the idempotent `security:migrate_plaintext_credentials` worker task, scheduled every six hours, to encrypt and blank legacy `backup_storage_configs.access_key` / `secret_key` and `cluster_registry_configs.registry_password` rows;
  - [x] update handlers;
  - remove plaintext read paths after one release.
- [x] Add a test that prevents new secret-like columns without an encryption/hashing annotation.

### Acceptance Criteria

- Secret material at rest is hashed or encrypted consistently.
- New migrations fail review when adding unclassified secret-bearing columns.

## P1: Production Security Defaults and Deployment Hardening

### Tasks

- [x] Add container and pod security contexts for all chart workloads:
  - [x] `runAsNonRoot`;
  - [x] `allowPrivilegeEscalation: false`;
  - [x] `readOnlyRootFilesystem` where possible;
  - [x] `seccompProfile: RuntimeDefault`;
  - [x] drop Linux capabilities.
- [x] Add default NetworkPolicies:
  - [x] frontend to server;
  - [x] server to Postgres, Redis, Dex, external HTTP(S), and Kubernetes API ports as needed;
  - [x] worker to Postgres, Redis, external HTTP(S), and Kubernetes API ports as needed;
  - [x] ingress controls for public listener ports and metrics ports;
  - [x] bundled Postgres and Redis ingress limited to server/worker pods.
- Make production preflight fail, not warn, for:
  - [x] missing `config.serverURL`;
  - [x] empty or dev `secrets.secretKey`;
  - [x] empty or dev `secrets.encryptionKey`;
  - [x] local-password-only auth without explicit acknowledgement;
  - [x] Postgres DSN without TLS when using external Postgres.
- [x] Add HTTP security headers:
  - CSP;
  - `X-Frame-Options` or CSP `frame-ancestors`;
  - `X-Content-Type-Options`;
  - `Referrer-Policy`;
  - HSTS when TLS is enabled.

### Acceptance Criteria

- Helm production rendering fails on weak production inputs.
- Pods pass a restricted Pod Security Standards review except where explicitly documented.

## P1: Control-Plane State Model and CRD Boundaries

### Finding

Astronomer stores durable product state in Postgres and mirrors only a subset of that state into Kubernetes CRDs. That split is valid, but the contract is not explicit enough yet.

Kubernetes/etcd should hold Kubernetes-native desired state and reconciliation surfaces. Postgres should hold relational product state, audit history, user identity, permissions, operation history, credentials, and cross-cluster inventory. Today the CRD path is optional, covers only `Cluster` and `Project`, runs inside server pods, now has leader election, and still treats controller startup failures as warnings.

If CRDs become the operator-facing GitOps API, this needs a stronger ownership and HA model.

### Tasks

- [x] Write a control-plane state contract that classifies each domain:
  - Postgres-owned: users, teams, RBAC bindings, audit logs, API tokens, operation history, credentials, billing/metadata, durable inventory.
  - Kubernetes-owned: ArgoCD `Application`, `ApplicationSet`, `AppProject`, cluster Secrets, generated workload manifests, status from Kubernetes controllers.
  - CRD-owned intent: operator-declared fleet objects such as clusters, projects, adoption policy, baseline profiles, and optional policy bundles.
  - Cached/mirrored: health summaries, component status, vulnerability reports, and discovered resources.
- [x] Define precedence rules for REST, UI, and CRD writes:
  - [x] make CRD spec authoritative for CRD-managed `Cluster` and `Project` fields;
  - [x] add field ownership / last-writer metadata so REST and CRD changes cannot silently fight.
- [x] Add explicit source-of-truth markers in DB rows:
  - [x] `managed_by`: `ui`, `api`, `crd`, `system`, `argocd`;
  - [x] `external_ref`: namespace/name/apiVersion/kind when a CRD owns the row;
  - [x] `observed_generation` for CRD-driven reconciles.
- [x] Wire the CRD controller to set those markers whenever it creates or claims `Cluster` and `Project` rows.
- [x] Promote CRD status conditions to standard Kubernetes condition shape:
  - [x] `type`;
  - [x] `status`;
  - [x] `reason`;
  - [x] `message`;
  - [x] `observedGeneration`;
  - [x] `lastTransitionTime`.
- [x] Enable Kubernetes API best-practice fields for every Astronomer CRD:
  - [x] `status` subresource so spec and status writers do not conflict;
  - [x] OpenAPI validation schemas with enum/default/min/max rules;
  - [x] printer columns for phase, age, owning DB row, and readiness;
  - [x] finalizers for external resources that must be cleaned up before CR deletion;
- [x] Add an explicit versioning strategy (`v1alpha1` to `v1beta1` to `v1`) and conversion webhook plan before any breaking schema change.
- [x] Add CRDs or CRD fields for the ArgoCD adoption lifecycle:
  - [x] `spec.argocd.autoAdopt`;
  - [x] `spec.argocd.instanceRef`;
  - [x] `spec.baseline.profile`;
  - [x] `spec.agent.privilegeProfile`;
  - [x] `status.argocd.phase`;
  - [x] `status.argocd.clusterSecretName`;
  - [x] `status.argocd.conditions`.
- Add a `PlatformBaseline` or `BaselineProfile` CRD for declarative baseline intent while continuing to use ArgoCD `ApplicationSet` CRDs for actual fan-out. Current implementation keeps baseline selection on `Cluster.spec.baseline.profile`; introduce a separate CRD only when multiple reusable baseline profiles become operator-managed objects.
- [x] Add an `AdoptionPolicy` CRD or `Cluster.spec.adoptionPolicy` section so operators can declaratively choose auto-adoption, baseline profile, agent privilege profile, and allowed management modes. Implemented as `Cluster.spec.adoptionPolicy` plus existing `spec.argocd`, `spec.baseline`, and `spec.agent`; the controller persists policy intent into cluster annotations.
- Decide whether the CRD controller remains embedded in the server or moves to a dedicated controller deployment:
  - if embedded, keep leader election enabled and verify only one server replica reconciles at a time;
  - if dedicated, give it a separate least-privilege ServiceAccount and independent rollout/health checks.
- [x] Make CRD controller startup fail closed when `crds.enabled=true` in production.
- Add a reconciliation ownership matrix to code and docs:
  - [x] DB row created from REST/UI should not be overwritten by a CR unless ownership is transferred;
  - [x] CR-owned `Cluster` and `Project` rows reject REST/UI updates to CR-owned fields;
  - [x] add an explicit takeover operation for UI/API ownership transfer;
  - ArgoCD-owned component deployment fields should not be mutated by the legacy Helm-over-tunnel tool path.
- Add reconciliation idempotency and conflict tests:
  - CRD create/update/delete;
  - REST update of CRD-owned fields;
  - controller restart mid-reconcile;
  - duplicate server replicas;
  - DB restore while CRDs still exist;
  - CRD restore while DB rows already exist.

### Acceptance Criteria

- Every durable field has a documented source of truth.
- CRD-managed fields cannot be silently overwritten by REST/UI writes without explicit ownership transfer.
- Exactly one active reconciler handles CRDs in HA deployments.
- Postgres restore and Kubernetes/etcd restore procedures describe how to re-establish consistency.
- ArgoCD owns deployment reconciliation; Astronomer CRDs own fleet/adoption intent; Postgres owns product/audit/credential state.

## P1: Postgres Robustness, HA, and Restore Guarantees

### Finding

Using Postgres instead of Kubernetes etcd for the product control-plane database is appropriate for this system, but it raises a different set of reliability requirements. The plan needs explicit work for migrations, backups, restore drills, consistency repair, and queue/idempotency semantics.

### Tasks

- [x] Define production Postgres requirements:
  - [x] managed Postgres or HA Postgres with automated failover;
  - [x] TLS required for external Postgres;
  - [x] point-in-time recovery;
  - [x] regular logical or physical backups;
  - [x] tested restore into a clean environment.
- [x] Add migration safety standards:
  - [x] backward-compatible migrations before code rollout;
  - [x] no long exclusive locks on large tables;
  - [x] online backfills for large encrypted/JSON columns;
  - [x] migration rollback or forward-fix notes.
- [x] Add backup and restore runbooks for:
  - [x] Postgres-only restore;
  - [x] Kubernetes/etcd-only restore;
  - [x] full management-plane restore;
  - [x] adopted-cluster reconnect after restore;
  - [x] ArgoCD resync after restore.
- Add consistency repair jobs:
  - [x] DB cluster exists but CRD missing: only applies to CRD-owned rows because API/UI-owned clusters are not mirrored into Kubernetes by design. The `crd:ownership_drift_check` task now checks stored Kubernetes external refs for CRD-owned cluster rows and writes `CRDOwnershipSynced=False` when the CR is missing instead of recreating declarative GitOps input behind the operator's back.
  - [x] CRD exists but DB row missing: handled by the CRD controller's idempotent create/claim reconcile path.
  - [x] DB says ArgoCD registered but ArgoCD cluster Secret missing: handled by the periodic `argocd:auto_register_cluster` sweep, which re-registers live clusters into ArgoCD.
  - [x] ArgoCD cluster Secret exists but `argocd_managed_clusters` row missing: the ArgoCD auto-adoption sweep now scans Astronomer-owned ArgoCD cluster Secrets and rebuilds the missing Postgres index row for the built-in single-instance topology, refreshing owned labels at the same time.
  - [x] operation row stuck in `running` after worker/server crash: operation processors query `pending` and `running` rows on each reconcile tick and ArgoCD sync operations additionally poll with a timeout, so crashed in-process work is retried or failed by its domain reconciler.
- Make all long-running workers idempotent with durable operation keys.
- Add transaction boundaries around state changes that enqueue jobs:
  - [x] add a generic `task_outbox` table with dedupe keys, delivery attempts, retry status, and operator-visible `dead` state;
  - [x] add a leader-elected dispatcher that drains committed outbox rows into Redis/Asynq and treats duplicate Asynq task IDs as already delivered;
  - [x] add a producer helper so handlers can write outbox rows through the same transaction as product state;
  - [x] migrate cluster-registration confirm/retry platform-baseline template apply enqueue to outbox-first delivery;
  - [x] migrate agent-connect ArgoCD auto-adoption enqueue to outbox-first delivery;
  - [x] migrate cluster DELETE/decommission immediate enqueue to outbox-first delivery for REST and CRD finalizer paths;
  - [x] migrate GitOps-triggered decommission enqueue to outbox-first delivery;
  - [x] migrate cloud credential materialization/delete enqueue to outbox-first delivery;
  - [x] migrate cluster create/adopt platform-default template apply and non-registration template apply/reapply to outbox-first delivery;
  - [x] audit lower-criticality direct enqueue paths and either migrate them or document why periodic reconciliation/scheduler ownership is sufficient;
  - [x] wrap outbox writes and their associated product-state writes in explicit database transactions where the handler currently performs separate calls, or document/encode a retryable reconciliation decision where one larger transaction is not the right boundary;
  - [x] add Prometheus alerts and a runbook for `task_outbox.status='dead'` and stalled due rows;
  - [x] add admin/support API views for `task_outbox.status='dead'` and retrying non-delivered rows;
  - [x] add dashboard UI for `task_outbox.status='dead'`.

Lower-criticality direct enqueue audit, 2026-06-12:

- `projects.go` project reconciliation/cleanup still uses direct enqueue with an in-process requester fallback. This should be migrated when project CRD ownership is expanded, but the current blast radius is lower than cluster adoption/decommission because project state is reconciled from database ownership and failed work can be retried by a later edit or repair sweep.
- `cluster_registries.go` apply/unapply, `network_policies.go` apply, and `apiserver_allowlist.go` reconcile are desired-state reconcilers. Direct enqueue is acceptable short term because each has persistent DB state and periodic or explicit reconciliation ownership; add outbox if these become externally visible operation promises.
- `control_plane.go` notification sends are best-effort side effects and should stay out of critical product-state transactions unless notification delivery becomes part of an SLA.
- `worker/tasks/security_scan.go` re-enqueues ingest after timeout from inside an Asynq task. This is covered by worker retry semantics and should not write a second product-state outbox row.
- `worker/tasks/cluster_template_apply.go` recovery sweep and `server.go` first-boot catalog sync are scheduler/bootstrap paths, not request-time state-plus-task commits.

Transaction-boundary follow-up:

- [x] REST cluster DELETE/decommission and CRD finalizer decommission now use a single SQL statement for `cluster_decommissions` plus `task_outbox`.
- [x] convert cluster-registration confirm template-application plus outbox writes into one SQL helper.
- [x] convert cluster-registration retry step reset plus outbox write into one explicit transaction or SQL helper.
- [x] convert cloud credential materialization/delete state updates plus outbox fan-out into explicit transactions where the state mutation and task intent are both required.
- [x] convert cluster-template application upsert/reapply plus outbox writes into explicit transactions where a user-facing operation promise is created.
- [x] decide whether GitOps decommission tombstone updates need the same atomic helper or whether the existing decommission retry sweep is enough. Decision: keep the existing durable decommission row plus retry sweep; the tombstone path is not the sole owner of cleanup progress, and failed delivery is repaired by the decommission scheduler.
- [x] evaluate whether cluster-registration phase transition plus baseline attachment should share one explicit transaction, or whether a failed attach after phase advance should be represented as a registration step failure and retried. Decision: keep the phase transition in the registration service boundary; record `template_failed` when baseline attachment cannot be committed so operators see and retry the failed attach.
- Add database invariants that support reconciliation:
  - [x] unique constraints for CR external refs;
  - [x] idempotency keys on durable task-outbox rows;
  - add idempotency keys on operation-domain rows that are not yet outbox-backed:
    - [x] add the shared `operation_idempotency_keys` ledger and SQL helpers;
  - [x] wire ArgoCD, tool, catalog, logging, monitoring, workload, fleet, restore, and deferred-operation create handlers to reserve `Idempotency-Key` before creating work and attach the resulting operation response atomically;
  - [x] add idempotent create helpers and handler wiring for fleet, restore, and deferred-operation rows, which have different schemas from the common operation queue shape;
  - [x] add replay tests proving a repeated `Idempotency-Key` returns the original operation rather than creating duplicate pending/running work for common operation-queue, fleet, restore, and deferred-operation paths.
  - [x] monotonic generation/observed generation columns for CR-owned rows;
  - [x] last-successful-reconcile and last-error fields for repair jobs. Implemented as `repair_job_states`, with the CRD ownership drift check and ArgoCD auto-adoption sweep recording durable repair health; expose these rows in the UI before promising operator-facing repair SLOs.
- Add DB health and capacity observability:
  - [x] connection pool saturation;
  - [x] query duration;
  - [x] transaction duration / oldest open transaction age;
  - [x] migration duration;
  - [x] deadlocks;
  - [x] replication lag;
  - [x] backup age;
  - [x] restore drill result age.

### Acceptance Criteria

- A failed Redis enqueue cannot permanently strand important DB state.
- Restores are rehearsed and documented, not assumed.
- Reconciliation jobs can repair drift between Postgres, CRDs, ArgoCD, and adopted clusters.
- Production installs have explicit database HA, TLS, backup, and PITR guidance.

## P1: UI Security and Access Control

### Findings

- The Next.js middleware currently returns `NextResponse.next()` for all routes.
- The sidebar shows links regardless of user permissions and feature gates.
- Auth redirect happens client-side in dashboard layout, which can create loading flashes and unnecessary API calls.

### Tasks

- [x] Add a real frontend auth gate for dashboard routes.
- [x] Load current user permissions once and expose a `can(resource, verb, scope)` helper.
- [x] Hide or disable nav items the user cannot access.
- [x] Mirror backend feature gates in navigation and pages.
- Standardize `403` pages and empty states so unauthorized views do not look broken:
  - [x] Add a shared `EmptyState` component with action links/buttons and disabled-action support.
  - [x] Migrate the settings admin gate, feature-disabled dashboard view, and backups overview.
  - [x] Migrate remaining bespoke "not found", "no data", and permission-denied copy across cluster detail, ArgoCD detail, settings detail, security, and catalog pages.

### Acceptance Criteria

- [x] A logged-out browser cannot render dashboard chrome before redirect.
- Users do not see primary actions they cannot execute.
- [x] Feature-disabled pages are hidden or show a clear disabled state.

## P1: UI Consistency and Workflow Polish

### Tasks

- [x] Add a shared empty/disabled/forbidden state component for common page-level no-data and access-control states.
- Create a shared page scaffold for list/detail/settings pages.
- Replace bespoke buttons, badges, forms, modals, and tabs with shared components.
- Keep dashboard pages dense and operational:
  - avoid nested cards;
  - keep table controls stable;
  - use icon buttons for common actions;
  - standardize destructive confirmations.
- Add responsive checks for:
  - [x] cluster detail;
  - [x] registration wizard;
  - [x] ArgoCD pages;
  - [x] catalog install modal;
  - [x] settings forms.
- Add loading, partial failure, and stale-data states for multi-cluster pages.

### Acceptance Criteria

- Common workflows have predictable layouts and controls.
- Text does not overflow buttons, badges, sidebars, or cards on mobile and desktop.

## P2: Break Up Large Backend and Frontend Files

### Findings

Large files are hiding ownership boundaries:

- `internal/server/routes.go`: central route registry is very large.
- `internal/handler/argocd.go`, `catalog.go`, `tools.go`, `resources.go`: multiple responsibilities per file.
- `frontend/src/lib/api.ts`, `frontend/src/lib/hooks.ts`, `frontend/src/types/index.ts`: monolithic client layer.

### Tasks

- Split routes by domain:
  - auth;
  - clusters;
  - workloads;
  - integrations;
  - admin;
  - streaming.
- Create a route metadata registry with auth/RBAC/scope/security annotations.
- Split frontend API clients by domain and export through an index.
- Split React Query hooks by domain.
- Split shared types by domain and generate common API types from OpenAPI where feasible.

### Acceptance Criteria

- Route security annotations are testable.
- New contributors can edit one domain without reading a 1,000+ line file.

## P2: Consolidate Duplicate Operation Controller Code

### Finding

ArgoCD, Tools, Catalog, Logging, Monitoring, and Workloads all duplicate operation patterns: claim pending rows, supersede stale rows, dispatch bounded workers, record operation events, retry, list, get, and controller status.

### Tasks

- Introduce a small generic operation runner package:
  - [x] claim;
  - [x] supersede;
  - [x] bounded dispatch;
  - [x] retry eligibility guard;
  - retry backoff policy;
  - [x] event recording hooks;
  - [x] status summary.
- Keep domain-specific execution logic inside each handler.
- [x] Add table-driven tests for the generic runner.
- [x] Migrate one domain first: Tools now uses the shared claim runner.
- [x] Roll out the shared runner to ArgoCD, Catalog, Logging, Monitoring, and Workloads.
- [x] Ensure operation retry endpoints enforce update authorization before requeueing. Workloads and Monitoring had gaps; regression tests now prove read-only users cannot retry those target-scoped operations.
- Consolidate remaining list/get response helpers after the claim runner is migrated across all operation domains. Controller status summaries and retry eligibility are now shared.

### Acceptance Criteria

- Operation behavior is consistent across domains.
- Retry and event semantics are covered once and reused.

## P2: Standardize Authorization in Handlers

### Tasks

- Replace per-handler superuser checks with a shared middleware/helper.
  - [x] Add a shared `requireSuperuser` helper with centralized tests.
  - [x] Migrate high-visibility admin gates for admin drill, admin queues, task outbox, dashboards, platform settings, maintenance, platform defaults, baseline coverage, quotas, Vault, compliance export, compliance baselines, GitOps, support bundle, and management logs.
  - [x] Migrate remaining bespoke admin/self-service gates where the handler still hand-parses `GetAuthenticatedUser`: TOTP admin disable, registration cancel, and route-level key-status now use the shared superuser gate, and the legacy error-returning admin helper delegates authenticated DB-user lookup to the shared authorization helper. Remaining direct authenticated-user reads are middleware, self-service identity, stream ticket, or audit/display attribution paths.
- Remove duplicate `GetAuthenticatedUser` parsing code from handlers.
- Add typed permission helpers for cluster, project, and global scopes.
- Add tests for every admin route proving non-superusers get `403`.
- [x] Add a CI check for routes under `/admin/` that lack either admin middleware or an explicit documented exception.

### Acceptance Criteria

- Admin access control is uniform and easy to audit.

## P2: API Contract and Error Consistency

### Tasks

- Generate or validate OpenAPI from actual route registration.
- Standardize response envelopes across handlers.
- Standardize error shape:
  - `code`;
  - `message`;
  - optional `details`;
  - request id.
    - [x] Request/correlation IDs are already generated, validated, stored on context, and echoed in `X-Request-ID` and `X-Correlation-Id` response headers.
    - [x] Add a request-aware error response helper so `request_id` can also be included in JSON error bodies.
    - [x] Migrate shared authorization and superuser helper failures to the request-aware error helper.
    - [x] Migrate the service proxy, stream ticket issuance, operation retry-state guard, operation list/get/retry/status endpoints for ArgoCD, Tools, Catalog, Logging, Monitoring, and Workloads, and monitoring operation authorization errors to the request-aware error helper.
    - [x] Migrate SSO login/callback and admin queue/task-outbox/backup-drill operational errors to the request-aware error helper.
    - [x] Migrate AuthHandler login/refresh/password/API-token flows and TOTP enrollment/verify/admin flows to the request-aware error helper.
    - [x] Migrate remaining production Go `RespondError(w, ...)` call sites in handlers and route guards to request-aware responses; leave the compatibility helper and its tests intact.
- [x] Remove stale Django/Python comments from frontend and docs.
- [x] Add contract tests for frontend-called endpoints: Jest now verifies critical SPA endpoints such as auth login/logout/me, clusters, registration options, activity, alert events, tools, ArgoCD instances, and backup drill exist in `docs/openapi.yaml`.

### Acceptance Criteria

- Frontend API calls match backend routes without ad hoc aliases.
- Errors are predictable enough for shared UI handling.

## P2: ArgoCD Adoption Plan Integration

### Tasks

- [x] Implement the Phase 1 ArgoCD auto-adoption worker and service-token proxy route.
- [x] Ensure the new ArgoCD cluster proxy is protected by a service identity, not by unauthenticated access to the generic k8s proxy.
- [x] Add built-in ArgoCD ApplicationSets for platform baseline components.
- [x] Add an ArgoCD-owned baseline status panel on cluster detail pages.
- [x] Show whether baseline components are managed by ArgoCD, pending ArgoCD registration, local, or Helm-over-tunnel.
- [x] Show per-component ownership for the built-in baseline ApplicationSet catalog: trivy-operator, kube-state-metrics, node-exporter, fluent-bit, and cert-manager.

### Acceptance Criteria

- New clusters are automatically registered into built-in ArgoCD.
- Platform baseline components converge through ArgoCD/ApplicationSets when ArgoCD baseline ownership is enabled.
- Ownership is visible in the UI.

## P3: Testing, Tooling, and Process

### Tasks

- [x] Add CI jobs:
  - [x] `go test ./...`;
  - [x] frontend typecheck;
  - [x] frontend unit tests;
  - [x] Helm template validation for dev and production values;
  - [x] route security tests;
  - [x] dependency vulnerability scan;
  - [x] container image scan;
  - [x] SBOM generation.
- [x] Resolve frontend dependency hygiene:
  - [x] update React 19-incompatible packages so `npm ci` succeeds without `--legacy-peer-deps`;
  - [x] triage and clear current `npm audit --audit-level=moderate` findings.
- [x] Upgrade Recharts to the active 3.x line and validate existing chart usage.
- [x] Add Playwright smoke tests for:
  - [x] login/logout;
  - [x] cluster registration;
  - [x] cluster detail read-only user;
  - [x] admin-only settings hidden for normal user;
  - [x] ArgoCD page access.
- [x] Add a security review checklist to PR template:
  - [x] new route auth/RBAC;
  - [x] new secret storage;
  - [x] new tunnel/proxy path;
  - [x] new external HTTP client;
  - [x] new Kubernetes RBAC.
- [x] Add threat model docs for:
  - [x] browser sessions;
  - [x] agent tunnel;
  - [x] ArgoCD cluster proxy;
  - [x] service proxy;
  - [x] in-browser kubectl shell.

### Acceptance Criteria

- CI catches unauthenticated high-privilege routes before merge.
- New secret and proxy code has explicit review hooks.

## Proposed Implementation Order

1. Protect `/k8s/*`, remotedialer demo, registration SSE, and fail-closed wiring.
2. [x] Replace browser stream URL JWTs with short-lived stream tickets.
3. [x] Move browser sessions away from `localStorage`.
4. [x] Add service proxy RBAC and allowlists.
5. Define the Postgres/CRD/ArgoCD source-of-truth contract.
6. Add Postgres HA, backup, restore, and consistency-repair guarantees.
7. [x] Add production chart hardening and security headers.
8. [x] Add frontend permission-aware navigation.
9. Split route/API/hook/type monoliths.
10. Consolidate operation controller duplication.
11. Implement least-privilege agent profiles.
12. Add CI security gates and threat model docs.

## Added Robustness Items From Postgres/CRD Review

Astronomer should treat Postgres as the durable control-plane state store and Kubernetes/etcd as the reconciliation substrate. That split is valid, but it needs explicit contracts so operators know which state wins.

Tasks to add:

- [x] Define source-of-truth ownership per domain:
  - Postgres authoritative: users, RBAC, projects, cluster inventory, operation history, audit, credentials metadata.
  - Kubernetes CRDs authoritative or mirrored: desired fleet resources that benefit from Kubernetes reconciliation and status conditions.
  - ArgoCD authoritative: long-lived component deployment desired state and health.
- [x] Add a `Cluster` CRD reconciliation contract only where Kubernetes-native reconciliation is valuable; do not mirror every Postgres row into CRDs.
- Add `AdoptionPolicy` and `BaselineProfile`/`PlatformBaseline` APIs only if they become declarative operator workflows; keep user/session/audit/credential records out of CRDs.
- [x] Add status subresources, finalizers, validation schemas, and printer columns before expanding the public CRD surface.
- [x] Add CRD versioning/conversion policy before any breaking public CRD schema change.
- [x] Add CRD controller writes for `managed_by`, `external_ref_*`, and `observed_generation` on claimed DB rows.
- [x] Add controller-runtime leader election for the current embedded CRD controller; verify any future controllers inherit the same pattern.
- [x] Add reconciliation repair jobs for currently supported drift classes: CRD-owned DB row drift and ArgoCD auto-adoption/managed-cluster index drift.
- [x] Add a transactional outbox for critical side effects currently emitted as best-effort Redis tasks after DB writes, covering cluster create/adopt baseline apply, ArgoCD registration, decommission, GitOps decommission, cloud credential materialization, and cluster-template apply/reapply.
- [x] Add Postgres HA requirements to Helm values and docs: external managed Postgres recommended, TLS required in production, pool sizing, backups, PITR, restore drill, and schema health fail-fast.
- [x] Add restore-order runbook covering mismatched Postgres and Kubernetes/etcd snapshots.

## Open Questions

- Should the generic Kubernetes passthrough continue to support arbitrary mutating requests, or should mutating resource changes move through typed handlers only?
- Should pod exec, attach, and port-forward be disabled on the generic passthrough and forced through audited typed handlers only?
- Which adopted-cluster features must remain available under a non-admin agent RBAC profile?
- Should ArgoCD use the generic k8s proxy, a dedicated Argo-only proxy, or Kubernetes-native credentials minted by the agent?
- Should browser auth be fully cookie-based, or should the frontend become a BFF that never exposes tokens to JavaScript?
- Which fields are CRD-authoritative once a `Cluster` or `Project` has an owning CR?
- Should the CRD controller stay embedded in the server with leader election, or move to a separate controller deployment?
- Should important async work use an outbox table instead of best-effort Redis enqueue after DB commits?
- What is the supported recovery order when Postgres and Kubernetes/etcd snapshots are from different times?

## Non-Goals

- Rewriting all handlers before closing security gaps.
- Removing the agent tunnel.
- Removing ArgoCD support.
- Replacing the existing RBAC engine.
