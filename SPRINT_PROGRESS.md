# astronomer-go sprint progress snapshot

_Snapshot taken 2026-05-12. Branch: `main` @ `e9fd1ea`._

## What just merged into main (sprint 15 — completed and merged)

All three landed atop `45606d1 feat(ui): catch frontend up with sprint 9-14 backend features`.

| # | Feature | Migration | Merge commit | Brief |
|---|---|---|---|---|
| 1 | External SIEM forwarder | 055 | (squashed in `392590c` chain) | Syslog UDP/TCP/TLS + Splunk HEC + NDJSON HTTPS; RFC 5424 / RFC 3164 / CEF / NDJSON formats; bus tap → bounded per-forwarder queue → dispatcher every 2s; daily 7d retention sweep; 31 tests; superuser-gated CRUD + `/test/` + `/status/`. |
| 2 | Fleet operations orchestrator | 056 | `392590c merge: sprint 15 — fleet operations orchestrator (migration 056)` | Coordinated multi-cluster operations with label-selector targeting, sequencing (sequential/parallel), max_concurrent cap, abort-on-error policy, pause/resume/abort, retry-failed; new RBAC resource `fleet_operations`; sub-op types: tool_upgrade/install/uninstall + apply_template; 17 tests. |
| 3 | Maintenance windows + deferred ops | 057 | `e9fd1ea merge: sprint 15 — maintenance windows (migration 057)` | Cron-based blackout/permitted windows with timezone + cluster selector + op_types; gate wired into 6 destructive mutation paths (cluster.delete, project.delete, tool.{install,upgrade,uninstall}, helm.{install,uninstall}, cluster_template.apply); on_block=refuse (409) or defer (202 + DispatchDeferred worker); 34 tests. |

Total migration count: **57 files** (= 114 .up + .down). `make check-migrations` clean.

Build state:
- `go build ./...` — clean
- `go vet ./...` — clean
- `go test -count=1 -short ./internal/siem/ ./internal/maintenance/ ./internal/worker/tasks/ ./internal/handler/` — passes

## What's pending (sprint 16 — agents PREPARED but NOT launched)

User asked to do these next. Agent prompts were drafted and ready to send (worktree-isolated, parallel). Tool-use was interrupted before launch — the prompts below are reproduced verbatim so they can be re-sent without redrafting.

Recommended migration numbering:
- 058 Custom dashboard widgets (Grafana embed)
- 059 Notification template editor
- 060 GitOps cluster registration
- 061 BYO helm catalog per project

After all four ship: deploy + test + validate on the live `.247` cluster.

### Sprint 16 agent prompt 1 — Custom dashboard widgets (058, M effort)

**Subagent**: general-purpose, isolated worktree.
**Description**: Custom dashboard widgets (Grafana embed)

Goal: operator pins widgets (Grafana panel iframe, raw URL iframe, PromQL → server-rendered SVG sparkline, PromQL → stat) to the global dashboard or per-cluster / per-project pages. Migrating real metrics next to the cluster they describe (Rancher's "monitoring overlay" parity).

Schema highlights:
- `dashboard_widgets` (id, name, widget_type, spec JSONB, scope=global|cluster|project, scope_ids[], grid x/y/w/h, refresh_seconds, enabled)
- `prometheus_datasources` (registry with Fernet-encrypted bearer + per-datasource TLS toggle)
- `ALTER TABLE clusters ADD COLUMN cluster_uid VARCHAR(64)` for Grafana templating; backfill = first 8 chars of UUID
- Constraints: widget_type ∈ {grafana_panel,prom_sparkline,prom_stat,url_iframe}; scope ∈ {global,cluster,project}

Server-side PromQL renderer (`internal/dashboards/prom.go`): `QueryRange`, `RenderSparkline` (minimal SVG, 200x60px, no deps), `EvalStat`. Cached for 30s on `(datasource_id,query,duration)`.

Public render endpoints (cluster:read scoped):
- `GET /api/v1/dashboards/global/`
- `GET /api/v1/dashboards/clusters/{id}/`
- `GET /api/v1/dashboards/projects/{id}/`
Response shape: per-widget `{id, name, widget_type, spec_resolved, grid, refresh_seconds, data:{sparkline_svg?,stat_value?,stat_unit?}}`.

Admin CRUD: `/api/v1/admin/dashboard-widgets/` + `/api/v1/admin/prometheus-datasources/` (+ `/test/`).

Frontend: widget grid component on global dashboard + cluster-detail + project-detail; admin form at `/dashboard/settings/widgets/`.

Constraints:
- Grafana iframe URL hosts validated against `dashboard_widgets_allowed_iframe_hosts` chart values (default empty).
- SVG rendered server-side trusted (operator-defined widgets); render via `dangerouslySetInnerHTML`.
- Promql injection: documented as effectively a Prom read-grant for widget admins.

Verification: check-migrations clean (58 files); build/vet/test clean (incl. -race). Seed three demo widgets (disabled) in migration.

Audit: `admin.dashboard_widget.{c,u,d}`, `admin.prometheus_datasource.{c,u,d}`.
Metrics: `astronomer_dashboard_widget_renders_total{type,outcome}`, `astronomer_dashboard_prom_cache_hit_total`.

### Sprint 16 agent prompt 2 — Notification template editor (059, S effort)

**Subagent**: general-purpose, isolated worktree.
**Description**: Notification template editor

Goal: pull every hardcoded email template (sprint 047 — 8 templates in `internal/email/templates.go`) and every webhook body template (sprint 048 — `internal/webhook/`) into a central registry; operators can override per-template from `/dashboard/settings/templates/`.

Schema:
- `notification_templates(id, template_key UNIQUE, channel ∈ {email,webhook}, subject_tpl, body_tpl, body_format ∈ {text,markdown,html,json}, enabled, updated_by, timestamps)`

Registry (`internal/notify/templates.go`):
- `TemplateDef{Key, Channel, Subject, Body, BodyFormat, Variables[]VariableSpec, Description}`
- `Registry() []TemplateDef` — exhaustive list (8 email + every webhook event)
- `Resolve(ctx, q, key)` — operator override if present+enabled else built-in. Subject + body resolved independently.

Wire `notify.Resolve` into:
- Email dispatcher (sprint 047 `internal/email/sender.go`)
- Webhook dispatcher (sprint 048 `internal/webhook/sender.go`)

Endpoints (superuser):
- `GET /api/v1/admin/notification-templates/` — list w/ `has_override` annotation
- `GET /api/v1/admin/notification-templates/{key}/` — merged + `default_subject` + `default_body` for diff
- `PUT /api/v1/admin/notification-templates/{key}/` — upsert
- `DELETE /api/v1/admin/notification-templates/{key}/` — reset to default
- `POST /api/v1/admin/notification-templates/{key}/preview/` — render against operator-supplied sample vars
- `GET /api/v1/admin/notification-templates/{key}/variables/`

Tests must include:
- Snapshot test per template proving default-output unchanged vs current constant (regression guard for the migration step).
- `TestRegistry_HasExpectedKeys`, `TestResolve_*`, `TestPreview_*`, `TestHandler_RequiresSuperuser`.

Frontend at `/dashboard/settings/templates/` — list + per-key detail page with split-view default vs override, sample-input JSON, Preview button, Save / Reset.

Constraint: text/template for webhook JSON + email-md (then md→html in transport); html/template for email-html bodies. PRESERVE existing dispatch behavior for any template without an override row.

Verification: 59 migration files, build/vet/test clean (incl. -race).

### Sprint 16 agent prompt 3 — GitOps cluster registration (060, M effort)

**Subagent**: general-purpose, isolated worktree.
**Description**: GitOps cluster registration

Goal: operator commits `clusters/<name>.yaml` (`apiVersion: astronomer.alphabravo.io/v1`, `kind: ClusterRegistration`, spec with labels/template/registries/toolPresets/project) to a tracked Git repo; astronomer polls, upserts clusters, and (under `on_delete` policy) tombstones+decommissions clusters whose YAML disappeared.

Schema:
- `gitops_registration_sources(id, name UNIQUE, repo_url, branch, path_prefix, auth_mode ∈ {none,https_token,ssh_key}, auth_encrypted, sync_mode ∈ {manual,interval}, sync_interval_seconds, on_delete ∈ {log,tombstone,decommission}, last_synced_at, last_synced_sha, last_error, enabled, …)`
- `gitops_registered_clusters(cluster_id PK FK→clusters, source_id FK, repo_path, last_yaml_sha, last_applied_at, status ∈ {active,tombstoned}, tombstoned_at, …)`

Worker (`internal/worker/tasks/gitops_sync.go`, `gitops:sync` task, 60s cadence):
1. For each enabled source past its sync_interval: clone or fetch via `go-git/v5` (NO shell-out), checkout branch, walk `path_prefix/*.{yaml,yml}` recursively.
2. Parse each file (`internal/gitops/parser.go`); reject non-`ClusterRegistration`.
3. Upsert cluster via the EXISTING cluster handler create flow (must refactor to expose a callable helper — not duplicate logic).
4. Compute missing-set; apply `on_delete` policy. tombstone reaper fires `cluster:decommission` after 24h grace.
5. Update last_synced_at / sha / clear last_error.

Endpoints (superuser):
- CRUD `/api/v1/admin/gitops-sources/`
- `POST /…/{id}/sync/` — manual trigger
- `GET /…/{id}/preview/` — dry-run (NO writes, NO enqueues)
- `GET /…/{id}/clusters/` — managed clusters + status

Tests: local bare repo in `t.TempDir()` (no network); covers parser validation, sync converge no-op, label update, all three on_delete policies, tombstone reap, restore-from-tombstone, manual mode, dry-run, superuser gate.

Constraints:
- `go-git/v5` only (`transport/http` BasicAuth for token-as-password; `transport/ssh` for key).
- Cache clone at `/tmp/gitops/<source_id>/`; idempotent on restart.
- Decommission mode warns at startup if `path_prefix` empty.
- DRY-RUN preview must not mutate DB / enqueue tasks.

Frontend at `/dashboard/settings/gitops/`.

Verification: 60 migration files, build/vet/test clean (incl. -race).

### Sprint 16 agent prompt 4 — BYO helm catalog per project (061, S effort)

**Subagent**: general-purpose, isolated worktree.
**Description**: BYO helm catalog per project

Goal: project admins subscribe their project to external Helm chart repos without admin involvement. Catalog browse becomes project-scoped (globals ∪ owned ∪ subscribed).

Schema:
- `ALTER TABLE helm_catalogs ADD COLUMN owner_project_id UUID REFERENCES projects(id) ON DELETE CASCADE` (NULL = public)
- `CREATE INDEX idx_helm_catalogs_owner_project ON helm_catalogs(owner_project_id) WHERE owner_project_id IS NOT NULL`
- `project_catalog_subscriptions(id, project_id FK CASCADE, catalog_id FK CASCADE, created_by, created_at, UNIQUE(project_id,catalog_id))`

Queries:
- `ListCatalogsForProject(projectID)` — UNION of globals + project-owned + subscribed
- `ListProjectSubscriptions`, `CreateProjectCatalogSubscription`, `DeleteProjectCatalogSubscription`
- `ListProjectOwnedCatalogs`, `CountSubscriptionsByCatalog`

Endpoints (project-admin gated):
- `GET /api/v1/projects/{id}/catalogs/`
- `POST /api/v1/projects/{id}/catalogs/` — create project-owned + auto-subscribe
- `POST /api/v1/projects/{id}/catalogs/{catalogID}/subscribe/` — public-only unless superuser
- `DELETE /api/v1/projects/{id}/catalogs/{catalogID}/` — project-owned ⇒ delete catalog (cascades); else delete subscription only
- `GET /api/v1/projects/{id}/catalogs/{catalogID}/charts/`

Admin endpoints unchanged + `?include_project_owned=true` toggle on `/api/v1/admin/catalogs/`.

Refactor catalog-browse to filter by `project_id` query param (existing field for install target). Sync task walks ALL rows including project-owned (no semantic change).

Tests cover: globals + owned + subscribed visibility; foreign-private hidden; superuser bypass; unsubscribe-global keeps catalog; unsubscribe-owned cascades; project delete cascades owned catalogs; project-admin gate.

Frontend: `/dashboard/projects/[id]/catalogs/` — list, "Subscribe to public catalog" modal, "Add private catalog" modal, per-catalog actions.

Verification: 61 migration files, build/vet/test clean (incl. -race).

## After sprint 16 — deploy + validate

User explicit request: "and deploy and test and validate" at the end of sprint 16.

Live deploy steps (same flow as sprint 14 redeploy):
1. From main with all 4 merged: `make docker-build-all IMG_TAG=sprint16`
2. `make k3d-import-all CLUSTER=astronomer-mgmt IMG_TAG=sprint16`
3. `make helm-install IMG_TAG=sprint16` (or `kubectl rollout restart` if values unchanged — sprint 14 incident learning)
4. Confirm migration head is 61: `kubectl -n astronomer exec astronomer-postgres-0 -- psql -U astronomer -c "SELECT version FROM schema_migrations"`
5. Hit `http://astronomer.5.78.101.247.nip.io:8080/` and exercise:
   - `/dashboard/settings/widgets/` — create a stat widget against the bundled Prom
   - `/dashboard/settings/templates/` — override the `email.alert_fired` body and preview
   - `/dashboard/settings/gitops/` — create a source pointed at a public read-only repo (e.g. github.com/anthropics/example-clusters) and do a dry-run /preview/
   - `/dashboard/projects/<id>/catalogs/` — add a private bitnami catalog, install a chart

Live URL + creds in memory: `project_live_env.md` (admin / `j3mMt0GJVtkQ3fYltBgV`).

## Process notes from sprint 15 (carry forward)

- **Worktree CWD drift**: Bash CWD persists between calls. After `cd …/worktrees/<agent-id>`, every subsequent Bash command runs from that worktree until a `cd` back to `/root/code/astronomer-all/astronomer-go`. Bit me once during sprint 15 — merge ran against the wrong HEAD before I noticed. Always re-`cd` to the primary repo before `git merge`.
- **Conflict pattern when merging multiple worker-adding sprints**: every sprint adds entries to `internal/worker/scheduler.go`, `internal/worker/worker.go`, and `internal/db/sqlc/querier.go`. Resolution is always additive (keep both sides, alphabetical or by migration #). Resolve by Edit, then `git add -A && git commit --no-edit`.
- **sqlc CLI is broken** in this repo (existing `compliance.sql` lexer issue). Recent sprints hand-write the sqlc.go files in the established `_models.go` / `_ext.sql.go` pattern (see `compliance_manual.go`, `quotas.sql.go`). Don't try to re-run `make sqlc-generate`.
- **Agents committing to wrong worktree**: a few sprints ago some agents committed directly to main rather than their isolated branch. Sprint 14+ uses `isolation: "worktree"` reliably. Check `git log worktree-agent-<id> --oneline main..HEAD` before merging — if commits aren't there, the agent likely landed them somewhere else.

## Memory index touchpoints

Already-saved memories that remain accurate:
- `user_role.md` — .247 Hetzner; archiving the legacy Python repo; astronomer-go is sole stack
- `project_live_env.md` — public URL, admin creds, k3d cluster name, Argo self-manage
- `project_astronomer_go_independence.md` — frontend now buildable from astronomer-go
- `feedback_frontend_image_name.md` — `astronomer-frontend` (no `-go-`)

No new memory needed for this snapshot; this file IS the snapshot. When sprint 16 is done, the relevant durable fact is "57+4 features shipped; migration head 61" — that goes in the live env memory if it changes URL/creds, otherwise nothing persists.
