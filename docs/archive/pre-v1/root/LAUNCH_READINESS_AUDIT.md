# Astronomer Launch-Readiness Audit

**Executive summary.** Fourteen subsystem finders swept the astronomer management plane; 20 findings survived adversarial verification (2 down-graded on verify, 2 latent-but-real). The dominant theme is **broken access control**: authorization was designed (RBAC resources are defined and wired into role templates) but never attached to whole route subtrees — backups and alerting have *zero* RBAC, and two user/audit reads are fully unauthenticated. Layered on top is one **critical, sanctioned-operation data-loss bug**: the documented key-rotation procedure silently corrupts most encrypted credential columns and locks out every MFA user. Correctness gaps (silent cloud-credential rotation failure, an MFA-enforcement lockout dead-end) round out the blocker set.

**Verdict: NOT LAUNCH-READY.** The single most important reason: `keyrotate` re-encrypts only 3 of ~14 Fernet columns, so following the official secret-rotation runbook silently and irreversibly bricks Vault/GitOps/cloud/registry/SMTP creds and every TOTP secret — a critical integrity failure triggered by a blessed admin operation, with an exit-0 giving false confidence. Even setting that aside, the cluster of missing-authorization blockers (any authenticated user can drive destructive cross-cluster restores, read notification-channel secrets, and enumerate the operator directory unauthenticated) independently gates launch.

---

## Launch Blockers

Critical + high issues that MUST be fixed before launch, ordered by severity then blast radius.

### 1. `keyrotate` re-encrypts only 3 of ~14 Fernet columns — key rotation silently corrupts creds and locks out MFA
- **Severity:** Critical · **File:** `cmd/keyrotate/main.go:96-100`
- **What's wrong:** The rewrite `targets` slice covers exactly three tables (`sso_configurations.client_secret_encrypted`, `argocd_instances.auth_token_encrypted`, `backup_storage_configs.encrypted_credentials`). The package doc and `docs/secret-rotation-runbook.md` claim it re-encrypts *every* Fernet column, but ≥10 others use the same `*auth.Encryptor` and are never swept: `vault_connections.auth_encrypted`, `cloud_credentials.data_encrypted`, `gitops_registration.auth_encrypted`, `user_totp_enrollments.secret_encrypted`, `smtp_settings.password_encrypted`, `webhook_subscriptions.secret_encrypted`, `sso_sessions.upstream_id_token_encrypted`, `argocd_cluster_proxy_tokens.token_encrypted`, `cluster_registry_configs.registry_password_encrypted`, `prometheus_datasources.auth_encrypted` (verify also flagged `siem_configurations.auth_encrypted` and a dashboards column). The one-time `tasks/plaintext_credential_migration.go` only wraps *empty* columns, so it cannot help rotation.
- **Impact:** Operator follows the runbook: promote NEW_KEY → run `keyrotate` (exits 0) → drop OLD_KEY. Every un-swept column still holds OLD_KEY ciphertext; once OLD_KEY leaves the fallback list those rows become undecryptable — Vault/GitOps/cloud-cred/registry/webhook/SMTP break and **every TOTP/MFA user is locked out**. Re-running `keyrotate` can never repair it. Recoverable only while OLD_KEY is still retained.
- **Fix:** Drive `targets` from `docs/secret-column-inventory.md` (add all `*_encrypted` columns); add a test that fails when a new `*_encrypted` column lacks a target (mirror `migration_secret_columns_test.go`); correct the runbook's completeness claim.

### 2. Backups surface has no RBAC — any authenticated user drives cross-cluster restores, deletes backups, injects storage creds
- **Severity:** High · **File:** `internal/handler/backups.go:947` (`CreateRestore`); routes `internal/server/routes_tools_controlplane.go:106-139`
- **What's wrong:** The entire `BackupHandler` family has zero authorization (no `authz` field, no `authorizeClusterAction`/superuser/binding check anywhere). The `/backups` subtree is wrapped only in `featureGate("feature.backups")` — no `requirePermission`, no `mutationWriteScope`. The only backstop, `RequireWriteScopeForMutations("")`, passes JWT browser sessions and legacy empty-scope tokens. `rbac.ResourceBackups` is defined (`internal/rbac/types.go:15,67`) and wired into role templates (`backups:manage`) — enforcement was simply never attached. Rows carry a caller-supplied `cluster_id` applied verbatim.
- **Impact:** Any authenticated principal (including a viewer or a user scoped only to an unrelated cluster) can: launch a destructive Velero restore that overwrites namespaces on *any* managed cluster (`backup.ClusterID` used verbatim, line ~985); delete any tenant's backups/schedules/storage; and write a `BackupStorageLocation` + credentials Secret into any cluster's velero namespace. Full tenant-isolation break.
- **Fix:** Add an `authz` field to `BackupHandler` (mirror `CatalogHandler.SetAuthorization`) and call `authorizeClusterAction(w, r, clusterID, rbac.ResourceBackups, verb)` in every handler after resolving the target cluster — load the row first for `{id}` routes; RBAC-filter list endpoints by `allowsCluster`.

### 3. Alerting surface has no RBAC — any authenticated user reads channel secrets, tampers rules, and suppresses alerts
- **Severity:** High · **File:** `internal/server/routes_rbac_audit_agents.go:118-148`; handler `internal/handler/alerting.go` *(merges the two overlapping alerting findings)*
- **What's wrong:** The entire `/api/v1/alerting/*` tree (channels, rules, events, silences) and `/api/v1/alerts/*` aliases register on the plain `authenticated` router with no `requirePermission`/`requireScope` on any verb — unlike every sibling block (rbac, native-rbac, agents, audit) in the same file. `AlertingHandler` has no `authz` field and no in-handler check (`CreateChannel:181`, `CreateRule:398`, etc.). `rbac.ResourceAlerts` is defined (`rbac/types.go:12`) and scoped per-role in migration 098, but referenced in no non-test route. `notificationChannelResponse` (`alerting.go:1399-1408`) returns raw channel `Configuration` verbatim, including delivery secrets.
- **Impact:** Any authenticated user (incl. read-only API tokens on the GET path, and any JWT session on mutations) can: read every channel's secret config (Slack webhook URLs, PagerDuty keys); create/modify/delete rules and channels (repoint a webhook to exfiltrate alert content); and create/expire silences to blind operators before or during an attack.
- **Fix:** Wrap `/alerting` and `/alerts` with `requirePermission(..., rbac.ResourceAlerts, verb)` (Read on GETs, Create/Update/Delete on mutations) plus the write-scope gate; redact secret fields from `notificationChannelResponse` (or restrict full-config reads to `alerts:update`).

### 4. Native-rule escalation guard undercharges wildcard core resources — enables cross-boundary secrets-read
- **Severity:** High · **File:** `internal/handler/native_rbac.go:306` *(requires `native_rbac_enabled` feature flag)*
- **What's wrong:** `enforceNoNativeEscalation` maps a rule's `(apiGroup,resource)` to a coarse RBAC resource via `mapNativeRuleResource`. `knownNativeK8sResource` returns `ok=false` for `resource=="*"` (and any unknown core resource), so with an empty apiGroup it falls through to `return rbac.ResourceClusters`. The guard then only requires the caller to hold `clusters:<verb>` for a wildcard core rule. At request time `rbac.NativeAllow` (`internal/rbac/native.go:58`) honors `{APIGroup:"",Resource:"*",Verbs:["read"]}` for *every* core resource including secrets (only privilege-escalation groups and exec/logs are refused).
- **Impact:** A caller holding `rbac:create` + `clusters:read` (e.g. a security/RBAC admin deliberately denied data-plane secret access) POSTs `{apiGroup:"",resource:"*",verbs:["read"]}`; the guard maps `*`→`ResourceClusters`, passes, and the stored rule then reads every Secret in every namespace/cluster via the k8s proxy — defeating the coarse `enforceNoEscalation` separation-of-duties boundary. Generalizes to `delete` etc.
- **Fix:** Charge wildcard/unknown core rules against the actual most-sensitive resource: require the mapped permission for every sensitive built-in the wildcard covers (secrets, pods, configmaps, storage, nodes, services, workloads, ingresses), or reject wildcard/unknown core-group rules at authoring unless the caller is superuser. Map unknown core resources fail-closed, not to `ResourceClusters`.

### 5. Unauthenticated user directory — `GET /api/v1/users/` and `/users/{id}/` dump all users + emails
- **Severity:** High · **File:** `internal/server/routes.go:572-573`
- **What's wrong:** Both reads register directly on the base `/api/v1` router (only `chimiddleware.Timeout` applies) with no `requireAuth`/`requireScope`/`requirePermission`. Handlers (`resources.go:902/923`) do zero identity checks and return `mapUser()` — username, email, displayName, enabled/IsActive, lastLogin, createdAt for every user. Write routes on the same resource *are* gated `ScopeAdmin`+RBAC; only reads are open. `routes_security_test.go:2672` whitelists them as `isRouterPublicReadPattern`, so CI blesses the gap.
- **Impact:** Any anonymous client with network reach dumps the complete operator directory (usernames, emails, last-login, active/disabled state) and fetches any account by UUID. Direct feed for targeted phishing, credential stuffing, and org recon against a K8s control plane. No login required.
- **Fix:** Wrap both reads with `requireAuth` + `requirePermission(..., rbac.ResourceUsers, VerbRead/VerbList)`, move them into the authenticated subtree, and remove them from the `isRouterPublicReadPattern` whitelist so the test enforces auth.

### 6. Cloud-credential rotation via UPDATE silently never propagates to the in-cluster Secret
- **Severity:** High · **File:** `internal/db/sqlc/cloud_credential_materialization_outbox_ext.sql.go:40`
- **What's wrong:** `CloudCredentialHandler.Update` re-encrypts `data_encrypted` then calls `materializeCredentialRefs(..., "apply")` with unchanged refs and `secret_name`. Both dedupe layers swallow it: the materialization CTE only resets status to `pending` when `secret_name` changes (unchanged → stays `applied`); the task_outbox CTE keeps `delivered` for the identical dedupe key (no data/version component). The outbox insert returns nil so the direct-enqueue fallback is skipped, and the drift sweep filters `WHERE status != 'applied'` (`cloud_credentials.sql.go:152`) so it never re-fires. (Contrast the self-healing cluster-registry sweep, which re-applies all rows.)
- **Impact:** Operator rotates a leaked AWS/GCP key, sees status `applied`, believes it took effect — but the in-cluster Secret keeps the OLD value indefinitely with no recovery path. Workloads keep using the compromised credential; the platform gives false assurance.
- **Fix:** Force re-materialization on a value change: reset status to `pending` when `data_encrypted` changes (add a data_version/updated_at compare), or fold a content hash/generation of `data_encrypted` into the outbox dedupe key.

### 7. "Require TOTP enrollment" is a permanent lockout dead-end — the enroll-only challenge is minted but no endpoint accepts it
- **Severity:** High · **File:** `internal/handler/auth.go:624`
- **What's wrong:** When MFA enrollment is enforced, an unenrolled local-password user who passes bcrypt gets HTTP 423 + a `PurposeTOTPEnrollOnly` challenge (a `PurposeToken`) instead of a session. But the only enrollment endpoints (`routes.go:500-501`) are behind `requireAuth`, which rejects *every* `PurposeToken` at `middleware/auth.go:234`. Repo-wide, `PurposeTOTPEnrollOnly` is generated once and consumed nowhere; the frontend "recovery" redirect never reads the hash either. Worsened by `SetTOTPPolicy` failing **closed** (enforcing) on any transient DB read error (`server.go:711`).
- **Impact:** The moment enforcement is enabled — or a Postgres hiccup trips the fail-closed path — every unenrolled user with no live session is permanently locked out: brand-new users, anyone whose access+refresh tokens expired, and unenrolled admins. Recovery requires a still-authenticated admin or DB surgery. (Already-logged-in users can still enroll via the normal wizard.)
- **Fix:** Add a middleware/route that accepts the `PurposeTOTPEnrollOnly` challenge (validate signature + Purpose + UserID) and mount enroll/start + enroll/confirm behind it, minting the full session pair on confirm. Until wired, don't let `SetTOTPPolicy` fail closed into enforcement, and gate the feature behind an explicit documented rollout.

---

## Should-Fix Before Launch

Medium issues — fix before or immediately after launch.

| # | Issue | File:line | Impact | Fix |
|---|-------|-----------|--------|-----|
| 8 | **k8s proxy reads request body fully into memory, no size cap (DoS)** | `internal/tunnel/proxy.go:400` | Authenticated user with one mutating verb POSTs a multi-GB body; `io.ReadAll` + base64 (+33%) + JSON marshal = ~2.6–3.7× resident before any tunnel send; a burst OOM-kills the shared replica, dropping all agent tunnels/tenants. Sibling doors already cap (`internal_k8s.go:189`=8 MiB, `internal_helm.go:132`=16 MiB). | Wrap `r.Body` in `http.MaxBytesReader`/`io.LimitReader` at 8–16 MiB in `buildK8sRequestPayload`, return 413; same bound on `forwardToOwnerPod`. |
| 9 | **Refresh endpoint bypasses MFA enforcement** | `internal/handler/auth.go:751` | `POST /auth/refresh/` re-issues a 7-day pair without the TOTP enrollment/`totpEnforced()` gate Login applies. A user who held a live refresh token when enforcement was flipped on rolls it forward forever, never enrolling — inverse asymmetry to the enroll-only lockout. | In `Refresh`, after loading the user re-run the same `totpEnforced()`+`IsEnrolled()` gate as Login; ideally switch to the revocation-aware validation path. |
| 10 | **Backup mutations lack the `ScopeWriteClusters` gate their siblings enforce** | `internal/server/routes_tools_controlplane.go:110` | Catalog/resource mutations carry `mutationWriteScope = RequireWriteScopeForMutations(ScopeWriteClusters)`; backups do not. An API token with a non-clusters write scope (`projects:write`, etc.) or legacy empty scope can drive destructive restore/delete/storage mutations the parallel subtrees block. Defense-in-depth behind blocker #2. | Wrap mutating backups routes with the same `mutationWriteScope` already constructed in this file. |
| 11 | **Unauthenticated audit/activity feed** | `internal/server/routes.go:517` | `GET /api/v1/activity/` on the base router returns per-row action + resourceName + timestamp from the audit log with no auth (whitelisted at `routes_security_test.go:2668`). Anonymous polling reveals a rolling feed of operations and named resources — recon the RBAC-gated `ListAuditLogs` otherwise protects. | Gate with `requireAuth` + read permission (`ResourceAuditLogs`/activity), move into authenticated subtree, remove from whitelist. |
| 12 | **HTTP apiserver-audit sender ignores the configured management CA bundle** | `internal/agent/apiserver_audit.go:231` | `newHTTPAuditSender(nil, ...)` uses a bare client trusting only the OS store; `cfg.CACert/CAChecksum` (applied to the tunnel via `BuildTLSConfig`) is never applied. On private/self-signed-CA clusters with `AUDIT_DELIVERY=http`, every audit POST fails TLS, the tailer never advances its checkpoint, and apiserver audit events are silently never delivered — a compliance/forensics gap in the private-CA mode. | Build the sender's client from the same `BuildTLSConfig(cfg.CACert, cfg.CAChecksum)`; on delivery error with a token present, fall back to the tunnel sender. |
| 13 | **`gitops_webhook_secret` config field never bound to its env var** | `internal/config/config.go:45` | The only mapstructure key with neither a `BindEnv` nor a default; with `AutomaticEnv` and no config file, `Load()` leaves it `""` even when `GITOPS_WEBHOOK_SECRET` is set (reproduced empirically). `gitops.go:465` then returns 503, so the git push-webhook sync endpoint can never be enabled in any deployment. Fails safe (disabled), so wiring defect not security hole. | Add `"gitops_webhook_secret"` to the `BindEnv` list alongside `manifest_signing_secret`. |

---

## Post-Launch / Nice-to-Have

Low severity — hardening, defense-in-depth, and latent correctness. (Two of these were down-graded from higher claims during verification.)

| # | Issue | File:line | Note |
|---|-------|-----------|------|
| 14 | **Audit batch-insert chunk exceeds PostgreSQL's 65,535 bind-param limit** | `internal/db/sqlc/audit_v1_manual.go:164` | 17 cols × `maxRowsPerExec`=4000 = 68,000 params; any chunk ≥3,856 rows overflows and the batch is dropped without retry. Latent: default buffers never reach it and `WithBufferSize` is a Go-API-only knob (no env/config, both prod callers pass no options). Trivial fix: `maxRowsPerExec = 65535 / auditLogColumnsPerRow`; fix the stale "16-column" comment. |
| 15 | **Audit bus publish bypasses secret redaction on the egress path** *(down-graded high→low)* | `internal/audit/audit.go:212` | `publishToBus` fans out raw `event.Detail` while `buildRow` sanitizes before the DB write, so SIEM/webhook sinks get unredacted detail. Verified latent: no current `Record`/`recordAudit` caller puts a secret-shaped key in `Detail`, and `Before`/`After` are never published. Fix anyway to prevent a future caller from leaking: publish `SanitizeDetail(event.Detail)`. |
| 16 | **Redaction kubeconfig detector is dead code** *(down-graded medium→low)* | `internal/redaction/redaction.go:90` | `strings.Contains(lower, "apiVersion:")` compares a case-folded string against a mixed-case literal → never matches, so the kubeconfig-string branch never fires. Narrow trigger: only leaks when a full kubeconfig YAML sits under a *non-sensitive* key (the `kubeconfig`-named field path is still redacted). Residual leak = `certificate-authority-data`/`client-*-data`. One-char fix: `"apiversion:"`; add a `String()`-level test. |
| 17 | **Dashboard widget iframes use `allow-same-origin allow-scripts`** *(partial)* | `frontend/src/components/dashboards/widget-grid.tsx:190` | The MDN-flagged sandbox-breakout combo; diverges from `SandboxedExtension.tsx:324` (scripts-only). Heavily gated: superuser-only widget authoring + `dashboard.allowed_iframe_hosts` allow-list (empty=block-all by default) + requires a same-origin injectable document. Real hardening gap, not the near-unconditional XSS the raw claim implied. Fix: drop `allow-same-origin`. |
| 18 | **Agent ServiceProxy builds upstream URL by unvalidated string concat (SSRF)** *(partial, latent)* | `internal/agent/service_proxy.go:67` | `fmt.Sprintf("http://%s.%s.svc...%s", ...)` with no validation; a path like `@169.254.169.254/...` smuggles the host via userinfo. Latent: the only originator (`Hub.ServiceProxyRequest`) has no non-test caller, and the live user-facing proxy uses `isSafeK8sName` + the apiserver `/proxy` subresource. Handler is registered on every agent, so fix before wiring: validate DNS labels, require leading `/`, build via `url.URL` fields. |
| 19 | **CSP allows `'unsafe-inline'` for `script-src`** | `internal/server/middleware/security_headers.go:8` | Applied globally; disables CSP's inline-script XSS mitigation for the management SPA. Pure defense-in-depth (needs a separate XSS to matter). Move inline bootstrap to nonce/hash-based CSP and drop `'unsafe-inline'` for scripts; verify against the built SPA. |

---

## What's Solid

Coverage was real, not everything is on fire:

- **ArgoCD integration** (`argocd-integration`) returned no surviving findings — no auth/wiring defects in the ArgoCD proxy path beyond the shared-body DoS already tracked under the tunnel proxy (#8).
- **DB layer** (`db-layer`) surfaced only one *latent* low (#14); the rest of the SQL/batch paths audited clean.
- **Config/limits/policy** (`config-limits-policy`) had no high/critical — the two findings (#13 wiring, #19 CSP) are a fail-safe wiring gap and a defense-in-depth header, not exploitable holes.
- The **auth token-scope backstop** (`RequireWriteScopeForMutations`) and the **per-route `requirePermission` pattern** are correctly implemented where wired — the failures are omissions on specific subtrees (backups, alerting), not a broken authorization *engine*. The coarse RBAC binding guard (`enforceNoEscalation`) works and is test-covered; only the native-rule wildcard path (#4) slips it.
- **Secret redaction at rest** works: `SanitizeDetail` correctly redacts the `audit_log` table, and current audit call-sites scrub secrets at the source. The gaps are on the egress (#15) and a dead detector branch (#16), both latent.

---

## Coverage

One line per subsystem finder — what was scoped, so the audit breadth is visible.

| Finder | Scope audited |
|--------|---------------|
| `tunnel-proxy` | User-facing k8s/ArgoCD proxy request path, cross-pod forwarding, body handling, rate-limit classes → 1 medium (DoS #8) |
| `auth-jwt` | Login/Refresh/TOTP flows, JWT purpose tokens, MFA enrollment enforcement, fail-closed policy → 1 high (#7), 2 medium (#9) |
| `rbac-authz` | RBAC engine, native-rule authoring/escalation guard, alerting route gating → 1 high (#4), alerting (merged into #3) |
| `secrets-gitops` | Fernet column inventory, key-rotation tooling, encryptor usage across handlers → 1 critical (#1) |
| `handler-idor-a` | Backups/restore/schedule/storage handlers, cross-cluster ownership, write-scope gates → 1 high (#2), 1 medium (#10) |
| `handler-idor-b` | Alerting channels/rules/silences authorization + secret exposure → 1 high (#3) |
| `server-routes-lifecycle` | Base vs authenticated router mounting, public-read whitelist, user/activity reads → 2 (#5 high, #11 medium) |
| `worker-tasks` | Materialization outbox, dedupe keys, drift-sweep recovery for cloud-cred/registry → 1 high (#6) |
| `db-layer` | Batch insert param math, SQLC query filters → 1 low (#14) |
| `argocd-integration` | ArgoCD instance auth, cluster-proxy tokens, internal proxy path → clean (shared DoS under #8) |
| `frontend` | Dashboard widget iframes, sandbox flags, SPA CSP interaction → 1 low (#17) |
| `observability-compliance` | Audit bus egress redaction, SIEM/webhook sinks, kubeconfig redaction → 2 low (#15, #16) |
| `agent-binary` | Agent audit sender TLS/CA, service-proxy URL construction, tunnel dial → 1 medium (#12), 1 low (#18) |
| `config-limits-policy` | Env-var binding completeness, security headers, limits → 1 medium (#13), 1 low (#19) |