# Astronomer — Run-Readiness & Rancher-Parity Remediation Plan

> **STATUS: IMPLEMENTED & VALIDATED (2026-07-07).** **All 30 findings are now implemented** on the working tree across two waves — wave 1 (27 findings incl. the R-01 blocker + all five HIGH) and wave 2, the backlog (P-02 live-watch, P-03 Alertmanager inhibition, P-04 custom-OPA constraint authoring, F-05 SIEM/SCIM UIs). The new P-03/P-04 backend passed an adversarial security-verify pass (verdict SAFE); its two LOW defense-in-depth findings (DNS-1123 name/kind validation before SSA path build; fail-closed read authorization) were applied. Full gate is green — see **§0. Implementation Status** for the ledger + validation evidence. Two audit corrections surfaced during implementation: **O-04 was already enforced at the `values.schema.json` layer** (kept it, added the `enabled=false` opt-out), and **O-03/O-10 fixed latent pre-existing bugs** (the agent-disconnect metric vanished on disconnect; the dashboard glob never shipped any dashboard).

**Date:** 2026-07-07
**Scope:** Whole-platform audit of Astronomer at commit `fa0527d` for (a) whether it is fully ready to run in production and (b) whether it matches or beats Rancher for **day-2 operation of already-provisioned clusters**. Cluster/node **provisioning is intentionally out of scope** (no machine drivers, no node pools, no cloud provisioning) and its absence is *not* treated as a gap.
**Method:** Multi-agent audit across six dimensions (run-readiness, security/authz, correctness/HA, frontend/UX, ops/observability, Rancher parity). Every finding was adversarially re-verified against the real code before inclusion; false positives and already-fixed items were dropped. 30 findings survived verification.

---

## 0. Implementation Status (2026-07-07)

Implemented across five file-disjoint workstreams (server-security, worker/tunnel/db, chart/docs/ops, frontend, rbac-parity), then integrated and validated on one working tree.

### Validation evidence (all green)

| Gate | Command | Result |
| --- | --- | --- |
| Build all binaries | `make build` | ✅ exit 0 |
| CI contract gate | `make verify` | ✅ exit 0 — OpenAPI coverage still 100% (406/406) |
| Static analysis | `go vet ./...` | ✅ clean |
| Full backend suite | `go test ./...` | ✅ exit 0, **0 failures** (incl. all new tests) |
| Chart render/contract | `go test ./deploy/ -count=1` | ✅ ok (incl. 6 new contract tests) |
| Frontend units | `npx jest` | ✅ **381/381** (up from 369), 43 suites |
| Frontend types | `npx tsc --noEmit` | ✅ clean |
| Helm render (default) | `helm template …` | ✅ renders |
| Helm render (prod, backups unwired) | `helm template … values-production.yaml` | ✅ correctly **fails** citing `managementBackup` |
| Helm render (prod, `enabled=false`) | opt-out set | ✅ renders, no backup CronJob |
| gofmt (changed files) | `gofmt -l` | ✅ clean |

Security-critical fixes verified by their own tests: R-01 argocd-authz (6 tests incl. viewer-denied, read-only-mutation-denied, fail-closed-on-nil-engine, HTML-nav→permission-page) and C-08 namespace exec/logs scoping (namespace-scoped allow/deny + cluster-wide). R-01's middleware is wired at the `/argocd` and `/argocd/*` mounts (`routes.go:434-435`), not just unit-tested. Change size: 26 Go files (+1299/−131) plus chart/docs/frontend, and ~13 new test files.

### Per-finding ledger

| ID | Sev | Status | Note |
| --- | --- | --- | --- |
| R-01 | Blocker | ✅ Done | `ArgoCDAuthz` middleware, fail-closed, wired + 6 tests |
| O-01 | High | ✅ Done | DR runbook key name corrected to `ASTRONOMER_ENCRYPTION_KEY` |
| O-02 | High | ✅ Done | `helm.sh/resource-policy: keep` on `<release>-secrets` + render test |
| O-03 | High | ✅ Done | `astronomer_agent_connections` 0/1 gauge survives disconnect; rule alerts on `== 0` |
| O-04 | High | ✅ Done* | *Already enforced by schema; added `enabled=false` opt-out + helper message + tests |
| F-01 | High | ✅ Done | 4 orphaned pages + service-mesh in nav & command palette; dead `provisioning/` dir removed |
| C-01 | Medium | ✅ Done | Worker now runs prod fail-fast (secrets + SchemaHealth); shared `internal/config/production.go` |
| H-01 | Medium | ✅ Done | Owner-checked (CAS) locator refresh; superseded pod self-cancels + test |
| F-02 | Medium | ✅ Done | Global-search deep links via `resolveDetailSlug` + routing test |
| F-03 | Medium | ✅ Done | RBAC users tab links to admin user-security page + Locked badge |
| F-04 | Medium | ✅ Done | `error.tsx`/`not-found.tsx`/`global-error.tsx` + cluster-scoped error boundary |
| F-05 | Medium | ✅ Done | key-status + shell-sessions (wave 1); SIEM-forwarder CRUD UI + SCIM token UI (wave 2) |
| O-05 | Medium | ✅ Done | audit/SIEM drop-counter alert rules + 2 runbook pages |
| O-06 | Medium | ✅ Done | schema floor bumped; `TestSchemaFloorTracksMaxMigration` guards drift |
| C-02 | Low | ✅ Done | worker `/healthz` (redis+scheduler) + liveness/readiness probes + render test |
| C-03 | Low | ✅ Done | embedded schema version; `/readyz` 503s until migrations catch up |
| C-04 | Low | ✅ Done | GitOps `lookup`-caveat documented in `bootstrap-secret.yaml` |
| C-05 | Low | ✅ Done | `keyrotate` falls back to `ASTRONOMER_ENCRYPTION_KEY` |
| C-08 | Low | ✅ Done | exec/logs consumers thread `{namespace}` into RBAC check + tests |
| C-11 | Low | ✅ Done | `docker-compose.yml` key name fixed |
| H-02 | Low | ✅ Done | health-sweep status write guarded by `last_heartbeat` in SQL + race test |
| R-03 | Low | ✅ Done | `deploy/k8s/03-secret.yaml` key name + valid dev Fernet value |
| O-08 | Low | ✅ Done | upgrade runbook preflight ref + backup label selector fixed |
| O-09 | Low | ✅ Done | `logging-flatlined.md` written; `TestPrometheusRunbookURLsResolve` guards links |
| O-10 | Low | ✅ Done | `management-plane.json` dashboard (also fixes the latent chart-glob so all dashboards ship) |
| F-06 | Low | ✅ Done | settings hub shows "Admins only" to non-superusers instead of 17 dead cards |
| P-01 | Low | ✅ Done | RBAC role inheritance (`resolveInherited`): transitive union, cycle + scope-mismatch fail-closed, provenance in preview API + 6 tests |
| P-02 | Low | ✅ Done | Live-watch: `useResourceWatch` hook folds pods-SSE + `?watch=true` proxy frames into the react-query cache with polling fallback (workloads page wired; resources page is a follow-on) |
| P-03 | Low | ✅ Done | Alert inhibition: migration 132, CRUD API (admin-scoped), two-pass eval suppression, inhibition UI + tests |
| P-04 | Low | ✅ Done | Custom Gatekeeper authoring: migration 133, list/validate/create/delete (RBAC fail-closed + audited), YAML+Rego structural validation, DNS-1123 name/kind guard, SSA via tunnel, policy UI + tests |

**Wave-2 (backlog) validation:** `make verify` still 100% (406/406 with 9 new documented routes), `make check-migrations` OK, `go test ./...` 0 failures, frontend **jest 417/417 / 52 suites**, tsc clean. Two small follow-ons remain, both explicitly noted, neither a blocker: **P-02** resources-page live-watch (workloads page is wired; the same hook drops onto the resources page), and **P-04** full Rego *compilation* (currently structural validation — package decl + balanced delimiters — because no OPA parser is vendored; a full AST compile means vendoring `github.com/open-policy-agent/opa`).

---

## 1. Executive Summary

**Verdict — can it run?** Yes, with one exception. The code **builds clean, passes its full test suite, and passes its own CI gate** (see §2). It will install and operate. But there is **one launch-blocking security hole** (§4, R-01) that must be fixed before any multi-user production exposure, plus a cluster of **operational-safety gaps that make disaster recovery unsafe as documented** (§5). Fix R-01 and the five HIGH items and it is production-ready.

**Verdict — is it as good as Rancher for day-2?** For adopted clusters, **yes on capability, and better in several areas** (Argo-native GitOps delivery, outbound-only agent security model, encrypted-at-rest credential columns, CRD-native API, an OpenAPI contract with a 100%-coverage CI gate that Rancher has no equivalent of). The parity gaps that remain (§6) are **polish and completeness**, not missing pillars: four fully-built operator screens are shipped but unreachable from the nav, RBAC role *inheritance* is declared-but-unimplemented, the resource explorer polls where a live-watch backend already exists, and there is no first-class custom-OPA-constraint authoring UI. None are blockers; all are closeable additions.

**The single most important cross-cutting theme:** an **encryption-key env-var name drift**. The binaries read `ASTRONOMER_ENCRYPTION_KEY`, but the raw k8s manifests, docker-compose, the `keyrotate` tool, and — most dangerously — the **disaster-recovery runbook** all still say bare `ENCRYPTION_KEY`. The Helm chart was already corrected by a prior sweep, so the chart install is safe; but an operator following the DR runbook during a real outage will "restore" the Fernet key under a name nothing reads, believe recovery succeeded, and find every encrypted column permanently undecryptable. This one naming bug spans findings R-03, O-01, C-05, C-06, and C-11 and should be fixed as a single coordinated change plus a drift-guard test.

### Findings at a glance

| Severity | Count | Headline |
| --- | --- | --- |
| 🔴 Blocker | 1 | ArgoCD UI proxy authenticates but never authorizes — any logged-in user becomes ArgoCD admin |
| 🟠 High | 5 | DR runbook uses wrong key name; chart doesn't `keep` the secret on uninstall; agent-disconnected alert can never fire; prod preflight doesn't require backups; four operator screens orphaned from nav |
| 🟡 Medium | 8 | Worker skips prod fail-fast checks; locator split-brain on ungraceful agent move; broken global-search deep links; unreachable admin user-security page; no error boundaries; admin APIs with no UI; missing audit-drop alerts; stale restore-drill schema floor |
| 🟢 Low | 16 | Manifest/compose/keyrotate key-name drift, worker probes, upgrade-window schema gate, bootstrap-password stability, namespace-scoped exec/logs, settings afford-then-deny, runbook drift, management-plane dashboard, RBAC inheritance, live-watch UI, Alertmanager inhibition, custom-OPA authoring |

### Recommended remediation sequence

- **Wave 0 — Launch blocker (do first, ~0.5 day):** R-01.
- **Wave 1 — DR safety & prod fail-fast (~2 days):** the key-name drift bundle (R-03, O-01, C-05, C-06, C-11), plus C-01 (worker prod checks), O-02 (`keep` annotation), O-03 (agent-disconnect alert), O-04 (require backups in preflight). This closes every way a production operator can lose data or fly blind.
- **Wave 2 — UX/parity completeness (~2–3 days):** F-01…F-06 (nav, deep links, error boundaries, admin surfaces) and the four parity additions P-01…P-04.
- **Wave 3 — Hardening & polish (~2 days):** remaining LOW ops/runbook/dashboard items and the correctness edge cases (H-01 locator, H-02 health-sweep).

Total: roughly **6–8 engineering days** to reach unambiguous launch-ready + Rancher-parity-or-better.

---

## 2. Baseline Gate Status (verified this run)

All of the following were executed against the working tree at `fa0527d` and **passed**:

| Gate | Command | Result |
| --- | --- | --- |
| Build (all binaries) | `make build` | ✅ server, agent, worker, astro, keyrotate compile |
| CI contract gate | `make verify` | ✅ OpenAPI coverage **100% (406/406)**, no route drift, route-table + apierror-catalog tests pass |
| Static analysis | `go vet ./...` | ✅ clean |
| Go tests | `go test ./...` | ✅ all packages pass (race detector on in `make test`) |
| Frontend unit tests | `npx jest` | ✅ **369/369** across 41 suites |
| Frontend types | `npx tsc --noEmit` | ✅ clean |

**Interpretation:** the gates are green, so none of the findings below are "the build is broken." They are the class of issue a green build does *not* catch: unsafe runtime defaults, authorization holes behind authenticated routes, doc/runbook drift from code, alerts that don't fire, and shipped-but-unwired features. Each finding cites `file:line` evidence and carries a verification verdict.

---

## 3. How to read a finding

Each item has: an **ID**, severity, whether it is a **Fix** (existing behavior is wrong/unsafe) or an **Addition** (capability missing for parity/readiness), the **evidence** (verified), **why it matters**, and an **ordered task list** you can execute directly. Effort is S (<½ day), M (½–1 day), L (>1 day).

---

## 4. 🔴 BLOCKER

### R-01 — ArgoCD UI reverse proxy authenticates but does not authorize *(Fix, security-authz, M)*

**Evidence.** `internal/server/routes.go:422-428` mounts the proxy with only authentication middleware:
```go
r.With(argoAuth).Handle("/argocd", deps.ArgoCDUIProxy)
r.With(argoAuth).Handle("/argocd/*", deps.ArgoCDUIProxy)
```
`argoAuth = AuthBrowserOrBearer(...)` (`internal/server/middleware/auth_browser.go:47-69`) wraps `AuthWithQueries` — **authentication only, no RBAC check, no feature gate**. The proxy Director (`internal/handler/argocd_ui_proxy.go:196-211`) then injects the **shared upstream ArgoCD admin token** into every forwarded request. **Verified CONFIRMED.**

**Why it matters.** Vertical privilege escalation / broken access control. Any authenticated principal — including a read-only viewer with zero ArgoCD/gitops/cluster grants — can browse to `/argocd/` or call `/argocd/api/v1/applications` with just their session cookie and be transparently authenticated to ArgoCD **as admin**: list, create, sync, and delete Applications across the fleet. This is the highest-impact issue found and must be fixed before any multi-user exposure.

**Fix tasks (ordered):**
1. Add an authorization middleware to the `/argocd` and `/argocd/*` mounts (`routes.go` ~line 427) that runs **after** `argoAuth`.
2. In it, load bindings via `deps.RBACQueries.GetUserBindings` and call `deps.RBACEngine.CheckPermission` with a method→verb map: `GET`/`HEAD` → require `ResourceArgoCD` (or `ResourceWorkloads`) `VerbRead` on the local cluster; `POST`/`PUT`/`PATCH`/`DELETE` and ArgoCD action paths (`.../sync`, `.../rollback`) → require `ResourceClusters` `VerbUpdate` on the local cluster. Note ArgoCD uses `GET` for app-tree reads but `POST` for sync — treat mutating action paths as writes explicitly.
3. **Fail closed:** if `deps.RBACEngine` or `deps.RBACQueries` is nil, return 403 rather than pass through, so a misconfiguration can't silently open the admin proxy. Return JSON 403 for XHR/WS and a permission page for HTML nav, mirroring the existing `AuthBrowserOrBearer` HTML-vs-JSON split.
4. Resolve the local cluster id once (same source `localClusterArgoCDTokenSource` uses) so the check is anchored to a concrete scope.
5. Add tests: (a) a viewer with no grants gets 403 on `GET /argocd/` and `GET /argocd/api/v1/applications` and no `argocd.token` cookie is injected upstream; (b) a granted user is proxied and the cookie is injected; (c) a mutating request from a read-only user is 403'd.
6. Record an audit row (`SetAuditWriter` is already wired) with the acting user for every proxied ArgoCD mutation. Consider defaulting the whole proxy to superuser-only behind a feature flag until per-request scoping is proven.

**Files:** `internal/server/routes.go`, `internal/server/middleware/auth_browser.go`, `internal/handler/argocd_ui_proxy.go`, `internal/server/server.go`

---

## 5. 🟠 HIGH

### O-01 — DR runbook reads/recreates the encryption-key Secret with the wrong key name *(Fix, ops, S)*

**Evidence.** `docs/management-plane-dr-runbook.md:101-104, 367-371, 388-396, 458-460` all use `jsonpath='{.data.ENCRYPTION_KEY}'` / `--from-literal=ENCRYPTION_KEY=...`, but the chart secret key is `ASTRONOMER_ENCRYPTION_KEY` (`deploy/chart/templates/secret.yaml:12`, consumed via `envFrom`). The jsonpath for a nonexistent key returns empty, so `base64 -d` emits nothing. **Verified CONFIRMED.**

**Why it matters.** The runbook's own stated invariant is Fernet-key continuity, yet **every command it gives to save, verify, or restore the key silently returns an empty string during a live disaster**, and its "Fix" recreates the Secret under a key the server/worker never read — the operator believes the key is restored while decryption stays permanently broken.

**Fix tasks:**
1. Replace all four `.data.ENCRYPTION_KEY` / `--from-literal=ENCRYPTION_KEY=` occurrences with `ASTRONOMER_ENCRYPTION_KEY`.
2. In the Fix section, patch only the key (`kubectl patch secret ... -p '{"stringData":{"ASTRONOMER_ENCRYPTION_KEY":"..."}}'`) **or** list every key the chart renders (`SECRET_KEY`, `ASTRONOMER_ENCRYPTION_KEY`, `GITHUB_*`, `GOOGLE_*`, `OIDC_*`, bundled `POSTGRES_PASSWORD`) so `envFrom` consumers don't lose vars.
3. Add a docs-drift test (extend `deploy/chart_operational_contract_test.go`) that greps the runbook for `data.ENCRYPTION_KEY` vs the key rendered in `templates/secret.yaml` so they can't diverge again. **This same test should cover the manifest/compose/keyrotate drift in R-03/C-05/C-11.**

**Files:** `docs/management-plane-dr-runbook.md`, `deploy/chart/templates/secret.yaml`, `deploy/chart_operational_contract_test.go`

### O-02 — Chart doesn't annotate `<release>-secrets` with `helm.sh/resource-policy=keep` *(Fix, ops, S)*

**Evidence.** The DR runbook (`docs/management-plane-dr-runbook.md:65-69`) claims the chart annotates `<release>-secrets` with `helm.sh/resource-policy=keep` so it survives `helm uninstall`. `grep -rn resource-policy deploy/chart/` shows the annotation only on `bootstrap-secret.yaml:33` and CRD templates — **`templates/secret.yaml` has no annotations block at all.** **Verified CONFIRMED.**

**Why it matters.** An accidental/intentional `helm uninstall` (which the DR runbook itself contemplates for cluster rebuilds) deletes the Secret holding the Fernet key and JWT signing key. Per the runbook, losing that key makes every encrypted column permanently unrecoverable. The documented safety net does not exist.

**Fix tasks:**
1. Add `annotations: { "helm.sh/resource-policy": keep }` to `deploy/chart/templates/secret.yaml` metadata (mirror `bootstrap-secret.yaml:30-33`).
2. Extend `deploy/chart_operational_contract_test.go` to assert the `-secrets` Secret carries the annotation.
3. Note in values comments that external-secret-manager users can ignore it, but the default keeps the key on uninstall.

**Files:** `deploy/chart/templates/secret.yaml`, `docs/management-plane-dr-runbook.md`, `deploy/chart_operational_contract_test.go`

### O-03 — `AstronomerAgentDisconnected` alert can never fire for a real disconnect *(Fix, ops, M)*

**Evidence.** `deploy/chart/templates/prometheus-rules.yaml:229-241` alerts on `astronomer_agent_last_seen_seconds > 120`. That gauge is populated only from `ListActiveConnections` (`internal/tunnel/connection_metrics.go:107-122`), whose SQL selects `WHERE status = 'connected'` (`internal/db/queries/agents.sql:4-5`), and `updateConnectionMetrics` calls `.Reset()` each tick (`connection_metrics.go:82`). On WS close, `persistDisconnect` sets `status='disconnected'` (`internal/tunnel/server.go:1021-1037`) — **so the series for a disconnected agent disappears, and a gauge that stops existing can never exceed a threshold.** **Verified CONFIRMED.**

**Why it matters.** The most common on-call scenario — agent pod deleted, cluster network cut, WS dropped — produces **no Prometheus signal**. Operators relying on the shipped alert are blind to real outages.

**Fix tasks:**
1. Publish a per-cluster staleness series that survives disconnect: use `ListLatestConnectionsByClusters` (`agents.sql:7-11`) over all non-decommissioned clusters and export age since `last_ping`/`disconnected_at` regardless of status, plus keep `astronomer_agent_connections` as 0/1.
2. Rewrite the rule to detect disappearance: alert on `astronomer_agent_connections == 0` on the always-present series (or an `absent()`-per-cluster pattern).
3. Add a unit test in `internal/tunnel`: a connection transitioning connected→disconnected still yields a sample with growing age (or a 0-valued connections gauge).
4. Update `docs/runbooks/agent-disconnected.md` for the new signal semantics.

**Files:** `deploy/chart/templates/prometheus-rules.yaml`, `internal/tunnel/connection_metrics.go`, `internal/db/queries/agents.sql`, `internal/tunnel/server.go`

### O-04 — Production preflight does not require management-plane backups to be wired *(Addition, ops, M)*

**Evidence.** `deploy/chart/templates/management-plane-backup-cronjob.yaml:1` renders only `if and .Values.managementBackup.enabled .Values.managementBackup.s3.bucket .Values.managementBackup.s3.credentialsSecretRef.name`. `enabled:true` is the default but `bucket`/`credentials` are empty, so **no CronJob renders**. `astronomer.requireProductionInputs` (`_helpers.tpl:326-397`) validates DSN/TLS/secrets/dex but has **no backup check**. **Verified CONFIRMED.**

**Why it matters.** RPO silently becomes infinite: an operator following `values-production.yaml` gets a clean render, a passing preflight, and no page telling them the management DB is unprotected. The entire DR story (nightly dump + weekly drill + staleness alerts) is a no-op until three S3 values are set.

**Fix tasks:**
1. In `astronomer.requireProductionInputs`, when `config.env=production`: fail the render if `managementBackup.enabled=true` but `s3.bucket` or `s3.credentialsSecretRef.name` is empty, pointing at the `values-production.yaml` example; allow explicit opt-out via `managementBackup.enabled=false` (and say so in the error).
2. Add render tests in `deploy/chart_operational_contract_test.go`: production+backup-unwired → render failure; production+`enabled=false` → success.
3. Optionally add a runtime rule (`absent(kube_cronjob_info{cronjob=~".*management-backup"})`) or NOTES.txt warning so a suspended/deleted CronJob is also detected.

**Files:** `deploy/chart/templates/_helpers.tpl`, `deploy/chart/templates/management-plane-backup-cronjob.yaml`, `deploy/chart/values-production.yaml`, `deploy/chart/templates/prometheus-rules.yaml`

### F-01 — Four fully-built cluster pages are orphaned — zero inbound navigation *(Fix, frontend, M)*

**Evidence.** These pages exist and are substantial but have **no link or `router.push` anywhere** (verified via repo-wide grep):
- `clusters/[id]/network-access/page.tsx` (504 lines) — apiserver allow-list UI (mode toggle, CIDR lists, drift badge, reconcile).
- `clusters/[id]/snapshots/page.tsx` (1454 lines) — Velero-backed workload snapshots + schedules.
- `clusters/[id]/registries/page.tsx` (640 lines) — private image-pull credentials, per-cluster, with a tunnel test action.
- `clusters/[id]/resources/page.tsx` (472 lines) — CRD-mirror read-only view.

`getClusterNavGroups` in `sidebar.tsx` has no entries for any of them. **Verified CONFIRMED.**

**Why it matters.** Shipped, working operator features (image-pull-secret management, Velero snapshots, apiserver network allow-listing, mirrored policy/quota views) are invisible — reachable only by hand-typing URLs. This is the literal difference between "has the feature" and "doesn't" for Rancher-parity claims; Rancher surfaces registries and snapshots prominently in the cluster nav.

**Fix tasks:**
1. Add sidebar entries in `getClusterNavGroups` (`frontend/src/components/layout/sidebar.tsx`): **Registries** and **Snapshots** (next to the existing "Control-plane DR" item) under Cluster; **Network & Access** under Cluster or Policy; **Mirrored Resources** under More Resources.
2. Gate **Network & Access** with the same agent-required/`isLocal` logic used for image-scans/shell if the allow-list needs a tunnel (check the page's `getApiserverAllowlist` calls).
3. Promote **service-mesh** to a real nav item instead of relying on the overview badge pill.
4. Add the new destinations to `command-palette.tsx` so keyboard users can reach them.
5. Delete the empty `clusters/[id]/provisioning/` directory (no `page.tsx`; dead route stub).

**Files:** `frontend/src/components/layout/sidebar.tsx`, `.../command-palette.tsx`, the four page files above.

---

## 6. 🟡 MEDIUM

### C-01 — Worker binary skips the production fail-fast checks the server enforces *(Fix, run-readiness, M)*

**Evidence.** `validateProductionSecurityConfig` is called only from `internal/server/server.go:311`; `database.SchemaHealth` only from `server.go:280`. `cmd/worker/main.go:163-205` treats a missing/invalid encryption key as warn-and-continue (line 204), and encryptor init failure (165-169) only logs. **Verified CONFIRMED.**

**Why it matters.** In production, a typo'd `ASTRONOMER_ENCRYPTION_KEY` or a dirty `schema_migrations` row crashes the server loudly but leaves the **worker Running**: ArgoCD auto-register proxy tokens, plaintext-credential migration, and email dispatch silently no-op while every sweep runs against a possibly corrupt schema — hiding exactly the misconfiguration class the server check exists to surface.

**Fix tasks:**
1. Move `validateProductionSecurityConfig`/`isProductionConfig` into a shared exported helper (e.g. `internal/config`).
2. Call it from `cmd/worker/main.go` after `config.Load()`; `os.Exit(1)` on error when `env=production` (keep warn-only for dev).
3. Also call `database.SchemaHealth` at worker startup and exit non-zero on dirty/multi-row state.
4. Add a regression test: worker refuses to start with `env=production` and an empty/dev encryption key.

**Files:** `cmd/worker/main.go`, `internal/server/server.go`, `internal/db/db.go`

### H-01 — Tunnel locator refresh loop overwrites a newer pod's directory entry (split-brain routing) *(Fix, correctness-ha, S)*

**Evidence.** `internal/tunnel/locator.go`: `Delete()` was hardened with a compare-and-delete Lua script (`locatorCASDelete`, 141-166) so a superseded owner can't clobber the new owner on a fast A→B move — but the periodic refresh `write()` (216-223) is a **plain unconditional `SET`** with no owner check, and `refreshLoop()` (225-239) fires it every 25s until A's disconnect path runs (which needs A's read/write pumps to return, ~30s on an ungraceful move). In that window, after B has taken ownership, A's ticker rewrites the redis key back to A's dead address. **Verified CONFIRMED** (bounded/self-healing ~30s, hence medium).

**Why it matters.** The locator is the cross-pod routing directory for `/k8s`, kubectl-shell, log-stream, and exec. While the key wrongly points at A, every proxied request 503s even though the agent is healthy on B. This defeats the exact HA guarantee the CAS-delete was added to protect, and recurs on **every** ungraceful agent move.

**Fix tasks:**
1. Replace `write()` with an owner-checked refresh: a Lua script that `SET`s (with TTL) only when the key is absent **or** already equals `l.address`; else return the current value.
2. In `refreshLoop`, when the refresh reports a different owner, treat this pod as superseded: log, cancel the loop, drop it from `l.cancels` so A stops fighting B.
3. Regression test: `Set` on pod-A, simulate pod-B taking ownership, tick A's refresh, assert redis still holds B's address and A's loop self-cancels.

**Files:** `internal/tunnel/locator.go`, `internal/tunnel/server.go`

### F-02 — Global search results dead-end at "Resource not found." *(Fix, frontend, S)*

**Evidence.** `frontend/src/app/dashboard/search/page.tsx:215` routes all unmatched types to `/dashboard/clusters/${cid}/resources/${resourceType}`; `SEARCHABLE_TYPES` (28-42) includes services, ingresses, configmaps, secrets, persistentvolumeclaims, which all hit that default and land on a not-found page. **Verified CONFIRMED.**

**Why it matters.** Global search is a headline navigation affordance; clicking a Service/Secret/ConfigMap/Ingress/PVC result dead-ends — it reads as broken.

**Fix tasks:**
1. Change the switch default to deep-link the generic detail route: `router.push(\`/dashboard/clusters/${cid}/${resourceType}/${ns ? ns + '/' : ''}${name}\`)` (matches `resolveDetailSlug` in `lib/k8s-paths.ts`), falling back to the list route when `name` is empty.
2. Add a jest test covering result-click navigation for each `SEARCHABLE_TYPES` value, asserting the path resolves to a known route.
3. Manually verify: search a Service and a Secret, click each, confirm the detail page renders.

**Files:** `frontend/src/app/dashboard/search/page.tsx`, `.../clusters/[id]/[resource]/[...path]/page.tsx`, `frontend/src/lib/k8s-paths.ts`

### F-03 — Admin user-security page is unreachable *(Fix, frontend, S)*

**Evidence.** `app/dashboard/admin/users/[id]/page.tsx` implements four superuser actions (unlock, force-logout, disable TOTP, resync groups; API in `lib/api/account-security.ts:218-246`) but repo-wide grep finds **no navigation to it**. **Verified CONFIRMED.**

**Why it matters.** When an account is locked out (MFA lockout, brute-force lock), there is no way in the UI to click to unlock it, force logout, disable TOTP, or resync groups — the operator must hit the API by hand.

**Fix tasks:**
1. In the RBAC Users tab (`app/dashboard/rbac/page.tsx`), add `onRowClick`/row-action linking each user to `/dashboard/admin/users/${u.id}`.
2. Surface a visible **Locked** badge in the users table (`locked_until`) so admins can find who needs unlocking.
3. Add a jest test asserting the row navigation target; optionally register "User security" in the command palette.

**Files:** `frontend/src/app/dashboard/rbac/page.tsx`, `.../admin/users/[id]/page.tsx`, `frontend/src/lib/api/account-security.ts`

### F-04 — No route-level error/not-found boundaries — uncaught render error white-screens the console *(Addition, frontend, S)*

**Evidence.** `find src/app -name error.tsx -o -name not-found.tsx -o -name global-error.tsx` returns **0 files**; the only error boundary is the extension sandbox's. **Verified CONFIRMED.**

**Why it matters.** A single uncaught render error takes down the whole dashboard rather than a scoped panel — no "Try again," no preserved nav, a blank screen. Rancher-class consoles degrade gracefully.

**Fix tasks:**
1. Add `src/app/dashboard/error.tsx` (client component) rendering `EmptyState` with the error, a "Try again" calling `reset()`, and a "Back to dashboard" link — keeps sidebar/topbar mounted.
2. Add `src/app/not-found.tsx` (and `dashboard/not-found.tsx`) with branded 404 + link to `/dashboard`.
3. Add `src/app/global-error.tsx` as the last-resort boundary for root-layout errors.
4. Optionally add `clusters/[id]/error.tsx` so a failure in one cluster tab preserves cluster nav context.

**Files:** `frontend/src/app/dashboard/layout.tsx`, `frontend/src/components/ui/empty-state.tsx`, new boundary files.

### F-05 — Admin/operations APIs with zero frontend surface *(Addition, frontend, M)*

**Evidence.** Cross-referencing `docs/routes.json` + `routes.go` against the frontend: `/api/v1/admin/siem-forwarders/*` (full CRUD+test+status), `/admin/shell-sessions[/{id}/commands]`, SCIM token endpoints, `/admin/key-status`, and fleet-operations endpoints have **no UI** (`siem-forwarders` appears nowhere in `frontend/src`). **Verified CONFIRMED.**

**Why it matters.** Real admin capability exists in the API but can't be operated from the console — including the shell-session audit trail that closes the loop on the kubectl-shell RCE surface, and encryption key-status that operators need before/after `keyrotate`.

**Fix tasks:**
1. Add a **SIEM forwarders** settings surface (card + `/dashboard/settings/siem`) with list/create/edit/delete, a Test button, and per-forwarder status wired to `/admin/siem-forwarders/*`.
2. Add a **Shell sessions** tab to the audit area listing `/admin/shell-sessions` with drill-down to `/{id}/commands`.
3. Add **SCIM token** management (create/list/revoke) to `/dashboard/settings/auth`.
4. Surface `/admin/key-status` on settings (key id, columns covered, last rotation).

**Files:** `internal/server/routes.go`, `docs/routes.json`, `frontend/src/app/dashboard/settings/page.tsx`, `.../agents/page.tsx`

### O-05 — No alert rules for audit-log, SIEM, or event-bus drop counters *(Addition, ops, S)*

**Evidence.** Metrics exist and are wired (`astronomer_audit_dropped_total`, `astronomer_audit_write_failures_total`, `astronomer_siem_dropped_total{reason=...}`, `astronomer_dropped_events_total`) but **no prometheus rule references any of them**. **Verified CONFIRMED.**

**Why it matters.** Silent audit-trail loss on a platform that sells compliance. If audit or SIEM events start dropping (queue full, retries exhausted), nobody is paged.

**Fix tasks:**
1. Add three warning rules to `prometheus-rules.yaml`: `rate(astronomer_audit_dropped_total[10m]) > 0`, `rate(astronomer_audit_write_failures_total[10m]) > 0`, `rate(astronomer_siem_dropped_total[10m]) > 0` (5–10m windows, component labels).
2. Optionally add `rate(astronomer_dropped_events_total{component="events_bus"}[10m]) > 0` at lower severity.
3. Write runbook pages `audit-events-dropped.md` and `siem-events-dropped.md`, referenced via `runbook_url`.

**Files:** `deploy/chart/templates/prometheus-rules.yaml`, `internal/audit/metrics.go`, `internal/siem/metrics.go`, `internal/observability/drop_metrics.go`

### O-06 — Restore-drill `expectedMinSchemaVersion` is stale (38 vs current 131) *(Fix, ops, S)*

**Evidence.** `deploy/chart/values.yaml:836-840` sets `expectedMinSchemaVersion: 38` with a comment to bump it per migration release; the latest migration is `131_...up.sql`. Nothing keeps it in sync. **Verified CONFIRMED.**

**Why it matters.** The weekly restore drill validates against a floor 93 migrations old, so it can pass on a badly-stale or partially-restored DB and give false confidence in DR.

**Fix tasks:**
1. Add a Go test (`deploy/chart_operational_contract_test.go`) that computes the max version from `internal/db/migrations/*.up.sql` and fails if `values.yaml` is more than N (e.g. 10) behind.
2. Bump `expectedMinSchemaVersion` to a recent floor (e.g. one release back).
3. Longer term, stamp the current max migration into a values override at release time instead of hand-editing.

**Files:** `deploy/chart/values.yaml`, `deploy/chart/templates/management-plane-restore-drill-cronjob.yaml`, `internal/db/migrations`

---

## 7. 🟢 LOW (hardening & polish)

These are real, verified, and worth doing, but none block launch or parity. Grouped by theme.

### Encryption-key name drift (do alongside O-01)

- **R-03 — Raw k8s manifests use bare `ENCRYPTION_KEY` + an invalid Fernet value** *(Fix, S)*. `deploy/k8s/03-secret.yaml:12` sets `ENCRYPTION_KEY` (injected via `envFrom` in `06-`/`08-`), which the binary never reads; the value isn't a decodable Fernet key either. These are the `kubectl apply -f deploy/k8s/` quickstart manifests. **Tasks:** rename to `ASTRONOMER_ENCRYPTION_KEY`, set a valid dev Fernet key (so the known-dev-value prod check still catches it), grep for remaining bare references. *(Dev-labeled, server logs a warning, and prod preflight fail-fasts — hence low.)*
- **C-05 — `keyrotate` reads `ENCRYPTION_KEY`, not `ASTRONOMER_ENCRYPTION_KEY`** *(Fix, S)*. `cmd/keyrotate/main.go:50,63`. **Tasks:** fall back to `ASTRONOMER_ENCRYPTION_KEY` when `ENCRYPTION_KEY` is unset; mention both in the error; add the rotation invocation to the DR runbook.
- **C-11 — `docker-compose.yml` has the same stale name** *(Fix, S)*. Fix in the same pass.

### Worker health

- **C-02 / O-07 — Worker Deployment has no liveness/readiness probes and the worker exposes no health endpoint** *(Addition, S)*. Every other long-running chart component has probes; the worker (a queue consumer) has none, weakening rollout gating and self-healing. **Tasks:** add a `/healthz` to `internal/worker/metrics_server.go` (wrap `promhttp` in a mux; ping Redis via the asynq inspector + a scheduler heartbeat), add liveness/readiness probes to `worker-deployment.yaml` behind a `worker.probes.enabled` value, add a render test.

### Deploy/upgrade correctness

- **C-03 — Upgrade window serves new code against old schema** *(Addition, M)*. `migrate-job.yaml:10` runs migrations `post-upgrade`, so new pods pass readiness before migrations apply, and `/readyz` never checks schema version. **Tasks:** embed the expected schema version in the binaries (generated from the max migration, wired into `make verify`); extend `/readyz` to 503 until `schema_migrations.version` ≥ the embedded minimum; optionally offer a `pre-upgrade` migrate mode for external-Postgres installs; document in the upgrade runbook.
- **C-04 — Bootstrap-password stability relies on helm `lookup`, inert under `helm template`/ArgoCD** *(Fix, S)*. `bootstrap-secret.yaml:15` uses `lookup`, which returns nil at template time, so `randAlphaNum 24` re-fires on every GitOps render. **Tasks:** document that GitOps installs must pin `bootstrap.password` or pre-create the Secret; optionally add a `bootstrap.existingSecret` escape hatch mirroring `postgres.external.dsnSecretRef`.

### Security scoping

- **C-08 — Namespace-scoped RBAC users can't exec/stream-logs in their authorized namespaces via the WS consumers** *(Addition, S)*. `exec_consumer.go:105-114` and `logs_consumer.go` authorize at cluster scope only, passing `uuid.Nil` and ignoring the `{namespace}` URL param — fail-closed, so no security hole, but a namespace-scoped user who *should* have access is denied (parity gap vs Rancher). **Tasks:** thread the `{namespace}` param into `CheckPermission` (cluster-wide grants still pass); align the resource/verb intent with the k8s-proxy's `ResourcePods`/`VerbExec` so both paths agree; add allow/deny tests per namespace.

### Frontend polish

- **F-06 — Settings hub shows all 17 admin-only cards to non-admins (afford-then-deny)** *(Fix, S)*. `settings/page.tsx` renders cards unfiltered; non-admins bounce off a 403 per card. **Tasks:** use `useIsSuperuser()` to render a single "Admins only" state (or only openable cards) for non-superusers; gate the sidebar "Settings" item like the "Auth" item; keep deep-link behavior via `SettingsAuthGate`.

### Ops/runbook/dashboard drift

- **H-02 — Health-check sweep clobbers `clusters.status` from a stale full-fleet snapshot** *(Fix, S)*. `health_check.go:105-151` snapshots the whole fleet then writes snapshot-derived status unconditionally (`UpdateClusterStatus` has no `last_heartbeat` re-check), so a reconnect mid-sweep can be flapped to `disconnected` for one cycle (self-heals on the ~15s publisher pass). **Tasks:** guard the transition in SQL (`... WHERE last_heartbeat < now() - interval '2 minutes'` for disconnected, and the inverse for active), or re-read the cluster before computing status; add a race test.
- **O-08 — Upgrade runbook references a nonexistent `scripts/preflight.sh` and a wrong backup-job label selector** *(Fix, S)*. `docs/upgrade-runbook.md:20,85-86`; the chart stamps `component=management-backup` not `managementBackup`. **Tasks:** point at the real render-time preflight (or add a thin `scripts/preflight.sh`), fix the selector, add a docs-smoke/review check tied to `_helpers.tpl` component names.
- **O-09 — `AstronomerClusterLoggingFlatlined` alert links to a nonexistent runbook** *(Fix, S)*. `prometheus-rules.yaml:396` → `logging-flatlined.md` which doesn't exist. **Tasks:** write `docs/runbooks/logging-flatlined.md`; add a chart test asserting every `runbook_url` basename resolves to a file in `docs/runbooks/`.
- **O-10 — No Grafana dashboard for the management plane itself** *(Addition, M)*. All six shipped dashboards are fleet/workload-facing; nothing visualizes `astronomer_http_requests_total`, worker queue depth/latency, DB pool, agent connections, or backup/restore-drill recency. **Tasks:** add `deploy/dashboards/management-plane.json` (5xx ratio + latency, asynq queue depth/latency by queue+state, task_outbox oldest-due age, DB pool health, agent connections/reconnect rate, backup/restore-drill Job recency); follow the sidecar-label convention so `dashboards-configmap.yaml` auto-loads it.

---

## 8. Rancher Day-2 Parity Assessment

For adopted-cluster operation, Astronomer **matches or beats** Rancher across the day-2 pillars. Where Astronomer already leads: Argo-native GitOps delivery (vs Fleet), the outbound-only agent connectivity model (no inbound firewall paths to managed clusters), encrypted-at-rest credential columns with token hashing, a CRD-native API, and an OpenAPI contract enforced by a 100%-coverage CI gate. The remaining parity gaps are all **additions**, verified as genuine day-2 (non-provisioning) capabilities Rancher has:

- **P-01 — RBAC role inheritance/aggregation is declared but never implemented** *(low)*. `Inherits []string` is parsed on role templates (`internal/rbac/templates.go:68`) but consumed nowhere; no template declares `inherits:`. Rancher's `RoleTemplate.RoleTemplateNames` flattens transitively. **Tasks:** implement a composition pass (`resolveInherited`) that transitively unions permission sets with cycle detection and deterministic order, called at template load in `engine.go`; validate each `Inherits` name resolves to an existing template of compatible scope (fail closed on unknown/cross-scope); expose flattened effective permissions in `rbac_effective.go` so the UI distinguishes inherited from direct grants; add a seeded template using `inherits` + tests for union/cycle/scope-mismatch. *(Downgraded to low: it's a declared-but-dormant convenience feature, not a security gap — current templates fully enumerate permissions.)*
- **P-02 — Resource explorer polls instead of live-watching, despite backend watch support** *(low)*. The backend already has a pod-watch SSE endpoint (`internal/handler/pods_watch.go`), a chunked `?watch=true` passthrough proxy, and an SSE event bus — the frontend still drives updates with react-query polling. **Tasks:** wire `frontend/src/lib/live-events.ts` (or a new hook) to the pods/watch SSE endpoint, upserting rows on ADDED/MODIFIED/DELETED; generalize to arbitrary curated types via `?watch=true` through the `/k8s/*` proxy, folding events into the react-query cache with a polling fallback; drop/lengthen `refetchInterval` once a watch is live; add a test asserting the list mutates from a simulated watch stream.
- **P-03 — Alerting has no Alertmanager inhibition rules** *(low)*. `grep inhibit internal/` → 0 hits. The alerting engine models rules/channels/silences/recovery but not suppression of dependent lower-severity alerts during an incident. **Tasks:** add an inhibition-rule model (source-match/target-match selectors + equal labels) to the alerting schema/API (`internal/handler/alerting.go`); apply it during evaluation (`alert_evaluation.go`) so a firing source suppresses matching targets and releases on resolve; surface matchers in the alerting UI mirroring the silence UI; add source-suppresses-target and source-resolves-releases tests.
- **P-04 — No first-class authoring of custom Gatekeeper/OPA constraints** *(low)*. `internal/gatekeeperpolicy/bundle` ships a fixed 6-resource starter; there's no API to author/edit custom ConstraintTemplates/Constraints. **Tasks:** add a constraint-authoring API/handler that validates submitted YAML (kind/apiVersion, Rego parse) before server-side-applying through the tunnel; track authored constraints alongside the starter bundle so drift-detection treats them as first-class; add a policy UI listing bundle + custom constraints with violation counts (reuse existing violation ingest); add valid/invalid submission + idempotent-apply tests.

---

## 9. Verification Checklist (run before declaring done)

After each wave, re-run the full gate — the same one that is green today, so any regression is obvious:

```bash
# Backend
make build && make verify && go vet ./... && make test

# Frontend
cd frontend && npx jest && npx tsc --noEmit

# Chart (each render-assertion added above)
helm template astronomer deploy/chart --set config.env=production        # expect failure when backups unwired (O-04)
helm template astronomer deploy/chart | kubeconform -strict -            # worker probes (C-02), keep annotation (O-02)
go test ./deploy/ -run 'OperationalContract|Render'                      # secret keep, backup-required, schema-floor, runbook-links
```

**Per-blocker manual verification:**
- **R-01:** as a seeded viewer with no ArgoCD grants, `GET /argocd/api/v1/applications` → expect 403 and no `argocd.token` cookie forwarded; as a granted user → proxied.
- **O-01/O-02:** in a throwaway k3d install, follow the DR runbook's save/verify/restore steps end-to-end and confirm the key round-trips; run `helm uninstall` and confirm `<release>-secrets` survives.
- **O-03:** delete an agent pod, confirm `AstronomerAgentDisconnected` (or its replacement) fires within the `for` window.
- **H-01:** simulate an ungraceful A→B agent move, confirm the redis locator key stabilizes on B and no sustained 503 window.

---

## 10. Appendix — Provenance

Full machine-readable findings (evidence, verdicts, files, effort) are in the audit run artifacts. Each finding above was produced by a dimension-specific auditor that read the cited files, then re-checked by an independent adversarial verifier instructed to reject false positives, already-fixed items, and over-rated severities; only `CONFIRMED` and severity-corrected findings were kept. Counts: 30 verified (1 blocker, 5 high, 8 medium, 16 low; 17 fixes, 13 additions).
