# Astronomer-go Rancher-parity Plan

**Date:** 2026-05-08
**Status:** Approved by user 2026-05-08
**Scope:** What we build next to close the Rancher gap on day-2 operations, while declining to compete on day-0 cluster provisioning.

---

## Strategic posture

> *Astronomer-go targets ~80% of Rancher's day-2 operational surface (tools, monitoring, RBAC, GitOps, backups, audit) while declining to compete on day-0 cluster provisioning. Smaller product, ships faster, defensible.*

Bring-your-own-cluster only. We manage what users point at us. ArgoCD is our native CD (not Fleet). Velero is our native backup. Dex is our identity broker. CIS scans via `rancher/cis-operator`. Other security tooling (NeuVector, StackRox, Trivy, OPA) is recommended-via-tools-catalog, not built-in.

---

## Decisions captured (2026-05-08)

| Area | Decision |
|---|---|
| GitOps engine | **ArgoCD** (full lifecycle: install + manage), not Fleet |
| Backup engine | **Velero** (BackupStorageLocation + Schedule + Backup + Restore CRDs) |
| Auth providers | **Dex shim** — astronomer-go talks OIDC to Dex; Dex handles SAML/LDAP/AD/Azure AD/Okta/etc. |
| Cluster provisioning | **Skip** — BYO cluster only. No EKS/GKE/AKS/RKE2 provisioning. |
| Multi-tenancy | **Full enforcement** — Project membership reconciles ResourceQuota + LimitRange + NetworkPolicy onto each namespace |
| Security scanning | **CIS only** via `rancher/cis-operator`. NeuVector/StackRox/Trivy are user-installed via the tools catalog, not built in. |
| Live updates | **Per-cluster k8s informers** — agent watches; deltas push through tunnel → SSE → UI invalidation |
| Quick wins | **All four** — generic OIDC, OCI catalog, cross-cluster resource search, honest ArgoCD reconciler |

---

## Phase A — Quick wins (target: 1–2 days)

Land these first. Each is small, each closes a real-world bug or unlocks a marquee feature.

### A1. Generic OIDC provider
**Why:** Unblocks Keycloak / Authentik / Auth0 / Okta-OIDC / any conformant IdP. Today `internal/auth/oauth.go:73-84` rejects anything beyond `github`/`google`.

**Scope:**
- Accept arbitrary `issuer_url` on the SSO provider config.
- On registration, fetch `<issuer>/.well-known/openid-configuration` and discover `authorization_endpoint`, `token_endpoint`, `userinfo_endpoint`, `jwks_uri`.
- Cache the discovery doc; refresh on token validation failure.
- Validate ID tokens via JWKS instead of provider-specific user lookup.

**Files:** `internal/auth/oauth.go`, `internal/auth/oidc_discovery.go` (new), `internal/handler/sso.go` (route already exists).

**Acceptance:** Register a Keycloak realm with no provider-specific code; sign in successfully.

### A2. OCI Helm registry support
**Why:** Bitnami, GHCR, ECR, Cloudsmith all serve charts via OCI. Today catalog ingest only handles `<repo>/index.yaml`.

**Scope:**
- Detect `oci://` URLs in `CreateRepository`.
- Use `helm.sh/helm/v3/pkg/registry` (already in go.mod via tunnel agent's helm SDK) to list and pull charts.
- Reuse the existing `HelmChart` / `HelmChartVersion` schema; add `repo_type='oci'` so the UI can show a different icon.

**Files:** `internal/handler/catalog.go` (`fetchAndIngestRepoIndex`), maybe a small `internal/handler/catalog/oci.go`.

**Acceptance:** Add `oci://ghcr.io/argoproj/argo-helm` as a repo, list charts, install one through our tunnel.

### A3. Cross-cluster resource search
**Why:** Marquee feature most multi-cluster UIs lack. Find all `Pod`s across all clusters matching a label selector.

**Scope:**
- New endpoint `GET /api/v1/resources/search/?type=pods&label=app=foo&namespace=...`
- Fan out the existing per-cluster `resources.go` listing across all `status='active'` clusters in parallel.
- Aggregate + return with a `cluster_id` + `cluster_name` field on each item.
- Frontend: a global search bar in the topbar that opens results page.

**Files:** new handler `internal/handler/resources_search.go`, route in `routes.go`, frontend page `dashboard/search/page.tsx` + topbar input.

**Acceptance:** Search "pods labeled app=coredns" finds matches across all connected clusters.

### A4. Honest ArgoCD reconciler
**Why:** Currently `argocd.go:1005-1021` writes `SyncStatus="Synced"` to our DB without calling ArgoCD. Demos lie.

**Scope:**
- Replace the fake sync branch with a real `POST {instance.api_url}/api/v1/applications/{name}/sync` (auth via decrypted `auth_token`).
- Honor body `{revision, prune, dry_run}` flags from our API.
- Surface ArgoCD's response status / operation phase back into our `argocd_operations` table.

**Files:** `internal/handler/argocd.go` only.

**Acceptance:** Trigger a sync from our UI; the upstream ArgoCD UI shows the sync started; our status reflects ArgoCD's truth, not a fabricated row.

---

## Phase B — Foundation (target: 1–2 weeks)

These are the structural rebuilds. Most depend on Phase A (e.g. honest ArgoCD reconciler is a precursor to the full ArgoCD lifecycle).

### B1. ArgoCD full lifecycle (`tools.go` install + Application/AppProject/ApplicationSet management)
**Why:** Decision: ArgoCD is our GitOps engine. Today's handler is a registry + (after A4) a sync proxy. Need to make it a *manager*.

**Scope:**
1. **Install path** — add ArgoCD to `internal/handler/tools.go` catalog. Helm chart `argo-cd` from `https://argoproj.github.io/argo-helm`. Tool slug `argocd`. Per-cluster install via existing tools UI.
2. **Application CRUD** — endpoint `POST /api/v1/argocd/instances/{id}/applications/` creates an `Application` CRD on the upstream ArgoCD (not just a row in our DB). PATCH/DELETE round-trip too.
3. **AppProject CRUD** — `POST /api/v1/argocd/instances/{id}/projects/` creates `AppProject` for multi-tenant guardrails.
4. **ApplicationSet** — Generators (List, Cluster, Git) for fan-out across our managed clusters. Critical for "deploy this Helm release to all prod clusters" UX.
5. **Cluster registration** — `argocd cluster add` equivalent: register each of *our* connected clusters into the upstream ArgoCD via `argocd-cm` ConfigMap. Use the agent's existing kubeconfig generation.
6. **Repo credentials** — secrets management (Git creds, SSH keys, image pull secrets) round-tripped through our Encryptor.

**Files:** extend `internal/handler/argocd.go`; new `internal/handler/argocd/` subpackage if it grows; tools catalog entry.

**Acceptance:** From our UI: install ArgoCD into cluster X, register clusters X+Y, create an ApplicationSet that deploys the same chart to both, watch both clusters reconcile.

### B2. Velero backup engine
**Why:** Replace the stub `worker/tasks/backup_execution.go` with a real backup. Trust over velocity.

**Scope:**
1. **Tools catalog entry** for Velero (`vmware-tanzu/velero` Helm chart).
2. **CRD round-trip** — our `BackupStorageConfig` row creates a Velero `BackupStorageLocation`. Our `BackupSchedule` creates a Velero `Schedule`. Manual `trigger-now` creates a `Backup` CR. `Restore` row creates a `Restore` CR.
3. **Status sync** — agent watches Velero CRs for status; pushes to server; UI shows real progress + errors.
4. **Remove the stub** — `worker/tasks/backup_execution.go:73-77` deleted.

**Files:** `internal/worker/tasks/backup_execution.go` (rewrite), `internal/handler/backups.go` (now proxies CRDs through tunnel), tools catalog entry.

**Acceptance:** Configure an S3 backend, schedule daily backup of namespace X, trigger now, see Velero artifacts in S3, restore to a different namespace.

### B3. Project enforcement controller
**Why:** Today's `projects.go` accepts a `resource_quota` JSON blob and stores it. Nothing enforces it. UI lies.

**Scope:**
1. On `AddNamespace` and on cluster reconnect, agent applies:
   - `ResourceQuota` matching project's `resource_quota` field (cpu / memory / pods / storage).
   - `LimitRange` for default container requests/limits if project specifies.
   - `NetworkPolicy` for pod-to-pod isolation across project boundary (deny-all by default; allow same-project).
2. Drift detection: agent watches for deletes/edits and re-applies (informer-based).
3. Worker: `worker/tasks/project_reconcile.go` — periodic sweep across all projects.
4. UI: badge "enforced" / "drift detected" on each namespace.

**Files:** new `internal/agent/project_reconciler.go`, `internal/worker/tasks/project_reconcile.go`, `internal/handler/projects.go`.

**Acceptance:** Set quota of 4 CPU / 8Gi mem on a project; add namespace `team-a`; `kubectl describe quota -n team-a` shows the limit; deleting it manually causes the agent to re-apply within 30s.

### B4. Dex shim for enterprise auth
**Why:** SAML / LDAP / AD / Azure AD coverage. Decision: don't port Rancher's six providers; let Dex broker.

**Scope:**
1. **Tools catalog entry** for Dex (`dexidp/helm-charts/charts/dex`). Single-instance deployment in the management cluster.
2. **Pre-canned configs** in our UI for common scenarios:
   - Azure AD (tenant + client ID + secret)
   - LDAP / AD (server, bind DN, search base)
   - SAML (metadata URL or XML)
   - Okta (issuer + client + secret)
   - GitLab, Bitbucket, etc.
3. Each user-input config writes a `dex.config` connector entry; Dex hot-reloads.
4. Astronomer-go itself registers Dex as just another OIDC provider (via A1's generic OIDC). One issuer, one client.
5. UI: a wizard under Settings → Authentication that walks through each connector type.

**Files:** tools catalog entry; new `internal/handler/dex_config.go` for config CRUD; SSO provider table gets a new `kind='dex'` row variant.

**Acceptance:** Configure Azure AD via the wizard; user logs in with their corporate AAD account; lands in our dashboard with claims-mapped roles.

### B5. CIS scans via cis-operator
**Why:** Compliance reports without us writing the scanner.

**Scope:**
1. **Tools catalog entry** for `rancher/cis-operator` (Apache 2.0).
2. Agent surfaces `ClusterScan`, `ClusterScanProfile`, `ClusterScanReport` CRDs via existing tunnel proxy.
3. Our `security.go` `SecurityScanResult` table mirrors the latest scan's report; `worker/tasks/security_scan.go` triggers a scan + ingests the report.
4. UI: Security → CIS shows pass/fail + severity.

**Files:** `internal/handler/security.go`, `internal/worker/tasks/security_scan.go`, tools catalog entry.

**Acceptance:** Run a scan on a cluster, see CIS-1.7 results in the UI, rescan after fixing one finding, see it pass.

**Out of scope (defer / recommend external):** OPA/Gatekeeper, NeuVector, StackRox, Trivy. Each can be installed via tools catalog when users want them; no native handler.

### B6. Per-cluster k8s informers → SSE live updates
**Why:** Make the whole UI live without polling. Today the dashboard is live (cluster status + metrics); detail pages still poll.

**Scope:**
1. **Agent side:** start `informers.SharedInformerFactory` for the agent's clientset. Watch Pods, Deployments, ReplicaSets, StatefulSets, DaemonSets, Services, Events, Nodes, ConfigMaps, Secrets (metadata only).
2. **Tunnel protocol:** new message types `WATCH_EVENT` carrying `{kind, namespace, name, op: added|modified|deleted, resource_version}`. Coalesce events at 1s windows on the agent to avoid flooding.
3. **Server side:** subscribe per-cluster watches to its own internal channel; publish `cluster.k8s_changed` events with `{cluster_id, kind, namespace, name, op}` payload onto the SSE bus.
4. **Frontend:** existing `useLiveQueryInvalidation` already subscribes to `cluster.k8s_changed` on the resource pages. Just gets real now.

**Files:** `internal/agent/informers.go` (new), `pkg/protocol/types.go` (new types), `internal/tunnel/handler.go` (route WATCH_EVENT → bus.Publish), agent main.go.

**Acceptance:** Open a cluster's pod list; create a pod in that cluster via kubectl; see the row appear in the UI within 2 seconds without a manual refresh.

---

## Phase C — Polish (target: 1 week, after Phase B lands)

### C1. System chart auto-management
Match Rancher's `pkg/controllers/dashboard/systemcharts/` — auto-install + auto-upgrade `rancher-webhook`, `cert-manager` (when needed), and our own admission helpers. Worker-driven.

### C2. Etcd snapshot reads
Read existing etcd snapshots from k3s/RKE2 nodes; expose under `/clusters/{id}/snapshots/`. **Read-only**: we don't take or restore them, just surface what's there. Pairs nicely with Velero (which handles workload state, not control plane).

### C3. Cluster Tools catalog growth
Currently `tools.go` is good but sparse. Add curated entries:
- ArgoCD (B1)
- Velero (B2)
- Dex (B4)
- cis-operator (B5)
- cert-manager
- ingress-nginx (or NGINX Gateway Fabric)
- metrics-server (often missing on bare clusters)
- monitoring stack (Prometheus + Grafana, already partially there)
- Loki / Promtail
- NeuVector (recommended for runtime security)
- Trivy operator (recommended for image scanning)

### C4. RoleTemplates
Reusable RBAC bundles — `RoleTemplate` table + UI for defining and applying. Inherited by ProjectRoleBindings.

---

## Out of scope (explicit decisions)

These are intentionally NOT in this plan. Revisit only if user demand justifies the cost.

- **Cluster provisioning** — RKE2/k3s/EKS/GKE/AKS. Position as BYO cluster.
- **Node drivers** (vSphere, Linode, DO, Harvester, etc.).
- **Pipelines / CI** — dead even in Rancher.
- **Steve API / Norman abstraction** — our `/resources/` proxy is sufficient.
- **NeuVector / StackRox / OPA / Trivy** as native handlers — install-via-catalog only.
- **Rancher-backup operator** — Velero covers our needs; Postgres backup is a separate ops concern.
- **Cluster templates / hardened profiles** — ad hoc via cluster-tools catalog.

---

## Sequencing rationale

Phase A first because each is <1 day and each removes a current lie or unlocks a marquee feature. **A4 (honest ArgoCD reconciler) is a hard prerequisite for B1 (ArgoCD full lifecycle)** — get the foundation truthful before stacking more on top.

Phase B in roughly the order listed: B1 → B2 → B3 are independent and can fan out. B4 depends on A1 (generic OIDC). B5 is small and standalone. B6 is the biggest single piece and depends on protocol additions; it's also the most user-visible quality-of-life win after B1.

Phase C is polish — runs in parallel with bug fixing once the parity story is intact.

---

## Acceptance criteria for "Rancher parity reached"

The user can demo the following end-to-end without leaving astronomer-go's UI:

1. ✅ Sign in via Azure AD (Dex)
2. ✅ Register two existing k8s clusters
3. ✅ Browse pods/deployments/services on either, see live updates without refresh
4. ✅ Create a Project, add namespaces from both clusters, set quota — see ResourceQuota applied
5. ✅ Install ArgoCD into Cluster A from the tools catalog
6. ✅ Create an ApplicationSet that deploys a Git-tracked Helm chart to both clusters
7. ✅ Trigger sync, watch reconciliation in real time
8. ✅ Configure S3 backup destination, schedule a daily Velero backup
9. ✅ Run a CIS scan, view findings
10. ✅ Receive a Slack notification on a Prometheus alert firing across either cluster

Hit those 10 in order from a fresh deploy and we're at parity.

---

## Open items / unknowns

- **Dex deployment topology** — single Dex per management plane vs per-tenant Dex? Decision deferred; default to single.
- **ArgoCD multi-tenancy model** — one ArgoCD per cluster, or one global ArgoCD that manages all? Probably the former (cluster-local install) but worth validating with first design call.
- **Cross-cluster RBAC for ApplicationSet generators** — who's allowed to fan out an Application across clusters they don't have role bindings on? AppProject restrictions work, but our UI needs to surface this clearly.
- **Velero CSI snapshots** require the cluster to have a CSI driver with snapshot support. Document a "supported environments" matrix before release.
- **CIS profile selection** — RKE2-hardened, k3s-hardened, generic-cluster — these aren't the same. Default to whichever matches `cluster.distribution`.

---

## Files to drill into when starting work

- ArgoCD stub: `internal/handler/argocd.go:977-1021`
- Backup stub: `internal/worker/tasks/backup_execution.go:73-77`
- Project quota no-op: `internal/handler/projects.go:48,152,209`
- OAuth provider gate: `internal/auth/oauth.go:73-84`
- Tools catalog: `internal/handler/tools.go` (template for B1/B2/B4/B5)
- Tunnel protocol: `pkg/protocol/types.go` (extension point for B6)
- Bus: `internal/events/bus.go` (extension point for B6)
- Existing SSE bus consumers: `astronomer/frontend/src/lib/live-events.ts`
