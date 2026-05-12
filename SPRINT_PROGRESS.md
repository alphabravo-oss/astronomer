# astronomer-go sprint progress snapshot

_Snapshot updated 2026-05-12 18:06. Branch: `main` @ `33d0332`. Live `.247` running this revision (re-tagged as `:dev`)._

## Cumulative shipped (through sprint 18)

Migration head: **69**. Live URL: `http://astronomer.5.78.101.247.nip.io:8080/` · admin / `j3mMt0GJVtkQ3fYltBgV`.

### Sprint 15
- 055 SIEM forwarder · 056 Fleet operations · 057 Maintenance windows

### Sprint 16
- 058 Dashboard widgets · 059 Notification template editor · 060 GitOps cluster registration · 061 BYO helm catalogs per project

### Sprint 17
- 062 Image vulnerability scanning · 063 Read-side API audit · 064 Compliance baselines · 065 In-browser kubectl shell · **wterm swap**

### Sprint 18
- 066 Cluster groups / folders (tree-depth 3, fleet selector ext is a follow-up)
- 067 Vault integration (resolver in helm install / tools / cluster-templates)
- 068 Network policy templates (4 builtins + reconciler + drift sweep)
- 069 CRD-mirror v2 (IngressClass / GatewayClass / NetworkPolicy / ResourceQuota / LimitRange)
- **Live UI fix**: `frontend/src/lib/api/dashboards.ts` was prepending `/api/v1/` over the axios baseURL → 404. Stripped the prefix; all dashboard calls now route via the shared baseURL.
- **Merge follow-fix**: CRD-mirror v2 `listMirrored*` exports + types were dropped during the cluster-detail.ts merge (HEAD-only); re-appended from worktree before deploy.

Live verification done:
- `/health/` → version `369fb14`
- migrations head `SELECT MAX(version)` = 69
- `dashboards/global/` 200 (was 404 with doubled prefix)
- `cluster-groups/`, `admin/vault-connections/`, `admin/network-policy-templates/` all 200

## Pending (sprint 19 — agents to launch)

Picking the next 4 from the roadmap. Mix of security hardening + operator UX.

| # | Feature | Why | Effort | Migration |
|---|---|---|---|---|
| 1 | **Cluster apiserver allow-list** | Operators want a "lockbox" mode: only Astronomer's tunnel egress IPs can hit the cluster apiserver. Sprint 070 adds a per-cluster `apiserver_allowlist_cidrs` config + a reconciler that patches the cloud LB / firewall. | M | 070 |
| 2 | **Service mesh tile (Istio + Linkerd detect/install)** | We mirror lots of CRDs already (sprint 069); sprint 071 adds a dedicated tile that detects an installed mesh (gateway/peerauth/destinationrule shapes), and lets operators install via the catalog. | M | 071 |
| 3 | **Alerting anomaly detection** | Today alerts are static-threshold (`> 90% CPU`). Sprint 072 adds rolling-window baselines per `cluster × metric` and "deviation from baseline" alert rules. | M | 072 |
| 4 | **Catalog ratings + recommendations** | Sister of sprint 061 BYO catalogs. Operators rate installed charts; we surface "popular in your org" + "frequently installed together" recommendations on the catalog browse. | S/M | 073 |

After sprint 19: deploy + verify live.

## Tech-debt punch list (carry forward)

- **060**: Fernet-encrypt the `gitops_registration_sources.auth_encrypted` blob at write time.
- **062**: Wire the Trivy `Ingester.AuditHook` once a CRD-mirror event dispatcher exists.
- **064**: Webhook + SMTP delete enforcement when those resources are listed in an active baseline's `required_*` field.
- **065**: Audit-log fan-out for kubectl session `expired`/`reaped` worker events. Register `astronomer_kubectl_sessions_active` gauge + opened/commands counters.
- **066**: Fleet selector `group_id` branch — schema + API field exist, but `internal/worker/tasks/fleet_orchestrate.go` still uses main's selector evaluator without the group expansion.
- **066**: Sidebar hierarchical tree + cluster-detail "Move to group" + breadcrumb (admin CRUD already shipped).
- **067**: `vault.reference.resolved` / `.failed` audit-log rows (metrics only today).
- **068**: Audit `drift_detected` / `reconciled` actions for NetworkPolicy reconciler.
- **069**: `astronomer_crd_mirror_rows` gauge populator task; `CustomResourceDefinition` self-mirror.

## Cumulative roadmap remaining (post-19)

- Cost allocation per project (L effort, AWS Cost Explorer / GCP billing API plumbing)
- Application marketplace recommendation engine v2 (cross-tenant trends, requires telemetry opt-in)
- Per-namespace fine-grained RBAC mirror for kubectl shell (sprint 065 v1 was cluster-wide)
- Bulk cluster operations richer UI (fleet ops 056 backend is rich, UI is sparse)
- IP geolocation badges on audit log
- Service-account-token rotation reminder
- Helm `--atomic --wait` toggle per project

User declined: licensing management.

## Process notes (unchanged)

- Agent leaks to main: `git checkout -- .` + remove untracked, then re-merge the worktree branch.
- 3-way merge for worker/scheduler/querier/routes/server: every backend sprint adds entries; resolve additively.
- Test-name collisions: prefix per-feature.
- sqlc CLI broken (compliance.sql lexer); hand-write `*_ext.sql.go` / `*_manual.go`.
- ArgoCD self-manage reconciles helm values back to `:dev`. Deploy via re-tag locally + `kubectl rollout restart`.
- CWD discipline: worktree agents stay in their own dir; merge work happens from primary.
- **Frontend leak**: agents working on `frontend/src/lib/api/cluster-detail.ts` produce massive HEAD-vs-worktree blobs; remember to scan for missing `Mirrored*` / `listMirrored*` style imports after the merge before docker building. Symptom: Next.js build "export not found" with import trace pointing at the consumer page.
- **Frontend axios baseURL**: it's `/api/v1` (set in `lib/api.ts`). API client modules should pass paths starting at `/admin/...` or `/clusters/...` — NEVER prefix `/api/v1/`. The sprint-058 dashboards client had that bug; verify any new API client file against this rule before deploy.

## Memory index (unchanged)

- `user_role.md`, `project_live_env.md`, `project_astronomer_go_independence.md`, `feedback_frontend_image_name.md`
