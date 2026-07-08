# Production-Readiness Plan — Astronomer as a Rancher Alternative

**Written:** 2026-07-08 · **Against commit:** `a4c04ee` (main) · **Method:** 4-theme
read-only audit (ns-RBAC completeness, scale/load, upgrade/DR, security of the last
~20 commits) with an adversarial verify pass on the security findings. Findings below
were spot-verified against the code by the author before inclusion.

> **Scope.** This plan covers the gap between *"pilot-ready"* (true today) and
> *"unattended production as a Rancher alternative"*. It separates **code/chart fixes**
> from **validation runs** (which need infra). It does **not** re-cover what the audit
> found already-solid (see [Already closed](#already-closed--do-not-re-audit)).

---

## 1. Bottom line / go-no-go

Astronomer is **pilot-ready now** and **not yet unattended-production-ready**. The gap is
concrete and finite: **1 blocker, 5 highs, ~10 mediums**, plus a validation program that
needs modest infra. Nothing here is a redesign.

The four themes ranked by what actually blocks production:

| Theme | Verdict | Headline gap |
|---|---|---|
| **Upgrade / DR** | ❌ Blocker + 2 High | Server/migrate schema-skew can 503 the plane permanently; **backup omits the encryption keys**, so restore-to-new-cluster silently yields undecryptable data. |
| **Security (new surfaces)** | ⚠️ 2 Confirmed | Catalog **sync** path is an unguarded SSRF (my own 012 miss); `GuardPublicHost` is DNS-rebinding-vulnerable. Core authz work verified **sound**. |
| **Scale / load** | ⚠️ 3 High | Worker fleet sweeps run **serially per-cluster** (blow their interval ~100–200 clusters); `?limit` unclamped at ~63 endpoints (memory-amp/DoS). |
| **Multi-tenancy (ns-RBAC)** | ✅ Isolation sound, ⚠️ 1 High UX | Raw-proxy filtering is **closed** (real `nsfilter.go`). But typed live-watch SSE 403s tenants, so live views break with the flag on. |

**Recommended path:** land the *Immediate code fixes* (Phase 0, ~2–3 days, mostly S/M),
then run the *Validation program* (Phases 1–3) on the infra below. Flip
`namespace_scoped_rbac_enabled` on by default only after the multi-tenancy validation +
the live-watch fix.

---

## 1b. Phase 0 status (updated 2026-07-08)

All Phase 0 code/chart fixes are **implemented, gate-verified, and committed to main**
(adversarially reviewed; one regression the review caught was fixed before commit).

| Fix | Status | Notes |
|---|---|---|
| **F1** server↔migrate coupling | ✅ Done | helm render fails on a server/migrate tag skew (+ deploy render tests) |
| **F2** catalog sync SSRF | ✅ Done | guard added to catalog_sync + blessed fetches |
| **F3** DNS-rebinding | ✅ Done | new `httpclient.SafeClient` re-checks the dialed IP |
| **F4** backup keys + drill decrypt | ✅ Done | opt-in wrapped key backup + decrypt-or-fail drill |
| **F5** `?limit` clamp | ✅ Done | ~70 sites swept to `queryLimit`; contract test blocks new ones |
| **F6** worker sweeps | ✅ Done | fan-out + deadlines; **mesh:detect was inert — fixed**; false-resolve regression guarded |
| **F7** live-watch ns-scope | ⚠️ Partial | typed `/pods/watch/` SSE now tenant-safe, BUT see follow-up ★ |
| **F8** bootstrap-secret | ✅ Done | render fails on unpinned prod GitOps password |
| **F8** readyz pending-vs-never | ✅ Done | 503 message now names the skew cause |
| **F8** RBAC cache stampede | ⏳ Deferred | targeted invalidation — security-sensitive, own pass |
| **F8** impersonation dead code | ⏳ Deferred | cosmetic; fold in with the cache fix |

**★ New follow-up (F7-b, HIGH):** fixing `WatchPods` secured the typed SSE, but the
**browser** live view drives the *raw k8s-proxy watch* (`/k8s/{path}?watch=true`), which
still fails closed for namespace-scoped tenants (`routes.go:1339`). To make the tenant
live view actually update, either namespace-filter the raw-proxy watch stream
(`internal/tunnel` `WithNamespaceFilter` path) **or** switch the frontend pod live view to
consume the now-scoped `/pods/watch/` SSE. Do this before flipping the ns-RBAC default.

**Confirmed:** the `mesh:detect` open question — it *was* inert (gated on `status='healthy'`
which no writer sets); F6 fixed it to select active clusters.

---

## 2. Infra to provision

Tiered so you only spin up what a given phase needs. "Fake agents" = a small Go generator
opening N outbound tunnel WS connections that answer heartbeats/metrics/proxy round-trips —
it lets us simulate 100–200 adopted clusters against one management plane without 200 real
clusters. Most validation is local; **real multi-cloud is only needed for cross-distro
parity.**

| Tier | What to provision | Powers |
|---|---|---|
| **T0 — Local (have it)** | The k3s mgmt cluster + 2–3 k3d adopted clusters on the dev host; MinIO container for S3; dnsmasq container for rebinding tests | Upgrade/DR drills, SSRF suite, ns-RBAC isolation matrix, flag-flip regression |
| **T1 — One load host** | 1 mgmt VM (4 vCPU/8GB: server+worker+Postgres+Redis) + 1 fake-agent VM (200 agents) + 1 k6/Playwright load-gen VM + stub Prometheus/webhook/SIEM receivers | All scale/load tests (alert-eval blowout, tunnel sweeps, `?limit` DoS, DB-pool saturation, outbox drain, frontend QPS) |
| **T2 — Real multi-cloud** | 1 EKS (2 nodes) + 1 GKE (2 nodes) + 1 DOKS (2 nodes), each adopted into the mgmt plane | Cross-distro parity of confined watch/exec/logs/shell; provider apiserver-RBAC/audit/latency realism |
| **T3 — Prod-posture** | External managed Postgres + external Redis/Valkey; a 3-node k3s mgmt cluster | DB-pool/scale realism, external-PG migrate preflight, HA rolling-upgrade behavior |

**Provision order:** T0 first (unblocks Phase 0/1 immediately), T1 for Phase 2, T2+T3 for
Phase 3. AWS/GCP/DOKS are only required at T2/T3.

---

## 3. Phase 0 — Immediate code/chart fixes (no infra; land before validation)

These are small, high-leverage, and don't need a cluster to write. Ordered by leverage.

### F1 · [BLOCKER] Couple server ↔ migrate schema so a skewed deploy can't 503 the plane
- **Why:** This exact failure hit the real deploy this week — bumping the server image
  without bumping the migrate init image left the DB at 131 while the binary required 133,
  and the C-03 `/readyz` schema floor held the pod out **permanently** (not transiently).
- **Evidence:** `internal/db/schema_version.go` (`ExpectedSchemaVersion`), `internal/server/readiness.go`
  (the floor), the server Deployment's migrate initContainer + `deploy/chart/templates/migrate-job.yaml`.
  Nothing asserts the server image and migrate image ship the same schema.
- **Fix options (pick one):**
  1. **Version-lock in the chart:** a single `image.tag` drives both server and migrate
     (they're built from the same commit), and a `helm` template assertion / `NOTES.txt`
     check rejects a mismatched override. Cheapest.
  2. **Preflight init:** add an init step that compares the migrate image's max-migration to
     the server's `ExpectedSchemaVersion` and fails fast with a clear message *before* the
     server container starts (so the failure names the cause, not a silent 503).
- **Effort:** M · **Verify:** F1 validation (Phase 1, "N-1→N upgrade with skew trap").

### F2 · [HIGH · security, my own 012 regression] Guard the catalog **sync** SSRF sink
- **Why:** The 012 SSRF work guarded only the interactive `TestRepoConnection`; the primary
  fetch — the background **sync** of a DB-stored repo URL — is unguarded. A stored
  `http://169.254.169.254/...` or an internal Service URL is fetched server-side on every sync.
- **Evidence (verified):** `internal/worker/tasks/catalog_sync.go:100,104` (index.yaml),
  `:262` (chartURL), and `internal/catalog/blessed.go:173,177` fetch via `http.NewRequestWithContext`
  + `client.Do` with **no** `httpclient.GuardPublicHost`. Guard currently only at
  `internal/handler/catalog.go:1309,1341` and `internal/worker/tasks/notification_dispatch.go:401`.
- **Fix:** call `httpclient.GuardPublicHost(url)` immediately before each `client.Do` in
  `catalog_sync.go` and `blessed.go`; on failure return a generic error and abort the sync.
  Add a deny test seeding a repo with a loopback/link-local URL.
- **Effort:** S.

### F3 · [MED · security] Close the DNS-rebinding TOCTOU in `GuardPublicHost`
- **Why:** `GuardPublicHost` resolves the host, then the HTTP client re-resolves and dials —
  an attacker-controlled DNS name can pass the check then rebind to `169.254.169.254`/loopback
  at dial time.
- **Evidence:** `internal/httpclient/ssrf.go` (resolve-only), callers dial with a stock client.
- **Fix:** provide a shared `httpclient.SafeClient()` whose `net.Dialer.Control` re-validates
  the **connked** IP against the same deny rules at dial time (pin/re-check post-resolution),
  and route the guarded sinks through it. Keeps `GuardPublicHost` as a cheap pre-filter.
- **Effort:** M.

### F4 · [HIGH · DR] Back up the encryption/JWT keys with the DB, and make the drill prove decryptability
- **Why:** The nightly backup captures Postgres but **not** the Fernet/JWT key Secret. A restore
  onto a new cluster brings back rows whose agent tokens, SSO secrets, and gitops creds are
  **undecryptable** — and the weekly restore drill never decrypts anything, so it stays green
  while real DR is broken.
- **Evidence (verified):** `deploy/chart/templates/management-plane-backup-cronjob.yaml:91`
  runs `pg_dump` only — no `kubectl get secret`/key capture; the restore-drill cronjob restores
  the DB but does not decrypt a sample column.
- **Fix:** (a) include the encryption-key Secret in the backup artifact (encrypted with a
  separately-held KMS/passphrase, **not** alongside the DB in plaintext); (b) extend the
  restore-drill to decrypt one known encrypted column post-restore and fail if it can't;
  (c) document key custody in the DR runbook.
- **Effort:** M · **Verify:** F4 validation (Phase 1, "backup→restore onto a new cluster").

### F5 · [HIGH · scale] Clamp `?limit` at all list endpoints
- **Why:** ~63 of ~74 handler list sites pass the client `?limit` straight into SQL with no cap,
  including the largest tables (audit_log, alert events, users) — a memory-amplification / cheap
  DoS lever, and it scales with data volume.
- **Evidence:** `internal/handler/response.go:106` (`queryLimitOffset` clamp) is used by only
  ~11 files; the rest use raw `queryInt(r,"limit",…)`.
- **Fix:** sweep raw limit-parsing onto `queryLimitOffset` (cap 200; audit keeps its 500); add a
  contract test that fails if a handler parses `limit` without the clamp.
- **Effort:** M · **Verify:** F5 validation (Phase 2, "unbounded-limit DoS test").

### F6 · [HIGH · scale] Parallelize + deadline the worker fleet sweeps
- **Why:** `alert:evaluate` global PromQL rules (60s cadence) and the tunnel sweeps
  `mesh:detect` / `gatekeeper:policy_apply` (5m) iterate the **whole fleet serially** under a
  single leader lock with per-cluster tunnel round-trips and no aggregate deadline. They blow
  their interval somewhere between 100–200 clusters, and a few slow/disconnected agents stall
  the entire sweep.
- **Evidence:** `internal/worker/tasks/alert_evaluation.go` (per-cluster serial PromQL),
  `mesh_detect.go`, `gatekeeper_policy_apply.go`, `health_check.go` (~5 serial DB round-trips/cluster/60s).
- **Fix:** bounded `errgroup` fan-out (reuse the `resources_search.go` SetLimit(16) pattern) with
  a per-sweep deadline and per-cluster timeout so slow agents don't stall the tick; add an asynq
  task timeout so an overrun can't run 30m while the scheduler keeps enqueuing.
- **Effort:** L · **Verify:** F6 validation (Phase 2, sweep-blowout tests).
- **⚠️ Investigate first:** `mesh:detect` may be **inert** today — it skips clusters whose
  `status != 'healthy'` (`mesh_detect.go:127`) but nothing ever sets `status='healthy'`
  (`health_check.go` only writes `active`/`disconnected`). Confirm; if so this is a functional
  bug to fix regardless of scale.

### F7 · [HIGH · multi-tenancy] Make typed live-watch usable by namespace-scoped tenants
- **Why:** `WatchPods` (the live-watch SSE) is gated by a **cluster-wide** `requirePermission`,
  so a namespace-confined tenant 403s even for a namespace they fully own — the live view breaks
  entirely under multi-tenancy. (Isolation is fail-closed, so no leak — a UX/functionality gap.)
- **Evidence:** `internal/server/routes.go:1020-1023` (cluster-wide gate on WatchPods);
  the raw-proxy watch is path-scoped only and `consumeStreamingResponse` applies no ns filter.
- **Fix:** derive `engine.AuthorizedNamespaces` for the caller and either scope the watch to an
  owned namespace via the path, or open per-namespace upstream watches and filter frames before
  forwarding; replace the cluster-wide route gate with a namespace-scoped one.
- **Effort:** L · **Verify:** F7 validation (Phase 1, two-tenant isolation matrix — watch row).

### F8 · [MED, batch of small hardening]
- **Bootstrap-secret re-roll under GitOps (C-04):** `bootstrap-secret.yaml` `randAlphaNum`
  re-fires on every render → fail the render unless `bootstrap.password` is pinned / add an
  `existingSecret` escape hatch. **S.**
- **`/readyz` "pending vs never":** distinguish "migrations not applied yet" from "applied schema
  can never reach the floor" so a permanent stall doesn't look transient (`readiness.go` +
  `schema_version.go`). **M.**
- **Self-manage revert race:** document that `kubectl set image` is the only safe upgrade path,
  or make `buildSelfManagedAstronomerValues` not clobber an operator values-only change. **M.**
- **ns-RBAC cache stampede:** replace `InvalidateAll()` on project-namespace/role edits
  (`projects.go:264-270,1312,1437`, `rbac.go:112`) with targeted per-project invalidation. **M.**
- **Impersonation dead code:** wire or delete `kubectl_shell_scope.go:251-265`
  `ImpersonationHeaders()` (no prod consumer; misleads auditors). **S.**

---

## 4. Validation program (needs infra)

Each task states infra tier and pass criteria. Write these as repeatable scripts/tests so they
become CI/nightly gates, not one-offs.

### Phase 1 — Upgrade/DR + tenant isolation (T0 local)
- **V1 Fresh install** (T0): all Deployments Available, migrate Succeeded, `/readyz` 200 with
  schema==max & dirty=false, bootstrap login works.
- **V2 N-1→N upgrade + skew trap** (T0, replicaCount>1): clean rolling upgrade; **pre-F1** reproduce
  the 503 to lock the regression, **post-F1** the skew is refused with a clear message.
- **V3 Forced rollback across a migration** (T0): additive migration → N-1 pods Ready against DB@N;
  destructive migration → failure is observable and blocked by lint/doc post-fix.
- **V4 Backup→restore onto a NEW cluster** (T0: 2 k3d + MinIO, +1 remote k3s VM preferred for real
  tunnel reconnect): **pre-F4** decryption fails as predicted; **post-F4** a previously-adopted agent
  reconnects to the restored plane and an encrypted column decrypts.
- **V5 Two-tenant isolation matrix** (T0: mgmt + **2** adopted k3d, ≥4 ns each with pods/secrets/cm
  + a namespaced CRD): every cross-tenant read empty/403; every in-tenant read exactly the tenant's
  objects; cluster-wide admin unaffected — across **typed reads, raw proxy, watch, shell, exec, logs**.
- **V6 Adversarial proxy-filter fuzz** (T0 + apiserver audit on the adopted cluster + a >400KiB ns for
  chunked reassembly): no response leaks an out-of-allow-set object; Table/unparseable/non-List bodies
  yield empty/403; `?watch=TRUE`/casing tricks don't bypass.
- **V7 Flag-flip regression** (T0): superuser + cluster-wide responses byte-identical ON vs OFF;
  project-scoped filtered exactly ON, unchanged OFF.

### Phase 2 — Scale/load (T1 one load host + fake agents)
- **V8 Alert-eval interval blowout:** p95 `alert:evaluate` tick < 60s, zero skipped ticks at 100
  clusters × 5 global rules × 150ms Prom latency; record the (clusters, rules, latency) cliff.
- **V9 Tunnel-sweep blowout** (T1 + 3–5 real k3d for response realism): each sweep < 5m at 100
  clusters with 10% slow agents; record the cluster count where it exceeds 5m and whether one
  disconnected agent stalls it.
- **V10 Unbounded-`?limit` DoS** (T1 + row-seeding): post-F5 no endpoint returns > its clamped page
  regardless of client limit; per-request RSS delta < ~50MB; pre-fix reproduce the blowup.
- **V11 DB-pool saturation** (T1 + k6): p95 API < 500ms and pool acquire-wait p95 < 50ms at target
  concurrency; identify where `MaxConns` (default 25) must rise.
- **V12 task_outbox drain rate** (T1 + event generator + stub receivers): outbox pending depth stays
  bounded and returns to ~0 between bursts; record the sustained rate where backlog grows.
- **V13 Frontend steady-state QPS + refocus storm** (T1 + Playwright): req/min/tab within budget with
  no unbounded growth; registration badge issues zero requests once phase=ready.

### Phase 3 — Real multi-cloud + prod posture (T2 + T3)
- **V14 Cross-distro confined watch/exec/logs/shell** (T2: EKS+GKE+DOKS adopted): isolation identical
  to the k3d baseline on every distro; per-namespace Roles created and enforced (cross-ns kubectl in
  shell denied by the managed apiserver).
- **V15 ns-RBAC expansion at scale** (T3: 3-node k3s + prod Postgres, 30k synthetic bindings seeded in
  DB): p99 authz+filter latency within budget (<200ms); one `InvalidateAll` does not cause a Postgres
  query storm (validates F8 cache fix).
- **V16 SSRF regression suite across ALL sinks** (T0 + fake IMDS + dnsmasq): every sink returns a
  generic failure and never fetches the internal/metadata resource; **V16b** DNS-rebinding: post-F3
  the dial uses the guard-validated IP and the rebind is rejected.
- **V17 Cross-cluster IDOR probe** (T0: 2 adopted k3d A/B): every mutation on an A-owned resource by a
  B-only principal → 403; spoofed body `cluster_id` cannot retarget.

---

## 5. Suggested execution order

1. **Phase 0 fixes** F1–F8 (2–3 days; mostly S/M, F6/F7 are the L's). Land F2/F3 (SSRF) and F5
   (`?limit`) first — smallest, purely defensive.
2. **Provision T0**, run Phase 1 (V1–V7). Gate: DR drill green *including decryptability*, tenant
   isolation matrix 100% clean.
3. **Provision T1**, run Phase 2 (V8–V13). Gate: no sweep blows its interval at your target fleet
   size; `?limit` clamped; pool sized.
4. **Flip `namespace_scoped_rbac_enabled` default** only after V5–V7 + F7 pass.
5. **Provision T2/T3**, run Phase 3 (V14–V17) before declaring unattended-production-ready.

---

## 6. Already closed — do NOT re-audit

The audit specifically confirmed these are **solid**, to save future effort:
- **Raw `/k8s/*` proxy namespace filtering is real and fail-closed** — `internal/tunnel/nsfilter.go`
  + `proxy.go:192-198,576-583` filter cluster-wide LIST bodies to the caller's allow-set; namespace
  resolved from the **parsed path**, never `?namespace=`; watches/mutations/named-GETs for
  cluster-wide-only tenants fail closed.
- **exec/logs/shell thread the concrete namespace** through `CheckPermission`
  (`exec_consumer.go:108-116`, `logs_consumer.go:99-107`); shell provisions genuine per-namespace
  Roles (`kubectl/session.go:216-222`); client `Impersonate-*`/`Authorization`/`X-Remote-*` headers
  are stripped by an allowlist at the agent (`pkg/proxyhdr/proxyhdr.go`).
- **Core authz from this session is sound** — every state-changing `/security`, `/logging`,
  `/catalog`, `/controllers` route is gated; logging authorizes against the resource's **own**
  persisted `clusterID` (no cross-cluster IDOR); the `/controllers` superuser gate fails closed;
  the namespace-binding **escalation guard is non-bypassable** (empty-namespace/cluster-wide grants
  require the caller to hold the perm cluster-wide; route requires global `rbac:create`;
  `enforceNoEscalation` re-checks each rule at the target scope with correct wildcard semantics).
- Request-path fan-outs already fixed: cross-cluster search (bounded errgroup + top-K heap),
  fleet_orchestrate (tick budget + max_concurrent), project-quota (cached), compliance-posture (batched).
- **Rejected security findings** (don't chase): "authz gates fail open on nil RBACEngine" (not real);
  "webhook `last_error` embeds URL" (superuser-only, already plaintext-at-rest).

## 7. Open questions for the maintainer
- **server+migrate packaging:** OK to drive both from one `image.tag` (F1 option 1), or must they
  stay independent artifacts (forces the chart-assertion route)?
- **Frontend tenant reads:** does the UI route *all* tenant reads through the filtered `/k8s/*` proxy,
  or does it call the typed live-watch SSE / typed generic endpoints that 403 for tenants? Determines
  whether F7 alone makes the tenant UI fully usable or whether the typed generic handlers also need
  scoped filtering (currently `LOW`-rated because fail-closed).
- **`mesh:detect` inert?** Confirm the `status='healthy'` gap (F6 note) — is the sweep running at all today?
