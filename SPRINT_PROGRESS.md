# astronomer-go sprint progress snapshot

_Updated 2026-05-12 ~19:35. Branch: `main` @ `c8140f4`. Live `.247` at migration head **074**._

## Cumulative shipped

URL: `http://astronomer.5.78.101.247.nip.io:8080/` · admin / `j3mMt0GJVtkQ3fYltBgV`

### Sprint 15 (merged) — 055 SIEM forwarder · 056 Fleet operations · 057 Maintenance windows
### Sprint 16 (merged) — 058 Dashboard widgets · 059 Notification templates · 060 GitOps registration · 061 BYO catalogs
### Sprint 17 (merged) — 062 Image vuln scans · 063 Read audit · 064 Compliance baselines · 065 Kubectl shell · wterm swap
### Sprint 18 (merged) — 066 Cluster groups · 067 Vault · 068 NetworkPolicy templates · 069 CRD-mirror v2
### Sprint 19 (070 merged; 071–073 pending) — 070 Apiserver allow-list
### Sprint 20 (merged) — 074 Platform baseline auto-attach + kubectl image preload + empty-state CTAs

## What's PENDING on worktree branches (not yet merged to main)

| Sprint 19 worktree | Migration | Branch |
|---|---|---|
| Service mesh detect tile | 071 | `worktree-agent-ac6f9e3c054e86270` |
| Anomaly detection on alerting | 072 | `worktree-agent-ac65dc923f75ed66f` |
| Catalog ratings + recommendations | 073 (agent used 055 locally; needs rename to 073 before merge) | `worktree-agent-aaf5e65b6644bfbb0` |

Each shipped as an isolated worktree on an older base commit. Standard merge pattern:
1. `git merge worktree-agent-<id> --no-ff -m "merge: sprint 19 — <feature> (migration NNN)"`
2. Resolve worker/scheduler/querier/routes/server additively (same as every prior merge).
3. Rename migration files inside the worktree before merging if they collide (catalog-ratings is the one that needs this).

## Recent live ops + fixes (this session)

- Dashboard layout rework: full-width clusters table + Recent Activity + Platform Health rail + Quick Actions row.
- `datasource_not_found: default` 404 fix → demo widgets silently dropped when their datasource is missing.
- `/api/v1/api/v1/dashboards/global/` doubled-prefix 404 → `dashboards.ts` paths normalized.
- Kubectl shell ImagePullBackOff in k3d → pre-loading `bitnami/kubectl:1.31.4` via `k3d-bootstrap.sh` + `images.txt` (sprint 20).
- UI guards: `is_local=true` clusters now hide Shell + Image Scans tabs with helpful empty-state pages.
- Architectural answer: cluster registration now auto-attaches a `Platform baseline` cluster_template (sprint 20). Operators can change or disable it via `/admin/platform-settings/default-cluster-template/`.

## Tech-debt punch list (carry forward, not yet scheduled)

- **060**: Fernet-encrypt `gitops_registration_sources.auth_encrypted` at write time.
- **062**: Wire Trivy `Ingester.AuditHook` once a CRD-mirror event dispatcher exists.
- **064**: Webhook + SMTP delete enforcement when listed in an active baseline's `required_*` field.
- **065**: Audit-log fan-out for kubectl session `expired`/`reaped` worker events. Register `astronomer_kubectl_sessions_active` gauge.
- **066**: Fleet selector `group_id` branch — schema + API field exist, but the orchestrator's selector evaluator doesn't expand group_id yet.
- **066**: Sidebar hierarchical tree + cluster-detail "Move to group" + breadcrumb (admin CRUD shipped).
- **067**: `vault.reference.resolved` / `.failed` audit-log rows (metrics-only today).
- **068**: Audit `drift_detected` / `reconciled` actions for NetworkPolicy reconciler.
- **069**: `astronomer_crd_mirror_rows` gauge populator; `CustomResourceDefinition` self-mirror.
- **070**: AKS / DOKS / SelfManaged real provider drivers (EKS + GKE done; rest are detect-only scaffolds).
- **074**: Catalog chart-install page accepting `?cluster_id=` so CTAs deep-link into a pre-targeted install; today the Trivy CTA falls back to `/dashboard/catalog?search=trivy`.
- **074**: Dedicated "Image Scans" frontend tab — CTA currently lives on the CIS-scans tab.
- **074**: `kind=builtin` enforcement on cluster_templates handler (seeded row has `spec.builtin=true` flag for future use).

## Cumulative roadmap remaining

Larger / not-yet-scoped:

- Cost allocation per project (L effort, AWS Cost Explorer / GCP billing API plumbing)
- Anomaly v2: cross-cluster baselines ("this cluster's CPU is 4σ above fleet mean")
- Per-namespace fine-grained RBAC mirror for kubectl shell (sprint 065 v1 is cluster-wide)
- Bulk cluster operations richer UI (fleet ops 056 backend is rich; UI sparse)
- Application marketplace recommendation engine v2 (cross-tenant, requires telemetry opt-in)
- IP geolocation badges on audit log
- Helm `--atomic --wait` toggle per project

User declined: licensing management.

## Process notes (unchanged)

- Agent leaks to main: `git checkout -- .` + remove untracked, then re-merge the worktree branch.
- 3-way merge for worker/scheduler/querier/routes/server: every backend sprint adds entries; resolve additively.
- Test-name collisions: prefix per-feature.
- sqlc CLI broken (compliance.sql lexer); hand-write `*_ext.sql.go` / `*_manual.go`.
- ArgoCD self-manage reconciles helm values back to `:dev`. Deploy via re-tag locally + `kubectl rollout restart`.
- CWD discipline: worktree agents stay in their own dir; merge work happens from primary.
- **Frontend leak**: agents working on `frontend/src/lib/api/cluster-detail.ts` produce massive HEAD-vs-worktree blobs; scan for missing `listMirrored*` / `Mirrored*` imports after the merge before docker building.
- **Frontend axios baseURL** is `/api/v1`. API client modules pass paths starting at `/admin/...` or `/clusters/...` — NEVER prefix `/api/v1/`.
- **kubectl shell + image scans require a remote agent**. The local-agent inside the server pod doesn't reliably support these flows. UI is now gated on `is_local`.

## Memory index (unchanged)

- `user_role.md`, `project_live_env.md`, `project_astronomer_go_independence.md`, `feedback_frontend_image_name.md`
