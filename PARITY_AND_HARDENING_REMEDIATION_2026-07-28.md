# Parity and Hardening Remediation Plan

**Date:** 2026-07-28
**Scope:** Rancher parity (excluding cluster provisioning / node drivers and Fleet), agent quality, transport security, crypto/TLS/secret management, ArgoCD subsystem, code health.
**Status:** PROPOSAL — nothing here is approved work.

---

## How to use this document

There are two readers.

**Human owner.** Every `###` subsection is one proposal. Review it, then mark it `APPROVED` or `REJECTED` inline (edit the "Status:" line under the heading, which currently reads `PENDING APPROVAL`). Severities were assigned by an adversarial verification pass and have not been softened; where the verifier corrected the original claim, the correction is folded into the text and flagged inline with **[corrected]**. Several claims that looked like defects were refuted outright and are parked in Appendix A so nobody re-investigates them.

**Executing AI agent.** Do not start work on any item whose Status line still reads `PENDING APPROVAL`. When an item is approved:

- Every file path, line number, function name, and test name you need is in this document. You are not expected to have the audit's context.
- Line numbers are as of 2026-07-28 on the current tree. Re-anchor by symbol name if they have drifted; do not trust a line number over a symbol.
- Each item lists the tests that **must fail before your change and pass after**. Write and run the failing test first. An item is not done until its acceptance checkboxes are all satisfiable.
- Where two finding IDs are listed on one heading, they were the same defect observed from two dimensions; fix once.
- Run the full gate (`make verify` plus the frontend suite) before declaring an item done, and fix what it surfaces rather than deferring it as pre-existing.

---

## 1. Executive summary

### (a) Do we match or beat Rancher outside provisioning and Fleet?

**Qualified no.** We beat Rancher on agent privilege confinement, proxy header policy, audit enforcement, token scoping, Helm values UX, the operations queue, and image vulnerability scanning (§3). But three core RBAC semantics are wrong in ways a Rancher-trained evaluator hits in the first hour: cluster and project LIST return everything or 403 with no per-object filtering (`cluster-project-list-unfiltered`), project role bindings grant nothing on cluster resources unless an off-by-default flag is set (`project-bindings-inert-by-default`), and the exec/logs WebSockets gate on `clusters:update`/`clusters:read` instead of `pods:exec`/`pods:logs`, so the four shipped diagnostics roles cannot exec while any `clusters:update` holder can (`exec-logs-verb-mismatch`). Structurally, there is no downstream per-user impersonation at all (`no-downstream-impersonation`) — Rancher's is the reference implementation and ours is absent, which means the control plane's own path parsing is the only per-user control and downstream audit cannot attribute an action to a human. On features, the managed monitoring stack has a complete backend and zero UI (`monitoring-stack-lifecycle-no-ui`), CIS scans cannot be scheduled (`no-scheduled-cis-scans`), and Helm release history/rollback has no UI (`no-release-history-or-revision-rollback-ui`).

### (b) Is the agent elite?

**No.** It is competent and its privilege model is genuinely better than Rancher's, but the transport has four structural defects that a large cluster will find: a 256-slot send channel where any dropped data frame force-closes the whole tunnel while unpaced bulk producers feed it (`send-failclose-livelock`), unbounded goroutine-per-message dispatch each able to buffer 64 MiB against a 512Mi limit (`unbounded-handler-goroutines`), unpaginated cluster-wide pod/node LISTs every 30s served from etcd rather than the watch cache (`heartbeat-full-cluster-list-storm`), and self-upgrade that accepts any image string, reports success before the rollout happens, and never uses the rollback image it computes — with `strategy: Recreate`, a bad image takes the cluster dark with no remote recovery (`self-upgrade-no-verification-no-rollback`). Secondary: full-object informer caches including Secret data (`informer-full-object-caches`), no read/write deadlines (`agent-no-read-idle-deadline`), and silent U+FFFD corruption of binary exec/log payloads (`exec-and-log-binary-corruption`).

### (c) Are TLS, keys, and secrets correct and system-managed?

**Qualified no.** The at-rest scheme is sound — Fernet encrypt-then-MAC, multi-key rotation, and `cmd/keyrotate` provably covers all 15 ciphertext columns with a build-time guard and ciphertext-CAS updates (Appendix B). Certificate issuance for the public listener correctly delegates to cert-manager. Against that: the chart ships a world-readable JWT signing key and Fernet key as defaults with `config.env: development`, and both the chart guard and the runtime guard are inert outside production, with no warning path at all (`dev-keys-default-and-silent`). ArgoCD instance TLS verification defaults OFF because a non-pointer bool decodes an omitted field to `false`, and a partial PUT silently downgrades an already-secure instance (`argocd-verify-ssl-fail-open`). `clusters.ca_certificate` is never written by any code path, so every ArgoCD cluster registration is pinned to `insecure: true` and the downloadable kubeconfig omits the CA (`cluster-ca-never-persisted`). The internal ArgoCD proxy mints a per-pod self-signed cert and the client hardcodes `Insecure: true` (`argocd-internal-proxy-tls-insecure-skip-verify`). One decrypt path returns raw ciphertext as a credential on failure (`gitops-decrypt-silent-ciphertext-fallback`).

### (d) Is code quality enterprise grade?

**Qualified yes on process, no on structure.** The contract gates are unusually strong — route coverage, embedded-spec byte compare, error-code docs, CSRF/auth route classification, migration safety, sqlc drift, a race suite, Playwright plus live-backend e2e, Trivy and SBOM per image, and a build-time audit-coverage contract that makes an uninstrumented mutating handler fail compilation. Only 5 `t.Skip` calls exist in the whole Go suite. Three specific holes: `golangci-lint` is configured but never invoked by CI or any verify target (`golangci-lint-never-runs-in-ci`), the API contract gate never checks request-DTO field coverage (`no-request-schema-field-gate`), and the code-health gate is structurally blind to hand-written types shadowing generated ones (`api-ts-shadow-generated-types`). Structurally, five files exceed 2,800 lines, and the copy-paste in one of them already produced a live authorization gap: two POST previews and one GET on `/api/v1/settings/monitoring/*` run with no authorization and no `requireAuth`, returning the decoded backend auth config to an unauthenticated caller (`monitoring-go-3815-line-split`).

---

## 2. Priority index

| Pri | ID | Dim | Eff | Status |
|---|---|---|---|---|
| ~~P0~~ ✅ | `agent-ingest-token-shared-service-user-cross-cluster-rce` | transport-security | M | **shipped 2026-07-28** |
| ~~P0~~ ✅ | `resources-group-version-kind-route-bypasses-per-resource-rbac` | transport-security | S | **shipped 2026-07-28** |
| ~~P1~~ ✅ | `monitoring-go-3815-line-split` (Part A) | code-health / security | S | **shipped 2026-07-28** |
| ~~P1~~ ✅ | `k8s-proxy-proxy-subresource-not-gated-nodes-proxy-is-kubelet-rce` | transport-security | M | **shipped 2026-07-28** |
| P1 | `exec-logs-verb-mismatch` + `exec-logs-ws-ignore-pods-exec-and-pods-logs-verbs` | parity-core | M |
| P1 | `role-update-no-escalation-guard` | parity-core | S |
| P1 | `cluster-project-list-unfiltered` | parity-core | M |
| P1 | `project-bindings-inert-by-default` | parity-core | M |
| P1 | `no-downstream-impersonation` + `no-downstream-user-impersonation-agent-sa-is-cluster-admin` | parity-core | L |
| P1 | `argocd-authz-anchored-to-management-cluster` | argocd | L |
| P1 | `baseline-appset-floating-chart-version` | argocd | M |
| P1 | `dev-keys-default-and-silent` | crypto-tls | S |
| P1 | `argocd-verify-ssl-fail-open` | crypto-tls | S |
| P1 | `send-failclose-livelock` | agent | L |
| P1 | `unbounded-handler-goroutines` | agent | S |
| P1 | `heartbeat-full-cluster-list-storm` | agent | M |
| P1 | `self-upgrade-no-verification-no-rollback` | agent | M |
| P1 | `catalog-sync-oci-git-blind-and-abort` | parity-features | M |
| P1 | `monitoring-stack-lifecycle-no-ui` | parity-features | L |
| P2 | 30 items — see §6 | mixed | S–L |
| P3 | 20 items + 6 strengths — see §7 | mixed | S–L |

---

## 3. Where we already beat Rancher

Six verified strengths. Each is a property to *protect*, not work to do; the "remediation" is the regression fence.

### [strength-agent-privilege-profiles] Agent privilege profiles default to least-privilege viewer

**Status:** PENDING APPROVAL · **Severity** low (strength) · **Dimension** parity-core · **Effort** S

**Evidence** — `deploy/agent/template.go:117-125` `NormalizePrivilegeProfile` maps the empty string to the viewer profile ("Default to least-privilege read-only viewer"), and `:139-145` fails an unrecognized value **closed** to viewer rather than admin. Six profiles at `:23-28` (admin, operator, viewer, namespace-operator, namespace-viewer, custom) render into ClusterRole rules via `RBACRulesYAML` (`:148-163`); the viewer ruleset `:266-305` is get/list/watch only and deliberately omits `secrets`. `RBACBindingKind` (`:165-172`) downgrades the namespace-* profiles to a namespaced RoleBinding. Rancher has no equivalent: `rancher/pkg/systemtemplate/template.go:107-123` binds `cattle` to a `cattle-admin` ClusterRole that is `apiGroups: ['*'] / resources: ['*'] / verbs: ['*']` plus `nonResourceURLs: ['*']`, unconditionally, on every imported cluster.

**Why it matters** — For read-only fleet observability, our downstream footprint is dramatically smaller than Rancher's and the choice is explicit and auditable at adoption time. It does not substitute for per-user impersonation (`no-downstream-impersonation`) — it lowers the ceiling for all users at once rather than separating them — but it bounds that gap's blast radius, and it is the reason several findings in this report are scoped narrower than they first appear.

**Remediation** — None. Protect it: keep `deploy/serviceaccount_clusterrole_render_test.go` and `deploy/agent/template_test.go:478` as the fence. Surface the selected profile in the cluster detail UI and adoption wizard so the default is visible rather than invisible.

**Tests** — Add `deploy/agent/template_test.go::TestNormalizePrivilegeProfile_EmptyDefaultsToViewer` if absent; it must fail if the default ever flips.

**Acceptance**
- [ ] `TestNormalizePrivilegeProfile_EmptyDefaultsToViewer` exists and asserts viewer for `""` and for an unrecognized value.
- [ ] Selected profile is rendered on the cluster detail page.

### [strength-proxy-header-allowlist] Proxy header policy is a fail-closed allowlist

**Status:** PENDING APPROVAL · **Severity** low (strength) · **Dimension** parity-core · **Effort** S

**Evidence** — `pkg/proxyhdr/proxyhdr.go:29-35` defines `forwardableHeaders` as exactly five entries (accept, accept-encoding, content-type, content-length, user-agent); `ShouldForwardRequestHeader` (`:39-42`) returns false for everything else. The package doc (`:1-14`) names the threats: `X-Remote-User`/`X-Remote-Group`/`X-Remote-Extra-*` front-proxy spoofing, `Impersonate-*`, and the caller's `Authorization`. Enforced at both hops — `internal/tunnel/proxy.go:412-421` server side, `internal/agent/k8sproxy.go:218-222` (streaming) and `:439-443` (unary) agent side. Response direction guarded too (`internal/tunnel/proxy.go:383-393`). Rancher uses a targeted denylist (`rancher/pkg/clusterrouter/proxy/proxy_server.go:280-285`) and only on one branch.

**Why it matters** — A future Kubernetes identity header, or one a downstream apiserver is configured to trust, is inert by construction with no code change. Double enforcement means a compromised or downlevel control-plane replica cannot inject identity headers a current agent would honor.

**Remediation** — None. **Hard constraint for the executing agent:** when `no-downstream-impersonation` lands, the impersonation identity must travel as a **typed field on `protocol.K8sRequestPayload`** and be stamped by the agent. It must **not** be added to `forwardableHeaders`, or this property is destroyed.

**Tests** — `pkg/proxyhdr/proxyhdr_test.go::TestShouldForwardRequestHeader_RejectsIdentityHeaders`, table over `Authorization`, `Impersonate-User`, `Impersonate-Group`, `Impersonate-Extra-Scopes`, `X-Remote-User`, `X-Remote-Group`, `X-Remote-Extra-Foo`, `Cookie`, `X-Forwarded-For` — all false.

**Acceptance**
- [ ] The negative-case table test exists and is in the default gate.
- [ ] No new entry was added to `forwardableHeaders` by the impersonation work.

### [strength-audit-and-token-controls] Build-gated audit coverage, read-side audit policies, scoped/IP-restricted tokens

**Status:** PENDING APPROVAL · **Severity** low (strength) · **Dimension** parity-core · **Effort** S

**Evidence** — `internal/audit/coverage_contract_test.go:11-80` parses handler sources with `go/ast` and asserts a named per-file, per-handler expectation map (auth Login/Refresh/Logout/ChangePassword/CreateToken/RevokeToken, all 17 ArgoCD mutations, backups, clusters, dex_config, group_mappings, projects, rbac) each emits an audit call — a mutating handler without one fails the build. `internal/audit/action_contract_test.go` pins the action vocabulary. `internal/handler/read_audit_policies.go:1-13` documents an operator-configurable read-audit CRUD surface that invalidates the evaluator cache immediately. Proxy path instrumented both ways: `internal/server/routes.go:1426-1428` `auditK8sProxyMutations`, `:1430-1451` `auditK8sProxySecretReads` emitting `cluster.secret.read` with the parsed object ref. Tokens: `internal/handler/auth.go:1505-1515` validates and persists a per-token CIDR allowlist via `auth.ParseAllowedCIDRs` (rejecting with 400 rather than persisting an allow-everything row), `:1496-1499` persists a scope set, `:1531-1543` records scopes plus `allowed_cidr_count` — the count, not the ranges. Rancher's audit is generic request middleware with verbosity levels and no per-handler contract; its tokens have a max-TTL clamp but no scope set and no IP allowlist.

**Why it matters** — Audit completeness is a build gate rather than a review discipline. Per-token scopes plus CIDR allowlists are a containment control for CI credentials that Rancher does not offer.

**Remediation** — None. Grow the `expected` map as mutating handlers land. **[corrected]** The original claimed Rancher is ahead on token lifecycle; in fact `internal/handler/platform_settings.go:140` already defines `token.max_ttl_min` (default 525600) and it is simply never read — see `api-token-no-max-ttl`. Until that is wired, the admin UI advertises a ceiling it does not enforce, which undercuts this strength's compliance story.

**Tests** — Existing `TestKeyMutatingHandlersEmitAudit` and the action contract test stay in `make verify`. When max-TTL lands, extend `internal/handler/auth_test.go` so the audit detail assertion covers the effective (possibly clamped) expiry.

**Acceptance**
- [ ] Both contract tests remain in the default gate.
- [ ] `api-token-no-max-ttl` is scheduled, so the advertised setting stops being a false assurance.

### [strength-helm-values-schema-form] Schema-driven Helm values form with inferred schemas

**Status:** PENDING APPROVAL · **Severity** low (strength) · **Dimension** parity-features · **Effort** S

**Evidence** — `frontend/src/routes/dashboard/catalog/index.tsx:709-782` resolves `$ref`/`$defs` (`resolveSchemaRefs`), gates the generated form on `hasRenderableSchema`, and keeps a YAML editor in two-way sync (`handleSchemaValuesChange` dumps form→YAML, `handleYAMLChange` re-merges with a parse-error banner); `editorMode` falls back to `yaml` when no schema is usable. When a chart ships no schema the backend synthesises one: `internal/handler/catalog_hydrate.go:225-260` `inferSchema`/`inferNode` with a depth cap and an `x-astronomer-inferred` marker, hydrated from the chart archive at `:49-63` (HTTP `:147`, OCI `:195`). Rancher gets form-driven installs only for charts shipping a hand-authored `questions.yaml`.

**Why it matters** — A form for effectively every chart with no chart-side authoring, and the YAML escape hatch always one toggle away. This is an improvement on the reference implementation, not parity.

**Remediation** — None. Keep `hasRenderableSchema` conservative so a bad inferred schema never blocks install. Consider reusing the component for the app-upgrade modal (`frontend/src/routes/dashboard/clusters/$id/apps/index.tsx:256-261` seeds only `currentValues`).

**Tests** — Existing `frontend/src/lib/helm-values-schema.test.ts` and `internal/handler/catalog_infer_schema_test.go`. Add `TestInferSchemaNeverBlocksInstall` asserting `hasRenderableSchema` is false (YAML mode) for pathological values (deeply nested arrays, null-only maps).

**Acceptance**
- [ ] `TestInferSchemaNeverBlocksInstall` exists.
- [ ] Install remains possible for any chart regardless of schema quality.

### [strength-catalog-operations-queue] Durable, auditable operations queue behind every Helm/monitoring/logging/tool mutation

**Status:** PENDING APPROVAL · **Severity** low (strength) · **Dimension** parity-features · **Effort** S

**Evidence** — Catalog: `internal/handler/catalog.go:1824` `enqueueOperation`, `:1920` `claimPendingCatalogOperations` (row-level locking), `:1982` execute, `:2150` `recordCatalogOperationEvent`, `:1894` preview projection. Monitoring: `internal/handler/monitoring.go:2914` `createMonitoringOperation`, `:3108` claim, `:3428` `rollbackIfConfigured` with configurable auto-rollback and max attempts (`:3369-3398`). Logging: `/logging/operations/` at `internal/server/routes_tools_controlplane.go:265-267`. Retry via `POST .../operations/{id}/retry/` with per-operation RBAC re-checked against the target cluster (`internal/handler/catalog.go:1727-1760`, `internal/handler/monitoring.go:3039-3092`), audited, and surfaced in the UI Operations tab.

**Why it matters** — Rancher's catalogv2 runs helm operations as pods and exposes logs, but has no first-class retry/preview/auto-rollback contract and no unified per-operation authorization. Ours is observable, resumable, per-stage logged, and rolls back on failure.

**Remediation** — None. The one consistency gap is that CIS scans deliberately bypass this machinery for an in-process goroutine — see `cis-ingest-in-process-goroutine-loses-scans`.

**Tests** — Existing catalog/monitoring claim/execute/retry tests. Add a cross-cutting `TestAllOperationSurfacesAreRetryable` asserting catalog/monitoring/logging operation tables each expose list/get/retry with cluster-scoped authorization.

**Acceptance**
- [ ] Cross-surface retryability test exists.
- [ ] CIS scans are folded into the same model or the divergence is documented.

### [strength-cis-wizard-and-vuln-scanning] CIS wizard with live profile discovery, plus an image-vulnerability surface Rancher has no equivalent for

**Status:** PENDING APPROVAL · **Severity** low (strength) · **Dimension** parity-features · **Effort** S

**Evidence** — `frontend/src/routes/dashboard/security/scans/new/index.tsx:23-80`: cluster step, live `useCISProfiles(clusterId)` list, recommended-profile preselect, review step. Backend discovery: `internal/handler/security.go:922` `ListProfiles` → `:1042` `listClusterScanProfiles` reading `ClusterScanProfile` CRs, with a distribution-derived fallback at `:1022` `resolveClusterScanProfileName` and a `source: 'fallback'` marker rendered at `index.tsx:189`. Flattened report `internal/scanner/cis_report.go`; CSV export `internal/handler/security.go:893` wired to `frontend/src/routes/dashboard/security/scans/$scanId/index.tsx:149-164`. Image scanning has no Rancher counterpart at all: `internal/server/routes_security.go:70-93` exposes per-cluster summary, top images, report detail, rescan, history sparkline, latest-vs-prior diff, CSV export, per-image timeline, live scan-progress poll, and fleet rollups.

**Why it matters** — Rancher's CIS UI requires the operator to know the profile name; we discover it and degrade explicitly. Rancher ships no built-in image CVE surface.

**Remediation** — None. Two follow-ons: persist and surface whether the profile was explicit or fallback (`internal/handler/security.go:634-661`) so an operator can tell which benchmark a historical result used; and add scheduling (tracked as `no-scheduled-cis-scans`).

**Tests** — Existing `internal/handler/security_cis_test.go`. Add `TestListProfilesFallbackIsLabelled` asserting `source='fallback'` when no `ClusterScanProfile` CRs are readable, since the UI branches on it.

**Acceptance**
- [ ] `TestListProfilesFallbackIsLabelled` exists.
- [ ] Historical scan rows record which profile was actually resolved.

---

## 4. P0 — blockers

> **Both P0 items were implemented, reviewed, gated and deployed on 2026-07-28.** Full Go suite, `make verify`,
> and the race suite are green; both fixes are running and validated on k3d-astro-p63. Everything from P1 down
> remains `PENDING APPROVAL`.

Two items. Both are cross-tenant authorization breaks reachable from a low-privilege or least-trusted position. Nothing else should be scheduled ahead of them.

### [agent-ingest-token-shared-service-user-cross-cluster-rce] Every agent receives a bearer token owned by ONE shared service user holding `clusters:update` on EVERY cluster

**Status:** ✅ SHIPPED 2026-07-28 (migration 140) · **Severity** blocker · **Dimension** transport-security · **Effort** M

> **Implemented and validated.** `AgentIngestServiceUsername` is now `system:agent-ingest:<cluster-uuid>`, the
> reserved role grants `audit_ingest:create` (new `rbac.ResourceAuditIngest`) instead of `clusters:update`, and
> the ingest route gates on it. Migration 140 retires the legacy shared principal on upgrade — this was the
> load-bearing part: the runtime get-or-create paths never rewrite existing rows, so without it an upgraded
> install kept its fleet-wide bindings AND would have failed the narrowed gate. Verified live on k3d-astro-p63:
> legacy `system:agent-ingest` now inactive with 0 bindings / 0 live tokens, new per-cluster principal holds
> exactly 1 binding, and the ingest route returns 202 for a caller holding the new permission.
> Two follow-ups deliberately **not** taken here: plan item 3 (the `api_tokens.cluster_id` machine-identity
> guard and refusing `is_service` users in `stream_tickets.go` Create), and narrowing
> `AgentIngestTokenScopes()` off `clusters:write` so the credential is stopped twice rather than once.
> Also still open: `GetUserByUsername` is unfiltered, so SCIM `userName`-equality can still resolve a service
> principal by exact name (`ListUsers`/`CountUsers` were filtered).

**Evidence** — `internal/tunnel/server.go:544-556`: on every successful CONNECT the hub mints an apiserver-audit ingest token and ships its plaintext to the agent in CONNECT_ACK (`ackPayload.AuditIngestToken = token`). `internal/auth/agent_ingest_token.go:145-176` `IssueAgentIngestToken` resolves a **single** reserved principal `system:agent-ingest` (const `:22`, `ensureAgentIngestServiceUser` `:199-208`), a **single** shared role granting `clusters:update` (`:113-115` `agentIngestClusterRoleRules`, `ensureAgentIngestClusterRole` `:211-232`), then `ensureAgentIngestBinding` (`:234-255`) adds a cluster-scoped `cluster_role_binding` for the connecting cluster **to that same user**. After N clusters connect, the one service user carries N bindings. The token is a normal `astro_*` API token (`AgentIngestTokenParams` `:88-101`, scopes pinned to `["clusters:write"]` at `:76`). `internal/server/middleware/auth.go:164-220` authenticates by hash and resolves the row to `dbUser.ID` — the identity is the shared user, not the cluster, with only an `IsActive` check. `internal/rbac/engine.go:140-161` + `bindingApplies` `:271-293` evaluate ALL of that user's bindings, so `CheckPermission(clusters, update, clusterB)` is true for a token minted for clusterA.

Escalation chain: `internal/handler/stream_tickets.go:74-90` issues an `exec` stream ticket for any `cluster_id` given `ScopeWriteClusters` (the ingest token's exact scope set) plus `clusters:update` (granted fleet-wide); the route is registered with only `requireAuth` (`routes.go:627`); the existing test `internal/server/agent_authz_scope_test.go:194-203` proves a `["clusters:write"]` token with `clusters:update` gets HTTP 201. The ticket passes `internal/tunnel/exec_consumer.go:151` `authenticateStreamRequest` and `:108-117` `authorizeCluster` (also `clusters:update`), and `internal/agent/k8sproxy.go:418-449` executes downstream as the agent's own service account. The issuer is wired unconditionally in production (`internal/server/server.go:744-745`).

**Why it matters** — Any operator who can read one agent pod's memory or env, any cluster-admin of a single adopted (possibly customer-owned) cluster, or anyone who captures one CONNECT_ACK obtains the ability to POST forged kube-apiserver audit batches for **any** cluster (destroying the forensic record) and to exercise `clusters:update` fleet-wide. **[corrected]** The interactive-shell half is gated by the target cluster's agent privilege profile: `deploy/agent/template.go:117-146` defaults an unannotated registration to read-only `viewer`, and `pods/exec` exists only in the `operator` and `admin` profiles. So cross-cluster exec applies to operator/admin adoptions — which is the profile the shipped ArgoCD baselines select — while forged audit ingest and cross-cluster `clusters:update` apply everywhere. Rancher never mints a fleet-wide principal for a per-cluster agent (`rancher/pkg/clusterrouter/proxy/proxy_server.go:290-296`).

**Remediation** — Items 1 and 2 ship together; 1 alone closes the cross-cluster path and is the minimal fix, 2 is what stops an ingest credential satisfying the exec gate at all.

1. **Per-cluster ingest identity.** In `internal/auth/agent_ingest_token.go`, change `AgentIngestServiceUsername` (`:22`) from a constant to a derived per-cluster username, e.g. `system:agent-ingest:<cluster-uuid>`. Change `ensureAgentIngestServiceUser` (`:199-208`) to take `clusterID` and resolve/create that per-cluster row. Each token's owning user then carries exactly one cluster binding; `ensureAgentIngestBinding` (`:234-255`) is unchanged in shape but now binds a distinct principal per cluster.
   *Verified against source 2026-07-28: the file already has the per-cluster naming convention one layer down — `AgentIngestTokenName(clusterID)` (`:109-111`) returns `"agent-ingest-" + clusterID.String()` so `RevokeAPITokensByName` can revoke the prior token per cluster. The defect is precisely that this per-cluster identity stops at the `api_tokens.name` layer and never reaches the owning principal. Mirror that existing helper for the username rather than inventing a new scheme, and reuse the same revoke-old/mint-fresh ordering.*
2. **Dedicated audit-ingest permission.** Add `rbac.ResourceAuditIngest` and use `rbac.VerbCreate` in `internal/rbac/types.go`. Change `agentIngestClusterRoleRules` (`internal/auth/agent_ingest_token.go:113-115`) to grant that instead of `clusters:update`. Change the ingest route gate at `internal/server/routes_security.go:59` (the `requirePermission(ResourceClusters, VerbUpdate)` call, see also `:57`) to require the new permission. An ingest credential can then never satisfy the exec-ticket or k8s-proxy gates.
3. **Machine-identity guard (defence in depth).** Add an optional `cluster_id` column to `api_tokens` via a new migration; have `internal/server/middleware/auth.go` reject a cluster-bound token whose `chi.URLParam(r, "cluster_id")` does not match. Alternatively/additionally refuse `is_service` users in `internal/handler/stream_tickets.go` `Create`.
4. **[corrected — do NOT do this as a substitute]** The original proposal to withhold the token when `AUDIT_DELIVERY != http` is not a fix: it does not address the shared-principal defect. Skip it, or treat it as an unrelated cleanup after 1+2 land.

**Tests**
- `internal/auth/agent_ingest_token_test.go::TestAgentIngestTokenIsScopedToOneCluster` — issue tokens for clusterA and clusterB; assert the two tokens resolve to **different** user IDs, and that the clusterA user's binding set contains exactly one binding (clusterA). Must fail today.
- `internal/server/agent_authz_scope_test.go::TestAgentIngestTokenCannotMintExecTicketForAnotherCluster` — build the router as `newStreamTicketRouter` does, with the ingest token's real scopes `["clusters:write"]` and the clusterA-only binding set; `POST /api/v1/streams/tickets/` with `{"stream_type":"exec","cluster_id":"<clusterB>"}`; assert **403**. Today: 201.
- A regression case asserting the audit-ingest route still accepts a correctly-scoped ingest token after the permission change.

**Acceptance**
- [ ] Two clusters connecting produce two distinct `users` rows; neither carries a binding for the other's cluster.
- [ ] `agentIngestClusterRoleRules` no longer contains `clusters:update`.
- [ ] The ingest route requires the new audit-ingest permission and rejects a token holding only `clusters:update`.
- [ ] Both new tests fail on the pre-change tree and pass after.
- [ ] Existing apiserver-audit ingest end-to-end path still works (agent → hub → ingest route).

### [resources-group-version-kind-route-bypasses-per-resource-rbac] `GET /clusters/{id}/resources/{group}/{version}/{kind}/` proxies an arbitrary cluster-wide LIST behind only `clusters:read`

**Status:** ✅ SHIPPED 2026-07-28 · **Severity** blocker · **Dimension** transport-security (reported under parity) · **Effort** S

> **Implemented and validated.** Deletion was taken, as recommended. The route registration and the
> `ListResources` handler are gone; the path was removed from `docs/openapi.yaml`, the embedded asset
> re-synced (byte-identical, confirmed with `cmp`), the typed SDK regenerated (252 lines dropped from
> `pkg/astroclient`), and the route-table golden updated. Verified live on k3d-astro-p63:
> `/resources/core/v1/secrets/`, `/resources/core/v1/configmaps/` and `/resources/apps/v1/deployments/`
> all return **404**, while the allowlisted sibling `/resources/services/` still returns 200 and the
> `/k8s/*` passthrough still serves group/version/kind reads with per-resource authz, namespace filtering
> and secret-read auditing intact.

**Evidence** — `internal/server/routes_resources_workloads.go:112-113` registers `r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/resources/{group}/{version}/{kind}/", deps.Resources.ListResources)` — the only authorization is `clusters:read`. `internal/handler/resources.go:338-352` `ListResources` takes `group`, `version`, `kind` verbatim from the URL and builds the upstream path with no allowlist (`path = fmt.Sprintf("/api/%s/%s", version, kind)` when `group=="core"`, else `/apis/%s/%s/%s`), then `h.proxyJSON` → `:1521` → `:1503` `h.requester.Do` → `internal/handler/k8s_requester.go:69+` → `hub.SendToAgent` → `internal/agent/k8sproxy.go:418-449`, executed with the agent's service account. Agent-side there is no path or kind restriction — only the `proxyhdr` header allowlist and a body size cap.

Sibling routes are protected and this one is not: `ListNamedResources`/`ListGenericResources` are constrained by the `resourceDefs` allowlist (`resources.go:1530-1539`) and gated per-resource via `requireNamedResourcePermission` (`routes_resources_workloads.go:114-125`); the `/k8s/*` passthrough maps secrets to `rbac.ResourceSecrets` and audits reads (`routes.go:1509-1552`, `1430-1451`). None applies here: no `ResourceSecrets` check, no namespace filter (`tunnel.WithNamespaceFilter` is only stashed by `requireK8sProxyPermission`, `routes.go:1392`), no `cluster.secret.read` audit row. `GET /api/v1/clusters/<id>/resources/core/v1/secrets/` returns every Secret in the cluster. No frontend caller exists (grep of `frontend/src` finds zero references; the UI uses `/k8s/*` via `lib/k8s-paths.ts:199`).

**Why it matters** — The lowest-privilege role in the product (`clusters:read`, held by every viewer/troubleshooter template, e.g. `internal/db/migrations/032_builtin_role_catalog.up.sql:20-21`) can exfiltrate cluster-wide content from every managed cluster with one GET, with no secret-read audit trail, defeating namespace-scoped RBAC which only filters `/k8s/*`. **[corrected]** Impact is bounded by the adopted cluster's agent privilege profile: on `viewer` (the default for an unannotated registration, `deploy/agent/template.go:117-146`, secrets deliberately excluded at `:266-270`) this yields cluster-wide ConfigMaps, ServiceAccounts, and CRD content rather than Secrets. On `operator` — the profile the shipped ArgoCD baselines select — and on `admin` (`*/*/*`, `template.go:259-264`), the every-Secret-in-every-namespace read is exactly as described. Still a blocker: one route defeats `ResourceSecrets`, the namespace filter, and secret-read auditing simultaneously.

**Remediation** — Preferred: delete.

1. **Verify no non-frontend consumer** first: grep `internal/astrocli`, `tools/`, docs route inventories, and any catalog code for the `/resources/{group}/{version}/{kind}/` shape. (The frontend has none.)
2. **Delete the route** at `internal/server/routes_resources_workloads.go:112-113` and the `ListResources` handler at `internal/handler/resources.go:338-352`. `/k8s/*` already serves arbitrary group/version/kind reads with correct authz.
3. Regenerate the route inventory / golden route table (`docs/generated-route-inventory.json`, `go test ./internal/server/ -run RouteTable`) and remove the route from `docs/openapi.yaml` plus the embedded copy (`make openapi-embed`).
4. **Only if the route must stay:** (a) replace the middleware with a request-derived gate that builds the ref from `{group}/{version}/{kind}` and runs it through `knownK8sProxyResource`/`k8sProxyResourcePolicy` (`routes.go:1593-1615`), requiring the mapped `(resource, list)` permission; (b) reject kinds outside a vetted set and refuse `secrets`; (c) apply the namespace allow-set path used by `requireK8sProxyPermission` (`routes.go:1345-1400`) plus `applyNamespaceFilter`; (d) wire an `auditK8sProxySecretReads` equivalent.

**Tests**
- `internal/server/routes_security_test.go::TestResourcesGroupVersionKindRouteRequiresPerResourcePermission` — user holding ONLY `clusters:read`, `GET /api/v1/clusters/<id>/resources/core/v1/secrets/`, assert **403** (today it passes authz and reaches the proxy). Positive case: a user with `secrets:list` gets through — or 404 if deleted.
- If deletion is taken: `internal/server/routes_security_test.go::TestResourcesGroupVersionKindRouteDeleted` asserting 404.
- Route-table golden test must be updated and green.

**Acceptance**
- [ ] `GET /api/v1/clusters/<id>/resources/core/v1/secrets/` with only `clusters:read` returns 403 or 404.
- [ ] Grep confirms no in-repo consumer was broken.
- [ ] Route inventory, OpenAPI spec, and embedded spec are consistent (`make verify` api-contract scope green).

---

## 5. P1 — high

### 5.1 Security-urgent (execute alongside P0)

### [monitoring-go-3815-line-split] Three unauthenticated `/settings/monitoring/*` endpoints, produced by copy-paste in a 3,815-line file

**Status:** ✅ Part A SHIPPED 2026-07-28 · Part B still PENDING APPROVAL · **Severity** high · **Dimension** code-health + transport-security · **Effort** Part A: S; Part B: L

> **Part A implemented and validated.** Confirmed live before the fix: anonymous
> `GET /api/v1/settings/monitoring/backend/` returned **200**. Both layers were fixed deliberately — the three
> handlers gained the `authorizeGlobalAction` preamble their mutating siblings already had, AND the routes moved
> onto a `requireAuth` group, with a test that unwires the handler's authz support to prove the router-level
> check stands alone. `authConfig` is now redacted. Verified live on k3d-astro-p63: all three routes return
> **401** anonymously, and an authenticated wildcard admin still gets 200.
> Enumeration beyond the audit's list of three found `ListOperations` also anonymously reachable; it now
> requires auth but deliberately keeps per-row RBAC filtering rather than gaining a global gate, which would
> break cluster-scoped binding holders. `PUT /settings/monitoring/backend/` and POST retry moved to write-scope.
> **Root cause of the blind spot:** the Monitoring handler was never wired into `newRouteSecurityRouter`, so all
> 38 monitoring routes were invisible to every registry-driven security test. Wiring it — and then the remaining
> 21 handler deps, surfacing 228 untested routes — shipped as a separate commit (`2c92f45`), which also adds the
> whole-router default-deny sweep that would have caught this class of bug.
> **Part B (the 3,815-line file split) was explicitly NOT done** and remains open.
> Also noted, not fixed: `internal/handler/monitoring.go` is gofmt-dirty at HEAD, pre-existing.

**Evidence** — Three structurally identical Preview/Install/Upgrade/Replace/Uninstall/Status families: shared-Thanos `monitoring.go:498-735`, shared-Alertmanager `:737-974`, per-cluster stack `:1071-1283`. Every mutating sibling opens with `h.authz.authorizeGlobalAction(w, r, rbac.ResourceMonitoring, ...)` (`:519, 546, 582, 627, 677`), but `PreviewSharedThanosStack` (`:498`), `PreviewSharedAlertmanager` (`:737`) and `GetBackendConfig` (`:413`) have **no authorization call at all** — and their routes at `internal/server/routes.go:584, 590, 596` are registered on the bare `r` inside `r.Route("/settings")` (`routes.go:534`), which has no group-level `requireAuth`. There is no global auth middleware on the router (only RequestID/RealIP/SecurityHeaders/RequestLogger/Recoverer/Metrics/CORS, `routes.go:354-379`). Neither `/settings/monitoring/backend` nor the preview routes appear in `docs/security-sensitive-routes.json`, `docs/route-risk-classifications.json`, or `isRouterPublicReadPattern` (`routes_security_test.go:2735-2741`). Rest of file: async operation engine `3094-3550`, PromQL client `1284-1836`, Helm values/objstore rendering `1880-2790`, legacy shims `3703-3815`, dead assignment `_ = ok` at `:1165`.

**Why it matters** — **[corrected — the severity is carried entirely by the unauthenticated exposure, which is a security finding in its own right]** `GET /api/v1/settings/monitoring/backend/` returns `monitoringBackendResponse` (`monitoring.go:1836-1852`), which includes the full decoded `authConfig` map — the operator-supplied auth material for the Thanos/Alertmanager backend — to **any unauthenticated caller**. Both POST preview routes render Helm values unauthenticated. **[corrected]** The mutating routes on bare `r` are *not* exposed: `bindingsForContext` (`authorization.go:215-231`) returns `restricted=true` with nil bindings for an anonymous request, so `authorizeGlobalAction` 403s. The per-cluster family is protected separately by `requirePermission` middleware in `routes_clusters.go:81-85` — so the file uses two incompatible authz strategies, which is what allowed the omission to go unnoticed.

**Remediation** — **Part A ships immediately and independently of Part B.**

**Part A (S, urgent):**
1. Add `h.authz.authorizeGlobalAction(w, r, rbac.ResourceMonitoring, rbac.VerbRead)` as the first statement of `PreviewSharedThanosStack` (`monitoring.go:498`), `PreviewSharedAlertmanager` (`:737`), and `GetBackendConfig` (`:413`), returning on false exactly as the mutating siblings do.
2. Wrap the three route registrations at `internal/server/routes.go:584, 590, 596` in `requireAuth` (or move them under the same `monitoringMutate` group used at `routes.go:583` with a read-verb variant), so an anonymous request is rejected before reaching the handler.
3. Add all three routes to `docs/route-risk-classifications.json` and `docs/security-sensitive-routes.json`.
4. Delete the dead `_ = ok` at `monitoring.go:1165`.

**Part B (L, backlog refactor — schedule separately):** split along four seams and collapse the triplication.
- `internal/handler/monitoring_stack_shared.go` — Thanos + Alertmanager families (`498-974`) rewritten against one generic `stackLifecycle[Req any]` holding `{Resource rbac.Resource, AuditPrefix, ChartRepo, ChartName string, Load, Metadata, ReplaceRequired, Persist, Enqueue}` with five methods, so the authz preamble is structurally unskippable (~230 duplicated lines removed).
- `internal/handler/monitoring_stack_cluster.go` — per-cluster family (`1071-1283`) as a third instantiation.
- `internal/handler/monitoring_operations.go` — `ListOperations`/`GetOperation`/`RetryOperation`, reconciler, claim/execute/rollback/readiness (`284-412`, `2811-3050`, `3094-3610`).
- `internal/handler/monitoring_metrics.go` — PromQL client, series math, summaries (`85-160`, `1284-1836`, `3703-3815`).
- Leave the handler struct, DTOs, and Helm-values rendering in `monitoring.go` (~900 lines).

**Tests**
- `internal/server/routes_security_test.go`: extend the unauthenticated-deny table (which today lists only `{"/api/v1/settings/monitoring", "handler.MonitoringHandler"}` at `:2561`) with explicit 401/403 cases for `GET /api/v1/settings/monitoring/backend/`, `POST /api/v1/settings/monitoring/thanos/preview/`, `POST /api/v1/settings/monitoring/alertmanager/preview/`. **All three must fail today.**
- `internal/handler/monitoring_stack_test.go`: a table over the three lifecycle instantiations asserting each of Preview/Install/Upgrade/Replace/Uninstall returns 403 for a caller without monitoring permission. This is the test that makes the triplication safe to keep if Part B is deferred.

**Acceptance**
- [ ] Anonymous `GET /api/v1/settings/monitoring/backend/` returns 401, not the decoded `authConfig`.
- [ ] Both preview routes reject anonymous and unprivileged callers.
- [ ] Three new deny-table cases fail before the change and pass after.
- [ ] The three routes appear in both route-risk registries.
- [ ] Part B tracked as separate work; not required for Part A sign-off.

### [k8s-proxy-proxy-subresource-not-gated-nodes-proxy-is-kubelet-rce] The `/proxy` subresource is authorized as a plain read/update of the parent resource

**Status:** ✅ SHIPPED 2026-07-28 (migration 141) · **Severity** high · **Dimension** transport-security · **Effort** M

> **Implemented and validated.** The `subresource == "proxy"` branch is placed before both the `pods/log` branch
> and the generic fallthrough — the ordering the audit flagged as load-bearing. Mutating `nodes/*/proxy`
> additionally requires `pods:exec` via a second `CheckPermission`, since one call cannot express AND. Proxy
> calls are now audited. A 14-case table test covers bare `/nodes/n1/proxy`, `/proxy/run/...`, mixed-case
> `/PROXY/`, port-qualified `https:web:443`, unknown CRDs, and both single-permission deny cases.
> **Migration 141 is an ADD-IF-MISSING reconcile, not a rewrite.** It restores `pods:proxy`/`services:proxy` on
> built-in rows predating 032/098 (both insert with `WHERE NOT EXISTS`, skipping existing rows) so those roles
> do not silently lose access. Operator-authored custom roles are untouched. **No template gains `nodes:proxy`** —
> Node Operator keeps `nodes:[read,list,update,manage]` because cordon/drain/label need no kubelet proxy, and
> widening it would re-open the RCE. Verified live: 0 built-in roles hold `nodes:proxy`.
> **Live validation limit, stated honestly:** on k3d-astro-p63 a wildcard admin's `nodes/proxy` request passes
> our gate and is then refused by the *downstream* cluster (`system:serviceaccount:astronomer:astronomer cannot
> get nodes/proxy`) because of the agent privilege profile — so the live run proves the gate does not regress
> admin access, but the negative case (limited user denied at our gate) is covered by the unit table, not by the
> cluster.

**Evidence** — `internal/server/routes.go:1509-1552` `k8sProxyPermission` special-cases only `exec|attach|portforward` (`:1524` via `isHighRiskPodProxySubresourceRef`, `:1962-1977`) and `pods/log` (`:1529`). There is **no branch for `subresource == "proxy"`**, so it falls through to `k8sProxyVerb` (`:1617-1645`) + `namedResourcePermission` (`:1749-1757`). `parseK8sProxyObjectRef` (`:1685-1716`) parses `/api/v1/nodes/n1/proxy/...` to resource=nodes, name=n1, subresource=proxy; `k8sProxyVerb` returns `VerbRead` for GET-with-name and `VerbUpdate` for POST-with-name. Result: `GET /api/v1/nodes/n1/proxy/runningpods/` → `nodes:read`; `POST /api/v1/nodes/n1/proxy/run/<ns>/<pod>/<container>` → `nodes:update` (the same verb the cordon/label routes use, `routes_resources_workloads.go:135-141`); `GET /api/v1/namespaces/x/pods/p/proxy/admin` → `pods:read`; services likewise. `rbac.VerbProxy` exists (`internal/rbac/types.go:105`) and the catalog grants it (`032_builtin_role_catalog.up.sql:20-21` pods:proxy; `098_rancher_grade_role_catalog.up.sql:51` services:proxy) but **no code path ever asks for it** — grep outside `internal/rbac` and the migrations returns only the builtin-role contract test. The forwarded request runs as the agent's SA (`internal/agent/k8sproxy.go:418-449`).

**Why it matters** — `nodes/proxy` reaches the kubelet's authenticated API; `/run/` and `/exec/` execute commands in any container on that node, so `nodes:update` — a routine "can cordon/label nodes" grant — becomes RCE that bypasses the `pods:exec` gate, and `nodes:read` becomes "read every container's logs on the node". `pods/proxy` and `services/proxy` turn a read grant into arbitrary HTTP (including POST) against any in-cluster listener — an SSRF pivot into the tenant network. **[corrected]** The kubelet-RCE half depends on the agent profile: `POST /nodes/{n}/proxy/run/...` needs `create` on `nodes/proxy` downstream, which only the opt-in `admin` profile's `*/*/*` grants (`deploy/agent/template.go:259-264`); `operator`'s wildcard is get/list/watch only (`:362-365`) and its enumerated rules never include `nodes/proxy`; `viewer` has neither. So: full node RCE behind `nodes:update` on **admin-profile clusters only**; on operator/admin, GET node/pod/service proxy still works and is an SSRF pivot plus kubelet read (`/runningpods`, `/logs`). Severity stays high on that basis.

**Remediation**
1. In `internal/server/routes.go`, add `isProxySubresourceRef(ref)` matching `ref["subresource"] == "proxy"` for any resource.
2. In `k8sProxyPermission`, return `(mapped-resource, rbac.VerbProxy)` from that branch **before** the `pods/log` branch is reached and before the `k8sProxyVerb` fallthrough at `:1528`. **[corrected — ordering is load-bearing; a `proxy` subresource must not reach the log or generic-verb paths.]**
3. Add `nodes` to the high-risk set so `nodes/proxy` maps to `(rbac.ResourceNodes, rbac.VerbProxy)` and — because kubelet `/run` is RCE — **additionally** require `(ResourcePods, VerbExec)` for that ref.
4. **New migration required or every proxy call regresses to 403:** grant `VerbProxy` on the relevant resources to the owner/admin role templates and to the four `pods:proxy` roles that already carry it in principle. Verify against `internal/db/migrations/032_builtin_role_catalog.up.sql` and `098_rancher_grade_role_catalog.up.sql`.
5. Add `proxy` to `auditK8sProxyMutationsWithAction` detail (`routes.go:1484-1507`) so these calls are always audited.

**Tests**
- `internal/server/routes_security_test.go::TestK8sProxyProxySubresourceRequiresProxyVerb` — table over `GET /nodes/n1/proxy/pods`, `POST /nodes/n1/proxy/run/ns/p/c`, `GET /namespaces/x/pods/p/proxy/`, `GET /namespaces/x/services/s/proxy/` with a user holding `nodes:[read,update] + pods:[read] + services:[read]` but no `:proxy` → all **403** (today all pass). Same table with the proxy verb granted → allowed. `POST /nodes/n1/proxy/run/...` additionally requires `pods:exec`.
- Unit test on `k8sProxyPermission` asserting the exact returned `(resource, verb)` pairs for each ref shape.
- Migration test asserting the shipped admin/owner templates still pass all four cases after the grant.

**Acceptance**
- [ ] `k8sProxyPermission` returns `VerbProxy` for every `subresource == "proxy"` ref.
- [ ] `nodes/proxy` POST requires both `nodes:proxy` and `pods:exec`.
- [ ] No shipped role template loses an access it had before (migration + test proves it).
- [ ] Proxy calls appear in the mutation audit detail.

### 5.2 Parity — core RBAC semantics

### [exec-logs-verb-mismatch] + [exec-logs-ws-ignore-pods-exec-and-pods-logs-verbs] WebSocket exec/logs gate on `clusters:update`/`clusters:read` instead of `pods:exec`/`pods:logs`

**Status:** PENDING APPROVAL · **Severity** high · **Dimension** parity-core / transport-security · **Effort** M
*(Two finding IDs, one defect. Fix once.)*

**Evidence** — `internal/tunnel/exec_consumer.go:108-118` `authorizeCluster` ends in `ec.rbacEngine.CheckPermission(bindings, rbac.ResourceClusters, rbac.VerbUpdate, clusterID, uuid.Nil, namespace)` and is the sole authz call in `HandleExec` (`:159-163`); the route at `internal/server/routes.go:1097-1100` carries `rateLimit` only. `internal/tunnel/logs_consumer.go:99-108` mirrors it with `ResourceClusters, VerbRead` (route `:1102-1104`). `internal/handler/stream_tickets.go:74-90` mirrors both. The HTTP k8s-proxy path uses the opposite vocabulary: `routes.go:1524-1525` returns `(ResourcePods, VerbExec)` for exec/attach/portforward and `:1529-1531` `(ResourcePods, VerbLogs)`. `internal/handler/kubectl_shell.go:243` uses the same `clusters:update` coupling.

The dedicated verbs exist (`internal/rbac/types.go:103-105`) and the catalog grants them: `internal/db/migrations/032_builtin_role_catalog.up.sql:20-21` and `33-34` give Cluster Operator, Cluster Troubleshooter, Project Operator, Project Troubleshooter `pods:[read,list,watch,logs,exec,proxy]` alongside `clusters:[read]` only. `internal/rbac/templates/support-engineer.yaml` grants `pods:[read,list,watch,logs,exec]` + `clusters:[read,list]`, no update. `internal/rbac/templates/platform-operator.yaml` grants `clusters:[create,read,update,list]` and has **no `pods` rule at all**.

**Why it matters** — Both directions are broken, asymmetrically. **Functional/parity:** the roles whose entire purpose is pod diagnostics hold `pods:exec` but not `clusters:update`, so the terminal 403s for exactly the personas it was built for. **Security (the actual over-grant):** any principal holding `clusters:read` streams every container's output in every namespace — pod logs routinely carry tokens and PII — and an operator's granular `pods:logs` grant is never consulted. `platform-operator`, documented as day-2 workflows "without granting full RBAC, user, SSO, or destructive platform administration" and holding zero `pods` grants, passes the `clusters:update` check and gets an interactive shell on any pod on any cluster. The permission-preview and effective-permissions UIs therefore report grants the system does not enforce.

**Remediation** — **[corrected]** There is no split-brain *within* the WS path: ticket issuance and redemption both use `clusters:update`/`read` and the comments at `exec_consumer.go:100-107` show the coupling is deliberate. The mismatch is WS-path vs HTTP-proxy-path, so step 2 is about keeping the two halves aligned while both move, not about fixing an internal inconsistency.

1. `internal/tunnel/exec_consumer.go` `authorizeCluster` (`:108-118`) → `CheckPermission(bindings, rbac.ResourcePods, rbac.VerbExec, clusterID, uuid.Nil, namespace)`.
2. `internal/tunnel/logs_consumer.go` `authorizeCluster` (`:99-108`) → `rbac.ResourcePods, rbac.VerbLogs`.
3. `internal/handler/stream_tickets.go:74-90` — exec/shell → `pods:exec`, logs → `pods:logs`, so ticket and socket agree. Keep the `ScopeWriteClusters` write-scope backstop for exec/shell unchanged.
4. `internal/handler/kubectl_shell.go:243` Open-time gate → `pods:exec`.
5. **Migration, mandatory:** grant `pods:exec` / `pods:logs` to any admin/owner template that today carries only `clusters` verbs, or existing admins lose the terminal on upgrade. Check `internal/db/migrations/098_rancher_grade_role_catalog.up.sql`. Do **not** silently add `pods:exec` to `platform-operator` — its own description argues against it; leave it without exec and put the change in the release notes.
6. **[corrected — keep as a hardening item, not the headline]** `exec_consumer.go:112` and `logs_consumer.go:102` return `true` when `rbacEngine == nil || rbacQuerier == nil`. Production wires both at `server.go:1579`, so this is a latent regression risk rather than a live hole. Invert to deny and have tests inject a permissive engine.

**Tests**
- `internal/tunnel/exec_authz_verb_test.go::TestAuthorizeCluster_SupportEngineerWithPodsExecIsAllowed` — bindings from the `support-engineer` ruleset return true. **Fails today.**
- `::TestAuthorizeCluster_ClustersUpdateWithoutPodsExecIsDenied` — `platform-operator` ruleset returns false. **Fails today.**
- `internal/tunnel/logs_consumer_test.go::TestHandleLogsRequiresPodsLogs` — `clusters:[read]` only → 403. **Fails today (200).**
- `::TestAuthorizeCluster_NilEngineDeniesByDefault` pins the fail-closed change.
- `internal/server/exec_verb_parity_test.go::TestWSExecAndK8sProxyExecUseSameResourceVerb` — assert `k8sProxyPermission` on an `/exec` path and the consumer's check agree on `(ResourcePods, VerbExec)`.
- `internal/server/agent_authz_scope_test.go` — extend `issueTicket` cases: a `pods:exec`-only binding mints an exec ticket; a `clusters:read`-only binding cannot mint a logs ticket.

**Acceptance**
- [ ] `support-engineer` can open an exec session; `platform-operator` cannot.
- [ ] A `clusters:read`-only principal cannot stream pod logs.
- [ ] Ticket issuance and socket redemption use the same verb.
- [ ] Migration proves no shipped admin/owner role loses terminal or log access.
- [ ] Nil engine/querier denies.

### [role-update-no-escalation-guard] PUT on global/cluster/project roles has no escalation check and no built-in-role protection

**Status:** PENDING APPROVAL · **Severity** high · **Dimension** parity-core · **Effort** S

**Evidence** — `internal/handler/rbac.go:233-261` `UpdateGlobalRole` decodes and calls `h.queries.UpdateGlobalRole(...)` with `Rules: defaultJSON(req.Rules)` — no guard call, no `is_builtin` check, no caller-permission comparison. `UpdateClusterRole` (`:337-363`) and `UpdateProjectRole` (`:436-462`) are identical in shape. The escalation machinery exists and is wired only to *bindings*: `:885-895` `guardGlobalBinding`, `:900-911` `guardClusterBinding`, `:913-923` `guardProjectBinding`, all delegating to `:941` `enforceNoEscalation`. SQL has no guard: `internal/db/queries/rbac.sql:14-22` is an unqualified `UPDATE global_roles SET ... rules = $6 WHERE id = $1`; `:24-25` an unqualified DELETE (whose `ON DELETE CASCADE on global_role_bindings` removes every binding for the role, per the comment at `rbac.go:276-277`). The targets are real rows: `internal/db/migrations/001_initial.up.sql:686-688` seeds `('Administrator', ..., '[{"resource":"*","verbs":["*"]}]', true)`. Route requires only `rbac:update` (`internal/server/routes_rbac_audit_agents.go:21`). `internal/handler/rbac_escalation_test.go` covers only binding creation. Rancher blocks exactly this: `rancher/pkg/api/norman/customization/globalrole/validator.go:29-37` strips every mutable field on a PUT when `gr.Builtin` is true.

**Why it matters** — A delegated RBAC administrator holding global `rbac:update` calls `PUT /api/v1/rbac/global-roles/{id}/` on the role they are already bound to with `{"rules":[{"resource":"*","verbs":["*"]}]}`; `h.invalidateAll()` at `rbac.go:258` dumps the cache, so on the next request they are full platform admin — the binding-time guard never runs because no binding was created. `DELETE` on the seeded `Administrator` role cascades away every real administrator's binding, locking the platform out with no recovery short of DB surgery. **[corrected]** Not reachable out of the box: sweeping all 34 templates, the only shipped role granting `rbac:update` is `platform-admin.yaml`, which is already `*:*`. `compliance-manager.yaml:17-19` grants `rbac:[read, list]` only. Exploitation requires an operator-authored custom role carrying `rbac:update`-without-`*` — precisely the delegated-RBAC-admin persona the binding guards exist for. Worth fixing before ship; not a live out-of-box hole.

**Remediation**
1. In `internal/handler/rbac.go` add `guardGlobalRoleRules(w, r, id, req.Rules)` and cluster/project variants; call each at the top of `UpdateGlobalRole:233`, `UpdateClusterRole:337`, `UpdateProjectRole:436`, before the `h.queries.Update*` call.
2. Each guard does two things: (a) load the existing row via `GetGlobalRoleByID`/`GetClusterRoleByID`/`GetProjectRoleByID` and reject with 403 `apierror.Forbidden` when `role.IsBuiltin` — mirroring Rancher's validator (allow display_name/description edits only if a softer policy is wanted); (b) call `h.enforceNoEscalation(w, r, defaultJSON(req.Rules), clusterID, projectID, "")` on the **incoming** rules with the same scope arguments the binding guards use.
3. Apply the identical `IsBuiltin` rejection in `DeleteGlobalRole:263`, `DeleteClusterRole:364`, `DeleteProjectRole:463`.
4. Add the escalation check to `CreateGlobalRole:200` / `CreateClusterRole:299` / `CreateProjectRole:398` so the catalog stays honest.
5. **Belt-and-braces, with a required handler change [corrected]:** appending `AND is_builtin = false` to the three `Update*` and three `Delete*` statements in `internal/db/queries/rbac.sql` and regenerating sqlc makes them `:one` queries returning `pgx.ErrNoRows`, which the handler **currently maps to a 500 (`UpdateError`)**, not the not-found path. Adjust the handler branch to map `pgx.ErrNoRows` here to 403/404 explicitly, or skip step 5 and rely on the Go guard.

**Tests**
- `internal/handler/rbac_escalation_test.go::TestUpdateGlobalRole_BlocksPrivilegeEscalation` — caller bound to a role granting only `rbac:[read,update]` PUTs rules `[{"resource":"*","verbs":["*"]}]` → 403, persisted rules unchanged.
- `::TestUpdateClusterRole_BlocksPrivilegeEscalation`, `::TestUpdateProjectRole_BlocksPrivilegeEscalation` (holds the permission on cluster A, attempts to write it into a role used on cluster B).
- `internal/handler/rbac_builtin_immutable_test.go::TestUpdateBuiltinRoleRejected`, `::TestDeleteBuiltinRoleRejected` — superuser targeting `is_builtin=true` gets 403 and the row survives.
- `::TestUpdateCustomRoleBySuperuserSucceeds` guards against over-blocking.
- All four escalation/builtin tests must fail on current main.

**Acceptance**
- [ ] A caller cannot write a permission into a role that they do not themselves hold at that scope.
- [ ] Built-in roles cannot be updated or deleted through the API.
- [ ] Superusers can still edit custom roles.
- [ ] No 500s introduced on the not-found path.

### [cluster-project-list-unfiltered] Cluster and project LIST return every row, and a cluster-scoped user gets 403 on the fleet landing page

**Status:** PENDING APPROVAL · **Severity** high · **Dimension** parity-core · **Effort** M

**Evidence** — `internal/handler/clusters.go:957-1003` `(*ClusterHandler).List` calls `h.queries.ListClusters(r.Context(), sqlc.ListClustersParams{Limit, Offset})` and `CountClusters(r.Context())` — no user, no bindings, no predicate. `internal/handler/projects.go:443-468` is the same shape. The only gate is `internal/server/routes_clusters.go:18` `requirePermission(..., rbac.ResourceClusters, rbac.VerbList)`; that middleware (`routes.go:1187-1228`) derives scope via `permissionScopeIDs(r)` (`:1232-1253`), which for the collection URL finds no `cluster_id` and no `id` and passes `uuid.Nil`. `(*Engine).bindingApplies` (`internal/rbac/engine.go:271-293`) then returns true only for global bindings — a cluster-scoped binding has `b.ClusterID != ""`, which can never equal `uuid.Nil.String()`. `rg 'AuthorizedClusters|visibleClusters|clusterIDsFor'` returns nothing: no post-query filter exists. Rancher filters per object (`rancher/pkg/rbac/user_based.go:76-100` `FilterList`).

**Why it matters** — **[corrected — the framing should be inverted from the original]** The "leak" half is weak: a global binding on `Read Only` (`*:read,list`, `001_initial.up.sql:689`) is by definition a platform-wide grant, so returning every cluster is what that binding says. The shipping-relevant half is the second: a user whose only grant is a `cluster_role_bindings` row (Cluster Owner on one cluster) gets a flat **403** on `GET /api/v1/clusters/` and `GET /api/v1/projects/`, so the fleet landing page — the primary surface of the product — is unusable for exactly the Rancher-style scoped persona a multi-tenant evaluation will create first.

**Remediation** — Step 1 is load-bearing; steps 2–3 keep the response correct once the gate is relaxed.

1. **Relax the route gate** at `routes_clusters.go:18` and the projects equivalent to an `engine.HasAnyNamespaceAccess`-style admission (`internal/rbac/engine.go:204-207`) or a new `HasAnyClusterAccess`, so a cluster-scoped-only user reaches the handler and receives a filtered page instead of a 403. The handler becomes the single enforcement point.
2. **Scoped SQL.** New queries in `internal/db/queries/clusters.sql` and `projects.sql`: `ListClustersForUser`/`CountClustersForUser` taking `user_id`, returning rows where the user holds a global binding, or a `cluster_role_bindings` row for that cluster, or a `project_role_bindings` row on a project owning namespaces on that cluster (join `project_namespaces`, the table `SQLCRBACQuerier.expandProjectBindings` at `internal/server/middleware/rbac_queries.go:217-259` already uses). Mirror for projects via `project_role_bindings` plus cluster-binding reachability. Push the predicate into SQL — a Go-side filter would break `total` and page sizes.
3. **Handler.** In both `List` methods read the caller with `middleware.GetAuthenticatedUser(r.Context())`, ask the injected `middleware.RBACQuerier` for bindings, short-circuit to the existing unfiltered query when `engine.CheckSuperuser(bindings)` or a global binding grants `clusters:list`/`projects:list`, else use the scoped query.
4. Audit the sibling collection endpoints for the same shape before shipping.

**Tests**
- `internal/handler/clusters_list_scope_test.go`: `TestList_ClusterScopedUserSeesOnlyBoundClusters` (exactly A, `total == 1`), `TestList_GlobalBindingSeesAll`, `TestList_SuperuserSeesAll`, `TestList_NoBindingsSeesEmpty` (200 + empty page, **not** 403, **not** full list).
- Mirror all four in `internal/handler/projects_list_scope_test.go`.
- `internal/server/routes_clusters_scope_test.go::TestClusterListRouteAdmitsClusterScopedUser` asserting the middleware no longer 403s a cluster-only binding.
- Every one fails on current main.

**Acceptance**
- [ ] A user bound only as Cluster Owner on one cluster loads `/dashboard/clusters` and sees exactly that cluster.
- [ ] Pagination `total` matches the filtered set.
- [ ] A user with zero bindings gets 200 and an empty page.
- [ ] Superuser and global-list behavior unchanged.

### [project-bindings-inert-by-default] Project role bindings grant nothing on cluster resources unless an off-by-default flag is set

**Status:** PENDING APPROVAL · **Severity** high · **Dimension** parity-core · **Effort** M

**Evidence** — `internal/server/middleware/rbac_queries.go:178-188` emits a project binding with only `binding.ProjectID` populated; `ClusterID` and `Namespace` stay empty. `(*Engine).bindingApplies` (`internal/rbac/engine.go:271-293`) matches it only when the request carries the same `projectID`; the k8s proxy URL `/api/v1/clusters/{cluster_id}/k8s/*` has no `project_id` param, so `permissionScopeIDs` (`routes.go:1243-1251`) returns `uuid.Nil` and the binding is skipped. `AuthorizedNamespaces` (`engine.go:222-263`) ignores it too — the switch at `:237-244` accepts only `isGlobal && Namespace==""` or `clusterMatch`. The only bridge is `expandProjectBindings` (`rbac_queries.go:217-259`), gated at `:199-205` on `q.namespaceScoping`, whose default is false: `internal/config/config.go:296` `envconfig.Default{Key: "namespace_scoped_rbac_enabled", Value: false}`, with the comment at `:119-130` deferring promotion "until the namespace-binding authoring UI ships". The same flag gates `routes.go:1347` and every `requireListPermission(..., deps.NamespaceScopedRBAC)`. Rancher has no such flag (`rancher/pkg/controllers/managementuser/rbac/prtb_handler.go:34-47`).

**Why it matters** — Out of the box a user bound as `project-owner` or `project-member` — the canonical Rancher tenancy grant, and 8 of 34 shipped templates are project-scoped — can see and do nothing with the workloads, pods, or namespaces in that project. Every `pods`, `workloads`, `custom_resources` rule in `project-owner.yaml`, `project-member.yaml`, `workload-deployer.yaml`, `namespace-operator.yaml` is inert on the k8s-proxy and workload-list routes. The workaround (`NAMESPACE_SCOPED_RBAC_ENABLED=true`) is undocumented in the template descriptions.

**Remediation** — **[corrected — the backend authoring surface the config comment calls blocking already exists, which materially lowers the cost of flipping the default]** `routes_projects.go:25-26` registers `POST /projects/{id}/add-namespace/` and `/remove-namespace/`, and `internal/handler/projects.go:113-145` performs the transactional `project_namespaces` writes plus RBAC-cache flush. Step 1 is therefore a frontend task only.

1. **Frontend only:** expose project→namespace assignment on the project detail route, calling the existing add/remove endpoints. Confirm `rbac_effective.go` stops reporting the seam as pending.
2. **Make expansion unconditional:** delete the `if q.namespaceScoping` branch at `rbac_queries.go:199-205` so `expandProjectBindings` always runs; drop `namespaceScoping`, `SetNamespaceScoping`, `NewSQLCRBACQuerierWithNamespaceScoping`. The expansion is already fail-closed (DB error propagates; a project with no namespaces contributes nothing), so a project with zero mapped namespaces behaves exactly as today.
3. **Flip the filter default:** set `namespace_scoped_rbac_enabled` to true in `internal/config/config.go:296` and in the chart values once (2) lands, so `requireK8sProxyPermission`'s allow-through-and-filter branch (`routes.go:1347-1396`) engages for project members. **Blocked on `namespace-filtered-watch-local-path-filters-raw-chunks` (P2)** — flipping this default without that fix makes namespace-scoped watches drop event batches.
4. Add a startup warning when any `project_role_bindings` rows exist while the flag is off, so existing installs learn their project grants are inert.

**Tests**
- `internal/server/middleware/rbac_queries_project_test.go::TestGetUserBindings_AlwaysExpandsProjectNamespaces` — a project binding plus two `ListProjectNamespaces` rows yields two synthetic `Scope:"cluster"` bindings with the right `ClusterID`/`Namespace`, no flag set.
- `::TestGetUserBindings_ProjectWithNoNamespacesContributesNothing`.
- `internal/server/routes_k8sproxy_project_binding_test.go::TestK8sProxy_ProjectMemberListsPodsInProjectNamespaces` — caller bound as `project-member` on a project owning `team-a` issues `GET /api/v1/clusters/{id}/k8s/api/v1/pods`, expects 200 filtered to `team-a`.
- `::TestK8sProxy_ProjectMemberDeniedOutsideProjectNamespaces` for `team-b`.
- All fail on current main.

**Acceptance**
- [ ] Project bindings resolve to namespace-scoped cluster bindings with no flag set.
- [ ] A project member can list pods in their project's namespaces and is denied elsewhere.
- [ ] Project→namespace assignment is reachable in the UI.
- [ ] Flag default flip is sequenced after the watch-filter fix.

### [no-downstream-impersonation] + [no-downstream-user-impersonation-agent-sa-is-cluster-admin] No downstream per-user impersonation

**Status:** PENDING APPROVAL · **Severity** high · **Dimension** parity-core / transport-security · **Effort** L
*(Two finding IDs, one defect. Fix once. This is a design item, not a patch.)*

**Evidence** — The agent builds ONE client from its own in-cluster SA and reuses it for every proxied request: `internal/agent/k8sproxy.go:98` (`rest.InClusterConfig()`), `:108` (`rest.TransportFor(cfg)`), `:225` and `:449` (`p.httpClient.Do(httpReq)`). Caller identity is deliberately erased: `:218-223` and `:442-447` drop every header not in the allowlist; `pkg/proxyhdr/proxyhdr.go:23-35` names `impersonate-*` and `x-remote-*` as explicitly excluded; `internal/tunnel/proxy.go:412-421` does the same server-side. Exec (`internal/agent/exec.go`), logs (`internal/agent/logs.go`) and helm (`internal/agent/helm.go`) all use the shared clientset from `K8sProxy.Client()` (`k8sproxy.go:129-131`). The only impersonation code that exists is dead: `internal/handler/kubectl_shell_scope.go:258-265` `CallerScope.ImpersonationHeaders()` returns `Impersonate-User: astronomer:user:<uuid>`, and `rg ImpersonationHeaders` matches only its definition, its doc comment, and `kubectl_shell_scope_test.go:50,89`. Rancher does the opposite: `rancher/pkg/clusterrouter/proxy/proxy_server.go:288-296` fetches a per-user impersonator token and sets it as the `Authorization` bearer; `rancher/pkg/impersonation/impersonation.go:34-39` provisions a `cattle-impersonation-<user>` ServiceAccount + ClusterRole per user.

**Why it matters** — **[corrected on two counts; the original title "every user's cluster action executes as one privileged agent SA" and "the agent SA is cluster-admin" both overstate]**
- Not *every* path. The kubectl-shell path already materializes downstream per-caller enforcement: `internal/handler/kubectl_shell_scope.go:4-25` and `:267-276` describe a per-session ServiceAccount bound to a ClusterRole/namespaced Roles derived from the caller's own astronomer bindings, and `kubectl_shell_test.go:738` asserts the superuser ClusterRoleBinding. **The gap is specifically the HTTP k8s-proxy and the WS exec/logs paths.**
- The agent SA is not cluster-admin by default: `deploy/agent/template.go:117-146` defaults to `viewer` and fails closed to `viewer`; `*/*/*` exists only in the opt-in `admin` profile (`:259-264`); `operator` is cluster-wide read plus a scoped write allowlist (`:363-407`). The management cluster's own SA is an enumerated allowlist (`deploy/chart/templates/serviceaccount.yaml:16-66`).
- The control plane *does* authorize every proxied request (`requireK8sProxyPermission`, `routes.go:1335-1400`). So this is a **missing second line of defense and an attribution gap**, not an open door. "Any single defect is instantly full cluster-admin" is true but conditional on that defect existing — and this report contains two such defects (P0 items), which is the argument for the second line.
- Concretely: the customer's downstream RoleBindings, OPA/Gatekeeper user-based policies, `kubectl get events` and the apiserver audit log all attribute every action to `system:serviceaccount:astronomer-system:astronomer-agent`, so a customer cannot forensically attribute a destructive change to a person, and cannot deny astronomer a capability downstream.

**Remediation** — Ship behind a per-cluster flag defaulting off, then flip after soak. **The remediation understates the work: setting `Impersonate-User` requires the agent SA to hold `impersonate` on users/groups (a privilege increase for viewer/operator) plus a mapping of astronomer roles to downstream ClusterRoles; without that mapping every impersonated request simply 403s.**

1. **Typed payload field, never a header.** Add `ImpersonateUser string` and `ImpersonateGroups []string` to `protocol.K8sRequestPayload` (`pkg/protocol/types.go`). **Do not add anything to `pkg/proxyhdr.forwardableHeaders`** — see `strength-proxy-header-allowlist`; the allowlist must remain a hard deny for caller-supplied identity headers.
2. **Populate server-side.** In `internal/tunnel/proxy.go` `buildK8sRequestPayload` (`:396-446`) and `internal/handler/k8s_requester.go`, populate from `appmiddleware.GetAuthenticatedUser(r.Context())` using the canonical identity `kubectl_shell_scope.go:263` already mints (`astronomer:user:<uuid>`) plus a group per applicable astronomer role name. Never from client headers.
3. **Stamp agent-side.** In `internal/agent/k8sproxy.go` `executeUpstream` and `HandleStreamRequest`, set `Impersonate-User`/`Impersonate-Group` on `httpReq` from that field only. Fail closed with a 403 Status object when the field is empty on a user-originated stream — **but exempt the machine paths in step 5, or self-manage breaks.**
4. **Downstream role materialization.** Add an agent-side reconciler (`internal/agent/project_reconciler.go` is the documented hook point) that materializes a ClusterRole/Role + Binding per astronomer role-template grant for the impersonated subject, mirroring `rancher/pkg/impersonation/impersonation.go`. Grant the agent SA `impersonate` on users/groups in `deploy/agent/template.go`. This is the largest piece and gates everything.
5. **Machine-identity exemptions, marked explicitly in the payload:** the ArgoCD internal proxy (`NewInternalArgoCDProxyRouter`, `routes.go:1461`) and the self-management path into `AstronomerOwnedNamespaces`.

**Tests**
- `internal/agent/k8sproxy_impersonation_test.go::TestExecuteUpstream_SetsImpersonationHeadersFromPayload` — httptest apiserver; payload with `ImpersonateUser: "astronomer:user:<uuid>"`; assert upstream carries `Impersonate-User` with that exact value plus the agent's own bearer in `Authorization`.
- `::TestExecuteUpstreamSetsImpersonationFromPayloadNotHeaders` — payload carries `impersonate.user=alice` **and** a spoofed `Impersonate-User: system:admin` in `req.Headers`; assert the outgoing request has exactly `Impersonate-User: alice`.
- `::TestExecuteUpstream_UserOriginatedRequestWithoutIdentityFailsClosed` — empty `ImpersonateUser` with `Origin: user` returns 403 and never reaches upstream; machine-origin payloads still pass.
- `internal/tunnel/proxy_impersonation_test.go::TestBuildK8sRequestPayload_PopulatesCallerIdentity` — authenticated context yields the canonical identity; anonymous yields empty; a client-supplied `Impersonate-User` header does not land in the payload.
- Regression: `pkg/proxyhdr/proxyhdr_test.go` must still assert `ShouldForwardRequestHeader("Impersonate-User") == false`.

**Acceptance**
- [ ] Downstream apiserver audit records show `astronomer:user:<uuid>`, not the agent SA, for user-originated proxy calls on a flag-enabled cluster.
- [ ] Caller-supplied `Impersonate-*` headers are still stripped.
- [ ] Self-manage and the ArgoCD internal proxy continue to work (machine exemption).
- [ ] Flag defaults off; enabling it on a cluster without materialized downstream roles produces a clear 403 rather than a silent failure.

### 5.3 ArgoCD

### [argocd-authz-anchored-to-management-cluster] Every ArgoCD route authorizes against the instance's cluster, never the destination cluster

**Status:** PENDING APPROVAL · **Severity** high · **Dimension** argocd · **Effort** L

**Evidence** — `internal/handler/argocd.go:2565-2580` `loadInstance()` resolves `{id}` → instance and calls `h.authz.authorizeClusterAction(w, r, instance.ClusterID, rbac.ResourceWorkloads, verb)`. Every lifecycle endpoint funnels through it with no second destination-scoped check: CreateApplication (`:2630`), PatchApplication (`:2663`), DeleteApplication (`:2698`), CreateProject (`:2729`), PatchProject (`:2774`), DeleteProject (`:2802`), CreateApplicationSet (`:2834`), DeleteApplicationSet (`:2939`), CreateRepo (`:3300`), DeleteRepo (`:3370`), SyncAppByName (`:974`). `RegisterManagedCluster` (`:2999-3013`) parses `cluster_id` and calls `GetClusterByID` for existence only — **no authorization on that cluster**; same for `RefreshManagedClusterLabels` (`:3153-3167`) and `UnregisterManagedCluster` (`:3229-3246`). `SyncApp` (`:728-735`) and `RefreshApp` (`:908-915`) load the instance from the app row and authorize on `instance.ClusterID`, never on the Application's `spec.destination.server`. The UI proxy repeats it: `internal/server/middleware/argocd_authz.go:66-80` anchors to `newLocalClusterResolver(...)` and `internal/server/routes.go:438-440` mounts it fleet-wide. `validateApplicationSetClusterGenerators` (`argocd.go:2871-2911`) only forces the cluster generator to carry `astronomer.io/managed-by=astronomer`; it does not restrict the selector to clusters the caller is granted. Route registration has no compensating middleware (`internal/server/routes_tools_controlplane.go:84-131`, only `featureGate("feature.argocd")`). In the shipped topology there is exactly one instance named `local` bound to the local cluster (`internal/server/self_manage_argocd.go:38`, `:254-286`).

**Why it matters** — A principal holding `workloads:update` on the management cluster can sync-with-prune or delete any Application deploying to any tenant cluster; `POST /instances/{id}/clusters/{victimClusterID}/register/` with `insecure=true` and an attacker-controlled `server` to re-point a cluster they do not own; `DELETE .../clusters/{victimClusterID}/register/` to break a tenant's GitOps delivery; and create an ApplicationSet fanning arbitrary Helm charts onto every cluster. **[corrected]** Escalation requires an existing grant on the cluster hosting the ArgoCD instance — the built-in `Cluster Operator` (`032_builtin_role_catalog.up.sql:20`) qualifies — so this is a lateral expansion of an already-privileged position, not an unauthenticated break. It is still a genuine cross-tenant authz gap: that grant should not confer sync/prune/delete/register over every other cluster's delivery.

**Remediation**
1. Add `authorizeDestination(w, r, instance, destinationServer string, verb rbac.Verb) bool` in `internal/handler/argocd.go`, mapping `spec.destination.server` → `argocd_managed_clusters.server_url` → `cluster_id` (via `ListArgoCDManagedClusters` or a new `GetArgoCDManagedClusterByServer` query) and calling `h.authz.authorizeClusterAction` on that cluster id. **Deny when the destination cannot be resolved.**
2. Call it from `SyncApp`, `SyncAppByName`, `RefreshApp`, `CreateApplication` (from `req.Spec.Destination`), `PatchApplication` (resolve the current app, then re-check), `DeleteApplication`. **[caveat]** Generated baseline Applications use the `{{server}}` template, so the destination is only resolvable on the live/upstream object — resolve against the upstream Application, not the stored spec, for those.
3. In `RegisterManagedCluster` / `RefreshManagedClusterLabels` / `UnregisterManagedCluster`, add an explicit `h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceClusters, rbac.VerbUpdate)` on the URL's `cluster_id` **in addition to** the instance check.
4. For `CreateApplicationSet`, require the caller to hold the verb on every cluster the generator selector currently matches (resolve via `ListArgoCDManagedClusters` + label match), or gate the endpoint on superuser.
5. **[corrected — prefer the defensible option]** For the UI proxy (`internal/server/middleware/argocd_authz.go`), restrict it to superusers, or resolve the application via the upstream API before deciding. Parsing `/argocd/api/v1/applications/{name}` out of the path is fragile because the ArgoCD SPA also issues stream/watch and resource-tree calls. Document the reduced surface either way.

**Tests** — `internal/handler/argocd_destination_authz_test.go`:
- (a) user with `workloads:update` bound to the management cluster only → `POST /api/v1/argocd/instances/{id}/clusters/{otherClusterID}/register/` returns **403**.
- (b) same user → `DELETE .../clusters/{otherClusterID}/register/` returns **403**.
- (c) same user → `POST /api/v1/argocd/applications/{id}/sync/` for an app whose destination resolves to another cluster returns **403**.
- (d) a user WITH `workloads:update` on the destination cluster gets **202**.
- All four must fail before the change.

**Acceptance**
- [ ] Register/unregister/refresh authorize the URL's `cluster_id`.
- [ ] Sync/refresh/create/patch/delete authorize the resolved destination cluster.
- [ ] Unresolvable destination is denied, not allowed.
- [ ] Self-manage and baseline reconcile loops still function (machine paths).

### [baseline-appset-floating-chart-version] Platform baseline ApplicationSets pin `targetRevision: "*"` with automated prune+selfHeal

**Status:** PENDING APPROVAL · **Severity** high · **Dimension** argocd · **Effort** M

**Evidence** — `internal/server/baseline_appsets.go:437-461` builds the generated Application spec with `"targetRevision": "*"` (`:442`) and `syncPolicy.automated = {prune: true, selfHeal: true}` (`:449-453`). Chart coordinates are public: `internal/baseline/registry.go:72-75` (kube-state-metrics) and `:83-86` (prometheus-node-exporter), both `https://prometheus-community.github.io/helm-charts`, both `DefaultEnabled: true`. The ApplicationSet is rewritten on every self-manage tick (`internal/server/self_manage_argocd.go:56` `localArgoBootstrapPeriod = 30s`, `:203-208`) and fans out to every cluster Secret matching `astronomer.io/managed-by=astronomer, astronomer.io/is-local=false` (`baseline_appsets.go:382-388`). Platform baseline management defaults ON (`baseline_appsets.go:165-177` returns true for a nil querier, a query error, or an unmarshal error). Same floating default in `internal/crd/controller.go:2646` (`strutil.FirstNonBlankTrimmed(revision, "*")`).

**Why it matters** — `targetRevision: "*"` resolves to the newest chart version on every reconciliation, so a new upstream release of kube-state-metrics or node-exporter is applied to every adopted cluster within minutes, unreviewed, with prune enabled. A single bad upstream release or a compromised public chart repo is a fleet-wide outage or supply-chain event with no pin, no staging, and no operator approval. It also breaks the air-gap story: every sync requires the management cluster's repo-server to reach `prometheus-community.github.io`. Rancher's ManagedChart carries an explicit `Version` (`rancher/pkg/apis/management.cattle.io/v3/managed_chart.go:26`). **[corrected]** Nothing is broken as shipped and there is a kill switch (the `argocd.manage_baseline` platform setting and per-component `argocd.baseline.<slug>` gates, `baseline_appsets.go:165-184`) — this is a default-on latent risk, not an active defect. It should not ship as-is.

**Remediation**
1. Add `ChartVersion string` to `internal/baseline/registry.go` `Component`; populate with pinned semver for kube-state-metrics and prometheus-node-exporter.
2. Plumb it through `baselineApplicationSetComponent` (`internal/server/baseline_appsets.go:47-65`) and `baselineComponentFromTool` (`:209-233`) so a `cluster_tools` row can override it.
3. Extend `baselineChartCoordinates` (`:114-119`) with a `version` JSON field so the tools catalog can carry the pin. **[corrected]** `baselineComponentFromTool` must carry it through, and **the pin must be validated as a semver constraint**, since ArgoCD interprets `targetRevision` for Helm charts as a constraint, not an exact version.
4. Replace the literal at `:442` with the pinned version, falling back to a compiled-in default rather than `*`.
5. `internal/crd/controller.go:2646` — make an unset ComponentBundle `targetRevision` a validation error instead of defaulting to `*`.
6. Add a platform setting to allow `*` explicitly for operators who want floating versions.

**Tests**
- `internal/server/baseline_appsets_test.go` — assert `baselineApplicationSetObject(...)` sets `spec.template.spec.source.targetRevision` to a pinned semver and never `"*"`; assert a tool-row override wins.
- `internal/crd/controller_test.go` — a ComponentBundle with an empty `source.targetRevision` fails validation instead of rendering `*`.
- A registry test asserting every `DefaultEnabled` component in `internal/baseline/registry.go` carries a non-empty, semver-valid `ChartVersion`.

**Acceptance**
- [ ] No generated Application carries `targetRevision: "*"` unless the explicit opt-in setting is on.
- [ ] Every default-enabled baseline component has a validated version pin.
- [ ] ComponentBundle validation rejects an unset revision.

### 5.4 Crypto / TLS

### [dev-keys-default-and-silent] World-readable dev Fernet and JWT signing keys are the chart defaults, accepted silently outside production

**Status:** PENDING APPROVAL · **Severity** high · **Dimension** crypto-tls · **Effort** S

**Evidence** — `deploy/chart/values.yaml:529` ships `secretKey: "local-dev-secret-key-change-in-production"` (the HMAC key for every access/refresh/purpose JWT — `internal/auth/jwt.go:605-606`) and `:534` ships `encryptionKey: "RX3rwYkQNmaSq4_UmGs7sPXONIjnB-M6q0gZtB79vQA="` (the Fernet key wrapping every stored credential). `deploy/chart/values.yaml:401-406` sets `config.env: development` with `debug: "true"` and `allowedHosts: "*"`. The chart guard that rejects both sentinels (`_helpers.tpl:417-423`) sits inside `astronomer.requireProductionInputs`, whose entire body is wrapped in `{{- if eq (default "" .Values.config.env) "production" }}` (`_helpers.tpl:378`), so it never fires under the shipped defaults. The runtime guard short-circuits identically: `internal/config/production.go:55-58` returns nil immediately when `!IsProduction(cfg)`, and it is the only consumer of `devSecretKey`/`devEncryptionKey`. Every non-test reference to both literals: `config/production.go`, a mirrored const block at `server/server.go:187-188` that is otherwise unused, the chart, `deploy/k8s/03-secret.yaml`, `deploy/docker-compose.yml`. There is **no log line, metric, or health degradation** anywhere for sentinel-in-use outside production; `server.go:344-358` warns only when the encryption key is entirely empty. `NewJWTManager` keeps `[][]byte{[]byte("")}` when no non-empty key is supplied (`internal/auth/jwt.go:148-154`) and signs with `m.secretKeys[0]` (`:606`).

**Why it matters** — `helm install astronomer ./deploy/chart` with no overrides produces a fully functional management plane whose JWT signing key is a public string in this repository. Anyone can mint `{user_id: <admin uuid>, token_type: "access"}` and sign it; the revocation checker (`internal/auth/jwt.go:420-477`) does not help because a forged token can carry any `iat`/`jti`. The same install encrypts every kubeconfig, git PAT, registry password, cloud credential, and SSO client secret under a published Fernet key. Operators get zero feedback. `env=production` is a posture flag with no enforcement relationship to actually being in production.

**Remediation** — **[corrected]** The sentinels are known-bad values, not empty strings, so the chart's existing `fail` machinery already has the right comparison. The only chart change needed is to **lift the sentinel checks out of the `config.env == production` wrapper at `_helpers.tpl:378`** — do not rewrite `templates/secret.yaml`. Note `deploy/docker-compose.yml` and `deploy/k8s/03-secret.yaml` are explicitly local-dev artifacts, not the shipped install path.

1. Move the sentinel comparisons in `_helpers.tpl:417-423` outside the `production` conditional so `helm template` with no `--set` fails unconditionally with a generation recipe (`openssl rand -base64 32`; the Fernet one-liner is already in the values comment) when `secrets.existingSecret` is unset and either value is the sentinel. Set both `values.yaml` defaults to `""`.
2. In `internal/config/production.go`, split the sentinel comparison out of `ValidateProductionSecurity` into an exported `DevSentinelsInUse(cfg) []string`, and call it **unconditionally** from `internal/server/server.go` (near `:359`) and `cmd/worker/main.go`: `log.Error` on every boot naming the sentinels, and increment a gauge `astronomer_insecure_dev_key_in_use{key="secret_key|encryption_key"}` so `deploy/chart/templates/prometheus-rules.yaml` can alert.
3. Surface the flag on the existing `/api/v1/admin` diagnostics that already report `Encryptor.KeyCount()` / `JWTManager.KeyCount()` so the UI can show a red banner.
4. Reject the empty-string JWT key outright in `NewJWTManager` (`internal/auth/jwt.go:148-154`) — return an error rather than a manager signing with a zero-length HMAC key.

**Tests**
- `internal/config/production_test.go` — `DevSentinelsInUse` returns both key names for a dev config with `Env: "development"` (no such function today).
- `internal/server/server_test.go` — booting with the dev sentinels and `env=development` emits an ERROR log containing `secret_key` and sets the gauge to 1.
- Chart: `helm template` with no `--set` must fail with the "generate a key" message instead of rendering the known literals (`make chart-test` / helm-unittest).
- `internal/auth/jwt_test.go` — `NewJWTManager` with no non-empty key returns an error.

**Acceptance**
- [ ] `helm template ./deploy/chart` with no overrides fails.
- [ ] Booting with a sentinel key logs ERROR and sets the gauge, in every env.
- [ ] A zero-length JWT signing key is impossible.
- [ ] Local dev still works via an explicit values file or `existingSecret`.

### [argocd-verify-ssl-fail-open] ArgoCD instance API defaults TLS verification OFF; PUT downgrades already-secure instances

**Status:** PENDING APPROVAL · **Severity** high · **Dimension** crypto-tls · **Effort** S

**Evidence** — `internal/handler/argocd.go:229` declares `VerifySsl bool \`json:"verify_ssl"\`` — a non-pointer bool, so an omitted field decodes to `false`. `CreateInstance` (`:472`) and `UpdateInstance` (`:587-588`, which reuses the same struct as a full replace) write `VerifySsl: req.VerifySsl` straight into the row, with **no preserve-on-omit branch** — contrast the token, which has exactly such a branch two lines above (`:582-585` restores `current.AuthTokenEncrypted` when omitted or when the sentinel is echoed). The DB default is the opposite: `internal/db/migrations/001_initial.up.sql:541` `verify_ssl BOOLEAN NOT NULL DEFAULT true`. Every other skip-verify toggle in the codebase is expressed in the safe direction (`tls_skip_verify BOOLEAN NOT NULL DEFAULT false` — migrations `055`, `067`, `058`). The UI sends `verifySsl: true` (`frontend/src/components/argocd/register-instance-modal.tsx:65`), so the defect is API-only.

**[Critical evidence correction]** The stored flag reaches TLS **twice**, not once: `instanceHTTPClient` (`argocd.go:2026-2039`) builds a dedicated `InsecureSkipVerify: true` client whenever `VerifySsl` is false, and that client is injected at `:2058-2061`, so the `Options.VerifySSL` branch in `internal/handler/argocd/client.go:189-192` is **bypassed when HTTPClient is non-nil** — the insecure client still wins.

**Why it matters** — Everything this client carries is a high-value credential sent to an operator-supplied `api_url`: the decrypted ArgoCD admin bearer token (`argocd_instances.auth_token_encrypted`, `argocd.go:2046`), and on `POST /api/v1/argocd/instances/{id}/repos/` the git repository password and SSH private key (`RepoCreateRequest.Password`/`.SSHPrivateKey`, `:3284-3295`). A `curl` create omitting `verify_ssl`, or any partial PUT (rename, change `api_url`) against an instance created with `verify_ssl=true`, silently flips to unverified TLS with no error and an audit record that logs `"verify_ssl": false` as if intended. An on-path attacker then harvests an ArgoCD admin token (= arbitrary manifest apply on every managed cluster) plus git deploy credentials. **[corrected]** Not remote-unauthenticated: the caller must already hold `clusters:update` on the target cluster (`argocd.go:462`, `:569`), and harvesting requires an on-path position.

**Remediation**
1. Change `CreateArgoCDInstanceRequest.VerifySsl` to `*bool` (`internal/handler/argocd.go:229`).
2. Add `func resolveVerifySSL(req CreateArgoCDInstanceRequest, current *bool) bool` returning `*req.VerifySsl` when non-nil, else `*current` when updating, else `true` on create.
3. Use it at `CreateArgoCDInstance` (`:472`) and `UpdateArgoCDInstance` (`:587`); the update path must pass `&current.VerifySsl`, matching the sentinel-preserve pattern already used for the token at `:577-585`.
4. **[corrected — necessary but not sufficient on its own]** Invert `internal/handler/argocd/client.go` `Options` to `SkipTLSVerify bool` so the zero value is secure, and **fix `instanceHTTPClient` (`argocd.go:2026`) in the same change** — otherwise flipping the Options field silently does nothing. Update the construction site at `:2058`. **Note:** `internal/worker/tasks/argocd_auto_register_cluster.go:650` is a *different* type (`argocdclient.TLSClientConfig`, the cluster registration payload) and is unrelated to this step — it is covered by `cluster-ca-never-persisted`.
5. `h.log.Warn` whenever an instance is written with verification disabled, mirroring `internal/handler/siem.go:364-367`.
6. Data migration is a no-op (existing explicit `false` rows stay operator-chosen); document the flip in the chart README.

**Tests** — table test in `internal/handler/argocd_test.go`:
- (a) `POST /api/v1/argocd/instances/` omitting `verify_ssl` must persist `verify_ssl=true` (**fails today** — persists false).
- (b) `PUT` with only `name`/`api_url` against a row stored `verify_ssl=true` must leave it `true` (**fails today** — flips to false).
- (c) explicit `"verify_ssl": false` still persists false.
- `internal/handler/argocd/client_test.go` — `NewClient(url, tok, Options{})` produces a client whose Transport does NOT set `InsecureSkipVerify`.
- New: assert `instanceHTTPClient` returns a verifying client for a row with `verify_ssl=true` and that no code path constructs `InsecureSkipVerify: true` for such a row.

**Acceptance**
- [ ] Omitted `verify_ssl` on create → stored true.
- [ ] Partial PUT preserves the stored value.
- [ ] `instanceHTTPClient` and `Options` agree; no residual insecure client for a verifying instance.
- [ ] A disabled-verification write logs a warning.

### 5.5 Agent

### [send-failclose-livelock] 256-slot send channel + fail-close + unpaced bulk producers

**Status:** PENDING APPROVAL · **Severity** high · **Dimension** agent · **Effort** L

**Evidence** — `internal/agent/tunnel.go:74` `sendCh: make(chan *protocol.Message, 256)`. `:533-549` `Send` is a non-blocking select whose default arm records a dropped event and spawns `go tc.failClose("send buffer full")`, which force-closes the entire WebSocket (`:656-671`: `setConnected(false)` + `conn.Close(websocket.StatusInternalError, ...)`). Four unpaced producers write into that same channel: (1) `internal/agent/state_subscriber.go:813-836` `replayAll()` iterates EVERY object in EVERY recorded informer store (Pods, Services, Nodes, ConfigMaps, Deployments, ReplicaSets, StatefulSets, DaemonSets + 15 metadata kinds + CRDs, recorded at `:536-543`, `:775-782`, `:394-402`) calling `emit` in a tight loop with no pacing; `emit` (`:762-768`) only logs on `Send` failure and its comment "we drop and let the next emit win the race" is false given `failClose`; (2) `internal/agent/mirror_subscriber.go:409` `replayAll()` does the same; (3) `internal/agent/k8sproxy.go:245-265` streams 16 KiB watch frames per read with no flow control; (4) `internal/agent/logs.go:107-120` emits one frame per log line; exec (`exec.go:224-237`) too. `runReconnectReplay` (`state_subscriber.go:789-805`) fires `replayAll` on every tunnel false→true edge.

**Why it matters** — On a large cluster the reconnect replay emits >10k frames as fast as `json.Marshal` runs into a 256-slot buffer drained at network speed; when it overflows, `Send` fail-closes the tunnel, the agent reconnects, and the false→true edge replays again. Second-order: after `failClose` the writeLoop exits, so every subsequent `Send` in the in-flight replay also fails (`failCloseOnce` dedupes the close). The same fires from a single `kubectl logs -f` on a chatty pod or a few concurrent UI watches — one noisy stream tears down the control channel for the whole cluster (all watches, exec sessions, heartbeats, decommission RPC). Rancher's remotedialer has per-connection backpressure windows and never kills the session because one stream is slow. **[corrected]** "Permanent flap on any non-trivial cluster" is overstated: `writeLoop` drains concurrently and the kernel/TLS send buffer absorbs thousands of ~200-byte frames, so small and medium clusters can replay without hitting 256 in flight. The honest statement: **large-cluster replays and any sustained bulk stream can saturate the 256-slot buffer, and when they do the connection is killed and the reconnect re-triggers the same replay.**

**Remediation** — Item 1 is the highest-value part and should land first.

1. **Restrict `failClose` to control-frame drops only.** A dropped data frame must fail that one stream (emit an end frame with an error), never the connection. This alone converts an outage into a degraded stream.
2. Split `sendCh` into priority classes, or give bulk/stream traffic its own bounded queue, so control frames (heartbeat, CONNECT, decommission ACK, upgrade result) can never be starved or collapsed by data frames.
3. Add `SendBlocking(ctx, msg)` that waits on `sendCh <- msg` with a timeout; use it from `state_subscriber.emit`, the mirror subscriber emit paths, `k8sproxy.sendStreamFrame`, `logs.HandleLogStart`, and the exec `tunnelWriter.Write`. Keep non-blocking `Send` only for fire-and-forget telemetry.
4. Pace `StateSubscriber.replayAll` and `MirrorSubscriber.replayAll` with a token bucket (~200 objects/sec) and abort the replay if the tunnel drops mid-way.
5. Add `astronomer_agent_tunnel_send_dropped_total{reason}` and a send-queue-depth gauge (see `agent-observability-gap`).

**Tests**
- `internal/agent/tunnel_test.go::TestSendDropOnDataFrameDoesNotCloseTunnel` — fill `sendCh`, send a `MsgStateUpdate`, assert `IsConnected()` stays true. **Fails today.**
- `internal/agent/state_subscriber_test.go::TestReplayAllPacedUnderBoundedSender` — register a store with 5000 cached objects and a sender whose channel holds 256; assert every object is eventually emitted and the connection watcher never observes a close. **Fails today.**

**Acceptance**
- [ ] A dropped data frame fails one stream, not the connection.
- [ ] A 5000-object replay completes over a 256-slot channel.
- [ ] Control frames are never dropped by data-frame pressure.
- [ ] Drop counter and queue-depth gauge are exported.

### [unbounded-handler-goroutines] `readLoop` spawns an unbounded goroutine per inbound message, each able to buffer 64 MiB

**Status:** PENDING APPROVAL · **Severity** high · **Dimension** agent · **Effort** S

**Evidence** — `internal/agent/tunnel.go:490-510`: every dispatched non-heartbeat, non-audit-ack message runs in `go func(m *protocol.Message){...}` with no semaphore, worker pool, or in-flight cap. Production wiring for `MsgK8sRequest` is `AdaptStreamingHandler(k8s.HandleRequestStreaming)` (`cmd/agent/main.go:124`), which funnels through `executeUpstream` (`internal/agent/k8sproxy.go:418-485`): `io.ReadAll` of a `LimitReader` at `maxK8sResponseBodyBytes+1` (`:172`, 64 MiB) then a base64 copy (+33%) — chunking happens only on the way out, after the whole body is buffered. The comment at `:457-462` names the exact risk ("with goroutine-per-message dispatch, several concurrent large reads multiply this and OOM the agent pod") but only the per-request cap was added. Container limit 512Mi (`deploy/agent/install.yaml.template:675-677`). No semaphore or `maxConcurrent` bound exists on either side of the tunnel.

**Why it matters** — Two concurrent large LISTs already exceed 512Mi; a handful guarantees an OOMKill. Reachable from ordinary UI use (several users opening resource lists on a big cluster) and trivially weaponizable by anything that can drive the server's k8s-proxy for a cluster. Each OOM restart re-lists all informers and re-triggers the reconnect replay (`send-failclose-livelock`), so the failure amplifies rather than settles.

**Remediation**
1. Add a buffered semaphore in `internal/agent/tunnel.go` (`inflight chan struct{}`, size ~16, configurable via `ASTRONOMER_MAX_INFLIGHT_REQUESTS`), acquired before the `go func` dispatch at `:490` and released in its defer.
2. **[corrected — do NOT apply backpressure by stalling the reader]** `readLoop` is the only reader, so stalling it also stops heartbeats/pongs and the server reaps the agent within ~20-30s (`internal/tunnel/server.go:732-753`). On acquisition failure, reply immediately with a `MsgError` carrying a retryable code so the server-side originator fails fast, and **keep `readLoop` draining**.
3. Exempt cheap control types (heartbeat/pong/exec input/resize/stream stop) from the semaphore so a saturated proxy queue cannot deadlock session control.
4. Derive `maxK8sResponseBodyBytes` from the container limit or `GOMEMLIMIT` rather than a fixed 64 MiB — one 64 MiB body plus its base64 copy is already ~17% of 512Mi before allocator overhead. Stream large unary responses through the existing chunked path instead of `io.ReadAll`.

**Tests**
- `internal/agent/tunnel_test.go::TestInflightRequestsAreBounded` — register a handler that blocks on a channel, push N=100 `MsgK8sRequest` frames; assert at most `maxInflight` handler invocations are concurrently active and the rest are queued or rejected with `MsgError`.
- `::TestControlFramesBypassInflightLimit` — with the semaphore saturated, a heartbeat still gets a pong.

**Acceptance**
- [ ] Concurrent proxied requests per agent are bounded and configurable.
- [ ] `readLoop` never stalls; heartbeats survive saturation.
- [ ] Rejections carry a retryable error code.
- [ ] Response body cap is derived from the memory limit.

### [heartbeat-full-cluster-list-storm] Unpaginated cluster-wide LISTs of all pods and nodes every 30s/60s

**Status:** PENDING APPROVAL · **Severity** high · **Dimension** agent · **Effort** M

**Evidence** — `internal/agent/health.go:314-327` `collectHeartbeat` calls `client.CoreV1().Nodes().List(...)` and `client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})` — no `Limit`, no `ResourceVersion: "0"`, no field selector — every heartbeat tick (default 30s, `internal/agent/config.go:120`). `:148-158` `collectMetricsPayload` repeats both full LISTs plus a per-namespace aggregation on the metrics tick (default 60s, `config.go:121`), and `:363-386` lists nodes a third time. `Start` (`:95-116`) fires both tickers with no connectivity gate, and collection happens BEFORE the send (`:256-280`, `:119-137`), so it continues at full rate while the tunnel is down. Because `ResourceVersion` is unset these are **quorum reads served from etcd, not the apiserver watch cache** — the worst variant. The agent already runs synced Pod and Node informer caches for exactly these objects (`internal/agent/state_subscriber.go:468-470`).

**Why it matters** — Every 30s each adopted cluster serves a full unpaginated pod list to its own agent: on a 10k-pod cluster, a multi-hundred-megabyte apiserver serialization plus the same allocation in the agent, twice a minute, forever. This is precisely the workload that pushes kube-apiserver into OOM/latency collapse, and operators will correctly blame the agent. Allocation churn also fights the 512Mi limit. **[corrected]** The tickers do not start until the first successful connect (`cmd/agent/main.go:303-311` polls `tunnel.IsConnected` before `health.Start`), so the "keeps hammering during a management-plane outage" behavior applies after the first connection — which is the steady state anyway.

**Remediation**
1. Take counts from the informer caches: give `HealthReporter` a `SetCountSource(func() (nodes, pods int, ok bool))` wired in `cmd/agent/main.go` to `StateSubscriber` store lengths, falling back to a List only when the subscriber never synced.
2. **Smallest correct fix if (1) is too invasive:** where a List must remain, pass `metav1.ListOptions{ResourceVersion: "0", Limit: 500}` and page, so it is served from the watch cache and never allocates the whole set at once.
3. Gate collection on connectivity: skip the `collectHeartbeat`/`collectMetricsPayload` bodies when `hr.connected.Load()` is false (emit a cheap liveness-only frame or nothing) and back the tickers off while disconnected.
4. Same treatment for the per-namespace pod-metrics aggregation in `collectMetricsPayload`.

**Tests**
- `internal/agent/health_test.go::TestHeartbeatUsesInformerCountsAndDoesNotListPods` — fake clientset with a reactor that fails the test if a cluster-wide pod List is issued once a count source is wired.
- `::TestCollectorsSkipWhenDisconnected` — `connected=false` ⇒ zero API calls over N ticks.
- Both fail today.

**Acceptance**
- [ ] Steady-state heartbeat issues zero cluster-wide unpaginated LISTs.
- [ ] Any remaining List uses `ResourceVersion: "0"` and pages.
- [ ] Disconnected agent does no collection work.

### [self-upgrade-no-verification-no-rollback] Self-upgrade accepts any image, reports success before rollout, never uses the computed rollback image

**Status:** PENDING APPROVAL · **Severity** high · **Dimension** agent · **Effort** M

**Evidence** — `internal/agent/self_upgrade.go:65-111` `patchAgentDeployment` takes `payload.TargetImage` verbatim; the only validation is TrimSpace + non-empty (`:72-75`) before it is written into the container spec (`:90`) — no registry allow-list, no digest pinning, no signature check. `HandleUpgrade` sets `result.Success = true` the instant the Update call returns (`:44-52`, message "agent deployment image patched"), and `internal/tunnel/handler.go:261-268` turns that ack into `StatusSucceeded`, so the heartbeat-based confirmation `MarkRunningAgentUpgradeSucceededByVersion` (`handler.go:167-183`) is dead on the normal path. `RollbackImage` is computed and persisted in the plan (`internal/handler/agent_fleet.go:1541-1544`, `1574-1576`, `1588`) and rendered as prose in `PostUpgradeHealthChecks`, but `protocol.AgentUpgradePayload` (`pkg/protocol/types.go:271-278`) carries only OperationID/ClusterID/TargetVersion/TargetImage/AgentNamespace/AgentDeployment — no rollback field, and grep finds no rollback code path anywhere. `deploy/agent/install.yaml.template:562-563` is `strategy: type: Recreate`.

**Why it matters** — A typo'd or unpullable image leaves the Deployment permanently broken while the fleet UI reports `succeeded`. With `Recreate` the old pod is terminated FIRST, so a bad image takes the cluster fully dark — no tunnel, therefore no way to push a corrective upgrade; recovery requires manual kubectl on every affected cluster. Because the fleet believes it succeeded, a batched rollout marches on and repeats the outage. **[corrected]** Drop the "anyone who can reach the upgrade API" framing: `POST /agents/fleet/{cluster_id}/upgrade/` is gated by `requirePermission(ResourceAgents, VerbUpdate)` (`internal/server/routes_rbac_audit_agents.go:93-94`), so the arbitrary-image angle is escalation available to an already-authorized fleet operator or a compromised management plane, not an unauthenticated one. **The operational half — premature success, no rollout verification, no rollback, Recreate — is what should drive the fix; items 2 and 4 below are load-bearing.**

**Remediation**
1. Add `validateTargetImage` in `internal/agent/self_upgrade.go`: require a configured allow-list prefix supplied at install time (new `ASTRONOMER_AGENT_IMAGE_REPOSITORY` rendered into `deploy/agent/install.yaml.template` and read in `internal/agent/config.go`), and prefer digest form (`@sha256:`), rejecting bare mutable tags unless explicitly permitted.
2. **Change success semantics.** After the Update, poll the Deployment status (or watch its ReplicaSet) with a bounded timeout; report `Success=true` only once `UpdatedReplicas == Replicas && AvailableReplicas >= 1`. Otherwise report `Success=false` with the pod's waiting reason (ImagePullBackOff/CreateContainerError).
3. Carry `RollbackImage` in `protocol.AgentUpgradePayload` and, on rollout-verification failure, re-patch the Deployment back to it before returning the failed result.
4. **Stop marking the operation succeeded on the patch ack** (`internal/tunnel/handler.go:261-268`) — move to `running` and let `MarkRunningAgentUpgradeSucceededByVersion` (`handler.go:167`) be the only success edge, with a server-side sweeper failing operations stuck in `running` past a deadline.

**Tests**
- `internal/agent/self_upgrade_test.go::TestUpgradeRejectsImageOutsideAllowedRepository`
- `::TestUpgradeReportsFailureWhenRolloutNeverBecomesAvailable` — fake clientset whose Deployment status never updates ⇒ `Success=false`.
- `::TestUpgradeRepatchesRollbackImageOnFailedRollout`
- `internal/tunnel/handler_test.go::TestUpgradeAckDoesNotTerminateOperation` — an ack with `Success=true` leaves status `running` until a heartbeat reports the target version.

**Acceptance**
- [ ] An unpullable image produces a failed operation, not a succeeded one.
- [ ] The agent rolls itself back to `RollbackImage` on failed rollout.
- [ ] Images outside the configured repository are rejected.
- [ ] The stuck-`running` sweeper exists and fires.

### 5.6 Parity — features

### [catalog-sync-oci-git-blind-and-abort] Scheduled catalog sync cannot sync OCI (or git) repos and aborts the whole sweep on the first error

**Status:** PENDING APPROVAL · **Severity** high · **Dimension** parity-features · **Effort** M

**Evidence** — `internal/worker/tasks/catalog_sync.go:67-89`: the `catalog:sync` periodic task (registered `@every 6h` at `internal/worker/scheduler.go:50`) does `ListEnabledHelmRepositories(ctx)` then, for every repo unconditionally, `repositoryIndexURL(repoRecord.Url)` → `fetchRepositoryIndex(...)`. `repositoryIndexURL` (`:125-133`) appends `/index.yaml` to whatever URL is stored; `fetchRepositoryIndex` (`:111`) issues a plain `http.Client.Do`. No OCI branch exists in the file. OCI is first-class: `internal/handler/catalog.go:341-343` auto-detects `oci://` and sets `repo_type="oci"`, `internal/handler/catalog_oci.go:70` implements a full OCI ingest, and the UI offers the toggle (`frontend/src/routes/dashboard/catalog/index.tsx:1033`). Every failure path in the loop is `return err` (`:77, 81, 84, 87`), so the first bad repo halts the sweep for every repo after it (`ListEnabledHelmRepositories`, `internal/db/queries/catalog.sql:23-24`, returns all enabled repos including oci/git rows). The worker never applies repo credentials — `applyRepoIndexAuth` (`internal/handler/catalog.go:581-604`) exists only on the handler path. The worker caps at `catalogMaxVersionsPerChart = 3` (`catalog_sync.go:29-30`, applied `:156-157`) while the handler ingests every version (`catalog.go:707-753`).

**Why it matters** — An admin adds an OCI registry (ghcr.io / ECR / Harbor); the UI accepts it, the manual Sync button works (the handler branches to `fetchAndIngestOCIRepo`), and then the catalog silently stops updating: the 6-hourly sweep fails on the `oci://` URL and returns, so **no repo after it in the list is refreshed either**. New chart versions never appear fleet-wide, with no UI error, only a worker log line. Same for a private HTTP repo (401) or a transient DNS blip. **[corrected]** Two nuances: an `oci://` URL fails before `client.Do` — `httpclient.GuardPublicHost` (`catalog_sync.go:105-107`) rejects it — but the effect is the same `return err` abort; and operator-triggered sync (`POST .../sync/`, `catalog.go:497-503`) does branch to OCI and does apply credentials, so the break is confined to the unattended sweep.

**Remediation** — Item 1 is the highest-value half and should land first.

1. **Per-repo error isolation.** Wrap the per-repo body in a closure that logs and records the error and `continue`s instead of `return err`, accumulating a joined error after the loop.
2. **Branch on repo type.** Export/move `handler.IsOCIRepo` and `isOCIRepoSpec` (`internal/handler/catalog_oci.go:31`, `internal/handler/catalog.go:1502`) into a shared package (e.g. `internal/catalog`) and call the OCI ingest for oci repos. Better: extract `fetchAndIngestRepoIndex`/`fetchAndIngestOCIRepo` out of `CatalogHandler` into `internal/catalog/ingest.go` taking a Querier + `*http.Client`, and have both the worker and the handler call it.
3. Apply `applyRepoIndexAuth` in the worker path.
4. Drop `catalogMaxVersionsPerChart` or apply the same cap on both paths so version sets are deterministic.
5. For `repo_type="git"` (accepted at `internal/handler/catalog.go:346-349`) either implement clone+index or reject at create — see `git-backed-chart-repos-accepted-never-synced` (P3), which recommends rejecting.

**Tests**
- `internal/worker/tasks/catalog_sync_test.go::TestHandleCatalogSyncSkipsOCIAndContinuesAfterError` — three enabled repos `[oci://registry.test/charts, https://broken.invalid, https://good.test]`; assert (a) the OCI repo is routed to the OCI ingest (or skipped, not HTTP-fetched), (b) `UpdateHelmRepositoryLastSynced` is still called for the good repo, (c) the task returns a non-nil aggregated error naming the broken repo.
- `::TestCatalogSyncAppliesRepoAuth` — Authorization header present on the index request for a basic-auth repo.
- Both fail today.

**Acceptance**
- [ ] One failing repo does not starve the rest of the sweep.
- [ ] OCI repos are refreshed by the unattended sweep.
- [ ] Private repos authenticate in the worker path.
- [ ] Handler-sync and worker-sync produce the same chart-version set.

### [monitoring-stack-lifecycle-no-ui] The entire monitoring-stack lifecycle has no UI and no CLI

**Status:** PENDING APPROVAL · **Severity** high · **Dimension** parity-features · **Effort** L

**Evidence** — Backend is complete and routed: per-cluster at `internal/server/routes_clusters.go:78-85` (`GET/PUT /clusters/{id}/monitoring/config/`, `GET .../stack/status/`, `POST .../stack/preview|install/`, `PUT .../stack/upgrade/`, `POST .../stack/replace/`, `DELETE .../stack/uninstall/`) and shared-stack at `internal/server/routes.go:584-600` (`/monitoring/backend/`, `/monitoring/thanos/{status,preview,install,upgrade,replace,uninstall}/`, `/monitoring/alertmanager/{...}/`). It installs kube-prometheus-stack with Thanos sidecar + object storage (`internal/handler/monitoring.go:1924-1975`, `2665-2673`) and a shared Thanos query/store/compact/ruler stack (`:2020-2081`) — ~3,800 lines with an operations queue, preview, auto-rollback and retry. The frontend touches none of it: `rg -n 'monitoring/stack|monitoring/thanos|monitoring/alertmanager|monitoring/backend|monitoring/config' frontend/src` returns zero hits; `frontend/src/routes/dashboard/monitoring/index.tsx:21-46` is only a cluster-picker over `useClusterMetrics`/`useClusterNodes`; there is no cluster-scoped Monitoring nav entry (`frontend/src/components/layout/sidebar.tsx:170-215`). `internal/astrocli` references none of these paths.

**Why it matters** — In Rancher this is the most-used monitoring flow: Cluster → Monitoring → Install, tweak retention/storage/Grafana, Upgrade, Uninstall (`ui/app/router.js:70` per-cluster, `:130` per-project). An astronomer admin lands on Monitoring, sees empty charts because no Prometheus exists, and has no path forward. It strands dependents: shared Thanos is the backend the metrics APIs read from (`internal/handler/monitoring.go:1657`) and shared Alertmanager is where alert routing lands. **[corrected]** "No path forward inside the product" is not strictly true — an operator can install kube-prometheus-stack by hand through the Catalog/Apps UI (`frontend/src/lib/catalogs/suggested.ts:29-36`) and baseline Tools seed kube-state-metrics/node-exporter. What is unreachable is **astronomer's own managed stack lifecycle** (Thanos sidecar + object storage + backend wiring + operations/rollback), so a hand-installed chart is not wired into the backend the metrics APIs read.

**Remediation**
1. Add `frontend/src/routes/dashboard/clusters/$id/monitoring/index.tsx` and a nav item in `getClusterNavGroups` (`frontend/src/components/layout/sidebar.tsx:195-215`, permission `{resource:'monitoring',verb:'read'}`, featureFlag `feature.monitoring`).
2. Page content: a status panel bound to `GET /clusters/{id}/monitoring/stack/status/`; an install/upgrade form over the `MonitoringStackRequest` fields (retention, storageSize/Class, scrapeInterval, enableGrafana/Alertmanager, thanosSidecarEnabled, storageConfigId) that first calls `.../stack/preview/` and renders the sanitized values diff; Uninstall behind `ConfirmDialog`.
3. Add a platform-level tab under `/dashboard/monitoring` for the shared Thanos + Alertmanager stacks over `/monitoring/thanos/*` and `/monitoring/alertmanager/*`, plus the backend config editor over `/monitoring/backend/` (**gated by the authz fix in `monitoring-go-3815-line-split` Part A — do not build the editor against unauthenticated routes**).
4. Reuse the operations-table pattern from `frontend/src/routes/dashboard/logging/index.tsx` (Operations tab) against `GET /monitoring/operations/` so installs are observable.
5. Add the API layer in `frontend/src/lib/api/` (not the `api.ts` monolith — see `api-hooks-flat-2700-line-modules`) and hooks in `frontend/src/lib/hooks.ts`.

**Tests**
- `frontend/src/routes/dashboard/clusters/$id/monitoring/index.test.tsx` — render against a mocked `stack/status` response; assert Install is disabled when `status=installed`, Upgrade/Uninstall shown when installed, and clicking Install issues `POST /clusters/{id}/monitoring/stack/install/` with the form values.
- Extend `internal/server/route_table_test.go` (or `route_dump_test.go`) with an assertion that every registered `/monitoring/**` route has at least one referencing call site in `frontend/src` or `internal/astrocli`, so API-only surfaces are caught at build time.

**Acceptance**
- [ ] An operator can install, upgrade, and uninstall the managed monitoring stack for a cluster entirely in the UI.
- [ ] Preview diff is shown before install/upgrade.
- [ ] Operations are observable and retryable from the UI.
- [ ] The API-only-surface route test exists and is green.

---

## 6. P2 — medium

### 6.1 Parity — core

### [group-membership-only-refreshed-at-login] IdP group membership is reconciled only on SSO login

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** parity-core · **Effort** M

**Evidence** — `internal/auth/group_sync.go:93-105` documents `SyncUserGroups` as the reconciler that adds and revokes `source='group_sync'` bindings. Grep over non-test Go yields exactly two production call sites: `internal/handler/sso.go:437` (SSO callback, `true, // claims are fresh on every SSO callback`) and `internal/handler/group_mappings.go:345` (manual admin `ResyncUser`). `internal/worker/scheduler.go:81-83` registers only `RefreshGroupSyncMetricsType` (a gauge, @every 5m) — no membership reconcile, and no group task exists in `internal/worker/tasks/`. Authorization reads only materialized rows: `internal/db/queries/rbac.sql:142-186` `ListUserBindingsWithRoles` filters each UNION arm on `user_id`, fronted by a TTL cache (`internal/server/middleware/rbac_queries.go:135-139`). Rancher runs a refresh daemon: `rancher/pkg/auth/providerrefresh/daemon.go:22-39`, default `auth-user-info-max-age-seconds` 3600 (`rancher/pkg/settings/setting.go:183`).

**Why it matters** — An employee removed from `platform-admins` in Okta/Entra keeps their bindings until they next complete an interactive SSO login. With refresh tokens and long-lived sessions that can be days or indefinitely, and an off-boarded user has no reason to log in again, so revocation may never fire. Manual `ResyncUser` requires knowing which user to resync, which does not scale to a group deletion.

**Remediation** — **[corrected — sequence matters; step 2 is the part that actually closes the finding, not an optional extra]**
1. New task `internal/worker/tasks/group_sync_refresh.go` registered alongside the existing periodic tasks: page through users having any `source='group_sync'` binding, read the stored claims snapshot from `user_idp_groups` (`GetUserIDPGroups`, already in the `SCIMQuerier` surface at `internal/handler/scim.go:83`) and call `auth.SyncUserGroups` with `claimsAvailable=true`. **This only catches SCIM/admin-driven edits — for an Okta group removal with no SCIM the snapshot is exactly as stale as the bindings.**
2. **Provider-side refresh (the load-bearing step).** For Dex/OIDC connectors, store the refresh token at login and use it to re-fetch the `groups` claim before calling `SyncUserGroups`. When the refresh fails, pass `claimsAvailable=false` so the existing policy at `group_sync.go:120-126` declines to revoke on a transient outage rather than mass-deprovisioning.
3. Config keys mirroring Rancher — `auth_group_resync_interval_minutes` (default 60) and an on/off switch — in `internal/config/config.go` next to the other auth defaults.
4. On revocation, invalidate the RBAC cache for the affected user (`SQLCRBACQuerier.Invalidate`) and consider revoking their JWTs via `internal/auth/jwt_revocation` so the change is immediate rather than TTL-bounded.

**Tests** — `internal/worker/tasks/group_sync_refresh_test.go`: `TestGroupSyncRefresh_RevokesBindingWhenGroupRemovedFromSnapshot` (binding deleted, `Invalidate` called), `TestGroupSyncRefresh_AddsBindingWhenGroupAdded`, `TestGroupSyncRefresh_SkipsRevocationWhenClaimsUnavailable` (provider fetch error leaves bindings intact), `TestGroupSyncRefresh_SkipsUsersWithOnlyManualBindings`. All fail today (the task does not exist).

**Acceptance**
- [ ] Removing a user from an IdP group revokes their bindings within the configured interval without an interactive login.
- [ ] A provider outage never mass-revokes.
- [ ] Manual bindings are untouched.

### [api-token-no-max-ttl] API tokens can be created with no expiry, and the shipped max-TTL setting is ignored

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** parity-core · **Effort** S

**Evidence** — `internal/handler/auth.go:1488-1494`: `var expiresAt pgtype.Timestamptz; if req.ExpiresInDays > 0 { ... }`. When `expires_in_days` is omitted or 0, `expiresAt` stays `{Valid:false}` and that NULL is persisted verbatim at `:1517-1525`. No clamp, no upper bound; the only pre-checks are the per-user token quota (`:1477-1487`) and CIDR parsing (`:1506-1515`). The request struct at `:1391` declares `ExpiresInDays` as a plain int with no validation tag. **[critical evidence correction — this strengthens the finding and changes the remediation]** The platform setting **already exists**: `internal/handler/platform_settings.go:140` defines `"token.max_ttl_min"` `{Type: typeInt, Default: 525600, Description: "Maximum allowed API token expiry in minutes (1 year default)"}`, and a repo-wide grep for `max_ttl_min` finds that definition **and nothing else**. Rancher clamps: `rancher/pkg/settings/setting.go:180` `auth-token-max-ttl-minutes` default 129600, with `ClampToMaxTTL` in `rancher/pkg/auth/tokens/manager.go`.

**Why it matters** — An operator can set a maximum token TTL in the admin UI today and it is **silently ignored** — worse than an absent control, because it is a false compliance assurance. Any user who can mint an API token (self-service) creates a never-expiring bearer credential; the only remedy is manual per-token revocation after discovering it.

**Remediation** — **Wire the existing key; do not introduce a new one.**
1. Read `token.max_ttl_min` through the existing `SettingsCache` (`internal/handler/settings_cache.go`) in `CreateToken` (`internal/handler/auth.go` ~`:1488`).
2. Add `auth.token_allow_no_expiry` (default false) for the service-account case.
3. Compute `maxMinutes` from the setting: when `req.ExpiresInDays <= 0`, set `expiresAt = now + maxMinutes` (or reject with 400 when `token_allow_no_expiry` is false and the caller must be explicit); when the request exceeds the ceiling, clamp and **surface the clamped value in the response**.
4. Record the effective expiry in the audit detail map at `:1536` alongside the existing scope-set and CIDR-count fields.
5. Backfill/reporting: a worker task listing tokens with `expires_at IS NULL`, surfaced on the admin token screen; stamp them with the ceiling only behind an explicit operator action. **Do not silently expire existing credentials on upgrade.**
6. Apply the same ceiling to SCIM tokens (`internal/auth/scim_token.go`) and agent ingest tokens if they share the shape.

**Tests** — `internal/handler/auth_token_ttl_test.go`: `TestCreateToken_DefaultsToMaxTTLWhenExpiresInDaysOmitted`, `TestCreateToken_ClampsExpiresInDaysToMax` (3650 days vs a 90-day setting → ~90, response reports the clamp), `TestCreateToken_RejectsNoExpiryWhenDisallowed`, `TestCreateToken_HonorsShorterRequestedTTL`. First three fail today.

**Acceptance**
- [ ] `token.max_ttl_min` actually constrains token creation.
- [ ] Omitted expiry no longer persists NULL.
- [ ] Existing never-expiring tokens are reported, not silently killed.

### [project-quota-no-aggregate-accounting] Project quota is replicated per namespace with no project-level ceiling or used-limit

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** parity-core · **Effort** M

**Evidence** — `internal/worker/tasks/project_reconcile.go:324-358` `reconcileProjectNamespace` runs per (project, cluster, namespace) and applies the project's quota to each namespace independently: `renderResourceQuota(namespace, project.ResourceQuota)` server-side-applied to `/api/v1/namespaces/%s/resourcequotas/%s` (`:342-345`), and the explicit-policy path (`:352-357`) does the same with `renderProjectResourceQuota`, which (`:823-836`) copies the scalar columns `ResourceQuotaCpuLimit`/`MemoryLimit`/`PodCount` onto `spec.hard`. No summation, no ceiling comparison, no persisted used-limit anywhere in the file or in `internal/handler/projects.go`. Rancher: `rancher/pkg/controllers/managementuser/resourcequota/resource_quota_calculate_used.go:164-180` sums per-namespace limits into `Spec.ResourceQuota.UsedLimit`, alongside `resource_quota_validate.go` and `namespace_quota_reset.go`.

**Why it matters** — A project configured with "10 CPU" spanning five namespaces actually grants 50 CPU. Adding a sixth namespace silently raises the real ceiling by another 10 with no validation and no signal. There is no `used` figure, so an operator cannot answer "how much of project P's allowance is consumed". For a platform pitching multi-tenant containment, the headline containment control does not contain. **[corrected]** Slight overstatement in the original: the API field names (`resource_quota_cpu_limit` etc. on the project object) carry no doc text claiming project-wide totals, so this is an under-specified control rather than an actively mislabeled one. The concrete defects are (a) no aggregate ceiling, (b) no used-limit visibility, (c) adding a namespace silently multiplies the allowance.

**Remediation**
1. Schema: add `namespace_default_resource_quota` (JSONB, same shape as `resource_quota`) alongside the existing project-level fields, and `resource_quota_used_limit` JSONB for the computed aggregate.
2. Validation: before `AddNamespace` (`internal/handler/projects.go`) and `UpdatePolicy` (`:805`), compute the sum of per-namespace defaults across existing `project_namespaces` plus the incoming one and reject with 400 when it exceeds the ceiling — mirror `rancher/.../resource_quota_validate.go`.
3. Reconcile: in `reconcileProjectNamespace` (`project_reconcile.go:324`) apply the **per-namespace default**, not the project total; after the loop, recompute the sum and persist to `resource_quota_used_limit`.
4. **Migration is mandatory, not optional:** copy the current `resource_quota` into `namespace_default_resource_quota` so behavior is byte-identical on upgrade, and leave the project ceiling unset (unbounded) until an operator opts in. Flipping the existing column's meaning would tighten running workloads on upgrade.
5. Surface `used` and `ceiling` in `projectToResponse` and the project detail UI.

**Tests** — `internal/worker/tasks/project_reconcile_quota_test.go`: `TestReconcile_AppliesNamespaceDefaultNotProjectTotal`, `TestComputeUsedLimit_SumsAcrossNamespaces`. `internal/handler/projects_quota_validation_test.go`: `TestAddNamespace_RejectsWhenSumExceedsProjectCeiling`, `TestAddNamespace_AllowsWhenUnderCeiling`, `TestUpdatePolicy_RejectsCeilingBelowCurrentUsed`. All fail today.

**Acceptance**
- [ ] Per-namespace default and project ceiling are distinct fields.
- [ ] Adding a namespace that would exceed the ceiling is rejected.
- [ ] `used` is persisted and exposed.
- [ ] Upgrade is behavior-identical for existing projects.

### 6.2 Parity — features

### [alertmanager-drops-pagerduty-msteams] PagerDuty and MS Teams channels are dropped from every rendered Alertmanager config

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** parity-features · **Effort** S

**Evidence** — `internal/worker/tasks/notification_dispatch.go:50-67` declares five supported channel types (slack, pagerduty, msteams, webhook, email) and `internal/handler/alerting.go:281-284` validates `CreateChannel` against that list. But both Alertmanager renderers map only three: `internal/handler/alerting.go:1344-1360` (`case "slack"`, `case "webhook"`, `case "email"`, `default: continue`) and `internal/handler/monitoring.go:2349-2360` (`case "slack","webhook"`, `case "email"`, `default: continue`). Routes are appended inside the same loop after `receivers = append(...)` (`alerting.go:1361-1368`), so a pagerduty/msteams channel produces neither a receiver nor a route. Alertmanager natively supports `pagerduty_configs` and `msteams_configs`.

**Why it matters** — **[corrected — the original "operator gets zero pages" claim is wrong and must not be repeated]** Astronomer's own evaluator is the primary delivery path: `internal/worker/tasks/alert_evaluation.go:157-190` `dispatchAlertNotifications` enqueues `notification:send` per bound enabled channel, and `notification_dispatch.go` natively formats pagerduty (Events API v2) and msteams. **PagerDuty/Teams channels do page.** The real defect is narrower: the receiver set rendered for the shared Alertmanager (and the ConfigMap copy) silently omits two of five supported types, so anything routed through Alertmanager — e.g. Thanos-Ruler-fired alerts — lands on the null receiver for those channels. Hence medium.

**Remediation**
1. Extract the receiver-rendering switch into one shared function (see `duplicate-alertmanager-renderer`), e.g. `internal/notify/alertmanager.go` `func RenderReceiver(channelType string, cfg map[string]any) (map[string]any, bool)`.
2. Add `case "pagerduty"`: `receiver["pagerduty_configs"] = []map[string]any{{"routing_key": firstConfigString(cfg, "routing_key","integration_key","key"), "send_resolved": true}}`.
3. Add `case "msteams"`: `receiver["msteams_configs"] = []map[string]any{{"webhook_url": firstConfigString(cfg, "url","webhook_url"), "send_resolved": true}}`.
4. Change `default:` to log a warning and return a per-channel `unsupported` marker that `CreateChannel`/`UpdateChannel` surface in the API response.
5. Render `slack` as `slack_configs` with `api_url` when the config carries a Slack API URL rather than collapsing it to a generic webhook.

**Tests** — `internal/handler/alerting_test.go::TestRenderAlertmanagerConfigIncludesPagerDutyAndMSTeams` — build channels of every type in `SupportedNotificationChannels`, render, unmarshal, assert `len(receivers) == len(channels)+1` and that receivers with `pagerduty_configs` and `msteams_configs` exist with matching route matchers. Mirror in `internal/handler/monitoring_test.go` for `renderSharedAlertmanagerConfig`. Add a guard test asserting the renderer covers every entry of `tasks.SupportedNotificationChannels`.

**Acceptance**
- [ ] All five channel types render a receiver and a route.
- [ ] Adding a new channel type without wiring the renderer fails the guard test.

### [alertmanager-email-missing-smtp-globals] Rendered Alertmanager config emits `email_configs` with no SMTP globals, which Alertmanager rejects at load

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** parity-features · **Effort** M

**Evidence** — `internal/handler/monitoring.go:2354-2357` renders `receiver["email_configs"] = []map[string]any{{"to": email, "send_resolved": true}}` and `:2370-2373` sets `"global": {"resolve_timeout": "5m"}` — no `smtp_smarthost`, `smtp_from`, `smtp_auth_username`, `smtp_auth_password`. Identical at `internal/handler/alerting.go:1354-1357` and `1371-1374`. Alertmanager's loader errors with `no global SMTP smarthost set` / `no global SMTP from set`, rejecting the whole document. This config is not advisory: `internal/handler/monitoring.go:2278-2296` unmarshals the rendered YAML and passes it as the `config` value of the prometheus-community `alertmanager` chart release (`applySharedAlertmanager`, `:2298-2311`). SMTP settings exist (`internal/handler/smtp.go`, `internal/db/sqlc/smtp.sql.go`) but `rg -n 'smtp|smarthost'` over both renderers returns nothing.

**Why it matters** — The moment any email channel is enabled, the shared Alertmanager install/upgrade ships an invalid config: the pod fails to load it and either refuses to start or keeps serving the previous config. Install/Preview returns 202 with a healthy-looking operation. **[corrected — impact is narrower than "takes down ALL alert delivery"]** Astronomer-native alert email goes through the SMTP path (`internal/worker/tasks/email_dispatch.go`; `notification_dispatch` explicitly no-ops for email), and slack/pagerduty/msteams/webhook are delivered by the in-process evaluator (`alert_evaluation.go:157-190`) independent of Alertmanager. **What breaks is the shared Alertmanager install/upgrade itself and any Alertmanager-routed delivery.**

**Remediation**
1. **Cheapest guard, do this regardless:** validate the rendered YAML through `github.com/prometheus/alertmanager/config.Load` before handing it to Helm, and fail Install/Upgrade with a 400 carrying the loader error instead of shipping a broken config.
2. Give `MonitoringHandler` the same `SettingsCache`/SMTP querier `AlertingHandler` uses; load the configured SMTP row (`internal/db/sqlc/smtp.sql.go`).
3. When any enabled channel is email, emit `global: {smtp_smarthost, smtp_from, smtp_auth_username, smtp_auth_password, smtp_require_tls}`.
4. If no SMTP config exists, skip email receivers entirely and report that in the Preview response, rather than emitting an unloadable document.

**Tests** — `internal/handler/monitoring_test.go::TestSharedAlertmanagerConfigLoadsWithEmailChannel` — seed one enabled email channel + SMTP settings, render, feed to `config.Load`, assert no error and `global.smtp_smarthost` populated. `::TestSharedAlertmanagerConfigRejectsEmailWithoutSMTP` — no SMTP row: the render omits the email receiver or Install returns 400. Both fail today.

**Acceptance**
- [ ] Every rendered config passes `config.Load` before reaching Helm.
- [ ] Email receivers carry SMTP globals or are omitted.
- [ ] Install/Upgrade fails loudly on an unloadable config.

### [alertmanager-routing-configmap-inert] Alerting mutations write an Alertmanager routing ConfigMap nothing consumes

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** parity-features · **Effort** M

**Evidence** — Every alerting mutation calls `syncSharedAlertingAssets` (`internal/handler/alerting.go:300, 382, 447, 515, 600, 629, 834, 880, 914, 939`), which applies three ConfigMaps (`:1202-1216`): `astronomer-ruler-rules`, `astronomer-alertmanager-routing`, `astronomer-alert-silences`. Only the first is wired to a workload — `internal/handler/monitoring.go:2060-2067` sets Thanos Ruler's `rule.rules = {create: false, name: "astronomer-ruler-rules"}`. Repo-wide grep for `alertmanager-routing` returns exactly one hit (the write at `alerting.go:1207`); nothing reads `astronomer-alert-silences` either. The persisted hashes (`:1224-1233`) are never read back. The Alertmanager release consumes an inline `config` value computed only during Preview/Install/Upgrade/Replace (`monitoring.go:2274-2296`), and the @every 2m monitoring reconcile (`internal/worker/tasks/monitoring_reconcile.go:90-143`) only health-checks the backend.

**Why it matters** — **[corrected]** Channel changes DO take effect for astronomer-rule notifications (the evaluator delivers directly). What is stale is the Alertmanager-side routing — relevant for Thanos-Ruler-fired copies of the same rules — plus two orphan ConfigMaps that imply propagation and silencing that do not exist (silences are honored only inside astronomer's evaluator, `internal/worker/tasks/alert_evaluation.go:352-390`). An operator reasonably concludes the assets are live because they are written into the cluster.

**Remediation**
1. Stop passing `config` inline. Install the alertmanager chart with `configFromSecret`/`existingSecret` pointing at an astronomer-managed Secret named `astronomer-alertmanager-config` with key `alertmanager.yml`, set in `sharedAlertmanagerPayload`.
2. Have `syncSharedAlertingAssets` write that Secret, so every mutation propagates live via the chart's `configmapReload` sidecar (already enabled, `monitoring.go:2296-2298`).
3. Delete the dead `astronomer-alertmanager-routing` ConfigMap write.
4. Either delete the `astronomer-alert-silences` write or replace it with real Alertmanager silence API calls through the tunnel on `CreateSilence`/`ExpireSilence`/`DeleteSilence`.
5. Define the Secret name as a single shared const referenced from both the writer and the Helm values.

**Tests** — `internal/handler/alerting_test.go::TestSyncSharedAlertingAssetsWritesConsumedAlertmanagerSecret` — fake requester records applied objects; assert the config lands at the exact name/key referenced in the helm values. `internal/handler/monitoring_test.go` asserts `sharedAlertmanagerPayload` references that same const. `TestNoOrphanAlertingAssets` enumerates applied object names and requires each to appear in the monitoring stack values.

**Acceptance**
- [ ] A channel or rule change propagates to the running Alertmanager without a manual stack upgrade.
- [ ] No applied asset is orphaned.

### [no-release-history-or-revision-rollback-ui] No Helm release history / revision picker / values view; the only rollback control blindly targets revision-1

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** parity-features · **Effort** M

**Evidence** — Backend exposes real Helm history: `internal/handler/catalog.go:1580-1613` `ListInstalledChartRevisions` calls `h.helm.History(...)` returning `[]protocol.HelmRevision`, routed at `internal/server/routes_tools_controlplane.go:222`; `GetInstalledChartValues` at `:1619-1640`, routed `:221`. `RollbackInstalledChart` accepts an explicit `{"revision": N}` (`:1349-1358`). No UI caller: grep for `revisions` in `frontend/src` hits only the ArgoCD history helper (`lib/api.ts:2172-2178`); no `getInstalledChartValues`; no history UI on the cluster Apps page. The only rollback affordance is `frontend/src/routes/dashboard/catalog/index.tsx:193-207` — an icon button firing `rollback.mutate({id: row.id, revision: row.revision - 1})` with **no confirm dialog** (contrast the adjacent uninstall at `:208-215`, which does confirm) and no version context; the comment at `:195` admits "UX-06: hide Upgrade until an upgrade modal / version picker is wired". `row.revision` is astronomer's DB-tracked revision, not guaranteed to equal Helm's.

**Why it matters** — Rancher's installed-app detail is built around the release history table with per-row "Rollback to this revision" and a values view. Operators cannot see what changed, cannot roll back more than one step, cannot inspect effective values, and can trigger a destructive rollback with one unconfirmed click on a possibly-stale revision number. **[corrected]** The button is disabled when `row.revision <= 1`, so the blind case is only N → N-1, not revision 0.

**Remediation**
1. Add `getInstalledChartRevisions` / `getInstalledChartValues` to `frontend/src/lib/api/` and `useInstalledChartRevisions` to `frontend/src/lib/hooks.ts`.
2. Add a release detail overlay reachable from `frontend/src/routes/dashboard/catalog/index.tsx` (Installed tab row click) and `frontend/src/routes/dashboard/clusters/$id/apps/index.tsx`, with tabs History / Values / Notes.
3. Replace the bare rollback button with a row action that opens the History tab and calls `rollbackChart(id, revision)` for the selected row behind `ConfirmDialog`.
4. Have `ListInstalledChartRevisions` mark the row matching `installed.Revision` so the UI can label "current", and prefer the Helm-reported revision over the DB column when computing the rollback target in `internal/handler/catalog.go:1349-1352`.

**Tests** — `frontend/src/routes/dashboard/catalog/release-history.test.tsx` — mock three revisions; assert all three render, clicking Rollback on revision 1 opens a confirm and then POSTs `{revision: 1}`, and no request is issued when the confirm is dismissed. Go: `internal/handler/catalog_test.go::TestRollbackUsesHelmReportedRevisionWhenAvailable`.

**Acceptance**
- [ ] History and values are visible in the UI.
- [ ] Rollback targets an explicitly selected revision behind a confirm.
- [ ] The Helm-reported revision wins over the DB column.

### [cis-ingest-in-process-goroutine-loses-scans] CIS report ingestion is an in-process goroutine; the durable task exists but is never enqueued

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** parity-features · **Effort** S

**Evidence** — `internal/handler/security.go:686-695`: after creating the ClusterScan CR and the DB row, `go h.pollScanReport(scan.ID, req.ClusterID, scanName)` with the comment "We don't use asynq here even though the worker package registers a task type for it". `pollScanReport` (`:709-757`) holds all state in memory: 60 attempts × 30s against `context.Background()`. No reconciler for orphaned rows: `internal/worker/scheduler.go:43-80` registers no scan sweep. Meanwhile `internal/worker/tasks/security_scan.go:21-52` implements exactly the durable design ("a re-enqueue pattern instead of a single long-running task so a worker restart doesn't lose progress") with `SecurityScanIngestPayload.AttemptCount`. The enqueuer is wired but dead: `internal/server/routes_security.go:127-131` calls `deps.Security.SetIngestQueue(asynq.NewClient(...))` and the field is assigned at `security.go:145` and never read.

**Why it matters** — Any server restart, rolling upgrade, or eviction during the 30-minute window abandons every in-flight CIS scan. The row stays `status=running` with no error and no terminal transition, so the Security page shows a permanently spinning scan and the operator cannot distinguish hung from slow. Rancher's cis-operator scans are CR-backed and converge after restart. The dead code creates a false impression the durable path is live.

**Remediation** — **[corrected — option (a) as originally written will NOT work; two extra wiring steps are required]**

Option (b) — **recommended, cheapest**: add a `security:reconcile_running_scans` periodic task registered in `internal/worker/scheduler.go` that lists security scan rows with `status='running'` and `updated_at` older than 35 minutes and calls `UpdateSecurityScanFailedWithMessage`, plus a server-start pass that restarts `pollScanReport` for rows still running.

Option (a) — durable path, if taken, requires all three:
1. Replace `go h.pollScanReport(...)` in `CreateScan` with `h.ingestQueue.Enqueue(tasks.NewSecurityIngestTask(...), asynq.Queue("tunnel"), asynq.ProcessIn(ingestPollInterval))`.
2. **Call `tasks.ConfigureSecurityIngest` (`internal/worker/tasks/security_scan.go:92`) during server startup with the hub-backed K8s requester** — it is never called anywhere in the tree today, so `securityIngestDeps.Queries/K8s` are nil and `HandleSecurityIngest` no-ops by design at `:189-196`.
3. **Register `TypeSecurityIngest` on the server's tunnel worker.** It is registered only in `Worker.RegisterHandlers` (`internal/worker/worker.go:248`), which runs on the standalone worker that has no tunnel hub; it is absent from `RegisterTunnelHandlers` (`:221-236`), the only mux drained from the `tunnel` queue.

Either way, delete whichever of `SetIngestQueue` / `pollScanReport` becomes dead.

**Tests** — `internal/worker/tasks/security_scan_test.go::TestRunningScansAreReapedAfterTimeout` — a 40-minute-old running row transitions to failed. If option (a): `internal/handler/security_cis_test.go::TestCreateScanEnqueuesDurableIngest` (fails today: zero enqueues) plus a wiring test asserting `ConfigureSecurityIngest` was called and the type is registered on the tunnel mux.

**Acceptance**
- [ ] No scan row can remain `running` indefinitely after a restart.
- [ ] No dead enqueuer or dead poller is left in the tree.

### [no-scheduled-cis-scans] CIS scans are one-shot only — no recurring schedule, retention, or regression alert

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** parity-features · **Effort** L

**Evidence** — The only start path is `POST /security/scans/` (`internal/server/routes_security.go:44`) → `internal/handler/security.go:617`, building a single `astronomer-cis-<unix>` ClusterScan CR (`:663`, `createClusterScanCR` `:991`). The request struct accepts only `cluster_id` + `profile`/`scan_type` (`:617-625`). No cron entry in `internal/worker/scheduler.go:43-80`; no schedule table (`rg -n 'scan_schedule' internal/db/migrations` finds nothing). The wizard is one-shot (`frontend/src/routes/dashboard/security/scans/new/index.tsx:42-80`). Rancher's ClusterScan carries `spec.scheduledScanConfig{cronSchedule, retentionCount}` exposed on its scan-create screen.

**Why it matters** — Compliance programs require continuous benchmarking evidence. Without scheduling, CIS posture is only as fresh as the last human click, drift between scans is invisible, and scan rows accumulate unbounded. **[corrected]** This is a missing feature, not a malfunction — nothing breaks today.

**Remediation**
1. Add a `cis_scan_schedules` table (cluster_id, profile, cron_schedule, retention_count, enabled, last_run_at) with sqlc queries.
2. **Prerequisite step:** extract the body of `CreateScan` into `func (h *SecurityHandler) startScan(ctx, clusterID uuid.UUID, profile string) (sqlc.SecurityScanResult, error)` reusable by handler and task.
3. CRUD handlers alongside the scan endpoints, routed under `/security/scan-schedules/` gated by the existing `secCreate`/`secUpdate`/`secDelete` middleware (`internal/server/routes_security.go:28-30`).
4. Register `security:run_scheduled_scans` in `internal/worker/scheduler.go` (@every 5m, tunnel queue) finding due schedules, invoking `startScan`, and pruning per `retention_count`.
5. Surface as a "Recurring" step in `frontend/src/routes/dashboard/security/scans/new/index.tsx` and a Schedules tab on `frontend/src/routes/dashboard/security/index.tsx`.
6. Optional: emit an alert event when a scheduled scan's fail count rises versus the previous run (the diff machinery exists for image vulns, `internal/handler/image_vulns` `ClusterDiff`).

**Tests** — `internal/worker/tasks/security_scan_schedule_test.go::TestScheduledScansFireWhenDue` (cron `0 * * * *`, `last_run_at` 2h ago → one scan created, `last_run_at` advanced; immediate second run creates none), `::TestScheduleRetentionPrunesOldScans` (retention_count=3 with 5 completed scans → 2 oldest deleted).

**Acceptance**
- [ ] Schedules can be created, listed, and disabled from the UI.
- [ ] Due schedules fire exactly once per interval.
- [ ] Retention prunes old scans.

### 6.3 Agent

### [agent-no-read-idle-deadline] No read/write deadline or liveness watchdog — a half-open tunnel leaves the agent "connected" and Ready

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** agent · **Effort** M

**Evidence** — `internal/agent/tunnel.go:455-465` `readLoop` calls `tc.readMessage(ctx, tc.conn)` with the deadline-free loop context; `:683-693` `readMessage` calls `conn.Read(ctx)` with no timeout. Writes likewise: `:515-529` `writeLoop` → `:696-702` `writeMessage` → `conn.Write(ctx, ...)`. The agent answers `MsgHeartbeat` with a PONG (`:468-477`) but never requires one and never times out waiting; there is no `lastActivity` tracking and no watchdog (`cmd/agent/main.go` wires only `SetConnectionListener` at `:301`). Only the SERVER has liveness: `internal/tunnel/server.go:732-753` pings every 20s with a 10s write timeout. `/readyz` is a single boolean off `tc.connected` (`health.go:536-542`) and `/healthz` is a static 200 (`:532-534`), so the liveness probe cannot restart a wedged agent. Compare `rancher/remotedialer@v0.6.0/types.go:6-7` (`PingWaitDuration = 60s`, `PingWriteInterval = 5s`) and `wsconn.go:80-97`, which sets a 60s read deadline refreshed by every ping/pong on BOTH ends.

**Why it matters** — **[corrected — the original "hours" claim is wrong]** Both dial paths get TCP keepalives by default: `websocket.Dial` with no HTTPClient uses `http.DefaultTransport` (KeepAlive 30s), and the CA-pinned path at `tunnel.go:167-171` builds an `&http.Transport{}` with no `DialContext`, using `net.Dialer`'s zero-value default (~15s). On Linux defaults a black-holed path surfaces a read error in roughly **2.5–5 minutes**, not hours. The honest defect: agent-side detection is 5-10× slower than the server's own reaping (20s ping / 10s write timeout), is not configurable, and leaves `/readyz` reporting 200 for that whole window — the cluster shows Disconnected in the UI with a healthy-looking pod. A blocked `conn.Write` can also wedge `writeLoop` until `sendCh` saturation trips `failClose`.

**Remediation**
1. Add `lastActivity atomic.Int64` updated in `readMessage` on every successful read.
2. Wrap each `conn.Read` in `context.WithTimeout(ctx, readIdleTimeout)` — default 60s (≥ 2× the server's 20s ping interval), configurable via `ASTRONOMER_TUNNEL_READ_IDLE_SECONDS` — so a silent peer returns a deadline error and drops into `reconnectLoop`.
3. Wrap `conn.Write` in `context.WithTimeout(ctx, writeTimeout)` (10s, matching `internal/tunnel/server.go:60`).
4. Optionally originate a WS ping on a 20s ticker so the path is exercised both ways.
5. Ensure the deadline error path calls `setConnected(false)` before re-dialing so `/readyz` flips.

**Tests** — `internal/agent/tunnel_test.go::TestReadLoopExitsOnIdleTunnel` — httptest WS server accepts CONNECT/ACK then goes silent (never reads, writes, or closes); assert `IsConnected()` → false and `reconnectLoop` entered within ~2× the configured idle timeout. `::TestWriteBlocksAreBounded` — a server that stops reading causes a write error rather than a permanently blocked `writeLoop`.

**Acceptance**
- [ ] A silent peer is detected within the configured idle timeout.
- [ ] `/readyz` flips within that window.
- [ ] A blocked write cannot wedge `writeLoop`.

### [split-brain-across-server-replicas] Two agents claiming one cluster are only fenced within a single server pod; AgentID is always empty

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** agent · **Effort** L

**Evidence** — `internal/tunnel/server.go:594-605`: supersession is purely in-process — `h.agents.Set` returns the previous `*AgentConnection` and cancels it; that map is per-pod (`internal/tunnel/sharded_agents.go`). Nothing consults the redis locator before registering: `:631-634` unconditionally calls `loc.Set(ctx, clusterID)`, and `internal/tunnel/locator.go:114-139` cancels its own refresh loop and does an unconditional SET (`:240-247`, comment: "newest-owner-wins"). `persistConnect` (`:981-998`) marks prior DB rows disconnected, which has no effect on a live socket owned by a sibling pod. `validateAndMaybeRotateToken` (`:1051-1126`) does not fence — two agents presenting the same durable token both validate. Supersede coverage is same-pod only (`internal/tunnel/server_supersede_locator_test.go:17-48`, `:57-106`). AgentID is always empty in production: `internal/agent/config.go:109` defaults `agent_id` to `""` and `deploy/agent/install.yaml.template` (env block `:594-650`) never sets `ASTRONOMER_AGENT_ID`, so `payload.AgentID` recorded at `server.go:582, 616, 1001` is `""`.

**Why it matters** — Two live tunnels for one cluster: heartbeats interleave and overwrite each other's health rows, decommission RPC goes to one agent while the other keeps running, and neither is told to stop. Because AgentID is empty, neither the audit trail (`server.go:616`), `agent_connections`, nor an operator can distinguish the two claimants. **[corrected on two points]** (1) Not reachable in the shipped topology: the agent Deployment is `replicas: 1` with `strategy: Recreate` (`install.yaml.template:555, 562-563`) precisely so two agents never coexist — this needs an operator to scale the Deployment or apply one join manifest against a second cluster. (2) Routing does not continuously flip-flop: `locator.go:295-321` `refreshTick` is owner-checked and the loser STOPS refreshing, so routing settles on the newest claimant; "the key flips to whichever pod wrote last" is true only at claim time.

**Remediation** — Items 1 and 3 are cheap and worth doing on their own. **Item 2 (redis lease + takeover pub/sub) is a much larger change than the residual risk justifies; propose it separately.**
1. **Populate AgentID.** Add `ASTRONOMER_AGENT_ID` to `deploy/agent/install.yaml.template` sourced from `fieldRef: metadata.uid` (or namespace/podname), and fall back in `internal/agent/config.go` to `os.Hostname()+"-"+startup nanos` when unset.
3. **Make duplication loud.** Emit an `agent.split_brain` audit event plus a Prometheus counter when the incoming AgentID differs from the incumbent's, so a duplicated join command is not silent.
4. On supersede, close the loser with a distinguishable WS status/reason so it backs off longer instead of immediately re-dialing.
2. *(deferred)* Fenced lease: redis `SET NX` on `tunnel:owner:<cluster_id>` carrying `{pod_addr, session_id, agent_id}` with the locator TTL; on a different holder, publish a takeover request the owning pod consumes by calling `disconnectImpl(clusterID)`; register only after release or expiry. Reuse the generation-counter pattern in `internal/tunnel/locator.go:65-72`.

**Tests** — `internal/tunnel/server_test.go::TestDistinctAgentIDsAreRecorded`; `deploy/agent/template_test.go::TestManifestRendersAgentIDEnv`. If item 2 is approved: `internal/tunnel/server_supersede_ha_test.go::TestCrossPodSupersedeEvictsIncumbent` — two Hubs sharing one miniredis-backed Locator; the same cluster connecting to hub B cancels and removes hub A's connection and the locator points at B.

**Acceptance**
- [ ] Every agent process reports a stable unique instance id.
- [ ] A second claimant produces an audit event and a metric.
- [ ] The losing connection is closed with a distinguishable reason.

### [informer-full-object-caches] Typed informers cache full Pod/ConfigMap/Secret objects although only metadata is emitted

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** agent · **Effort** M

**Evidence** — `internal/agent/state_subscriber.go:372` builds the typed factory with plain `informers.NewSharedInformerFactory` (no `informers.WithTransform` anywhere in the package), and `:465-487` attaches full typed Pods/Services/Nodes/ConfigMaps/Secrets (when the profile allows, `:472-473`)/Deployments/ReplicaSets/StatefulSets/DaemonSets, plus Events (`:490-531`). Each cache holds complete objects including `Secret.Data`, `ConfigMap.Data`, and `metadata.managedFields`. The emitted payload uses only Op/Kind/APIGroup/APIVersion/Namespace/Name/ResourceVersion/CoalesceKey (`:741-750`) — the header comment at `:462-464` ("ConfigMaps and Secrets fan out their metadata only — never data") is true for the wire but not for memory. The P4.6 expansion set correctly uses metadata-only informers (`:391-397`), so the contrast is in-tree. Container limit 512Mi with no `GOMEMLIMIT`.

**Why it matters** — **[corrected — downgraded on both legs]** *Security:* the Secret informer is gated on `s.watchSecrets` from `ProfileAllowsSecrets` — admin/operator only (`health.go:461-473`); on those profiles the management plane can already read any secret through the k8s proxy on demand, so caching raises the value of a heap dump or container escape but grants no new capability, and viewer/namespace-* profiles never build the informer. *Footprint:* the OOM claim is plausible but unquantified — typed Pod objects, not ConfigMap data, dominate on most clusters. Still worth fixing: it is zero-benefit resident memory and zero-benefit secret plaintext, and it compounds the OOM-restart → replay-storm loop in `send-failclose-livelock`.

**Remediation**
1. Build the typed factory with `informers.NewSharedInformerFactoryWithOptions(client, resync, informers.WithTransform(stripHeavyFields))`, where `stripHeavyFields` nils `ObjectMeta.ManagedFields`, the `kubectl.kubernetes.io/last-applied-configuration` annotation, `Secret.Data`/`StringData`, `ConfigMap.Data`/`BinaryData`, and Pod spec fields the emit path never reads.
2. Better: move Pod/Service/ConfigMap/Secret/Node onto the metadata informer factory already used at `:391-397`. **Verify nothing downstream reads beyond metadata first** — `mirror_subscriber.go` uses its own factory for IngressClass/NetworkPolicy/ResourceQuota/LimitRange only, so it is unaffected.
3. Set `GOMEMLIMIT` (e.g. 400MiB) in `deploy/agent/install.yaml.template` so the Go heap backs off before the kubelet OOM-kills, and parametrize the memory limit for large clusters.

**Tests** — `internal/agent/state_subscriber_test.go::TestInformerCacheDoesNotRetainSecretData` — seed a fake clientset with a Secret containing known bytes, run with `watchSecrets` enabled, assert the recorded store's cached object has empty `Data` (and empty `ManagedFields` for a Pod). Fails today.

**Acceptance**
- [ ] No Secret or ConfigMap value is resident in agent memory.
- [ ] `GOMEMLIMIT` is set and below the container limit.
- [ ] Emitted state payloads are unchanged.

### [exec-and-log-binary-corruption] Exec and log payloads round-trip raw bytes through a JSON string

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** agent · **Effort** S

**Evidence** — `internal/agent/exec.go:224-237` `tunnelWriter.Write` does `json.Marshal(map[string]string{"stream": w.stream, "data": string(p)})` on raw SPDY exec-stream bytes and returns `len(p)` unconditionally. Go's `encoding/json` coerces invalid UTF-8 in a string to U+FFFD, so the wire bytes are not the pod's bytes. Same on the stdin path (`:159-178`) and in `internal/agent/logs.go:107-111` (`json.Marshal(map[string]string{"line": line})`). The k8s proxy path base64-encodes correctly (`internal/agent/k8sproxy.go:254, 337`), so the fix pattern already exists in-tree.

**Why it matters** — Any exec session moving binary data is silently corrupted: `kubectl exec -- tar cf - /data`, `cat` of a binary, database dumps piped out of a pod, TTY sessions with non-UTF-8 locales or raw escapes. Corruption is silent and `Write` claims full success, so a user restoring a backup taken through the console gets a broken archive with no indication. **[additional]** For logs, `bufio.Scanner` also strips the line terminator and drops any line over the 1 MiB buffer (`logs.go:103-105`, `scanner.Err` only logged), so log fidelity is already lossy independent of UTF-8 — the b64 change alone will not make `kubectl logs` byte-exact.

**Remediation**
1. `internal/agent/exec.go`: emit `{"stream":..., "data_b64": base64.StdEncoding.EncodeToString(p)}`; accept both fields on the input path for one release.
2. `internal/agent/logs.go`: emit `line_b64` likewise.
3. Update `internal/tunnel/exec_consumer.go` and `internal/tunnel/logs_consumer.go` to prefer the b64 field and fall back to the legacy string field, so an old agent paired with a new server still works. **Both consumers must ship in the same release as the agent change.**
4. Bump the agent compatibility floor once the fallback is retired.
5. Separately, address the scanner truncation in `logs.go:103-105` (larger buffer or a byte-oriented reader) if byte-exact logs are a goal.

**Tests** — `internal/agent/exec_test.go::TestExecOutputPreservesInvalidUTF8` — write `[]byte{0xff, 0xfe, 0x00, 0x41}` through `tunnelWriter`, assert decoded bytes are byte-identical (today `0xff/0xfe` become `0xEF 0xBF 0xBD`). Matching `logs.go` test and a round-trip test through `internal/tunnel/exec_consumer_test.go`.

**Acceptance**
- [ ] Binary exec output round-trips byte-exact.
- [ ] An old agent still works against the new server for one release.

### [no-decommission-signal-to-offline-agent] A decommissioned agent that was offline never learns it was decommissioned

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** agent · **Effort** M

**Evidence** — `Hub.SendDecommission` (`internal/tunnel/server.go:890-919`) returns `connected=false` when no AgentConnection is registered locally and the locator does not point at a sibling; `phaseCleanupManagedSide` (`internal/worker/tasks/cluster_decommission.go:694-730`) then records `detail["skipped"]="agent not connected"` and returns nil, after which the reconciler proceeds to `phaseRevokeAgentToken` and the tombstone phase. There is no pending/deferred decommission mechanism (`deferred_operations`/`DispatchDeferred` is the maintenance-window replayer; `cluster_decommission_outbox` is only the transactional asynq enqueue). Agent side has no terminal state: `reconnectLoop` (`internal/agent/tunnel.go:403-428`) retries forever (capped at `max_reconnect`, default 300s) and `Connect` (`:125-140`) routes even a first-connect failure into it. A server-side rejection cannot carry a teardown instruction: `internal/tunnel/server.go:506-522` closes with `StatusPolicyViolation` **before** any CONNECT_ACK is written, and `ConnectAckPayload{Accepted:false}` is never constructed anywhere in `internal/tunnel`, so the branch at `tunnel.go:225-228` is dead for revocation.

**Why it matters** — Decommission is only complete for clusters that happen to be connected. An offline cluster keeps running the agent Deployment, the `astronomer-*` namespaces, cluster-scoped RBAC, and baseline tooling forever — an orphaned, permanently-failing-to-authenticate workload the operator must find by hand, producing indefinite background auth-failure traffic from clusters nobody tracks. *(Matches the previously documented decommission cleanup gap.)*

**Remediation** — Items 1+2 are self-contained and deliver most of the value.
1. In `internal/tunnel/server.go`, when `validateAndMaybeRotateToken` fails specifically because the cluster/token was revoked-by-decommission, write a `ConnectAckPayload{Accepted:false, Reason:"decommissioned"}` (or use a dedicated WS close code) before closing, instead of the bare policy-violation close.
2. In `internal/agent/tunnel.go:225-228`, treat that reason as terminal: stop the reconnect loop, invoke the existing `DecommissionHandler` with a full-footprint payload derived from local defaults, and exit non-zero once teardown finishes.
3. **[corrected — "revoke-after-ack" cannot be layered onto the current flow]** The reconciler tombstones the cluster row and deletes its tokens in later phases, so a reconnecting agent days later would have no cluster row to authenticate against. The workable shape is a **standalone pending-teardown record keyed by cluster_id that survives the tombstone**, plus a short-lived teardown-only credential.
4. Surface still-orphaned clusters in the fleet UI from the existing orphan-audit data.

**Tests** — `internal/agent/tunnel_test.go::TestDecommissionedRejectionStopsReconnectAndTearsDown` (fake server replies `Accepted=false`/`Reason=decommissioned`; assert `reconnectLoop` exits and the decommission handler ran); `internal/tunnel/server_test.go::TestRevokedTokenConnectSendsDecommissionAck`.

**Acceptance**
- [ ] An agent whose credential was revoked by decommission tears itself down and stops re-dialing.
- [ ] Orphaned clusters are visible in the fleet UI.

### 6.4 Transport security

### [namespace-filtered-watch-local-path-filters-raw-chunks] Namespace-scoped watch filtering on the same-pod path filters raw 16 KiB chunks, not whole NDJSON events

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** transport-security / code-health · **Effort** M

**Evidence** — `internal/tunnel/proxy.go:333-347` `consumeStreamingResponse`: each `K8sStreamFrameData` frame is base64-decoded and passed **whole** to `watchEventAllowed(bodyBytes, allowed)`; on `ferr != nil || !keep` the frame is dropped (`:344-346`). The producer is not event-aligned: `internal/agent/k8sproxy.go:243-258` reads into a fixed 16 KiB buffer (`k8sStreamChunkSize`, `:163`) and emits whatever a single `resp.Body.Read` returned, so one frame can hold several concatenated NDJSON events or a truncated one. `watchEventAllowed` (`internal/tunnel/nsfilter.go:175-180`) is a plain `json.Unmarshal`, which errors on both shapes. The cross-pod path does it correctly line-by-line with `bufio.Scanner` (`proxy.go:663-681`). Gated on `namespace_scoped_rbac_enabled` (`internal/config/config.go:293-296`, default false) plus a namespace-restricted caller.

**Why it matters** — **[corrected — the failure is intermittent, not total]** A Read returning exactly one event plus trailing newline unmarshals fine (`json.Unmarshal` tolerates trailing whitespace), which is the common steady-state case. Frames are silently discarded whenever several events coalesce into one Read (the initial ADDED burst, any busy namespace) or a single event exceeds 16 KiB (large CRDs/pods). So the symptom is a watch that **drops arbitrary batches of events and leaves the UI stale and inconsistent**, not one that is eternally silent — path-dependent (the identical request against a sibling replica works) and therefore very hard to debug in the field. Fail-closed, so no data leak. **This blocks the flag default flip in `project-bindings-inert-by-default`.**

**Remediation**
1. In `consumeStreamingResponse`, maintain a per-stream carry-over buffer: append each decoded data frame, split on `'\n'`, evaluate each **complete** line with `watchEventAllowed`, write the allowed lines (with the newline), keep the trailing partial for the next frame.
2. Factor the shared logic out of `forwardFilteredOwnerWatch` (`proxy.go:642-683`) into a `watchLineFilter` type used by both paths so they cannot drift again.
3. Guard the buffer with the same 4 MiB cap the scanner uses (`:665`); drop and close the stream if a single line exceeds it.

**Tests** — `internal/tunnel/proxy_test.go::TestConsumeStreamingResponseFiltersAcrossChunkBoundaries` — drive with frames that (a) contain two complete events (one allowed, one denied) and (b) split a single allowed event across two frames; assert the client receives exactly the allowed events, in order, once. `::TestLocalAndCrossPodWatchFilterAgree` feeding the same byte sequence through both paths.

**Acceptance**
- [ ] Coalesced and split frames both filter correctly.
- [ ] Local and cross-pod paths produce identical output for identical input.
- [ ] `project-bindings-inert-by-default` step 3 is unblocked.

### [tunnel-rate-limits-keyed-on-spoofable-ip-and-run-before-auth] Rate limits never key on the user and the IP key is attacker-controlled

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** transport-security · **Effort** M

**Evidence** — `internal/server/routes.go:1056-1064` orders `rateLimit(ClassK8sProxy)` FIRST, then `requireAuth`; exec (`:1098-1099`), logs (`:1102-1103`) and the kubectl shell (`:1118-1120`) carry `rateLimit` with **no `requireAuth` in the chain at all** (they authenticate inside the handler). `internal/server/middleware/api_rate_limit.go:349-370` `apiRateLimitKey` prefers `GetAuthenticatedUser(r.Context())` — unpopulated at that point — and always falls through to `"ip:"+host(r.RemoteAddr)`. `chimiddleware.RealIP` is installed globally with no trusted-proxy allowlist (`routes.go:356`), so `RemoteAddr` is derived from client-supplied `X-Forwarded-For`/`X-Real-IP`. The tunnel CONNECT failure limiter has the same weakness via `ConnectClientIP` (`internal/tunnel/server.go:186-197`) → `middleware.RemoteIPAddr` (`internal/server/middleware/audit.go:205-238`), which takes the first XFF entry unconditionally and feeds `lim.Blocked/Fail/Reset` (`server.go:393-407, 512-528`) and the audited `Event.IPAddress`; `internal/tunnel2/server.go:86, 130` reuses it.

**Why it matters** — **[corrected — the availability half of the original claim is wrong and must not be repeated]** Because `RealIP` rewrites `RemoteAddr` from XFF, traffic behind an ingress keys on the forwarded client IP, not one ingress-pod IP, so one busy operator does **not** 429 the whole tenant. What survives: (a) these classes never key per-user despite the comment at `api_rate_limit.go:349-352`; (b) the key is fully attacker-controlled — rotating `X-Forwarded-For` gives a fresh bucket for every k8s-proxy call, every exec/logs session open, and every tunnel CONNECT auth failure, **defeating the A4 credential-probe throttle entirely** — and writes attacker-chosen source IPs into `agent.connected` / `agent.auth_failed` audit rows, poisoning the forensic trail that work exists to produce.

**Remediation** — Items 2 and 3 are load-bearing; reordering alone does not help exec/logs/shell, which have no `requireAuth` in the chain.
2. Replace the unconditional `chimiddleware.RealIP` at `routes.go:356` with a trusted-proxy-aware variant: add a `trusted_proxy_cidrs` config value and honour XFF/`X-Real-IP` only when `r.RemoteAddr` is inside it, else use the socket peer.
3. Apply the same trust policy inside `RemoteIPAddr` (`internal/server/middleware/audit.go:205-238`) so `ConnectClientIP` and audit rows use the vetted address; keep the raw XFF in an audit detail field for diagnostics.
1. Reorder `rateLimit(...)` after `requireAuth(...)` at `routes.go:1056-1064`; for exec/logs/shell add an explicit pre-limiter identity step or key those classes on `cluster_id` (already available via `apiRateLimitClusterID`, `api_rate_limit.go:373+`).

**Tests** — `internal/server/middleware/api_rate_limit_test.go::TestAPIRateLimitKeysOnUserWhenAuthenticated` (two requests from the same RemoteAddr, different users → independent buckets on the k8s-proxy route); `::TestRealIPIgnoresUntrustedXFF` (untrusted peer with `X-Forwarded-For: 1.2.3.4` keys on the peer address); `internal/tunnel/connect_limiter_test.go::TestConnectLimiterNotBypassedByForgedXFF` (N+1 failed CONNECTs from one socket peer, each with a different XFF, still trips the limiter).

**Acceptance**
- [ ] The CONNECT failure throttle cannot be bypassed by rotating XFF.
- [ ] Audit rows record a vetted source address.
- [ ] Authenticated k8s-proxy traffic keys per user.

### [kubectl-shell-ws-reopen-skips-rbac-when-scope-flag-off] Re-opening a kubectl-shell WebSocket re-checks only session ownership

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** transport-security / agent · **Effort** S

**Evidence** — `internal/handler/kubectl_shell.go:527-604` `HandleWS`: after `AuthorizeStreamRequestWithTickets` (`:536`) the only authorization is `loadSessionForCluster` (`:549` → `:879-914`), which checks that the row exists, `row.ClusterID` matches the URL, and `row.UserID == callerID` (or caller is superuser) — **no RBAC verb is evaluated**. The defence-in-depth re-derivation at `:574-583` runs only when `shellScopeEnabled` is true (`internal/handler/kubectl_shell_scope.go:328-339`), requiring `NamespaceScopedRBAC` or the `feature.shell_scope_to_caller` flag; both default false (`internal/config/config.go:293-296`, `:338`). The in-code comment at `:568-573` states the risk explicitly. The relay then runs with the agent's SA (`internal/tunnel/exec_consumer.go:209-347` → `internal/agent/exec.go`).

**Why it matters** — After an admin strips a user's cluster binding or demotes them, that user can still open a fresh WebSocket against their existing `active` session row and get a live shell. Rancher re-authorizes every proxied upgrade against current bindings. **[bounded]** The window is the remaining session lifetime (idle timeout 30 min, hard cap `kubectl_shell_session_hard_cap_hours` default 4, `config.go:298-299`), the session must still be `status=="active"` (`:550-553`), and the caller still needs a valid JWT/ticket — a stale-authorization window rather than indefinite access.

**Remediation**
1. Add an **unconditional** RBAC re-check immediately after `loadSessionForCluster` and **before the cross-pod forward at `:599-603`**: resolve `row.UserID`'s bindings via `h.Bindings.GetUserBindings` and require the same permission the Open path enforces — after the `exec-logs-verb-mismatch` fix, `(rbac.ResourcePods, rbac.VerbExec)` at `row.ClusterID`, namespace `row.Namespace` when set. 403 on failure; fail closed when the engine/querier are unwired in production.
2. Keep the flagged `deriveScopeForCaller` block as additional namespace narrowing.
3. Add a background sweep that marks sessions `revoked` when the owner's binding disappears (worker task alongside the existing idle-timeout reaper).

**Tests** — `internal/handler/kubectl_shell_test.go::TestHandleWSDeniesWhenCallerLostClusterBinding` — create an active session for user U with a cluster binding, remove the binding from the RBAC fake, open the WS, assert 403 (today the upgrade succeeds). Keep `TestHandleWSAllowsOwnerWithBinding` green.

**Acceptance**
- [ ] Losing a binding immediately prevents re-opening an existing shell session.
- [ ] The re-check runs before the cross-pod forward.

### 6.5 Crypto / TLS

### [cluster-ca-never-persisted] `clusters.ca_certificate` is never written, so registrations pin `insecure: true` and the kubeconfig download omits the CA

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** crypto-tls · **Effort** M

**Evidence** — `ca_certificate` is declared once (`internal/db/migrations/001_initial.up.sql:76`, `TEXT NOT NULL DEFAULT ''`) and appears in zero INSERT/UPDATE statements: `internal/db/queries/clusters.sql` `CreateCluster` (~`:57`) and `UpdateCluster` (~`:62`) omit it; a repo-wide search excluding generated sqlc returns only reads. Consumers always take the empty branch: `internal/server/self_manage_argocd.go:341` (`"insecure": cluster.CaCertificate == ""`) and `:344-345`; `internal/handler/argocd.go:2291-2292` in `upsertLocalManagedClusterRegistration`; `internal/worker/tasks/argocd_auto_register_cluster.go:625-626`. The drift check at `self_manage_argocd.go:397-399` compares `existingCfg.TLSClientConfig.Insecure != (cluster.CaCertificate == "")`, so a hand-hardened Secret is rewritten back to insecure. `internal/handler/clusters.go:2288-2290` emits an always-empty `certificate-authority-data` in the operator-facing kubeconfig download.

**Why it matters** — The ArgoCD cluster Secret written by the self-manage loop carries a freshly minted 24h `argocd-application-controller` ServiceAccount token (`argocd_auto_register_cluster.go:686-697`, `self_manage_argocd.go:335`) and ships it with `tlsClientConfig.insecure: true`. The downloadable kubeconfig is also plainly broken for any cluster whose API server cert is not in the system trust store. **[corrected]** Downgraded from high: token exposure requires an attacker already able to intercept traffic to the API server inside the management cluster's pod network (TLS is still negotiated; only verification is skipped). Two further corrections: `RegisterManagedCluster` **does** honour a per-call `ca_data` override (`argocd.go:3038-3041`, `caData = cluster.CaCertificate` only when the request field is empty), so a manual registration is secure for that call — it is the periodic `refreshLocalManagedClusterRegistration` → `upsertLocalManagedClusterRegistration` path (`argocd.go:2230-2292`) that ignores it and clobbers the Secret, and only when the token is near expiry or the server URL drifted. And the **non-local** cluster path never reads `ca_certificate` at all — it returns `TLSClientConfig{Insecure: true}` unconditionally (`argocd_auto_register_cluster.go:650`) — so populating the column fixes the local-cluster and manual-registration paths plus the kubeconfig download, **not** managed clusters.

**Remediation** — **[corrected — start with the smallest defensible fix, not the agent CONNECT-frame extension]**
1. Accept and persist `ca_certificate` on cluster create/update: add it to `internal/db/queries/clusters.sql` `CreateCluster`/`UpdateCluster` and to `PUT /api/v1/clusters/{id}/` (`internal/handler/clusters.go` `UpdateCluster`).
2. Stop discarding the `ca_data` supplied to `RegisterManagedCluster` (`internal/handler/argocd.go:3038`) — persist it so the reconciler does not clobber a manual registration.
3. Fix `buildDirectKubeconfig` (`internal/handler/clusters.go:2288-2290`) to omit `certificate-authority-data` entirely when the column is empty rather than emitting `""`.
4. *(Follow-up, larger)* Capture the CA at join: extend the agent CONNECT/metadata frame (`internal/agent/tunnel.go` CONNECT payload, handled in `internal/tunnel/server.go`) to report `/var/run/secrets/kubernetes.io/serviceaccount/ca.crt` and the resolved apiserver URL; add `UpdateClusterAPIServerTLS` and call it from the CONNECT handler.
5. Add `astronomer_argocd_cluster_registration_insecure_total` so residual insecure registrations are visible.

**Tests** — `internal/server/self_manage_argocd_test.go` — a cluster row with a non-empty `ca_certificate` makes `ensureLocalArgoClusterSecret` write `tlsClientConfig.insecure=false` and `caData=<pem>`, and `localArgoClusterSecretUpToDate` returns true (today the fixture cannot be constructed through the public write path). Regression test that `buildDirectKubeconfig` omits `certificate-authority-data` when empty. If step 4 lands: `internal/tunnel/server_connect_test.go` — a CONNECT frame carrying `ca_certificate` persists it on the cluster row.

**Acceptance**
- [ ] `ca_certificate` is writable through the API and survives reconciliation.
- [ ] A manual registration's `ca_data` is not clobbered.
- [ ] The kubeconfig download is valid for private-CA clusters.
- [ ] Remaining insecure registrations are counted in a metric.

### 6.6 ArgoCD

### [appproject-scoping-absent] No AppProject is ever created — everything runs in ArgoCD's permissive `default` project

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** argocd · **Effort** L

**Evidence** — Every Argo object hard-codes `project: default`: `internal/server/baseline_appsets.go:438`, `internal/server/self_manage_migration.go:170`, `internal/crd/controller.go:1100` (ClusterBaseline template), `:2510` and `:2528` (GitOpsTarget source and bundleRef materialization), `:2646/:2652` defaults. `rg AppProject deploy/chart/templates` returns only a comment in `serviceaccount.yaml:141`; the only AppProject artifact anywhere is the CRD inside `deploy/chart/charts/argo-cd-9.5.21.tgz`. Astronomer "projects" are only Secret labels (`internal/argolabels/labels.go`, `ProjectMembershipPrefix`). The validation layer admits fully-open projects: `internal/handler/argocd_project_validation.go:19-25` skips validation when `destination.Server == "*"`, and `internal/argosecurity/policy.go:1502-1506` returns nil for `sourceRepos: ["*"]`.

**Why it matters** — ArgoCD's `default` AppProject ships with `sourceRepos: ['*']`, `destinations: [{namespace:'*', server:'*'}]`, `clusterResourceWhitelist: [{group:'*',kind:'*'}]`. With everything in it, ArgoCD provides zero tenant confinement below the API, so `argocd-authz-anchored-to-management-cluster` has no backstop. **[corrected]** Downgraded to medium: this is an unused defence-in-depth layer, not an exploitable defect on its own — reaching the point where it matters already requires that coarse grant, and API-created Applications still pass `argosecurity.ValidateMutation` (canonical credential-free repo/server URLs, closed source model, no inline manifests).

**Remediation**
1. Add `deploy/chart/templates/argocd-appproject-platform.yaml` rendering an AppProject `astronomer-platform` with `sourceRepos` limited to the embedded helm repo (`internal/server/self_manage_argocd.go:48` `localArgoRepoURL`) plus operator-configured repos, `destinations` limited to the astronomer namespace on the local cluster, and a minimal `clusterResourceWhitelist`.
2. Add `astronomer-baseline` allowing the baseline chart repos with `destinations: [{server: '*', namespace: <component namespaces>}]`; switch `internal/server/baseline_appsets.go:438` to it.
3. Extend `internal/crd/controller.go` `ProjectReconciler` to create an AppProject per Project CR whose `destinations` are that project's clusters (labels exist via `argolabels.ProjectMembershipPrefix`); set `controller.go:1100/2510/2528` to that name.
4. **[corrected — narrow the claim]** `internal/server/self_manage_values.go:899` is inside `validateActiveSelfManagedUpgradeIdentity` — it is an identity assertion about astronomer's own self-managed Application, not a platform-wide rejection of scoped projects. Relax it to accept the platform project name.
5. **[corrected — do not apply as a flat rejection]** Tightening `internal/handler/argocd_project_validation.go` to reject `destinations[].server == "*"` conflicts with `ValidateMutationJSONForPath`'s deliberate `allowProjectDestinationWildcard` behavior (`policy.go:670-675`, `:845-848`); that context threading must be reworked first, or the tightening scoped to non-superusers at a different layer.

**Tests** — `deploy/chart_appproject_render_test.go` (helm template emits `AppProject/astronomer-platform` with non-wildcard sourceRepos+destinations); `internal/server/baseline_appsets_test.go` (generated ApplicationSet template `spec.project == "astronomer-baseline"`); `internal/crd/controller_test.go` (a ClusterBaseline in namespace X materializes `spec.template.spec.project` = the project's AppProject name).

**Acceptance**
- [ ] Platform and baseline Applications run in scoped AppProjects.
- [ ] Per-project AppProjects are materialized from Project CRs.
- [ ] Self-manage identity validation accepts the platform project.

### [decommission-wedges-baseline-applications] Decommission deletes the ArgoCD cluster Secret while generated Applications still carry `resources-finalizer`

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** argocd · **Effort** M

**Evidence** — `internal/server/baseline_appsets.go:424` stamps `"finalizers": ["resources-finalizer.argocd.argoproj.io"]` on every generated Application (rationale `:416-423`). `internal/worker/tasks/cluster_decommission.go:909` (`lookupClusterSecret`) and `:924` (`Secrets(argoCDNamespace).Delete(...)`) delete the upstream Secret, then `:951` drops the index rows (`DeleteArgoCDManagedClustersByCluster`). `rg 'Application|argoproj' internal/worker/tasks/cluster_decommission.go` returns nothing — no phase touches argoproj.io Applications. The only Application-aware surface is the read-only report `internal/handler/argocd_orphans.go:61` (`InstanceOrphanReport`, reason `stale_destination_cluster` `:19-22`).

**Why it matters** — Order of operations: the Secret disappears → the cluster generator stops emitting that row → the ApplicationSet controller deletes the generated Application → the finalizer requires the app controller to prune resources on a destination that is no longer a registered cluster → the prune can never succeed and the finalizer never clears. Applications sit in Terminating indefinitely, the controller retries forever, and one permanent zombie accumulates per decommissioned cluster per baseline component. The only fix is a manual `kubectl patch --type=merge -p '{"metadata":{"finalizers":null}}'` per app. **[corrected]** Downgraded to medium: this is management-plane debris; no tenant workload is harmed and no data is lost.

**Remediation** — Add a decommission phase that runs **before** the cluster-Secret delete in `internal/worker/tasks/cluster_decommission.go`.
1. List `argoproj.io/v1alpha1` Applications in `argoCDNamespace` with a dynamic client, matched on `spec.destination.server == row.ServerUrl` (the reliable key; label matching on `astronomer.io/baseline=platform` plus the cluster's `nameNormalized` suffix is a secondary filter).
2. **Preferred ordering:** delete with the finalizer intact **first**, while the Secret still exists, so a still-reachable cluster gets its resources pruned; strip `metadata.finalizers` via a merge patch only after a bounded timeout. This also mitigates the flip side — leaving baseline components running on the decommissioned cluster.
3. Record counts in the phase detail map alongside `argocd_cluster_secrets_removed`.
4. Only then delete the cluster Secret at `:924`. Add the dynamic client to `ClusterDecommissionDeps`.
5. Extend `internal/handler/argocd_orphans.go` with a POST remediation endpoint that force-clears finalizers on `stale_destination_cluster` orphans.

**Tests** — `internal/worker/tasks/cluster_decommission_test.go` — fake dynamic client with two Applications whose `spec.destination.server` matches and which carry the finalizer; assert the phase deletes both AND clears finalizers **before** the cluster Secret Delete call is observed (order-asserting fake), and that the detail reports `argocd_applications_removed: 2`. Fails today.

**Acceptance**
- [ ] Decommission leaves no Application in Terminating.
- [ ] Prune is attempted while credentials still exist.
- [ ] An orphan-remediation endpoint exists for pre-existing debris.

### [clusterbaseline-gitopstarget-target-management-cluster] ClusterBaseline / GitOpsTarget selectors do not exclude the local management cluster

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** argocd · **Effort** S

**Evidence** — `internal/crd/controller.go:1231-1234` `clusterBaselineClusterSelector` seeds `matchLabels` with only `{astronomer.io/managed-by: astronomer}` plus the operator's labels — no `astronomer.io/is-local: "false"`; `gitOpsTargetClusterSelector` (`:2732-2735`) is identical. Validation requires only managed-by (`:1516-1518`, `:2761-2763`). The platform baseline deliberately does exclude it (`internal/server/baseline_appsets.go:383-386`). The local ArgoCD cluster Secret carries `managed-by=astronomer` with `is-local` forced true (`internal/server/self_manage_argocd.go:427-429` → `argolabels.ManagedClusterLabels`, key `argolabels.IsLocalLabelKey`, `internal/argolabels/labels.go:24, :64`), so it matches. Generated Applications run in `default` (`controller.go:1100`) with prune/selfHeal from `ClusterBaselineSyncPolicy` (`internal/crd/types.go:577-581`).

**Why it matters** — The natural gesture — a ClusterBaseline with `clusterSelector.matchLabels: {astronomer.io/managed-by: astronomer}` meaning "my whole fleet" — silently includes the management cluster and installs the bundle into the control plane with prune and selfHeal, in the wide-open default project. Worst case a bundle collides with the self-managed astronomer release in the same namespace (both automated+prune+selfHeal in the same project, and `self_manage_migration.go`'s restage loop will fight it), or an operator-authored bundle deletes control-plane resources. **[corrected]** Honest framing is "footgun / inconsistent default" rather than a bug — the selector does what the operator wrote; the collision scenario additionally requires the bundle to target the astronomer namespace. The inconsistency with the platform baseline's explicit exclusion is what makes it worth fixing.

**Remediation**
1. Add `astronomer.io/is-local: "false"` to the base matchLabels in both `clusterBaselineClusterSelector` (`:1232-1234`) and `gitOpsTargetClusterSelector` (`:2733-2735`), using `argolabels.IsLocalLabelKey` rather than a literal.
2. Add an explicit opt-in `spec.clusterSelector.includeManagementCluster bool` that drops the exclusion when true.
3. Validate in `validateClusterBaselineSpec` (`:1511`) / `validateGitOpsTargetSpec` (`:2756`) that an operator setting `astronomer.io/is-local: "true"` in matchLabels has also set the opt-in.
4. **Release note required:** this is a behavior change for any existing ClusterBaseline that intentionally covers the local cluster.

**Tests** — `internal/crd/controller_test.go` — `clusterBaselineClusterSelector(LabelSelectorSpec{MatchLabels: {"astronomer.io/managed-by":"astronomer"}})` emits `astronomer.io/is-local: "false"`; the opt-in removes it; same two cases for `gitOpsTargetClusterSelector`. An envtest case asserting a fleet-wide ClusterBaseline produces no Application whose destination is the local cluster's `api_server_url`.

**Acceptance**
- [ ] Fleet-wide selectors exclude the management cluster by default.
- [ ] An explicit opt-in exists and is validated.
- [ ] Release note drafted.

### [leave-local-ownership-exclusion-fails-open] The `leave_local` ownership decision fails open on a transient DB error

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** argocd · **Effort** S
**⚠ Verification note:** this is the one item in the input that carried **no verifier verdict** ("no verdict returned; unverified"). Re-verify the code before acting on it.

**Evidence (as claimed, unverified)** — `internal/server/baseline_appsets.go:313-337` `leaveLocalExclusionsByComponent`: `if q == nil { return out }` and `decisions, err := q.ListArgoCDBaselineOwnershipDecisionsByDecision(...); if err != nil { return out }` — both return an empty exclusion map, and the comment at `:310-312` says so explicitly ("A query error (or nil querier) fails OPEN to an empty map"). The caller at `:280-289` then builds the ApplicationSet with no cluster-id `NotIn` matchExpression (`:371-381`) and immediately `res.Update`s it (`:300-303`), on every 30s reconcile tick (`internal/server/self_manage_argocd.go:56`, `:203-208`). Generated Applications carry `automated: {prune: true, selfHeal: true}` (`baseline_appsets.go:449-453`).

**Why it matters** — `leave_local` is the operator's explicit statement that a cluster keeps a component under local management. On a DB blip during a reconcile tick the exclusion disappears from the generator, ArgoCD generates a baseline Application for that cluster, and prune+selfHeal takes ownership of resources the operator asked it not to touch. Because the appset is rewritten every 30s, one failed query is enough; the next successful tick restores the exclusion, deleting the Application again with the `resources-finalizer` cascading a prune — an oscillating, destructive loop driven by a transient error.

**Remediation**
1. Change `leaveLocalExclusionsByComponent` to return `(map[string][]string, error)`.
2. Propagate the error to `ensureBaselineApplicationSets` (`:267`) and return it, so `reconcileLocalArgoSelfManagement` (`internal/server/self_manage_argocd.go:203-208`) **skips the appset write entirely** for that tick and logs a warning, leaving the previously-correct ApplicationSet in place.
3. Do the same for the nil-querier branch, unless the caller is a test fake — tests should inject an empty-but-successful lister instead.
4. Add a metric/audit for skipped baseline reconciles.

**Tests** — `internal/server/baseline_appsets_test.go` — with a fake querier whose `ListArgoCDBaselineOwnershipDecisionsByDecision` errors, assert `ensureBaselineApplicationSets` returns an error AND performs zero Update/Create calls against the fake dynamic client (today it writes an appset with no exclusions). Second case: an existing appset carrying a cluster-id `NotIn` expression is byte-identical after the failing tick.

**Acceptance**
- [ ] Claim re-verified against current code before implementation.
- [ ] A query error skips the write rather than writing an unfiltered appset.
- [ ] Skipped reconciles are observable.

### [ui-proxy-permission-mapping-ignores-argocd-verbs] The ArgoCD UI proxy maps all mutations to `clusters:update`

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** argocd · **Effort** S

**Evidence** — `internal/server/middleware/argocd_authz.go:88-100` `argoCDProxyPermission` returns `(rbac.ResourceClusters, rbac.VerbUpdate)` for every non-GET method and for the paths in `isArgoCDMutatingPath` (`:104-114` — only `/sync`, `/rollback`, `/terminate`, `/resource-action`), and `(rbac.ResourceArgoCD, rbac.VerbRead)` otherwise. It never consults the argocd-native verbs the catalog defines: `internal/db/migrations/098_rancher_grade_role_catalog.up.sql:15` GitOps Admin = `argocd:[create,read,update,delete,list,sync,manage]` with `clusters:[read,list]` only; `:48` GitOps Deployer (a **project** role) = `argocd:[read,list,sync]`; `internal/db/migrations/032_builtin_role_catalog.up.sql:20` Cluster Operator = `argocd:[read,list,sync]`. Since `internal/rbac/engine.go:140-160` only matches bindings whose scope applies, GitOps Admin's global `clusters:[read,list]` cannot satisfy `clusters:update`.

**Why it matters** — The model and the enforcement disagree in both directions. A GitOps Admin — the role created to administer ArgoCD — 403s the moment they click Sync. Conversely, any principal holding `clusters:update` (a plain cluster administrator with no GitOps grant) can sync, roll back, terminate and delete Applications fleet-wide through the admin-token-injecting proxy. Operators will conclude the GitOps roles are broken and hand out `clusters:update`, which is exactly the over-grant the catalog was designed to avoid. **[corrected — this strengthens the finding]** GitOps Deployer and Workload Deployer are **project** roles, and the proxy calls `engine.CheckPermission(..., clusterID, uuid.Nil)` at `argocd_authz.go:77` with a nil projectID, so `bindingApplies` never matches a project-scoped binding — **those roles cannot even READ through the proxy**, not merely fail on Sync.

**Remediation**
1. Change `argoCDProxyPermission` to return the ArgoCD-native permission, and have `ArgoCDAuthz` accept a **set of alternatives**: reads require `argocd:read` OR `argocd:list`; sync/rollback/terminate/resource-action require `argocd:sync` OR `clusters:update`; POST/PUT/PATCH/DELETE on applications/applicationsets/projects/repositories require `argocd:update`/`argocd:manage` OR `clusters:update`.
2. Extend the `CheckPermission` call at `:77` to loop over the alternatives (mirror `server.requireAnyPermission`'s shape).
3. **Decide and implement what scope project-scoped argocd grants evaluate against**, otherwise GitOps Deployer / Workload Deployer remain unusable in the console even after the verb mapping is fixed. This is the load-bearing half.
4. Extend `isArgoCDMutatingPath` to cover `/resource/actions`, `/operation`, and `?refresh=hard` on application GETs.
5. Update `docs/security-sensitive-routes.json`.

**Tests** — `internal/server/middleware/argocd_authz_test.go`: (a) `argocd:[read,list,sync]` → 200 for `POST /argocd/api/v1/applications/foo/sync` (fails today, 403); (b) `argocd:[read,list]` → 403 on the same and 200 on `GET /argocd/api/v1/applications`; (c) `clusters:update`-only still passes (back-compat); (d) `GET /argocd/api/v1/applications/foo/resource/actions` is classified as a mutation; (e) a project-scoped GitOps Deployer binding can read through the proxy.

**Acceptance**
- [ ] GitOps Admin and GitOps Deployer can use the console at their documented level.
- [ ] `clusters:update`-only holders keep working (no regression) but the native verbs are consulted first.
- [ ] Project-scoped argocd grants resolve.

### 6.7 Code health and gates

### [drain-force-field-never-reaches-ui] `drainNodeRequest.Force` exists in Go but is absent from the spec, generated types, and api.ts

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** code-health / parity · **Effort** S

**Evidence** — Go: `internal/handler/resources.go:62-72` declares `Force bool \`json:"force,omitempty"\`` with a comment mirroring `kubectl drain --force`; it gates real behavior at `:1318` (`case len(pod.Metadata.OwnerReferences) == 0 && !req.Force:` → the pod becomes a blocker). Spec: `docs/openapi.yaml:1335-1349` `NodeDrainRequest` lists only ignore_daemonsets / delete_empty_dir_data / grace_period_seconds / dry_run. Generated TS: `frontend/src/types/openapi.generated.ts:906-911` mirrors that. Hand-written TS: `frontend/src/lib/api.ts:456-461` `DrainNodeRequest` also omits it. Both UI call sites omit it: `frontend/src/lib/api.ts:493` `drainNode(clusterId, nodeName, data: DrainNodeRequest = {})`, called from `frontend/src/routes/dashboard/clusters/$id/$resource/index.tsx:864` (passes only ignore_daemonsets/delete_empty_dir_data) and `frontend/src/routes/dashboard/clusters/$id/nodes/$nodeName/index.tsx:354` (**passes no body at all**, so it also silently drops ignore_daemonsets). **[corrected]** The original cited `resources.go:1485 Force: true` as an in-repo consumer; that is `kubeutil.ApplyOptions` on the server-side-apply proxy path and is unrelated. **There is no in-repo consumer of the drain Force field at all** — which strengthens the dead-field claim but the cited line must be dropped.

**Why it matters** — Rancher's node drain exposes `--force`. Here, a node carrying a standalone pod has no force affordance anywhere in the product. **[corrected]** Not a dead end: the resource explorer can delete the standalone pod directly and drain then proceeds, so this is a missing-affordance/parity gap rather than "must fall back to kubectl". The `nodes/$nodeName` no-body bug is the independently significant half.

**Remediation**
1. Add `force: {type: boolean, default: false}` to `NodeDrainRequest` in `docs/openapi.yaml:1335-1349`; run `make openapi-embed` and `npm run openapi:types`.
2. Delete the hand-written `DrainNodeRequest`/`DrainNodePodRef`/`DrainNodeResponse` (`frontend/src/lib/api.ts:456-477`) and re-export the generated `NodeDrainRequest`/`NodeDrainPodRef`/`NodeDrainResponse` — see `api-ts-shadow-generated-types`.
3. Add a `force` checkbox to the drain dialog at `frontend/src/routes/dashboard/clusters/$id/$resource/index.tsx:864`, labelled with the irreversibility warning already written in `resources.go:67-70`.
4. Fix `frontend/src/routes/dashboard/clusters/$id/nodes/$nodeName/index.tsx:354` to pass a body.
5. Close the gate hole — see `no-request-schema-field-gate`.

**Tests** — Go: `TestDrainNodeRequestFieldsAreDocumented` in `internal/handler` — reflect over `drainNodeRequest`'s json tags and assert every tag appears under `components.schemas.NodeDrainRequest.properties` in `internal/handler/assets/openapi.yaml`. Fails today on `force`. Frontend: vitest asserting `drainNode` forwards `{force: true}`; a Playwright assertion that the drain dialog renders a force control.

**Acceptance**
- [ ] `force` is in the spec, the generated types, and the drain dialog.
- [ ] The node detail page sends its drain options.
- [ ] No hand-written drain types remain.

### [api-ts-shadow-generated-types] Seven hand-written types in api.ts shadow generated OpenAPI types, and the gate is blind to it

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** code-health · **Effort** S

**Evidence** — `frontend/src/lib/api.ts` declares 28 local types, 7 of which duplicate names exported from `frontend/src/types/openapi.generated.ts`: `FeatureFlags` (`:217`), `RegistrationStep` (`:313`), `RegistrationStatus` (`:335`), `NodeKeyValueRequest` (`:498`), `NodeKeyRequest` (`:503`), `NodeTaintRequest` (`:507`), `NodeTaintRemoveRequest` (`:513`) — all also at `openapi.generated.ts:1681, 1704-1709, 1731-1732`. `DrainNodeRequest` (`:456`) shadows `NodeDrainRequest` under a different name and is missing `force`. The gate cannot see any of it: `scripts/code-health-inventory.mjs:151` does `if (rel(file) === 'frontend/src/types/openapi.generated.ts') continue;` inside `duplicateFrontendApiShapeTypes`, and the regex only matches names ending Request/Response/WriteRequest, so `FeatureFlags`/`RegistrationStatus` would not be caught anyway.

**Why it matters** — The point of `npm run openapi:types:check` is that the frontend cannot silently disagree with the backend contract. Seven hand-copies punch through it: a backend field added to the spec regenerates `openapi.generated.ts` and passes the gate green while `api.ts` — the module every caller imports — keeps the stale shape. That is exactly how the drain `force` field got lost.

**Remediation** — **[corrected — the original drift diagnosis is inverted; do not "fix" the spec]** The handler only validates `key` (`internal/handler/resources.go:1190-1194` — trims Key, 400s if empty; `Value` is used as-is and may legitimately be an empty label/annotation value), so the generated `"value"?: string` is **correct** and the hand-written `value: string` in api.ts is the odd one out. Drop the proposed "make `value` required in docs/openapi.yaml" step; it would encode a constraint the backend does not enforce.
1. Delete the 7 shadow declarations plus `DrainNodeRequest`/`DrainNodePodRef`/`DrainNodeResponse` (`api.ts:456-477`) and replace with re-exports from `@/types/openapi.generated`, keeping the old names as aliases (`export type DrainNodeRequest = NodeDrainRequest;`) so the change is import-compatible.
2. Add `force` to `NodeDrainRequest` in the spec (from `drain-force-field-never-reaches-ui`), then `make openapi-embed` + `npm run openapi:types`.
3. Close the gate hole: add a hard gate `shadowedGeneratedApiTypes(files)` in `scripts/code-health-inventory.mjs` that collects the exported names from `openapi.generated.ts` (**without skipping that file**) and fails when any other file under `frontend/src` declares an interface/type of the same name. Register it in `buildInventory()`, in `renderMarkdown()`'s `hardFailures` sum, and in the Hard Gates list; regenerate `docs/rancher-quality-phase0-code-health-inventory.md`.

**Tests** — `cd frontend && npm run code-health` must fail on the current tree (7 shadowed names) and pass after. `frontend/src/lib/__tests__/api-types.test.ts` with a type-level assertion so `npm run type-check` also catches divergence. `./scripts/verify-enterprise.sh api-contract` stays green.

**Acceptance**
- [ ] No hand-written type shadows a generated one.
- [ ] The new hard gate fails when one is reintroduced.
- [ ] No spec change encodes a constraint the backend does not enforce.

### [no-request-schema-field-gate] The API contract gate never checks that a Go request-DTO field exists in the OpenAPI schema

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** test-gap / code-health · **Effort** M

**Evidence** — `scripts/verify-enterprise.sh:55-83` `verify_contract_artifacts` runs exactly five checks: `openapi-coverage` (route coverage), generated frontend types, the embedded-asset byte compare between `docs/openapi.yaml` and `internal/handler/assets/openapi.yaml`, error-code docs, and JSON well-formedness of three route-metadata files. None inspect a request body schema; `scripts/openapi-coverage.mjs` (223 lines) contains no reference to `properties`, `requestBody`, or `schemas`. `verify_api_contract` (`:85-107`) adds route-table, error-catalog and route-security runs — again nothing field-level. The live consequence is `drain-force-field-never-reaches-ui` with every gate green.

**Why it matters** — This is the gate that would have caught the drain parity bug and keeps the generated-types pipeline honest. Without it the contract is only as good as whoever remembered to edit two files, and the failure is silent both ways: a Go field with no spec entry is unreachable from the UI; a spec field with no Go field decodes to nothing.

**Remediation** — A Go-side reflection test, so it lives next to the DTOs.
1. Create `internal/handler/openapi_schema_contract_test.go` with a registry `var openapiRequestDTOs = map[string]any{"NodeDrainRequest": drainNodeRequest{}, "NodeKeyValueRequest": nodeKeyValueRequest{}, "NodeTaintRequest": nodeTaintRequest{}, "CreateClusterRequest": CreateClusterRequest{}, ...}` mapping schema name → zero value. Seed the ~30 highest-traffic request DTOs; a partial registry that grows is worth more than a blanket rule with an exemption list.
2. The test parses `internal/handler/assets/openapi.yaml` (already embedded and already byte-identical to `docs/openapi.yaml` by the existing gate) and walks each struct's json tags via reflect, skipping `json:"-"`.
3. **[corrected]** Assert **"every Go json tag has a schema property"**, not a strict 1:1 match. A strict match would fail immediately on legitimate cases — embedded structs, `omitempty` optionality, and fields like `NodeKeyValueRequest.Value` that are optional in the schema by design.
4. Wire into `verify_api_contract`: `run_logged openapi-schema-contract go test ./internal/handler/ -run TestOpenAPIRequestSchemaFields -count=1`.

**Tests** — `TestOpenAPIRequestSchemaFields` must FAIL on the current tree with `NodeDrainRequest: Go field "force" has no schema property` and pass once the spec gains `force`. Add a negative case by temporarily adding a bogus property to a schema and confirming the reverse direction is reported (as a warning, per step 3).

**Acceptance**
- [ ] The test fails on the pre-fix tree for the known-missing field.
- [ ] It is wired into `verify-enterprise.sh`.
- [ ] It does not fail on legitimate optionality or embedded structs.

### [golangci-lint-never-runs-in-ci] `.golangci.yml` and `make lint` exist but nothing invokes golangci-lint

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** test-gap / code-health · **Effort** S

**Evidence** — `Makefile:68-69` defines `lint: golangci-lint run ./...` and `.golangci.yml:1-12` configures `default: standard` with sqlc excluded. `rg -n golangci .github scripts Makefile` returns only those two Makefile lines. `scripts/verify-enterprise.sh` (the self-declared authoritative entry point) runs `go build`, `go vet`, `go test`, `go test -race`, and contract checks, never golangci-lint. `.github/workflows/pr-validation.yaml:43` and `api-contract.yaml:42` only shell out to it. `golangci-lint` is not installed in this environment, so the config has almost certainly never been enforced.

**Why it matters** — The `standard` set includes `unused`, `errcheck`, `ineffassign`, and `staticcheck` — exactly the dead-code class this audit had to find by hand. `go vet` is a much weaker net. **[corrected]** The originally cited example (`internal/handler/monitoring.go:1165 _ = ok`) is **not** valid evidence: assignment to the blank identifier is deliberately ignored by ineffassign and staticcheck. It is still dead code worth deleting (folded into `monitoring-go-3815-line-split` Part A) but no linter would flag it.

**Remediation**
1. Add a `step "Go lint"` to `verify_backend()` in `scripts/verify-enterprise.sh`, right after the `go vet` step (~line 122): `run_logged go-lint golangci-lint run ./...`.
2. Add a `golangci/golangci-lint-action@v6` install step to the `backend` job in `.github/workflows/pr-validation.yaml` before the gate step; pin the version alongside the existing `SQLC_VERSION`/`OAPI_CODEGEN_VERSION` convention.
3. Expect a first-run backlog: run `golangci-lint run ./... | wc -l` first. If large, land the gate with `--new-from-rev origin/main` so only new code is blocked and file existing hits as a follow-up.
4. Keep the sqlc exclusion; add `internal/handler/assets` if the embed dir trips it.

**Tests** — CI is the test: `./scripts/verify-enterprise.sh backend` must fail on a branch introducing an unused unexported function or an ineffectual assignment. Verify by deliberately introducing one, confirming red, then removing it.

**Acceptance**
- [ ] `golangci-lint` runs in the backend gate and in CI.
- [ ] A deliberate `unused`/`ineffassign` regression turns the gate red.
- [ ] Any pre-existing backlog is either fixed or explicitly staged with `--new-from-rev`.

### [permission-decision-unmemoized-defeats-every-usememo] `usePermissionDecision` returns a fresh object every render

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** code-health · **Effort** S

**Evidence** — `frontend/src/lib/permission-hooks.ts:9-16` returns `explainPermission(user, resource, verb, scope)` directly with no `useMemo`; `explainPermission` (`frontend/src/lib/permissions.ts:79-102+`) returns an object literal on every branch. `frontend/src/routes/dashboard/clusters/$id/$resource/index.tsx:793-808` `useClusterResourcePermissions` calls it 9× and returns a bare object literal (only `scope` is memoized, `:795`), so `permissions` is fresh every render. Every column memo in that file therefore has a dep that changes identity every render: `:2081` and `:2303` use `[clusterId, permissions]`; `:1029, 1588, 1676, 1764, 2253, 2379, 2452, 2520` use `[clusterId, permissions.read, permissions.update, permissions.delete]` — two dep-list styles, both equally ineffective. **[minor]** Counts in the original were slightly off (18 `useMemo` occurrences in the file, ~10 `usePermissionDecision` call sites there and ~25 across src); immaterial.

**Why it matters** — Each memoized `columns` array rebuilds on every render, allocating a fresh accessor closure per column including one rendering a full `<ActionMenu>` with 3 onClick closures per row. `DataTable` receives a new `columns` identity every render, so any internal memoization is defeated and every row re-renders on every keystroke in the search box, every polling refetch, and every dialog open/close. **[corrected]** Downgraded from high: this is wasted re-render work, not incorrect behavior — nothing renders wrong.

**Remediation**
1. `frontend/src/lib/permission-hooks.ts:13-16` — wrap: `return useMemo(() => explainPermission(user, resource, verb, scope), [user, resource, verb, scope.type, (scope as {id?: string}).id]);`. The extra primitive deps make it safe for callers passing an inline literal.
2. `frontend/src/routes/dashboard/clusters/$id/$resource/index.tsx:796-807` — wrap the returned object in `useMemo(() => ({create, read, ...}), [create, read, update, del, scale, restart, exec, logs, manage])`.
3. Normalize the two dep-list styles to `[clusterId, permissions]` at `:1029, 1588, 1676, 1764, 2081, 2253, 2303, 2379, 2452, 2520` — correct once `permissions` is stable.
4. Confirm `explainPermission` is pure w.r.t. `user` identity; if `useAuthStore((s) => s.user)` can return a new object on unrelated store writes, add a shallow selector.

**Tests** — `frontend/src/lib/__tests__/permission-hooks.test.ts` — `renderHook` with identical props over two rerenders, assert `result.current` is referentially identical (`toBe`). Fails today. Second: a render-counting test on one table asserting the `columns` array identity is stable across a parent rerender that does not change `clusterId`.

**Acceptance**
- [ ] `usePermissionDecision` is referentially stable across rerenders with identical inputs.
- [ ] Column memos actually cache.
- [ ] Dep-list styles are normalized.

### [resource-table-copypaste-2855-line-route] Near-identical `<X>Table` components in one 2,855-line route file

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** code-health · **Effort** L

**Evidence** — `frontend/src/routes/dashboard/clusters/$id/$resource/index.tsx` is 2,855 lines with 21 top-level `*Table` components. `ReferenceGrantsTable` (`:2283-2330`) and `PVsTable` (`:2333+`) are line-for-line the same shell — `useXQuery + useRouter + useDeleteX + useClusterResourcePermissions + useState<yamlTarget> + useState<deleteTarget> + useMemo(columns with a 3-item ActionMenu) + <DataTable> + <YamlViewDialog> + <ConfirmDialog>` — differing only in resource string, hook, mutation, and copy. The 26 module-level column arrays (`:147-756`) are the only genuinely per-resource content. The copy-paste has already produced drift: dep lists differ between clones (`:2081/:2303` vs `:1588/1676/1764/2253/2379/2452`).

**Why it matters** — Adding one resource type means cloning ~50-90 lines; changing action-menu UX or permission-denied behavior means editing many places, and a miss is invisible. It is a merge-conflict magnet, and the whole file ships in the route chunk for anyone opening any resource list.

**Remediation** — **[corrected — volume was materially overstated; the file already contains the abstractions the original proposed inventing]** `RouteTable<T>` at `:2114` already generalizes the shell and the five `*RoutesTable` components (`:2173-2201`) are 6-line wrappers around it; `NamespacedActions` is a shared action-menu component; `GenericResourceTable` exists at `:2582`. The 26 column arrays are irreducible. Genuine duplication is roughly **10-12 hand-rolled shells at ~50 lines each (~500-600 lines)**, not 1,500-1,800. **The right framing is "finish the `RouteTable`/`GenericResourceTable` generalization the file already started."**
1. Generalize `RouteTable<T>` (or extract it to `frontend/src/components/resources/resource-list-table.tsx`) to take `{ clusterId, resourceType, columns, useList, useDelete?, keyExtractor, namespaced, labels }` and own the yamlTarget/deleteTarget state, the ActionMenu composition, DataTable, YamlViewDialog, and ConfirmDialog.
2. Convert the ~10-12 remaining hand-rolled shells to use it.
3. Move the 26 column arrays (`:147-756`) into `frontend/src/components/resources/columns/` split by family — workloads, networking, gateway, storage, config, rbac.
4. Reduce `index.tsx` to the route shell plus a `RESOURCE_REGISTRY: Record<string, ResourceTableConfig>` dispatcher.
5. Keep the genuinely bespoke tables (`WorkloadsTable:1273` with scale/restart/exec/logs; `NodesTable:824` with cordon/drain/taints) as thin wrappers passing extra menu items in.

**Tests** — `frontend/src/components/resources/__tests__/resource-list-table.test.tsx` covering the shared shell once: (a) delete disabled with the RBAC reason when `permissions.delete.allowed === false`, (b) confirming calls the mutation with the right path, (c) row click suppressed when read is denied. Registry completeness: `expect(Object.keys(RESOURCE_REGISTRY)).toEqual(expect.arrayContaining(SUPPORTED_RESOURCE_TYPES))`. The existing `npm run test:e2e:smoke` route crawl must stay green.

**Acceptance**
- [ ] Remaining hand-rolled shells use the shared component.
- [ ] Column definitions live outside the route file.
- [ ] Smoke crawl green; no behavior change.

### [newapp-1600-line-constructor] `server.NewApp` is a single 1,600-line function constructing 85 handlers

**Status:** PENDING APPROVAL · **Severity** medium · **Dimension** code-health · **Effort** L

**Evidence** — `internal/server/server.go:304` `func NewApp(...)` runs to ~`:1929` (~1,626 lines) of a 2,298-line file. Inside: 85 `handler.New*(...)` calls, 141 lines of `deps.`/`Set*` wiring, a `RouterDependencies{...}` composite literal opening at `:1150` and running ~350 lines, then conditional post-literal patches at `:1498-1499, 1630-1631, 1658`. The only structure is prose comments. The next largest symbol is `StartInternalArgoCDProxy` at `:1994` (75 lines). The "missed wiring is invisible until runtime" precedent is documented in-tree at `:469-473` (the apiserver-allowlist handler was constructed only in test routers, leaving documented routes unrouted in production).

**Why it matters** — Every feature adding a handler edits the same function, making it the highest merge-conflict surface in the backend across parallel branches. There is no way to construct just the tunnel or just the auth subsystem for a wiring test, which is why `validateProductionSecurityWiring` (`:199`) re-derives its checks from the finished `RouterDependencies`. **[corrected]** Downgraded from high: no demonstrated defect in current behavior, and `validateProductionSecurityWiring` does backstop the wiring class post-hoc. `New` being only ~110 lines in the CRD controller weakens the analogous argument there, but here the function really is the full 1,600.

**Remediation** — Split into phase functions in new files, each returning a small typed struct; leave `NewApp` as ~120 lines of sequenced calls. The dependency direction is already one-way, so no shared mutable state crosses backwards.
1. `internal/server/deps_core.go` — `buildCore(ctx, cfg, logger) (coreDeps, error)`: DB open + migration-drift check, encryptor, SSO manager, Dex bootstrap/drift, JWT manager, RBAC engine, event bus, redis, leader elector (`server.go:304-~400`).
2. `internal/server/deps_tunnel.go` — `buildTunnel(core) tunnelDeps`: Locator, shared connect failure limiter, hub, `tunnel2.RemoteServer` (`:421`), cross-pod K8sRequester + HelmRequester with the PSK derivation (`~:380-450`).
3. `internal/server/deps_handlers.go` — `buildHandlers(core, tunnel) handlerDeps`: the 85 constructor calls and their Set* wiring (`~:450-1149`), sub-grouped by domain if over ~600 lines.
4. `internal/server/deps_router.go` — `routerDeps(core, tunnel, handlers) RouterDependencies`: the composite literal (`:1150-~1497`) plus the conditional assignments.
5. `internal/server/background.go` — `startBackground(ctx, core, handlers)`: reconciler/leader-loop starts and `kickFirstBootCatalogSync` (`~:1660-1906`).

**Tests** — `internal/server/deps_test.go::TestBuildHandlersWiresEveryRouterDependency` — reflect over `RouterDependencies` and assert every non-nil-able pointer field is populated by `routerDeps(...)` under a full-feature config; this catches the "handler constructed but never assigned" class that previously shipped. `TestBuildCoreFailsClosedOnDirtySchema` exercising the migration-drift branch (`:323-327`) in isolation.

**Acceptance**
- [ ] `NewApp` is ≤ ~150 lines.
- [ ] The wiring-completeness reflection test exists and passes.
- [ ] No route or behavior change (`RouteTable` golden green).

---

## 7. P3 — low

These are real but bounded. Several are pure backlog structure with no defect; those are marked as such so the owner can reject them cheaply.

### 7.1 Parity

### [no-default-role-no-creator-owner] New users get zero permissions; creators get no binding on what they create

**Status:** PENDING APPROVAL · **Severity** low · **Dimension** parity-core · **Effort** M

**Evidence** — `rg 'CreateGlobalRoleBinding|CreateClusterRoleBinding|CreateProjectRoleBinding'` over non-test, non-sqlc Go returns only the RBAC route registrations (`internal/server/routes_rbac_audit_agents.go:34,37,40,44,47,50`), the RBAC handler (`internal/handler/rbac.go:493,582,683`), and the agent-ingest special case (`internal/auth/agent_ingest_token.go:247`). `internal/handler/sso.go:501-529` `findOrCreateUser` provisions the user row with no binding; neither `users.go` nor `scim.go` `CreateUser` (`:222`) creates one; there is no `new_user_default` column on `global_roles` (`001_initial.up.sql:686-689`). `internal/handler/projects.go:471-548` `Create` inserts no `project_role_bindings` row for the caller (only `created_by_id` at `:490-493`). Rancher: `NewUserDefault` (`rancher/pkg/apis/management.cattle.io/v3/authz_types.go:162-164`) honored at `rancher/pkg/auth/providers/common/usermanager.go:674`, and creator-owner via `project_cluster.CreatorIDAnnotation`.

**Why it matters** — **[corrected — the creator-owner half of the original rationale is factually wrong and must not be repeated]** Creating a project requires `projects:create` evaluated at `POST /api/v1/projects/`, where `permissionScopeIDs` (`routes.go:1232-1250`) yields `uuid.Nil`/`uuid.Nil`, so only a **global** binding can pass — and `bindingApplies` (`engine.go:271-292`) returns true for global bindings at every scope. A platform-operator who creates a project **can** see and manage it; it is not invisible. Same for cluster adoption. What actually remains: every SSO- or SCIM-provisioned user lands with zero bindings and a 403 on essentially every route unless an operator has authored a group mapping matching one of their claims, so onboarding requires a manual admin action per user. Plus there is no durable ownership record beyond `created_by_id`.

**Remediation** — Part 1 is the piece worth doing; part 2 is a nice-to-have.
1. **Default role.** Add a boolean `new_user_default` column to `global_roles` via migration; expose it on the role CRUD request/response in `internal/handler/rbac.go` (`roleRequest` at `:161` already carries `IsBuiltin`). In `internal/handler/users.go` `Create`, `internal/handler/scim.go:222` `CreateUser`, and the SSO just-in-time path in `internal/handler/sso.go`, insert a `global_role_bindings` row for every role with the flag set, using a distinct `source` value (`'default'`) so it is not swept by `SyncUserGroups`. Seed the flag onto the existing `Standard User` role.
2. *(optional)* **Creator-owner.** In `(*ProjectHandler).Create` (`internal/handler/projects.go:471`), after the project row commits inside the same transaction (`SetRunTx` is wired at `:243`), insert a `project_role_bindings` row binding the creator to the built-in `Project Owner` role; mirror in `(*ClusterHandler).Create` with `Cluster Owner`. Skip when the caller is a superuser; record an audit row; invalidate the caller's RBAC cache.

**Tests** — `internal/handler/users_default_role_test.go::TestCreateUser_BindsNewUserDefaultGlobalRoles`, `::TestCreateUser_NoDefaultRolesProducesNoBindings`; `internal/handler/scim_default_role_test.go::TestSCIMCreateUser_BindsNewUserDefaultRoles`. If part 2: `internal/handler/projects_creator_owner_test.go::TestCreate_BindsCreatorAsProjectOwner`, `::TestCreate_SuperuserCreatorSkipsRedundantBinding`, `::TestCreate_BindingRollsBackWithProjectOnTxFailure`, and the cluster mirror.

**Acceptance**
- [ ] A flagged default role is applied to SSO- and SCIM-provisioned users automatically.
- [ ] Default bindings are distinguishable from group-sync and manual bindings.

### [git-backed-chart-repos-accepted-never-synced] `repo_type="git"` repos are accepted and stored but never cloned or indexed

**Status:** PENDING APPROVAL · **Severity** low · **Dimension** parity-features · **Effort** M

**Evidence** — `internal/handler/catalog.go:345-352` explicitly accepts git repos (`// DIR-07: accept git-sourced chart repos (clone/index path lands in worker).`) with no validation of `repo_type` against an allowed set, and persists the row. `SyncRepo` (`:497-503`) branches only two ways (`isOCIRepoSpec` → OCI ingest, else `fetchAndIngestRepoIndex`), and `fetchAndIngestRepoIndex` (`:606`) builds `<url>/index.yaml`. `isOCIRepoSpec` (`:1501-1508`) knows only `oci`. No git branch in the worker sweep. The only git-clone code in the tree is the unrelated GitOps source sync (`internal/worker/tasks/gitops_sync.go`). The UI offers only `['helm','oci']` (`frontend/src/routes/dashboard/catalog/index.tsx:1033`).

**Why it matters** — Rancher's ClusterRepo supports git-backed chart repos as a first-class source (`pkg/apis/catalog.cattle.io/v1/types.go:71-75`), the common pattern for internal chart monorepos with no published index. Here an API client creates one, it appears in the list, and every sync fetches `<git-url>/index.yaml` and 404s. **[corrected]** API-only (the UI never offers it), no data-loss or security consequence — a dead row with a failed sync. The substantive half is the parity gap.

**Remediation** — **Reject is the recommended default.**
- **Reject (S):** delete the git special-case at `internal/handler/catalog.go:345-352` and return 400 `unsupported repo_type` for anything not in `{helm, oci}`, so create fails loudly instead of persisting a dead row.
- **Implement (M):** add `internal/catalog/gitrepo.go` using go-git (already a dependency) to shallow-clone `url`@`branch` into a temp dir, walk for Chart.yaml files (or an in-repo index.yaml), and feed discovered charts through the same ingest as the HTTP path; add branch/path/auth fields to `CreateRepoRequest`/`UpdateRepoRequest` (`:317-327`, `:399-409`) and a `git` option to the UI toggle.

**Tests** — `internal/handler/catalog_test.go::TestCreateRepoGitTypeIsSyncable` — either charts ingest from a fixture git dir (implement) or `CreateRepo` returns 400 (reject). Today `SyncRepo` returns 502 after an index.yaml 404; the test must show that is no longer reachable.

**Acceptance**
- [ ] Creating a git repo either works end-to-end or is rejected at create.
- [ ] No path can persist a permanently unsyncable repo row.

### [per-cluster-observability-ia-gap] Monitoring, Alerting, Logging and CIS are global-only screens

**Status:** PENDING APPROVAL · **Severity** low · **Dimension** parity-features · **Effort** L

**Evidence** — `getClusterNavGroups` (`frontend/src/components/layout/sidebar.tsx:169-215`) contains Overview, Adoption, Nodes, Namespaces, Events, Tools, Apps, Service Mesh, plus agent-gated Image Scans, Shell, Control-plane DR, Registries, Snapshots, Network & Access — no Monitoring, Alerts, Logging, Notifiers, or CIS. Those live only at platform level (`:120-127` Observability, `:143-149` Security). The global Monitoring page compensates with an in-page cluster picker and says so (`frontend/src/routes/dashboard/monitoring/index.tsx:22-27`). Rancher mounts monitoring/logging/alerts/notifiers/cis/backups under `cluster { path: '/c/:cluster_id' }` (`ui/app/router.js:58-62, 70, 90-92`) and repeats per-project (`:126-130`). The backends are already cluster-aware (e.g. `internal/handler/logging.go:736` authorizes on `output.ClusterID`).

**Why it matters** — Muscle memory from Rancher is "select cluster → do the cluster thing". **[corrected]** This is an IA preference claim and partly mitigated today: the global Alerting/Logging/Security tables carry a cluster column and cluster-scoped create forms, Monitoring has a picker, and the cluster Overview surfaces metrics cards and an image-vuln rollup (`frontend/src/routes/dashboard/clusters/$id/index.tsx:363-435`). The one genuinely absent capability — per-cluster monitoring stack state — is already covered by `monitoring-stack-lifecycle-no-ui`. **Treat this as the IA follow-on to that item, not a standalone defect.**

**Remediation**
1. Extract the current global page bodies into `-page.tsx` components parameterised by `clusterId` (the codebase already uses this pattern — `frontend/src/routes/dashboard/rbac/-page.tsx`, `settings/siem/-page.tsx`).
2. Add `frontend/src/routes/dashboard/clusters/$id/{monitoring,alerting,logging,compliance}/index.tsx` reusing those components with a fixed clusterId.
3. Add the four nav entries to `getClusterNavGroups` with matching permission metadata.
4. Add cluster-filter query params to the corresponding list hooks.

**Tests** — extend `frontend/src/routes/__tests__/route-ranking.test.ts` so the new paths resolve; a nav test asserting the four entries appear and are filtered out without the declared permission; a component test that `.../alerting` renders only rules whose clusterId matches the route param.

**Acceptance**
- [ ] Cluster-scoped Monitoring/Alerting/Logging/Compliance routes exist and are permission-gated.
- [ ] Global and cluster routes share one implementation.

### [loki-log-query-not-surfaced] The Loki log-query endpoint is implemented and routed but has no UI

**Status:** PENDING APPROVAL · **Severity** low · **Dimension** parity-features · **Effort** S

**Evidence** — `internal/handler/logging.go:722-758` implements `QueryOutput` (`POST /logging/outputs/{id}/query/`, routed `internal/server/routes_tools_controlplane.go:276`) with a real Loki client path and a 501 for other output types. `frontend/src/routes/dashboard/logging/index.tsx:47-55` declares three tabs only (Outputs, Pipelines, Operations); no frontend caller exists.

**Why it matters** — We ship log aggregation configuration but no way to read the aggregated result, so "where are my cluster's logs" still ends in a separate Grafana/Loki tab. The backend work is done and merely unexposed. Purely an unexposed-capability gap; nothing is broken.

**Remediation**
1. Add `queryLoggingOutput(id, body)` to `frontend/src/lib/api/` and a `useQueryLoggingOutput` mutation hook.
2. Add an "Explore" tab to `frontend/src/routes/dashboard/logging/index.tsx`, enabled only for outputs whose `outputType === 'loki'`: LogQL/free-text input plus time-range picker, results rendered with the existing pod-logs viewer styling (`frontend/src/components/workloads/pod-logs-viewer.tsx`).
3. Keep the 501 path visible as a disabled state naming the output type, so the capability boundary is explicit.

**Tests** — `frontend/src/routes/dashboard/logging/explore.test.tsx` — mock a loki output and a query response; assert the tab is enabled only for loki outputs, submitting issues `POST /logging/outputs/:id/query/` with the entered expression and range, and a 501 renders the unsupported state.

**Acceptance**
- [ ] Loki-backed outputs are queryable in-product.
- [ ] Non-Loki outputs show an explicit unsupported state.

### [no-drift-exception-mechanism] No `ignoreDifferences` surface on ClusterBaseline/GitOpsTarget or the typed Application spec

**Status:** PENDING APPROVAL · **Severity** low · **Dimension** argocd · **Effort** M

**Evidence** — `ignoreDifferences`/`IgnoreDifferences` has zero occurrences in `internal/` or `frontend/src`. `internal/handler/argocd/applications.go:27-33` `ApplicationSpec` has only `{Project, Source, Sources, Destination, SyncPolicy}`. `internal/crd/types.go:577-581` `ClusterBaselineSyncPolicy` is only `{Automated, Prune, SelfHeal}`. `selfHeal: true` is unconditional on the platform baseline (`internal/server/baseline_appsets.go:452`) and the self-managed Application (`internal/server/self_manage_migration.go:188`). Rancher/Fleet exposes `Diff *fleet.DiffOptions` on ManagedChart (`rancher/pkg/apis/management.cattle.io/v3/managed_chart.go:34`).

**Why it matters** — Mutating webhooks (Istio/Linkerd/Vault injection, OPA/Kyverno defaulting), HPA-owned `spec.replicas`, and cloud-controller-populated Service fields rewrite exactly the fields ArgoCD manages, producing an endless OutOfSync→sync→mutate loop. **[corrected — the "no workaround" claim is wrong]** `argosecurity`'s mutation walk is not a closed whitelist at the spec level (`internal/argosecurity/policy.go:794-885` restricts only specific keys), so `ignoreDifferences` **passes validation today**: it can be set via `PATCH /api/v1/argocd/instances/{id}/applications/{name}/`, which forwards the raw merge patch verbatim (`internal/handler/argocd.go:2680-2683`), and via the UI proxy (`internal/handler/argocd_ui_proxy.go:664`). The drift-exception annotation is explicitly admitted (`policy.go:1854` rejects only compare-options values other than `IgnoreExtraneous`). **The real, narrow gap:** the typed `CreateApplication` spec cannot express it, and ClusterBaseline/GitOpsTarget sync policy and the platform baseline have no field or default — and for those, selfHeal is operator-opt-in on the CRD path, so the sync-loop scenario is avoidable.

**Remediation** — **[corrected — remediation step 1's "add an explicit admit in the policy key walk" is unnecessary; the policy already permits it]**
1. Add `IgnoreDifferences []ResourceIgnoreDifferences` to `internal/handler/argocd/applications.go` `ApplicationSpec` and to the ApplicationSet template path.
2. Add `IgnoreDifferences` to `ClusterBaselineSyncPolicy` and `GitOpsTargetSyncPolicy` in `internal/crd/types.go`, plus the CRD schemas in `deploy/chart/templates/crd-clusterbaseline.yaml` and `crd-gitopstarget.yaml`; render into the template spec at `internal/crd/controller.go:1105-1108` and `:2307-2312`.
3. Ship a sane default for the platform baseline in `internal/server/baseline_appsets.go`: ignore `/spec/replicas` on Deployments and annotation keys owned by known injectors.
4. Surface it in the frontend Application detail view.

**Tests** — `internal/crd/controller_test.go` — a ClusterBaseline with `syncPolicy.ignoreDifferences: [{group:apps, kind:Deployment, jsonPointers:[/spec/replicas]}]` renders that block into `spec.template.spec.ignoreDifferences`. `internal/argosecurity/policy_test.go` — `ValidateMutation` accepts a spec carrying `ignoreDifferences` and still rejects inline secret material inside it. `internal/server/baseline_appsets_test.go` — the generated baseline template carries the default replicas ignore rule.

**Acceptance**
- [ ] The typed API and both CRDs can express drift exceptions.
- [ ] The platform baseline ships a sensible default.

### 7.2 Agent and transport hygiene

### [tunnel2-unfinished-migration-live-on-server] + [tunnel2-and-serviceproxy-live-but-unused-surfaces] + [tunnel2-parallel-stack-always-on] Two tunnel stacks, one always-on and carrying no traffic; plus a caller-less agent ServiceProxy handler

**Status:** PENDING APPROVAL · **Severity** low · **Dimension** agent / code-health · **Effort** M
*(Three finding IDs, one decision. Fix once.)*

**Evidence** — `internal/server/server.go:421-422` constructs `tunnel2.NewRemoteServer(logger, queries)` unconditionally and `internal/server/routes.go:1028-1038` registers `/api/v1/connect/{cluster_id}/` with no gate; only the `/v2/pods/` demo handler is production-gated (`:1044-1051`, `if !isProductionConfig(cfg)`). The agent half is unreachable in production: `cmd/agent/main.go:46-53` exposes `connect2` as an experimental subcommand ("cannot adopt bootstrap credentials or receive durable-token rotations"), enforced at `:96-101`, and `deploy/agent/install.yaml.template:592-594` hardcodes `command: [astronomer-agent, connect]`. `internal/agent2/client.go:80-82` is `allow := func(proto, address string) bool { return true }`. `internal/tunnel2/server.go:127-201` `authorize` runs the shared `connectauth.Validate` but has **no `agentcompat.Evaluate` version block** (contrast `internal/tunnel/server.go:481-492`) and **no connect-timestamp skew check** (contrast `:470-478`); `tunnel2/server.go:205-209` states it cannot see the agent version. `docs/dual-tunnel-matrix.md` says the install default remains legacy `connect`, cutover is not done, and multi-replica locator HA is "No" for remotedialer. Separately, `Hub.ServiceProxyRequest` (`internal/tunnel/originators.go:213-217`) has **zero non-test callers** repo-wide — the user-facing service proxy route (`routes_resources_workloads.go:192-200`) goes through `handler.ServiceProxyHandler` over the K8sRequester, not the agent message — yet `MsgServiceProxyRequest → ServiceProxy.HandleRequest` is still registered by `cmd/agent/main.go:168` and `internal/server/localcluster.go:211`.

**Why it matters** — **[corrected — this is migration hygiene and drift risk, NOT a security exposure]** tunnel2 enforces the same fail-closed `connectauth.Validate` and the same per-IP `ConnectFailureLimiter`, and the only consumer of a tunnel2 session (`remoteV2PodsHandler`) is excluded from production builds, so a session opened there grants an attacker nothing and still requires a valid durable token. The `allow=true` line is dead code in production. The agent's SERVICE_PROXY handler can only be invoked by the server — the trusted end of a tunnel it already owns. What is real: **`agentcompat` is not a fleet-wide control** because one of two mounted connect surfaces skips it; two stacks must be kept in security lockstep with code nothing exercises; and an operator has no way to declare "we are not using this".

**Remediation** — Pick one and make it explicit.
- **Preferred (gate it):** add `tunnel2_enabled` (default false) to `internal/config` and the chart values; construct `RemoteServer` at `server.go:421` only when set, leaving `deps.RemoteServer` nil so the nil-safe route registration skips `/api/v1/connect` by default. Add a startup validation in `validateProductionSecurityWiring` (`server.go:199`) refusing to start when `tunnel2_enabled` is true AND `server.replicaCount > 1`, encoding the documented locator gap as a check rather than prose. Update the Defaults table in `docs/dual-tunnel-matrix.md`.
- **Alternative (delete):** remove `internal/tunnel2`, `internal/agent2`, the `connect2` verb, and the `RemoteServer` field (~1,080 lines plus wiring). The shared `internal/tunnel/connectauth` work is retained either way.
- **If tunnel2 is retained and the migration is being finished:** add the `agentcompat` gate (carry the agent version in an upgrade header, evaluate in `authorize` before returning authed=true), add the timestamp-skew check, and replace `allow` in `internal/agent2/client.go:82` with an allow-list (`kubernetes.default.svc:443` plus configured service-proxy targets).
- **ServiceProxy:** delete `Hub.ServiceProxyRequest` (`originators.go:213-217`), the `ServiceProxyReply` type, both `RegisterHandler(protocol.MsgServiceProxyRequest, ...)` calls, and `internal/agent/service_proxy.go` — or, if planned, gate it behind an agent config flag defaulting off plus a service/namespace allowlist.
- Add a decision-record comment in `cmd/agent/main.go`.

**Tests** — `internal/server/routes_security_test.go::TestConnectV2RouteAbsentWhenDisabled` — with `tunnel2_enabled=false`, `GET /api/v1/connect/<id>/` returns 404. `internal/server/server_test.go::TestTunnel2RefusesMultiReplica`. A contract test asserting both connect paths call `connectauth.Validate` with the same arguments. `TestNoServiceProxyHandlerRegistered` in `internal/agent` (or delete `service_proxy_test.go` with the file). Existing `internal/tunnel2/connect_auth_test.go` and `connect_limit_test.go` stay green if the route is retained behind the flag.

**Acceptance**
- [ ] An operator can turn the second connect surface off, or it no longer exists.
- [ ] `agentcompat` covers every mounted connect surface, or the uncovered one is unreachable by default.
- [ ] No caller-less agent message handler remains registered.

### [agent-observability-gap] The agent exposes almost no self-telemetry

**Status:** PENDING APPROVAL · **Severity** low · **Dimension** agent · **Effort** S

**Evidence** — `internal/agent/metrics.go:9-36` registers exactly two agent metrics (`astronomer_agent_state_updates_received_total`, `..._handled_total`). No tunnel-connected gauge, reconnect counter, queue-depth gauge, or build_info anywhere in `internal/agent`. `/healthz` is a static 200 (`health.go:532-534`) and `/readyz` a bare boolean (`:536-542`), even though `collectHeartbeat` already computes `DegradedReasons` (`:299-332`). The pod is scraped (`prometheus.io/scrape: "true"`, port 8081) and `/metrics` is served at `health.go:543`.

**Why it matters** — Flapping tunnels, half-open connections, informers that never synced, and OOM-restart loops are hard to see from the cluster side, where the customer's monitoring lives. **[corrected — two of the originally listed gaps are wrong]** (a) Send drops **are** already exported: `tunnel.go:538` calls `observability.RecordDroppedEvent`, incrementing `astronomer_dropped_events_total{component="agent_tunnel_send",reason="channel_full"}` on the default registry (`internal/observability/drop_metrics.go:5-16`), which `promhttp.Handler()` serves — an operator can already alert on drop rate; it just is not namespaced under `agent_`. (b) The default registry also carries the Go and process collectors, so goroutine count and RSS (the OOM signal) are visible. (c) Tunnel state is observable via `/readyz`, which does flip on every transition. **Residual accurate gap:** no connected gauge, no reconnect/flap counter, no build_info, no send-queue-depth gauge, and no degraded-reason detail on the probes.

**Remediation**
1. Extend `internal/agent/metrics.go` with `astronomer_agent_tunnel_connected` (gauge, set from the existing `SetConnectionListener` wiring at `cmd/agent/main.go:301`), `astronomer_agent_tunnel_reconnects_total{reason}`, `astronomer_agent_tunnel_send_dropped_total{type}` (incremented alongside the existing drop record at `tunnel.go:538`), `astronomer_agent_tunnel_send_queue_depth` (sampled from `len(sendCh)`), `astronomer_agent_heartbeat_failures_total`, `astronomer_agent_informer_sync_failed{kind}`, `astronomer_agent_build_info{version,commit}`.
2. Enrich `/readyz` to return the current `DegradedReasons` list and the last-connected timestamp.
3. Ship a companion PrometheusRule (or document the expressions) for flap rate and send-drop rate.

**Tests** — `internal/agent/metrics_test.go::TestTunnelLifecycleMetricsRecorded` using `prometheus/testutil.CollectAndCount`/`ToFloat64` around a simulated connect→drop→reconnect cycle; `internal/agent/health_test.go::TestReadyzReportsDegradedReasons`.

**Acceptance**
- [ ] Connected state, reconnect rate, queue depth, and build info are scrapeable from the agent.
- [ ] `/readyz` reports why it is not ready.

### 7.3 Crypto / TLS

### [argocd-internal-proxy-tls-insecure-skip-verify] + [internal-argocd-proxy-hand-rolled-cert] The internal ArgoCD→cluster proxy uses a per-pod self-signed cert with `Insecure: true` on the client

**Status:** PENDING APPROVAL · **Severity** low · **Dimension** crypto-tls · **Effort** M
*(Two finding IDs, one defect. Fix once.)*

**Evidence** — `internal/server/server.go:2026-2036` generates the listener cert at boot with `certutil.GenerateSelfSignedCertKey(...)` — new key and cert on every pod start, never rotated, never persisted, no CA any peer can pin. `internal/worker/tasks/argocd_auto_register_cluster.go:649-650` returns `&argocdclient.TLSClientConfig{Insecure: true}` alongside the plaintext cluster proxy token from `ensureArgoCDClusterProxyToken` (`:652-685`) and the server URL `<base>/api/v1/internal/argocd/clusters/<id>/k8s` (`:638`); the in-code comments at `:640-649` record the CAData pinning as an outstanding follow-up. That token is the sole gate on the front door (`routes.go:1064-1078` → `requireArgoCDClusterProxyToken` `:1834-1872`) with no RBAC and no namespace filter in the chain. The chart already runs cert-manager for the public listener (`deploy/chart/templates/tls-issuer.yaml:11-20`, `tls-certificate.yaml:13-32` with `duration`/`renewBefore`).

**Why it matters** — **[corrected — the originally stated attack is overstated]** `deploy/chart/templates/networkpolicy.yaml:94-115` is an **ingress** control: it governs who may connect to :8090, not who may answer traffic addressed to the server Service. A mislabelled or compromised pod in the ArgoCD namespace therefore gains the ability to *call* the proxy (where it still needs a valid cluster-scoped token), not to impersonate the listener. Impersonation additionally requires DNS/ARP hijack or CNI-level compromise. Also, an adversary who has compromised the argocd-application-controller/argocd-server pods can already read the same token from ArgoCD's own cluster Secret, so the marginal exposure is the on-path/service-hijack case only. **Report this as a certificate-lifecycle and defence-in-depth gap with a self-documented follow-up, not an authentication bypass.** The unrotated per-pod key with no expiry policy is a real lifecycle gap in a product that otherwise delegates to cert-manager.

**Remediation**
1. Add `deploy/chart/templates/internal-proxy-certificate.yaml`: a cert-manager `Certificate` reusing `astronomer.tls.issuerName`, dnsNames = the server Service DNS names already listed at `internal/server/server.go:2028`, writing to `<fullname>-internal-proxy-tls`. **Must be stable across replicas** — that is precisely why pinning is impossible today.
2. Mount the Secret into the server Deployment (`deploy/chart/templates/server-deployment.yaml`) and add `internal_proxy_tls_cert_file` / `internal_proxy_tls_key_file` / `internal_proxy_ca_file` to `internal/config/config.go`.
3. In `StartInternalArgoCDProxy` (`internal/server/server.go:1994-2052`), load the mounted pair when configured; keep `GenerateSelfSignedCertKey` as the dev fallback. **[corrected]** Do **not** hard-fail production on the self-signed path immediately — that would break existing installs on upgrade. Warn first, then flip in a later release.
4. In `managedClusterCredential` (`internal/worker/tasks/argocd_auto_register_cluster.go:638-650`), read the CA PEM from the mounted `ca.crt` and return `&argocdclient.TLSClientConfig{Insecure: false, CAData: caPEM}`, falling back to `Insecure: true` only when no CA is configured.
5. Keep an `argocd_internal_proxy_insecure_tls` escape hatch refused under `cfg.Production`, mirroring `internal/handler/remoteproxy/proxy.go` `TLSOptions.Validate`.

**Tests** — `internal/worker/tasks/argocd_auto_register_cluster_test.go::TestManagedClusterCredentialPinsCA` — with a configured CA bundle, `Insecure=false` and non-empty `CAData` (today unconditionally `Insecure: true`). `internal/server/server_test.go::TestStartInternalArgoCDProxyUsesConfiguredCert` — the listener presents the chart-provided cert; a client pinned to the chart CA completes the handshake, an unpinned self-signed peer is rejected. Chart test: `helm template` renders the Certificate and the Deployment mounts it.

**Acceptance**
- [ ] The internal listener uses a chart-managed, rotating, replica-stable certificate.
- [ ] The worker pins the CA rather than skipping verification.
- [ ] Existing installs upgrade with a warning, not a hard failure.

### [gitops-decrypt-silent-ciphertext-fallback] GitOps sync returns raw ciphertext as the git credential when decryption fails

**Status:** PENDING APPROVAL · **Severity** low · **Dimension** crypto-tls · **Effort** S

**Evidence** — `internal/worker/tasks/gitops_sync.go:789-797`: `func decryptGitAuth(blob string) string { if gitopsDeps.Decryptor == nil || blob == "" { return blob }; if plaintext, err := gitopsDeps.Decryptor.Decrypt(blob); err == nil { return plaintext }; return blob }` — the error branch discards `err` and returns the ciphertext. Callers use it directly as a credential: `:769` `&githttp.BasicAuth{Username: "astronomer-gitops", Password: decryptGitAuth(src.AuthEncrypted)}` and `:774` `gitssh.NewPublicKeys("git", []byte(decryptGitAuth(src.AuthEncrypted)), "")`. The sibling ArgoCD path is hardened the other way (`internal/handler/argocd.go:2046-2056` logs the failure and sends no bearer token). The write side encrypts on create and update (`internal/handler/gitops.go:250-256`, `:344-350`), and `gitops_registration_sources.auth_encrypted` is in keyrotate's rewrite targets (`cmd/keyrotate/main.go:169`), so a decrypt failure means key loss or a keyrotate miss.

**Why it matters** — **[corrected — blast radius is smaller than originally stated]** The ssh_key branch already fails loudly (`gitssh.NewPublicKeys` cannot parse a Fernet token; `buildGitAuth` returns `parse ssh key: ...` at `:775-777`), so **only the https_token branch is silent**. What leaks there is a Fernet **ciphertext**, not the plaintext PAT — the git host cannot decrypt it — so the honest impact is a **misdiagnosed sync failure**: an at-rest-encryption break presenting as a generic git auth error, with no metric distinguishing "credential is wrong" from "our encryption is broken". That matters because keyrotate is the only thing re-encrypting this column and a partial run should be loud.

**Remediation**
1. Change `decryptGitAuth` to `func decryptGitAuth(blob string) (string, error)`.
2. **[corrected — the passthrough is load-bearing]** The doc comment at `:784-788` states the nil-Decryptor passthrough exists for legacy plaintext rows. Returning an error unconditionally on a non-nil Decryptor will break any pre-encryption plaintext row. Either distinguish "not a Fernet token at all" (passthrough) from "a Fernet token that will not decrypt" (error), or pair the change with a backfill check.
3. Propagate the error at the two call sites in `gitAuthMethod` (`:769`, `:774`) as `fmt.Errorf("source %q: decrypt auth blob: %w", src.Name, err)`.
4. Add `astronomer_gitops_auth_decrypt_failures_total{source}` next to the gauges in `updateSourceGauges` (`:799-808`) and a Prometheus rule in `deploy/chart/templates/prometheus-rules.yaml` — this is the canary for an incomplete key rotation.

**Tests** — `internal/worker/tasks/gitops_sync_test.go` — a Decryptor whose `Decrypt` always errors plus an `https_token` source with a non-empty `auth_encrypted`: assert `gitAuthMethod` returns an error naming the source AND the returned `githttp.BasicAuth` is nil (today it returns a BasicAuth whose Password is the ciphertext). Second case: `Decryptor == nil` passes the raw value through unchanged. Third: a legacy plaintext row still authenticates.

**Acceptance**
- [ ] A decrypt failure produces a diagnosable error, not a ciphertext credential.
- [ ] Legacy plaintext rows still work.
- [ ] A metric distinguishes decryption failure from auth failure.

### [keyrotate-dex-cutover-offset-not-advanced] The Dex public-clients cutover never advances OFFSET on the write path

**Status:** PENDING APPROVAL · **Severity** low · **Dimension** crypto-tls / code-health · **Effort** S

**Evidence** — `cmd/keyrotate/main.go:495-497`: `if dryRun { offset += len(batch) }` — the offset advances only in dry-run. On the real path the loop relies on rows leaving the `WHERE public_clients_cutover_at IS NULL` predicate (`:440`); every failure branch (`:463-465` unmarshal, `:472-474` encrypt, `:481-483` exec, `:486-489` zero rows affected) does `result.failed++; continue`, leaving the predicate satisfied, so those rows are re-selected at the same offset. The only exit is `if len(batch) < batchSize { break }` (`:498`). Separately, `--batch-size` is documented as "rows per transaction (default 100)" (`:62`) but `rewriteColumn` issues one autocommit `db.ExecContext` per row (`:274`) with no `BeginTx` anywhere in the file.

**Why it matters** — **[corrected — narrower than described]** `dex_settings` is a singleton in practice (migration 023 documents "the singleton dex_settings row"; migration 137 hardcodes `id='00000000-0000-0000-0000-000000000001'`), so with the default `--batch-size 100` the `len(batch) < batchSize` check always breaks on the first pass and the infinite loop cannot occur. The hang requires an operator to pass `--batch-size 1` (or any value ≤ the number of persistently failing rows), e.g. with malformed public_clients JSON. If it does hang, the operator is mid-rotation with two keys configured and no signal about whether it is safe to drop the old one, because the runbook's "only after this command exits 0" gate (`:10-12`) never resolves. **The misdocumented `--batch-size` is the part most likely to bite**, since an operator reading "rows per transaction" reasonably believes a crash rolls back a partial batch.

**Remediation**
1. In `cutoverDexPublicClients` (`:433-504`), move `offset += len(batch)` out of the `if dryRun` guard so it always advances. The `IS NULL` predicate plus `ORDER BY id` already drops successful rows, and advancing past failures is correct since they are counted in `result.failed` and force a non-zero exit (`:147-149`).
2. Reword the `--batch-size` help at `:62` to "rows per SELECT page".
3. Add to the package doc (`:1-21`): each row is committed independently; a crash mid-run is safe because both keys remain configured; `verifyPrimaryOnly` (`:506-521`) is the completion gate.

**Tests** — `cmd/keyrotate/rewrite_test.go` — with sqlmock, a cutover SELECT returning a full `batchSize` page whose UPDATEs all report 0 rows affected: assert `cutoverDexPublicClients` returns after exactly two SELECTs with `failed == batchSize` rather than looping. Today the test would hang.

**Acceptance**
- [ ] A page of persistently failing rows terminates with a non-zero exit.
- [ ] `--batch-size` help and the package doc describe the actual transaction semantics.

### [argocd-repo-encrypt-and-discard] Dead encrypt-and-discard of git repo password and SSH key

**Status:** PENDING APPROVAL · **Severity** low · **Dimension** code-health / crypto-tls · **Effort** S

**Evidence** — `internal/handler/argocd.go:3317-3331` in `CreateRepo`: a comment claiming "Defense-in-depth: round-trip the secret through the Encryptor so it lands in our request audit (when we add one) ciphered", then `if _, err := h.encryptor.Encrypt(req.Password); err != nil { ... }` and the same for `req.SSHPrivateKey` — both ciphertexts discarded, the only observable effect a Warn on failure. The audit record at `:3338-3346` correctly records only `has_password`/`has_private_key` booleans. Plaintext is then handed to `client.CreateRepository(r.Context(), req.toClient())` (`:3333`).

**Why it matters** — A reviewer reasonably concludes the credential is protected at this boundary; it is not. It burns two Fernet encryptions per call, and the dangling "(when we add one)" invites someone to later log the ciphertext into an audit row — a Fernet token in `audit_logs`, a table deliberately **not** in keyrotate's `rewriteTargets` (`cmd/keyrotate/main.go:163-179`), would silently become undecryptable on the next rotation. No security consequence today.

**Remediation**
1. Delete `internal/handler/argocd.go:3319-3331` (the whole `if h.encryptor != nil { ... }` block).
2. **[corrected]** Do **not** look for a copy in `TestRepo` — `internal/handler/argocd.go:3395-3418` decodes, validates the repo URL, and calls `client.TestRepository` directly; there is nothing to remove there.
3. If credential-bearing request auditing is genuinely wanted later, route the request body through `redaction.Payload` (`internal/redaction/redaction.go:31-41`), which already maps `password`/`privatekey`/`clientsecret` to `[redacted]`. Reference that in a comment instead of a TODO.

**Tests** — No new behavioral test needed (the block is provably inert). Ensure existing `internal/handler/argocd_test.go` CreateRepo/TestRepo cases pass, and add an assertion that the recorded audit metadata contains only `has_password`/`has_private_key` and never a value decryptable under the test Encryptor.

**Acceptance**
- [ ] The dead block is gone.
- [ ] The audit assertion exists.

### 7.4 Code-health backlog (no defect; structure only unless noted)

### [operation-supersede-drift-kills-inflight-ops] Four of six operation reconcilers omit `ShouldSupersede`

**Status:** PENDING APPROVAL · **Severity** low · **Dimension** code-health · **Effort** S

**Evidence** — `internal/handler/operation_runner.go:70-78` treats a nil `ShouldSupersede` as "always supersede". Only `internal/handler/monitoring.go:3122-3124` supplies it. `internal/handler/tools.go:1291`, `logging.go:1216`, `catalog.go:1931`, `workloads.go:1396` set only `IsFreshRunning`, and their feeder queries include running rows (`internal/db/queries/tool_operations.sql:28-32` and the logging/catalog/workload equivalents, `WHERE status IN ('pending','running')`). The supersede write has no status guard (`tool_operations.sql:80-88`), nor do `MarkX...Completed/Failed` (`:60-68`). `operationstate.IsRetryable` includes `'superseded'`.

**Why it matters** — **[corrected — the originally claimed failure path is largely unreachable, and the "double Helm apply" reasoning is wrong]** `processPendingOperations` → `dispatchClaimed` ends in `wg.Wait()` (`reconciler_dispatch.go:67`), and `runReconciler` (`tools.go:337-350`) is a single goroutine selecting on ticker/trigger, so no second tick runs while an op executes; under multi-replica the reconcilers are leader-elected (`server.go:1705-1710`, `runServerReconcilerLeader` at `:100`). A `running` row seen by a tick is therefore either from the blocked current batch (impossible) or genuinely dead after a crash — in which case superseding it is correct. Residual exposure is only the transient dual-leadership window the code itself acknowledges (`server.go:1693-1695`). Also, the newer pending op is claimed and dispatched regardless of whether the older row was superseded — monitoring, which *has* the guard, behaves identically — so supersede is not what causes concurrent applies. **What is genuinely worth fixing is narrow and cheap.**

**Remediation** — **[corrected — the proposed `RunningLease` redesign of `operationRunnerConfig` is disproportionate; do not do it]**
1. Add `AND status IN ('pending','running')` to the `MarkX...Superseded` queries (`tool_operations.sql:80-88` and the logging/catalog/workload/monitoring/argocd equivalents) so a losing writer no-ops.
2. Add `AND status = 'running'` to the `MarkX...Completed/Failed` queries so a terminal row cannot be resurrected.
3. Stop treating a still-running op as retryable in `operationstate.IsRetryable`.

**Tests** — sqlc-level: `MarkToolOperationSuperseded` on an already-`completed` row returns `pgx.ErrNoRows`; `MarkToolOperationCompleted` on a `superseded` row likewise. Handler-level: a `running` op is not offered for retry.

**Acceptance**
- [ ] A losing writer cannot flip a terminal operation.
- [ ] A running operation is not retryable.

### [argocd-go-3430-line-split] `internal/handler/argocd.go` (3,430 lines) bundles four subsystems

**Status:** PENDING APPROVAL · **Severity** low · **Dimension** code-health · **Effort** L
*(Pure structure recommendation. No defect, no behavior change. Reject cheaply if the owner prefers.)*

**Evidence** — Four concerns with almost no cross-references: (1) instance CRUD + auth-token handling + client construction (`:119-236, 393-605, 2026-2063, 2565-2618`); (2) the async sync-operation engine (`:345-392, 1108-1270, 1336-1530, 1438-1830` incl. `claimPendingArgoCDOperations:1452`, `executeSync:1627`, `pollRunningOperations:1832`); (3) managed-cluster registration, label reconciliation, and Argo cluster-Secret plumbing (`:2124-2562`, `:2963-3271`) — ~700 lines of Kubernetes Secret/label/JWT logic importing corev1 + client-go into the handler package; (4) thin Argo API passthrough CRUD (`:2619-3423`).

**Why it matters** — Second-largest backend file and the place most active feature work lands, so a standing merge-conflict hotspot. **The one substantive sub-claim worth keeping:** the cluster-Secret/label/JWT helpers can only be exercised through an HTTP handler, which is why `jwtExpiry:2457` and `argoCDClusterTokenExpiry:2444` have no direct unit coverage.

**Remediation**
1. `internal/handler/argocd_instances.go` — struct, `ArgoCDQuerier`, constructors, `instanceResponse`/`decryptInstanceToken`/`resolveAuthToken`, instance CRUD, `InstanceHealth`, `loadInstance`, `instanceHTTPClient`/`argoCDClient`, `translateClientError`, `recordArgoAudit` (`:42-236, 312-344, 393-605, 789-816, 937-972, 2026-2063, 2565-2618`).
2. `internal/handler/argocd_operations.go` — the sync-operation engine (`:345-392, 973-1025, 1108-1270, 1336-1530, 1571-1830, 2064-2123`).
3. `internal/handler/argocd_managed_clusters.go` — registration handlers (`:2124-2562, 2963-3271`). **Move the pure-Kubernetes helpers** (`clusterSecretNameFromServer`, `findArgoCDClusterSecretByServer`, `mergeAstronomerManagedLabels`, `astronomerManagedLabelsPatch`, `stringMapEqual`, `argoCDClusterTokenExpiry`, `jwtExpiry`, `decodeBase64URL`) into `internal/argolabels` or a new `internal/argosecret` package so they lose the handler dependency.
4. `internal/handler/argocd_resources.go` — Application/Project/ApplicationSet/Repo passthrough CRUD and DTOs (`:2619-3423`). Target: no file over ~950 lines.

**Tests** — No behavior change. Gate is the existing suite plus new direct coverage the move enables: `internal/argosecret/secret_test.go` covering `jwtExpiry` (expired / absent-exp / malformed) and `mergeAstronomerManagedLabels` preserving non-astronomer labels. `go test ./internal/handler/ ./internal/argosecret/ -count=1` and `go test ./internal/server/ -run RouteTable`.

**Acceptance**
- [ ] No route moved (golden route table green).
- [ ] The extracted helpers have direct unit tests.

### [crd-controller-2919-line-split] `internal/crd/controller.go` (2,919 lines) holds six reconcilers plus all validation

**Status:** PENDING APPROVAL · **Severity** low · **Dimension** code-health · **Effort** M
*(Pure structure recommendation. No defect. `New` is only ~110 lines, so the "wiring and bodies in one blast radius" argument is weaker than originally stated.)*

**Evidence** — Six reconcilers with disjoint types: ClusterReconciler (`:290-477`), ProjectReconciler (`:486-613`), ClusterBaselineReconciler (`:628+`, `:931-1250`, `:1511-1578`), ComponentBundleReconciler (`:685+`, `:1579-1853`), AgentProfileReconciler (`:725+`, `:1872-2057+`), GitOpsTargetReconciler (`:767-897`). Genuinely shared: `reconcileSimpleFinalizer:905`, `pollOrDefault:898`, the Argo ApplicationSet upsert/hash/ownership trio (`:1251, 1282, 1286, 1294`), the label/annotation builders (`:1331-1372`), and the Argo rollup/rank helpers (`:1380-1502`).

**Remediation** — Keep `controller.go` for `New` + `ControllerConfig` + the shared interfaces (`:47-178`); split into `reconciler_cluster.go`, `reconciler_project.go`, `reconciler_clusterbaseline.go`, `reconciler_componentbundle.go`, `reconciler_agentprofile.go`, `reconciler_gitopstarget.go`, plus `argoappset.go` (`:1251-1372`) and `argorollup.go` (`:1373-1502`). Those two are the only symbols with more than one reconciler as a caller; everything else is single-owner. No file over ~450 lines.

**Tests** — Pure move: `go build ./... && go test ./internal/crd/... -count=1 -race` unchanged. Add `internal/crd/ownership_test.go` asserting via `go/ast` that no `reconciler_*.go` file declares a symbol referenced from a different `reconciler_*.go` file, so the file cannot silently re-merge.

**Acceptance**
- [ ] Test suite unchanged and green.
- [ ] The seam guard test exists.

### [parse-cluster-id-helper-ignored-87-times] An existing `parseClusterID` helper is used 11 times; the same preamble is hand-rolled everywhere else

**Status:** PENDING APPROVAL · **Severity** low · **Dimension** code-health · **Effort** M

**Evidence** — `internal/handler/image_vulns.go:474-481` defines `func parseClusterID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool)` and it has 11 call sites across 4 files. `uuid.Parse(chi.URLParam(r, "cluster_id"))` appears 44 more times across 18 files (cluster_snapshots.go ×7, agent_fleet.go ×6, network_policies.go ×4, cluster_templates.go ×4, control_plane_snapshots.go ×3, cluster_registries.go ×3, argocd.go ×3, ...), and the literal `RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")` appears 87 times. `json.NewDecoder(r.Body).Decode(&req)` appears 122 times with `apierror.InvalidBody, "Invalid JSON body"` ~95 times and no shared helper. No global request-body size cap exists for the REST handlers (`MaxBytesReader` appears only in `internal/tunnel` proxy paths).

**Why it matters** — ~350 lines of boilerplate and 87 places an error-catalog change must be applied by hand. **[corrected]** No inconsistency was actually found — no site got the status or error code wrong — so "87 chances to get it wrong" is speculative. The concrete win is the missing body-size cap that a shared decode helper would add everywhere at once.

**Remediation**
1. Create `internal/handler/request_params.go` with `parseClusterID` moved out of `image_vulns.go:474`, plus `parseUUIDParam(w, r, name, label string) (uuid.UUID, bool)` and `decodeJSONBody[T any](w http.ResponseWriter, r *http.Request, dst *T) bool` — the latter wrapping the decode sites with the canonical `apierror.InvalidBody` response **and an `http.MaxBytesReader` cap**, which hand-rolled sites lack.
2. Mechanically convert the 44 `uuid.Parse(chi.URLParam(...))` sites, starting with the density leaders.
3. Convert the JSON-decode sites in the same pass. Expected removal: ~330 lines across ~30 files.
4. **Do NOT extract an authorization helper in the same change** — authz preambles differ meaningfully per route and merging them would hide real policy.

**Tests** — `internal/handler/request_params_test.go` covering `parseUUIDParam` (valid / malformed / missing → 400 + `apierror.InvalidID`) and `decodeJSONBody` (valid / malformed → 400 + `apierror.InvalidBody` / oversize → 413). Then a convention gate: extend `scripts/code-health-inventory.mjs` with a Go hard gate failing when `uuid.Parse(chi.URLParam(` or `json.NewDecoder(r.Body).Decode(` appears in `internal/handler/*.go` outside `request_params.go`.

**Acceptance**
- [ ] All REST JSON bodies are size-capped.
- [ ] The convention gate prevents regression.
- [ ] No authorization logic was folded into a shared helper.

### [tool-cluster-status-n-plus-1] `ClusterStatus` issues one DB round-trip per enabled tool

**Status:** PENDING APPROVAL · **Severity** low · **Dimension** code-health · **Effort** S

**Evidence** — `internal/handler/tools.go:649-689`: `ListEnabledTools` (unbounded — `internal/db/queries/tools.sql:10-11`, no LIMIT) then a loop (`:677-689`) calling `h.queries.GetLatestToolOperationForTarget` per tool (`internal/db/queries/tool_operations.sql:34-38`). The index exists (`internal/db/migrations/009_tool_operations.up.sql:20-21`), so each query is cheap; the cost is round-trip count. The in-code comment at `:671-675` documents this as a deliberate replacement for a prior global scan. `ListInstalledChartsByCluster` right above is capped at 200 while the tool list is not.

**Why it matters** — **[corrected]** N is the number of operator-enabled tools — realistically tens, not hundreds — and each query is index-backed, so the honest impact is tens of milliseconds of avoidable latency on one page, not a scaling defect.

**Remediation**
1. Add to `internal/db/queries/tool_operations.sql`: `-- name: ListLatestToolOperationsForTargets :many` / `SELECT DISTINCT ON (target_key) * FROM tool_operations WHERE target_type = $1 AND target_key = ANY($2::text[]) ORDER BY target_key, created_at DESC;`
2. `make sqlc`; add the method to the `ToolQuerier` interface in `internal/handler/tools.go` (~`:56`).
3. Rewrite `:676-689` to build the target-key slice once, issue the single query, and filter to pending/running in Go exactly as today.
4. **[corrected — do not paginate `ListEnabledTools`]** The endpoint's contract is "status for every enabled tool"; `LIMIT/OFFSET` would silently truncate the status page. Prefer a hard cap with an explicit signal, or leave the list unbounded and just batch the operation lookup.
5. Apply the same `DISTINCT ON` pattern to `internal/handler/network_policies.go:452-455` and `internal/handler/projects.go:1104-1106` if they show up on hot paths; `projects.go:926-929` and `argocd.go:660-663` are cache-guarded and can be left alone.

**Tests** — `internal/handler/tools_operation_test.go` — a fake `ToolQuerier` counting calls; 25 enabled tools; assert `GetLatestToolOperationForTarget` is called 0 times and `ListLatestToolOperationsForTargets` exactly once, with a byte-identical response body. Fails today (25 calls). Plus a sqlc-level test that `DISTINCT ON` returns the newest row per `target_key`.

**Acceptance**
- [ ] One query regardless of tool count.
- [ ] Response body unchanged.

### [api-hooks-flat-2700-line-modules] `api.ts` (2,763 lines) and `hooks.ts` (2,216 lines) are flat catch-alls with a stalled, already-sanctioned split target

**Status:** PENDING APPROVAL · **Severity** low · **Dimension** code-health · **Effort** M

**Evidence** — `frontend/src/lib/api.ts` is 2,763 lines of ~400 four-line fetch wrappers with banner comments as the only structure. `frontend/src/lib/hooks.ts` is 2,216 lines with 151 `export function use*` declarations and 30+ banner separators. The split target already exists and is already blessed: `scripts/code-health-inventory.mjs:93-97` allows direct fetch/axios in `frontend/src/lib/api.ts` **or** anything under `frontend/src/lib/api/`, and that directory already holds ~20 feature modules (account-security, admin-operations, cluster-detail, cluster-groups, cluster-snapshots, dashboards, extensions, native-rbac, settings, vault, ...) with colocated tests. The migration was started and stalled with the bulk still in the monolith.

**Why it matters** — Two files nearly every frontend change touches, at the top of the conflict list for parallel branches. **[corrected]** The tree-shaking argument is weak — these are route-level chunks in a Vite build where the api module graph is shared anyway. The useful observation is that the target directory and the gate allowance already exist, so continuing is a mechanical move, not a redesign.

**Remediation**
1. Move each banner-delimited block of `api.ts` into the matching `frontend/src/lib/api/<feature>.ts`; reduce `api.ts` to the axios instance + interceptors + `export * from './api/*'` so no call site changes.
2. Mirror for `hooks.ts` into `frontend/src/lib/hooks/<feature>.ts`, with `hooks.ts` re-exporting.
3. **[corrected — do not use a declining line-count gate on two named files; it is trivially gamed by adding a third monolith]** Instead add a gate requiring **new exported API functions to live under `frontend/src/lib/api/`**, which enforces the actual intent.

**Tests** — `cd frontend && npm run type-check && npm test && npm run build` green; `git diff --exit-code src/routeTree.gen.ts` clean (existing drift step in `scripts/verify-enterprise.sh`). Compare `npm run build` chunk sizes before/after for the largest route chunk to confirm the barrel does not defeat tree-shaking.

**Acceptance**
- [ ] Bulk of `api.ts`/`hooks.ts` lives in feature modules.
- [ ] A new API function cannot be added to the monolith.

### [dockerignore-omits-large-build-artifacts] `.dockerignore` does not exclude `bin/`, `screenshots/`, `keyrotate`, `worker`

**Status:** PENDING APPROVAL · **Severity** low · **Dimension** code-health · **Effort** S

**Evidence** — `.dockerignore` lists only node_modules, .next, `**/node_modules`, `**/.next`, out, .env, .env.*, .git, .gitignore, *.md (with a content/ carve-out), coverage. The repo root holds `keyrotate` (26M), `worker` (125M), `bin/` (297M), `screenshots/` (73M) — all uncommitted (`git ls-files` and `git log --all` return nothing for them). Note only `bin/` is actually in `.gitignore` — `keyrotate`, `worker`, and `screenshots/` are merely untracked, so they show up as noise in `git status` and one `git add -A` commits 150 MB+. `Makefile:131-146` builds server/agent/worker/migrate/shell with context `.` and `deploy/docker/Dockerfile.server:11` does `COPY . .`.

**Why it matters** — Local-developer tax only; CI checks out fresh so the paths do not exist. `make docker-build-all` sends ~470MB of stale binaries and PNGs to the daemon five times, and `COPY . .` busts the layer cache whenever any of them changes.

**Remediation**
1. Add `bin/`, `screenshots/`, `frontend/dist`, `/keyrotate`, `/worker`, `/server`, `/agent`, `*.test`, `coverage.out`, `.worktrees/`, `.claude/` to `.dockerignore`. This additive fix is the safe first step.
2. *(Optional, riskier)* Invert to a denylist-of-everything plus explicit allows for the five Go images. **[caution]** The Go images also embed assets — `internal/handler/assets/openapi.yaml` is embedded and byte-compared by the verify gate — so any allowlist must keep the embed directories or the build silently loses them.
3. Leave `frontend/Dockerfile` alone (context `frontend`, already covered by `**/node_modules`).

**Tests** — Measurable rather than unit: `docker build -f deploy/docker/Dockerfile.server -t t .` with `bin/` and `worker` present; confirm the "transferring context" size drops from hundreds of MB to single-digit MB. Optionally add a `make docker-context-size` target and a CI budget assertion.

**Acceptance**
- [ ] Build context is single-digit MB with artifacts present.
- [ ] Embedded assets still present in the built image (verify gate green).

### [duplicate-alertmanager-renderer] Alertmanager config rendering is duplicated across two handlers and has drifted

**Status:** PENDING APPROVAL · **Severity** low · **Dimension** code-health · **Effort** M

**Evidence** — `internal/handler/alerting.go:1311-1388` `renderAlertmanagerConfig` and `internal/handler/monitoring.go:2314-2390` `renderSharedAlertmanagerConfig` are near-identical ~75-line copies; the second's own comment says it mirrors the first (`:2318-2320`). They have diverged: `alerting.go:1345-1353` has separate `case "slack":` and `case "webhook":` arms while `monitoring.go:2350` collapses them; `alerting.go:1370` threads configurable timings via `h.alertmanagerTiming(ctx)` (defined `alerting.go:142`) while `monitoring.go:2377-2382` hardcodes `30s`/`5m`/`3h` with a comment admitting "monitoring stack render does not currently thread SettingsCache". The rule↔channel link-loading blocks (`alerting.go:1316-1334`, `monitoring.go:2320-2338`) are byte-identical.

**Why it matters** — **[corrected]** As a standalone finding this is maintainability. Its only user-visible half — platform-settings `group_wait`/`group_interval`/`repeat_interval` never reaching the running Alertmanager — is a consequence of the fact that the copy honouring those settings writes an inert ConfigMap. **Fix together with `alertmanager-routing-configmap-inert`; this consolidation is the enabling refactor, not an independent defect.** Both copies also carry the pagerduty/msteams and SMTP defects, which is the multiplication cost.

**Remediation**
1. Move the renderer into `internal/notify/alertmanager.go`: `func RenderConfig(channels []sqlc.NotificationChannel, rules []sqlc.AlertRule, links map[uuid.UUID]map[uuid.UUID]bool, opts Options) (string, error)` where `Options` carries groupWait/groupInterval/repeatInterval and the SMTP globals.
2. Reduce both handler methods to loading channels/rules/links + Options and delegating.
3. Thread `SettingsCache` into `MonitoringHandler` so timings come from the same source on both paths.
4. Collapse the byte-identical link-loading into the same helper.

**Tests** — `internal/notify/alertmanager_test.go::TestRenderConfigGolden` with a golden YAML fixture covering all five channel types, custom timings, and SMTP globals. `TestHandlersShareRenderer` in `internal/handler` — render the same channel/rule set through both handlers and assert byte-identical output; fails today because of the group_wait divergence.

**Acceptance**
- [ ] One renderer, two callers.
- [ ] Both paths honour platform-settings timings.
- [ ] Shipped together with the inert-ConfigMap fix.

---

## 8. Suggested execution order

Dependencies are the reason for the sequence; within a stage, items are independent unless noted.

**Stage 0 — ship first, no dependencies, all small.**
1. `monitoring-go-3815-line-split` **Part A only** (unauthenticated `/settings/monitoring/*`). One-file change; do not wait for the refactor.
2. `agent-ingest-token-shared-service-user-cross-cluster-rce` items 1+2 together.
3. `resources-group-version-kind-route-bypasses-per-resource-rbac` (delete the route).
4. `dev-keys-default-and-silent`.
5. `argocd-verify-ssl-fail-open` (steps 1-4 in one change — step 4's `instanceHTTPClient` fix is required or the rest is inert).

**Stage 1 — RBAC semantics. Order matters within this stage.**
6. `exec-logs-verb-mismatch` — includes the role-catalog migration. **Must land before** `kubectl-shell-ws-reopen-skips-rbac-when-scope-flag-off`, which re-checks the same verb.
7. `k8s-proxy-proxy-subresource-not-gated-nodes-proxy-is-kubelet-rce` — also needs a role-catalog migration; **coordinate with (6) so the two migrations do not conflict**.
8. `role-update-no-escalation-guard`.
9. `cluster-project-list-unfiltered` — step 1 (gate relaxation) first, then the scoped SQL.
10. `kubectl-shell-ws-reopen-skips-rbac-when-scope-flag-off` (after 6).
11. `ui-proxy-permission-mapping-ignores-argocd-verbs` — decide project-binding scope semantics here; that decision is shared with (12).
12. `project-bindings-inert-by-default` — **blocked on** `namespace-filtered-watch-local-path-filters-raw-chunks` for the default flip (step 3). Land steps 1-2 and 4 first; flip the flag after (13).
13. `namespace-filtered-watch-local-path-filters-raw-chunks`.

**Stage 2 — agent stability. Order matters: 14 makes 15 and 16 safe to exercise.**
14. `send-failclose-livelock` item 1 (restrict `failClose` to control frames) — do this before anything that increases frame volume.
15. `unbounded-handler-goroutines`.
16. `heartbeat-full-cluster-list-storm`.
17. `informer-full-object-caches` — reduces the OOM pressure that 15 also targets.
18. `self-upgrade-no-verification-no-rollback` — **land before any fleet-wide agent rollout of stages 2-3**, otherwise a bad agent image during this work takes clusters dark.
19. `agent-no-read-idle-deadline`.
20. `exec-and-log-binary-corruption` — agent and both tunnel consumers in the same release.
21. `send-failclose-livelock` items 2-5; `agent-observability-gap` (the metrics it adds are the ones item 5 needs).

**Stage 3 — ArgoCD.**
22. `baseline-appset-floating-chart-version`.
23. `leave-local-ownership-exclusion-fails-open` (re-verify first).
24. `argocd-authz-anchored-to-management-cluster`.
25. `appproject-scoping-absent` — the defence-in-depth layer under (24); lower value if (24) is done well, so sequence after.
26. `decommission-wedges-baseline-applications`; `clusterbaseline-gitopstarget-target-management-cluster`; `cluster-ca-never-persisted` (steps 1-3 only).

**Stage 4 — gates, then the features and refactors they protect.**
27. `golangci-lint-never-runs-in-ci`; `no-request-schema-field-gate`; `api-ts-shadow-generated-types`. **Do these before the large refactors** so the refactors are gated.
28. `drain-force-field-never-reaches-ui` (uses 27's gate to prove itself).
29. `catalog-sync-oci-git-blind-and-abort`; `git-backed-chart-repos-accepted-never-synced` (reject option — same file).
30. Alerting cluster, in one sequence because they overlap the same two renderers: `duplicate-alertmanager-renderer` → `alertmanager-drops-pagerduty-msteams` → `alertmanager-email-missing-smtp-globals` → `alertmanager-routing-configmap-inert`.
31. `cis-ingest-in-process-goroutine-loses-scans` (option b) → `no-scheduled-cis-scans` (needs the `startScan` extraction).
32. `monitoring-stack-lifecycle-no-ui` — **after** stage 0 item 1, since the backend-config editor must not be built against unauthenticated routes. `per-cluster-observability-ia-gap` follows it.
33. `no-release-history-or-revision-rollback-ui`; `loki-log-query-not-surfaced`.

**Stage 5 — long-tail parity and structure, any order.**
34. `no-downstream-impersonation` — largest single item; treat as its own project. Sequence after stage 1 so the control-plane authz it backstops is already correct.
35. `group-membership-only-refreshed-at-login`; `api-token-no-max-ttl`; `project-quota-no-aggregate-accounting`; `no-default-role-no-creator-owner`.
36. `split-brain-across-server-replicas` (items 1, 3, 4 only); `no-decommission-signal-to-offline-agent`; `tunnel-rate-limits-keyed-on-spoofable-ip-and-run-before-auth`; the tunnel2 decision.
37. Structure backlog: `monitoring-go-3815-line-split` Part B, `newapp-1600-line-constructor`, `argocd-go-3430-line-split`, `crd-controller-2919-line-split`, `resource-table-copypaste-2855-line-route`, `api-hooks-flat-2700-line-modules`, `parse-cluster-id-helper-ignored-87-times`.
38. Cheap independents that can be slotted anywhere: `permission-decision-unmemoized-defeats-every-usememo`, `tool-cluster-status-n-plus-1`, `dockerignore-omits-large-build-artifacts`, `argocd-repo-encrypt-and-discard`, `keyrotate-dex-cutover-offset-not-advanced`, `gitops-decrypt-silent-ciphertext-fallback`, `operation-supersede-drift-kills-inflight-ops`, `no-drift-exception-mechanism`, `argocd-internal-proxy-tls-insecure-skip-verify`.

---

## Appendix A — refuted claims

Do not re-raise these. They were investigated and rejected as reportable defects.

### [no-applicationset-rollout-strategy] "No progressive rollout: ApplicationSets fan out to the entire fleet at once"

**Refuted.** The factual observation is true — there is no `strategy` key in `internal/server/baseline_appsets.go:405-466`, `internal/crd/controller.go:1117-1135`, or `:2287-2320`, and no `RollingSync`/`maxUpdate`/`progressiveSync` anywhere in `internal/`. But it is a feature request framed entirely through the **deliberately-excluded Fleet-parity dimension**: the whole rationale and the reference citation are Rancher's `fleet.RolloutStrategy`/`BundleTarget` on ManagedChart, and astronomer uses ArgoCD instead of Fleet on purpose. Nothing in the shipped code misbehaves, and the concrete blast-radius risk it argues (a bad chart hitting the whole fleet) is already fully captured by `baseline-appset-floating-chart-version`.

The proposed remediation is also **wrong as written**: ArgoCD ApplicationSet progressive syncs are an opt-in alpha feature gated by `ARGOCD_APPLICATIONSET_CONTROLLER_ENABLE_PROGRESSIVE_SYNCS` (`deploy/chart/charts/argo-cd-9.5.21.tgz` → `argo-cd/templates/argocd-applicationset/deployment.yaml:157`, sourced from the `argocd-cmd-params-cm` key `applicationsetcontroller.enable.progressive.syncs`), and `rg progressive deploy/` finds no setting for it — so emitting `spec.strategy` would be **silently ignored** by the shipped controller.

### Claims corrected during verification (kept, but with the wrong half removed)

These were not refuted outright, but a specific sub-claim was wrong and has been removed from the body above. Listed here so a future reader does not reintroduce them from an older draft:

| Finding | Removed sub-claim |
|---|---|
| `alertmanager-drops-pagerduty-msteams` | "Operator gets zero pages." The in-process evaluator delivers PagerDuty and Teams natively. |
| `alertmanager-routing-configmap-inert` | "Alerts route to null until someone re-runs the upgrade." Channel changes do take effect for astronomer-rule notifications. |
| `alertmanager-email-missing-smtp-globals` | "Takes down ALL alert delivery." Only the shared Alertmanager release and Alertmanager-routed delivery break. |
| `tunnel-rate-limits-keyed-on-spoofable-ip...` | "One busy operator 429s the whole tenant." `RealIP` keys on the forwarded client IP, so buckets are per client IP. |
| `no-default-role-no-creator-owner` | "A project creator cannot see the project they created." Global bindings apply at every scope. |
| `no-downstream-impersonation` | "The agent SA is cluster-admin" and "every user action runs as it." Default profile is viewer; the kubectl-shell path already impersonates. |
| `golangci-lint-never-runs-in-ci` | `monitoring.go:1165 _ = ok` as linter evidence. Blank-identifier assignment is deliberately ignored by ineffassign/staticcheck. |
| `api-ts-shadow-generated-types` | "The spec's optional `value` is the drift." The handler does not require it; the hand-written type is the odd one out. |
| `drain-force-field-never-reaches-ui` | `resources.go:1485 Force: true` as an in-repo consumer. That is `kubeutil.ApplyOptions`, unrelated. |
| `no-drift-exception-mechanism` | "There is no workaround." `ignoreDifferences` passes `argosecurity` validation today via PATCH and the UI proxy. |
| `operation-supersede-drift-kills-inflight-ops` | "A reconciler tick supersedes an in-flight op" and "double Helm apply." Ticks are serialized by `wg.Wait()` and leader-elected. |
| `agent-no-read-idle-deadline` | "Hours, because Go has no TCP keepalive." Both dial paths get keepalives; detection is ~2.5-5 minutes. |
| `agent-observability-gap` | "Send drops are invisible." They are already exported as `astronomer_dropped_events_total{component="agent_tunnel_send"}`. |
| `resource-table-copypaste-2855-line-route` | "~1,800 lines of copy-paste." `RouteTable<T>`, `NamespacedActions`, and `GenericResourceTable` already exist; real duplication is ~500-600 lines. |
| `argocd-internal-proxy-tls...` | "A mislabelled pod in the ArgoCD namespace can impersonate the listener." The NetworkPolicy is an ingress control; impersonation needs DNS/ARP hijack. |
| `git-backed-chart-repos...`, `per-cluster-observability-ia-gap` | Impact framing downgraded; see the items. |

---

## Appendix B — coverage and limits

### What was examined

**parity-core.** Read in full or in the relevant regions: `internal/rbac/{types,engine,native,templates}.go` and all 34 `internal/rbac/templates/*.yaml`; `internal/tunnel/{proxy,nsfilter,exec_consumer,logs_consumer}.go`; `internal/agent/{k8sproxy,project_reconciler}.go`; `pkg/proxyhdr`; `internal/server/routes.go` (authz middleware 1180-1710), `routes_rbac_audit_agents.go`, `routes_clusters.go`, `internal/server/middleware/rbac_queries.go`; `internal/handler/{rbac,clusters List,projects List/Create,auth CreateToken,kubectl_shell_scope,scim,sso,stream_tickets,read_audit_policies}.go`; `internal/auth/group_sync.go`; `internal/sessionpolicy`; `internal/config/config.go`; `internal/db/queries/rbac.sql`; migrations `001` and `098`; `internal/worker/tasks/project_reconcile.go`; `deploy/agent/template.go` and `install.yaml.template`; `internal/audit/coverage_contract_test.go`. Rancher reference: `pkg/impersonation`, `pkg/clusterrouter/proxy/proxy_server.go`, `pkg/rbac/{access_control,user_based}.go`, `pkg/controllers/managementuser/rbac/prtb_handler.go`, `pkg/controllers/managementuser/resourcequota/*`, `pkg/auth/providerrefresh/daemon.go`, `pkg/auth/audit/*`, `pkg/settings/setting.go`, `pkg/systemtemplate/template.go`, `pkg/api/norman/customization/globalrole/validator.go`, `pkg/apis/management.cattle.io/v3/authz_types.go`.

**parity-features.** `internal/handler/catalog.go` (full), `catalog_oci.go`, `catalog_hydrate.go`, `catalog_installed_enriched.go`; `internal/handler/monitoring.go` (stack + shared Thanos + shared Alertmanager, renderers, operations executor, auto-rollback); `internal/handler/alerting.go` (channels, rules, events, silences, inhibitions, `syncSharedAlertingAssets`, both renderers); `internal/handler/logging.go`; `internal/handler/security.go`; `internal/handler/backups*.go` and `cluster_snapshots.go` / `control_plane_snapshots.go`; `internal/worker/tasks/{catalog_sync,security_scan}.go` in full; `internal/worker/scheduler.go`. Frontend: sidebar nav model, `routes/dashboard/{catalog,monitoring,logging,alerting,security/scans/new,clusters/$id/apps}`, `lib/api.ts` + `lib/hooks.ts`. Endpoint-to-UI coverage established by exhaustive ripgrep per backend path prefix. Rancher: `pkg/apis/catalog.cattle.io/v1/types.go` RepoSpec, `pkg/catalogv2` layout, `ui/app/router.js`.

**agent.** Full: `internal/agent/{tunnel,config,token_persistence,health,self_upgrade,decommission,exec,logs,k8sproxy,adapters,metrics,tls}.go`; `state_subscriber.go` (1-200, 280-580, 718-837); `internal/agent2/client.go`; `internal/agentcompat`; `internal/agentlifecycle`; `internal/registration/phase.go`; `cmd/agent/main.go`; `deploy/agent/install.yaml.template` (Deployment/RBAC). Server counterparts: `internal/tunnel/{server,handler,locator}.go`, `internal/tunnel2/server.go`, `internal/handler/agent_fleet.go`. Rancher: `remotedialer@v0.6.0` types/wsconn/session.

**transport-security.** Traced end-to-end (handler → route middleware → tunnel → agent → kube API): `internal/tunnel/{handler,server,proxy,stream,stream_auth,nsfilter,connect_limiter,exec_consumer,logs_consumer,internal_k8s,internal_helm,ws_owner_proxy}.go`, `internal/tunnel/connectauth/validate.go`, `internal/tunnel2/server.go`, `internal/agent/{k8sproxy,service_proxy}.go`, `internal/agent2/client.go`, `internal/netpol/render.go`, `internal/handler/{resources,k8s_requester,kubectl_shell,stream_tickets}.go`, `internal/auth/{streamauth,stream_tickets,agent_ingest_token}.go`, `internal/server/routes.go` + `routes_resources_workloads.go` + `routes_security.go`, `internal/server/middleware/{auth,argocd_authz,api_rate_limit,audit}.go`, `internal/rbac/engine.go`, `pkg/proxyhdr`, chart networkpolicy/ingress/httproute. Three traces completed in full: kubectl proxy, exec WS, agent CONNECT.

**crypto-tls.** Full: `internal/auth/{crypto,jwt,streamauth}.go`, `internal/vault/client.go`, `internal/cloudcreds/registry.go`, `internal/redaction`, `internal/agent/tls.go`, `cmd/keyrotate/main.go` + `coverage_test.go`, `internal/config/production.go`, chart secret/bootstrap/dex/tls templates, `_helpers.tpl` production preflight, `internal/tunnel/connectauth/validate.go`. Targeted: `internal/handler/argocd.go` (client construction, instance CRUD, managed-cluster registration, repo CRUD), `internal/handler/argocd/client.go`, `internal/handler/gitops.go`, `internal/worker/tasks/{gitops_sync,argocd_auto_register_cluster,siem_dispatch}.go`, `internal/server/{server,self_manage_argocd,routes}.go`, `internal/handler/{clusters,users,cluster_registries,remoteproxy/proxy,kubectl_shell}.go`, `internal/db/queries/clusters.sql`, migrations 001/093/094/102.

**argocd.** Full or in-region: `internal/handler/argocd.go` (instance CRUD, app read/sync/refresh, lifecycle CRUD), `argocd_ui_proxy.go` (all 775 lines), `argocd_project_validation.go`, `argocd_orphans.go`, `internal/handler/argocd/{client,clusters,projects,applicationsets}.go`, `internal/argosecurity/policy.go`, `internal/server/middleware/argocd_authz.go`, `internal/server/routes_tools_controlplane.go`, `internal/server/routes.go` (argocd mount, internal proxy, token gate), `self_manage_argocd.go` (all 666 lines), `self_manage_values.go`, `self_manage_migration.go`, `baseline_appsets.go` (all 488 lines), `internal/worker/tasks/{argocd_auto_register_cluster,argocd_refresh_managed_cluster}.go`, `cluster_decommission.go` argo phase, `internal/crd/controller.go` materialization/selectors/validation, `internal/crd/types.go`, `internal/baseline/registry.go`, chart argo-cd values and networkpolicy. Rancher: `managed_chart.go`.

**code-health.** Symbol maps plus targeted regions of the five oversized files; all six `claimPending*` reconcilers and their SQL; a scripted scan of all 885 sqlc declarations for missing LIMIT; `internal/agent2`, `internal/tunnel2`; `docs/package-ownership.md`, `docs/dual-tunnel-matrix.md`; `Makefile`, `scripts/verify-enterprise.sh`, `scripts/code-health-inventory.mjs` (full), all four workflows, `.gitignore`/`.dockerignore`/`.golangci.yml`; frontend `api.ts`, `hooks.ts`, `$resource/index.tsx`, `permission-hooks.ts`, `permissions.ts`, `openapi.generated.ts`, `vitest.config.ts`.

### Explicitly out of scope

Cluster provisioning and node drivers; Fleet (ArgoCD is the intentional substitute). One finding was refuted specifically because it was Fleet-framed — see Appendix A.

### Verified clean — deliberately not reported (do not re-audit)

- **Fernet at-rest scheme.** AES-128-CBC + HMAC-SHA256, encrypt-then-MAC, per-message random IV, multi-key rotation with primary-first ordering. Sound.
- **`cmd/keyrotate` coverage.** Sweeps all 15 ciphertext columns plus the Dex JSONB blobs, uses ciphertext-CAS so a concurrent server write is never clobbered, gates completion on a primary-only verification pass. The build-time `TestRewriteTargetsCoverAllEncryptedColumns` guard is real and works.
- **Agent tunnel CA pinning.** `BuildTLSConfig` (Rancher `CATTLE_CA_CHECKSUM` semantics) is fail-closed and the mounted CA is genuinely wired — contradicting the stale note in `docs/join-process-review.md`.
- **Token storage.** Agent and registration tokens are stored hash-only (sha256 over 32-byte crypto/rand); the legacy plaintext `token` columns are written as `""` by all current callers.
- **JWT revocation.** Fully wired on deactivate, delete, admin password reset, SCIM deprovision, and force-logout, with cross-replica cache invalidation; negative verdicts are never cached.
- **Stream WebSocket origin check.** `InsecureSkipVerify` applies to the *origin* check only and no cookie auth is accepted (ticket or bearer required), so cross-site WebSocket hijacking is not reachable.
- **remoteproxy v2.** Its insecure transport is hard-gated out of production by `TLSOptions.Validate`.
- **Skip-verify defaults.** SIEM, Vault, and dashboards all default `tls_skip_verify` false.
- **Chart certificate handling.** Issuance and renewal delegated to cert-manager; the runtime Dex Secret is correctly marked GitOps-inert (`Prune=false`, `IgnoreExtraneous`, no `data`). The bootstrap-secret `lookup` regeneration hazard is documented and production-guarded.
- **Frame binding and replay.** Streams are per-AgentConnection keyed by a server-minted UUID, so a compromised agent cannot touch another cluster's streams. Tickets are single-use (Redis GETDEL) and cluster-scoped; CONNECT replay is bounded by clock skew plus single-use registration tokens. Route-table overflow is bounded (256 streams/agent, 16 MiB WS read limit, 16 MiB proxy body cap, 64 MiB agent response cap).
- **ArgoCD cluster-proxy token gate.** `routes.go:1834-1872` — hash-compared, cluster-bound, purpose-checked, expiry-checked, constant-time.
- **argosecurity sanitize/validate layer.** Closed source model, duplicate-key rejection, JSON-patch pointer projection, body-size ceilings, protocol-upgrade refusal on the proxy.
- **Self-management write barrier.** Approval-hash staging, controller-quiesce gates, adoption-evidence UID/resourceVersion re-verification. Unusually careful; no revert race could be constructed.
- **GitOps auth-blob encryption.** The previously-noted never-decrypted bug is fixed (`internal/worker/tasks/gitops_sync.go:766-795`).
- **`internal/server/routes.go` (2,157 lines) should NOT be split.** A route table is meant to be one readable manifest; additions are append-only inside existing blocks so the conflict surface is low; and it is guarded by a golden `RouteTable` contract test plus `docs/generated-route-inventory.json`, so drift is caught mechanically. Splitting would trade a safety property for cosmetics.
- **The "committed binaries" concern is FALSE.** `keyrotate`, `worker`, `bin/`, and `screenshots/` exist in the worktree but are gitignored, absent from `git ls-files`, and absent from `git log --all`. Nothing to purge from history; only the Docker build context is affected (P3).
- **`ClusterHandler.List` ArgoCD enrichment** is already correctly batched (`internal/handler/clusters.go:658-694`). `filterAppsForTargets` is O(clusters × apps) with map lookups — fine at realistic page sizes.
- **Backups/restore.** Reviewed at API-surface and reconciler level (schedules, storage configs with connection test, Velero BSL/Schedule/Backup/Restore CRs, retention enforcement). No reportable parity gap. etcd restore being runbook-only (`internal/handler/control_plane_snapshots.go:349-366`) is a deliberate design choice given astronomer does not own node lifecycle.
- **Test suite integrity.** Only 5 `t.Skip` calls in the whole Go suite; the suite is not silently skipping.

### Not examined — known gaps in this audit

- **No code was executed and no test was run.** Every finding is from reading source; every line citation is a line that was actually opened. Nothing here has been reproduced at runtime.
- **`golangci-lint` could not be run** (binary not installed in the audit environment) and a hand-rolled AST unused-export scan timed out — which is itself the argument for that gate finding. There is no measurement of how large the first-run lint backlog is.
- **`leave-local-ownership-exclusion-fails-open` has no verifier verdict.** It is the only such item; re-verify before implementing.
- **`internal/worker/tasks`** was not systematically reviewed outside the specific tasks named in findings.
- **Two DB-gated concurrency tests never run in CI** — `internal/db/sqlc/argocd_operations_concurrency_test.go` and `dex_lifecycle_concurrency_test.go` require `ARGOCD_OPERATION_CONCURRENCY_TEST_DATABASE_URL`, which is set in no workflow. Narrow (2 files) but worth a follow-up.
- **`internal/handler/argocd.go:1030-2540`** (the durable-operation reconciler state machine and sync-window override handling) was read only at its entry points. `internal/handler/argocd_ownership.go`, `internal/gitops/{parser,apply}.go`, and `internal/handler/gitops.go` beyond credential handling were not reviewed. The frontend ArgoCD views were not reviewed.
- **MFA/TOTP, lockout, and password policy** (`internal/auth/{totp,lockout}.go`) were not exercised in depth. They exist and appeared conventional; no findings claimed either way.
- **Agent internals not read:** `helm.go`, `rbac.go`, `project_reconciler.go`, `reconcile.go`, `mirror_subscriber.go` beyond the replay path, `apiserver_audit.go`; `internal/registration/service.go` beyond the phase machine; the full RBAC/profile rendering in `deploy/agent/template.go`.
- **101 of 885 sqlc `:many` queries lack a LIMIT.** Sampled; most are small config tables where pagination would be noise. Only `ListEnabledTools` is on a demonstrated hot path (P3). `ListMirroredNetworkPolicies`, `ListMirroredResourceQuotas`, and `ListAllProjectNamespaces` are the plausible next tier if fleet size grows, but no hot call path could be demonstrated.
- **Two authorization observations noted but not reported** because they fell under other dimensions and were not chased to a verdict: `/catalog/charts/*` and `/catalog/installed/` carry no route-level RBAC gate (handlers do filter internally, and `values_override` is correctly withheld from list projections), and `/admin/compliance-baselines/*` read endpoints are gated on `requireAuth` only. Worth a follow-up pass.
- **Helm chart templates** beyond the specific files cited, and the Rancher `ui/` reference tree beyond `router.js`, were out of scope for the code-health dimension.

