# Plan 001: Residual enterprise HA, SSRF completion, and Rancher day-2 parity

> **Audience**: Human review first; then split into numbered executor child plans
> (`002-…`) only after approval of waves and sequencing.
>
> **Repo root for execution**: `/root/astronomer-all/astronomer` (the `astronomer`
> git checkout). Product plans live under `plans/`; advisor plans live here under
> `advisor-plans/`.
>
> **Relationship to Plan 000**: Plan
> [000-enterprise-quality-rancher-parity-master-plan.md](./000-enterprise-quality-rancher-parity-master-plan.md)
> captured the 2026-07-08 deep dive. Much of Wave A–C from that plan is **landed
> on disk (often uncommitted)**. This plan **001** is the **2026-07-09 residual
> assessment**: only what remains open after re-verification. Do **not** re-open
> closed items listed in §3 without new evidence.
>
> **Drift check (run first)**:
> ```bash
> git rev-parse --short HEAD
> git status -sb
> git diff --stat 2991f9d -- internal/db/queries/tool_operations.sql \
>   internal/webhook/sender.go internal/handler/catalog_hydrate.go \
>   internal/events/bus.go internal/tunnel2/server.go
> ```
> If HEAD moves or the cited files diverge from the “Current state” excerpts,
> re-spot-check evidence before implementing. Large uncommitted remediation may
> already exist on top of `2991f9d` — prefer reconciling with working tree over
> re-implementing.

## Status

| Field | Value |
|---|---|
| **Priority** | P0 program (residual master) |
| **Effort** | XL overall (~3–6 eng-weeks if parallelized by wave; Wave A alone ~3–5 days) |
| **Risk** | MED–HIGH overall (HA claim SQL is LOW–MED; dual-tunnel cutover and Redis bus are HIGH) |
| **Depends on** | Plan 000 closed items remain closed; uncommitted Wave work should be committed or rebased before long-lived branches diverge |
| **Category** | security · correctness · HA · scale · compliance · parity · ux · tests · debt · docs |
| **Planned at** | commit `2991f9d` + residual tree, **2026-07-09** |
| **Method** | 4 deep parallel residual audits (security, correctness/HA/scale, Rancher day-2 parity, ops/frontend/tests) + parent re-verification of P0/P1 evidence |
| **Scope exclusions** | Cluster provisioning / machine drivers / node pools; Fleet product (Argo CD is the GitOps engine); Rancher Prime commercial entitlements; redesign of Postgres/asynq hybrid architecture |

---

## 1. Why this matters

Astronomer is a day-2 multi-cluster management plane for **already-provisioned**
clusters: outbound agent tunnels, Postgres product state, Argo CD delivery, Helm
chart control plane. Prior remediation closed real launch blockers (authz holes,
keyrotate coverage, catalog redaction, registration CAS, SafeClient on many
sinks, SCIM Groups write scaffolding, SSA apply, etc.).

What remains is the difference between:

- **pilot / multi-user production with ops attention**, and
- **elite unattended enterprise + Rancher-grade operator confidence** for adopted fleets.

The residual gaps are **concrete and finite**:

1. **Multi-replica HA is partially wrong** — ArgoCD ops claim atomically; tool /
   catalog / logging / monitoring / workload ops do not, while every server
   replica runs reconcilers (`server.replicaCount` defaults to **2**).
2. **Operator-URL SSRF is incomplete** — SafeClient won the catalog/SIEM/backup/
   cloud-tester battles; webhooks, chart hydrate, Prometheus, Argo CD, Vault still
   use plain clients.
3. **Dual tunnel stacks drift** — install still ships legacy `connect`; tunnel2
   auth is weaker and not locator-HA.
4. **Enterprise policy UI lies slightly** — password/session settings registry
   exists but handlers hardcode defaults; ns-RBAC must stay default-off until F7-b.
5. **Scale and comparison docs over/under-claim** — no certified load baseline;
   Rancher comparison doc is stale.

Closing this plan does **not** require redesign. It requires finishing patterns
that already exist in-repo (ArgoCD claim SQL, SafeClient, fleet paging helper,
`can()` nav, DR key-wrap plumbing).

---

## 2. Executive verdict (baseline for this plan)

| Question | Baseline (2026-07-09) | Target after this plan |
|---|---|---|
| Production pilot? | Yes | Yes |
| Unattended multi-replica HA at chart defaults? | **No** (CORR-R01) | **Yes** for ops + backups + events |
| Operator-URL SSRF complete? | Partial | Complete with explicit private-allow |
| Dual tunnel single spine? | No (legacy default) | Documented path + auth parity; cutover optional Wave D |
| Rancher day-2 adopted clusters? | ~4.3/5 | ~4.6/5 (UI density still intentional multi-sprint) |
| Measured scale claims? | None | At least one recorded harness pass row |

**Cross-cutting theme:** elite *scaffolding* exists. Remaining work is **closing
optional/incomplete paths so defaults, docs, and multi-replica behavior match
reality**.

**Critical product flag (unchanged):** keep `namespace_scoped_rbac_enabled`
**default OFF** until F7-b (raw k8s-proxy watch namespace filtering) lands and
soaks. This plan includes F7-b but does **not** flip the default without an
explicit product sign-off gate.

---

## 3. Closed items — do not re-open (re-verified 2026-07-09)

| Prior ID / claim | Status | Evidence (disk) |
|---|---|---|
| Catalog repo credential redaction + list RBAC | **FIXED** | `catalog.go` redaction; `catalog_redact_test.go`; routes gated |
| Catalog handler index ingest SafeClient | **FIXED** | `catalog.go` GuardPublicHost + SafeClient |
| Cloud tester / BackupHandler SafeClient constructors | **FIXED** | `cloud_credentials_test_endpoint.go`, `backups.go`, `sec03_safeclient_constructors_test.go` |
| SIEM SafeClientWithTLS | **FIXED** | `siem_dispatch.go`, `siem/transport.go` |
| Registration phase CAS ↔ sqlc | **FIXED** | `registration/service.go` + compile-time assert + `phase_cas_test.go` |
| Metrics heartbeat CAS + full fleet page | **FIXED** | `metrics/publisher.go` |
| Fleet worker sweeps paged | **FIXED** | `worker/tasks/fleet_sweep.go` |
| Privilege API groups proxy = native | **FIXED** | `routes.go` + `privilege_groups_test.go` |
| Control-plane GET RBAC | **FIXED** | `routes_tools_controlplane.go` |
| ArgoCD atomic op claim | **FIXED** | `argocd_operations.sql` status predicate |
| Tunnel locator CAS + multi-replica readyyz | **FIXED** | `tunnel/locator.go`, `server.go` POD_IP |
| Worker leaders / task outbox / decommission lease | **FIXED** | leader package, outbox, decommission SQL |
| OpenAPI CI scaffolding / Trivy / SBOM | **FIXED** (coverage claim nuance → CI-R01) | workflows under `.github/` |
| SCIM Users + Groups create/patch | **FIXED** (DELETE residual → SCIM-R01) | `routes.go`, `scim.go` |
| Frontend can() / partial nav gates / SSA apply / offline banner | **FIXED** (depth residuals remain) | `permissions.ts`, `api.ts`, layout |
| Password **default** policy enforcement | **FIXED** (settings wire-up residual → AUTH-R01) | `password_policy.go` used with `DefaultPasswordPolicy()` |

---

## 4. Commands (verification baseline)

Run from repo root `astronomer/` unless noted.

| Purpose | Command | Expected |
|---|---|---|
| Build | `make build` | exit 0; binaries in `bin/` |
| CI contract | `make verify` | exit 0 |
| Go tests | `make test` or `go test -race -count=1 ./...` | exit 0 |
| Focused package | `go test -race -count=1 ./internal/handler/ ./internal/db/... ./internal/httpclient/ ./internal/webhook/ ./internal/tunnel2/ ./internal/events/` | exit 0 |
| Vet | `go vet ./...` | clean |
| Migrations lint | `make check-migrations` | OK |
| sqlc regenerate | `make sqlc` (or repo’s documented sqlc target) | generated files match |
| Chart tests | `go test ./deploy/ -count=1` | ok |
| Frontend unit | `cd frontend && npx jest` | all pass |
| Frontend types | `cd frontend && npx tsc --noEmit` | clean |
| E2E (when UI wave) | `cd frontend && npx playwright test` (or project script) | pass or documented skip |
| Load baseline | `make load-test LOADTEST_SERVER=... LOADTEST_TOKEN=...` | pass verdict; row in `docs/scale-baseline.md` |
| Grep gate (SSRF) | `grep -RIn 'http\.Client{' internal/handler internal/webhook internal/siem internal/vault internal/monitoring --include='*.go' \| grep -v _test.go` | only allow-listed intentional clients |

---

## 5. Rancher day-2 scoreboard (residual context)

| Dimension | Score | Residual this plan addresses |
|---|:---:|---|
| Inventory & health | 4 | SCALE-R01, CORR-R04 |
| Registration / agent | 4 | SEC-R01, DEBT-R01 |
| Argo CD (not Fleet) | 4 | SEC-R05, optional decommission hygiene |
| RBAC / tenancy | 4 | RBAC-R01 / F7-b (default stays OFF) |
| Auth / SCIM / MFA | 4 | AUTH-R01, AUTH-R02, SCIM-R01 |
| Observability | 5 | SEC-R04; non-Loki 501 is P3 |
| Backup / DR | 5 | CORR-R03, DR-R01 |
| Compliance | 5 | Soft edges documented only |
| Catalog | 4 | SEC-R03, CAT-R01 |
| Tools / shell / apply | 5 | SSA force soft (P3); F7-b watch |
| UI density | 3 | UX-R01–R04 |
| Settings | 4 | AUTH-R01/R02 |
| Audit / SIEM | 5 | SEC-R09 webhook redaction |
| HA / airgap | 4 | CORR-R01/R02/R06, DEBT-R01 |
| API / OpenAPI / CLI | 5 | CI-R01 honesty |

**Intentionally out of scope forever for this product line:** node/cluster
provisioning; Fleet product feature set.

---

## 6. All residual findings (inventory)

Severity for prioritization:

- **P0** — multi-replica wrongness / silent double execution
- **P1** — trust, SSRF residual, HA completeness, enterprise IdP truth
- **P2** — scale honesty, polish, incomplete wiring
- **P3** — debt, docs nuance, long-horizon

Effort: **S** hours · **M** ~1 day · **L** multi-day · **XL** multi-week.

### 6.1 P0

#### [CORR-R01] Atomic `Mark*Running` for non-ArgoCD operations · P0 · S–M · MED · HIGH

- **Why**: Chart defaults `server.replicaCount: 2` (server/worker/frontend). Every
  server process starts tool/catalog/logging/monitoring/workload reconcilers.
  Claim is `UPDATE … WHERE id = $1` with no status predicate → **both pods can
  claim and run the same op** (double Helm install/upgrade/uninstall).
- **Evidence**:
  - Bad: `internal/db/queries/tool_operations.sql` `MarkToolOperationRunning`
    (`WHERE id = $1` only). Same shape:
    `catalog_operations.sql`, `logging_operations.sql`, `workload_operations.sql`,
    `monitoring_operations.sql`.
  - Good exemplar: `internal/db/queries/argocd_operations.sql` `MarkArgoCDOperationRunning`
    with `status = 'pending' OR (status = 'running' AND started_at stale)`.
  - Claim loop: `internal/handler/operation_runner.go` — on mark error, skip; but
    unconditional UPDATE almost never errors.
  - Reconcilers started per server: `internal/server/server.go` (~reconciler start block).
- **Impact**: Double-apply under HA; racing completion/error writes; customer-visible
  tool thrash.
- **Fix** (mirror ArgoCD exactly):
  1. Update all five `Mark*OperationRunning` SQL queries to ArgoCD’s claim predicate
     (pending **or** stale running; reuse the same 1-minute fresh window the
     reconcilers already use for `IsFreshRunning`).
  2. `make sqlc` / regenerate.
  3. Ensure Go claim path treats `pgx.ErrNoRows` as “skip” (already continues on err).
  4. Add contract tests modeled on ArgoCD atomic tests: two concurrent
     `Mark*Running` → exactly one succeeds.
  5. Optional follow-up (Wave C CORR-R06): leader-elect server reconcilers so N
     pods don’t all list pending (correctness is claim; efficiency is leader).
- **Out of scope**: Changing ArgoCD claim semantics; moving reconcilers to worker
  process (nice-to-have, not required for P0).
- **Verify**:
  ```bash
  go test -race -count=1 ./internal/handler/ -run 'Tool|Catalog|Logging|Workload|Monitoring|Operation|Atomic'
  go test -race -count=1 ./internal/db/...
  # manual: deploy server.replicaCount=2, enqueue one tool install, assert single attempt_count increment path
  ```

---

### 6.2 P1 — Security

#### [SEC-R01] tunnel2 connect auth parity with hub A3 / durable tokens · P1 · M–L · MED · HIGH

- **Why**: Two connect surfaces with different credential semantics. Hub enforces
  single-use registration after adoption + durable agent tokens. tunnel2 only
  checks registration token + cluster match.
- **Evidence**:
  - tunnel2: `internal/tunnel2/server.go` authorize path (`GetRegistrationTokenByToken`
    only; no adoption gate / durable hash / rotate).
  - Hub: `internal/tunnel/server.go` A3 / durable validation.
  - SQL comment that CONNECT enforcement lives on hub path (`db/queries/clusters.sql`).
  - Both routes mounted: `internal/server/routes.go` (`/ws/agent/tunnel` and
    `/api/v1/connect` family).
- **Impact**: Spent registration tokens may still authorize tunnel2; durable
  tokens may not work on tunnel2 → cutover risk and auth bypass window.
- **Fix**:
  1. Extract shared `ValidateAgentConnect(ctx, clusterID, token) (kind, err)` used
     by hub and tunnel2 (single package under `internal/tunnel/auth` or similar).
  2. tunnel2: reject post-adoption registration tokens; accept durable hashed
     tokens; audit `token_kind`.
  3. Shared connect failure limiter already exists — keep one view.
  4. Tests: registration after adopt denied on both; durable accepted on both;
     mismatch cluster denied.
- **Depends on**: Do **before** DEBT-R01 install flip to `connect2`.
- **Verify**: `go test -race -count=1 ./internal/tunnel2/ ./internal/tunnel/`.

#### [SEC-R02] Webhook sender → SafeClient · P1 · S · LOW · HIGH

- **Evidence**:
  - `internal/server/server.go` — `webhook.NewSender(nil) // default http.Client`
  - `internal/webhook/sender.go` — nil → `&http.Client{}` (no timeout, no dial guard)
- **Impact**: Highest-value remaining SSRF among operator-configured URLs.
- **Fix**:
  1. `webhook.NewSender(httpclient.SafeClient(30*time.Second))` at server wire-up.
  2. Change default nil client to SafeClient (or refuse nil).
  3. Document optional `AllowPrivateDestinations` for true on-prem receivers if
     product needs it (prefer explicit setting over silent open client).
  4. Unit test: loopback URL denied at dial.
- **Verify**: `go test -count=1 ./internal/webhook/`; constructor test like SEC-03.

#### [SEC-R03] Catalog hydrate SafeClient + catalog:read on values/readme · P1 · S–M · LOW · HIGH

- **Evidence**:
  - `internal/handler/catalog_hydrate.go` — `&http.Client{Timeout: 60s}` chart download
  - Routes for chart values/readme under-gated vs repos (`routes_tools_controlplane.go`)
- **Impact**: SSRF via malicious chart URL in index; any auth’d user may trigger.
- **Fix**:
  1. GuardPublicHost + SafeClient on hydrate (share helper with catalog sync).
  2. Gate GET values/readme/list charts with `catalog:read|list`.
  3. Deny-tests for loopback chart URL.
- **Verify**: `go test -count=1 ./internal/handler/ -run Catalog`.

#### [SEC-R04] Prometheus / dashboard clients SafeClient + private policy · P1 · M · MED · HIGH

- **Evidence**: `internal/dashboards/prom.go`, `internal/monitoring/prometheus.go`
  plain clients; dashboard test may leak upstream errors.
- **Impact**: Operator-configured QueryURL SSRF; TLS skip-verify residual.
- **Fix**:
  1. Use `SafeClient` / `SafeClientWithTLS` for public URLs.
  2. Explicit `allowPrivateBackend` (or in-cluster DNS allowlist) for Prometheus
     inside the management cluster — **document** and default-deny for public-only.
  3. Sanitize admin test error messages (no raw dial internals to clients).
- **Verify**: unit tests for deny public→loopback; allow private when flag set.

#### [SEC-R05] Argo CD HTTP clients SafeClient / internal allow · P1 · M · MED · HIGH

- **Evidence**: `internal/handler/argocd.go`, `internal/handler/argocd/client.go`
  plain clients; optional `InsecureSkipVerify`.
- **Impact**: Stored Argo `api_url` can point at internal hosts with decrypted token.
- **Fix**: Same dual-mode as SEC-R04: SafeClient for external; explicit allow for
  `*.svc` / configured internal CIDRs; keep verify_ssl semantics.
- **Verify**: client unit tests; no regression on in-cluster Argo install path.

#### [SEC-R06] Vault client dial policy · P2 (listed here for security wave) · S–M · MED · HIGH

- **Evidence**: `internal/vault/client.go` TLS client without SafeClient.
- **Fix**: Guard + allow-private flag (Vault is almost always private) —
  **default allow private for Vault only**, but still block link-local/metadata
  if product agrees; document threat model.
- **Verify**: vault package tests.

#### [SEC-R07] Global event SSE per-event RBAC · P2 · M–L · MED · HIGH

- **Evidence**: `internal/handler/events_stream.go` — ticket/JWT only; comment that
  per-event RBAC is not enforced.
- **Fix**: Filter events by `allowsCluster` / resource before write; or
  tenant-scoped channels. Must land **with or after** CORR-R02 bus work so filter
  applies once.
- **Verify**: stream tests with two users, two clusters.

#### [SEC-R08] SafeClient block CGNAT `100.64.0.0/10` · P2 · S · LOW · HIGH

- **Evidence**: `internal/httpclient/ssrf.go` `isPublicIP` uses `IsPrivate` only.
- **Fix**: Deny RFC 6598 + other special-use nets; extend `ssrf_test.go`.
- **Verify**: `go test -count=1 ./internal/httpclient/`.

#### [SEC-R09] Redact webhook delivery payloads · P2 · S–M · LOW · MED

- **Evidence**: `internal/webhook/tap.go` stores `RawJSON` without audit sanitize.
- **Fix**: Run through `audit.SanitizeDetail` / redaction package before insert
  and before Send.
- **Verify**: webhook tests with password-shaped fields.

---

### 6.3 P1 — Correctness / HA

#### [CORR-R02] Cross-replica event bus (Redis pub/sub or streams) · P1 · L · HIGH · HIGH

- **Why**: `events.Bus` is process-local. SSE, registration SSE, webhook tap, SIEM
  tap only see events published on **that** pod. Chart has **no** sessionAffinity.
- **Evidence**: `internal/events/bus.go` (in-memory, buffer 64, drop on full);
  subscribers in `events_stream.go`, `cluster_registration.go`, `webhook/tap.go`,
  `siem/bus_tap.go`.
- **Impact**: Live UI miss; uneven webhook/SIEM delivery under multi-replica.
- **Fix sketch**:
  1. Keep local bus for same-pod fan-out.
  2. On Publish: also `PUBLISH` to Redis channel (or XADD stream) with event JSON
     + id + pod id.
  3. Each server: subscribe and re-inject to local bus with **dedupe** (event id)
     so SIEM/webhooks don’t double-send when both local and remote paths fire —
     prefer: **only the publishing pod** runs SIEM/webhook taps; remote pods only
     feed SSE. Document that choice.
  4. Sequence IDs for clients if needed; backpressure = drop with metric (same as today).
  5. Fail readyyz or warn if multi-replica and Redis unavailable (already required
     for locator).
- **Verify**:
  - Unit: mock redis pubsub inject.
  - Integration: 2 server pods, publish on A, SSE client on B receives.
  - Metric: `events_bus_remote_ingest_total`.

#### [CORR-R03] Backup reconciler claim-before-Velero-apply · P1 · S · LOW · HIGH

- **Evidence**:
  - `internal/handler/backups_reconciler.go` — `applyVeleroBackupForRow` **then**
    `UpdateBackupStarted`.
  - SQL `UpdateBackupStarted` is already status-guarded (`:execrows`) but Go
    ignores rows-affected and applies **before** claim.
- **Impact**: Two pods can both apply Velero CRs for one pending row.
- **Fix**:
  1. Claim first: `rows, err := UpdateBackupStarted`; if rows==0, return.
  2. Then apply Velero; on apply failure, mark failed (or revert to pending with care).
  3. Same ordering for restores if mirrored.
  4. Test: two concurrent reconcilePendingBackup → one apply.
- **Verify**: `go test -race -count=1 ./internal/handler/ -run Backup`.

#### [DEBT-R01] Dual tunnel operational debt (tracked; full cutover Wave D) · P1 · L–XL · HIGH · HIGH

- **Current truth**: Production install template runs **legacy** `astronomer-agent connect`.
  CLI documents `connect2` as preferred. remotedialer has **no** locator
  cross-pod proxy; legacy hub **does**.
- **Evidence**: `deploy/agent/install.yaml.template` command `connect`;
  `cmd/agent/main.go` dual commands; `tunnel2/server.go` local sessions only.
- **Wave B (required before any default flip)**: SEC-R01 auth parity + feature
  matrix doc (what works on connect2 vs connect).
- **Wave D (cutover)**:
  1. Port exec/logs/helm/state-mirror or keep those RPCs on hub until parity.
  2. Locator for tunnel2 **or** document sticky+single-owner limitation.
  3. Feature flag install template `connect2`.
  4. Deprecation timeline for hub.
- **Do not** mark dual-tunnel “DONE” until install default + feature matrix green.

---

### 6.4 P1 — Auth / IdP truth

#### [AUTH-R01] Wire password policy from platform settings end-to-end · P1 · S–M · LOW · HIGH

- **Evidence**:
  - Registry: `platform_settings.go` keys `password.min_length` etc.
  - Handlers always `auth.DefaultPasswordPolicy()`: `users.go`, `auth.go` change-password.
  - Frontend settings hub copy mentions password policy; no form; create-user UI
    min length 8 vs server default 12.
- **Fix**:
  1. `LoadPasswordPolicy(ctx, settingsProvider) PasswordPolicy` with defaults fallback.
  2. Use in CreateUser, UpdateUser password, ChangePassword, password reset, SCIM
     user create/patch password if any.
  3. Platform settings UI section for password.* keys.
  4. Align RBAC create-user client validation with server (min 12 + complexity).
- **Verify**: unit tests override min_length=16; API rejects 12-char if setting 16;
  jest for form min.

#### [AUTH-R02] Session timeout productization · P1 · M · MED · HIGH

- **Evidence**: `session.timeout_minutes` applied at JWT mint only; no idle logout UI;
  platform settings page may omit the key.
- **Fix**:
  1. Expose setting in platform UI; document **absolute access-token TTL** vs
     inactivity (honest copy).
  2. Optional: frontend idle timer that clears session after N minutes no activity
     **and** refuses refresh (if refresh tokens exist — match real auth design).
  3. Do not claim “idle session timeout” unless implemented.
- **Verify**: mint with setting 5 → JWT exp ~5m; UI test optional.

#### [SCIM-R01] Groups DELETE + non-global create mapping · P1 · S–M · MED · MED

- **Evidence**: routes POST/GET/PATCH Groups only; create forces `Scope: "global"`.
- **Fix**: DeleteGroup route + handler; document IdP mapping; optional scope from
  externalId / enterprise extension if product wants project-scoped SCIM later
  (default global is OK if documented).
- **Verify**: `go test -count=1 ./internal/handler/ -run SCIM`.

---

### 6.5 P2 — Scale, DR, multi-tenancy, UX, catalog, CI, docs

#### [CORR-R04] Silent 1000 caps / incomplete internal pagination · P2 · S–M · LOW · HIGH

- **Hot paths**: ops status lists (tools/catalog/workloads/monitoring/argocd),
  alerting summaries, backups retention, **catalog GC** (`catalog_sync.go` —
  versions beyond 1000 never GC’d), Argo auto-register instance lists, anomaly
  rules, projects-by-cluster.
- **Fix**: Reuse fleet/metrics paging helper pattern (`listAllClustersPaged` style);
  loop until short page; add tests that seed >1000 and assert full coverage for GC.
- **Priority order**: catalog GC → Argo auto-register → ops status admin lists.

#### [CORR-R05] Fleet selector unbounded cluster load · P2 · M · MED · HIGH

- **Evidence**: fleet SQL selects all non-decommissioned clusters for selector eval.
- **Fix**: SQL-side label filter and/or paged evaluation; hard safety cap with
  error if exceeded until paging lands.
- **Verify**: unit test with mock large fleet.

#### [CORR-R06] Server reconciler × replicaCount load · P2 · M · MED · HIGH

- **Why**: Even with CORR-R01 fixed, N pods list pending ops and contend on CAS.
- **Fix options** (pick one; prefer least design churn):
  A. Advisory-lock leader for server reconcilers (mirror worker leader), or
  B. Move reconcilers to worker process only, or
  C. Shard by op id hash % replica count (requires stable pod ordinal — fragile).
- **Recommend A** for Wave C.
- **Verify**: metric that only one pod processes pending lists per interval.

#### [DR-R01] Encryption key wrap secret hard-required when backups on · P2 · S · LOW · HIGH

- **Evidence**: `values.yaml` `encryptionKeyBackup.enabled: true` but inert until
  `wrappingSecretRef.name` set; warning only.
- **Fix**: Production preflight / NOTES **fail** (or `values-production` schema
  test) when management backup S3 enabled and wrap secret empty; chart test asserts.
- **Verify**: `go test ./deploy/ -run Preflight|DR|EncryptionKey`.

#### [RBAC-R01 / F7-b] Namespace-filter raw k8s-proxy watch · P2 · L · HIGH · HIGH

- **Why**: Blocks promoting `namespace_scoped_rbac_enabled` default ON.
- **Evidence**: list filter exists; watch fail-closed / unfiltered residual
  (see prod-readiness F7-b notes, `pods_watch` ns-scope vs raw proxy).
- **Fix**:
  1. Namespace-filter watch frames for partial grants (same model as pod watch).
  2. Tests: tenant with ns binding receives only allowed ns events.
  3. Soak; **then** separate product decision to flip default (not automatic).
- **Verify**: handler + e2e permission tests.

#### [UX-R01] Explorer form density beyond ConfigMap · P2 · L · MED · MED

- Expand structured forms: multi-key ConfigMap, Secret, Service ports, Deployment
  scale/image fields; pass **namespace** from picker into forms.
- Keep YAML+SSA path; forms are progressive enhancement.
- **Verify**: jest component tests; optional playwright create ConfigMap multi-key.

#### [UX-R02] Complete cluster nav `permission` metadata · P2 · S–M · LOW · HIGH

- **Evidence**: many sidebar cluster items lack `permission` → unauthorized users
  see dead links → 403.
- **Fix**: Attach `clusters:read` / resource verbs to every item; filter already
  respects metadata when set.
- **Verify**: `permission-gates` e2e extended; unit filter test.

#### [UX-R03] Offline mutations disabled · P2 · S · LOW · HIGH

- Banner exists; mutations still fire.
- **Fix**: Disable primary write buttons / intercept API mutations when
  `!navigator.onLine` (and optionally server unreachable flag).
- **Verify**: jest/react testing library.

#### [UX-R04] Broaden `liveAwareRefetchInterval` · P2 · S · LOW · MED

- Apply to pods, workloads, events, agents, fleet ops hooks.
- **Verify**: hooks tests.

#### [CAT-R01] Git chart repos end-to-end · P2 · M–L · MED · MED

- API accepts `repo_type=git`; no worker clone/index; UI helm/oci only.
- **Fix**: worker poll clone + index path; UI type + branch/path fields; document
  auth_config for git.
- **Verify**: worker task test with httptest git or fixture repo.

#### [CI-R01] OpenAPI coverage honesty + govulncheck + e2e depth · P2 · S–M · LOW · HIGH

- Script fails on **extra** routes not missing; stop claiming “100% coverage” or
  raise bar for critical paths.
- Add `govulncheck ./...` to CI.
- Expand e2e: login smoke, RBAC gate real, YAML apply dry-run, offline banner.
- **Verify**: CI green on PR.

#### [DOC-R01] Refresh Rancher comparison + stub runbooks honesty · P2 · S · LOW · HIGH

- Rewrite `docs/rancher-astronomer-comparison.md` against current code (SCIM,
  MFA, ns bindings, SSA, session TTL, Argo).
- Mark scenario runbooks stub maturity; keep alert-linked set golden.
- Fix dead UI doc links (`/docs/...` vs `/astronomer-docs/`).

#### [SCALE-R01] Record a real load-test baseline · P2 · M · MED · HIGH

- Run harness against real management plane; append pass row to
  `docs/scale-baseline.md`. **No invented numbers.**
- Fix harness connectivity issues first if report was fail-to-dial.

#### [P3 pack] · batch · S–M · LOW · MED

| ID | Item |
|---|---|
| SEC-R11 | Fernet TTL=0 — document only; enforce key rotation runbook |
| SEC-R12 | Legacy plaintext durable token path — force hash-only |
| SEC-R14 | Agent privilege profiles — warn on elevate; keep viewer default |
| UX-P3 | SSA `force: true` → opt-in force after dry-run |
| LOG-P3 | Non-Loki QueryOutput 501 — implement or hide UI |
| PAG-P3 | Handler `TODO(total)` misleading totals |
| COMP-P3 | Compliance soft edges inventory |

---

## 7. Phased delivery program

### Wave A — Trust under HA + cheap SSRF (do first) · ~3–5 eng-days · Risk LOW–MED

| Order | ID | Title | Effort |
|:---:|---|---|:---:|
| A1 | CORR-R01 | Atomic Mark*Running for 5 op types + tests | S–M |
| A2 | CORR-R03 | Backup claim-before-apply | S |
| A3 | SEC-R02 | Webhook SafeClient | S |
| A4 | SEC-R03 | Catalog hydrate SafeClient + RBAC | S–M |
| A5 | SEC-R08 | CGNAT deny in isPublicIP | S |
| A6 | AUTH-R01 | Password policy settings wire-up + UI | S–M |

**Wave A exit criteria**:
- [ ] Concurrent double-claim tests green for tool + catalog (minimum) and all five types preferred
- [ ] Backup unit test claim-first
- [ ] Grep gate: no plain client in webhook/hydrate production paths
- [ ] Password setting change enforced in CreateUser test
- [ ] `go test -race -count=1 ./internal/handler/ ./internal/webhook/ ./internal/httpclient/ ./internal/db/...`
- [ ] `make build` green

**Parallelism**: A3/A4/A5 parallel after A1 starts; A2 parallel to A1; A6 parallel.

---

### Wave B — Auth surfaces + operator-URL SSRF completion · ~1–1.5 eng-weeks · Risk MED

| Order | ID | Title | Effort |
|:---:|---|---|:---:|
| B1 | SEC-R01 | tunnel2 ↔ hub shared connect auth | M–L |
| B2 | SEC-R04 | Prometheus/dashboard SafeClient + private policy | M |
| B3 | SEC-R05 | Argo CD client SafeClient + internal allow | M |
| B4 | SEC-R06 | Vault dial policy | S–M |
| B5 | SEC-R09 | Webhook payload redaction | S |
| B6 | AUTH-R02 | Session setting UI + honest docs (+ optional idle) | M |
| B7 | SCIM-R01 | DeleteGroup + docs | S |
| B8 | DR-R01 | Wrap secret hard-fail in preflight/prod | S |
| B9 | DEBT-R01 prep | Feature matrix doc connect vs connect2 | S |

**Wave B exit criteria**:
- [ ] tunnel2 denies post-adoption registration token (shared tests with hub)
- [ ] Documented private-backend allowlist for Prom/Argo/Vault
- [ ] Chart test fails when backup on + wrap secret empty (prod profile)
- [ ] SCIM DeleteGroup covered
- [ ] `go test -race -count=1 ./internal/tunnel2/ ./internal/tunnel/ ./internal/handler/ ./internal/vault/ ./deploy/`

**Dependency**: B1 before any install default change (Wave D).

---

### Wave C — HA completeness + scale correctness · ~1.5–2.5 eng-weeks · Risk HIGH (bus)

| Order | ID | Title | Effort |
|:---:|---|---|:---:|
| C1 | CORR-R02 | Redis-backed event fan-out + dedupe policy | L |
| C2 | SEC-R07 | Per-event SSE RBAC filter | M–L |
| C3 | CORR-R04 | Page/GC internal 1000 caps (catalog GC first) | M |
| C4 | CORR-R05 | Fleet selector paging/cap | M |
| C5 | CORR-R06 | Server reconciler leader election | M |
| C6 | RBAC-R01 / F7-b | ns-filter watch for partial grants | L |
| C7 | SCALE-R01 | Real load-test pass row | M |
| C8 | CI-R01 | govulncheck + OpenAPI claim fix + e2e depth | S–M |

**Wave C exit criteria**:
- [ ] 2-replica SSE integration test green
- [ ] Catalog GC processes >1000 versions fixture
- [ ] Server leader metric shows single active reconciler set
- [ ] F7-b tests green; **default ns-RBAC still OFF** unless product sign-off
- [ ] `docs/scale-baseline.md` has ≥1 pass row **or** explicit “blocked: …” with reason
- [ ] CI includes govulncheck

---

### Wave D — Product parity polish + tunnel cutover · multi-sprint · Risk HIGH (cutover)

| Order | ID | Title | Effort |
|:---:|---|---|:---:|
| D1 | DEBT-R01 | connect2 cutover when matrix green | XL |
| D2 | UX-R01 | Explorer forms (Secret, multi-key CM, Service, Deploy fields) | L |
| D3 | UX-R02 | Full cluster nav permissions | S–M |
| D4 | UX-R03 | Offline mutation guard | S |
| D5 | UX-R04 | liveAware breadth | S |
| D6 | CAT-R01 | Git catalog worker + UI | M–L |
| D7 | DOC-R01 | Comparison doc rewrite + UI doc links | S |
| D8 | P3 pack | SSA force opt-in, plaintext durable kill, etc. | M |
| D9 | Product gate | Consider ns-RBAC default ON after F7-b soak | decision |

**Wave D exit criteria**:
- [ ] Install template decision recorded (stay on connect **or** flip with matrix)
- [ ] Comparison doc matches code
- [ ] Playwright/e2e permission + apply smoke green
- [ ] No open P0/P1 from this plan without explicit waiver in README

---

## 8. Cross-cutting design decisions (bind executors)

### 8.1 Exemplar: atomic operation claim (ArgoCD)

Copy this shape for all `Mark*OperationRunning` queries:

```sql
-- Exemplar: internal/db/queries/argocd_operations.sql
UPDATE argocd_operations
SET status = 'running', attempt_count = attempt_count + 1,
    started_at = now(), error_message = '', updated_at = now()
WHERE id = $1
  AND (
      status = 'pending'
      OR (status = 'running' AND (started_at IS NULL OR started_at < now() - interval '1 minute'))
  )
RETURNING *;
```

Go claim loop must treat zero rows as skip (not fatal). Prefer `:one` RETURNING
so sqlc surfaces `ErrNoRows`.

### 8.2 Exemplar: SafeClient wire-up

```go
// Prefer:
httpclient.SafeClient(30 * time.Second)
// TLS custom roots (SIEM/Vault pattern):
httpclient.SafeClientWithTLS(timeout, tlsConfig)
// Private backends: explicit option or separate constructor — never silent &http.Client{}
```

Existing constructor tests: `internal/handler/sec03_safeclient_constructors_test.go`.

### 8.3 Event bus dual-path rule (CORR-R02)

**Recommended policy** (minimize double SIEM/webhook):

| Consumer | Local publish pod | Remote inject pod |
|---|---|---|
| SSE / registration SSE | yes | yes |
| Webhook tap | yes | **no** |
| SIEM bus tap | yes | **no** |
| Metrics publisher | already per-pod OK | n/a |

Document in `internal/events/README` or package comment.

### 8.4 Private destination policy matrix

| Sink | Default | Private allow |
|---|---|---|
| Catalog / hydrate / blessed | Public-only SafeClient | No (or superuser override) |
| Webhooks | Public-only | Opt-in setting |
| SIEM | Already SafeClientWithTLS | Existing private path if any — preserve |
| Prometheus / dashboards | Public-only | Opt-in (common) |
| Argo CD | Public-only | Allow in-cluster DNS / flag |
| Vault | **Allow private** by default | Still block link-local & metadata |

### 8.5 Namespace RBAC default

| Gate | Rule |
|---|---|
| During Waves A–C | `namespace_scoped_rbac_enabled` default **false** |
| After F7-b + soak | Product decision only; requires release note + upgrade note |

---

## 9. Testing strategy (program-level)

### 9.1 Unit / race

| Area | Tests to add |
|---|---|
| Op claims | Concurrent double Mark*Running for each op type |
| Backup | Claim-first; second reconciler no apply |
| SafeClient sinks | Dial deny 127.0.0.1 / metadata for webhook, hydrate, prom, argo |
| SSRF CGNAT | `100.64.0.1` denied |
| tunnel2 auth | Adopted registration denied; durable accepted |
| Password policy | Settings override min_length |
| SSE RBAC | Filtered events |
| Event bus | Dedupe remote inject; taps not double-firing |
| Catalog GC | >1000 versions all visited |
| F7-b | Watch frames filtered |

### 9.2 Integration / chart

| Test | How |
|---|---|
| Chart DR preflight | `go test ./deploy/` with production wiring sets |
| Multi-replica SSE | k3d or kind with 2 server pods + redis (optional in CI if expensive — gate behind `INTEGRATION=1`) |
| Agent connect matrix | Existing k3d adoption scripts if present |

### 9.3 Frontend

| Test | How |
|---|---|
| Permission nav | Extend `permission-gates.spec.ts` |
| Password form min | jest |
| Offline disable | jest |
| Forms | component tests for multi-key ConfigMap |

### 9.4 Validation checklist (release sign-off)

```text
[ ] make build && make verify
[ ] go test -race -count=1 ./...
[ ] go test ./deploy/ -count=1
[ ] cd frontend && npx tsc --noEmit && npx jest
[ ] grep gate for plain http.Client on operator-URL packages (allow-list reviewed)
[ ] Documented private-backend settings in chart README / runbook
[ ] docs/scale-baseline.md honest
[ ] docs/rancher-astronomer-comparison.md refreshed
[ ] No P0 open; P1 either closed or waived with owner + date in advisor-plans/README.md
[ ] Working tree remediation committed or PR-linked
```

---

## 10. Suggested executor child plan split

After human approval of this master plan, write child plans (one finding or tight
cluster per file). Recommended numbering:

| Child | Scope | Depends |
|---|---|---|
| `002-atomic-operation-claims.md` | CORR-R01 | none |
| `003-backup-claim-first.md` | CORR-R03 | none |
| `004-safeclient-webhook-hydrate-cgnat.md` | SEC-R02, R03, R08 | none |
| `005-password-policy-settings.md` | AUTH-R01 | none |
| `006-tunnel2-auth-parity.md` | SEC-R01 | none |
| `007-prom-argo-vault-ssrf.md` | SEC-R04, R05, R06 | 004 patterns |
| `008-webhook-redaction-scim-session-dr.md` | SEC-R09, SCIM-R01, AUTH-R02, DR-R01 | none |
| `009-redis-event-bus-sse-rbac.md` | CORR-R02, SEC-R07 | none |
| `010-paging-fleet-selector-reconciler-leader.md` | CORR-R04, R05, R06 | 002 |
| `011-f7b-namespace-watch.md` | RBAC-R01 | none |
| `012-scale-baseline-ci-docs.md` | SCALE-R01, CI-R01, DOC-R01 | none |
| `013-ux-nav-offline-live-forms.md` | UX-R01–R04 | partial can() already |
| `014-git-catalog.md` | CAT-R01 | none |
| `015-tunnel-cutover.md` | DEBT-R01 | 006 + feature matrix |

Executors must use the plan template in
`/root/astronomer-all/.agents/skills/improve/references/plan-template.md`
for each child: self-contained excerpts, STOP conditions, verification gates.

---

## 11. Risk register

| Risk | Mitigation |
|---|---|
| CORR-R01 changes mark semantics break stale-retry | Mirror ArgoCD 1m window exactly; race tests |
| SafeClient breaks on-prem webhooks/Prom | Explicit allow-private settings; release notes |
| Redis bus double SIEM delivery | Taps only on publishing pod (policy §8.3) |
| tunnel2 auth change strands agents | Feature flag; dual-path tests; don’t flip install first |
| F7-b incomplete → tenants miss events | Prefer fail-closed filtered empty over leak |
| Large uncommitted tree conflicts | Commit Wave A–C before long child branches |
| Scope creep into provisioning/Fleet | Hard exclusion; reject PRs that add drivers |

---

## 12. STOP conditions (any executor / wave)

Stop and report to the human — do **not** improvise:

1. sqlc generation fails or migrations required beyond query file edits for claims.
2. ArgoCD claim exemplar was changed upstream in a way that invalidates copy.
3. Redis is optional in some deploy profiles that still set `replicaCount>1` —
   design conflict for CORR-R02 / locator.
4. Flipping `namespace_scoped_rbac_enabled` default without F7-b green + product OK.
5. Changing agent install default to `connect2` without SEC-R01 + feature matrix.
6. “Fixing” scale by inventing baseline numbers.
7. Re-introducing plain `http.Client` for operator URLs without allow-list comment
   and review.
8. Secret values appearing in logs/tests/fixtures (use redacted placeholders).

---

## 13. Out of scope (explicit)

- Cluster / node / network provisioning, CAPI, cloud machine drivers
- Replacing Argo CD with Fleet or building Fleet-compatible Bundle CRDs
- Full Steve-level form parity for every CRD (UX-R01 is progressive, not complete Steve)
- Pure controller-runtime rewrite of reconcilers
- Redis Cluster support for locator (single-node client assumption stands unless
  separate design)
- Rancher Prime / support entitlements
- Implementing this plan’s code in the advisor session that only writes plans

---

## 14. Live environment note (2026-07-09)

At assessment time, namespace `astronomer` had **no** management-plane pods; leftover
agents in `astronomer-system` were unhealthy (`ImagePullBackOff` / `Error`). Do not
treat a prior `eq-*` image tag as still live without re-deploy. Wave validation that
needs a cluster must **re-install** with multi-replica values.

---

## 15. Acceptance definition — “elite residual closed”

This master plan is **complete** when:

1. All **P0** and **P1** IDs are **DONE** or waived with owner+date in
   `advisor-plans/README.md`.
2. Wave A–C exit criteria checklists are fully ticked in the PR that closes them.
3. `docs/scale-baseline.md` is honest (pass row or explicit empty with harness status).
4. `docs/rancher-astronomer-comparison.md` no longer contradicts SCIM/MFA/ns-RBAC/SSA.
5. Dual-tunnel status is documented as either cut over or “legacy default intentional
   with timeline.”
6. Working tree enterprise remediation is committed or linked to open PRs.

**Not required for “complete”:** full Steve form density; ns-RBAC default ON;
connect2 as install default (those are Wave D product choices).

---

## 16. Appendix A — Finding ID crosswalk

| This plan | Theme | Wave |
|---|---|---|
| CORR-R01 | Atomic op claims | A |
| CORR-R03 | Backup claim-first | A |
| SEC-R02 | Webhook SafeClient | A |
| SEC-R03 | Hydrate SafeClient | A |
| SEC-R08 | CGNAT | A |
| AUTH-R01 | Password settings | A |
| SEC-R01 | tunnel2 auth | B |
| SEC-R04 | Prom SSRF | B |
| SEC-R05 | Argo SSRF | B |
| SEC-R06 | Vault SSRF | B |
| SEC-R09 | Webhook redact | B |
| AUTH-R02 | Session product | B |
| SCIM-R01 | Groups DELETE | B |
| DR-R01 | Key wrap hard-fail | B |
| DEBT-R01 | Dual tunnel | B prep / D cutover |
| CORR-R02 | Redis bus | C |
| SEC-R07 | SSE RBAC | C |
| CORR-R04 | 1000 caps | C |
| CORR-R05 | Fleet selector | C |
| CORR-R06 | Reconciler leader | C |
| RBAC-R01 | F7-b watch | C |
| SCALE-R01 | Load baseline | C |
| CI-R01 | CI honesty | C |
| UX-R01–R04 | UI density/nav/offline/live | D |
| CAT-R01 | Git catalog | D |
| DOC-R01 | Docs | D |
| P3 pack | Misc | D |

---

## 17. Appendix B — Reasoning summary (why this order)

1. **CORR-R01 first**: Chart already multi-replica; every other HA story is
   undermined if ops double-fire. Cheap SQL + tests.
2. **SafeClient cheap wins next**: Webhook + hydrate + CGNAT are high leverage /
   low design risk, finishing the SSRF program customers already partially paid for.
3. **Password settings next**: Enterprise IdP buyers audit this in week one; code
   already half-exists.
4. **tunnel2 auth before cutover**: Prevents “prefer connect2” from becoming a
   security regression.
5. **Prom/Argo/Vault together**: Same private-allow design language.
6. **Redis bus after claims**: Events matter once double-ops stop lying about state.
7. **F7-b before default tenancy**: Correct fail-closed multi-tenant explorer.
8. **Scale measurement before marketing**: Architecture paging without numbers is
   still a claim gap.
9. **UI density last among big rocks**: Important for Rancher switchers, not for
   trust/HA correctness.

---

## 18. Appendix C — Uncommitted work hygiene

Assessment observed a **large dirty working tree** on top of `2991f9d` containing
much of Plan 000 implementation. Before multi-agent execution of this plan:

1. Inventory `git status` and group into logical commits / PRs (security, HA,
   frontend, docs).
2. Ensure CI is green on that base.
3. Re-run drift check on every child plan’s paths.
4. Prefer rebasing child work on the committed base rather than stacking on
   anonymous dirt.

---

*End of Plan 001 — Residual enterprise HA, SSRF completion, and Rancher day-2 parity.*
