# astronomer-go sprint progress snapshot

_Snapshot updated 2026-05-12 16:08. Branch: `main` @ `92e8d0a`. Live: `.247` running this revision._

## Cumulative shipped through sprint 16 (DONE)

Migration head: **61**. Live deploy URL: `http://astronomer.5.78.101.247.nip.io:8080/`.

### Sprint 15 (merged earlier this session)
- **055 SIEM forwarder** — syslog UDP/TCP/TLS + Splunk HEC + NDJSON HTTPS; RFC 5424 / RFC 3164 / CEF / NDJSON; bus tap → bounded per-forwarder queue → dispatcher every 2s
- **056 Fleet operations** — coordinated multi-cluster ops with label-selector targeting, sequencing, max_concurrent, abort-on-error policy
- **057 Maintenance windows** — cron-based blackout/permitted windows; gate wired into 6 destructive mutation paths; on_block=refuse/defer with replayer registry

### Sprint 16 (merged + deployed this session)
- **058 Dashboard widgets** — Grafana iframe + PromQL → server-rendered SVG sparkline/stat + URL iframe; per-cluster_uid templating; allow-list-host validation
- **059 Notification template editor** — 18 templates centralized in registry; snapshot tests proving default output byte-identical to current code; override-or-default Resolve
- **060 GitOps cluster registration** — `kind: ClusterRegistration` YAML in Git → go-git/v5 poll → upsert clusters; on_delete log/tombstone/decommission with 24h grace
- **061 BYO helm catalogs per project** — catalogs scope = globals ∪ project-owned ∪ subscribed; admin view unchanged; project admins manage their own

Live `.247` verification done:
- `/health/` reports `version=92e8d0a`
- Migration `SELECT MAX(version) FROM schema_migrations` = 61
- All 6 new admin surfaces hit 200 with admin JWT
- Notification-template registry returns 18 entries

Deploy gotcha: **ArgoCD self-manage** reconciles helm values back to `tag: dev` (the Argo Application has them hard-coded). So `helm upgrade --set image.*.tag=...` is overwritten within seconds. Workaround used: re-tag the new images as `:dev`, k3d-import, then `kubectl rollout restart`. Long-term fix is to update the Argo Application's helm values (or move the tag into a chart-level default).

## Pending (sprint 17 — agents to be launched)

User said keep going + comprehensive testing. Picking 4 more from the Rancher parity gap, all S/M effort, suitable for parallel agents:

| # | Feature | Why | Effort | Migration |
|---|---|---|---|---|
| 1 | **Image vulnerability scanning** (Trivy via in-cluster operator + result aggregation) | We have CIS scans (security_scans, 037) but no container-image scans. SOC2/PCI auditors expect both. | M | 062 |
| 2 | **API read-audit** (selective GET-logging for sensitive resources + compliance retention) | We audit mutations but not reads. HIPAA/PCI ask for "who saw what cluster credential and when". | S | 063 |
| 3 | **Compliance baselines** (PCI / HIPAA / FedRAMP preset bundles: quota plan + PSS profile + audit retention + window template) | We have all the pieces (051/041/057/030); pre-bundled presets save operators hours of click-ops. | S | 064 |
| 4 | **In-browser kubectl shell** (terminal in cluster detail page via the existing tunnel exec channel) | We have exec channel from sprint 14 (`tunnel.NewExecConsumer`). UI surface is the missing piece. Massive operator-UX win, no new infra. | M | 065 |

After sprint 17: deploy + verify live.

## Cumulative roadmap remaining (post-17)

Already noted, not yet scheduled:
- Cluster groups / folders (org structure for 50+ clusters)
- Vault integration for helm values (operator: AppRole → Helm install templates)
- More CRD-mirror resources (Webhook / Template / Quota)
- Helm upgrade automation (already partial via cluster_templates 049)
- Cost allocation per project (L effort, AWS Cost Explorer / GCP billing API plumbing)
- Bulk cluster operations (effectively delivered by fleet ops 056; UI surface could be enriched)

User declined: licensing management.

## Process notes (carry forward)

- **Agent leaks to main**: agents in worktrees occasionally write into the primary repo working tree. The recovery: `git checkout -- .` + remove untracked files that match the agent's intended output. Their commits are still safely on `worktree-agent-<id>`.
- **3-way merge for worker/scheduler/querier**: every backend sprint adds entries; resolve additively, sorting by migration #.
- **Test-name collisions**: avoid `TestHandler_RequiresSuperuser` — too generic. Sprint 16 had three agents each pick that name; sprint 17 prompts will tell each agent to prefix with the feature.
- **sqlc CLI broken** (compliance.sql lexer); hand-write `*_ext.sql.go` / `*_manual.go` files following the established pattern. Don't run `make sqlc-generate`.
- **CWD discipline**: worktree agents stay in their own worktree directory; merge work happens from `/root/code/astronomer-all/astronomer-go`.

## Memory index (unchanged)

- `user_role.md`
- `project_live_env.md`
- `project_astronomer_go_independence.md`
- `feedback_frontend_image_name.md`
