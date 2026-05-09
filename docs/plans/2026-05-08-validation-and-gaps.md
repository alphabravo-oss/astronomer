# Astronomer-go vs Rancher: Validation Report &amp; Remaining Gaps

**Date:** 2026-05-08
**Scope:** Deep audit after Phase A + Phase B shipped. What actually works end-to-end vs what's wired in code only. What's still missing strategically.

---

## TL;DR

We shipped a lot of correct backend code. Several pieces have integration debt that means they're **not actually functional from a user perspective even though tests pass**. There are also six structural gaps not in the original Rancher comparison that materially affect production-readiness.

**Critical gaps you need to know about:**
1. **B6 (live k8s state subscriber) wired but not flowing.** Agent logs `state subscriber started`. Server receives 0 `MsgStateUpdate` messages despite cluster activity. Likely a send-side drop or limiter bug. Must fix before claiming "live UI everywhere."
2. **Phase B features have no frontend UI.** ArgoCD lifecycle / Velero / CIS / Dex backends shipped. The pre-existing dashboard pages (`dashboard/argocd/`, `dashboard/backups/`, `dashboard/security/`) don't call any of the new endpoints. **No Dex page exists at all.** Users cannot exercise B1/B2/B4/B5 from the UI.
3. **Audit logging is partial.** Only `projects.go` writes `audit_logs` rows. ArgoCD sync, Velero schedule create, Dex connector add, security scan trigger, login — none of these record audit events. Compliance gap.
4. **No backup of our own state.** Postgres is a single StatefulSet pod with one PVC. If it dies, all DB-side state (clusters, projects, RBAC, audit, scans, schedules) is gone. Plan said "skip rancher-backup operator" — that decision needs revisiting.
5. **Single-replica everything.** `replicaCount: 1` for server, worker, frontend; one Postgres pod. No HA story. Acceptable for demo, blocker for production.
6. **No self-observability.** No `/metrics` endpoint on the API server. No tracing. No structured request log shipping. Operators can't see latency/error rate of the system itself.

---

## 1. Per-feature validation

### A1 — Generic OIDC ✅ functional
- Discovery cache + JWKS cache + claim mapping land cleanly. 22/22 tests pass.
- Code reuses existing `sso_configurations` JSONB; no schema change needed.
- **Demo gap:** never tested against a real Keycloak / Auth0 in this run. Code path is exercised only by the `httptest.Server` mock. **Recommend:** spin up Keycloak in the local cluster, configure a realm, log in once — then mark "verified in prod."

### A2 — OCI Helm registry ✅ functional
- `oci://` URLs branch cleanly; explicit-charts-list strategy honest about what's possible.
- Agent's Helm SDK already supports OCI install (`registry.NewClient`).
- **Demo gap:** no live OCI repo added in this session. Recommend a smoke test against `oci://ghcr.io/argoproj/argo-helm` for `argo-cd` chart pull.

### A3 — Cross-cluster search ✅ functional, demonstrated
- Backend fan-out across 4 clusters returned 4/4 results, 0 failed. Working.
- Topbar input + `/dashboard/search` page both deployed.
- **Caveat:** RBAC gate is `workloads:read`; should be per-resource-type for fine-grained tenants. Document or refactor.

### A4 — Honest ArgoCD reconciler ✅ functional
- Typed client + 30s × 60-attempt poller in place. Migration 018.
- **Demo gap:** no actual ArgoCD instance running anywhere — none of this code path has been exercised against a real ArgoCD server in this session.

### B1 — ArgoCD full lifecycle ⚠️ wired but undemonstrated
- 17 new endpoints respond 200. Migration 019. ApplicationSet generators implemented.
- **Critical:** **No frontend UI calls these endpoints.** `dashboard/argocd/page.tsx` exists from the Python era and queries the OLD argocd handler, not the new B1 routes. Users cannot create Applications/Projects/ApplicationSets via UI.
- **Critical:** No real ArgoCD has been installed via the tools catalog — entire B1 flow is API-only at this point.
- Recommend: install ArgoCD in `local` cluster via tools API, register `prod`/`staging`/`dev` as ArgoCD destinations, create an ApplicationSet, watch reconciliation. **Until this happens, B1 is theoretical.**

### B2 — Velero backup engine ⚠️ wired but undemonstrated
- Real SigV4 S3 probe replaces stub. CR rendering + apply path works in unit tests.
- Encryptor wired into S3 cred handling (verified in code).
- **Critical:** **No frontend UI** for backups CRUD. The pre-existing `dashboard/backups/page.tsx` calls the legacy backups handler, not the new Velero-aware paths.
- **Critical:** No real Velero has been installed in any cluster. End-to-end backup of a real namespace has not been tested.
- Schedule reconciler started but no status-poll loop verified yet against a running Velero.

### B3 — Project enforcement ✅ functional, demonstrated
- Created project `team-alpha` with quota; namespace labeled and `ResourceQuota astronomer-quota` applied on `prod` cluster. Verified via `kubectl --context k3d-prod -n team-alpha get resourcequota`.
- **Caveat:** server log showed one warning `"label namespace: cluster agent not connected"` during a tunnel reconnect — eventual consistency saved us, but the apply path isn't transactional. If the agent disconnects mid-apply, the state could be partial (label applied, quota not, etc.). Fine for a 5-min sweep, worth knowing.
- **Wiring quirk:** the WORKER process logs `"project reconcile runtime not configured, skipping"` because the worker has no tunnel access. The SERVER's in-process reconciler (configured via `tasks.ConfigureRuntime`) is what actually reconciles. Two separate reconciler instances; one is a no-op. Document or remove the worker registration.

### B4 — Dex shim ⚠️ backend-only
- 10 connector types in catalog, validator + renderer + apply path all present. 17 test sub-cases pass.
- Migration 023 + tools catalog entry.
- **Critical:** **No frontend at all** — no `app/dashboard/auth/dex/` pages, no wizard, no even a list view. Users cannot configure Dex through the UI.
- **No real Dex has been installed.** Hot-reload of the ConfigMap is theoretical; depends on the Helm chart's preset values mounting `astronomer-dex-config`. The migration's preset claims it does but this hasn't been verified against a running Dex pod.

### B5 — CIS scans ⚠️ wired but undemonstrated
- Endpoint live; scan creation issues `ClusterScan` CR via tunnel; in-process poller flattens reports.
- Migration 022 + tools catalog entry.
- **Critical:** Pre-existing `dashboard/security/page.tsx` doesn't call the new `/security/scans/{id}/`, `/security/profiles/`, or CSV export. UI is stale.
- **No cis-operator installed yet** in any cluster.

### B6 — Live k8s state subscriber ❌ NOT WORKING (despite passing tests)
- Code exists. Agent logs show `state subscriber started`. **Server logs show 0 `MsgStateUpdate` messages received.**
- Tested by creating `probe-pod` in dev cluster — no `cluster.k8s_changed` event ever fired on the server SSE bus.
- Hypothesis: agent's per-key 1s rate limiter is over-aggressive at startup, OR the tunnel.Send call is silently dropping into a full send channel (256 buffer), OR there's a code-path bug where the informer's callbacks never trigger the dispatch.
- **Action required:** instrument the agent's `dispatch()` function with a debug log per emit, redeploy agent image, observe.

---

## 2. Cross-cutting validation findings

### Audit logging (compliance) ❌
```
SELECT action, resource_type FROM audit_logs WHERE created_at > now() - '1 hour'
                  →  project.create, project.add_namespace
```
Only B3 writes audit rows. **Missing audit for:** login/logout, ArgoCD app create/sync/delete, Velero schedule create, Dex connector add, CIS scan trigger, RBAC role binding changes, cluster register/delete, project quota update.

For SOC2/ISO/PCI the audit log is the FIRST thing an auditor asks for. Right now it would fail any external audit.

### Encryption at rest ⚠️ partial
- ✅ `dex_config.go` uses Encryptor for `client_secret`, LDAP `bindPW`.
- ✅ `argocd.go` decrypts `auth_token_encrypted`.
- ❓ `argocd/repos.go` — Git creds and SSH keys: not verified in this audit. Fix expected per the agent's report; verify.
- ❓ `backups_velero.go` — S3 access_key/secret_key encrypted? Agent reported yes, not visually verified.
- ❌ Webhook receivers for OAuth (state cookies) — no signature/HMAC validation seen.

### Self-observability ❌
- No `/metrics` endpoint exposing API server's own Prometheus metrics (request count, latency, error rate, in-flight requests).
- No OpenTelemetry tracing wired.
- No structured access-log shipping (we log to stdout but no SIEM target).
- No /readyz that distinguishes "DB up" from "tunnel hub up" from "asynq broker up."

### Rate limiting / abuse protection ❌
- `POST /api/v1/auth/login/` is unthrottled. A brute-force attempt is not rate-limited.
- `POST /api/v1/clusters/` and `POST /api/v1/clusters/{id}/register/` are protected by RBAC but not rate-limited; an authed user could fan out tokens.
- No CAPTCHA / challenge for the bootstrap flow.

### Webhook story ❌
- No `/webhooks/git/` for Git push → ArgoCD sync trigger. Standard pattern.
- No HMAC verification helpers.
- No inbound notification webhook for Alertmanager → our alerting events table.

### High availability ❌
- `replicaCount: 1` on server, worker, frontend.
- Single-pod Postgres StatefulSet (no replication, no PITR).
- Single-pod Redis (no AOF backup, no Sentinel).
- Single-pod ingress (NGINX Gateway Fabric).
- No leader election in worker → safe for now because we have one. If we scale to 2 workers, periodic tasks will double-fire.

### Disaster recovery ❌
- No documented procedure for "Postgres pod died — how to recover."
- No documented procedure for "k3d cluster wiped — how to re-bootstrap with the same data."
- The chart has no `pre-restore` Job to ingest a Velero backup of the management plane's namespaces.

### Frontend completeness ❌ critical
| Phase B feature | Backend | Frontend |
|---|---|---|
| ArgoCD lifecycle (B1) | ✅ 17 new routes | ❌ stale page calling old routes |
| Velero backup (B2) | ✅ real CR round-trip | ❌ stale page |
| Project enforcement (B3) | ✅ ResourceQuota+LimitRange+NetworkPolicy | ⚠️ project page may not show enforcement state |
| Dex (B4) | ✅ full handler | ❌ NO PAGE AT ALL |
| CIS scans (B5) | ✅ scan trigger + report ingest | ❌ stale page |
| k8s informers (B6) | ⚠️ wired, not flowing | n/a (page uses existing live-events hook which is correct) |

---

## 3. Strategic gaps NOT in the original Rancher comparison

These are real Rancher features we didn't inventory the first time around. Each is a defensible "Phase D" entry.

### Pod Security Admission reconciler
Rancher applies `pod-security.kubernetes.io/{enforce,warn,audit}` labels to namespaces based on the project's security profile. We have B3 NetworkPolicy reconciliation but **no PSA labels**. PSA is the k8s-native successor to PSP and is the default expectation for any "secure-by-default" platform.

### Image pull secret propagation
Rancher copies a project's image pull secrets to every namespace in the project. We don't. Common problem when teams use private registries.

### Built-in role catalog parity
Our seeded roles (`Administrator`, `Standard User`, `Cluster Owner`, `Cluster Member`, `Project Owner`, `Project Member`) — six roles. Rancher ships ~20 fine-grained roles (`view`, `edit`, `cluster-admin`, `members-edit`, `restricted-admin`, `nodes-view`, `secrets-edit`, etc.). Customers will compare side-by-side and notice.

### Cluster events live stream
A "tail -f" for the events.k8s.io API across all clusters. Rancher shows a "fleet events" view. We don't have one. (Trivial to build once B6 actually works.)

### Web terminal in the UI
We have `/api/v1/ws/exec/` but no UI page that consumes it. Rancher's "Launch kubectl shell" button is one of its most-used features.

### Inbox / bell-icon notifications
The topbar has a bell, but does it actually populate? The alerting handler creates events; do they pipe to a per-user inbox? Spot check needed.

### Onboarding wizard
First login post-bootstrap currently lands on a blank dashboard. Rancher walks through "register your first cluster," "configure auth," "install monitoring." Drives adoption.

### Helm app values forms (vs raw YAML)
Rancher renders a JSON-Schema-driven form for chart values. We probably show a textarea. UX gap, especially for non-developer admins.

### Fleet "Bundles" equivalent
A bundle = multiple Helm releases + raw manifests + secrets, deployed atomically across clusters. ArgoCD's `ApplicationSet` covers most of this but the "atomic group of mixed-source resources" is a Fleet concept that doesn't quite map. Decide if we care.

### Cluster comparison view
"Show me what's different between staging and prod" — diff of installed tools, chart versions, namespace count, RBAC bindings. Rancher partially supports this. Good marquee feature once B6 events flow.

### TLS / cert-manager integration in the chart
Today the chart ships HTTP. Adding cert-manager as a tool catalog entry + auto-issuing certs for the gateway is table stakes for any production install.

### OAuth state-token tamper-evidence
The OAuth callback flow uses a `state` cookie (presumably). Verify it's HMAC-signed and time-bound — otherwise CSRF is possible.

---

## 4. What should we do, in order

### P0 — Block "ready for production demo"
1. **Fix B6 informers actually flowing.** Add debug logs, find the silent drop. Without this, the dashboard's headline live-update story is unrealized.
2. **Add audit logging to all new endpoints.** Define a single `audit.Record(ctx, action, resourceType, resourceID, ...)` helper, sprinkle at every mutating handler. ~1 day.
3. **Wire one frontend page per Phase B feature** to the new endpoints. Even minimal: a list view + "create new" form. ~3-5 days for all four (B1 ArgoCD, B2 Velero, B4 Dex, B5 CIS).
4. **Demonstrate one Phase B feature end-to-end** against a real upstream. E.g. install ArgoCD via tools catalog, register clusters, create an ApplicationSet, watch sync. **Until we do this once, Phase B is theoretical.**

### P1 — Block "production install"
5. **Postgres backup story.** Either the rancher-backup operator on a schedule, or wire `pg_dump → S3` via Velero (since we now have it).
6. **HA chart values.** `replicaCount: 3` for server, 3 for frontend, optional Postgres replication via Bitnami's HA chart. ~1 day for the chart, more for verification.
7. **`/metrics` endpoint** with prometheus client_golang. ~2 hours.
8. **Rate limiter on `/auth/login/`** — `httprate` middleware (already used by chi). 30 minutes.
9. **PSA reconciler in B3.** Extend the project reconciler to also set namespace labels. ~half a day.

### P2 — Block "marquee parity"
10. **Built-in role catalog growth** — match Rancher's 15-20 standard roles. Migration + UI.
11. **Web terminal UI** — page that opens the existing `/api/v1/ws/exec/` WebSocket.
12. **Cluster events stream view** — once B6 flows, `/dashboard/events/` aggregates across clusters.
13. **Helm values forms from JSON Schema** — render a form when chart's `values.schema.json` exists.
14. **Onboarding wizard** — gated on `bootstrap_completed && first_user_login`.
15. **Image pull secret propagation in projects.**
16. **OAuth state-token HMAC** — pen-test fix.
17. **TLS / cert-manager** — as a tool catalog entry + ingress annotation.
18. **Webhooks** — `/webhooks/git/`, `/webhooks/alertmanager/` with signature verification.

### Out of scope (confirmed)
- Cluster provisioning (RKE2/EKS/GKE/AKS).
- Pipelines.
- Steve API / Norman.
- NeuVector/StackRox/OPA/Trivy as native handlers.

---

## 5. Validation checklist for the next cycle

When we say "Phase B is done" we should be able to show, in one screen-recording, against a fresh cluster:

```
[ ] Bootstrap an admin user.
[ ] Sign in with Azure AD via Dex (or simulated OIDC).
[ ] Register two existing clusters via the install template.
[ ] Browse pods/deployments on either, see live updates within 1s of kubectl create.
[ ] Create a Project with quota; add namespaces from both clusters; verify ResourceQuota + LimitRange + NetworkPolicy + PSA label all applied.
[ ] Install ArgoCD into Cluster A from the tools catalog.
[ ] Register both clusters as ArgoCD destinations.
[ ] Create an ApplicationSet that deploys a Git-tracked Helm chart to both.
[ ] Trigger sync, watch reconciliation in real time (UI auto-updates from B6 events + B1 polling).
[ ] Configure S3 backup destination, run a Velero backup of one namespace, verify objects in S3.
[ ] Run a CIS scan, view findings.
[ ] Receive a Slack notification on a Prometheus alert firing.
[ ] Search "pods labeled app=foo" → see results across both clusters.
[ ] Audit log shows every action above (login, register, install, sync, backup, scan).
```

We have not run this checklist yet. Phase A + B's true completion gates on it.

---

## Files referenced

- B6 not flowing: `internal/agent/state_subscriber.go:381 lines`, `internal/tunnel/handler.go:53` (case wired)
- Stale frontend pages: `astronomer/frontend/src/app/dashboard/{argocd,backups,security}/page.tsx`
- Missing Dex frontend: should be `astronomer/frontend/src/app/dashboard/auth/dex/`
- Audit log gap: only `internal/handler/projects.go` calls `CreateAuditLog`
- HA gap: `astronomer-go/deploy/chart/values.yaml:43,66` `replicaCount: 1`
- No metrics endpoint: search `internal/server/routes.go` for `/metrics` returns nothing
- No rate limiter: search `internal/server/middleware/*.go` for `rateLim|throttle` returns nothing
