# astronomer-go sprint progress snapshot

_Updated 2026-05-12 ~19:47. Branch: `main` @ `8844890`. Live `.247` at migration head **074**, server version `8844890`._

## Cumulative shipped

URL: `http://astronomer.5.78.101.247.nip.io:8080/` · admin / `j3mMt0GJVtkQ3fYltBgV`

| Sprint | Headline features | Migrations | Status |
|---|---|---|---|
| 15 | SIEM forwarder · Fleet ops · Maintenance windows | 055–057 | live |
| 16 | Dashboard widgets · Notification templates · GitOps registration · BYO catalogs | 058–061 | live |
| 17 | Image scans · Read audit · Compliance baselines · Kubectl shell + wterm | 062–065 | live |
| 18 | Cluster groups · Vault · NetPol templates · CRD-mirror v2 | 066–069 | live |
| 19 | Apiserver allow-list · Service mesh · Anomaly detection · Catalog ratings | 070–073 | live |
| 20 | Platform-baseline auto-attach · kubectl image preload · empty-state CTAs | 074 | live |

## Pending (sprint 21 — seeded catalogs at bootstrap)

Closes the last gap in the auto-attach flow uncovered during sprint 20 live testing. When the platform-baseline template auto-attaches on cluster register, the reconciler looks up each tool's slug (trivy-operator, kube-state-metrics, node-exporter, fluent-bit, cert-manager) in `helm_charts`. If the upstream `helm_repositories` haven't been registered + synced yet (the default on a fresh install), every slug resolves to nothing → all five tool installs fail per-row.

**Sprint 21 fix**: seed the chart's recommended helm_repositories at bootstrap + kick an initial `catalog:sync` so the slugs resolve out-of-the-box.

| Repo | Charts we care about |
|---|---|
| Bitnami (`https://charts.bitnami.com/bitnami`) | kube-state-metrics, node-exporter, fluent-bit |
| Aqua Security (`https://aquasecurity.github.io/helm-charts`) | trivy-operator |
| Jetstack (`https://charts.jetstack.io`) | cert-manager |

Migration **075** seeds the three rows (idempotent ON CONFLICT). Bootstrap kicks `catalog:sync` once at first-boot if the helm_charts table is empty.

## Tech-debt punch list (carry forward)

- **060**: Fernet-encrypt `gitops_registration_sources.auth_encrypted`.
- **062**: Wire Trivy `Ingester.AuditHook` once a CRD-mirror event dispatcher exists.
- **064**: Webhook + SMTP delete enforcement when listed in an active baseline's `required_*` field.
- **065**: Audit fan-out for kubectl session `expired`/`reaped` worker events. Register the active-sessions gauge.
- **066**: Fleet selector `group_id` branch — schema + API field shipped; selector evaluator doesn't expand group_id yet. Sidebar hierarchical tree + cluster-detail "Move to group" deferred.
- **067**: `vault.reference.resolved` / `.failed` audit-log rows (metrics-only today).
- **068**: Audit `drift_detected` / `reconciled` actions for NetworkPolicy reconciler.
- **069**: `astronomer_crd_mirror_rows` gauge populator; `CustomResourceDefinition` self-mirror.
- **070**: AKS / DOKS / SelfManaged real provider drivers (EKS + GKE done; rest are detect-only scaffolds).
- **074**: Catalog chart-install page accepting `?cluster_id=`; dedicated Image-Scans frontend tab; `kind=builtin` enforcement on cluster_templates handler.

## Cumulative roadmap remaining

Larger / not-yet-scoped:

- Cost allocation per project (L effort, billing-API plumbing)
- Anomaly v2: cross-cluster baselines ("this cluster's CPU is 4σ above fleet mean")
- Per-namespace fine-grained RBAC mirror for kubectl shell
- Bulk cluster operations richer UI (fleet-ops backend is rich; UI sparse)
- Application marketplace recommendation engine v2 (cross-tenant, requires telemetry opt-in)
- IP geolocation badges on audit log
- Helm `--atomic --wait` toggle per project

User declined: licensing management.

## Process notes (carry forward — unchanged)

- Worktree agent leaks to main: `git checkout -- .`, remove untracked, then re-merge.
- 3-way merge for worker/scheduler/querier/routes/server: every backend sprint adds entries; resolve additively.
- Test-name collisions: prefix per-feature.
- sqlc CLI broken (compliance.sql lexer); hand-write `*_ext.sql.go` / `*_manual.go`.
- ArgoCD self-manage reconciles helm values back to `:dev`. Deploy via re-tag locally + `kubectl rollout restart`.
- CWD discipline: worktree agents stay in their own dir; merge work happens from primary.
- Frontend leak guard: agents working on `frontend/src/lib/api/cluster-detail.ts` produce massive HEAD-vs-worktree blobs; scan for missing `listMirrored*` / `Mirrored*` imports after merge before docker building.
- Frontend axios baseURL is `/api/v1` — API client paths start at `/admin/...` or `/clusters/...`, NEVER `/api/v1/`.
- Kubectl shell + image scans require a remote agent. UI is gated on `is_local`.

## Memory index

- `user_role.md`, `project_live_env.md`, `project_astronomer_go_independence.md`, `feedback_frontend_image_name.md`
