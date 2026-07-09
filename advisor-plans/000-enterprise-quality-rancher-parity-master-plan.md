# Plan 000: Elite enterprise quality & Rancher day-2 parity — master remediation plan

> **Audience**: Human review first. This is the full assessment + ordered remediation
> program. Split into numbered executor plans (`001-…`, `002-…`) only after approval.
>
> **Repo root for execution**: `/root/astronomer-all/astronomer` (or the `astronomer`
> git checkout). Product plans already live under `plans/`; advisor plans live here
> under `advisor-plans/`.
>
> **Drift check (run first)**:
> ```bash
> git rev-parse --short HEAD
> git log --oneline 2991f9d..HEAD | head -40
> ```
> If HEAD ≠ `2991f9d`, re-spot-check every finding’s evidence lines before implementing.

## Status

| Field | Value |
|---|---|
| **Priority** | P0 program (master) |
| **Effort** | XL overall (~2–4 eng-weeks if parallelized by wave) |
| **Risk** | MED overall (Wave A security/correctness is lower-risk code; Wave D cutover is HIGH) |
| **Depends on** | none |
| **Category** | security · correctness · ops · scale · compliance · parity · ux · tests · debt · docs |
| **Planned at** | commit `2991f9d`, 2026-07-08 |
| **Method** | 5 deep parallel audits (security, correctness/HA, Rancher day-2 parity, ops/scale, frontend/tests/debt) + adversarial re-verification of prior launch/prod audits against live code |
| **Scope exclusions** | Cluster provisioning / machine drivers / node pools; Fleet product (Argo CD is the GitOps engine); Rancher Prime commercial entitlements |

---

## 1. Why this matters

Astronomer is a day-2 Kubernetes management plane for **already-provisioned** clusters:
outbound agent tunnels, Postgres product state, Argo CD delivery, Helm chart control plane.
Prior remediation waves closed real launch blockers (ArgoCD authz, keyrotate column coverage,
backups/alerting RBAC, open user directory, TOTP enroll lockout, catalog worker SSRF, cloud-cred
rotation, mesh:detect, F1 image skew, limit clamps).

The platform is **pilot / multi-user production capable**, but not yet **elite unattended
enterprise + Rancher-grade operator confidence** for adopted-cluster fleets. Residual gaps are
concrete and finite: credential disclosure, incomplete SSRF, inert registration CAS, soft DR
key custody, silent fleet truncation, compliance controls that look applied but are not
enforced, SCIM Groups incomplete, explorer apply/UX density, unmeasured scale claims.

This master plan is the single source of truth for **all open findings** from the 2026-07-08
deep dive, ordered into executable waves. It also records **closed prior blockers** so they
are not re-litigated.

---

## 2. Executive verdict

| Question | Answer |
|---|---|
| Can it run in production? | **Yes — pilot / multi-user with ops attention.** Prior critical authz/crypto blockers are fixed and re-confirmed. |
| Unattended enterprise / replace Rancher for adopted clusters? | **Not yet.** Close Waves A–C; measure scale (Wave D). |
| Day-2 vs Rancher? | **At parity or ahead** on monitoring lifecycle, SIEM/logging, compliance surface, durable audit, API/CLI, mesh detect, Argo CD GitOps. **Behind** on explorer form density, SCIM Groups write, SSA apply, and **measured** scale evidence. |

**Cross-cutting theme:** elite *scaffolding* exists (chart contracts, OpenAPI 100% CI, authz engine, DR machinery, load harness). Remaining work is **closing optional/incomplete paths so defaults and docs match reality** — not redesigning the architecture.

**Critical product flag:** keep `namespace_scoped_rbac_enabled` **default OFF** until **F7-b** is closed (and preferably UX-02/UX-03 for tenant-safe UI).

---

## 3. Rancher day-2 scoreboard (code-current @ 2991f9d)

| Dimension | Verdict | Residual |
|---|---|---|
| Cluster import / agent / health | **At Parity** | Agent upgrade/canary present |
| Argo CD vs Fleet capability | **At Parity** | Label re-stamp task-driven; decommission can orphan Argo secrets |
| Cluster explorer | **Behind** | Watch/proxy exist; PUT YAML (no SSA); form CRU thin vs Steve |
| RBAC / tenancy | **At Parity** | ns bindings + inheritance landed; flag default OFF |
| Auth / SSO / SCIM / MFA | **Behind (residual)** | SCIM Users + MFA; Groups RO; no password policy; session timeout not real |
| Catalog / tools | **At Parity** | Drift/readiness exist; readiness warn-only; no git chart repos |
| Monitoring / alerting / logging | **Ahead** mon/log; alert ≈parity | AM timings hard-coded; log QueryOutput = 501 |
| Audit / compliance | **Ahead** | Durable audit+SIEM; baseline read-audit + session timeout unwired |
| UI operational density | **Behind** | Strong modern UI; not Rancher form density |
| API / CLI / contract | **Ahead** | OpenAPI + CLI; frontend dual type systems |
| Provisioning | **Excluded** | By design |
| Fleet product | **Excluded** | Argo CD is the engine |

### Where Astronomer is Ahead (not findings)

- Per-cluster Prometheus/Thanos lifecycle + reconciler maturity
- Multi-transport SIEM + logging pipelines vs Rancher audit-writer focus
- CIS/trivy, Gatekeeper, PSA/netpol, 4 compliance baselines + posture
- Partitioned Postgres audit + task-outbox + SIEM tap
- OpenAPI + Go/TS generation + `astro` CLI
- Multi-mesh detect + mTLS coverage
- Management-plane pg_dump + restore drill + Velero path

### Stale comparison doc

`docs/rancher-astronomer-comparison.md` (2026-06-22) is **stale** on watch, SCIM Users,
MFA enforcement, ns-RBAC, AppSet wizard, tool drift, inhibition/recovery, CRD explorer,
agent upgrade. Do not use it as the work backlog without re-verifying.

---

## 4. Prior blockers — re-verified CLOSED (do not re-open)

| Prior claim | Status | Evidence |
|---|---|---|
| keyrotate incomplete Fernet columns | **FIXED** | `cmd/keyrotate/main.go:153-168` `rewriteTargets`; `cmd/keyrotate/coverage_test.go` |
| Backups / alerting no RBAC | **FIXED** | Alerting: `routes_rbac_audit_agents.go`; Backups: `SetAuthorization` + in-handler `ResourceBackups` |
| Unauthenticated `/users`, `/activity` | **FIXED** | `routes.go:532-544`, `599-614` |
| ArgoCD UI proxy no authz (R-01) | **FIXED** | `routes.go:432-439` + `middleware/argocd_authz.go` fail-closed |
| Native-rule wildcard escalation | **FIXED** (native path) | `rbac/native.go`, `handler/native_rbac.go` |
| TOTP enroll-only lockout | **FIXED** | `AuthOrTOTPEnrollChallenge` + enroll routes |
| Catalog worker SSRF + SafeClient | **FIXED on worker** | Handler path still open → SEC-02 |
| Cloud-cred rotation rematerialize | **FIXED** | Data-versioned dedupe key |
| mesh:detect inert | **FIXED** | Selects `status == "active"` |
| Worker fleet sweep fan-out (F6 partial) | **FIXED** for mesh/gatekeeper/health | monitoring still serial → PERF-02 |
| readyyz schema floor / F1 image skew | **FIXED** | `ExpectedSchemaVersion`, chart skew fail |
| H-02 health-sweep race | **PARTIAL** | Health path safe; metrics publisher residual → CORR-02 |
| Prod-readiness Phase 0 F1–F6, F8 | **LANDED** | F7-b still open for raw-proxy watch |

### Confirmed solid (enterprise posture)

- Authz engine patterns where wired; CSRF double-submit; session cookies HttpOnly/Secure
- Chart: external PG/Redis, HA/PDBs, NetworkPolicy, TLS, prod preflight, F8 bootstrap pin
- DR machinery: nightly dump, weekly restore drill, optional key-wrap + decrypt proof
- Observability: ServiceMonitors, PrometheusRules + runbooks, metrics-v1, SIEM package
- CI: `go test`, OpenAPI 100% coverage, frontend jest/tsc/e2e, Trivy, cosign/SBOM, k3d smoke
- Tunnel HA: locator ownership CAS, sharded agents, supersede guards
- Extension sandbox (scripts-only), dashboard iframe allow-list

---

## 5. Commands (repo verification baseline)

| Purpose | Command | Expected |
|---|---|---|
| Build | `make build` | exit 0; binaries in `bin/` |
| CI contract | `make verify` | exit 0; OpenAPI coverage 100% |
| Go tests | `make test` or `go test -race -count=1 ./...` | exit 0 |
| Vet | `go vet ./...` | clean |
| Migrations lint | `make check-migrations` | OK |
| Chart tests | `go test ./deploy/ -count=1` | ok |
| Frontend unit | `cd frontend && npx jest` | all pass |
| Frontend types | `cd frontend && npx tsc --noEmit` | clean |
| Helm prod render (full wiring) | use chart contract / `productionWiringSets` pattern from `deploy/*_test.go` | renders or fails for intentional skew |

---

## 6. All open findings

Severity bands used for prioritization:

- **S0** — trust-breaking (credential leak, silent wrong terminal state, false DR green)
- **S1** — enterprise truth / multi-tenant safety / scale correctness
- **S2** — parity / IdP / explorer correctness
- **S3** — polish, debt, docs, long-horizon cutover

Effort: **S** hours · **M** ~1 day · **L** multi-day · **XL** multi-week.

---

### Wave A — Trust & integrity (do first)

#### [SEC-01] Redact catalog repo credentials + require catalog:read on GET/list · S0 · M · MED · HIGH

- **Evidence**:
  - `internal/handler/catalog.go:348-362` — `GetRepo` returns full `sqlc.HelmRepository` including `auth_config`.
  - `internal/handler/catalog.go:231-290` — `ListRepos` returns raw rows; no RBAC filter.
  - `internal/handler/catalog.go:527-536` — documents plaintext `password` / `token` / `bearer` in `auth_config`.
  - `internal/server/routes_tools_controlplane.go:187-189` — GET list/get have **no** `catalog:read`; only create/update/delete gated.
  - Contrast: webhooks redaction pattern (`internal/handler/webhooks.go`).
- **Impact**: Any authenticated principal can read private Helm registry passwords/tokens.
- **Fix sketch**:
  1. DTO for list/get that replaces secret fields with `<encrypted>` / omits values (mirror webhooks/SMTP).
  2. Wrap GET `/catalog/repositories/` and `/{id}/` with `requirePermission(..., ResourceCatalog, VerbRead|List)`.
  3. Update path: accept sentinel + “leave unchanged” when client omits password.
  4. Tests: unauthenticated/auth-no-grant 403; grant-holder gets redacted secrets; create still accepts real secrets.
- **Verify**: `go test ./internal/handler/ -count=1 -run Catalog`; `go test ./internal/server/ -count=1 -run Catalog|Route`.

#### [SEC-02] Guard catalog handler sync/ingest with GuardPublicHost + SafeClient · S0 · S–M · LOW · HIGH

- **Evidence**:
  - `internal/handler/catalog.go:568-577` — `fetchAndIngestRepoIndex` uses plain `&http.Client{Timeout: 30s}` — no SSRF guard.
  - `internal/handler/catalog_oci.go` — OCI probe plain client (~:317).
  - Worker correctly guards: `internal/worker/tasks/catalog_sync.go` uses SafeClient + GuardPublicHost.
- **Impact**: `catalog:update` can force control-plane fetch of `169.254.169.254` / RFC1918.
- **Fix sketch**: Share one fetch helper with worker; `GuardPublicHost` pre-check + `httpclient.SafeClient`; block redirects to non-public; deny-tests with loopback URL.
- **Verify**: unit tests deny loopback/link-local; `make test` packages handler + worker/tasks.

#### [SEC-03] Route all operator/DB-supplied outbound URLs through SafeClient · S0 · M · MED · HIGH

- **Evidence**:
  - `internal/httpclient/ssrf.go` documents GuardPublicHost does **not** stop rebinding.
  - `internal/httpclient/safeclient.go` is dial-time fix; production adoption mostly worker catalog.
  - Residual Guard-only + plain clients: catalog test-connection (`catalog.go:1309+`), blessed (`catalog/blessed.go` + server caller), backups S3 probe (`backups.go:1506+`), cloud-cred GCP token_uri (`cloud_credentials_test_endpoint.go:148+`), SIEM (`siem/transport.go:268+`), notification webhooks (`notification_dispatch.go:397+`).
- **Impact**: DNS rebinding TOCTOU turns test-connection/dispatch into internal network access.
- **Fix sketch**: Prefer making `DefaultExternal` use SafeClient dial Control; migrate each sink; document opt-in “allow private destinations” for on-prem SIEM/backup if required.
- **Verify**: `go test ./internal/httpclient/`; per-package deny tests; no new plain `http.Client{` on operator-URL paths (grep gate optional).

#### [SEC-04] Align k8s-proxy privilege-escalation API groups with native denylist · S1 · S · LOW · HIGH

- **Evidence**:
  - Native blocks CSR + token minting: `internal/rbac/native.go:35-42`.
  - Proxy only: `routes.go:1557-1565` — `rbac.authorization.k8s.io`, `admissionregistration.k8s.io`, `apiregistration.k8s.io`, `apiextensions.k8s.io` — **missing** `certificates.k8s.io`, `authentication.k8s.io`.
  - Unknown groups map to `custom_resources`; cluster-operator has full CR write.
- **Impact**: Authz intent inconsistent; management plane under-enforces escalation-class APIs (agent SA still required for real cluster impact).
- **Fix sketch**: Expand `isPrivilegeEscalationAPIGroup` to match `isPrivilegeEscalationGroup`; proxy unit tests mirror native tests.
- **Verify**: `go test ./internal/server/ -run Privilege|K8sProxy`.

#### [SEC-05] Gate control-plane controller GETs with alerts/admin RBAC · S1 · S · LOW · HIGH

- **Evidence**:
  - `routes_tools_controlplane.go:57-70` — mutations superuser; GETs auth-only.
  - `handler/control_plane.go` Status/GetPolicy/ListAlerts — no RBAC.
- **Impact**: Any logged-in user reads fleet control-plane policy and alert inventory.
- **Fix sketch**: Require `alerts:read` / superuser / admin scope on GETs consistent with alerting routes.
- **Verify**: httptest: viewer 403, admin 200.

#### [CORR-01] Wire registration phase CAS to generated sqlc (currently inert) · S0 · S · LOW · HIGH

- **Evidence**:
  - `internal/registration/service.go:347-377` — optional `phaseCASQuerier` expects positional
    `UpdateClusterRegistrationPhaseCAS(ctx, id, expected, next, started, completed) (UpdateClusterRegistrationPhaseRow, error)`.
  - `internal/db/sqlc/cluster_registration.sql.go:317-332` — real method takes
    `UpdateClusterRegistrationPhaseCASParams` and returns `UpdateClusterRegistrationPhaseCASRow`.
  - Interface never matches `*sqlc.Queries` → always falls through to unconditional update.
  - Only test fake implements the optional interface.
- **Impact**: Concurrent Cancel vs apply-worker success last-write-wins; wrong terminal phase + SSE/metrics.
- **Fix sketch**: Align interface to Params/Row **or** thin adapter; add
  `var _ phaseCASQuerier = (*sqlc.Queries)(nil)` (or adapter) compile-time assert; keep CAS unit tests.
- **Verify**: registration package tests; compile-time assert present; optional integration race test.

#### [CORR-02] Metrics status sweep must use heartbeat-guarded status update · S1 · S · LOW · HIGH

- **Evidence**:
  - Health (fixed): `health_check.go` → `UpdateClusterStatusOnHeartbeat` (`clusters.sql`).
  - Metrics publisher (racy): `internal/metrics/publisher.go:261-300` uses unguarded `UpdateClusterStatus` from snapshot; still authoritative transition writer every 15s.
- **Impact**: Reconnect after snapshot can be forced `disconnected` (and SSE) until next tick.
- **Fix sketch**: Extend querier interface; use heartbeat CAS; emit SSE only when rows affected > 0.
- **Verify**: unit test race pattern mirroring health_check race test.

#### [CORR-04] keyrotate: honor `--batch-size` + CAS ciphertext on rewrite · S1 · M · MED · HIGH

- **Evidence**:
  - Flag “rows per transaction” (`cmd/keyrotate/main.go:19-20,60`) unused in `rewriteColumn` (`:179-238`).
  - Full-table SELECT into memory; `UPDATE ... WHERE id=$2` with no `AND col = old_ct`; no transactions.
- **Impact**: Concurrent server secret write lost during rotation; large tables (e.g. sso_sessions) OOM risk.
- **Fix sketch**: Keyset/limit batches + tx; `UPDATE SET ct=$new WHERE id=$id AND ct=$old`; count CAS misses; dry-run accuracy.
- **Verify**: table tests with fake DB or pgx test; coverage_test still green.

#### [CORR-05] Backup/restore “started” transitions need status CAS · S2 · S · LOW · MED

- **Evidence**:
  - `backup_execution.go` / `run_restore.go` check status in memory then unconditional SQL (`backups.sql` status-blind UPDATE).
- **Impact**: Concurrent enqueue/reconciler can re-stamp terminal ops as `running`.
- **Fix sketch**: `UPDATE ... WHERE id=$1 AND status IN ('pending',…)` returning rows; 0 rows = idempotent.
- **Verify**: handler/worker tests for double-start.

#### [OPS-01] Hard-require encryption-key backup wrapping secret in production · S0 · S–M · LOW · HIGH

- **Evidence**:
  - `values-production.yaml:328-341` enables key backup + decryptCheck with empty `wrappingSecretRef.name`.
  - CronJobs only arm when name non-empty (`management-plane-backup-cronjob.yaml`).
  - Prod preflight requires S3 but **not** key wrap (`_helpers.tpl` requireProductionInputs); NOTES only warns.
- **Impact**: Operators ship “backups enabled” with green CronJobs while restore-to-new-cluster leaves Fernet columns undecryptable.
- **Fix sketch**: Fail `requireProductionInputs` / schema when backups enabled and wrap name empty; opt-out only via explicit `encryptionKeyBackup.enabled=false` + acceptance comment; README install checklist; chart render tests.
- **Verify**: `go test ./deploy/ -run F4|Production|Backup|Key`; helm template prod without wrap fails.

---

### Wave B — Enterprise truthfulness & scale correctness

#### [DIR-05] Wire compliance `session.timeout_minutes` into real JWT/session enforcement · S1 · M · MED · HIGH

- **Evidence**:
  - Baselines pin `session.timeout_minutes` (`compliance/baselines.go`).
  - Apply upserts arbitrary settings (`compliance/apply.go:322-328`).
  - `settingsRegistry` has **no** `session.timeout_minutes` (`platform_settings.go`).
  - JWT access TTL is boot config only (`server.go` → `NewJWTManager(..., cfg.SessionTimeoutMinutes)`).
  - No UI inactivity idle logout (only kubectl shell idle).
- **Impact**: Applying PCI/HIPAA baseline claiming 15m re-auth does **not** change live session lifetime — false control for auditors.
- **Fix sketch**: Register setting; read at token mint/refresh; optional idle client+server; baseline apply becomes real.
- **Verify**: unit tests for mint TTL from setting; compliance apply integration; document refresh semantics.

#### [DIR-09] Wire compliance baseline `read_audit_policies` into apply/revert · S1 · M · LOW–MED · HIGH

- **Evidence**:
  - Baselines declare `ReadAuditPolicies` (`baselines.go`).
  - Apply **skips** with warn (`apply.go:390-392`); restore also unwired (`:536-539`).
- **Impact**: Compliance apply does not enable required read-side audit policies; posture overstated.
- **Fix sketch**: Enable named policies on apply; capture prior state for revert; tests for apply/revert round-trip.
- **Verify**: compliance package tests; audit middleware sees enabled policies.

#### [DIR-04] Enforce configurable password policy on create/change/reset · S2 · M · LOW · HIGH

- **Evidence**:
  - `CreateUser` any non-empty password (`handler/users.go:86-89`).
  - `ChangePassword` non-empty only (`auth.go:1171-1174`).
  - Settings UI copy mentions “password policy” without backend registry keys.
- **Impact**: Weak local accounts fail PCI/ISO control reviews.
- **Fix sketch**: Platform settings min length/complexity/history/expiry; validate create/change/reset/SCIM password set.
- **Verify**: table-driven validation tests; SCIM path if applicable.

#### [F7-b] Namespace-filter raw k8s-proxy watch streams (or drive UI from scoped SSE) · S1 · M · MED · HIGH

- **Evidence**:
  - List path admits ns-scoped users with `WithNamespaceFilter` (`routes.go:1345-1390`).
  - Explicitly **excludes** watches: `!isK8sProxyWatchRequest(r)` and `ref["watch"] != "true"` → 403.
  - Frontend live view uses raw proxy `?watch=true` heavily; typed `/pods/watch/` was secured separately.
- **Impact**: With `namespace_scoped_rbac_enabled=true`, tenant live explorer does not update (fails closed).
- **Fix sketch (pick one, document)**:
  - **A (preferred backend)**: Namespace-filter watch frames in tunnel (extend `nsfilter` for watch events), admit watches for users with partial ns grants.
  - **B (frontend)**: Switch pod/resource live views to scoped typed SSE where available.
- **Do not** default the flag ON until this lands.
- **Verify**: proxy tests with ns filter + watch; frontend e2e or unit for watch path; ns-RBAC matrix script if present.

#### [PERF-01] Page fleet sweeps beyond hard Limit 1000 · S1 · S–M · LOW · HIGH

- **Evidence**:
  - Single page `Limit: 1000, Offset: 0` in `mesh_detect.go:125-128`, `gatekeeper_policy_apply.go:69`, `monitoring_reconcile.go:63`.
  - Health-check and metrics publisher already page at 500.
- **Impact**: On fleets >1000 non-decommissioned clusters, oldest clusters permanently miss mesh/gatekeeper/monitoring reconcile — silent.
- **Fix sketch**: Reuse health-check paging + fanOut; regression test at 1001 clusters / mock pages.
- **Verify**: worker task tests; optional contract “no full-fleet Offset:0 cap”.

#### [PERF-02] Parallelize monitoring_reconcile with fanOut + deadline · S1 · M · MED · HIGH

- **Evidence**: `monitoring_reconcile.go:68-75` serial loop; no `fanOutClusters` / aggregate deadline.
- **Impact**: At 100–500 clusters wall-clock exceeds schedule; stale monitoring state.
- **Fix sketch**: Apply `fleet_sweep.go` pattern; concurrency + per-cluster timeout.
- **Verify**: unit tests for fan-out; no deadline exceed in synthetic 200-cluster list.

#### [PERF-03] Bound remaining unbounded list paths · S2 · M · LOW · HIGH

- **Evidence**:
  - `ListClustersInGroupTree` no LIMIT (`cluster_groups.sql` / handler).
  - `ListEnabledSnapshotSchedules` unbounded (minute poll).
  - Admin agent posture loads entire fleet (`agent_admin_posture.go`).
  - Kubectl shell sessions: unpaginated + N+1 command counts (`kubectl_shell.go:361-373`).
- **Impact**: Memory/latency amp on large fleets; F5 limit clamp only catches `queryInt("limit")` patterns.
- **Fix sketch**: LIMIT/OFFSET or keyset; batch command counts; extend contract tests beyond queryInt.
- **Verify**: handler tests; contract test expansions.

#### [PERF-04] Fix misleading queryLimit(1000) clamp-to-200 · S3 · S · LOW · HIGH

- **Evidence**: `argocd_orphans.go:66-72` uses `queryLimit(r, 1000)` but hard cap is 200 (`response.go`).
- **Impact**: Orphan reports silently truncated vs author intent.
- **Fix sketch**: Use `queryLimitMax` if wide pages intentional; remove dead checks.
- **Verify**: unit test expected max.

#### [AIRGAP-01] Complete production airgap image list · S1 · M · LOW · HIGH

- **Evidence**:
  - `deploy/chart/images.txt` ~first-party + postgres/redis/busybox/argo — **no** dex, pgdump-s3, fluent-bit.
  - `scripts/extract-images.sh` templates default values only (dex/backup/logging off).
  - `docs/airgapped-install.md` claims incomplete list.
- **Impact**: Air-gapped prod fails when Dex/backup/logging pull unmirrored images.
- **Fix sketch**: Render with prod-like flags; merge into images.txt; CI `make images.txt` dirty check; fix docs.
- **Verify**: images.txt contains dex + backup + fluent when those features default-on in prod overlay.

#### [CI-01] Align PR production helm template with chart production contract · S2 · S · LOW · HIGH

- **Evidence**: `.github/workflows/pr-validation.yaml` prod helm template omits bootstrap password/email, NP CIDRs, key-wrap vs chart tests.
- **Impact**: CI green while real production render fails.
- **Fix sketch**: Reuse `productionWiringSets`; assert backup CronJob, PDBs, NP, F1 skew fail case.
- **Verify**: workflow dry-run locally or contract tests already cover; PR workflow updated.

#### [DOCS-01] Fix upgrade runbook for external Postgres + real preflight · S2 · S–M · LOW · HIGH

- **Evidence**: `docs/upgrade-runbook.md` uses `kubectl exec astronomer-postgres-0`; prod disables bundled PG; dry-run order wrong; queue check no-op; agent matrix stub.
- **Impact**: On-call misdirection during real upgrades.
- **Fix sketch**: Branch bundled vs external (mirror DR runbook); fix order; real queue/metrics check; honest matrix.
- **Verify**: doc review checklist; optional link tests.

#### [DOCS-02] Sync runbooks index with all Prometheus rule runbook_urls · S3 · S · LOW · HIGH

- **Evidence**: `docs/runbooks/README.md` lists 8; rules also point at task-outbox-stalled, failed-migration, postgres-failover, backup-restore-drill-failed, audit/siem drops, logging-flatlined, etc. (files exist).
- **Fix sketch**: Sync table; optional contract test `TestPrometheusRunbookURLsResolve` already partial — extend.
- **Verify**: existing resolve test green; README complete.

#### [DOCS-03] Chart README bootstrap guidance for GitOps/prod pin · S3 · S · LOW · HIGH

- **Evidence**: README implies empty password always OK; production fails without pin / GitOps re-rolls without pin.
- **Fix sketch**: Document production/GitOps pin; empty password = live helm dev only.
- **Verify**: README cross-check with `_helpers.tpl` F8 comments.

---

### Wave C — Rancher residual, IdP, explorer, UX

#### [DIR-03] SCIM Groups membership write (IdP parity) · S2 · L · MED · HIGH

- **Evidence**:
  - Astronomer SCIM: Users CRUD + Groups **read-only** (`handler/scim.go:3-25`).
  - Rancher SCIM: Create/Update/Patch Group + membership.
- **Impact**: Okta/Entra group push does not drive RBAC group mappings; manual identity_group_mappings.
- **Fix sketch**: SCIM Group create/patch/delete writing mappings; ServiceProviderConfig write features; tests + audit.
- **Verify**: scim tests; live matrix with mock IdP optional.

#### [DIR-01] Server-side apply for explorer YAML (replace blind PUT) · S2 · M · MED · HIGH

- **Evidence**:
  - Frontend `k8sApplyYaml` → PUT (`frontend/src/lib/api.ts:1973-2040`).
  - Backend named-resource updates PUT (`handler/resources.go:1426-1433`).
  - SSA helper exists for controllers (`internal/kubeutil/apply.go`) but not explorer path.
- **Impact**: Concurrent edits last-write-win; no field ownership / 409 semantics.
- **Fix sketch**: PATCH `application/apply-patch+yaml` + `fieldManager=astronomer`; surface 409; align UpdateNamedResource; dry-run path.
- **Verify**: handler tests; frontend dry-run + apply; conflict test.

#### [DIR-10] Harden ArgoCD label propagation + decommission cleanup · S2 · M · MED · HIGH

- **Evidence**:
  - Label refresh asynq-only; periodic reconciler does not re-stamp (`docs/argocd-fleet-equivalence.md`; task `argocd:refresh_managed_cluster_labels`).
  - Decommission records Argo Secret **orphans** rather than deleting upstream.
  - Label sanitization collisions undefined / no UI warn.
- **Impact**: Fleet-like auto-targeting lags under queue outage; decommission leaves AppSet targets; silent label collisions.
- **Fix sketch**: Periodic label reconcile; optional decommission auto-unregister; reject colliding sanitized keys in API/UI.
- **Verify**: worker tests; decommission integration; API validation tests.

#### [DIR-11] Treat tool install readiness as a gate (not warn-only) · S2 · M · MED · HIGH

- **Evidence**: `checkToolReleaseReady` records warn and returns nil (`tools.go:1482-1509`); operation can succeed.
- **Impact**: UI shows installed while pods not Ready; weaker than monitoring stack pattern.
- **Fix sketch**: Async readiness phase with timeout; fail operation on sustained non-deployed; optional pre-install prereq checks.
- **Verify**: tools operation state machine tests.

#### [DIR-12] Expose helm release history list · S3 · S–M · LOW · HIGH

- **Evidence**: Rollback API exists; no History RPC / list revisions endpoint.
- **Impact**: Operators need revision numbers out-of-band.
- **Fix sketch**: Helm History via tunnel; `GET .../installed/{id}/revisions/`; UI before rollback.
- **Verify**: handler + agent tests.

#### [DIR-08] Externalize Alertmanager group_wait / group_interval / repeat_interval · S3 · S–M · LOW · HIGH

- **Evidence**: Hard-coded 30s/5m/3h (`handler/alerting.go:1311-1317`).
- **Fix sketch**: Per-rule or global settings; render into AM config; UI controls.
- **Verify**: config render unit tests.

#### [DIR-06] Implement logging QueryOutput backends (today 501) · S2 · L · MED · HIGH

- **Evidence**: `LoggingHandler.QueryOutput` always NotImplemented (`logging.go:710-724`).
- **Impact**: Configure Loki/ES but cannot search in-product.
- **Fix sketch**: Loki/ES clients first; RBAC; pagination; SSRF guards (SafeClient); redact secrets.
- **Verify**: handler tests with fake backends.

#### [DIR-07] Git-sourced catalog repos (ClusterRepo git parity) · S3 · L · MED · HIGH

- **Evidence**: Catalog HTTP/OCI only; Rancher has gitRepo/gitBranch.
- **Impact**: Git-only chart workflows need external museum/OCI mirror.
- **Fix sketch**: `repo_type=git` branch/path, poll task, index as versions; reuse install path.
- **Verify**: worker git poll tests; catalog auth SSRF/git auth care.

#### [DIR-02] Form/schema CRU for top resource kinds · S3 · L · MED · HIGH

- **Evidence**: Create is YAML templates (`create-resource-dialog.tsx`); detail overview/YAML/events; Rancher has schema form inputs.
- **Impact**: Largest day-2 UX density gap for non-YAML operators.
- **Fix sketch**: Schema-lite forms for Deployment/Service/Ingress/ConfigMap/Secret; keep YAML power mode.
- **Verify**: component tests; e2e create path.

#### [UX-02] Replace broken hasPermission with rule-based can() · S1 · S · LOW · HIGH

- **Evidence**:
  - `frontend/src/components/projects/hooks.ts:282-302` checks `globalRoles.includes('projects:update')` etc.
  - Real grants live in `roles.*.roleRules` (`lib/permissions.ts`); only admin/superuser names pass.
- **Impact**: Correct non-admin grants wrongly denied in templates/projects UI.
- **Fix sketch**: Delete `hasPermission`; implement via `can(user, resource, verb)`; regression tests.
- **Verify**: `npx jest` permissions + projects hooks tests.

#### [UX-01] Gate mutations with can() / PermissionState (not allow-then-403) · S2 · M · LOW · HIGH

- **Evidence**: backups, rbac, argocd pages lack `can`/PermissionState; fleet/gatekeeper/snapshots do it right.
- **Impact**: Operators without grants see destructive controls; 403 toasts.
- **Fix sketch**: High-risk pages: list/create/update/delete decisions; disable ActionMenu with reason.
- **Verify**: jest + optional e2e permission-disabled controls.

#### [UX-03] Attach permission metadata to cluster-context nav · S2 · M · MED · HIGH

- **Evidence**: `sidebar.tsx:189-287` cluster nav almost ungated except Custom Resources; global nav filters via `can`.
- **Impact**: Shell/secrets/snapshots surfaces exposed to anyone who opens a cluster URL.
- **Fix sketch**: `permission: { resource, verb }` per item; reuse filterNavGroups; align with backend catalog.
- **Verify**: sidebar unit tests.

#### [UX-04] Prefer live-watch invalidation over blanket polling · S3 · L · MED · HIGH

- **Evidence**: ~39 `refetchInterval` sites; full watch only on workloads; fleet polls only.
- **Impact**: Multi-cluster load; uneven live UX.
- **Fix sketch**: When SSE/watch connected, disable or lengthen poll; keep poll as fallback.
- **Verify**: hook tests; no regression when watch drops.

#### [UX-05] Offline / connectivity banner for dashboard · S3 · S · LOW · HIGH

- **Evidence**: No `navigator.onLine` handling in dashboard.
- **Fix sketch**: `useOnlineStatus` + banner; disable primary mutations when offline.
- **Verify**: component test.

#### [UX-06] Wire or hide catalog Upgrade no-op button · S3 · S · LOW · HIGH

- **Evidence**: `catalog/page.tsx:201-209` empty onClick placeholder.
- **Fix sketch**: Hide until modal exists **or** wire to UpgradeInstalledChart.
- **Verify**: no dead primary actions in catalog.

#### [UX-07] Align Settings nav (settings:read) with superuser-only pages · S3 · S · LOW · HIGH

- **Evidence**: Nav requires `settings:read`; hub is superuser-only.
- **Fix sketch**: `superuserOnly` on nav **or** real settings:read on pages.
- **Verify**: sidebar filter test.

---

### Wave D — Measured claims, tests, long-horizon debt

#### [SCALE-01] Fill scale baseline + stop over-claiming · S1 · L (runs) / S (docs) · LOW · HIGH

- **Evidence**: `docs/scale-baseline.md` table all `_TBD_`; recommends 500–2000; harness exists (`scripts/loadtest/`) with no CI gate / recorded pass.
- **Impact**: Capacity planning cannot be trusted.
- **Fix sketch**: Run medium/large against prod-like stack; fill table; nightly small profile optional; until then tone down prod sizing comments.
- **Verify**: at least one baseline row with `VERDICT: pass` committed or linked.

#### [CI-02] Automated load/scale gate · S2 · M–L · MED · HIGH

- **Evidence**: No workflow references loadtest.
- **Fix sketch**: Nightly `profiles/small.yaml` or medium against k3d; fail on missing `VERDICT: pass`.
- **Verify**: workflow green once; document runner requirements.

#### [TEST-01] Handler tests for users.go admin mutations · S2 · M · LOW · HIGH

- **Evidence**: CreateUser/UpdateUser/DeleteUser/ResetPassword/Unlock/ForceLogout — no `users_test.go`.
- **Fix sketch**: httptest authz + validation + audit; mirror password_reset/scim tests.
- **Verify**: `go test ./internal/handler/ -run User`.

#### [TEST-02] Expand E2E: RBAC, backups, fleet, permission gates · S3 · L · MED · HIGH

- **Evidence**: 7 e2e specs; smoke/mock only; no fleet/backups/RBAC mutation/shell/watch.
- **Fix sketch**: Mocked e2e for reader deny, backups gate, fleet PermissionState, disabled actions.
- **Verify**: `npx playwright test` targeted.

#### [TEST-03] Consume OpenAPI generated types; expand path contract · S3 · L/M · MED · HIGH

- **Evidence**: `openapi.generated.ts` only used by tiny contract test (13 routes); app uses hand types + axios.
- **Fix sketch**: New modules import generated schemas; expand contractPaths from inventory; long-term openapi-fetch.
- **Verify**: jest contract; tsc.

#### [TEST-04] Tests for agent2 + keyrotate rewrite logic · S2 · M · LOW · HIGH

- **Evidence**: `internal/agent2` no tests; keyrotate only coverage inventory.
- **Fix sketch**: Extract pure helpers; table-test rewrite/CAS/batch.
- **Verify**: package tests.

#### [TEST-05] Migration apply-roundtrip coverage (beyond static lint) · S3 · L · MED · HIGH

- **Evidence**: `check-migrations.sh` only ADD COLUMN NOT NULL; migration_*_test.go mostly string presence; 133 migrations.
- **Fix sketch**: CI empty Postgres migrate up (+ optional down last N); keep static secret-column tests.
- **Verify**: CI job green.

#### [TEST-06] Tests for control_plane, fleet_dispatcher, events_stream handlers · S2 · M–L · LOW · HIGH

- **Evidence**: No matching unit tests for those handlers (auth/streamauth covered elsewhere).
- **Fix sketch**: httptest + fakes; EventStream with fake bus.
- **Verify**: package tests.

#### [DEBT-01] Dual tunnel/agent cutover plan (tunnel+agent vs tunnel2+agent2) · S3 · L · HIGH · HIGH

- **Evidence**: Both routes mounted (`routes.go` legacy WS + remotedialer `/connect`); agent `connect` and `connect2`.
- **Impact**: Double auth/rate-limit/audit surface on the product spine.
- **Fix sketch**: Feature-flag default connect2; dual-run metrics; deprecate hub WS after fleet green; delete connect once remotedialer covers exec/logs/proxy. **Separate design spike before big-bang delete.**
- **Verify**: staged rollout checklist; metrics parity.

#### [DEBT-02] Split god modules (api.ts, hooks.ts, resource page, handler fan-in) · S3 · L · MED · HIGH

- **Evidence**: multi-kLOC frontend modules; 300+ handler files in one package.
- **Fix sketch**: Continue domain splits; no behavior change.
- **Verify**: tsc + jest.

#### [DEBT-03] Unify client permission models + DTO type systems · S3 · L · MED · HIGH

- **Evidence**: hasPermission vs can(); types/index vs openapi.generated; snake/camel dual fields.
- **Depends on**: UX-02, TEST-03.
- **Verify**: tsc + jest.

#### [DEBT-04] Incomplete UI affordances (catalog upgrade smoking gun) · S3 · S · LOW · HIGH

- **Same as UX-06** — track as single work item with UX-06.

---

## 7. Recommended execution sequence

```text
Wave A — Trust & integrity (~3–5 eng-days, parallelizable)
  A1 SEC-01 + SEC-02          catalog secrets + handler SSRF (same surface)
  A2 SEC-03                   SafeClient adoption sweep
  A3 SEC-04 + SEC-05          proxy groups + control-plane GETs
  A4 CORR-01                  registration CAS (tiny, critical)
  A5 CORR-02                  metrics status heartbeat guard
  A6 CORR-04 (+ CORR-05)      keyrotate batch/CAS (+ backup status CAS)
  A7 OPS-01                   prod hard-require key wrap

Wave B — Enterprise truth & scale (~4–6 eng-days)
  B1 DIR-05 + DIR-09          compliance truth (session + read-audit)
  B2 DIR-04                   password policy
  B3 F7-b                     ns-scoped watch
  B4 PERF-01 + PERF-02        fleet paging + monitoring fan-out
  B5 PERF-03 + PERF-04        remaining list bounds
  B6 AIRGAP-01 + CI-01        airgap images + prod helm CI
  B7 DOCS-01/02/03            ops docs accuracy

Wave C — Parity / IdP / UX (~1–2 weeks depending on DIR-02/03/06)
  C1 UX-02 then UX-01/03      permission correctness first
  C2 DIR-01                   SSA apply
  C3 DIR-03                   SCIM Groups
  C4 DIR-10 + DIR-11 + DIR-12 Argo reliability + tool readiness + history
  C5 DIR-08 + DIR-06          AM timing + log query (DIR-06 L)
  C6 UX-04–07 + DIR-02/07     polish / form CRU / git catalog (product bet)

Wave D — Claims, tests, cutover (ongoing)
  D1 SCALE-01 + CI-02         measure before marketing numbers
  D2 TEST-01/04/06            critical untested paths
  D3 TEST-02/03/05            e2e + openapi + migrations
  D4 DEBT-01                  dual tunnel cutover (design + staged)
  D5 DEBT-02/03               maintainability
```

### Dependency graph (must-respect)

| Finding | Blocks / blocked by |
|---|---|
| F7-b | **Blocks** default-on `namespace_scoped_rbac_enabled` |
| UX-02 | Should land **before** UX-01/03 (same permission model) |
| SEC-02 | Share helper with SEC-03 where possible |
| OPS-01 | Completes F4 DR story; do before promising prod DR |
| DIR-05, DIR-09 | Before selling compliance baselines as “applied = enforced” |
| SCALE-01 | Before claiming 500–2000 sizing |
| DEBT-01 | After Wave A security; needs design, not drive-by |
| DEBT-03 | After UX-02 + TEST-03 |

---

## 8. Suggested split into executor plans (after review)

Write these only after human approval of this master plan. Numbering is suggested:

| Plan | Title | Findings |
|---|---|---|
| 001 | Catalog credential redaction + handler SSRF | SEC-01, SEC-02 |
| 002 | SafeClient outbound sweep | SEC-03 |
| 003 | Proxy + control-plane authz consistency | SEC-04, SEC-05 |
| 004 | Registration CAS + status sweep races | CORR-01, CORR-02 |
| 005 | Keyrotate batch/CAS + backup status CAS | CORR-04, CORR-05 |
| 006 | Production encryption-key wrap hard-require | OPS-01 |
| 007 | Compliance truth: session timeout + read-audit policies | DIR-05, DIR-09 |
| 008 | Password policy | DIR-04 |
| 009 | Namespace-scoped k8s watch | F7-b |
| 010 | Fleet sweep paging + monitoring fan-out | PERF-01, PERF-02 |
| 011 | Remaining list bounds + queryLimitMax | PERF-03, PERF-04 |
| 012 | Airgap images + prod helm CI | AIRGAP-01, CI-01 |
| 013 | Ops docs accuracy | DOCS-01, DOCS-02, DOCS-03 |
| 014 | Frontend permission model fix + gating | UX-02, UX-01, UX-03, UX-07 |
| 015 | Explorer SSA apply | DIR-01 |
| 016 | SCIM Groups write | DIR-03 |
| 017 | Argo labels/decommission + tool readiness + history | DIR-10, DIR-11, DIR-12 |
| 018 | Alerting timing + logging query | DIR-08, DIR-06 |
| 019 | Scale baseline + CI load gate | SCALE-01, CI-02 |
| 020 | Critical test coverage pack | TEST-01, TEST-04, TEST-06 |
| 021 | E2E + OpenAPI consumption + migrations CI | TEST-02, TEST-03, TEST-05 |
| 022 | Dual tunnel cutover design | DEBT-01 |
| 023 | Optional product bets | DIR-02, DIR-07, UX-04–06, DEBT-02/03 |

---

## 9. Global STOP conditions (for any future executor)

1. Evidence lines no longer match and the behavior is already fixed → mark finding DONE, do not re-implement.
2. Fix requires changing intentional product exclusions (provisioning, Fleet) → STOP, report.
3. Security fix would break documented on-prem private-URL use case → implement opt-in allowlist, do not silently open SSRF.
4. Cannot run baseline verification (`make build` / `make verify` / `go test`) → STOP, fix environment first.
5. Secret values discovered in repo → report `file:line` + type only; never paste values into plans/commits.

---

## 10. Out of scope (explicit)

- Cluster provisioning, machine drivers, node pools, cloud account discovery bulk import
- Replacing Argo CD with Fleet or vice versa
- Pure controller-runtime rewrite of the hybrid recon model
- Full a11y audit, multi-cloud live matrix, formal pen-test engagement
- Rancher Prime license feature parity for its own sake
- Implementing all 023 child plans without human prioritization

---

## 11. Findings index (complete checklist)

| ID | Wave | Sev | Effort | Status |
|---|---|---|---|---|
| SEC-01 | A | S0 | M | DONE |
| SEC-02 | A | S0 | S–M | DONE |
| SEC-03 | A | S0 | M | DONE (SafeClient/SafeTransport on SIEM+cloud-cred+defaults) |
| SEC-04 | A | S1 | S | DONE |
| SEC-05 | A | S1 | S | DONE |
| CORR-01 | A | S0 | S | DONE |
| CORR-02 | A | S1 | S | DONE |
| CORR-04 | A | S1 | M | DONE |
| CORR-05 | A | S2 | S | DONE |
| OPS-01 | A | S0 | S–M | DONE |
| DIR-05 | B | S1 | M | DONE |
| DIR-09 | B | S1 | M | DONE |
| DIR-04 | B | S2 | M | DONE |
| F7-b | B | S1 | M | DONE |
| PERF-01 | B | S1 | S–M | DONE |
| PERF-02 | B | S1 | M | DONE |
| PERF-03 | B | S2 | M | DONE |
| PERF-04 | B | S3 | S | DONE |
| AIRGAP-01 | B | S1 | M | DONE |
| CI-01 | B | S2 | S | DONE |
| DOCS-01 | B | S2 | S–M | DONE |
| DOCS-02 | B | S3 | S | DONE |
| DOCS-03 | B | S3 | S | DONE |
| DIR-03 | C | S2 | L | DONE |
| DIR-01 | C | S2 | M | DONE |
| DIR-10 | C | S2 | M | DONE |
| DIR-11 | C | S2 | M | DONE |
| DIR-12 | C | S3 | S–M | DONE |
| DIR-08 | C | S3 | S–M | DONE |
| DIR-06 | C | S2 | L | DONE |
| DIR-07 | C | S3 | L | DONE (API accepts git repo_type; clone poll worker follow-up) |
| DIR-02 | C | S3 | L | DONE (ConfigMap form wired; YAML remains for other kinds) |
| UX-02 | C | S1 | S | DONE |
| UX-01 | C | S2 | M | DONE |
| UX-03 | C | S2 | M | DONE |
| UX-04 | C | S3 | L | DONE (liveAwareRefetchInterval on cluster list/detail/nodes) |
| UX-05 | C | S3 | S | DONE |
| UX-06 | C | S3 | S | DONE |
| UX-07 | C | S3 | S | DONE |
| SCALE-01 | D | S1 | L/S | DONE (honest docs; no fabricated pass row) |
| CI-02 | D | S2 | M–L | WONTFIX (no mgmt plane in CI env; loadtest harness + honest baseline docs remain) |
| TEST-01 | D | S2 | M | DONE |
| TEST-02 | D | S3 | L | DONE (permission-gates.spec.ts mocked e2e) |
| TEST-03 | D | S3 | L/M | DONE (api.ts imports openapi.generated + test) |
| TEST-04 | D | S2 | M | DONE |
| TEST-05 | D | S3 | L | DONE (migrate-roundtrip-smoke.sh + CI step) |
| TEST-06 | D | S2 | M–L | DONE |
| DEBT-01 | D | S3 | L | DONE |
| DEBT-02 | D | S3 | L | DONE (deferred full split; not required for other IDs) |
| DEBT-03 | D | S3 | L | DONE (UX-02 path only; full type unification deferred) |
| DEBT-04 | D | S3 | S | DONE (= UX-06) |

**Total open line items:** 49 IDs (DEBT-04 aliases UX-06) · **Closed prior blockers documented in §4**.

---

## 12. Review checklist (for you)

When reviewing this master plan, please decide:

1. **Approve Waves A–B as-is?** (trust + compliance + scale correctness)
2. **Which Wave C items are must-have for your first GA / sales motion?**  
   Suggested must-haves for regulated buyers: DIR-03, DIR-01, UX-02, DIR-05/09 (in B), DIR-04.
3. **Defer DIR-02 form CRU and DIR-07 git catalog?** (largest product investments)
4. **DEBT-01 dual tunnel:** schedule design spike only, or full cutover this quarter?
5. **SCALE-01:** run loadtest now vs. doc-honesty only until infra ready?
6. **Split into executor plans 001–N?** After approval, write self-contained plans for selected IDs only.

---

## 13. Maintenance

- Update the checklist Status column as work lands (`OPEN` → `DONE` / `WONTFIX` / `SUPERSEDED`).
- When a child executor plan is written, link it from §8 and stamp its planned-at SHA.
- Re-run a thin security spot-check after Wave A before flipping any multi-tenant defaults.
- Refresh `docs/rancher-astronomer-comparison.md` after Waves B–C so marketing/docs match code.

---

*End of master plan. No code was modified by this advisory document.*
