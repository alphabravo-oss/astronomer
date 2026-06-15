# Rancher-Quality Phase 0 Quality Backlog

Date: 2026-06-14
Status: Active backlog derived from Phase 0 inventories and the master plan.

This backlog tracks the remaining quality work needed to keep Astronomer moving
toward Rancher-grade day-2 operations without adding cluster provisioning.

## P0 Security And Safety

- Completed route metadata for every mounted API route, not only high-risk and
  mutating routes.
  - Owner: backend/server.
  - Evidence: `docs/generated-route-inventory.json` has 405 rows, zero missing
    posture fields, zero generic handler owners, 262 high-risk registry rows,
    and 3 classified auth-flow rows.
  - Done: every row has auth, RBAC, CSRF, audit posture, handler owner, and
    representative tests, enforced by
    `TestRouteInventoryEntriesHaveCompleteSecurityPosture`.

- Finish high-risk audit coverage.
  - Owner: backend/handlers.
  - Evidence: `docs/security-sensitive-routes.json` has zero entries whose
    audit field says coverage remains open after closing the legacy
    Fleet-named operation audit gap, resource/node operations, RBAC
    global-role / role-binding, generic SSO provider, cluster registry, Dex
    configuration, Vault connection, singleton policy/network, workload
    mutation, project Vault/policy/catalog, project CRUD/namespace, cluster
    group, cluster snapshot/template, catalog installed, and backup audit
    batches.
  - Done when high-risk mutators emit explicit audit events with no request or
    response secrets.

- Decide and document specialized redactor ownership.
  - Owner: security/platform.
  - Evidence: shared `internal/redaction` now covers diagnostics/support
    bundles, while CloudCredential, Vault, and audit redactors remain
    specialized.
  - Done when each specialized redactor either wraps the shared package or has
    a documented stronger reason not to.

- Add CI gates for secret-looking schema columns.
  - Owner: database/security.
  - Evidence: `docs/secret-column-inventory.md`,
    `docs/secret-handling-policy.md`, and
    `internal/db/migrations/migration_secret_columns_test.go`.
  - Status: complete; PR validation runs `go test ./...`, which includes
    `TestSecretLikeMigrationColumnsAreClassified` and fails new
    secret-looking migration columns without explicit classification.

## P0 Reliability

- Verify every high-risk operation has durable operation state and idempotency.
  - Owner: backend/worker.
  - Evidence: `097_operation_idempotency_keys` exists, and
    `docs/rancher-quality-phase0-operation-task-inventory.md` now records task
    handlers, schedules, enqueue points, task-outbox producers, durable
    operation tables, and idempotency helper coverage.
  - Current gaps: durable operation helpers now cover the generated operation
    inventory, and direct enqueue call sites are classified in
    `docs/direct-enqueue-classifications.json`.
  - Done when operation creators use idempotency keys and durable outbox/task
    intent where Redis delivery loss would be user-visible.

- Add automated worker task inventory.
  - Owner: worker/platform.
  - Evidence: `scripts/operation-task-inventory.mjs`,
    `docs/rancher-quality-phase0-operation-task-inventory.md`, and
    `frontend/package.json` `code-health`.
  - Status: complete; `npm run code-health` now verifies the generated
    operation/task inventory is current and registered/scheduled task
    expressions remain parseable.

- Fix or remove the async compliance export task path.
  - Owner: backend/compliance.
  - Evidence: the broken `compliance:export` enqueue path is disabled;
    `docs/rancher-quality-phase0-operation-task-inventory.md` now reports no
    task types created or scheduled without a registered handler.
  - Status: complete for safety; future background compliance exports must add
    a registered worker, durable job table, durable output storage, and polling
    API before re-enabling 202 responses.

- Classify and close direct enqueue durability findings.
  - Owner: worker/platform plus feature owners.
  - Evidence: `docs/direct-enqueue-classifications.json` is enforced by
    `scripts/operation-task-inventory.mjs`, and
    `docs/rancher-quality-phase0-operation-task-inventory.md` now reports zero
    unclassified direct enqueue call sites.
  - Status: complete for classification; new direct enqueue rows fail
    `npm run code-health` until classified.

- Replace best-effort cleanup-only direct enqueue exceptions with durable
  delivery.
  - Owner: backend/registry and backend/projects.
  - Evidence: registry unapply delete cleanup uses
    `DeleteClusterRegistryConfigByIDWithTaskOutbox`; project namespace apply
    and cleanup fallback paths write `task_outbox` before direct queue fallback.
  - Status: complete for the generated direct-enqueue inventory.

- Close remaining Argo ownership conflict operations.
  - Owner: GitOps.
  - Evidence: master plan Phase 2 still lists adopt/replace migration execution
    as open.
  - Done when unsafe replacement is blocked, safe adoption/replace has a
    confirmable operation path, and outcomes are audited.

## P1 UI Quality

- Build shared page/action primitives for cluster, Argo, backup, RBAC, audit,
  and settings pages.
  - Owner: frontend.
  - Evidence: 98 `page.tsx` routes, with settings and clusters as the largest
    families.
  - Done when common loading, error, empty, stale, permission, destructive
    confirmation, and operation-progress states are shared.

- Add page-level e2e coverage for primary workflows.
  - Owner: frontend/QA.
  - Priority order: cluster detail, Argo application sync/rollback, audit
    search/export, RBAC effective permissions, backup/restore, support bundle.
  - Done when each workflow has a smoke path plus permission-denied behavior.

- Add viewport checks for dense operational screens.
  - Owner: frontend/design.
  - Done when cluster detail, Argo instance, audit, RBAC, settings, and backup
    pages pass desktop and mobile screenshots without clipped or overlapping
    controls.

## P1 Observability

- Finish standardized metrics inventory.
  - Owner: platform/observability.
  - Evidence: DB, worker, queue, tunnel, agent, Vault, GitOps, and Argo client
    metrics exist; HTTP server latency/error and audit-write failure metrics
    need explicit verification.
  - Done when each metric in Phase 8 has a metric name, owner, alert/runbook,
    and test or scrape validation.

- Link degraded states to runbooks.
  - Owner: backend/frontend.
  - Evidence: support bundle and Agent Fleet diagnostics expose degraded
    states, but not all UI surfaces link directly to runbooks.
  - Done when major degraded states include a stable runbook URL or action.

## P1 CRD And GitOps

- Add durable operation rows for CRD writers.
  - Owner: CRD/GitOps.
  - Evidence: `docs/crd-api.md` notes durable operation rows remain open for
    `ClusterBaseline` and `GitOpsTarget`.
  - Done when generated Argo `ApplicationSet` create/repair/delete actions can
    be correlated with operation history and audit.

- Finalize CRD conversion policy before beta.
  - Owner: CRD/API.
  - Evidence: all management CRDs remain `v1alpha1`.
  - Done when conversion webhooks or storage-version policy is documented and
    tested before any `v1beta1` API is served.

## P2 Code Health

- Process duplicate-detection candidates from the generated inventory.
  - Owner: platform.
  - Evidence: `docs/rancher-quality-phase0-code-health-inventory.md` and
    `npm run code-health`.
  - Initial candidate groups: inline/page-local React Query keys, duplicated
    unexported Go helper names, Kubernetes resource flatteners, audit builders,
    operation-state helpers, config loading, and token hashing/verification.
  - Completed cleanup: page-local API error `ResponseShape` parsers were
    consolidated into `frontend/src/lib/api/errors.ts`.
  - Completed cleanup: Argo React Query keys for instance, application,
    ApplicationSet, project, repository, managed-cluster, operation, and
    cluster-ownership surfaces were consolidated into `queryKeys.argocd`.
  - Completed cleanup: logging, cluster-group, and Vault connection admin
    pages were moved to shared query keys.
  - Completed cleanup: Agent Fleet diagnostics/operations, Extensions, and
    Settings Operations queues/DLQ/outbox pages were moved to shared query keys.
  - Completed cleanup: cluster overview plus Apps, Image Scans, Network &
    Access, Registries, Resources, Service Mesh, mTLS, Snapshots, Templates,
    and Workloads tabs were moved to shared `queryKeys.clusterPages` factories.
  - Completed cleanup: frontend lint now runs without warnings after catalog
    image, hook dependency, sidebar navigation, pod-log container, and cluster
    resource action-column fixes.
  - Completed cleanup: page-local `ResponseShape` types and app-route
    inline/page-local React Query keys are now `npm run code-health` hard
    gates.
  - Completed cleanup: duplicated tunnel pod exec/log stream authentication
    wrappers were consolidated into `internal/tunnel/authenticateStreamRequest`
    with tests for stream kind and cluster scoping.
  - Completed cleanup: authenticated-user UUID conversion is centralized in
    `middleware.AuthenticatedUserUUID` and reused by handler and audit
    middleware code paths.
  - Completed cleanup: backend duplicate-helper detection now ignores
    same-named receiver methods and Go `init` functions so the inventory tracks
    package-level helper candidates; duplicated CIS report JSON helpers moved
    into `internal/scanner`.
  - Completed cleanup: HTTPS request detection is centralized in
    `middleware.RequestIsHTTPS` and reused by auth cookies and security
    headers.
  - Completed cleanup: event payload JSON normalization is centralized in
    `events.RawJSON` and reused by SIEM and webhook taps.
  - Completed cleanup: event filter JSONB glob decoding is centralized in
    `events.DecodeFilterGlobs` and reused by SIEM and webhook taps; the
    logging handler keeps its local parser because it decodes a different
    structured filter shape.
  - Completed cleanup: duplicated string fallback helpers were removed from
    compliance, dashboards, and observability in favor of standard-library
    `cmp.Or`; the handler package keeps one local `defaultString` helper for
    its many same-package call sites.
  - Completed cleanup: first non-blank string selection is centralized in
    `internal/strutil` with separate preserve-original and trimmed variants;
    raw first non-empty string fallbacks use standard-library `cmp.Or`.
  - Completed cleanup: same-named duration parsers were renamed to
    `parsePromDuration` and `parseSnapshotTTLDuration` because Prometheus
    dashboard durations and Velero snapshot TTLs have different parse
    contracts.
  - Completed cleanup: same-named truncation helpers were renamed to
    contract-specific names because Prometheus error bodies, kubectl session
    responses, SIEM event payloads, and dispatcher `last_error` caps have
    different suffix and marker semantics.
  - Completed cleanup: unrelated outbound auth builders were renamed to
    `buildSMTPAuth` and `buildGitAuth` because SMTP mechanisms and go-git
    transport auth methods are different protocols.
  - Completed cleanup: Argo CD project membership lookup is centralized in
    `argolabels.ProjectsForCluster` and reused by the handler plus
    auto-registration and label-refresh workers.
  - Completed cleanup: unrelated secret apply helpers were renamed to
    `applyAlertSecret` and `applyLocalArgoSecret`; the generated inventory now
    reports zero duplicate-code candidates.
  - Done when candidates marked `needs investigation` are either removed,
    intentionally kept, or consolidated behind named target abstractions.

- Process dead-code candidates from the generated inventory.
  - Owner: platform.
  - Evidence: `docs/rancher-quality-phase0-code-health-inventory.md` and
    `npm run code-health`.
  - Completed cleanup: component dead-code detection now resolves absolute,
    relative, and dynamic component imports; the unused legacy `ClusterCard`
    and `BottomPanel` components and obsolete dashboard portal root were
    removed.
  - Completed cleanup: sqlc dead-code detection now searches all
    non-generated Go files across the repo, including CLI and tests, instead
    of only production `internal/` files.
  - Completed cleanup: removed the first unused sqlc query batch from
    canonical query files and generated/manual sqlc surfaces: unused agent
    connection get/count/delete helpers, unused cluster tool create/delete
    helpers, and the unused project catalog subscription count helper.
  - Completed cleanup: removed a second unused sqlc query batch covering
    chart-version, SMTP message/status, cluster-group, SSO, TOTP, and
    network-policy status count/read helpers.
  - Completed cleanup: removed a third unused sqlc query batch covering
    anomaly, API-server allowlist, API token, Argo application, cloud
    credential materialization, cluster condition, fleet operation, restore
    operation, and security scan delete helpers.
  - Completed cleanup: removed a fourth unused sqlc query batch covering
    single-row CRD mirror getters for ingress classes, GatewayClasses,
    NetworkPolicies, ResourceQuotas, and LimitRanges.
  - Completed cleanup: removed a fifth unused sqlc query batch covering
    logging enabled-by-cluster and pipeline/output join-table helpers.
  - Completed cleanup: removed a sixth unused sqlc query batch covering
    broad-list/count helpers for API tokens, agent connections,
    API-server allowlists, and cloud credentials.
  - Completed cleanup: removed a seventh unused sqlc query batch covering
    alerting alternate filter/read helpers for active rules, reverse channel
    lookup, cluster/firing event filters, and active silence reads.
  - Completed cleanup: removed an eighth unused sqlc query batch covering
    catalog/tool alternate lookups, category filters, tool update helpers,
    chart update helpers, and installed-chart-by-tool listing.
  - Completed cleanup: removed a ninth unused sqlc query batch covering
    backup/restore alternate status/list helpers and older
    security/pod-security status helpers superseded by current
    applied/report/failure-with-message paths.
  - Completed cleanup: removed a tenth unused sqlc query batch covering
    non-RBAC helpers for Argo applications/proxy tokens, backup drill inserts,
    cluster template reverse lookups, cluster token revocation, Fleet
    failed-target counts, GitOps registration reads, monitoring backend lists,
    network-policy application lists, quota setters, and Vault token-expiry
    updates.
  - Completed cleanup: removed the final unused sqlc query batch covering
    legacy RBAC role-by-name, binding-by-user/group, and per-user role join
    helpers superseded by the active `ListUserBindingsWithRoles`
    authorization query.
  - Done when candidate SQL queries, component files, Helm values, and stale
    compatibility paths are removed or explicitly retained with compatibility
    reasons.

- Consolidate stringly typed states.
  - Owner: backend.
  - Targets: operation statuses, task states, Argo sync/health states, agent
    compatibility states, audit result states.
  - Done when shared constants/types prevent typo-driven state drift.

- Harden Helm values validation.
  - Owner: deployment/platform.
  - Evidence: `deploy/chart/values.schema.json`, `go test ./deploy`,
    `helm lint deploy/chart`, and dev/production `helm template`.
  - Completed cleanup: chart values now have schema-backed type checks plus
    production conditional requirements for external Postgres/Redis, Gateway
    host, TLS, real secrets, bootstrap email, Dex client secret, and backup S3
    wiring.
  - Completed cleanup: production schema now also requires default-deny
    NetworkPolicy plus explicit external Postgres and Redis egress CIDRs, so a
    production install cannot render successfully while blocking its own DB or
    cache dependencies.
  - Done when future production-only values are either represented in the
    schema or explicitly documented as free-form operator overrides.

- Lock the Helm operational resource contract.
  - Owner: deployment/platform.
  - Evidence: `deploy/chart_operational_contract_test.go`,
    `go test ./deploy`, and `deploy/chart/README.md`.
  - Completed cleanup: bundled Postgres/Redis and BusyBox wait containers now
    declare `imagePullPolicy`, and render tests prove only short-lived
    migrate/preflight Jobs use Helm hooks, ServiceAccount/RBAC remain normal
    managed release resources, bootstrap Secret/env wiring is stable, and
    rendered workload containers declare pull policies.
  - Completed cleanup: NetworkPolicy render tests now cover the default-deny
    policy, expected frontend/server/worker/Dex/Postgres/Redis component
    policies, no egress from bundled Postgres/Redis pods, no broad production
    external egress, and absence of bundled DB/cache policies when production
    uses external dependencies.
  - Done when future chart resources continue to satisfy those render tests or
    add explicit replacement tests for any intentional exception.

## Tracking Rules

- New backlog items should cite a route, file, plan section, test, metric, or
  user-visible workflow.
- P0 items block a production-hardening release.
- P1 items block the Rancher-quality claim.
- P2 items can be batched, but each batch must preserve tests and avoid
  unrelated refactors.
