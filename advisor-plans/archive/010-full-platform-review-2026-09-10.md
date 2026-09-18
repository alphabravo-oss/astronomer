# Astronomer full platform review — what to fix

> **Archive status (2026-09-16):** REVIEW COMPLETE — its 50 findings were
> decomposed into Plans 011–015. Verified residuals are consolidated into Plan
> 016; completed findings are recorded there and must not be reopened without
> new evidence.

**Date:** 2026-09-10 · **Against commit:** `32100314` (main, clean tree) · **Goal stated by owner:** elite UI/UX, equal-or-better features than Rancher for adopted clusters; no provisioning; Flux instead of Fleet.

**Method.** Recon of README/docs/Makefile/CI, then eight parallel read-only audits (backend correctness, security, frontend correctness+perf, backend perf/scale, tests+DX+deps+docs, tech debt/architecture, UI/UX with screenshot review, Rancher parity against `/root/astronomer-all/rancher` types). Every finding in the priority table was re-opened and confirmed in the code by the lead before inclusion; subagent line numbers that did not hold were corrected or dropped. No source was modified.

**Verification baseline at HEAD.** `go build ./...` green. `go vet ./internal/... ./cmd/...` clean. Frontend `tsc --noEmit` and `eslint --max-warnings=0` clean. `docs/test-quarantine.json` empty. Not run: `go test -race ./...`, Playwright, stateful lanes.

**Counts.** ~458K non-test Go LOC (99K of it in one `internal/handler` package), ~121K non-generated frontend TS/TSX, 754 mounted routes, 202 route files, 61 `internal/*` packages.

---

## 1. Bottom line

Astronomer is a **strong, unusually well-hardened day-2 platform with a competent but not elite UI**. Compared to Rancher for adopted clusters it is **ahead on delivery governance, credential discipline, identity/evidence surfaces, and management-plane transparency**, and **behind on the daily-driver explorer UX** (no global namespace filter, no bulk actions, no persisted prefs, no custom-role editing in UI, no per-target delivery overrides, no IdP principal search).

The UI today is "solid internal tool, trending competitive." The design system is real; its *application* is inconsistent, density is wasted, mobile is broken, and several visible defects (`NaN undefined`, "over 56 years ago", lowercase breadcrumbs, native `<select>` chevrons colliding with text, a cluster page that can render with no title) undercut credibility on first contact.

Security: the core RBAC engine, namespace filtering, SSRF posture, IDOR checks and extension proxy all **verified sound**. But there are real authz gaps at the edges: an encoded-path bypass of the k8s-proxy permission classifier, a wildcard ClusterRole for the kubectl shell on a `clusters:update` gate, the service proxy gated on the wrong verb, dev-mode chart defaults that disable every production assertion, and the cross-pod internal door keyed off the Fernet key.

Scale: nothing has been measured (`docs/scale-baseline.md` has no recorded run). Reasoning from code, heartbeat write amplification, an un-leader-gated serial probe sweep capped at 500 clusters, unbounded `agent_connections` growth with no supporting index, and an unindexable audit search are the items most likely to fall over first.

### Fix-first (top 12, by leverage)

| # | Fix | Why first | Effort |
|---|---|---|---|
| 1 | **Canonicalise the k8s-proxy path once** and feed the same string to the RBAC classifier, the Secret-read auditor and the upstream forwarder (SEC-01) | `clusters:read` holder can reach Secrets via percent-encoded path with no audit row | S |
| 2 | **Chart defaults to production** (`env: production`, `debug: "false"`), and run wiring assertions unconditionally (SEC-07, SEC-08) | A default `helm install` disables every boot-time security check and registers a demo route | S |
| 3 | **Scope the kubectl shell to the caller by default** and drop `resources: ["*"]` for Secrets unless `secrets:read` (SEC-02) | `clusters:update` = read every Secret in the cluster, unaudited | M |
| 4 | **One `ClientIP(r)` helper using the existing `TrustedProxyCIDRs`** for login/TOTP rate limit, API-token CIDRs, tunnel connect limiter, audit IP, and `Secure` cookie decision (BUG-01/02/08, SEC-13) | Behind the shipped ingress the whole install shares one 5/min login bucket; token CIDR allowlists are inert; XFF forgeable elsewhere | S–M |
| 5 | **Service proxy on `services:proxy`** (SEC-06); **control-plane snapshot on `nodes:manage`+`pods:exec`** (SEC-10) | Verbs the role catalog advertises gate nothing | S |
| 6 | **Responsive shell + density container** (UX-01, UX-02) | Mobile unusable; 20% of every desktop width discarded | S+M |
| 7 | **Global namespace/project scope + searchable cluster switcher** (UX-04/PAR-02) | The single most-used Rancher control; absent | M |
| 8 | **Bulk actions on explorer lists** (PAR-01/UX-13) | `DataTable` supports it; zero pages use it | M |
| 9 | **Kill the visible defects**: `formatBytes` NaN, epoch-zero dates, breadcrumb slugs, `<select>` padding, cluster H1 fallback, sticky save bar (UX-05/06/08/12/14/23) | Each is S; together they are the "unfinished" signal | S |
| 10 | **Leader-gate + page + fan-out the cluster probe sweep**, leader-gate the live-metrics publisher, index `agent_connections(cluster_id, connected_at DESC)` + retention (PERF-03/04/06) | Silent staleness past ~50 clusters; per-replica duplicate load; slow-motion outage | S each |
| 11 | **Run one estate-100 / estate-500 load rung** and record it (PERF-01) | Every scale claim is currently unqualified | M |
| 12 | **QueryClient retry predicate via `apiErrorStatus`**; lazy-load Charlie shell; move `clsx` out of `vendor-charts` (FE-01/02/03) | Every 4xx retried twice; ~170 KB gzip on the login critical path for nothing | S |

---

## 2. Priority table (all categories)

Ordered by impact ÷ effort, discounted by confidence and fix risk. Security HIGH-confidence floats up. Section 3+ has the detail.

| # | ID | Finding | Cat | Impact | Effort | Risk | Conf |
|---|---|---|---|---|---|---|---|
| 1 | SEC-01 | k8s-proxy RBAC classifier reads raw (encoded) path, forwarder uses decoded path | Security | Secret read w/o `secrets:*`, no audit | S | LOW | HIGH |
| 2 | SEC-07 | Chart ships `env: development`, `debug: "true"`, `allowedHosts: "*"`; production assertions never run | Security | All boot-time guards off by default | S | LOW | HIGH |
| 3 | SEC-02 | kubectl shell ClusterRole `*/*` on `clusters:update`; `feature.shell_scope_to_caller` default false | Security | Cluster-wide Secret read outside audit | M | MED | HIGH |
| 4 | SEC-08 / DEBT-07 | Nil-dependency fail-open in stream auth, exec/logs consumers, hub validator, `requireAuth`/`requirePermission`/`featureGate` | Security | Wiring regression = unauthenticated exec | M | LOW | HIGH |
| 5 | BUG-01 / BUG-02 / BUG-08 / SEC-13 | Three client-IP derivations; login limiter keyed on ingress IP; token CIDRs inert; XFF trusted from any peer for audit/tunnel limiter/`Secure` cookie | Sec+Bug | Fleet-wide login lockout; bypassable limiter; forgeable audit IP | S–M | MED | HIGH |
| 6 | SEC-06 | Service proxy gated on `clusters:read/update`, not `services:proxy` | Security | Advertised verb gates nothing | S | MED | HIGH |
| 7 | SEC-09 | Cross-pod internal k8s/helm door PSK = SHA-256(Fernet key); no RBAC after PSK | Security | Fernet key leak = full API on every cluster | M | MED | HIGH |
| 8 | SEC-03 | SCIM tokens never expire/revoke; SCIM mounted outside audit/rate-limit | Security | Permanent, unaudited user/group control | M | LOW | HIGH |
| 9 | SEC-04 | TOTP failures don't count toward lockout; challenge JWT replayable 5 min | Security | 2FA brute-force at 5/min/IP/replica | M | MED | HIGH |
| 10 | SEC-10 | Privileged `nsenter` snapshot Job on `clusters:update` only | Security | Root on control-plane node | S | LOW | HIGH |
| 11 | SEC-11 | `golang.org/x/crypto` v0.55.0 with reachable SSH DoS advisories | Deps | Wedged resolver from hostile git source | S | LOW | HIGH |
| 12 | SEC-12 | GitOps `path_prefix` joined without containment check | Security | Superuser → worker-pod file read | S | LOW | HIGH |
| 13 | SEC-05 | Hosted-Loki query trusts `X-Grafana-User`; NetworkPolicy has no `from` | Security | In-cluster pod reads all tenants' logs | M | MED | MED |
| 14 | UX-01 | Sidebar never collapses; 8 broken mobile baselines committed | UX | Unusable on phone | M | MED | HIGH |
| 15 | UX-02 | `px-[10%]` content padding | UX | Density inverted vs Rancher | S | LOW | HIGH |
| 16 | UX-04 / PAR-02 | No global namespace filter; cluster switcher is native `<select>` | UX/Parity | Core Rancher control missing | M | MED | HIGH |
| 17 | PAR-01 / UX-13 | Bulk actions & column persistence built, unused (0 / 1 prod site) | Parity | N single-row ops for every batch task | M | MED | HIGH |
| 18 | UX-05/06/08/12/14/23 | Visible defects: `NaN undefined`, "56 years ago", slug breadcrumbs, select chevron overlap, titleless cluster page, sticky bar covers field | UX | Credibility | S | LOW | HIGH |
| 19 | PERF-03 | Cluster probe sweep: serial, `Limit: 500`, not leader-gated | Perf | Stale conditions past ~50 clusters | S | LOW | HIGH |
| 20 | PERF-06 | `agent_connections` unbounded; no `(cluster_id, connected_at DESC)` index | Perf | O(history) query every 15 s per replica | S | LOW | HIGH |
| 21 | PERF-04 | Live-metrics publisher on every replica, `SELECT *` fleet every 10–15 s | Perf | 30 wide-row fleet scans/min at 3 replicas | S | LOW | HIGH |
| 22 | PERF-02 | 6 statements per heartbeat; `last_heartbeat` indexed → no HOT updates | Perf | Dominant write load, table bloat | L | MED | HIGH |
| 23 | PERF-07 | Audit search: leading-wildcard LIKE, JSONB predicates, no `created_at`-led index, duplicate count | Perf | Largest table, most likely DB outage | M | MED | HIGH |
| 24 | PERF-01 | No recorded scale baseline | Perf | All perf ordering is unmeasured | M | LOW | HIGH |
| 25 | FE-01 | Retry predicate string-matches "401"; never fires | FE bug | Every 4xx ×3, triple refresh storms | S | LOW | HIGH |
| 26 | FE-02 / FE-03 / FE-04 | `clsx` inside `vendor-charts` (109 KB gz on login); Charlie shell + markdown eager (61 KB gz); per-chunk budget blind to eager closure | FE perf | ~560 KB gz eager | S | LOW | HIGH |
| 27 | FE-05 / FE-06 | Shell connect not cancellable on unmount; reconnect doesn't cancel prior attempt | FE bug | Orphaned in-cluster pods / exec slots | S | LOW | HIGH |
| 28 | FE-07 / UX-03 | 11 routes render empty state on query error; `StatePanel` used in 5/180 routes; permission state absent on 28/40 | FE/UX | 403 and 500 look like "nothing here" | M–L | LOW | HIGH |
| 29 | PAR-03 / PAR-04 | Custom roles: no edit/delete in UI; 37 templates with no apply path | Parity | Rancher RoleTemplate workflow impossible | M | MED | HIGH |
| 30 | PAR-05 | No per-target delivery overrides (Fleet `targetCustomizations`) | Parity | One bundle version per env variant | L | MED-HIGH | HIGH |
| 31 | PAR-06 | Binding picker = local users, capped at 200; no IdP principal search | Parity | Can't grant before first login | M | MED | HIGH |
| 32 | PAR-07 / UX-22 | No server-side user prefs, favorites, cluster badge/colour, landing page | Parity/UX | Prod/dev tabs look identical | M | LOW | HIGH |
| 33 | BUG-03 | JWT validation cache never evicts | Bug | Slow leak on hottest path | S | LOW | HIGH |
| 34 | BUG-04 / PERF-12 | 45 offset sites unclamped; 120 `:many` queries without LIMIT; workloads `limit` passthrough | Bug/Perf | 500s on `offset=-1`; OOM at fleet scale | S–M | LOW | HIGH |
| 35 | BUG-07 / BUG-09 | Registration phase CAS + step + publish not transactional; hub connect two-statement race | Bug | Stalled wizard; phantom offline clusters | S–M | MED | HIGH/MED |
| 36 | BUG-06 / BUG-05 | Bare `context.Background()` DB writes on tunnel teardown/ping; unsupervised project task goroutine | Bug | Goroutine pile-up during reconnect storms | S | LOW | HIGH |
| 37 | DEBT-04 | Two full tunnel/agent stacks shipped (`tunnel`+`agent` 20K LOC vs `tunnel2`+`agent2` 500 LOC) | Debt | Two auth paths to keep in sync | L | HIGH | HIGH |
| 38 | DEBT-05 / DEBT-06 | Feature-gate default hardcoded `true` ignoring registry; orphan flags; `namespace_scoped_rbac_enabled` comments say OFF, default is ON | Debt | New route ships feature on by accident | S | MED | HIGH |
| 39 | DEBT-02 / DEBT-15 / DEBT-14 | 28 identical `execute*Mutation` clones; 6 cluster-ID helpers at 83 sites; 7 pagination helpers | Debt | Audit invariant re-stated 28×; tenant-scoping preamble hand-rolled | M | MED | HIGH |
| 40 | DEBT-09 | Complexity ratchet: 6/7 ceilings stale, changed-files-only scope | DX | 60 Go files over budget, invisible | S | LOW | HIGH |
| 41 | DOC-01 / DEBT-11 / DOC-02 | Runbooks and Charlie ops doc still say "Argo"; gate misses bare `Argo` and line-wraps; 2,937 lines of Argo plans unmarked; tests split the literal to dodge the gate | Docs | On-call told to use a UI that doesn't exist | S | LOW | HIGH |
| 42 | DX-01 / DX-03 / DX-05 | `.env.example` documents retired Celery/NextAuth stack; `make dev` server on 8001, Vite proxies 8000; Makefile says Next.js; dev version literal `0.3.0-dev` | DX | Documented dev loop does not work | S | LOW | HIGH |
| 43 | DX-02 | No root `AGENTS.md`/`CLAUDE.md` despite 5 non-obvious gates | DX | Every agent rediscovers by failing CI | S | LOW | HIGH |
| 44 | TEST-01 / TEST-02 / TEST-03 | `astro` CLI 3.8% coverage; delivery HTTP layer 36%; source resolver 40% | Tests | Shipped binary and supply-chain input untested | M–L | LOW | HIGH |
| 45 | DEP-01 / DEP-02 / DEP-03 | `js-yaml` high advisory on untrusted-YAML paths; 16 majors behind incl. Tailwind 4, TS 7; no Node pin | Deps | Compounding upgrade debt | S / L | LOW / MED | HIGH |

---

## 3. Security

Verified sound and not re-listed: RBAC engine scope semantics (`internal/rbac/engine.go`), proxy namespace list/watch filtering (`internal/tunnel/nsfilter.go`), SSRF dial-time validation (`internal/httpclient/safeclient.go`, `internal/delivery/resolver/network.go`), nested-ID IDOR checks (404 on cluster mismatch), extension data proxy authz, parameterised SQL everywhere, route-template-only request logging.

### SEC-01 — k8s-proxy authorization path ≠ forwarded path
- `internal/server/routes.go:1022` parses the RBAC object ref from `chi.URLParam(r, "*")`. chi routes on `RawPath` when set (`chi/v5@v5.3.0/mux.go:448`), so this is the **encoded** string. `internal/tunnel/proxy.go:505-516` builds the upstream path from decoded `r.URL.Path`.
- `routes.go:1218-1253` splits on `/` without decoding; unknown resource → falls back to `clusters:read` (`:1367-1375`); `k8sProxySecretReadPermission` (`:1189-1200`) only fires when `resource == "secrets"`, so the mandatory `cluster.secret.read` audit row is skipped too.
- exec/attach/portforward are protected by a decoded-path backstop (`:1030`, `:1549-1562`). Secrets, ConfigMaps, RBAC objects and the privilege-escalation API-group gate are not. No test covers `%2`/`RawPath`.
- **Fix:** one canonicalisation step at the proxy chain entry (unescape, reject `..`/`%2f`, reject if decoded ≠ raw), stash in context, make classifier + auditors + `buildK8sRequestPayload` read that one value; table tests over encoded segments. **S / LOW.**

### SEC-07 — Chart defaults disable production hardening
- `deploy/chart/values.yaml:427-430`: `env: development`, `debug: "true"`, `allowedHosts: "*"`. `internal/config/production.go:113-116` returns nil unless production, so dev-key sentinels, non-TLS DSN, plaintext `server_url`, signed-artifact pinning are all skipped. `internal/server/server.go:272-302` wiring assertions likewise. `routes_long_lived.go:67-72` registers a demo pods endpoint when not production.
- **Fix:** default `env: production`, `debug: "false"`; add a `values-dev.yaml`; chart test asserting `ENV=production`; run the wiring assertions unconditionally. **S / LOW (with a release note).**

### SEC-02 — kubectl shell wildcard ClusterRole
- `internal/kubectl/manifests.go:172-193`: `apiGroups: ["*"], resources: ["*"], verbs: [get,list,watch]` bound cluster-wide. Route gated on `clusters:update` only (`routes_cluster_addons.go:170`). `feature.shell_scope_to_caller` default `false` (`platform_settings.go:187`). Contradicts `docs/rbac-permission-contract.md` ("generic cluster read access is not enough" for Secrets).
- **Fix:** default the scope-to-caller flag on (or remove it); derive an explicit resource allow-list from the caller's grants; exclude `secrets` unless `secrets:read/list/watch`; deny test. **M / MED.**

### SEC-08 + DEBT-07 — Fail-open on nil dependencies
- `internal/auth/streamauth.go:68-72` returns `(uuid.Nil, true)` with nil JWT manager. `internal/tunnel/exec_consumer.go:118-121`, `logs_consumer.go:106-110` return `true` with nil RBAC. `internal/tunnel/server.go:511` skips CONNECT validation with nil validator. `internal/server/routes.go:521-525, 553-569, 604-609, 699-703` return bare `next` when engine/querier/cache nil. Root cause is `RouterDependencies` (108 fields, `routes.go:35`) wired through 285 post-construction setters, so partial wiring is representable.
- **Fix:** invert every nil branch to deny; extend `validateProductionSecurityWiring` to hub validator, exec/logs consumers, ticket store; run it regardless of env. Longer term, required deps become constructor args (DEBT-07). **M / LOW.**

### BUG-01 / BUG-02 / BUG-08 / SEC-13 — Client IP and forwarded-header trust
- `middleware/login_rate_limit.go:214` keys on `RemoteAddr`; `:57` counts every attempt (not just failures) into 5/min. Chart ingress (`deploy/chart/templates/ingress.yaml:33-40`) routes `/api` straight to the server Service, so `RemoteAddr` = ingress pod IP for everyone → **one shared login bucket per replica**. Same limiter on TOTP verify and password reset (`routes_api_entry.go:27,48,59`).
- `internal/auth/api_token_scopes.go:222-231` deliberately reads `RemoteAddr` only (comment explains why), so per-token `allowed_cidrs` are **inert** behind the chart and `last_seen_remote_ip` records the proxy.
- `middleware/audit.go:338-372` `RemoteIPAddr` trusts XFF/X-Real-IP from any peer; used for audit IP and for the tunnel connect-failure limiter key (`tunnel/server.go:189-201`, `tunnel2/server.go:132`) → forgeable and map-inflating.
- `middleware/security_headers.go:41-49` `RequestIsHTTPS` trusts `X-Forwarded-Proto` from anyone → `Secure` cookie attribute (`handler/auth.go:528`) and HSTS decided by the client.
- `cfg.TrustedProxyCIDRs` and `TrustedRealIP` **already exist** (`routes.go:429`, `values.yaml:435-437`) — the three other sites just don't use them.
- **Fix:** one `ClientIP(r)` that walks XFF right-to-left discarding trusted-proxy hops; use for login limiter (count failures only), token CIDRs, tunnel limiter, audit, and `RequestIsHTTPS`; delete `clientKey` and `RemoteIPForRequest`. **S–M / MED** (token CIDRs become live; release note).

### SEC-06, SEC-10 — Wrong verbs on privileged routes
- `routes.go:1256-1266` service proxy checks `clusters:read|update`, never `services:proxy`, contradicting the permission contract. Fix: switch resource/verb, add `services:[proxy]` to intended built-ins, pin in `builtin_roles_contract_test.go`. **S / MED.**
- `control_plane_snapshots.go:618-640` renders `privileged: true, hostPID: true, nsenter --target 1`; route gated on `clusters:update` (`routes_cluster_addons.go:135`). Fix: also require `nodes:manage` + `pods:exec`, mirroring `routes.go:735-742`. **S / LOW.**

### SEC-09 — Internal door keyed off the Fernet key
- `tunnel/internal_k8s.go:104-112` `DerivePSK = SHA-256(prefix + ASTRONOMER_ENCRYPTION_KEY)`; `:178-247` forwards arbitrary caller-supplied method/path/body to any cluster's agent with no RBAC; mounted outside JWT chain (`routes_long_lived.go:92-102`). `internal_helm.go:175` twin.
- **Fix:** dedicated chart-generated `ASTRONOMER_INTERNAL_PSK` with dual-accept window; bind requests to a short-lived signed envelope (cluster id + path digest + timestamp). **M / MED.**

### SEC-03, SEC-04, SEC-05, SEC-11, SEC-12, SEC-14, SEC-15
- **SEC-03 SCIM:** `scim_tokens` has no `expires_at`/`revoked_at`/`allowed_cidrs` (`001_initial.up.sql:3692-3699`); `/scim/v2/*` mounted at top level outside audit/rate-limit (`routes_public.go:51-70`); `scim.go` has zero `recordAudit`. Fix: lifecycle columns enforced in `Auth`, `scim.*` audit rows, login-class limiter. **M / LOW.**
- **SEC-04 TOTP:** `totp.go:698-711` bad code → metric + 401 only; `auth.go:712-717` resets failed count on password success before 2FA; challenge JWT valid 5 min with no single-use state. Fix: wire `LockoutQuerier` into TOTP, reset only after second factor, `jti` consumed on use. **M / MED.**
- **SEC-05 Loki:** `lokiauth/auth.go:257-273` identity = `X-Grafana-User` header; admins get all clusters; `monitoring_stack_loki.go:877-895` NetworkPolicy ingress rule has ports but no `from`. Fix: `from:` selectors, bearer/mTLS on query path, strip inbound header. **M / MED.**
- **SEC-11:** `govulncheck` → GO-2026-6354/6355 reachable via `resolver.resolveGitSSH`. Fix: `go get golang.org/x/crypto@v0.56.0`, add govulncheck to CI. **S.**
- **SEC-12:** `gitops_sync.go:604-607` `filepath.Join(dir, prefix)` with no containment check. Fix: `Abs`/`EvalSymlinks` and reject outside `dir`; reject `..` at write time. **S.**
- **SEC-14:** `widget-grid.tsx:219-231, 250-262` iframe `src` unvalidated; CSP has no `frame-src`. Sandboxed, so presentation-level phishing only. Fix: scheme validation server+client, explicit `frame-src`. **S.**
- **SEC-15:** `gitops.go:601-613` one process-wide webhook secret, no body HMAC, no rate limit, unauthenticated route. Fix: per-source encrypted secret, provider-native HMAC + timestamp window, limiter. **M.**

---

## 4. Correctness (backend and frontend)

Backend is unusually clean: SKIP LOCKED leases, SQL CAS on claims, sharded agent map, keyset export, leader-gated periodic tasks. `go vet` clean. Classic patterns (unclosed bodies, unguarded maps, loop-var capture, unchecked assertions) came back empty.

| ID | Defect | Evidence | Fix | Effort |
|---|---|---|---|---|
| BUG-03 | JWT positive-validation cache never evicts expired entries; grows with users × refreshes × uptime | `internal/auth/jwt.go:500-527` | Delete on expired read + janitor ticker (mirror `connect_limiter.go:131`) + cap | S |
| BUG-04 | 45 pagination sites use `int32(queryInt(r,"offset",0))` unclamped → `OFFSET -1` = 500; overflow truncation | `handler/rbac.go:294…`, `security.go:298…`, `backups.go:338…`; helper exists at `response.go:147-160` (11 users) | Replace with `queryLimitOffset`; contract test banning new `queryInt(r,"offset"` | S |
| BUG-05 | `dispatchProjectTask` fires goroutine with `WithoutCancel`, no timeout, no semaphore, no shutdown tracking | `handler/projects.go:1900-1917` | Server-lifetime ctx + timeout + bounded semaphore + wait group | S |
| BUG-06 | Tunnel teardown/ping DB writes use bare `context.Background()`; `Drain` already uses 5 s timeout | `tunnel/server.go:706, 723, 742, 1158` vs `:944` | `WithTimeout(5s)` | S |
| BUG-07 | `registration.Advance`: phase CAS, step insert, SSE publish are three independent writes; error path returns stale pre-transition record that `OnAgentConnected` branches on | `internal/registration/service.go:360-397, 540-544` | Tx runner mirroring `server/mutation_tx.go:17`; publish after commit; return committed record | M |
| BUG-09 | `persistConnect` = disconnect-all + insert, non-transactional, disconnect failure only logged; cross-replica interleave can mark the live session disconnected | `tunnel/server.go:1109-1133` | One transaction / supersede-and-insert query | S |
| BUG-10 | `claimLatestOperations` swallows every `MarkRunning` error (CAS miss and DB failure alike) | `handler/operation_runner.go:83-86` | Distinguish `pgx.ErrNoRows`; log + counter otherwise | S |
| BUG-11 | `parseMemory` handles only `Ki/Mi/Gi` integers → `1.5Gi`, `1Ti`, `2G` become 0 | `handler/workloads.go:2013-2026` | `resource.ParseQuantity` (already a dep); same for `parseCPU` | S |
| BUG-14 | `plaintext_credential_migration` is the one scheduled task with neither leader gate nor row lease; N replicas re-seal the same rows | `worker/task_registry.go:216`; `tasks/plaintext_credential_migration.go:39-57` | `runPeriodicTaskWithLeader`; registry contract test | S |
| BUG-15 | `_ = i.ready.Reconcile(...)` swallowed with no log/metric; `mustPort` → `host:0` on malformed endpoint | `delivery/status/ingester.go:296-301`; `clusters_direct_kubeconfig.go:343-346` | Warn + counter; return `(int,bool)` | S |
| BUG-12/13 | Latent races: leader `release()` guarded by plain bool; `tunnel2.RemoteServer` setters unsynchronised (Hub uses `h.mu`) | `worker/leader/leader.go:51-67`; `tunnel2/server.go:71-77` | `sync.Once`; RWMutex/atomics | S |
| FE-01 | QueryClient retry checks `error.message.includes("401")`; transport puts status on `enriched.status`, message is server text → predicate never fires; every 4xx retried twice; 3 concurrent refreshes | `components/providers.tsx:35-38`; `lib/api/transport.ts:137-149`; helper at `lib/api/errors.ts:46-50` | `apiErrorStatus(error)`: no retry on 4xx except 408/429 | S |
| FE-05 | `ClusterShell` awaits `openShellSession` with no cancel token; unmount during POST orphans the in-cluster pod until idle timeout | `components/clusters/cluster-shell.tsx:108-118, 150-161`; pattern exists in `pod-terminal.tsx:94-102` | `attemptRef` cancel + best-effort close | S |
| FE-06 | `PodTerminal.handleReconnect` doesn't cancel prior ticket attempt → leaked exec WebSocket per double-click | `pod-terminal.tsx:94-95, 262-272` | Cancel previous attempt at top of `connectWebSocket` | S |
| FE-07 / FE-14 | No `throwOnError`; 11 routes render empty/“Cluster not found” on 500; `q.data!` in render branches | `providers.tsx:28-44`; `clusters/$id/registries/index.tsx:75-82`; `settings/vault/index.tsx:74`; `apps/index.tsx:1033` | `throwOnError` for ≥500 + per-page `StatePanel` for 4xx | M |
| FE-09 | Three manual-fetch routes lack cancellation; fast cluster switch paints cluster A's NetworkPolicies under cluster B | `clusters/$id/network-policies/index.tsx:67-88`; `settings/network-policies`, `settings/compliance/baselines` | Migrate to `useQuery` keyed on clusterId (pattern at `settings/templates/$key`) | S |
| FE-11 | Live-tail effect depends on `runQuery` whose deps include `query` → restarts poll and fires Loki query per keystroke; `inFlight` drops instead of debouncing | `logging/-logging-query-dialog.tsx:80, 106-113` | Params in ref, depend on `[liveTail]`, `useDebouncedValue` (react-pacer already installed) | S |
| FE-16 | Queued 401 retries never get `_retry=true` → re-enter refresh on second 401 | `lib/api/transport.ts:97-111` | Set `_retry` before queueing | S |

---

## 5. Performance and scale

**PERF-01 first:** `docs/scale-baseline.md:126-130` records `_none recorded_`; `loadtest-report-20260708.md` is a harness-error fail. The harness (`scripts/loadtest/`, `.github/workflows/scale-certification.yaml`) exists. Run estate-100 then estate-500, capture `pg_stat_statements` top-20, then re-rank the rest of this section from data. **M / LOW.**

| ID | Finding | Evidence | Fix | Effort |
|---|---|---|---|---|
| PERF-03 | Probe sweep: `ListClusters(Limit:500)` then **serial** `probeOne` (2 tunnel RTs, 5 s timeouts each) on a 45 s deadline / 60 s tick; started outside the leader gate on every replica | `server/cluster_probes.go:51-77`; `app_runtime_services.go:31` vs `app_runtime_foundation.go:50-55` | Page + `fanOutClusters`; start under `runServerReconcilerLeader` | S |
| PERF-04 | Live-metrics publisher on every replica; `listAllClusters` every 10 s and 15 s with `SELECT *` (34 cols incl. PEM + 2 JSONB) to read 4 fields | `metrics/publisher.go:198-283`; `queries/clusters.sql:55-58`; `app_runtime_services.go:22` | Narrow projection query; leader-gate | S |
| PERF-06 | `agent_connections` inserts per CONNECT, deleted only at decommission; hot queries need `(cluster_id, connected_at DESC)` — only `(cluster_id,status)`, `(agent_id)`, `(session_id)` exist; lateral runs every 15 s per replica | `queries/agents.sql:7-41`; `001_initial.up.sql:8049-8063`; `tunnel/connection_metrics.go:115-145` | Index migration + nightly retention task (model: `ClusterTombstoneRetentionType`) | S |
| PERF-02 | Per heartbeat: `last_ping` update, `UpdateClusterHeartbeat`, `UpsertClusterHealthStatus`, `MarkRunningAgentUpgradeSucceededByVersion`, `ClaimPendingAgentLifecycleOperation` — 6 autocommit statements; `last_heartbeat` is indexed (`:8539`) so every beat writes a new 34-column tuple + index entry; lifecycle claim has no `status` in its index | `tunnel/handler.go:185-266`; `clusters.sql:187-198`; `agent_lifecycle_operations_ext.sql.go:190-208` | Narrow liveness table or coalesced flush; partial index `WHERE status IN ('pending','running')`; skip claim unless flagged | L |
| PERF-07 | Audit search: `lower(col) LIKE '%…%'` across 7 columns + correlated `EXISTS` on users; `detail->>'cluster_id'` with no expression index; no index leading with `created_at` for the default `ORDER BY created_at DESC`; exact `count(*)` re-runs the predicate | `sqlc/audit_v1_filter_manual.go:58-213`; `001_initial.up.sql:7531-7636` | `created_at DESC` partition-local index; `pg_trgm` GIN where substring search is needed; expression indexes on detail keys; estimated count above threshold | M |
| PERF-13 | Audit CSV export: unbounded `OFFSET`-paged loop in the HTTP handler (O(n²)) | `handler/audit.go:260-367` | Keyset cursor (exists at `compliance.sql:8-25`), bounded range, async job for large exports | M |
| PERF-05 | SSE bus has no per-subscriber filter; every browser tab gets every event then RBAC-evaluates + `json.Marshal`; `cluster.metrics` embeds full node/namespace snapshots; ~270 events/s at 2000 clusters | `events/bus.go:434-465`; `handler/events_stream.go:129-199`; `tunnel/handler.go:529-539` | `Subscribe(ctx, filter)` evaluated before channel send; trim metrics frame | M |
| PERF-08 | N+1: alert sweep `GetClusterHealthStatus` per cluster serially every 60 s; shell list `CountKubectlSessionCommands` per row, admin list unpaginated | `tasks/alert_evaluation.go:651-681`; `handler/kubectl_shell.go:362-372, 458-471` | `= ANY($1::uuid[])` batch queries (pattern at `image_vulns.sql:63-73`) | S |
| PERF-09 | Cross-cluster search: no `limit` sent to k8s API, every cluster's full list buffered before top-K, no fleet deadline, silent 1000-cluster cap | `handler/resources_search.go:26-32, 209-214, 253-276, 360-427` | Push `limit`, stream into heap, request-level deadline with partial results | M |
| PERF-10 | Leader lease pins a pool conn for the task's duration; 25-conn default pool vs 10 worker concurrency × 16 fan-out; every fleet sweep is leader-only so replicas add zero throughput | `worker/leader/leader.go:36-49`; `db/db.go:32-33`; `worker/worker.go:412-419` | Dedicated elector pool; raise worker pool default; shard largest sweeps by `hashtext(cluster_id) % N` | M–L |
| PERF-11 | Delivery agent pushes full status every 15 s and pulls full snapshot every 30 s with no change detection; rollout reconcilers re-read all runtime rows every 5 s | `agent/delivery/runtime.go:21-22, 159-183, 408-425`; `delivery/status/ingester.go:124-160`; `rollout/postgres_reconciler.go:101, 196-199` | Digest-suppressed status with 5-min floor; ingest short-circuit; `updated_at`-gated runtime query | M |
| PERF-12 | 120 `:many` queries without LIMIT, incl. fleet-scaled ones; `workloads.go:927-929` forwards raw client `limit` to k8s; `audit.go:126` offset unclamped | `queries/agents.sql:4-5`, `kubectl_sessions.sql:33`, `cluster_condition_remediation.sql:1`, `delivery.sql:563,944`, `crd_mirror_v2.sql:83` | Bound the fleet-scaled subset; page callers; clamp passthroughs | M |
| PERF-14 | Cluster list: `ListPendingClusterDecommissions(limit = fleetTotal)` filtered in Go per request, plus extra `CountClusters` for scoped callers | `handler/clusters.go:882-935`; index `idx_cluster_decommissions_cluster` exists | `…ForClusters(cluster_ids uuid[])` variant | S |
| PERF-15 | GET handlers do 60 s chart-archive downloads and full support-bundle assembly (pod logs, events, helm) inline; no single-flight | `handler/catalog.go:2145-2187`; `catalog_hydrate.go:141-248`; `supportbundle.go:134-183` | `singleflight` + 10 s deadline; bundle as 202 durable operation (`RespondAcceptedOperation` exists) | M |
| FE-02/03/04 | `clsx` co-located in `vendor-charts` → 109 KB gz recharts on login; `CharlieShell` static import in layout route → 61 KB gz markdown for everyone; per-chunk budget can't see eager closure (~560 KB gz) | `vite.config.ts:6-14`; `routes/dashboard/route.tsx:14, 226-230`; `scripts/check-bundle-budget.mjs:12-32` | `vendor-utils` chunk; `lazy()` + `Suspense fallback={appShell}`; total-eager-gzip budget | S |
| FE-08 | Sidebar count badges fetch full object bodies for 26 resource types incl. every Secret/ConfigMap, every 30 s when SSE down | `components/layout/sidebar.tsx:639-724`; `lib/hooks/kubernetes-proxy.ts:12-19` | Count-only hook/endpoint | M |
| FE-12 | 10 unconditional `refetchInterval`s outside `liveFallback` (topbar 30 s on every page) | `topbar.tsx:150`; `snapshots-page.tsx:176,195,204`; `service-mesh/index.tsx:459,467`; … | Swap to `liveFallback(n)`; add missing SSE event types | S |
| FE-15 | `virtualized` on 2/63 `DataTable`s; global filter stringifies every cell per keystroke | `data-table.tsx:130-141, 365-379` | Profile at 5k rows; precomputed haystack; virtualize where not server-paged | M (MED conf) |

---

## 6. UI / UX

### Verdict from the screenshots (26 current baselines + 17 gallery pages)
- **Tier: solid internal tool → competitive. Not elite.** Clean token system, coherent dark mode, consistent card/tab/table language. The workload detail and cluster detail pages are elite-adjacent.
- **Sameness:** ~60% of pages are the same template (24 px title, grey subtitle, tab strip, search, `Columns`, empty table) with nothing operator-specific.
- **Density is inverted:** `px-[10%]` burns 20% of every width; 7-column tables render in ~830 px of a 1280 px screen with the lower two-thirds empty. Rancher shows more rows and columns.
- **Mobile is broken:** sidebar is a hard 240 px at 375 px; content ~135 px; titles truncate to "Platform …", cards clip mid-word; **8 broken mobile baselines are committed as correct**, so CI defends the bug.
- **Amateur tells:** lowercase slug breadcrumbs (`audit`, `catalog`, `fleet`, `resources`, `shell`); `NaN undefined` on three Monitoring stat cards; "over 56 years ago" on Projects/Agents/Cluster overview; a cluster overview with **no H1** in the current baseline; filter `<select>` chevrons overlapping their labels; cluster switcher text clipped by the version label; a project page whose H1 is the word "Project".
- **Empty states** are one grey sentence with no CTA (RBAC, Alerting, Logging, Fleet, Catalog — the Catalog one even misdiagnoses "adjust your filter" when zero repos are configured). The Workloads page is the lone good example.
- **IA is deep but unscoped:** no namespace filter, native `<select>` cluster switcher, shell is a route not a drawer, Settings is a 23-area / 51-route flat tile grid.

### Findings

| ID | Fix | Evidence | Effort |
|---|---|---|---|
| UX-01 | Responsive shell: off-canvas sidebar below `lg`, hamburger in topbar, full-width `<main>`; regenerate 8 mobile baselines | `routes/dashboard/route.tsx:186-189` (0 breakpoints); `sidebar.tsx:1076` (`w-16`/`w-60` toggle only) | M |
| UX-02 | Replace `px-[10%] py-6` with `px-6 xl:px-8 mx-auto max-w-[1800px]`; `fullBleed` opt-out for tables/logs/YAML | `route.tsx:202` | S |
| UX-04 / PAR-02 | `clusterScope` store (cluster + namespaces[]) in URL + localStorage; topbar namespace combobox; cmdk-backed cluster switcher with status dot and search; `resource-list-page` reads it by default; fails closed for namespace-confined users (`routes_resources_workloads.go:177` gate exists) | `sidebar.tsx:748-769` native `<select>`; `lib/store.ts:72-82` only `commandPaletteOpen`; namespaces endpoint exists | M |
| PAR-01 / UX-13 | Wire `selectable` + `bulkActions` into `resource-list-page.tsx`/core tables with per-item authz, confirm listing exact objects, per-object audit rows, partial-failure table; make `persistKey` auto-derived (1 production site today: `clusters/index.tsx:277`) | `data-table.tsx:97-100, 121-177, 452, 572-582`; only consumer is a unit test | M |
| UX-05 | `formatBytes`: `if (bytes == null \|\| !Number.isFinite(bytes)) return "—"` (siblings `formatCPU`/`formatPercentage` already do) | `lib/utils.ts:40-46`; `cluster-metrics-page.tsx:233-251`; `gallery/dashboard_monitoring.png` | S |
| UX-06 | `formatRelativeTime`: epoch-zero/`0001-01-01`/null → "Never"; overview also passes `undefined` into "of `${podCapacity}` capacity" | `lib/utils.ts:29-35`; `clusters/$id/index.tsx:428, 454` | S |
| UX-08 | Derive `routeLabels` from sidebar nav; title-case fallback; test that every sidebar href segment resolves | `topbar.tsx:41-104` (missing `audit`, `catalog`, `security`, `fleet`, `search`, `resources`, `shell`, `adoption`, `tools`, `apps`, `registries`, `snapshots`, `extensions`, `agents`) | S |
| UX-12 | `Select` gets `pr-8 appearance-none` + rendered chevron instead of reusing input `controlClassName`; later a real combobox (native option menus ignore dark mode) | `ui/input.tsx:5-6`; `ui/select.tsx:9`; `clusters/index.tsx:283-323` | S |
| UX-14 | Cluster overview: use `PageHeader`, `title={displayName \|\| name \|\| id}`, `ActionButton` for Kubeconfig; regenerate 6 baselines | `clusters/$id/index.tsx:217-249`; `cluster-overview-*-chromium-linux.png` (no H1) | S |
| UX-23 | Reserve bottom padding under the sticky settings save bar (it covers "Support URL") | `gallery/dashboard_settings_platform.png` | S |
| UX-03 / FE-07 | `<QueryStates>` wrapper over `StatePanel` (403→permission, network→disconnected, error→error, empty); land on top ~20 routes | `StatePanel` used in 5/180 routes; permission state absent 28/40; error absent on catalog/clusters/workloads/mtls/templates | L |
| UX-09 | Route `DataTable.emptyMessage` through `EmptyState` with required title/description/action; distinguish "no data" from "no results for filter" | `ui/empty-state.tsx`; `EmptyState` in 7 routes; `dashboard_catalog.png` misdiagnosis | M |
| UX-07 | Replace 16 `window.confirm()` sites with `ConfirmDialog` (`confirmValue` + `impact` for irreversible); ESLint ban `confirm`/`alert` in routes | `catalog/-installed-tab.tsx:114`; `security/index.tsx:335,449`; `settings/general/-tokens-tab.tsx:67`; `settings/vault/index.tsx:199`; … (16) | M |
| UX-15 | kubectl shell as a global bottom drawer via the existing `window-manager` (opened from topbar / `Ctrl+\``), scoped to active cluster; `/shell` deep-links into it | `components/window-manager/*` exists with 2 consumers; shell is a full route | M |
| UX-16 (corrected) | **Two detail pages for the same object.** The explorer detail (`/clusters/$id/$resource/$`) has Overview/YAML/Conditions/Events/Related/Rollout/Logs/Exec; the workload route (`/workloads/$kind/$namespace/$name`) is a separate older page with only Pods/Logs/Metrics and its own action bar. Consolidate: make the workload route render the explorer detail with Pods/Metrics as extra tabs | `components/resources/resource-detail.tsx:32-37, 89-112`; `workloads/$kind/$namespace/$name/index.tsx:54-56` | M |
| UX-17 | Unify tables: Workloads page and estate table hand-roll `<Table>` (uppercase headers, native selects, no sort/columns) — 39 files use raw primitives vs 65 `DataTable` | `workloads/index.tsx:375`; `routes/dashboard/index.tsx:211-215` | L |
| UX-18 | Settings: group 23 areas into 4–5 sections with a persistent left sub-nav; index every setting in cmdk; resolve duplicates (`settings/monitoring` = "Shared stacks", `settings/network-policies` vs per-cluster, `/backups` → `settings/backup` redirect) | `routes/dashboard/settings/*` (51 files); `dashboard_settings.png` | M |
| UX-19 | Migrate the 8 highest-risk hand-rolled forms (register, login, change-password, account security, platform, vault, auth settings, security) to `useAppForm`; add `<FormErrorSummary>`; ESLint-ban raw `<input>`/`<select>` under routes (185 / 56 sites) | `lib/form.ts:4-8` (45 adopters vs 34 raw `<form>` route files) | L |
| UX-20 | Registration wizard: real `<Stepper>` (`aria-current="step"`), one form state across steps, inline connect/progress via `OperationTimeline` instead of a separate route | `clusters/register/index.tsx:103` (hardcoded "Step 1 of 3"); `docs/accessibility-release-checklist.md:99-107` | M |
| UX-21 | `OperationTimeline` on fleet ops, backup runs, restores, catalog install/upgrade (3 consumers today; IA doc mandates it for upgrades/restores/syncs) | `ui/operation-timeline.tsx`; `dashboard_fleet.png` | M (verify step events exist) |
| PAR-07 / UX-22 | Server-side `user_preferences` (`GET/PUT /auth/me/preferences`, validated key registry): theme, density, landing route, time format, favourites; cluster `badge_text`/`badge_color` in header; expose `DataTable` `density="compact"` (built, 0 users) | `lib/theme.tsx:31-55` (localStorage only); `data-table.tsx:94,205,264`; `clusters_response.go:29-62` | M |
| UX-10 / UX-11 | Skip link + `<main id="main" tabIndex={-1}>` with focus on route change; global `prefers-reduced-motion` rule (only Charlie honours it today; `animate-fade-in` on every navigation) | `route.tsx:201-202`; `styles/globals.css` (no media query); `accessibility-release-checklist.md:41-42, 177` | S |
| FE-13 | Retire the `next/navigation` shim: 101 files import it, 87 use `href: string` Link, 1 uses typed `Link`; 35 `params.x as string` casts; only 16 routes `validateSearch` | `lib/navigation.ts:12-15, 53-60` | L (incremental) |
| DEBT-12 | Strip 73 no-op `"use client"` directives; ESLint ban | all under `components/` | S |
| DOC-05 / UX-24 | Regenerate `frontend/gallery` (stamped 0.2.0-dev, still has `dashboard_argocd*`) from the stubbed preview server; delete `screenshots/` Argo-era PNGs (untracked) | `frontend/scripts/shoot.mjs:10,16` needs a live host + creds | S |

---

## 7. Rancher parity (adopted-cluster scope; Flux, not Fleet)

### Where Astronomer is ahead (lead with these)
- **Delivery governance** Fleet lacks: approvals, maintenance windows, canary/blue-green, failure budgets, frozen placement snapshots, cosign/keyless/git signature policy, rollback, credential rotation (`/delivery/rollouts/{id}/{approve,pause,resume,abort,retry,rollback}`, `internal/delivery/model/types.go:52-115`, `placement.go:119-140`).
- **Credential discipline:** 15-min non-renewable direct kubeconfig that refuses to mint when audit storage is down (`clusters_direct_kubeconfig.go:29,177,243,279`); agent privilege profiles (adopt read-only); recorded, scoped, reaped shell sessions.
- **Management-plane transparency:** queues, DLQ retry, task outbox, webhook/SIEM delivery retry + test, key status, backup destination test/run, redacted support bundles.
- **Identity/evidence:** SCIM v2, TOTP + recovery, read-audit policies with sampling, SIEM forwarders, per-tenant quota plans, CVE history/diff/CSV, anomaly baselines, namespace-scoped bindings, effective-permission preview, cross-cluster resource search, cluster groups with hierarchy, onboarding templates for adopted clusters.

### Gaps (in scope)

| ID | Gap vs Rancher | Astronomer today | Fix | Effort | Sev |
|---|---|---|---|---|---|
| PAR-01 | Multi-select bulk actions | Built in `DataTable`, used by 0 pages | See UX section | M | high |
| PAR-02 | Header namespace/project filter | None | See UX-04 | M | high |
| PAR-03 | Custom RoleTemplates: edit/delete/inherit/locked | API complete (`rbac.go:308-636`, builtin freeze `:1208-1215`); UI can only create (`-roles-tab.tsx` has no actions column); `inherits` only for YAML templates | Row actions (edit/duplicate/delete, disabled for builtin) reusing `RoleEditor`; clone-from-template | M | high |
| PAR-04 | Curated role templates you can apply | 37 validated YAML templates, read-only `GET /rbac/templates`, apply endpoint "ships in a follow-up" (`rbac_templates.go:9-15`), no UI | Documented `POST /projects/{id}/apply-rbac-template/` under escalation guard; picker in binding modal | S–M | med |
| PAR-05 | Fleet `targetCustomizations` (per-cluster values/patches) | Values/patches only on immutable bundle version (`delivery/model/types.go:334-349, 443-470`); target has no override field | Bounded `overrides` on target/assignment hashed into placement snapshot + approval digest | L | high |
| PAR-06 | IdP principal search; grant before first login | Binding modal = local users, `pageSize: 200` (`-binding-modal.tsx:29,165-178`) | Server-side user search now; audited principal-search over connectors + reify-on-login later | S / M | high |
| PAR-07 | User preferences, favourites, cluster badge/colour, landing page | Theme in localStorage only | See UX-22 | M | med |
| PAR-08 | API-server allow-list enforcement | Self-managed/AKS/DOKS providers are stubs returning empty effective set (`apisvr/allowlist/providers/scaffolds.go:34-45`, `providers.go:20-32`) while UI shows drift vs a fabricated baseline | Surface `CanMonitor:false` honestly in UI + posture summary until enforcement lands | S (honesty) / M | med |
| PAR-09 | Cluster Tools: Istio/Longhorn/NeuVector one-click | 9 seeded tools; service mesh is detect/inspect only (`service_mesh.go:245-431`); Longhorn is in the blessed catalog | Curated tool rows + form schemas | S–M | med |
| PAR-10 | Multi-document YAML import | `yaml.load` single doc (`create-resource-dialog.tsx:136,213`) | `loadAll`, sequential apply, per-doc result table | S–M | med |
| PAR-11 | Agent tolerations/affinity/resources/proxy env | Fixed nodeSelector + 2 tolerations in template; no placeholders (`deploy/agent/install.yaml.template:834-842`, `template.go:114-119`) | Validated `agent_overrides` JSON rendered into manifest, digest in upgrade plan | M | med |
| PAR-12 | Audit verbosity knob; inactive-user retention | Policy-shaped read audit (better, but not the named control); no user sweep | Retention settings + daily sweep (model: `cluster_tombstone_retention.go`); map an incident tier onto read-audit policies | S / M | low–med |
| — | Project-aggregate quota (Rancher splits project total across namespaces) | Per-namespace only (`project_reconcile.go:326-341, 786-836`) | Project cap + division | M | med |
| — | Branch tracking / git webhooks for continuous deploy | Non-goal per ADR (`flux-native-delivery.md:92`) | Disclose in scorecard; optional opt-in poller later | — | med (workflow) |

### Scorecard accuracy (`docs/rancher-astronomer-comparison.md`)
Rows marked **Complete** that do not meet the doc's own bar ("public API **and operator UI**, with automated evidence"):
1. **Multi-cluster grouping and tenancy** → Partial (no role edit/delete in UI, templates unappliable, picker capped at 200, no principal search).
2. **Extension model** → Partial (flag defaults off "until the marketplace is built out", `platform_settings.go:180-182`).
3. **Security posture** → qualify the API-server allow-list clause (stubbed for self-managed/AKS/DOKS).
4. **GitOps delivery** → add "no per-target overrides; no branch tracking (by design)" to the limitation column.
5. **Backup/DR** → note `management_backup_enabled` and `control_plane_snapshots_enabled` default false.
6. **Identity** → note SSO requires installing the Dex tool; `dex_bundled_enabled` default false; no user retention.
7. **Workload/node ops** → add "single-object only" limitation.
8. `docs/routes.json` omits conditionally-mounted routes (e.g. `/admin/read-audit-policies/*`) that `openapi.yaml` includes.

---

## 8. Architecture and tech debt

| ID | Finding | Numbers | Fix | Effort |
|---|---|---|---|---|
| DEBT-01 | `internal/handler` is one package | 163 files, 98,988 LOC (21.6% of Go), 81 handler structs, 688 HTTP methods, 106 querier interfaces | Carve by bounded context along existing `*MutationTx`/`*Querier` pairs; `handler/httpx` leaf; boundary rule per subpackage | L (program) |
| DEBT-08 | 24 handler files > 1,000 lines | `catalog.go` 2,855 … `webhooks.go` 1,088; 92,742 LOC in files >1k; section dividers already present | Split along existing `// --- … ---` markers first (types / mapping / per-section) | M per file |
| DEBT-02 | 28 byte-identical `execute<Domain>Mutation` clones; 51 `*MutationTx` interfaces, 47 `SetRunTx`, 42 `TransactionalAuditWired` | `catalog.go:200` ≡ `logging.go:164` … | One generic `executeMutation[Q,T]` in `audit_helpers.go`; shared embedded `auditOutboxWriter` | M (audit path; test before/after) |
| DEBT-15 | Cluster-ID + scope preamble hand-rolled | 83 inline `chi.URLParam(r,"cluster_id")`, 258 `uuid.Parse(chi.URLParam…)`, 6 competing helpers | One `clusterIDFromRequest` + `respondInvalidClusterID`; or stash parsed UUID in context in `requireK8sProxyPermission` | M |
| DEBT-14 | 7 pagination entry points across 4 files + 33 hand-parsed limit/offset | `response.go:64,147`, `response_list.go:30,50,69`, `cluster_resources.go:417`, `workloads.go:456` | Keep `queryLimitOffset` + one `pageWindow` + `RespondList` | S |
| DEBT-03 | No service layer | 119/163 handler files import sqlc; 1,012 direct query sites; worker/charlie/server re-consume the same queries; `handler.WorkerRuntime()` lends handler logic to worker | Extract `internal/<domain>` services for projects + catalog first; forbid `worker/tasks` → sqlc once migrated | L |
| DEBT-07 | `RouterDependencies` 108 fields; 285 setters; 19 `app_*.go` wiring files | fail-open nil guards are load-bearing (SEC-08) | Group into sub-structs; required deps as constructor args | M–L |
| DEBT-04 | Two tunnel/agent stacks both mounted | `agent`+`tunnel` 20,089 LOC vs `agent2`+`tunnel2` 483 LOC; `connect` and `connect2` both registered; `connect2` "experimental", no HA locator | Publish capability matrix; decide finish-and-delete direction | L / HIGH risk |
| DEBT-05 | Feature flags: `FeatureGate` hardcodes default `true` ignoring `settingsRegistry`; `feature.delivery` seeded but unregistered; `feature.alerting` advertised to Charlie but unregistered; `feature.extensions` off only because one route passes `false` explicitly | `feature_gate.go:46`; `platform_settings.go:161-233`; `001_initial.up.sql:5164`; `capability_schema.go:354` | `settingsRegistry.DefaultBool(key)` consumed by the gate; register/drop orphans | S |
| DEBT-06 | `namespace_scoped_rbac_enabled` default ON (`config.go:378`) but 3 live comments + a test describe OFF as default | `workloads.go:174`, `authorization.go:146`, `pods_watch_nsscope_test.go:106` | Fix comments; add ON-path test; decide on retiring the 45-site kill switch | S |
| DEBT-09 | Complexity ratchet vacuous | 6/7 hotspot ceilings stale (`$resource/index.tsx` ceiling 4650, actual 7); budgets apply to changed files only; 60 Go files over budget unchecked | Bidirectional ratchet (fail when actual < ceiling) as `check-frontend-raw-transport.mjs:59` does; seed hotspot map from today's over-budget inventory | S |
| DEBT-10 | Dependency-boundary contract covers 5/61 packages; the "temporary" middleware exception is used by 55 handler files | `docs/architecture/dependency-boundaries.json` | Extract `internal/reqctx` leaf; delete `allowed_prefixes`; one rule per new subpackage | S–M |
| DEBT-16 | 81 bound config keys, 42 without defaults, 6 without struct fields; no `docs/configuration.md`; 124 stray `os.Getenv` in 20 packages bypass production validation | `internal/config/config.go` | Generate config reference (pattern: `error-code-docs.mjs`); lint forbidding `os.Getenv` outside config/cmd | S / M |
| DEBT-13 | `@tanstack/db` in 3 files vs react-query in 85 (235 `useQuery`); `@tanstack/store` in 4; `paced-invalidate.ts` bridges them | `lib/db/collections.ts` documents intent | Investigate-and-decide: finish for watch-backed screens or delete | M (MED conf) |
| FE-10 | Frontend god files | `cluster-detail.ts` 1,832; `snapshots-page.tsx` 1,727; `charlie-shell.tsx` 1,642; `delivery.ts` 1,384; `data-table.tsx` 1,350; `sidebar.tsx` 1,244 | Extract `useResourceCounts` + nav config from sidebar; split charlie shell into transport/reducer/view | L |
| DEBT-17/18 | `LEGACY_JSON_BUDGET` all zeros (migration done); `lib/api.ts` + `lib/hooks.ts` back-compat barrels; 10 sub-150-LOC packages; `window-manager` 1,121 LOC with 2 consumers | — | Delete the dead ratchet map and barrels; decide window-manager's future with UX-15 | S |
| DEBT-11 | `"argo" + "cd"` literal-splitting in 6 files to evade `argo_absent_test.go` | `db/freshinstall/freshinstall.go:21-27`, `deploy/*_test.go`, `cmd/astro/delivery_test.go` | Plain literals + explicit allowlist in the test | S |

---

## 9. Tests, DX, docs, dependencies

| ID | Finding | Fix | Effort |
|---|---|---|---|
| TEST-01 | `cmd/astro` (12,193 LOC, 61 commits/6 mo) at **3.8%** coverage; `client.go`, `auth.go`, `output.go` untested | Golden-output + `httptest` + config-dir tests first | M–L |
| TEST-02 | `internal/handler/delivery` at 36%; `rollout.go` Pause/Resume/Abort/Retry/Rollback/Approve + idempotency-key enforcement have no test file | Table test per verb through chi with fake controller | M |
| TEST-03 | `internal/delivery/resolver` at 40%; git SSH/known-hosts, OCI digest pinning, private-address denial only indirectly covered | Coverage profile → fixtures for the three paths | M |
| TEST-04 | Visual regression covers 4/130 routes | Add delivery, RBAC, backups, settings on chromium + mobile | S |
| TEST-05 | `make verify-enterprise` omits the 9 stateful/browser lanes CI runs; no `verify-all` | Aggregate target + README table of what each scope proves | S |
| TEST-06 | 16 test files for 138 route modules; route tier covered only by render-only crawl | RTL tests for rollout detail, RBAC bindings, backups | L |
| DX-01 | `.env.example` documents `CELERY_BROKER_URL`, `NEXTAUTH_SECRET`, `NEXT_PUBLIC_API_URL`, `PAYLOAD_SECRET`, `CMS_DATABASE_URL` (0 references) and omits 11 live vars (`LISTEN_ADDR`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `GRAFANA_PROXY_KEY`, `LOKI_UPSTREAM`, `ASTRONOMER_TUNNEL_EGRESS_CIDRS`, …) | Regenerate from the bind list; add a drift check | S |
| DX-03 | `make dev` publishes server on `8001:8000` (`deploy/docker-compose.yml:62`); `vite.config.ts:31` proxies to `:8000`; compose frontend collides on `:3000`; no Go hot reload | Default proxy to 8001 or a `dev:compose` script; compose frontend behind a profile; README "backend via compose + `npm run dev`" | S |
| DX-02 | No root `AGENTS.md`/`CLAUDE.md`; 5 non-obvious gates (generated files, raw-transport budget, banned-terminology, zero-retry flake policy, complexity ratchet) | One file: commands, generated list, gate scripts + what they reject, layout | S |
| DX-04 / DX-05 | No `.editorconfig`, pre-commit, prettier; Makefile help says "Next.js dashboard" (`Makefile:238`); `__APP_VERSION__` fallback `'0.3.0-dev'` vs package 1.1.0 | `.editorconfig` + staged-file hook; fix strings; derive version from `package.json` | S |
| DOC-01 | `check-docs.mjs:19-22` only matches `Argo CD|ArgoCD|argocd` per line → `docs/runbooks/high-http-error-rate.md:67-68` ("Argo\nCD UI"), `docs/charlie-operations.md:196-308` (5 Argo instructions), `db-pool-exhausted.md:55`, `postgres-failover.md:43` pass the gate | Add bare `\bArgo\b`, scan whole text; rewrite in Flux terms | S |
| DOC-02 | 4 Argo-era plans in `docs/plans/` with no status banner; `2026-05-08-validation-and-gaps.md` cites non-existent `internal/handler/argocd.go`; `advisor-plans/README.md` still lists 002/003 as active P0 Argo blockers; `docs/cluster-explorer-drilldown-plan.md` says "DRAFT" for a shipped feature; `plans/008-fleet-operations-ui.md` describes routes no longer mounted | `docs/plans/README.md` status table + superseded banners; update advisor index | S |
| DOC-03 | `MARKETING.md:92,112` say "provision new ones" / "stand up a new one"; `:57` and `README.md:170` say the opposite | Strike the two phrases | S |
| DOC-04 | `astro` CLI (27 command groups) undocumented; `cmd/astro/docs.go` generator exists and is unwired | `make cli-docs` → `docs/cli/`, link from README, freshness gate | S |
| DEP-01 | `js-yaml` 4.3.1 high advisory (GHSA-2883-xcg3-v3hh) on 8 runtime modules parsing operator/cluster YAML; `verify-enterprise.sh` runs `npm audit` at `moderate` — check why it passes | Bump to 4.3.2 | S |
| DEP-02 | 16 majors behind: Tailwind 3→4, TS 5→7, ESLint 9→10, react-table 8→9, lucide 0.5→1.4, date-fns 3→4, sonner 1→2, …; `@tanstack/db`/`react-db`/`react-pacer` exact-pinned pre-1.0 | Three waves: tooling → libs → Tailwind 4 / TS 7 with visual gate | S / M / L |
| DEP-03 | No `.nvmrc`/`engines`; `@types/node` ^20 vs Node 22 CI/Docker | Pin 22; bump types | S |
| DEP-04 | Retracted/deprecated transitive modules (`go-autorest/adal` retracted, `aws-sdk-go` v1, `pubsub`) via helm/sigstore | `go mod why`; bump; record accepted debt | S (MED conf) |

Go module currency is fine (k8s 0.36.3, controller-runtime 0.24.1, helm 3.21.4, asynq, sigstore all current or one minor behind). Structured logging + request IDs already in place. Error-code docs generated and gated. Test quarantine empty.

---

## 10. Suggested sequencing

**Wave 0 (days, all S):** SEC-01, SEC-07/08, client-IP helper (BUG-01/02/08, SEC-13), SEC-06, SEC-10, SEC-11, SEC-12, BUG-04 offset clamp, FE-01, FE-02/03, UX-05/06/08/12/14/23, PERF-03/04/06/14, DEBT-05/06 comment+default fix, DOC-01/03, DX-01/03/05, DEP-01/03. Then run PERF-01 (one load rung) so waves 2+ are ordered by data.

**Wave 1 (1–2 weeks):** SEC-02, SEC-03, SEC-04, SEC-09, UX-01, UX-02, UX-04/PAR-02, PAR-01/UX-13, UX-07, FE-05/06/07/09/11, BUG-03/05/06/07/09/10/11, PERF-07/08, DX-02 (`AGENTS.md`), DEBT-09 ratchet, DEBT-11, DOC-02.

**Wave 2 (2–4 weeks):** PAR-03/04, PAR-06, PAR-07/UX-22, UX-03/FE-07 state coverage, UX-09, UX-15, UX-16 consolidation, UX-18, UX-20/21, PAR-08 honesty, PAR-09/10/11, PERF-05/09/11/12/13/15, DEBT-02/14/15, TEST-02/03/04/05.

**Wave 3 (programs):** PAR-05 per-target overrides (design spike first: override digest into placement snapshot + approval), PERF-02 heartbeat redesign, PERF-10 sweep sharding, DEBT-04 tunnel decision, DEBT-01/03/07 handler carve + service layer, UX-17/19, FE-13, DEP-02 Tailwind 4 / TS 7, TEST-01/06.

**Dependencies:** PERF-01 before PERF-02/05/10/11 sizing · DEBT-08 file splits before DEBT-01 package carve · SEC-08 fail-closed before DEBT-07 constructor refactor (so tests supply fakes) · UX-02 before regenerating any visual baselines (do baselines once) · client-IP helper before flipping `allowed_cidrs` behaviour (release note).

---

## 11. Considered and rejected

- Cache-invalidation `Broadcast` errors swallowed → coordinator marks unhealthy and caches bypass; correct by design.
- `asynq.Scheduler` on every worker replica → 60/61 tasks leader-gated or lease-claimed; only BUG-14 affected.
- Agent-supplied `ObservedAt` on delivery events → rollout gates use server `now()`.
- `tunnel2` skipping heartbeat/registration advance → documented experimental scope.
- `dangerouslySetInnerHTML` in `widget-grid.tsx:175` → server-built SVG, documented.
- axios-vs-fetch duplication → governed by `check-frontend-raw-transport.mjs` with annotated exceptions; migration complete.
- `internal/envconfig` as a "second config system" → 32-line viper wrapper used only by `internal/config`.
- Multiple Go modules → only one `go.mod`.
- Branch tracking for delivery → recorded non-goal in the Flux ADR; disclosure gap only.
- Rancher provisioning, node drivers, Harvester, Windows, Fleet API → out of scope by product decision.

## 12. Not audited

`internal/charlie` (31,667 LOC) beyond integration shape and MCP transport; `internal/charliequalification/driver.go` (2,209 lines, highest-churn unaudited file); `internal/tunnel2` authentication internals; `internal/crd` beyond `hostAccess`; delivery rollout scheduler/placement freezing/generation fencing internals; agent-side Flux reconciler; Helm chart RBAC/PDB/backup templates; 15 of 18 GitHub workflows; `docs/runbooks` beyond Argo grep; runtime profiling (no server was started); `go test -race`, Playwright, and the stateful lanes; axe/keyboard/contrast measurement; i18n/RTL; Redis/asynq queue-depth behaviour.

---

*Executor-grade plans (self-contained, with verification gates) can be generated per finding on request; this document is the review, not the plan set.*
