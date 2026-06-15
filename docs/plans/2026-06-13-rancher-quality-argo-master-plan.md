# Rancher-Quality Argo-Based Management Master Plan

Date: 2026-06-13
Status: In progress
Scope: Rancher-quality management for adopted clusters, using Argo CD instead of Fleet, with cluster provisioning intentionally excluded.

## Implementation Progress

Implemented on 2026-06-14:

- Added shared structured-log helpers for `actor_id`, `actor_auth_method`, `cluster_id`, and `operation_id`, plus safe task-payload identifier extraction that does not mislabel generic `user_id` fields as actors.
- Wired HTTP request logs to record authenticated actor details via a per-request log context populated by auth middleware, and to derive cluster/operation IDs from chi route parameters.
- Wired worker start/completion logs to include actor, cluster, and operation IDs when those identifiers are present in task payloads.
- Added focused tests for structured log helper extraction, HTTP actor/cluster/operation fields, and worker payload identifier fields.
- Added `docs/package-ownership.md` defining ownership boundaries for server, middleware, handlers, auth, RBAC, agent, tunnel, GitOps, monitoring, DB, worker, task, and redaction packages.
- Updated `docs/secret-handling-policy.md` with the redaction ownership decision: diagnostics/support bundles use `internal/redaction`, while CloudCredential, Vault, and audit redactors remain intentionally specialized because their sentinels and stable JSON shapes are API/audit contracts.
- Added explicit `astronomer_audit_write_failures_total{path}` metrics for async audit batch insert failures and synchronous fallback write failures, with tests covering both paths.
- Updated `docs/metrics-v1.md` and `docs/logs-v1.md` to document existing HTTP, DB pool, worker queue-depth, agent heartbeat/tunnel, Argo client, audit, and structured-log identifier fields.
- Added `astronomer_k8s_proxy_errors_total{mode,reason}` for Kubernetes proxy-owned failures across normal and watch paths, with tests for agent-unavailable and invalid-agent-response errors.
- Added `astronomer_worker_job_retry_attempts_total{job}` from the central worker wrapper whenever Asynq supplies a retry count greater than zero, and logged `retry_count` / `max_retry` when available.
- Added `astronomer_argocd_applications{sync_status,health_status}` from a DB-backed reporter that publishes cached Argo Application sync/health state without polling Argo CD directly.
- Added `scripts/code-health-inventory.mjs` and `docs/rancher-quality-phase0-code-health-inventory.md` to make duplicate/dead-code detection reproducible, with hard gates for duplicate frontend dependency declarations and direct frontend HTTP calls outside `frontend/src/lib/api*`.
- Removed the duplicate direct `@playwright/test` declaration from `frontend/package.json`, centralized the compliance export presigned download fetch behind `frontend/src/lib/api/settings.ts`, and wired `npm run code-health` into PR validation.
- Consolidated six page-local frontend API error parsers into `frontend/src/lib/api/errors.ts`, removing the generated `ResponseShape` duplicate-code candidates from cluster-template, cloud-credential, and Dex connector create/edit pages.
- Expanded the shared frontend `queryKeys.argocd` factory and migrated the Argo instance, application detail, ApplicationSet wizard, Argo dialogs, and cluster ownership panel away from inline Argo React Query keys; regenerated the code-health inventory, reducing duplicate-code candidates from 123 to 90.
- Extended shared frontend query keys for logging, cluster groups, and Vault connections, migrated those admin pages away from inline React Query keys, and regenerated the code-health inventory, reducing duplicate-code candidates from 90 to 78.
- Extended shared frontend query keys for Agent Fleet diagnostics/operations, Extensions, and Settings Operations queues/DLQ/outbox pages, migrated those pages away from page-local query-key definitions, and regenerated the code-health inventory, reducing duplicate-code candidates from 78 to 74.
- Added shared `queryKeys.clusterPages` factories for cluster overview and cluster tab surfaces, migrated Apps, Image Scans, Network & Access, Registries, Resources, Service Mesh, mTLS, Snapshots, Templates, and Workloads away from page-local/inline query keys, and regenerated the code-health inventory, reducing duplicate-code candidates from 74 to 57.
- Improved the code-health scanner so component dead-code detection resolves absolute, relative, and dynamic component imports; removed the unused legacy `ClusterCard` and `BottomPanel` components plus the obsolete dashboard portal root, clearing frontend component dead-code candidates and reducing dead-code candidates from 97 to 92.
- Cleared the frontend lint warning baseline by replacing catalog raw image tags with `next/image`, stabilizing hook dependencies in monitoring, Dex install, cloud credential targets, CIS scan summaries, pod logs, sidebar navigation, and cluster resource action columns, and removing an obsolete hook-disable comment.
- Expanded code-health SQL dead-code detection to search all non-generated Go files across the repository, including CLI and tests, while keeping duplicate-helper detection scoped to production `internal/` Go files; the remaining 92 dead-code candidates have no non-generated Go references.
- Promoted page-local frontend `ResponseShape` types and app-route inline/page-local React Query keys into `npm run code-health` hard gates so the cleaned-up frontend API/query-key patterns cannot regress silently.
- Added `deploy/chart/values.schema.json` with type validation and production conditional requirements for external Postgres/Redis, Gateway host, TLS, real secrets, bootstrap email, Dex client secret, and backup S3 wiring; added chart tests proving invalid values fail, valid production wiring renders, and packaged chart archives include the schema.
- Added Helm operational-contract render tests proving only short-lived migrate/preflight Jobs use Helm hooks, runtime ServiceAccount/RBAC resources remain normal managed release objects, bootstrap password Secret/env wiring is stable and documented, and every rendered workload container declares an image pull policy. Also made bundled Postgres/Redis and BusyBox wait containers honor the chart-wide pull policy.
- Regenerated the code-health inventory after the chart-test additions; it still reports no top-level unused Helm values and `npm run code-health` verifies the checked-in inventory.
- Hardened production NetworkPolicy validation and tests: production schema now requires default-deny NetworkPolicy plus explicit external Postgres and Redis egress CIDRs, and render tests cover expected frontend/server/worker/Dex/Postgres/Redis policies, no broad production egress, and no bundled DB/cache policies when external dependencies are used.
- Consolidated duplicated tunnel stream authentication wrappers for pod exec and pod logs behind a shared `authenticateStreamRequest` helper, added kind/cluster-scoping tests, and regenerated the code-health inventory, reducing duplicate-code candidates from 57 to 56.
- Consolidated authenticated-user UUID conversion into `middleware.AuthenticatedUserUUID`, reused it from handler and audit middleware paths, added fallback-behavior tests, and regenerated the code-health inventory, reducing duplicate-code candidates from 56 to 55.
- Refined backend duplicate-helper detection so it ignores same-named receiver methods and Go `init` functions, then consolidated duplicated CIS report JSON parsing helpers into `internal/scanner`; regenerated the code-health inventory, reducing duplicate-code candidates from 55 to 12.
- Exported `middleware.RequestIsHTTPS` and reused it for auth-cookie security and HSTS decisions so HTTPS detection has one tested implementation; regenerated the code-health inventory, reducing duplicate-code candidates from 12 to 11.
- Added `events.RawJSON` for event-bus payload normalization and reused it from SIEM and webhook taps; regenerated the code-health inventory, reducing duplicate-code candidates from 11 to 10.
- Added `events.DecodeFilterGlobs` for JSONB event-filter glob decoding and reused it from SIEM and webhook taps while leaving the logging handler's distinct filter shape local; regenerated the code-health inventory, reducing duplicate-code candidates from 10 to 9.
- Replaced duplicated string fallback helpers with standard-library `cmp.Or` in compliance, dashboards, and observability while keeping the handler package's single shared local helper for its wider handler call sites; regenerated the code-health inventory, reducing duplicate-code candidates from 9 to 7.
- Added `internal/strutil` for first non-blank string selection with explicit preserve-original and trimmed variants, then reused it from auth, observability, CRD reconciliation, alerting, and notification dispatch paths while using `cmp.Or` for raw non-empty fallbacks; regenerated the code-health inventory, reducing duplicate-code candidates from 7 to 5.
- Renamed semantically different duration parsers to `parsePromDuration` and `parseSnapshotTTLDuration` instead of forcing a shared abstraction; regenerated the code-health inventory, reducing duplicate-code candidates from 5 to 4.
- Renamed semantically different truncation helpers to contract-specific names for Prometheus error bodies, kubectl session responses, SIEM event payload caps, and worker dispatch `last_error` caps; regenerated the code-health inventory, reducing duplicate-code candidates from 4 to 3.
- Renamed unrelated SMTP and go-git auth builders to `buildSMTPAuth` and `buildGitAuth` so outbound email and GitOps credential construction remain clearly separated; regenerated the code-health inventory, reducing duplicate-code candidates from 3 to 2.
- Consolidated Argo CD managed-cluster project membership lookup into `argolabels.ProjectsForCluster`, reused it from the handler and Argo auto-registration/label-refresh workers, and added helper tests; regenerated the code-health inventory, reducing duplicate-code candidates from 2 to 1.
- Renamed the remaining unrelated secret apply helpers to `applyAlertSecret` and `applyLocalArgoSecret` because alerting applies remote Opaque secrets through the requester API while self-managed Argo applies typed Kubernetes secrets with conflict retries; regenerated the code-health inventory, reducing duplicate-code candidates from 1 to 0.
- Removed an initial SQL dead-code batch from canonical query files and sqlc surfaces: unused agent connection get/count/delete helpers, unused cluster tool create/delete helpers, and the unused project catalog subscription count helper; regenerated the code-health inventory, reducing dead-code candidates from 92 to 84.
- Removed a second SQL dead-code batch from canonical query files and sqlc surfaces: unused chart-version, SMTP message/status, cluster-group, SSO, TOTP, and network-policy status count/read helpers; regenerated the code-health inventory, reducing dead-code candidates from 84 to 76.
- Removed a third SQL dead-code batch from canonical query files and sqlc surfaces: unused anomaly, API-server allowlist, API token, Argo application, cloud credential materialization, cluster condition, fleet operation, restore operation, and security scan delete helpers; regenerated the code-health inventory, reducing dead-code candidates from 76 to 67.
- Removed a fourth SQL dead-code batch from canonical query files and the CRD mirror sqlc shim: unused single-row CRD mirror getters for ingress classes, GatewayClasses, NetworkPolicies, ResourceQuotas, and LimitRanges; regenerated the code-health inventory, reducing dead-code candidates from 67 to 62.
- Removed a fifth SQL dead-code batch from canonical query files and sqlc surfaces: unused logging enabled-by-cluster and pipeline/output join-table helpers; regenerated the code-health inventory, reducing dead-code candidates from 62 to 56.
- Removed a sixth SQL dead-code batch from canonical query files and sqlc surfaces: unused broad-list/count helpers for API tokens, agent connections, API-server allowlists, and cloud credentials; regenerated the code-health inventory, reducing dead-code candidates from 56 to 50.
- Removed a seventh SQL dead-code batch from canonical query files and sqlc surfaces: unused alerting alternate filter/read helpers for active rules, reverse channel lookup, cluster/firing event filters, and active silence reads; regenerated the code-health inventory, reducing dead-code candidates from 50 to 43.
- Removed an eighth SQL dead-code batch from canonical query files and sqlc surfaces: unused catalog/tool alternate lookups, category filters, tool update helpers, chart update helpers, and installed-chart-by-tool listing; regenerated the code-health inventory, reducing dead-code candidates from 43 to 36.
- Removed a ninth SQL dead-code batch from canonical query files and sqlc surfaces: unused backup/restore alternate status/list helpers and older security/pod-security status helpers superseded by current applied/report/failure-with-message paths; regenerated the code-health inventory, reducing dead-code candidates from 36 to 26.
- Removed a tenth SQL dead-code batch from canonical query files and sqlc surfaces: unused non-RBAC helpers for Argo applications/proxy tokens, backup drill inserts, cluster template reverse lookups, cluster token revocation, Fleet failed-target counts, GitOps registration reads, monitoring backend lists, network-policy application lists, quota setters, and Vault token-expiry updates; regenerated the code-health inventory, reducing dead-code candidates from 26 to 12.
- Removed the final SQL dead-code batch from canonical query files and sqlc surfaces: legacy RBAC role-by-name, binding-by-user/group, and per-user role join helpers that were superseded by the active `ListUserBindingsWithRoles` authorization query; regenerated the code-health inventory, reducing dead-code candidates from 12 to 0.
- Completed all-row route security metadata in `docs/generated-route-inventory.json`: every one of the 405 mounted route rows now carries handler owner, surface, auth posture, RBAC posture, CSRF posture, audit posture, and representative test evidence. Added `TestRouteInventoryEntriesHaveCompleteSecurityPosture` and wired the assertion into route inventory generation so bare route rows cannot regress silently.
- Closed the legacy Fleet-named multi-cluster operation audit registry gap for create, abort, pause, resume, and retry-failed actions: handler tests now capture and assert `fleet.operation.created`, `fleet.operation.aborted`, `fleet.operation.paused`, `fleet.operation.resumed`, and `fleet.operation.retry_failed`, and the high-risk route registry / generated route inventory now reference those concrete events.
- Closed the resource and node high-risk audit registry batch: cordon/uncordon now emit explicit `cluster.node.cordoned` / `cluster.node.uncordoned` audit rows, existing resource create/update/delete and node drain/metadata/taint audit events now have direct handler-test assertions, and `docs/security-sensitive-routes.json` / generated route inventory now reference the concrete event names. Remaining high-risk registry entries whose audit field says coverage remains open dropped from 74 to 60.
- Closed the RBAC global-role and role-binding audit registry batch: handler tests now assert `role.create`, `role.update`, and `role.delete` on `global_role`, plus `binding.create` / `binding.delete` on global, cluster, and project role binding resources. Remaining high-risk registry entries whose audit field says coverage remains open dropped from 60 to 51.
- Closed the generic SSO provider audit registry batch: existing create/delete handler tests now wire the audit querier and assert `sso.provider.create` / `sso.provider.delete` rows without exposing provider secrets in audit detail. Remaining high-risk registry entries whose audit field says coverage remains open dropped from 51 to 49.
- Closed the cluster registry audit registry batch: the legacy single-registry routes and the multi-registry create/update/delete/test routes now have direct handler-test assertions for `cluster.registry.updated`, `cluster.registry.deleted`, `cluster.registry.created`, and `cluster.registry.tested`, with password and CA material excluded from audit detail. Remaining high-risk registry entries whose audit field says coverage remains open dropped from 49 to 43.
- Closed the Dex configuration audit registry batch: connector create/update/delete, settings update, rendered-config apply, and register-as-SSO tests now capture `dex.connector.*`, `dex.settings.update`, `dex.config.apply`, and `dex.register_sso` rows while keeping connector and client secrets out of audit detail. Remaining high-risk registry entries whose audit field says coverage remains open dropped from 43 to 37.
- Closed the Vault connection audit registry batch: create/update/delete/test handler tests now capture `admin.vault_connection.created`, `.updated`, `.deleted`, and `.tested` rows while keeping token/auth material out of audit detail. Remaining high-risk registry entries whose audit field says coverage remains open dropped from 37 to 33.
- Closed the singleton policy/network audit registry batch: API server allow-list PUT now has test-backed `cluster.apiserver_allowlist.updated` coverage with hashed CIDR evidence, network policy application create asserts `cluster.network_policy.applied`, and service-mesh policy validation now emits and tests `cluster.service_mesh.policy_validated` metadata without storing policy bodies. Remaining high-risk registry entries whose audit field says coverage remains open dropped from 33 to 30.
- Closed the workload mutation audit registry batch: scale, restart, and delete handler tests now create durable workload operations and assert `workload.scale`, `workload.restart`, and `workload.delete` audit rows for the target workload. Remaining high-risk registry entries whose audit field says coverage remains open dropped from 30 to 27.
- Closed the project Vault/policy/catalog audit registry batch: project default Vault assignment, project policy PATCH, project-owned catalog creation, and project catalog subscription tests now assert `project.default_vault_connection.set`, `project.update_policy`, `project.catalog.owned_created`, and `project.catalog.subscribed_foreign`. Remaining high-risk registry entries whose audit field says coverage remains open dropped from 27 to 23.
- Closed the project CRUD/namespace audit registry batch: create, update, delete, add-namespace, and remove-namespace handlers now have route-level test assertions for `project.create`, `project.update`, `project.delete`, `project.add_namespace`, and `project.remove_namespace`. Remaining high-risk registry entries whose audit field says coverage remains open dropped from 23 to 18.
- Closed the cluster group audit registry batch: create and bulk-move handler tests now capture `admin.cluster_group.created` and per-cluster `admin.cluster_group.moved_cluster` audit rows. Remaining high-risk registry entries whose audit field says coverage remains open dropped from 18 to 16.
- Closed the cluster snapshot/template audit registry batch: snapshot create, restore request, schedule create, template apply, and template detach tests now assert `cluster.snapshot.created`, `cluster.snapshot.restore_requested`, `cluster.snapshot.schedule_created`, `cluster.template_applied`, and `cluster.template_detached`. Remaining high-risk registry entries whose audit field says coverage remains open dropped from 16 to 11.
- Closed the catalog installed audit registry batch: installed chart create, upgrade, and delete tests now assert `catalog.installation.create`, `catalog.installation.upgrade`, and `catalog.installation.delete` with operation IDs in audit detail. Remaining high-risk registry entries whose audit field says coverage remains open dropped from 11 to 8.
- Closed the backup audit registry batch and completed the high-risk audit coverage pass: storage create/update/delete, ad-hoc backup create/delete, restore create, schedule create, and schedule trigger tests now assert `backup.storage.*`, `backup.create`, `backup.delete`, `backup.restore.create`, and `backup.schedule.*` rows without recording storage credentials. Remaining high-risk registry entries whose audit field says coverage remains open dropped from 8 to 0.
- Added `scripts/operation-task-inventory.mjs` and `docs/rancher-quality-phase0-operation-task-inventory.md`, then wired it into `npm run code-health` so CI records worker task types, handlers, schedules, enqueue points, task-outbox producers, durable operation tables, and operation idempotency helper coverage. The generated pass also fixed missed tunnel-queue scheduling/handler registration for `mesh:detect` and `cluster_groups:refresh_metrics`, disabled the broken `compliance:export` async branch until it has durable worker/status/output state, closed `AgentLifecycleOperation` idempotency with an `Idempotency-Key` aware create helper, and added enforced direct-enqueue classifications through `docs/direct-enqueue-classifications.json`.
- Regenerated SQLC from the canonical query files with `github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1`, removed stale hand-written/generated duplicate SQLC shims, fixed generated API drift in handlers/workers/tests, added `scripts/check-sqlc-generated.sh`, pinned `make sqlc-generate` to the same SQLC version, and wired `sqlc-check` into PR validation so query/generated mismatches fail CI.
- Added `internal/kubeutil` as the shared Kubernetes helper package for active duplicate call sites: canonical Argo/ApplicationSet and ConfigMap GVK/GVR metadata, namespaced-name helpers, unstructured constructors/spec hashing, and server-side apply path/header construction. Migrated CRD ApplicationSet reconciliation, built-in Argo baseline/self-management metadata, and project/network-policy/cloud-credential/registry SSA workers to the shared helpers with focused package tests.
- Completed the shared Kubernetes helper pass by adding `kubeutil` discovery/RESTMapper constructors and delete-option helpers, then migrating agent RBAC garbage collection, agent managed-side decommission cleanup, and server-side Argo orphan-secret cleanup to `kubeutil.DeleteOptions()`.
- Added shared audit request-event builders in `internal/audit` for HTTP method/path/user-agent/status/request metadata and UUID actor conversion, then migrated handler explicit audit rows, mutating-route audit middleware, and tunnel pod exec/log stream audit rows so only domain-specific action/detail assembly remains local.
- Added `internal/operationstate` for the shared status vocabulary used by the generic `*_operations` tables (`pending`, `running`, `completed`, `failed`, `superseded`) plus retryable/failure/queue-depth helpers. Handler operation constants now alias the shared package and the generic operation runner delegates status predicates to it.
- Added `internal/agentlifecycle` for agent lifecycle operation types/statuses shared by the Agent Fleet API and tunnel result handler, aliased Fleet operation/target common states to `internal/operationstate`, and removed remaining raw decommission phase status comparisons/audit writes in favor of the existing decommission constants.
- Removed the legacy redirect-only `/dashboard/clusters/[id]/provisioning` route and updated registration/surface inventory docs so `/dashboard/clusters/[id]/adoption` is the only cluster adoption timeline URL.
- Consolidated duplicate opaque-token hashing paths onto `auth.HashOpaqueToken`: stream API-token authorization and password-reset token persistence/lookup now use the same helper as Argo proxy tokens, registration tokens, and durable agent tokens. Stream-ticket and TOTP recovery-code hashes remain separate because they have distinct normalization and lifecycle contracts.
- Added `internal/envconfig` to centralize Viper environment bootstrapping, env binding, and default registration. Migrated the management-plane config loader and agent config loader onto it while preserving their separate structs, prefixes, defaults, and required-field validation.
- Added `internal/httpclient` for bounded outbound HTTP defaults, then replaced unsafe `http.DefaultClient` fallbacks in worker runtime, telemetry, SIEM, OIDC discovery, SSO user-info, local Argo bootstrap login, EKS/GKE API-server allow-list providers, and backup storage connectivity probes. Catalog repository fetches now have a 30 second per-repository context and notification webhooks have a 10 second per-post context, with focused tests covering the new defaults.
- Added a checked-in GolangCI-Lint v2 configuration, upgraded vulnerable Go dependencies, migrated websocket code from deprecated `nhooyr.io/websocket` to `github.com/coder/websocket`, removed stale dead code, fixed unchecked errors and staticcheck issues across CLI, handlers, workers, tunnel, agent, tests, and load-test tooling, and drove `golangci-lint run ./...` to zero issues.
- Regenerated `docs/rancher-quality-phase0-operation-task-inventory.md` after the lint cleanup so `npm run code-health` verifies the current worker/task inventory.
- Added shared frontend toast wrappers around Sonner, migrated direct page/dialog/hook toast calls through the wrapper, kept API-shaped error extraction in one formatter, and promoted direct `sonner` imports / `toast.*` calls outside the wrapper into a code-health hard gate.
- Removed unused global CSS component utilities (`glass-card`, `glow-*`, `table-row-hover`, `shimmer`) plus the unused Tailwind shimmer animation/background tokens after confirming no frontend references remain.
- Verified the frontend has a single Playwright smoke spec and no duplicated fixture modules to consolidate.
- Added frontend OpenAPI contract coverage proving `docs/openapi.yaml` stays byte-for-byte synchronized with the embedded handler asset while retaining critical documented-path assertions.
- Promoted duplicate frontend API `Request` / `WriteRequest` / `Response` shape names into the code-health hard gates; the generated inventory now verifies API clients stay under `frontend/src/lib/api*` and reports no duplicate frontend API shape definitions.
- Consolidated the remaining local generic `StatusBadge` usage onto `components/ui/status-badge.tsx`, renamed the template-specific badge to a domain-specific name, and added a code-health hard gate against new local generic `StatusBadge` components.
- Added `frontend/src/lib/auth/session.ts` as the single owner for the browser session cookie name and legacy token-storage cleanup; the API client, auth store, and middleware now use those shared constants/helpers, with tests and a code-health gate preventing literal key drift.
- Added a shared `components/ui/modal-shell.tsx` primitive with Escape/backdrop close behavior and migrated the Account Security 2FA dialogs off their page-local modal shell, with focused component tests.
- Added a deterministic OpenAPI component type generator (`scripts/generate-openapi-types.mjs`) that emits raw wire-shape types to `frontend/src/types/openapi.generated.ts`, plus `npm run openapi:types:check` in frontend code-health and API contract coverage proving every documented schema has a generated alias.
- Added shared `components/ui/table.tsx` primitives, migrated all page/raw table markup and the existing searchable `DataTable` onto those primitives, added focused table coverage, and promoted raw native table tags outside the shared primitive into a code-health hard gate.
- Added shared `components/ui/overlay-shell.tsx` and `drawer-shell.tsx`, expanded `modal-shell.tsx` with subtitle/title-icon/footer slots, added dialog roles plus focus entry/trap/restore behavior, migrated component and dashboard route overlays onto the shared shells, and promoted raw fixed full-screen overlays/backdrops outside the shared shells into a code-health hard gate.
- Expanded `components/ui/empty-state.tsx` into a shared state-panel system with empty, loading, error, permission, disconnected, and stale variants; added coverage for the new wrappers and migrated cluster-template and quota-usage one-off state blocks onto the shared primitives.
- Added shared `components/ui/action-button.tsx` for normal, primary, destructive, async-loading, dry-run/save, and disabled-with-reason actions; migrated confirmation dialog actions and YAML editor dry-run/save controls onto it, and extended action-menu items with disabled reasons.
- Added shared `components/ui/operation-timeline.tsx` for phase headers, step status icons, progress, error messages, empty states, and action slots; migrated the cluster registration/adoption timeline onto it while keeping the registration-specific SSE/poll/retry logic local.

Implemented on 2026-06-13:

- Added `docs/rancher-quality-phase0-inventory.md` as the first current-state inventory for Phase 0, covering route groups, high-risk routes, proxy surfaces, UI footprint, DB footprint, CRDs, worker automation, security posture, duplicate-code candidates, and remaining Phase 0 gaps.
- Added `docs/security-sensitive-routes.json` as a checked-in high-risk route registry for the currently route-tested long-lived and proxy surfaces.
- Updated `internal/server/routes_security_test.go` so `TestHighRiskRoutesDenyUnauthenticatedRequests` loads the checked-in high-risk route registry and verifies each sample path denies unauthenticated requests.
- Hardened the agent service proxy to defensively drop auth, cookie, host, proxy, forwarded, impersonation, and hop-by-hop request headers before forwarding to in-cluster services.
- Added `internal/agent/service_proxy_test.go` coverage proving the agent service proxy strips caller/security-sensitive headers while preserving legitimate service headers.
- Expanded the high-risk route registry to include exec WebSocket, logs WebSocket, Argo CD UI proxy root/suffix, and internal K8s/Helm cross-pod PSK routes.
- Updated the registry-backed route security test to support expected unauthenticated status codes, including `403` for server-internal PSK routes.
- Added `internal/tunnel/internal_k8s_test.go` coverage for disabled and bad-PSK internal K8s cross-pod requests.
- Expanded the registry again to include the session-scoped kubectl shell WebSocket and public token-gated registration manifest route.
- Added route-security fakes so the real server router mounts kubectl shell and registration manifest routes during registry-backed unauthenticated behavior tests.
- Reframed the cluster detail provisioning surface as an Adoption tab, added a redirect-only legacy `/provisioning` alias, and updated visible registration timeline copy so the product stays clear that Astronomer adopts existing clusters instead of creating them.
- Added `docs/rancher-quality-phase0-surface-inventory.md` with concrete dashboard page, frontend API client, SQL query, worker task, and high-risk backend surface lists plus required quality/security checks for each surface family.
- Expanded `docs/security-sensitive-routes.json` to 30 entries by adding self-service account security routes, TOTP mutators, API token creation/revocation, kubectl shell REST lifecycle routes, admin shell-history reads, and admin user unlock/force-logout/TOTP-disable/password-reset routes.
- Updated the registry-backed route-security harness to mount Auth, TOTP, and Resource handlers so those account and admin routes are registered and denied before authentication.
- Expanded `docs/security-sensitive-routes.json` to 44 entries by adding direct Kubernetes resource create/update/delete routes and node cordon, uncordon, drain, label, annotation, and taint mutation routes.
- Expanded `docs/security-sensitive-routes.json` to 74 entries by adding credential and secret-bearing configuration mutators for cluster registries, cloud credentials, Vault, SMTP, webhooks, SSO providers, and Dex connectors/settings.
- Updated the registry-backed route-security harness to mount cluster registry, cloud credential, Vault, SMTP, webhook, and Dex handlers so those credential routes are registered and denied before authentication.
- Expanded `docs/security-sensitive-routes.json` to 77 entries by adding public token-gated password reset request/complete and TOTP challenge verification routes with their expected unauthenticated malformed-request responses.
- Expanded `docs/security-sensitive-routes.json` to 120 entries by adding project, RBAC, backup/restore, snapshot, project catalog, cluster group, fleet-named operation, allow-list, catalog app, cluster template, network policy, workload, and service mesh operation mutators.
- Updated the registry-backed route-security harness to mount project, RBAC, backup, snapshot, catalog, project catalog, cluster group, fleet operation, allow-list, cluster template, network policy, workload, and service mesh handlers so those operation routes are registered and denied before authentication.
- Added `TestRouteInventoryCanBeGenerated`, a `chi.Walk` route inventory generator that can write `docs/generated-route-inventory.json` when run with `ASTRONOMER_WRITE_ROUTE_INVENTORY=1`.
- Generated and checked in `docs/generated-route-inventory.json` from the current router; later refreshes now track 405 mounted route entries with complete security-posture metadata.
- Added `docs/route-risk-classifications.json` to classify mutating routes outside the high-risk route registry; after promotion work this is now limited to the intentional public auth session-flow exception.
- Added `TestMutatingRoutesHaveSecurityClassification`, which walks the real router and fails when any new mutating route is neither covered by `docs/security-sensitive-routes.json` nor classified in `docs/route-risk-classifications.json`.
- Expanded `docs/security-sensitive-routes.json` to 121 entries by adding the remotedialer connect tunnel route.
- Expanded `docs/security-sensitive-routes.json` past 200 entries by promoting candidate high-risk, secondary, and alias mutating routes for cluster lifecycle, user management, RBAC, cluster templates, network policy templates, catalog repositories, backup aliases, cluster groups, projects, workloads, and legacy Fleet-named operations; current registry size is tracked in the Phase 0 inventories.
- Reduced `docs/route-risk-classifications.json` to one intentionally public auth session-flow rule; all other generated mutating routes are now covered by the high-risk registry.
- Hardened user-management routes so user create/update/delete/reset require `users:*` RBAC and admin API-token scope.
- Hardened legacy settings/SSO mutators so platform settings and SSO provider writes require explicit RBAC and admin API-token scope while preserving public SSO provider listing for the login page.
- Added `TestUserManagementRoutesRequireUsersRBACAndAdminScope` and `TestLegacySettingsMutatorsRequireRBACAndAdminScope`.
- Added `TestBrowserCookieMutatingRoutesRequireCSRF`, which checks every registry-marked browser-cookie mutating route rejects session-cookie requests without the double-submit CSRF token.
- Hardened the service proxy browser boundary so unsafe upstream response headers such as `Set-Cookie`, hop-by-hop headers, auth challenges, `Content-Length`, and `Clear-Site-Data` are not forwarded to the dashboard response.
- Added `TestServiceProxySanitizesResponseHeaders`.
- Hardened the generic Kubernetes proxy response boundary so one-shot, watch-stream, and cross-pod fallback responses strip unsafe headers such as `Set-Cookie`, auth challenges, hop-by-hop headers, `Content-Length`, and `Clear-Site-Data`.
- Added `TestWriteK8sResponseSanitizesUnsafeHeaders`, `TestHandleK8sProxyStreamingWatchSanitizesResponseHeaders`, and `TestForwardToOwnerPodSanitizesResponseHeaders`.
- Hardened the Argo CD UI/API proxy response boundary so unsafe upstream headers are stripped and only `argocd.token` cookies are returned, scoped host-only to `/argocd`, `HttpOnly`, `SameSite=Lax`, and `Secure` when the external request is HTTPS.
- Added selective Argo CD UI proxy audit events for document opens and mutating proxied API calls, wired the production proxy to the audit writer, and covered it with `TestArgoCDUIProxyAuditsDocumentAndMutatingRequests`.
- Added `TestArgoCDUIProxySanitizesResponseHeadersAndCookies`, which also proves the proxy strips Astronomer bearer/session credentials before forwarding to Argo CD.
- Updated PR validation so checked-in route metadata JSON is parsed and the focused route security gate runs the unauthenticated high-risk registry, mutating-route classification, browser CSRF, and route inventory tests.
- Enriched `docs/generated-route-inventory.json` so route rows matched by the high-risk registry include auth, RBAC, CSRF, audit, surface, and test metadata, and rows matched by `docs/route-risk-classifications.json` include classification reason metadata.
- Updated `docs/control-plane-state-contract.md` and `docs/crd-api.md` with planned CRD ownership for `ClusterBaseline`, `ComponentBundle`, `AgentProfile`, and `GitOpsTarget`, including controller expectations and versioning policy.
- Added Argo managed-cluster label collision validation in the API and registration dialog so user-supplied labels cannot overwrite `astronomer.io/*` or `argocd.argoproj.io/*` labels used for ApplicationSet targeting and Argo cluster Secret identity.
- Hardened local Argo managed-cluster refresh so legacy stored label rows cannot replay reserved Astronomer/Argo labels into upstream cluster registration.
- Standardized the first Argo cluster Secret selector label contract across API registration, explicit label refresh, worker label refresh, and local self-managed Argo bootstrap:
  - always stamps `astronomer.io/managed-by`, `astronomer.io/cluster-id`, `astronomer.io/cluster-name`, and `astronomer.io/is-local`;
  - conditionally stamps `astronomer.io/environment`, `astronomer.io/region`, `astronomer.io/provider`, and `astronomer.io/distribution` from the cluster row;
  - always stamps `astronomer.io/agent-privilege-profile` from the normalized reserved cluster annotation;
  - conditionally stamps sanitized `astronomer.io/agent-version` and `astronomer.io/kubernetes-version` from the cluster row;
  - keeps sanitized `astronomer.io/label-*` projections for user labels;
  - stamps durable project labels from Postgres project membership: singular `astronomer.io/project` / `astronomer.io/project-id` labels for single-project clusters and `astronomer.io/project.<name>=true` / `astronomer.io/project-id.<uuid>=true` membership labels for every project.
- Added focused worker coverage proving the existing 5-minute Argo auto-adoption sweep re-upserts eligible clusters into Argo CD with `upsert=true`, the proxy-based destination URL, refreshed proxy token, and the standardized label contract. The repair path also refreshes labels when rebuilding a missing `argocd_managed_clusters` row from an Astronomer-owned Argo cluster Secret.
- Added per-cluster Argo repair timeline visibility for periodic repair cases that have concrete drift evidence before the sweep runs:
  - stale Astronomer-owned labels detected before an Argo upsert;
  - missing Argo cluster Secret detected before an Argo upsert;
  - missing `argocd_managed_clusters` row rebuilt from an Astronomer-owned Argo cluster Secret.
- Expanded the built-in Argo-managed platform baseline catalog with `ingress-nginx`, kept the cluster API/UI baseline ownership catalog in sync, added explicit ApplicationSet target labels for adopted-cluster baseline fan-out, and ensured the legacy Helm baseline skip set treats `ingress-nginx` as Argo-owned when Argo baseline management is enabled.
- Hardened user-created ApplicationSet cluster generators so any cluster generator, including nested matrix generators, must select `astronomer.io/managed-by=astronomer`; the wizard now includes that selector by default.
- Added a cached Argo CD drift summary to the cluster detail API/UI using managed-cluster Application targets, including app count, sync counts, health counts, last sync, and highest-severity drift message.
- Added first-pass sync-wave annotations to platform-owned baseline Applications generated by the built-in ApplicationSets so CRD/operator components, ingress, observability, logging, and scanner components have explicit ordering.
- Added an explicit per-cluster `argocd_registration_repair_blocked` timeline event when periodic Argo repair detects managed-cluster drift but cannot mint or use the required cluster proxy credential material.
- Added guarded ApplicationSet wizard selector presets for all adopted clusters, environment-scoped clusters, sanitized user-label scoped clusters, and canary clusters; each preset includes `astronomer.io/managed-by=astronomer`.
- Added Argo orphan cluster Secret accounting to the periodic repair sweep and records the orphan/decommissioned/invalid/unattributed counts in repair job state metadata.
- Added agent heartbeat schema versioning at protocol version `1`; agents emit it, tunnel health persistence stores it in heartbeat conditions, and live heartbeat events publish it for UI/API consumers.
- Added a first-pass Agent Fleet compatibility matrix with server version, minimum compatible/supported agent versions, supported/deprecated/blocked/unknown agent status, summary counts, UI pills, upgrade recommendations for deprecated pre-1.0 agents, and tunnel rejection for agents below the minimum compatible version.
- Verified and test-hardened reconnect behavior already present in the agent/tunnel stack: exponential backoff, jitter/initial spread, server-side session replacement, stale-session cleanup, and reconnect-storm metrics.
- Added an Agent Fleet self-test API and UI action that converts diagnostics into deterministic pass/warn/fail checks for tunnel connection, heartbeat freshness, ping freshness, privilege profile, compatibility, live diagnostics, and cluster conditions.
- Added self-test handler coverage for healthy connected agents and disconnected blocked agents, exposed Agent Fleet routes in the route-security fixture, registered Agent Fleet POST routes in `docs/security-sensitive-routes.json`, regenerated route inventory, and documented the self-test endpoint in OpenAPI.
- Added `astro cluster self-test <id>` with human-readable and JSON output so the same Agent Fleet self-test can be run from the CLI.
- Expanded Agent Fleet diagnostics bundles with Argo registration/proxy target state plus live RBAC self-review, Kubernetes API readyz, and clock-skew checks surfaced as named pass/warn/fail diagnostics.
- Added the agent capability contract to heartbeat schema version `2`: agent build SHA, privilege profile, available API groups, enabled/denied features, last successful action, and degraded reasons are emitted by the agent, persisted in heartbeat health conditions, and published in live heartbeat events.
- Expanded agent upgrade plans with canary cluster IDs, batch size, max unavailable, rollback image, explicit preflight checks, post-upgrade health checks, API schema coverage, UI rendering, and focused handler tests.
- Added durable agent-token revocation support with `cluster_agent_tokens.revoked_at`, generated query coverage, reconnect rejection through the existing token lookup path, and migration content tests.
- Hardened agent diagnostics bundle downloads with a final recursive redaction pass covering sensitive keys, bearer/authorization/cookie/password/token assignment patterns, credential-bearing URLs, kubeconfig-shaped values, and private keys, with focused handler tests.
- Expanded agent install privilege profiles beyond `viewer`, `operator`, and `admin` by adding `namespace-viewer`, `namespace-operator`, and `custom` profiles; namespace profiles render namespaced `RoleBinding`s and `custom` renders no default Kubernetes permissions.
- Updated agent heartbeat and Agent Fleet capability inference for namespace-scoped and custom profiles, including explicit disabled/unknown capability reporting.
- Added explicit Agent Fleet offline-behavior metadata and UI rendering so disconnected clusters show last-known state, stale/offline status, queue-safe operations, and blocked live/in-cluster actions.
- Added a frontend ESLint flat config for ESLint 9/Next 16, kept conventional hook-ordering checks enabled, deferred noisy React Compiler advisory rules, and fixed the image-scans page conditional-hook violation.
- Added Helm-managed `ClusterBaseline`, `ComponentBundle`, `AgentProfile`, and `GitOpsTarget` CRD definitions with status subresources, standard condition schema, finalizer annotations, printer columns, chart RBAC, runtime scheme registration, defensive deepcopy implementations, static schema tests, and Helm render validation.
- Added first-pass validation/status reconcilers for `ClusterBaseline`, `ComponentBundle`, `AgentProfile`, and `GitOpsTarget`. These controllers install/remove current finalizers, validate the safe subset of each spec, and write `Accepted`, `Reconciled`, and `Ready` conditions; durable operation rows remain pending controller work.
- Expanded the Phase 1 CRD API surface with the missing declarative fields needed for the Argo writer and agent projection slices: `ClusterBaseline.spec.profileName`, `ComponentBundle.spec.defaultNamespace`, `ComponentBundle.spec.healthChecks`, runtime `source.secretRefs`, `AgentProfile.spec.allowedRules`, `AgentProfile.spec.hostAccess`, `AgentProfile.spec.networkEgress`, `GitOpsTarget.spec.projectSelector`, `GitOpsTarget.spec.bundleRef`, and `GitOpsTarget.spec.syncPolicy`. Added validation/deepcopy/schema coverage and explicit CRD-controller RBAC for Argo `ApplicationSet` resources.
- Added the first `GitOpsTarget` Argo writer: supported targets now create or repair Argo CD `ApplicationSet` resources in the configured Argo namespace from direct `sourceRepo`/`path` specs, resolvable `ComponentBundle` references, or governed same-namespace ConfigMap `templateRef` catalog entries, enforce the Astronomer-managed cluster selector, enforce `projectSelector` against same-namespace Projects for explicit cluster refs and durable project labels, roll aggregate child `Application` sync/health/count plus child Application/resource details into status, refuse to overwrite same-name ApplicationSets without matching CRD ownership labels/annotations, surface apply/status-read failures in status, and delete generated ApplicationSets before removing the `GitOpsTarget` finalizer. Durable operation rows remain open.
- Added the first `ClusterBaseline` Argo writer: baselines now resolve enabled same-namespace `ComponentBundle` references, create or repair one Argo CD `ApplicationSet` per bundle, enforce referenced ComponentBundle version pins, enforce the Astronomer-managed cluster selector boundary, support Helm parameter overrides and automated prune/self-heal policy, report generated ApplicationSet names plus aggregate child `Application` sync/health/count and child Application/resource details in status, refuse to overwrite same-name ApplicationSets without matching CRD ownership labels/annotations, delete stale generated ApplicationSets when bundle refs are removed, and clean up generated ApplicationSets before removing the `ClusterBaseline` finalizer. Durable operation rows remain open.
- Added `Cluster.spec.agent.profileRef` projection: Cluster CRDs can now reference a same-namespace `AgentProfile`; the Cluster reconciler validates and resolves the profile before DB sync, stamps `management.astronomer.io/agent-profile-ref`, and writes the existing `astronomer.io/agent-privilege-profile` annotation consumed by registration manifest rendering. The Cluster CRD schema now accepts `namespace-viewer`, `namespace-operator`, and `custom` profiles as well as `viewer`, `operator`, and `admin`.
- Added ComponentBundle version-pin enforcement for Argo writers: `ClusterBaseline` and `GitOpsTarget` bundle refs now fail closed when `version` does not match the referenced `ComponentBundle.spec.version`, while Argo source revisions are taken from `ComponentBundle.spec.source.targetRevision`.
- Added governed `GitOpsTarget.applicationSet.templateRef` resolution: template refs now resolve same-namespace ConfigMaps labeled `management.astronomer.io/gitops-template=true`, parse `data.template.json` into a constrained Argo source/project/namespace shape, reject inline `source.secretRefs`, and let target parameters override the destination namespace.
- Added richer `AgentProfile` install metadata projection: referenced profiles now validate and stamp agent image, ServiceAccount name, and pod labels into reserved cluster annotations, and the registration manifest renderer uses those annotations to render the agent Deployment, ServiceAccount, RoleBinding subject, and pod template labels.
- Added `AgentProfile` capability enforcement: built-in profiles now reject capability claims and `allowedRules` broader than their rendered RBAC boundary, while `custom` profiles can claim externally managed capabilities only when their declared rules imply the matching resource/verb.
- Added ApplicationSet spec drift fingerprints: generated `ClusterBaseline` and `GitOpsTarget` ApplicationSets now carry `management.astronomer.io/desired-spec-sha1`, repair owned out-of-band spec changes, and surface `ApplicationSetDriftRepaired` in status conditions when drift was corrected.
- Hardened `ComponentBundle` reference and values validation: `valuesSchemaRef` is constrained to same-namespace ConfigMap-style `name` or `name/key` references, `secretRefs` are same-namespace with Kubernetes-valid names/keys, ClusterBaseline override values are validated against referenced schemas before ApplicationSet generation, and generated ApplicationSet sources continue to exclude secret refs.
- Added a CRD finalizer recovery runbook covering stuck deletes, generated Argo ApplicationSet verification, preferred recovery, and last-resort manual finalizer removal.
- Added `GitOpsTarget.projectSelector` enforcement against same-namespace `Project` CRs: selected Projects must exist, explicit `selector.clusterRefs` must stay inside selected Projects' `spec.clusters`, and label-only selectors must carry a durable project label or degrade instead of generating a broad cross-tenant ApplicationSet.
- Added CRD finalizer timeout status behavior: blocked `Cluster`, `Project`, `ClusterBaseline`, and `GitOpsTarget` deletions now retain their finalizers after the 15-minute timeout while surfacing `phase: DeletingTimedOut` and `FinalizerTimeout` conditions, with focused controller tests and runbook/API documentation.
- Added shared Argo managed-cluster label projection for API registration, explicit refresh, worker repair, and local self-managed Argo bootstrap; periodic repair now patches stale project labels and CRD projectSelector enforcement accepts project membership labels for multi-project cluster targeting.
- Added sanitized Argo managed-cluster version labels for agent and Kubernetes versions so ApplicationSet selectors and repair jobs can target upgrade cohorts without reading Postgres.
- Added governed multi-version `ComponentBundle` catalogs: `spec.versions[]` now validates nested source/schema/secret/capability/health/upgrade policy entries, `status.availableVersions` reports resolvable pins, and both `ClusterBaseline` and `GitOpsTarget` fail closed on unknown versions while materializing the selected version's source, namespace, and values schema.
- Added envtest-backed CRD controller integration coverage for all six management CRDs, including real API status subresources, finalizer updates, DB-backed `Cluster` / `Project` fakes, and generated Argo `ApplicationSet` creation from a versioned bundle catalog. The test skips locally unless Kubernetes envtest assets are configured.
- Added Argo application resource drift counters: cached Argo Application rows now store nonnegative created/changed/pruned resource counts, sync/poll refreshes derive those counts from upstream Application `status.resources`, cluster detail drift summaries roll them up, OpenAPI documents them, and the cluster GitOps ownership panel renders resource-level drift when present.
- Added detailed child Argo Application status to `ClusterBaseline` and `GitOpsTarget`: status now includes generated Application name/namespace, sync status, health, revision, operation phase/message, and bounded child resource status details.
- Codified built-in Argo baseline sync-wave standards as named phases (`namespaces`, `crds`, `operators`, `policies`, `workloads`, `health-checks`), stamped the generated ApplicationSet/Application templates with `astronomer.io/sync-phase`, and added focused tests covering every shipped baseline component plus the reserved policy phase.
- Added a cached Argo baseline orphan report for each Argo CD instance, detecting Astronomer-generated baseline Applications whose cached destination no longer matches any managed Argo cluster registration, documenting it in OpenAPI, and surfacing the count/details on the Argo instance overview.
- Added the conservative `ClusterBaseline` per-cluster override model: literal `values` render as UI/API-managed Helm parameters, Git `valuesFrom` entries render as Argo Helm `valueFiles`, Secret/ConfigMap `valuesFrom` entries are validated as same-namespace governance references without inlining contents, and override precedence is documented as bundle defaults, Git value files, then literal Helm parameters. Durable product audit history for CRD override changes remains open.
- Added Argo Application rollback actions to the history tab: operators can preview a rollback with a dry-run sync to a prior revision, confirm an actual rollback to that revision, and both paths reuse the existing permission-gated, audited Argo sync operation row.
- Hardened Argo baseline ownership decisions so unsafe replacement is blocked for the local management cluster, replace decisions require a reason, and replacement cannot be recorded until the cluster is registered with Argo CD.
- Expanded the Argo orphan report to inspect live upstream Argo Applications, detect Astronomer-managed Applications whose live destination no longer maps to a managed Argo cluster registration, detect stale baseline ApplicationSet labels/owner references, scope live ApplicationSet ownership checks to known Astronomer baseline ApplicationSets, and surface cache-vs-live findings plus live scan degradation in the UI.
- Added AppProject sync-window support for Argo-native maintenance and blackout windows: the backend now models and validates `syncWindows`, project creation/editing can configure allow/deny windows with selectors, manual override, sync overrun, timezone, and AND matching, the AppProjects table shows configured allow/deny counts, and manual sync-window overrides require a reason that is captured in operation payload, execution events, and audit metadata.
- Added Gatekeeper to the built-in Argo-managed platform baseline as the policy-stack component, using the reserved `policies` sync phase and wave, plus catalog seed rows for Gatekeeper and ingress-nginx so coverage and manual tool flows match the Argo baseline.
- Added explicit durable agent-token audit events: registration-token handoff now records `agent.token.rotated` without storing or auditing plaintext token material, and cluster decommission records a direct `agent.token.revoked` event with removed credential counts.
- Hardened management-plane NetworkPolicy rendering with a namespace default-deny policy, granular external egress CIDR buckets for HTTPS, Postgres, Redis, Kubernetes API, and IdP/LDAP traffic, production overrides that clear broad legacy egress, and Helm render tests for both default-deny and granular egress behavior.
- Hardened chart-managed hook and backup jobs with non-root pod security contexts, dropped capabilities, blocked privilege escalation, read-only root filesystems where possible, tmp `emptyDir` scratch mounts where writable space is required, and Helm render tests that pin the container-security contract.
- Updated the threat model for the current Rancher-grade architecture, including Postgres/Redis/CRD/Argo state split, GitOps baseline ownership, management-plane NetworkPolicy/container posture, backup/restore boundaries, supply-chain inputs, and a PR-facing security checklist; expanded the GitHub PR template to require threat-model, chart hardening, and supply-chain review where relevant.
- Added `docs/kubernetes-proxy-inventory.md`, covering the generic Kubernetes proxy, Argo internal proxy, service proxy, Argo UI proxy, exec/log streams, kubectl shell, remotedialer, internal Helm, and internal K8s cross-pod routes with auth, audit, header/SSRF controls, representative tests, and remaining gaps.
- Added explicit audit events for mutating Argo CD internal Kubernetes proxy calls as `argocd.k8s_proxy.forwarded`, keeping request and response bodies out of audit detail while recording method, path, cluster, object reference, and proxy origin.
- Added route-level regression coverage proving direct `/api/v1/ws/exec/*` and `/api/v1/ws/logs/*` routes reject long-lived JWTs passed in `?token=`, matching the one-use stream-ticket posture documented in the proxy inventory.
- Added direct WebSocket stream-open audit events for `/api/v1/ws/exec/*` and `/api/v1/ws/logs/*` as `pod.exec.opened` and `pod.logs.opened`, preserving actor user ID/auth method and pod/container reference while excluding command input and log output.
- Promoted request IP extraction into the shared server middleware helper and reused it from handler and tunnel audit writers to avoid another copy of the same audit-address parsing logic.
- Confirmed the agent upgrade boundary for the Argo baseline engine: adopted-cluster agents are upgraded through the Agent Fleet lifecycle operation path, not a generic baseline ApplicationSet, because Argo reaches adopted clusters through the agent/proxy credential path. Treating the agent as an Argo-managed baseline chart would create circular recovery failure modes when the agent is disconnected or mid-upgrade.
- Extended supply-chain coverage so PR validation builds, scans, and emits SBOM artifacts for every first-party chart image, including `astronomer-frontend`, while release builds, signs, emits SBOM attestations, and attaches Buildx provenance for `server`, `worker`, `agent`, `migrate`, `shell`, and `frontend`.
- Added `TestForwardingRoutesAreDocumentedInProxyInventory` and wired it into PR validation so new Kubernetes proxy, service proxy, Argo UI/internal proxy, exec/log/shell WebSocket, remotedialer, or internal tunnel forwarding routes must be documented in `docs/kubernetes-proxy-inventory.md`.
- Added focused browser session hardening tests that pin session, refresh, and CSRF cookie `Secure`, `HttpOnly`, `SameSite=Lax`, `Path`, and `MaxAge` behavior, plus logout tests proving browser cookies are cleared while the bearer JTI is revoked.
- Hardened Kubernetes Secret reads so the raw `/k8s/*` proxy requires `secrets:read/list/watch`, the generic Secret resource list requires `secrets:list`, and cross-cluster `resources/search?type=secrets` filters by per-cluster `secrets:list`; all authorized user-facing Secret read/list/search paths now emit `cluster.secret.read` audit events with request metadata and no Secret values.
- Added an RBAC Effective tab that renders the current user's effective permission grants, contributing role bindings, scope targets, and high-risk grant counts from the existing `/rbac/my-permissions/` API, with frontend type-check and lint validation.
- Added a first-class Audit Log UI backed by the dedicated `/api/v1/audit/` API with composable server-side filters for actor, target, action, action class, cluster, project, result, time range, correlation ID, and request ID; added CSV export, row detail inspection, richer audit response fields, focused backend SQL/handler tests, and route-level RBAC tests for both `/api/v1/audit/` and legacy `/api/v1/settings/audit-logs/`.
- Hardened audit-log route access so the dedicated audit list/export/detail routes and the legacy settings audit-log route require explicit `audit_logs:read` or `audit_logs:list` RBAC instead of being available to any authenticated settings reader.
- Added `docs/rbac-permission-contract.md` plus RBAC package contract tests so canonical resources/verbs, UI action mapping, Kubernetes verb mapping, and embedded role-template vocabulary stay aligned.
- Expanded the effective-permissions API/UI with selected cluster/project/namespace context. Responses now mark whether each grant applies to the selected context and warn that namespace context remains advisory until namespace-scoped binding storage exists.
- Added a shared frontend permission-decision helper that returns allow/deny state, required `resource:verb`, scope, granting role bindings when present, and a request-access hint. Cluster network-access, template, registry, and snapshot actions now use canonical `clusters:update` checks instead of duplicated `globalRoles` string matching, and disabled tooltips explain the missing scoped permission plus who to ask for access.
- Split Kubernetes resource-family RBAC consistently across cross-cluster search, generic resource lists, dynamic group/version/kind resource discovery, named resource routes, node inventory, and node maintenance actions. Services/endpoints, ConfigMaps, storage, ingress/Gateway resources, network policies, nodes, pods, workloads, and Secrets now require their dedicated canonical resources instead of silently falling back to `workloads` or `clusters`, with route and search regression tests.
- Added namespace-aware RBAC enforcement prerequisites: the in-process `RoleBinding` model can carry a namespace, the RBAC engine fails namespace-scoped grants closed when the request namespace is missing or different, middleware extracts namespace from route params or `?namespace=`, and effective-permissions responses include namespace sources and context matching. Generic ConfigMap and Secret list routes now have namespace-scoped route regression coverage. DB storage, assignment APIs, and UI authoring for namespace bindings remain open.
- Aligned the node-detail UI with the server's dedicated node RBAC: cordon, uncordon, labels, annotations, taints, and YAML editing now use `nodes:update`, while drain uses `nodes:manage`, with disabled tooltips and handler guards using the shared permission-decision helper.
- Extended raw Kubernetes proxy RBAC beyond the previous `clusters:read/update` fallback: proxy requests now derive canonical resource-family permissions from the Kubernetes path and method for pods, logs, services/endpoints, ingress/Gateway resources, storage, ConfigMaps, Secrets, network policies, workloads, nodes, and safe fallback custom resources. Added route-security regression coverage proving `clusters:*` is no longer enough for known resource families while dedicated permissions pass.
- Aligned the high-traffic cluster resource explorer UI with the server-enforced RBAC split: nodes, namespaces, pods, workloads, Services, Ingresses, NetworkPolicies, Gateway API resources, PV/PVC/StorageClasses, and generic resources now disable create/edit/delete/YAML/log/exec/scale/restart/drain actions with shared permission reasons and confirm-time guards.
- Aligned the per-cluster Apps surface with the catalog handler RBAC contract: chart install now requires `catalog:create`, upgrade requires `catalog:update`, uninstall and failed-install cleanup require `catalog:delete`, and install/upgrade/uninstall confirmation modals keep the same permission guard if access changes while the modal is open.
- Aligned the per-cluster Tools surface with the same catalog RBAC contract: enable/retry/adopt now require `catalog:create`, disable requires `catalog:delete`, the preview and confirmation modals preserve permission-disabled reasons, and the shared confirmation dialog can render an externally supplied disabled reason.
- Completed the support-bundle operational baseline: downloads now run a final recursive redaction pass over every JSON section, redact sensitive pod log lines while streaming, include Argo health/last-sync state, management namespace NetworkPolicy summaries, and ingress/TLS certificate validity summaries without writing raw cert/key bytes, with focused handler tests for redaction and section coverage.
- Promoted dedicated and legacy audit-log read/export routes into the high-risk route registry, regenerated `docs/generated-route-inventory.json` with audit route security metadata, and added a route-inventory regression assertion so `/api/v1/audit/*` coverage cannot silently disappear.
- Extracted diagnostic/support-bundle redaction into shared `internal/redaction`, covering sensitive keys, credential-shaped strings, bearer/cookie/authorization assignments, credential-bearing URLs, kubeconfigs, private keys, sensitive log lines, and byte-count placeholders, with package tests and handler call-site coverage.
- Added `docs/rancher-quality-phase0-system-inventories.md` covering frontend route counts/owners, Postgres migration/query counts, CRD ownership, and worker task families with concrete follow-up gates for automation and CI.
- Added `docs/secret-handling-policy.md`, tying together hash-only tokens, Fernet-encrypted reusable credentials, Vault/external references, Kubernetes Secret boundaries, audit/log/export redaction, CRD/Argo secret rules, backup separation, and a review checklist.
- Completed the listed OpenTelemetry trace coverage by confirming existing API request, DB query, worker task, asynq traceparent, tunnel, and Kubernetes request spans and adding low-cardinality upstream Argo CD client request spans with status/error attributes.
- Added upstream Argo CD client Prometheus request metrics for total requests and latency by method, bounded path family, and status.
- Verified the route security inventory gate: generated route inventory entries carry auth/RBAC/CSRF/audit posture, high-risk registry metadata enriches the mounted routes, and `TestHighRiskRoutesDenyUnauthenticatedRequests` exercises every registry sample path against its expected unauthenticated status.
- Verified proxy-safety coverage across server, handler, and tunnel tests: generic Kubernetes proxy auth/RBAC/audit and header stripping, service proxy allowlist and sensitive-namespace rejection, Argo internal cluster-token scoping, Argo UI proxy response/cookie sanitization, and response-header sanitization are all test-covered and documented in `docs/kubernetes-proxy-inventory.md`.
- Verified the secret/token handling baseline: `docs/secret-handling-policy.md` defines hash-only token and Fernet-encrypted credential rules, auth/registration/handler tests cover token and credential flows, and migration guard tests pin secret-column classification plus registration/agent token hash-only migrations.
- Verified production NetworkPolicy readiness with Helm render tests and chart lint: the chart renders namespace default-deny, explicit component policies, granular external egress CIDR buckets, production values remove broad legacy egress, and bundled DB/cache policies are absent when external Postgres/Redis are required.
- Pinned every first-party Dockerfile external base image to immutable `tag@sha256` references for Go, Alpine, Node, nginx, and migrate bases, and added a deploy-package guard test that rejects unpinned external `FROM` images or `:latest`.
- Added explicit canonical RBAC gates for Kubernetes named/generic resource routes: services, ingress/Gateway, network policy, storage, ConfigMap, Secret, pod, workload, and fallback custom-resource mutations now require the matching product permission instead of authenticated-only access. Added route-security regression coverage and refreshed the high-risk route registry/inventory to remove stale “RBAC audit remains open” metadata for those resource mutation paths.
- Expanded built-in cluster day-2 role templates to match the stricter resource-route RBAC gates: cluster viewer, cluster operator, platform operator, and support engineer now carry non-secret common resource permissions for services, ingress/Gateway resources, storage, ConfigMaps, and NetworkPolicies. Added an RBAC contract regression test so cluster explorer roles keep those common resource-family grants.
- Verified API-token RBAC is already implemented and test-covered: tokens persist scopes, expiration, hash-only material, last-used/last-seen metadata, revocation state, CIDR restrictions, and create/revoke audit events. Service-account identity remains the open part of the service-account/API-token workstream.
- Added `docs/rancher-quality-phase0-quality-backlog.md` to track remaining P0/P1/P2 security, reliability, UI, observability, CRD/GitOps, and code-health work with owners, evidence, and definitions of done.
- Expanded structured log helpers and request/worker logging so HTTP completion logs include explicit `request_id` and worker logs include OTel `trace_id` plus asynq `task_id` when the processor supplies it.
- Added the shared frontend UI quality primitives for the current hardening slice: generated OpenAPI TypeScript validation, low-level table primitives with a raw-table regression gate, focus-managed overlay/modal/drawer shells with raw-overlay regression checks, shared action buttons/action-menu disabled reasons, shared loading/error/permission/disconnected/stale state panels, and a reusable operation timeline. Migrated the cluster registration timeline and high-traffic cluster-template/quota state surfaces onto the new primitives.
- Hardened the cluster GitOps ownership panel against missing or malformed Argo ownership payloads so smoke mocks and partial API failures fall back to cached cluster summary data without crashing, while ownership decision actions remain hidden unless an authoritative ownership response is present.
- Verified the frontend quality slice with `npm run type-check`, `npm run lint`, `npm test -- --runInBand`, `npm run code-health`, `git diff --check`, and `PLAYWRIGHT_CHROMIUM_EXECUTABLE=/snap/bin/chromium npm run test:e2e` because Playwright's bundled Chromium install path is not available on this Ubuntu 26.04 host.
- Expanded the global command/search layer: `Cmd/Ctrl+K` now consistently opens the command palette, `/` focuses the topbar resource search, and the palette includes bounded quick navigation for pages, resource-search shortcuts, clusters, projects, cached Argo CD applications, operational runbook destinations, and cluster registration. Added a shared cached Argo application API helper so palette and Argo instance pages do not duplicate that endpoint.
- Consolidated status-badge behavior by routing Argo sync/health badges through the shared `StatusBadge` primitive and extending the shared status normalization map to cover health, sync/drift, agent, permission, disabled, missing, and compliance-style states.
- Expanded the shared `DataTable` framework with density controls, selected-row bulk-action rendering, stable configurable loading skeleton rows, and focused tests while keeping existing sorting, filtering, column picker, row selection, pagination, and bounded table behavior intact.
- Added shared page layout primitives (`PageShell`, `PageHeader`, `PageSection`) for index/list, detail, settings, operation-history, diagnostics, and resource-explorer compositions without adding card chrome; migrated the ArgoCD instances index onto the new shell and added focused layout tests.
- Extracted the audit row detail drawer into a reusable `ActivityDetailsDrawer` side panel with shared field-grid and JSON-detail rendering, then reused it from the dedicated Audit Log page.
- Added `docs/ui-information-architecture.md` and `docs/ui-design-review-checklist.md` to pin the dashboard IA, navigation/search rules, page-pattern rules, state coverage, responsive checks, accessibility checks, and validation commands expected for Rancher-grade UI work.

## Executive Summary

Astronomer should become a Rancher-quality day-2 Kubernetes management platform for clusters that already exist. It should not clone Rancher's provisioning stack. The product should win by being excellent at adoption, visibility, safe operations, GitOps-based baseline management, policy, observability, security, and a dense operational UI.

The current system has a strong base: a Go management plane, Postgres, Redis/asynq, a Kubernetes agent and tunnel, Argo CD integration, Helm deployment, CRD support, RBAC templates, audit logs, monitoring/logging/security areas, and a Next.js UI. It is not yet Rancher-quality because the experience is not yet uniformly deep, live, auditable, policy-aware, test-hardened, and consistent across every operational surface.

This plan defines the work needed to make Astronomer feel elite:

- No cluster provisioning.
- Adopt existing clusters cleanly.
- Automatically register adopted clusters into the built-in Argo CD.
- Use Argo CD and ApplicationSets as the Fleet replacement.
- Use Postgres for product state and durable intent.
- Use CRDs for declarative Kubernetes-facing APIs and reconciliation surfaces.
- Use controllers and background workers for convergence.
- Use the agent for least-privilege cluster-local execution.
- Make the UI fast, dense, consistent, and safe for repeated daily operations.
- Make every high-risk action auditable, permission-gated, previewable, retryable, and observable.
- Keep code quality high with dead-code removal, duplicate-code controls, security checks, and broad tests.

## Non-Goals

These items are intentionally out of scope:

- Creating clusters.
- Cloud provider node pools.
- RKE2/k3s provisioning.
- EKS, GKE, AKS, vSphere, Harvester, or bare-metal provisioning workflows.
- Node drivers, machine configs, cloud credentials for provisioning, or Cluster API provider ownership.
- Replacing Argo CD with Fleet.
- Rebuilding Kubernetes itself or storing Kubernetes object state in Postgres.

Astronomer manages clusters after they exist.

## Product Target

The target is:

- Rancher-quality Cluster Explorer for adopted clusters.
- Rancher-quality multi-cluster inventory and day-2 operations.
- Rancher-quality RBAC and auditability.
- Rancher-quality operational safety.
- Rancher-quality UI consistency and density.
- Argo CD-native GitOps instead of Fleet.
- Stronger posture around modern cloud-native defaults: CRDs, controllers, ApplicationSets, OIDC, NetworkPolicy, supply-chain security, and least privilege.

The target is not:

- Feature-for-feature Rancher parity.
- Provisioning parity.
- UI mimicry.
- A thin dashboard over kubectl.

## Current Foundation

Existing strengths to preserve:

- Go backend with clear package boundaries under `internal/`.
- Postgres-backed management state.
- Redis/asynq background task execution.
- Kubernetes agent and tunnel concepts.
- Argo CD self-management and managed-cluster registration.
- Helm chart deployment with ingress, cert-manager compatibility, NetworkPolicy, and bootstrap secrets.
- Cluster, project, RBAC, monitoring, logging, backup, security, catalog, GitOps, and extension UI areas.
- CRD management API for clusters and projects.
- OpenAPI documents.
- CI workflows for Go, frontend, Helm, route security, SBOM, and smoke tests.

Known weaknesses to close:

- Some resource pages are not as live, complete, or consistent as Rancher's Explorer.
- GitOps targeting is present but needs deeper product conventions and failure handling.
- CRD coverage is not broad enough for all durable desired-state concerns.
- Agent lifecycle management is not complete enough for fleet-scale operations.
- RBAC needs stronger scope semantics, impersonation safety, and UI previews.
- Security needs repeatable threat-model gates and automated checks around every tunnel/proxy path.
- The UI needs a stronger shared design system, table framework, action patterns, and state consistency.
- Duplicate logic and compatibility-era code paths need systematic cleanup.
- Test coverage should be mapped to risk, not just package availability.
- Upgrade, backup, restore, and HA behavior need more continuous validation.

## Architecture Principles

### State Ownership

Use the right persistence model for the right problem:

| State type | Owner | Reason |
|---|---|---|
| Users, organizations, roles, sessions, auth config | Postgres | Product/account state, relational queries, audit needs |
| Cluster inventory and registration metadata | Postgres plus CRD status | Product state with Kubernetes-facing declarative integration |
| Desired baseline installation intent | CRDs plus Postgres operation rows | Needs GitOps/reconcile semantics and product visibility |
| Argo CD Applications/ApplicationSets | Kubernetes via Argo CD | Deployment desired state belongs in Kubernetes/GitOps |
| Raw Kubernetes workload objects | Target cluster API server | Kubernetes remains source of truth |
| Durable action intents | Postgres | Retry, audit, idempotency, UI history |
| Agent heartbeat and last observed status | Postgres plus ephemeral connection state | Queryable fleet state plus live tunnel state |
| Metrics/log streams | Prometheus/Thanos/log backend | Time series and logs should not be duplicated into app DB |
| Secrets | Kubernetes Secret or external secret manager | Avoid long-lived plaintext in Postgres |

Postgres is correct for management-plane product state. It should not replace etcd for Kubernetes object state. CRDs and controllers should be used where Kubernetes-native desired state and reconciliation are the right model.

### Reconciliation

Every ongoing platform concern should be converged by a controller or durable worker, not by a one-time handler mutation.

Required pattern:

1. API validates intent.
2. API writes an operation row or desired-state record.
3. Worker/controller claims work idempotently.
4. Worker/controller applies changes through the correct authority.
5. Status and events are recorded.
6. UI shows progress, failures, retries, and next action.

### Agent Boundary

The agent should be the only component that performs target-cluster local actions when direct server access is not appropriate.

Agent responsibilities:

- Authenticate to management plane.
- Maintain tunnel/session.
- Report version, capabilities, and privilege profile.
- Watch and summarize cluster state.
- Execute approved, RBAC-gated operations.
- Proxy Kubernetes API requests safely.
- Produce diagnostics and redacted logs.
- Rotate credentials and handle reconnect.

Agent non-responsibilities:

- Owning product account state.
- Making authorization decisions independent of the server.
- Storing long-lived management-plane secrets.
- Applying arbitrary unaudited mutations.

### GitOps Boundary

Argo CD replaces Fleet for deployment convergence:

- Argo CD cluster Secrets represent adopted target clusters.
- Astronomer projects product labels into Argo CD cluster Secret labels.
- ApplicationSets target clusters using those labels.
- Baseline components are deployed through Argo CD Applications/ApplicationSets.
- Astronomer owns policy, inventory, status, guardrails, and UX around this flow.

Argo CD is the deployment engine. Astronomer is the management product layer.

### Security Defaults

Default posture:

- Email-only login.
- OIDC/SAML ready architecture.
- MFA/TOTP support for local users.
- Strict session cookie handling.
- CSRF protection on browser-mutating requests.
- No caller credential forwarding to Kubernetes.
- Strip impersonation headers unless explicitly implementing controlled impersonation.
- Least-privilege agent install modes.
- NetworkPolicy by default.
- Secret rotation runbooks and tests.
- High-risk APIs route-tested.
- Audit events for all material reads and mutations.

### UI Principles

The UI should be operational, not decorative:

- Dense but readable.
- Fast first paint and fast interaction.
- Tables built for scanning, filtering, sorting, and bulk operations.
- Shared action patterns.
- Live health and sync state.
- Clear permission-disabled states.
- Confirmation for destructive actions.
- Preview/dry-run before high-risk mutations.
- No nested cards.
- No marketing landing pages in management workflows.
- No one-off page styling where a shared component fits.

## Phase Gate Model

Each phase has:

- Goal.
- Reasoning.
- Tasks.
- Security requirements.
- Testing requirements.
- Definition of done.

Work can run in parallel across phases, but release gates should follow the ordering below.

## Phase 0: Baseline Audit and Quality Inventory

### Goal

Create a factual inventory of current capabilities, duplicate code, dead code, test gaps, security-sensitive paths, and UI inconsistencies before broad implementation.

### Reasoning

Rancher-quality work cannot be driven by impressions. The system needs a durable audit artifact that names every high-risk path and every repeated pattern that should be consolidated.

### Tasks

- [x] Generate a current mounted route inventory:
  - all registered router methods and patterns
  - checked in as `docs/generated-route-inventory.json`
  - refreshed by `ASTRONOMER_WRITE_ROUTE_INVENTORY=1 go test ./internal/server -run TestRouteInventoryCanBeGenerated -count=1`
- [x] Enrich the route inventory with security metadata:
  - [x] all API routes
  - [x] auth requirement
  - [x] RBAC resource/action or conservative route-family posture where static route walking cannot prove exact handler-internal checks
  - [x] CSRF requirement
  - [x] audit event type or audit posture
  - [x] add an inventory assertion for dedicated `/api/v1/audit/*` routes
  - [x] handler package / route-family owner
  - [x] tests covering route metadata
  - current progress: generated inventory has 405 mounted route rows, zero missing posture fields, 43 handler owners, high-risk registry metadata for 262 mounted rows, and explicit mutating-route classification metadata for 3 auth-flow rows.
- [x] Add a mutating-route classification gate:
  - registry-backed high-risk route coverage through `docs/security-sensitive-routes.json`
  - intentional public auth session-flow exception through `docs/route-risk-classifications.json`
  - enforced by `TestMutatingRoutesHaveSecurityClassification`
- [x] Promote candidate high-risk route groups:
  - cluster lifecycle and registration mutators
  - user CRUD mutators
  - RBAC role and binding mutators
  - catalog repository and retry mutators
  - workload retry and pod deletion mutators
  - cluster template mutators
  - legacy Fleet-named operation aliases, or rename/deprecate them for the Argo-based model
- [x] Produce a Kubernetes proxy inventory:
  - [x] agent tunnel proxy
  - [x] service proxy
  - [x] Argo CD internal proxy
  - [x] kubectl shell
  - [x] logs/exec/port-forward style streams
  - [x] headers stripped
  - [x] SSRF protections
  - [x] impersonation behavior
  - [x] audit behavior
- [x] Produce a frontend page inventory:
  - route
  - owning feature area
  - API clients used
  - RBAC gates
  - loading/error/empty states
  - table/action components used
  - e2e coverage
- [x] Produce a database inventory:
  - tables
  - owner package
  - migration that created each table
  - sensitive columns
  - indexes
  - foreign keys
  - cascade behavior
  - retention policy
- [x] Produce a CRD inventory:
  - group/version/kind
  - schema owner
  - controller owner
  - status conditions
  - finalizers
  - conversion plan
  - tests
- [x] Produce a background job inventory:
  - task name
  - enqueue points
  - idempotency key
  - retry policy
  - dead-letter behavior
  - metrics
  - audit events
- [x] Run duplicate detection:
  - duplicate TypeScript API client functions
  - duplicate table implementations
  - duplicate auth/RBAC checks
  - duplicate Kubernetes object parsing
  - duplicate Go DTOs mirroring OpenAPI types
  - duplicate Helm value snippets
- [x] Run dead-code detection:
  - unreachable frontend routes
  - unused React components
  - unused Go functions
  - unused SQL queries
  - unused migrations helpers
  - unused Helm values
- [x] Create a tracked quality backlog from the audit.

### Recommended Tools

- `rg --files`
- `go test ./...`
- `go test -race ./...` for selected packages
- `go vet ./...`
- `golangci-lint run ./...`
- `npm run lint`
- `npm run type-check`
- `npm test -- --runInBand`
- `npm run test:e2e`
- `helm lint deploy/chart`
- `helm template deploy/chart`
- `git diff --check`
- `go list ./...`
- `go tool nm` only if needed for binary-level dead symbol research
- `ts-prune` or equivalent if added intentionally
- `depcheck` or equivalent if added intentionally

### Security Requirements

- Every route inventory row must indicate whether unauthenticated access is expected.
- Every tunnel/proxy path must have a negative test proving caller credentials and impersonation headers are not forwarded.
- Every route that mutates server state from browser sessions must document CSRF coverage.
- Every sensitive table must document secret handling and retention.

### Definition of Done

- [x] A checked-in audit document exists under `docs/`.
- [x] A raw mounted route inventory covers 100 percent of registered routes.
- [x] Route inventory metadata covers auth, RBAC, CSRF, audit posture, handler ownership, and tests for every registered route.
- [x] Proxy inventory covers 100 percent of Kubernetes or HTTP forwarding paths.
- [x] Dead-code candidates are split into `remove`, `keep`, and `needs investigation`.
- [x] Duplicate-code candidates have owners and target abstractions.
- [x] At least one CI job enforces the route security inventory or route security tests.

## Phase 1: Source-of-Truth and CRD Expansion

### Goal

Make the Postgres/CRD/Argo/Kubernetes split explicit and robust.

### Reasoning

Using Postgres is not a problem. Using Postgres as if it were etcd would be a problem. The system needs clear boundaries: product state in Postgres, Kubernetes desired state in CRDs and Argo CD, observed cluster objects in the target API server.

### Target CRDs

Existing or planned CRDs should evolve toward:

| Kind | Purpose | Source-of-truth role |
|---|---|---|
| `Cluster` | Adopted cluster registration intent and metadata | Declarative product-facing API mirrored to Postgres |
| `Project` | Tenant/project policy and cluster membership | Declarative product-facing API mirrored to Postgres |
| `ClusterBaseline` | Baseline components desired for a cluster/group | Desired state for Argo-managed components |
| `ComponentBundle` | Reusable application/baseline bundle | Declarative bundle catalog |
| `AgentProfile` | Agent privilege and capability mode | Declarative install/security profile |
| `AccessPolicy` | Project/cluster/namespace permissions | Kubernetes-facing RBAC policy surface |
| `PolicySet` | Pod security, network, image, admission policy grouping | Desired policy attachment |
| `BackupPlan` | Backup schedule, retention, restore target | Durable backup desired state |
| `ObservabilityProfile` | Metrics/logs/traces stack attachment | Desired observability configuration |
| `GitOpsTarget` | Argo ApplicationSet targeting policy | Desired deployment targeting |
| `ClusterHealthProfile` | SLOs and alert thresholds | Desired health policy |

### Tasks

- [x] Update `docs/control-plane-state-contract.md` with the final state ownership matrix.
- [x] Update `docs/crd-api.md` with planned CRDs and version policy.
- [x] Add `ClusterBaseline` CRD:
  - [x] cluster selector
  - [x] profile name
  - [x] component list
  - [x] version pins
  - [x] sync policy
  - [x] override values references
  - [x] status conditions
  - [x] controller validates selector/bundle shape and writes standard status conditions
  - [x] controller generates/repairs per-bundle Argo ApplicationSets from ComponentBundle refs
  - [x] controller removes stale/generated ApplicationSets on bundle removal and delete
  - [x] controller writes generated ApplicationSet references into status
  - [x] controller writes generated child Application sync/health status
  - [x] controller refuses to overwrite same-name ApplicationSets without matching CRD ownership labels/annotations
  - [x] controller detects and repairs generated ApplicationSet spec drift beyond ownership conflicts
- [x] Add `ComponentBundle` CRD:
  - [x] source repo/chart/path
  - [x] default namespace
  - [x] values schema reference
  - [x] required capabilities
  - [x] health checks
  - [x] upgrade policy
  - [x] controller validates source shape and required capability declarations
  - [x] controller validates values schema and secret reference shape without inlining secrets
  - [x] controller exposes source targetRevision as resolved revision
  - [x] controller enforces ComponentBundle version pins for ClusterBaseline and GitOpsTarget refs
  - [x] controller validates override values against referenced schemas
  - [x] controller supports a governed multi-version bundle catalog
- [x] Add `AgentProfile` CRD:
  - [x] privilege level
  - [x] allowed verbs/resources
  - [x] namespace scope
  - [x] host access flags
  - [x] shell/log/exec permissions through capability flags
  - [x] network egress profile
  - [x] controller validates profile/scope shape and reports effective RBAC summary
  - [x] controller projects referenced profiles into registration manifest privilege annotations
  - [x] controller projects richer install metadata into registration manifests
  - [x] controller blocks features the profile does not permit
- [x] Add `GitOpsTarget` CRD:
  - [x] cluster label selector
  - [x] project selector
  - [x] bundle reference
  - [x] ApplicationSet generation policy
  - [x] sync windows
  - [x] prune/self-heal controls
  - [x] controller validates Argo managed-cluster selector boundary and sync-window shape
  - [x] controller generates/repairs Argo ApplicationSets for direct source and ComponentBundle-backed targets
  - [x] controller surfaces generated Application sync/health rollup
  - [x] controller refuses to overwrite same-name ApplicationSets without matching CRD ownership labels/annotations
  - [x] controller resolves `templateRef` from a governed ConfigMap template catalog
  - [x] controller detects and repairs generated ApplicationSet spec drift beyond ownership conflicts
  - [x] controller enforces projectSelector against same-namespace Projects for explicit cluster refs and durable project labels
  - [x] controller reports deeper child Application/resource drift details
- [x] Add CRD status condition standard:
  - [x] standard Kubernetes condition shape
  - [x] `observedGeneration`
  - [x] `lastTransitionTime`
  - [x] status subresources
- [x] Add finalizer standard:
  - [x] finalizer names
  - [x] first-pass cleanup behavior for CRDs without external child resources
  - [x] timeout behavior
  - [x] manual recovery docs
- [x] Add conversion policy before any CRD reaches `v1beta1`.
- [x] Add CRD schema validation tests.
- [x] Add first-pass CRD controller unit tests for validation/status reconcilers.
- [x] Add CRD controller integration tests with envtest where practical.

### Example: ClusterBaseline

```yaml
apiVersion: management.astronomer.io/v1alpha1
kind: ClusterBaseline
metadata:
  name: production-standard
  namespace: astronomer-mgmt
spec:
  selector:
    matchLabels:
      tier: prod
  syncPolicy:
    automated: true
    prune: true
    selfHeal: true
  components:
    - name: ingress-nginx
      bundleRef: ingress-nginx
      targetNamespace: ingress-nginx
      version: 4.11.3
    - name: cert-manager
      bundleRef: cert-manager
      targetNamespace: cert-manager
      version: v1.15.3
    - name: astronomer-agent
      bundleRef: astronomer-agent
      targetNamespace: astronomer-system
      version: 0.1.2
status:
  conditions:
    - type: Ready
      status: "True"
      reason: ApplicationsHealthy
```

### Example: GitOpsTarget

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: observability-template
  namespace: astronomer-mgmt
  labels:
    management.astronomer.io/gitops-template: "true"
data:
  template.json: |
    {
      "project": "platform",
      "destinationNamespace": "monitoring",
      "source": {
        "type": "git-path",
        "repoURL": "https://github.com/example/platform-gitops.git",
        "path": "observability",
        "targetRevision": "main"
      }
    }
---
apiVersion: management.astronomer.io/v1alpha1
kind: GitOpsTarget
metadata:
  name: prod-observability
  namespace: astronomer-mgmt
spec:
  selector:
    matchLabels:
      astronomer.io/managed-by: astronomer
      tier: prod
  applicationSet:
    templateRef: observability-template
  syncPolicy:
    automated: true
    prune: true
    selfHeal: true
```

### Security Requirements

- CRD controllers must validate namespace boundaries.
- CRD controllers must never escalate RBAC beyond the configured agent profile.
- CRD status must not leak secrets.
- CRD specs that reference secrets must use `secretRef`, not inline secret values.
- Deletion finalizers must be safe under partial failure.

### Testing Requirements

- Unit tests for spec validation.
- Conversion round-trip tests for each served version.
- Controller reconcile tests:
  - create
  - update
  - delete
  - finalizer cleanup
  - status patch conflict
  - missing referenced object
  - permission denied
- Migration tests for Postgres-backed state mirrored from CRDs.
- Backup/restore tests to prove CRD and Postgres state can reconcile after restore.

### Definition of Done

- [ ] State ownership is documented and enforced in reviews.
- [x] CRDs exist for baseline, bundle, agent profile, and GitOps targeting.
- [x] Each CRD has status conditions, schema tests, unit reconcile tests, and envtest coverage where practical.
- [ ] Postgres stores durable product state and operation history, not copies of raw cluster objects.
- [ ] Argo CD owns desired deployment convergence.
- [ ] Target clusters remain source of truth for Kubernetes workloads.

## Phase 2: Argo CD as the Fleet Replacement

### Goal

Make Argo CD provide Fleet-equivalent cluster targeting, baseline deployment, drift reporting, rollback visibility, and adoption automation.

### Reasoning

Argo CD is a strong deployment engine, but Rancher/Fleet quality comes from product conventions around grouping, targeting, status, and error handling. Astronomer needs that layer.

### Tasks

- [x] Define standard Argo cluster Secret labels:
  - `astronomer.io/managed-by`
  - `astronomer.io/cluster-id`
  - `astronomer.io/cluster-name`
  - `astronomer.io/is-local`
  - `astronomer.io/project-id`
  - `astronomer.io/environment`
  - `astronomer.io/region`
  - `astronomer.io/provider`
  - `astronomer.io/agent-privilege-profile`
  - `astronomer.io/agent-version`
  - `astronomer.io/kubernetes-version`
  - sanitized user labels
  - distribution
  - multi-project membership labels
- [x] Implement first-pass standard labels for managed cluster registration, explicit refresh, worker refresh, and local Argo bootstrap.
- [x] Add normalized agent privilege profile label projection.
- [x] Add version labels after the underlying product fields and value sanitization policy are finalized.
- [x] Add label-collision validation in API and UI.
- [x] Add periodic Argo cluster Secret reconciliation:
  - not only on cluster update
  - detects missing labels
  - detects missing Secret
  - repairs when safe
  - records drift
- [x] Verify and test the existing periodic auto-adoption sweep:
  - runs every 5 minutes from the worker scheduler;
  - re-upserts eligible connected/local clusters into every configured Argo CD instance;
  - repairs missing/deleted Argo cluster Secrets through Argo CD `upsert=true`;
  - rebuilds a missing DB index row from an Astronomer-owned Argo cluster Secret for the single built-in instance case;
  - records global repair job success/failure state.
- [x] Add per-cluster drift event detail for periodic Argo repair:
  - [x] missing Secret repaired;
  - [x] missing label repaired;
  - [x] missing DB index row rebuilt from owned Secret;
  - [x] orphan Secret found;
  - [x] repair blocked because token material is unavailable.
- [x] Add built-in ApplicationSet templates:
  - [x] all adopted clusters
  - [x] project-scoped clusters after durable project labels are finalized
  - [x] environment-scoped clusters
  - [x] label-scoped clusters
  - [x] canary subset
- [x] Implement and test the built-in all-adopted-clusters baseline ApplicationSet template convention:
  - cluster generator selector `astronomer.io/managed-by=astronomer`;
  - cluster generator selector `astronomer.io/is-local=false`;
  - stable ApplicationSet names per baseline component;
  - `astronomer.io/baseline-target=adopted-clusters` metadata label for inventory and future drift reports.
- [x] Require user-created cluster-generator ApplicationSets to include `astronomer.io/managed-by=astronomer` so templates created through Astronomer cannot accidentally target non-Astronomer Argo cluster Secrets.
- [x] Add component baseline engine:
  - [x] ingress-nginx
  - [x] cert-manager
  - [x] metrics stack
  - [x] logging stack
  - [x] policy stack
  - [x] agent upgrades through Agent Fleet lifecycle operations, intentionally outside generic Argo ApplicationSet ownership
  - [x] custom bundles through `ComponentBundle` + `ClusterBaseline`
- [x] Add Argo-managed baseline components for `ingress-nginx`, cert-manager, metrics collection (`kube-state-metrics`, node exporter), logging (`fluent-bit`), image scanning (`trivy-operator`), and policy enforcement (`gatekeeper`).
- [ ] Add per-cluster override model:
  - [x] values from Git as Argo Helm `valueFiles`
  - [x] values from Kubernetes Secret as same-namespace validated governance refs, not inlined into ApplicationSets
  - [x] values from ConfigMap as same-namespace validated governance refs, not inlined into ApplicationSets
  - [x] values from UI-managed config as literal Helm parameters
  - [x] precedence rules
  - [ ] audit history for CRD override changes
- [x] Add Argo drift summary to cluster detail:
  - [x] Synced/OutOfSync counts from cached Argo Application rows
  - [x] Healthy/Progressing/Degraded counts from cached Argo Application rows
  - [x] last sync timestamp
  - [x] highest-severity drift/error message
  - [x] app count
  - [x] resources pruned/created/changed
- [ ] Add Argo ownership conflict handling:
  - [x] surface per-component baseline ownership state
  - [x] record audited adopt existing resources decision
  - [x] record audited leave existing resources unmanaged decision
  - [x] record audited replace existing resources decision
  - [x] block unsafe replacement decision for local/unregistered clusters
  - [ ] execute adopt/replace migration operations against existing Helm/local resources
- [x] Add sync wave standards:
  - [x] first-pass sync-wave annotations on generated platform-owned baseline Applications
  - [x] namespaces
  - [x] CRDs
  - [x] operators
  - [x] policies
  - [x] workloads
  - [x] health checks
- [x] Add rollback workflows:
  - [x] show previous revisions
  - [x] require permission
  - [x] dry-run where possible
  - [x] audit event
  - [x] operation record
- [x] Add sync windows:
  - [x] maintenance windows
  - [x] blackout windows
  - [x] emergency override through Argo `manualSync`
  - [x] approval workflow through required override reason and audit trail
- [x] Add orphan detection:
  - [x] cached Astronomer-generated baseline Applications without a matching managed Argo cluster target
  - [x] Argo cluster Secrets without matching Astronomer cluster
  - [x] DB records without Argo Secret
  - [x] live Argo Applications without matching Astronomer cluster labels
  - [x] ApplicationSet-generated Applications with stale labels/owner references from live Argo metadata

### Example: ApplicationSet Pattern

```yaml
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: baseline-prod
  namespace: argocd
spec:
  generators:
    - clusters:
        selector:
          matchLabels:
            astronomer.io/managed-by: astronomer
            astronomer.io/environment: production
  template:
    metadata:
      name: '{{name}}-baseline'
      labels:
        astronomer.io/component: baseline
    spec:
      project: astronomer-managed
      source:
        repoURL: https://github.com/example/platform-baselines.git
        targetRevision: main
        path: baselines/prod
      destination:
        server: '{{server}}'
        namespace: astronomer-system
      syncPolicy:
        automated:
          prune: true
          selfHeal: true
        syncOptions:
          - CreateNamespace=true
```

### Security Requirements

- Argo credentials must be stored in Kubernetes Secrets or external secret manager.
- Argo API tokens must be scoped.
- UI must not expose raw Argo tokens.
- Argo proxy routes must enforce server-side RBAC.
- ApplicationSet creation must validate destination namespace and cluster selector.
- Prevent cross-tenant ApplicationSet targeting.
  - [x] CRD writer enforces `projectSelector` for explicit cluster refs and durable project labels.
  - [x] All Argo managed-cluster Secrets carry durable project labels for broad project-scoped label selectors.
- Audit every sync, rollback, adoption, and ownership decision.

### Testing Requirements

- Unit tests for label projection and sanitization.
- Tests for label collision detection.
- Handler tests for cluster adoption and Argo registration.
- Reconciler tests for missing/stale Argo cluster Secrets.
- Integration test with local Argo CD:
  - register cluster
  - labels projected
  - ApplicationSet creates Application
  - label change removes Application
  - cluster removal cleans up
- UI tests for GitOps status, drift, and adoption decisions.
- Failure tests:
  - Argo unavailable
  - token expired
  - ApplicationSet controller unavailable
  - invalid chart values
  - resource ownership conflict

### Definition of Done

- [ ] New adopted clusters are automatically registered with built-in Argo CD.
- [ ] Baseline components deploy automatically through Argo CD.
- [ ] ApplicationSets provide Fleet-equivalent cluster targeting.
- [ ] Cluster labels reliably drive deployment targeting.
- [ ] Drift and health are visible in cluster and project views.
- [ ] Ownership conflicts are explicit and auditable.
- [ ] Failure modes are recoverable from the UI.

## Phase 3: Agent Fleet Maturity

### Goal

Make agent lifecycle, status, security, and diagnostics strong enough for large multi-cluster operations.

### Reasoning

Rancher-quality management depends on trust in the agent. Operators need to know whether each cluster is connected, what version the agent runs, what permissions it has, what it can do, and why it is degraded.

### Tasks

- [x] Define an agent capability contract:
  - [x] version
  - [x] build SHA
  - [x] Kubernetes server version
  - [x] distribution
  - [x] available APIs
  - [x] privilege profile
  - [x] enabled features
  - [x] denied features
  - [x] last successful action
  - [x] degraded reasons
- [x] Add agent heartbeat schema versioning.
- [x] Add compatibility matrix:
  - [x] server version
  - [x] agent version
  - [x] minimum supported agent
  - [x] deprecated agent
  - [x] blocked agent
- [x] Add agent upgrade plans:
  - [x] canary clusters
  - [x] batch size
  - [x] max unavailable
  - [x] rollback image
  - [x] preflight checks
  - [x] post-upgrade health checks
- [ ] Add agent credential rotation:
  - [x] registration token hash-only storage
  - [x] durable agent token hash-only storage after issuance
  - [x] token expiration for registration tokens
  - [x] used-token tracking for registration tokens
  - [x] durable token revocation gate
  - [ ] strict one-time registration-token enforcement after durable ACK delivery is made retry-safe
  - [x] explicit audit events for durable token rotation and revocation
- [x] Add reconnect hardening:
  - [x] exponential backoff
  - [x] jitter
  - [x] server-side session replacement
  - [x] stale session cleanup
  - [x] reconnect storm metrics
- [x] Add diagnostics bundle:
  - [x] agent pod status
  - [x] recent events
  - [x] redacted logs
  - [x] RBAC self-check
  - [x] network checks
  - [x] Argo registration/proxy target reachability
  - [x] Kubernetes discovery summary
  - [x] clock skew
- [x] Add offline cluster behavior:
  - [x] list queue-safe operations while offline
  - [x] mark unsupported live/in-cluster operations blocked
  - [x] surface last known state
  - [x] avoid false green status
- [x] Add least-privilege install profiles:
  - [x] read-only
  - [x] operations
  - [x] admin
  - [x] custom
  - [x] namespace-scoped
- [x] Add agent self-test command in UI and CLI.
  - [x] API endpoint
  - [x] Agent Fleet drawer action and checklist UI
  - [x] route-security, OpenAPI, and focused handler coverage
  - [x] CLI command
- [x] Add support bundle download with strict redaction.

### Security Requirements

- Agent tokens are never stored plaintext after issuance.
- Agent tunnel auth rejects replayed or expired tokens.
- Agent upgrade operations require explicit RBAC permission.
- Agent diagnostics redact:
  - Secrets
  - tokens
  - Authorization headers
  - cookies
  - kubeconfigs
  - private keys
  - connection strings
- Agent cannot execute arbitrary commands unless shell permission is explicitly granted.
- All agent actions include actor, cluster, verb, resource, and correlation id.

### Testing Requirements

- Unit tests for heartbeat parsing and compatibility decisions.
- Tunnel auth tests:
  - expired token
  - revoked token
  - wrong cluster
  - replay attempt
  - version unsupported
- Agent upgrade tests:
  - happy path
  - patch denied
  - image pull failure
  - health check failure
  - rollback
- Load tests:
  - 100 agents reconnect
  - 1,000 agents heartbeat
  - management plane restart during reconnect storm
- Redaction tests for diagnostics bundles.

### Definition of Done

- [ ] Every cluster has a clear agent health state.
- [ ] Agent permissions are visible and explain disabled UI actions.
- [ ] Agents can be upgraded in controlled batches.
- [x] Diagnostics are downloadable and redacted.
- [ ] Token rotation and revocation are supported.
- [x] Offline behavior is predictable.

## Phase 4: Rancher-Quality Cluster Explorer

### Goal

Make the cluster Explorer deep, fast, consistent, and safe across common Kubernetes resources.

### Reasoning

For adopted clusters, Cluster Explorer is the core product. It needs to replace common kubectl/Rancher daily workflows.

### Resource Coverage

Minimum supported resource groups:

- Namespaces
- Nodes
- Pods
- Deployments
- StatefulSets
- DaemonSets
- ReplicaSets
- Jobs
- CronJobs
- Services
- Ingresses
- Gateway API resources where available
- ConfigMaps
- Secrets with strict permission handling
- PVCs
- PVs
- StorageClasses
- ServiceAccounts
- Roles
- RoleBindings
- ClusterRoles
- ClusterRoleBindings
- NetworkPolicies
- Events
- CRDs
- Custom resources

### Shared UX Requirements

Every resource list should have:

- namespace filter when namespaced
- cluster context
- project context where applicable
- search
- sort
- column picker
- label filter
- field selector support where safe
- live/watch status
- stale data indicator
- empty state
- error state
- permission-denied state
- bulk selection where safe
- export YAML

Every resource detail should have:

- summary
- labels and annotations
- owner references
- conditions
- events
- YAML
- managed fields
- related resources
- RBAC/audit tab for sensitive resources
- action history where Astronomer performed actions

### Tasks

- [ ] Build a shared resource table framework.
- [ ] Build a shared resource detail drawer.
- [ ] Build a shared YAML editor:
  - schema awareness where possible
  - server-side dry-run
  - diff preview
  - conflict detection
  - managed fields display
  - warnings display
- [ ] Add watch-backed resource invalidation.
- [ ] Add polling fallback when watch is unavailable.
- [ ] Add safe delete:
  - propagation policy
  - finalizer warning
  - owner-reference impact
  - force delete requires elevated permission
- [ ] Add workload actions:
  - scale
  - restart rollout
  - pause/resume rollout
  - undo rollout
  - view rollout history
- [ ] Add pod actions:
  - logs
  - previous logs
  - exec shell
  - delete
  - describe-equivalent summary
  - image and probe status
- [ ] Add node actions:
  - cordon
  - uncordon
  - drain
  - taint
  - untaint
  - label
  - annotate
  - pressure summary
- [ ] Add storage actions:
  - PVC expansion where supported
  - view bound PV
  - storage class details
  - reclaim policy warning
- [ ] Add service/ingress actions:
  - endpoint inspection
  - backend health
  - TLS secret check
  - cert-manager certificate relation
- [ ] Add CRD/custom resource explorer:
  - API group navigation
  - schema display
  - custom resource list
  - generic YAML edit with dry-run
- [ ] Add cross-resource search:
  - by name
  - by image
  - by label
  - by namespace
  - by node
  - by owner

### Security Requirements

- Secrets are hidden unless user has explicit `secrets:read`.
- Secret values require a second explicit action and audit event.
- Exec/shell requires explicit permission and stream ticket.
- Logs may expose secrets; access must be permission-gated and audited.
- YAML apply must use server-side permission checks and audit the actor.
- The proxy must strip Authorization, Cookie, Host, X-Forwarded-*, and Impersonate-* headers unless a controlled feature explicitly needs them.
- Server must block path traversal and SSRF-style paths.

### Testing Requirements

- Handler tests for each high-risk action.
- Frontend unit tests for shared resource components.
- Playwright tests for:
  - pods list
  - deployment detail
  - YAML dry-run
  - conflict warning
  - permission denied
  - logs view
  - node drain guardrails
- Integration tests with fake Kubernetes API or envtest for:
  - list/watch fallback
  - delete options
  - patch/apply
  - events
- Security route tests for:
  - unauthenticated blocked
  - unauthorized blocked
  - impersonation stripped
  - CSRF required where browser mutation occurs

### Definition of Done

- [ ] Operators can perform common kubectl/Rancher inspect and repair workflows from Astronomer.
- [ ] Resource pages are visually and behaviorally consistent.
- [ ] High-risk actions are previewed, permissioned, and audited.
- [ ] Resource data is live or clearly marked as stale/degraded.
- [ ] Custom resources are discoverable and manageable.

## Phase 5: RBAC, Tenancy, and Authorization

### Goal

Make authorization reliable, explainable, scoped, and safe for multi-tenant operations.

### Reasoning

Rancher-quality management requires users to trust that permissions are precise. A UI action being hidden is not enough; the server must enforce every boundary.

### Scope Model

Required scopes:

- global
- organization
- project
- cluster
- namespace
- resource kind
- individual high-risk action

### Tasks

- [x] Define canonical RBAC resource/action list.
- [x] Document mapping from UI action to backend permission.
- [x] Document mapping from backend permission to Kubernetes verb/resource when applicable.
- [x] Add effective permission endpoint:
  - [x] current user
  - [x] selected target user
  - [x] selected cluster/project scope visibility in binding sources
  - [x] selected namespace context visibility, with enforcement status warnings until namespace-scoped binding storage lands
- [x] Add permission preview before saving role assignments.
- [x] Add role templates:
  - [x] cluster viewer
  - [x] cluster operator
  - [x] cluster admin / owner
  - [x] project viewer
  - [x] project operator / owner
  - [x] security auditor
  - [x] GitOps operator / deployer
  - [x] observability operator / monitoring admin
  - [x] backup operator
  - [x] support engineer
  - [x] break-glass admin through platform/cluster owner templates
- [x] Enforce canonical Kubernetes resource-family permissions on server routes and search:
  - [x] generic resource lists
  - [x] dynamic group/version/kind resource discovery
  - [x] named resource create/read/update/delete routes
  - [x] raw Kubernetes proxy read/list/watch/create/update/delete paths for known resource families
  - [x] cross-cluster resource search
  - [x] node inventory routes
  - [x] node maintenance routes, with drain gated by `nodes:manage`
- [ ] Add namespace-scoped bindings:
  - [x] namespace field in in-process binding model
  - [x] engine fail-closed namespace matching
  - [x] middleware namespace context extraction from route params and query params
  - [x] effective-permissions namespace source/context reporting
  - [ ] database storage without breaking existing sqlc `SELECT *` scans
  - [ ] create/list/delete API request and response fields
  - [ ] role-binding assignment UI controls
  - [ ] route coverage for namespace-sensitive resource paths:
    - [x] generic ConfigMap list
    - [x] generic Secret list
    - [ ] workload detail/list/action paths
    - [ ] pod list/log/delete paths
    - [ ] Service, Ingress, and NetworkPolicy paths
- [ ] Add temporary access grants:
  - start time
  - expiration
  - reason
  - approver
  - audit event
- [ ] Add break-glass workflow:
  - requires MFA
  - requires reason
  - time bound
  - prominent audit event
  - optional notification
- [x] Add permission explanation in UI:
  - [x] why action is disabled
  - [x] which role grants it through the Effective Permissions tab and shared permission decision model
  - [x] where to request access
  - [x] cluster resource explorer action disablement for nodes, pods, workloads, Services, Ingresses, NetworkPolicies, Gateway API, storage, and generic resources
  - [x] per-cluster Apps install, upgrade, uninstall, and failed-install cleanup disablement with catalog permission reasons and modal-time guards
  - [x] per-cluster Tools enable, retry, adopt, and disable action disablement with catalog permission reasons and modal-time guards
- [ ] Add service account/API token RBAC:
  - [ ] service account identity and ownership model
  - [x] token scope
  - [x] expiration
  - [x] last used
  - [x] revocation
  - [x] audit

### Security Requirements

- Server-side enforcement is mandatory for every protected action.
- UI gating is convenience only.
- No route should infer authorization from cluster membership alone.
- No wildcard permissions in built-in non-admin roles unless explicitly justified.
- Break-glass grants must be auditable and time-limited.
- API tokens must be hash-only at rest.

### Testing Requirements

- Permission matrix tests for all built-in roles.
- Route tests for every high-risk API.
- UI tests for disabled action explanations.
- Regression tests for cross-project and cross-cluster access denial.
- Tests for temporary grant expiration.
- Tests for API token revocation and last-used update.

### Definition of Done

- [x] Every route has explicit auth and RBAC metadata.
- [ ] Every UI action maps to a server-enforced permission.
- [x] Effective permissions are visible and test-covered.
- [x] Built-in roles support practical day-2 operations without overgranting.
- [ ] Cross-tenant access is denied by default.

## Phase 6: Security Hardening Program

### Goal

Make security a continuous engineering system, not a one-time review.

### Reasoning

The platform has powerful cluster access. Security quality must be measurable and enforced in tests and CI.

### Tasks

- [x] Update `docs/threat-model.md` with:
  - [x] management plane assets
  - [x] agent assets
  - [x] Argo CD assets
  - [x] target cluster assets
  - [x] browser/session assets
  - [x] supply-chain assets
  - [x] backup/restore assets
- [x] Add threat model checklist to PR template.
- [x] Add security-sensitive route registry.
- [x] Add route tests for all unauthenticated and high-risk endpoints.
- [x] Add proxy safety tests:
  - [x] header stripping
  - [x] host/path validation
  - [x] SSRF blocking
  - [x] method restrictions
  - [x] response header sanitization
- [x] Add CSRF tests for browser-mutating routes.
- [x] Add session hardening tests:
  - [x] secure cookie
  - [x] httpOnly
  - [x] sameSite
  - [x] expiration
  - [x] logout invalidation
- [x] Add login controls:
  - [x] email-only login
  - [x] rate limiting
  - [x] lockout/backoff
  - [x] audit failure events
  - [x] MFA enrollment
  - [x] OIDC support
- [x] Add secret handling policy:
  - [x] no plaintext secret persistence unless explicitly approved
  - [x] hash-only tokens
  - [x] encryption-at-rest for required secrets
  - [x] external secret manager option
  - [x] redaction library for diagnostics and support bundles
  - [x] decide whether domain-specific CloudCredential, Vault, and audit redactors should wrap the shared library or stay intentionally specialized
- [x] Add container security:
  - [x] non-root
  - [x] read-only root filesystem where possible
  - [x] dropped capabilities
  - [x] seccomp
  - [x] resource limits
  - [x] tmp writable volume only where needed
- [x] Add network security:
  - [x] default-deny NetworkPolicies
  - [x] explicit egress
  - [x] Argo egress
  - [x] Kubernetes API egress
  - [x] DNS egress
  - [x] database/redis egress
- [x] Add supply-chain controls:
  - [x] SBOM
  - [x] image signing
  - [x] vulnerability scan
  - [x] dependency audit
  - [x] provenance attestation
  - [x] pinned base images
- [ ] Add audit event coverage:
  - auth events
  - RBAC changes
  - cluster registration
  - agent lifecycle
  - [x] mutating proxy access for generic Kubernetes, service, Argo UI/API, and Argo internal Kubernetes proxy paths
  - [x] direct logs/exec/shell open events without stream payload capture
  - [x] secret reads
  - GitOps sync/rollback
  - [x] destructive Kubernetes operations through named/generic resource routes, generic Kubernetes proxy mutations, and node actions

### Testing Requirements

- `go test ./internal/server -run 'Test.*Route.*'`
- `go test ./internal/agent ./internal/tunnel ./internal/handler`
- `npm audit --audit-level=moderate`
- `govulncheck ./...`
- container scanner in CI
- Helm policy tests where added
- e2e auth/session tests
- redaction golden tests

### Definition of Done

- [x] Threat model is current.
- [x] Security-sensitive routes have tests.
- [x] Proxy paths have negative tests.
- [x] Secrets and tokens are hash-only or encrypted.
- [x] Images are signed and have SBOMs.
- [x] NetworkPolicy is default-on in production values.
- [ ] Audit log coverage is complete for high-risk actions.

## Phase 7: UI Quality System

### Goal

Make the frontend feel like a cohesive, professional management product.

### Reasoning

Rancher UI quality comes from consistency and workflow completeness. Astronomer needs shared primitives, strict page patterns, and careful validation on real viewports.

### Tasks

- [x] Define UI information architecture:
  - [x] Dashboard
  - [x] Clusters
  - [x] Cluster Explorer
  - [x] Projects
  - [x] GitOps
  - [x] Agents
  - [x] Observability
  - [x] Security
  - [x] Backups
  - [x] Catalog
  - [x] RBAC
  - [x] Settings
  - [x] Audit
- [x] Define shared page templates:
  - [x] index/list page
  - [x] detail page
  - [x] settings page
  - [x] operation history page
  - [x] diagnostics page
  - [x] resource explorer page
- [x] Build shared table system:
  - [x] density
  - [x] sorting
  - [x] filtering
  - [x] column picker
  - [x] row selection
  - [x] bulk actions
  - [x] stable loading skeleton
  - [x] virtualization where needed by current bounded/paginated tables
- [x] Build shared status badges:
  - [x] health
  - [x] sync
  - [x] agent connection
  - [x] drift
  - [x] permission
  - [x] degraded reason
- [x] Build shared action framework:
  - safe action
  - destructive action
  - dry-run action
  - async operation action
  - approval-required action
  - disabled-with-reason action
- [x] Build shared empty/error states:
  - no data
  - no permission
  - disconnected agent
  - degraded Argo
  - failed load
  - stale cache
- [x] Build shared drawer/dialog framework:
  - [x] keyboard accessible
  - [x] focus managed
  - [x] no nested modal traps
  - [x] consistent submit/cancel placement
  - [x] loading state
  - [x] error state
- [x] Build global command/search:
  - [x] clusters
  - [x] namespaces
  - [x] workloads
  - [x] pods
  - [x] projects
  - [x] GitOps apps
  - [x] docs/runbooks
- [x] Build audit/activity side panels.
- [x] Build operation timeline component.
- [x] Build design review checklist:
  - [x] no text overflow
  - [x] no overlapping content
  - [x] mobile/tablet/desktop layouts
  - [x] dark/light theme
  - [x] keyboard navigation
  - [x] screen reader labels
  - [x] no card-in-card layout
  - [x] icons for icon-suitable actions

### Page-Specific Requirements

Cluster list:

- health
- agent status
- Kubernetes version
- distribution
- project
- environment
- Argo sync
- baseline profile
- alerts
- last seen
- bulk label/annotate

Cluster detail:

- overview
- Explorer
- GitOps
- agent
- nodes
- workloads
- events
- policies
- observability
- access
- settings

GitOps:

- Argo instances
- managed clusters
- ApplicationSets
- Applications
- drift
- sync history
- ownership decisions
- rollback
- sync windows

Agents:

- version matrix
- upgrade readiness
- diagnostics
- privilege profile
- last heartbeat
- operation history
- reconnect status

Security:

- image scans
- policy violations
- RBAC findings
- secret access audit
- admission policy status
- compliance reports

### Testing Requirements

- Jest tests for shared components.
- Playwright tests for critical workflows:
  - login
  - cluster list
  - pods page
  - workload action
  - GitOps baseline status
  - agent diagnostics
  - RBAC permission denied
  - audit search
- Visual checks:
  - 375px mobile
  - 768px tablet
  - 1280px desktop
  - 1920px desktop
  - dark/light theme
- Accessibility checks:
  - keyboard navigation
  - focus order
  - aria labels
  - contrast

### Definition of Done

- [ ] Shared UI primitives replace one-off implementations.
- [ ] Critical pages pass viewport and interaction checks.
- [ ] Loading, empty, error, stale, and permission states are present.
- [ ] UI actions explain risk and permission.
- [ ] Page-level e2e tests cover primary workflows.

## Phase 8: Observability, Audit, and Operations

### Goal

Make the management plane and managed clusters observable, debuggable, and auditable.

### Reasoning

Rancher-quality platforms give operators clear answers: what changed, who did it, what is broken, and what to do next.

### Tasks

- [x] Standardize structured logs:
  - [x] request id
  - [x] actor id
  - [x] cluster id
  - [x] operation id
  - [x] task id
  - [x] trace id
- [x] Standardize metrics:
  - [x] HTTP latency and errors
  - [x] DB pool
  - [x] Redis/asynq queue depth
  - [x] task retries
  - [x] agent heartbeats
  - [x] tunnel sessions
  - [x] Argo sync state
  - [x] upstream Argo CD API request count/latency/error metrics
  - [x] Kubernetes proxy errors
  - [x] audit write failures
- [x] Add OpenTelemetry traces:
  - [x] API request
  - [x] DB query
  - [x] background task
  - [x] agent tunnel request
  - [x] Argo request
  - [x] Kubernetes request
- [ ] Add operation history everywhere:
  - GitOps sync
  - cluster registration
  - agent upgrade
  - node action
  - workload action
  - backup/restore
  - policy apply
- [x] Add audit search UI:
  - [x] actor
  - [x] target
  - [x] action
  - [x] cluster
  - [x] project
  - [x] result
  - [x] time range
  - [x] correlation id
  - [x] request id
  - [x] CSV export
  - [x] row detail inspection
  - [x] audit-log RBAC route tests
- [x] Add support bundle:
  - [x] management plane version
  - [x] Helm release metadata with release payload and values excluded/redacted
  - [x] pod status
  - [x] logs redacted
  - [x] DB migration version
  - [x] Argo health
  - [x] agent fleet status
  - [x] NetworkPolicy summary
  - [x] ingress/cert status
- [ ] Add runbook linking from degraded states.

### Testing Requirements

- Unit tests for audit event builders.
- Tests proving audit write failures do not silently pass for high-risk actions.
- Metrics endpoint tests.
- Trace propagation tests where feasible.
- [x] Support bundle redaction tests.
- E2E test for audit search after performing an action.

### Definition of Done

- [ ] Operators can answer who/what/when/where for high-risk actions.
- [ ] Degraded states link to runbooks.
- [ ] Agent, Argo, DB, Redis, and server health are visible.
- [x] Support bundles are safe to share with authorized support channels without plaintext credentials, tokens, private keys, kubeconfigs, raw cert/key bytes, or sensitive log lines.

## Phase 9: Platform Robustness, HA, Backup, and Restore

### Goal

Make Astronomer reliable under real production conditions.

### Reasoning

Rancher-quality management must survive upgrades, restarts, partial failures, and disaster recovery.

### Tasks

- [ ] Define supported topologies:
  - single-node dev k3s
  - small production
  - HA production
  - air-gapped
- [ ] Add HA chart values:
  - server replicas
  - worker replicas
  - PodDisruptionBudgets
  - anti-affinity
  - resource requests/limits
  - HPA where appropriate
- [ ] Validate Postgres:
  - external Postgres support
  - CNPG support
  - backups
  - point-in-time recovery
  - connection pool sizing
  - migration lock behavior
- [ ] Validate Redis:
  - external Redis support
  - persistence expectations
  - queue recovery
  - DLQ behavior
- [ ] Add backup/restore drills:
  - app DB
  - Kubernetes Secrets
  - Argo resources
  - CRDs
  - Helm values
  - restore into clean cluster
- [ ] Add upgrade drills:
  - chart upgrade
  - app rollback
  - DB migration rollback policy
  - agent upgrade
  - Argo upgrade
- [ ] Add readiness/liveness:
  - DB
  - Redis
  - tunnel hub
  - Argo optional dependency
  - migration status
- [ ] Add startup validation:
  - required secrets
  - cookie/session secret
  - bootstrap admin
  - external URLs
  - ingress domain
  - TLS config
  - NetworkPolicy assumptions

### Testing Requirements

- Helm render tests for dev and production values.
- Fresh cluster smoke test.
- Upgrade smoke test.
- Backup/restore drill in CI or scheduled workflow.
- Chaos tests:
  - server pod restart
  - worker restart
  - Redis unavailable
  - Postgres unavailable
  - Argo unavailable
  - agent disconnect
  - ingress/cert failure
- Load tests using existing `scripts/loadtest` profiles.

### Definition of Done

- [ ] HA values are documented and tested.
- [ ] Backup/restore is proven.
- [ ] Upgrade/rollback procedure is proven.
- [ ] Readiness reflects real dependency health.
- [ ] Failure modes are visible and recoverable.

## Phase 10: Code Quality, Dead Code, and Duplicate Removal

### Goal

Make the codebase easy to extend without accumulating hidden risk.

### Reasoning

Elite product quality depends on low-friction code. Duplicate API clients, duplicate table logic, ad hoc Kubernetes parsing, and stale compatibility paths slow every feature and hide bugs.

### Backend Tasks

- [x] Define package ownership:
  - `internal/server`
  - `internal/handler`
  - `internal/auth`
  - `internal/rbac`
  - `internal/agent`
  - `internal/tunnel`
  - `internal/gitops`
  - `internal/monitoring`
  - `internal/db`
  - `internal/worker`
- [x] Move shared Kubernetes helpers into one package:
  - [x] GVK/GVR metadata for built-in Argo CD and core ConfigMap resources
  - [x] namespaced/name helpers for controller-runtime lookups
  - [x] RESTMapper/discovery helpers
  - [x] unstructured object helpers for constructors and spec-hash annotations
  - [x] dry-run/apply helpers for server-side apply query/header construction
  - [x] delete option helpers
- [x] Move shared audit builders into one package.
- [x] Move shared operation-state helpers into one package.
- [x] Remove unused handlers and routes.
- [x] Remove unused SQL queries after verifying no generated references.
- [x] Consolidate duplicated config loading.
- [x] Consolidate token hashing/verification.
- [x] Consolidate redaction logic:
  - [x] shared package for diagnostic bundles and support bundles
  - [x] migrate or explicitly document specialized CloudCredential, Vault, and audit redaction paths
- [x] Replace stringly typed operation states with constants/types.
- [x] Add context deadlines for external calls.
- [x] Add idempotency keys to the common high-risk operation creation paths:
  ArgoCD, tool, catalog, logging, monitoring, workload, fleet, restore,
  deferred, and agent lifecycle operations now have idempotent helpers and
  handler usage recorded in
  `docs/rancher-quality-phase0-operation-task-inventory.md`.
- [x] Add `Idempotency-Key` support to agent lifecycle operation creation before
  treating fleet-scale agent upgrades as complete.
- [x] Disable the unhandled `compliance:export` async branch until a durable
  worker/status/output path exists.
- [ ] Add durable background compliance exports only if product requirements
  need them: registered worker, persisted job state, object-storage output,
  resumable/pollable status, and tests for server restart behavior.
- [x] Classify every direct enqueue row in
  `docs/rancher-quality-phase0-operation-task-inventory.md` as outbox-backed,
  operation-backed, repair-backed, intentionally best-effort, or wrapper-only.
- [x] Promote best-effort cleanup exceptions to durable delivery where
  product-critical: registry secret unapply now uses the atomic
  `DeleteClusterRegistryConfigByIDWithTaskOutbox` helper, and project namespace
  apply/cleanup fallback paths write `task_outbox` before direct queue fallback.
- [x] Ensure generated sqlc files match queries.

### Frontend Tasks

- [x] Consolidate API client functions under `frontend/src/lib/api`.
- [x] Remove duplicate API request types.
- [x] Generate or validate types from OpenAPI where practical.
- [x] Consolidate tables.
- [x] Consolidate status badges.
- [x] Consolidate drawer/dialog components.
- [x] Consolidate auth/session state.
- [x] Remove unused components.
- [x] Remove unused CSS classes.
- [x] Remove duplicate Playwright fixtures.
- [x] Eliminate one-off fetch wrappers.
- [x] Standardize React Query keys.
  - [x] Argo instance/application/ApplicationSet/project/repo/managed-cluster/operation/cluster-ownership keys use the shared `queryKeys.argocd` factory.
  - [x] Logging, cluster-group, and Vault connection admin pages use shared query keys.
  - [x] Agent Fleet, Extensions, and Settings Operations queues/DLQ/outbox pages use shared query keys.
  - [x] Cluster overview and core cluster tab pages use shared `queryKeys.clusterPages` factories.
  - [x] Generated inventory shows no remaining frontend app-route inline/page-local query-key candidates.
- [x] Standardize error handling and toast behavior.

### Helm/Deployment Tasks

- [x] Remove unused top-level values or verify none remain through the code-health inventory.
- [x] Add values schema.
- [x] Validate production-required values.
- [x] Avoid Helm hooks for long-lived resources.
- [x] Ensure ServiceAccount/RBAC are normal managed resources.
- [x] Ensure bootstrap secret behavior is documented and tested.
- [x] Ensure image tags and pull policy are consistent.
- [x] Ensure NetworkPolicies are complete.

### CI Quality Gates

- [x] `go test ./...`
- [x] selected `go test -race`
- [x] `go vet ./...`
- [x] `golangci-lint run ./...`
- [x] `govulncheck ./...`
- [x] `npm run lint`
- [x] `npm run type-check`
- [x] `npm test -- --runInBand`
- [x] `npm audit --audit-level=moderate`
- [x] `npm run code-health`
- [x] `npm run test:e2e` for smoke paths
- [x] `helm lint deploy/chart`
- [x] development and production `helm template`
- [x] `git diff --check`
- [ ] migration safety tests
- [ ] route security tests
- [x] OpenAPI validation

### Definition of Done

- [ ] Duplicate logic is either removed or justified.
- [ ] Dead code is removed.
- [ ] Shared abstractions exist only where they reduce real duplication.
- [ ] New features follow package ownership boundaries.
- [ ] CI catches formatting, tests, security route gaps, and Helm render issues.

## Phase 11: Testing Strategy

### Goal

Define enough tests to move fast without shipping hidden platform regressions.

### Test Pyramid

Backend unit tests:

- auth
- RBAC
- audit builders
- Kubernetes object helpers
- Argo label projection
- CRD validation
- operation state machines
- redaction
- token hashing

Backend handler tests:

- login
- sessions
- cluster registration
- Argo adoption
- Kubernetes proxy
- service proxy
- logs
- exec/shell tickets
- agent lifecycle
- RBAC role assignment
- secret reads
- destructive actions

Backend integration tests:

- Postgres migrations
- sqlc queries
- Redis/asynq task behavior
- Argo local integration
- Kubernetes envtest or fake API
- backup/restore flows

Frontend unit tests:

- API client behavior
- auth store
- permission gates
- shared table
- YAML editor
- status badges
- operation timeline
- error handling

Frontend e2e tests:

- login/logout
- cluster list
- cluster detail
- pods page
- workload scale/restart
- logs
- GitOps baseline status
- agent diagnostics
- RBAC assignment
- permission denied
- audit search

Security tests:

- unauthenticated denial
- unauthorized denial
- CSRF denial
- route registry coverage
- proxy header stripping
- SSRF blocking
- stream ticket expiration
- token revocation
- secret redaction

Performance tests:

- 100 clusters
- 1,000 clusters
- 10,000 namespaces
- 100,000 pods synthetic inventory
- reconnect storm
- list/watch load
- Argo ApplicationSet fanout
- frontend table performance

Upgrade tests:

- fresh install
- upgrade from previous chart
- rollback app image
- migration failure handling
- agent version skew
- CRD conversion

### Required Test Commands

Before merging large platform changes:

```sh
go test ./...
go test -race ./internal/auth ./internal/rbac ./internal/server ./internal/agent ./internal/tunnel
go vet ./...
golangci-lint run ./...
govulncheck ./...
cd frontend && npm run lint
cd frontend && npm run type-check
cd frontend && npm test -- --runInBand
cd frontend && npm audit --audit-level=moderate
cd frontend && npm run code-health
helm lint deploy/chart
helm template astronomer deploy/chart --namespace astronomer
git diff --check
```

Before release:

```sh
cd frontend && npm run test:e2e
make load-test
```

### Definition of Done

- [ ] Every high-risk backend route has negative and positive tests.
- [ ] Every critical UI workflow has e2e coverage.
- [ ] Every CRD has schema and reconcile tests.
- [ ] Every migration has forward safety coverage.
- [ ] Load tests have published baseline reports.
- [x] CI blocks regressions in auth, proxy, route security, Helm render, and type safety.

## Phase 12: Documentation and Operator Experience

### Goal

Make operators successful without reading code.

### Tasks

- [ ] Update install docs:
  - k3s
  - production
  - air-gapped
  - external Postgres
  - external Redis
  - ingress-nginx
  - cert-manager
  - bootstrap admin password
  - OIDC
- [ ] Update Argo docs:
  - built-in Argo
  - external Argo
  - cluster registration
  - ApplicationSet targeting
  - component baseline
  - drift handling
  - rollback
- [ ] Update agent docs:
  - install
  - privilege profiles
  - upgrade
  - diagnostics
  - token rotation
  - offline behavior
- [ ] Update CRD docs:
  - examples
  - versioning
  - status conditions
  - finalizers
  - restore behavior
- [ ] Update security docs:
  - threat model
  - secret handling
  - audit schema
  - network policies
  - image verification
  - compliance
- [ ] Update runbooks:
  - pods page fails
  - agent disconnected
  - Argo degraded
  - ApplicationSet not targeting
  - certificate stuck
  - DB unavailable
  - Redis queue backlog
  - migration failed
  - backup restore failed
  - token rotation
  - user locked out

### Definition of Done

- [ ] New operator can install and access the product from docs.
- [ ] New operator can adopt a cluster from docs.
- [ ] New operator can deploy baseline components through Argo from docs.
- [ ] New operator can recover from common degraded states from docs.
- [ ] Docs include commands that match current chart and API behavior.

## Release Readiness Checklist

Use this checklist before calling the product Rancher-quality for adopted clusters.

### Product

- [ ] Adopted clusters automatically appear in inventory.
- [ ] Adopted clusters automatically register into built-in Argo CD when enabled.
- [ ] Baseline components deploy through Argo CD.
- [ ] Cluster Explorer supports common day-2 workflows.
- [ ] Agent fleet status and upgrades are first-class.
- [ ] RBAC is clear, scoped, and enforced server-side.
- [ ] Audit logs answer who did what.
- [ ] UI handles loading, empty, error, stale, and permission states.

### Architecture

- [ ] Postgres stores product state and durable intent.
- [ ] CRDs expose declarative Kubernetes-facing management APIs.
- [ ] Argo CD owns deployment convergence.
- [ ] Target clusters own workload state.
- [ ] Controllers/workers converge long-running operations.
- [ ] Agent performs target-cluster local work with least privilege.

### Security

- [x] Email-only login is enforced.
- [x] MFA/OIDC path exists.
- [x] Browser sessions are hardened.
- [x] CSRF is enforced for registry-marked browser-mutating routes.
- [x] Proxy paths strip dangerous headers.
- [x] Tokens are hash-only or encrypted.
- [x] Secret reads are gated and audited.
- [x] NetworkPolicy is production-ready.
- [x] Images have SBOMs and signatures.

### Quality

- [x] No known dead routes.
- [x] No known unused major components.
- [ ] Duplicate API clients are consolidated.
- [ ] Duplicate table/action patterns are consolidated.
- [x] Helm values have schema and production validation.
- [ ] OpenAPI is current.
- [ ] Migrations are safe and tested.
- [ ] CI gates pass.

### Operations

- [ ] Backup/restore drill passes.
- [ ] Upgrade drill passes.
- [ ] HA topology is documented and tested.
- [ ] Load baseline is documented.
- [ ] Runbooks cover common failures.
- [ ] Support bundle is redacted and useful.

## Milestones

### Milestone A: Safety and Inventory

Deliver:

- route inventory
- proxy inventory
- UI inventory
- duplicate/dead-code backlog
- threat model update
- route security coverage improvements

Exit criteria:

- every high-risk route has known auth/RBAC/audit posture
- every proxy path has tests for dangerous header stripping
- dead-code cleanup tasks are actionable

### Milestone B: Argo Fleet-Equivalent Core

Deliver:

- periodic Argo cluster Secret reconciliation
- ApplicationSet templates
- baseline component model
- drift UI
- ownership conflict workflow

Exit criteria:

- newly adopted cluster gets baseline components through Argo without manual steps
- label changes update targeting reliably
- drift and errors are visible in the UI

### Milestone C: Agent Fleet Core

Deliver:

- agent capability contract
- compatibility matrix
- diagnostics bundle
- token rotation
- upgrade batches

Exit criteria:

- operators can see and upgrade the agent fleet safely
- disconnected/degraded agents are explainable
- diagnostics are redacted

### Milestone D: Cluster Explorer Depth

Deliver:

- shared resource table/detail/YAML editor
- workload/pod/node/storage/service actions
- CRD/custom resource explorer
- logs/exec/shell permission improvements

Exit criteria:

- common day-2 workflows work without kubectl
- destructive actions are previewed and audited
- UI is consistent across resource kinds

### Milestone E: Enterprise Hardening

Deliver:

- RBAC scope completion
- MFA/OIDC completion
- HA validation
- backup/restore drill
- load tests
- docs/runbooks

Exit criteria:

- production install is documented and tested
- upgrade/restore is proven
- security and quality gates pass

## Review Cadence

Weekly engineering review:

- open phase tasks
- blocked decisions
- security-sensitive changes
- test coverage deltas
- UI consistency deltas
- duplicate/dead-code backlog

Release review:

- release readiness checklist
- migration safety
- rollback plan
- known risks
- docs readiness
- support/runbook readiness

Security review:

- threat model changes
- new proxy/tunnel paths
- new auth/session behavior
- new secret storage
- new RBAC grants
- dependency vulnerabilities

UX review:

- critical path screenshots
- mobile/tablet/desktop checks
- keyboard navigation
- permission-disabled states
- destructive action flows
- text overflow and layout checks

## Risk Register

| Risk | Impact | Mitigation |
|---|---|---|
| Postgres used for raw Kubernetes state | stale/inconsistent state | keep raw object state in cluster API; store summaries and intent only |
| Argo cluster labels drift | wrong components deployed | periodic reconciliation and drift alerts |
| Agent overprivileged by default | cluster compromise blast radius | privilege profiles and least-privilege defaults |
| Proxy forwards caller headers | credential leak or impersonation | header stripping tests and route registry |
| UI hides but API allows action | privilege escalation | server-side RBAC tests |
| CRD schema changes break users | upgrade failure | versioning and conversion tests |
| Duplicate UI patterns persist | slow feature development | shared components and lint/review checklist |
| Long-running operations hidden | operator confusion | operation rows and timelines |
| Backup misses Kubernetes/Argo state | incomplete restore | documented restore bundle and drills |
| Reconnect storm overloads server | outage after network event | jitter, backpressure, load tests |

## Final Definition of Done

Astronomer can be called Rancher-quality for the intentionally scoped product when all of the following are true:

- Existing clusters can be adopted reliably.
- Adopted clusters are registered into built-in or configured Argo CD automatically.
- Baseline components deploy and reconcile through Argo CD.
- ApplicationSets provide Fleet-equivalent cluster targeting.
- Cluster Explorer covers the daily Kubernetes operational workflows.
- Agent fleet management is visible, upgradeable, diagnosable, and secure.
- RBAC is server-enforced, scoped, test-covered, and explainable in the UI.
- Every high-risk operation is auditable and recoverable.
- Postgres, CRDs, Argo, and target-cluster Kubernetes APIs have clear ownership boundaries.
- Production install supports HA, backup, restore, upgrade, and rollback.
- Security checks are part of CI and release review.
- UI is consistent, dense, responsive, and tested across critical workflows.
- Dead code and duplicate code have been removed or explicitly justified.
- Documentation and runbooks match real deployed behavior.
