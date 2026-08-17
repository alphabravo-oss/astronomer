# astronomer-go sprint progress snapshot

_Updated 2026-05-12 ~21:08. Branch: `main` @ `f751540`. Live `.247` at migration head **077**._

## Cumulative shipped

URL: `http://astronomer.5.78.101.247.nip.io:8080/` · admin / `<see astronomer-bootstrap Secret>`

> Retrieve the bootstrap admin password from the cluster Secret (never commit it here):
> `kubectl -n astronomer get secret astronomer-bootstrap -o jsonpath='{.data.password}' | base64 -d`

| Sprint | Headline features | Migrations | Status |
|---|---|---|---|
| 15 | SIEM forwarder · Fleet ops · Maintenance windows | 055–057 | live |
| 16 | Dashboard widgets · Notification templates · GitOps registration · BYO catalogs | 058–061 | live |
| 17 | Image scans · Read audit · Compliance baselines · Kubectl shell + wterm | 062–065 | live |
| 18 | Cluster groups · Vault · NetPol templates · CRD-mirror v2 | 066–069 | live |
| 19 | Apiserver allow-list · Service mesh · Anomaly detection · Catalog ratings | 070–073 | live |
| 20 | Platform-baseline auto-attach · kubectl image preload · empty-state CTAs | 074 | live |
| 21 | Seeded helm repos (bitnami/aqua/jetstack/prometheus-community) · first-boot sync · coverage endpoint · slug fix | 075–077 | live |
| 22 | Rancher-style 3-page registration wizard · phase machine · SSE event stream · tombstone filter on cluster list | 078 | live |

## Recent live fixes

- **Tombstoned-cluster ghosts** — `ListClusters` etc. now filter `WHERE decommissioned_at IS NULL`. Deletes actually clear from the UI once the worker tombstones.
- **Registration wizard live** — multi-page flow replaces the old modal; SSE drives the progress timeline.
- **TypeScript build errors** in the wizard fixed: `LiveEventType` union extended with the two registration event types; `ClusterRegistration` interface gained `distribution / region / install_baseline`.

## Pending (sprint 23 — polish + close known gaps)

Punch-list items from prior sprints that are user-visible:

1. **Cluster detail "Provisioning" tab** — sprint 22 deferred this. The wizard page-3 timeline exists; we need the same component reused on cluster detail so an operator returning to a half-provisioned cluster sees the same view.
2. **Catalog install page accepts `?cluster_id=`** — sprint 074 CTAs deep-link to `/catalog?search=trivy` as a stopgap. With a `cluster_id` param, the install form scopes to that cluster.
3. **Dedicated Image Scans tab on cluster detail** — sprint 074 landed the CTA on the CIS-scans tab as nearest-fit; it deserves its own tab.
4. **Empty-state CTAs across remaining cluster-detail tabs** — Tools (✓ done sprint 074), but Metrics, Logs, Workloads, Network Policies, Service Mesh still look dead on fresh clusters.
5. **Baseline reconciler check**: live test registered cluster `e81c58ee-...` and the `cluster_template_applications` row stayed `pending` because the worker short-circuits when the agent isn't actually healthy. Worth a small diagnostic + retry-with-backoff improvement so operators see the failure reason, not just an indefinitely-pending row.

## Tech-debt punch list (carry forward)

- **060** Fernet-encrypt `gitops_registration_sources.auth_encrypted`.
- **062** Wire Trivy `Ingester.AuditHook` to a CRD-mirror event dispatcher.
- **064** Webhook + SMTP delete enforcement when listed in active baseline `required_*`.
- **065** Audit fan-out for kubectl session `expired`/`reaped`; register active-sessions gauge.
- **066** Fleet selector `group_id` evaluator branch (schema + field shipped; orchestrator still labels-only). Sidebar hierarchical tree.
- **067** `vault.reference.resolved` / `.failed` audit rows (metrics-only today).
- **068** Audit `drift_detected` / `reconciled` actions for NetworkPolicy reconciler.
- **069** `astronomer_crd_mirror_rows` gauge populator; CustomResourceDefinition self-mirror.
- **070** AKS / DOKS / SelfManaged real provider drivers (EKS + GKE done; rest are detect-only).
- **074** `kind=builtin` enforcement on cluster_templates handler (seed marks `spec.builtin=true` for future).
- **078** Cluster-detail Provisioning tab; signed manifest URL with short TTL; per-tool install step rows in registration timeline; baseline lookup at startup wiring (`SetBaselineTemplateID`).

## Cumulative roadmap remaining

Larger / not-yet-scoped:

- Cost allocation per project (L effort, billing-API plumbing)
- Anomaly v2: cross-cluster baselines
- Per-namespace fine-grained RBAC mirror for kubectl shell
- Bulk cluster operations richer UI (fleet-ops backend is rich; UI sparse)
- Application marketplace recommendation engine v2 (cross-tenant, opt-in telemetry)
- IP geolocation badges on audit log
- Helm `--atomic --wait` toggle per project

User declined: licensing management.

## Process notes (unchanged)

- Worktree agent leaks to main: `git checkout -- .`, remove untracked, then re-merge.
- 3-way merge for worker/scheduler/querier/routes/server: every backend sprint adds entries; resolve additively.
- Test-name collisions: prefix per-feature.
- sqlc CLI broken (compliance.sql lexer); hand-write `*_ext.sql.go` / `*_manual.go`.
- ArgoCD self-manage reconciles helm values back to `:dev`. Deploy via re-tag locally + `kubectl rollout restart`.
- Frontend axios baseURL is `/api/v1` — API client paths start at `/admin/...` or `/clusters/...`, NEVER `/api/v1/`.
- Kubectl shell + image scans require a remote agent. UI is gated on `is_local`.

## Memory index

- `user_role.md`, `project_live_env.md`, `project_astronomer_go_independence.md`, `feedback_frontend_image_name.md`
