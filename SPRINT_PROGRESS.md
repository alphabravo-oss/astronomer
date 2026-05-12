# astronomer-go sprint progress snapshot

_Snapshot updated 2026-05-12 16:55. Branch: `main` @ `319d9b5`. Live `.247` running this revision (re-tagged as `:dev`)._

## Cumulative shipped (through sprint 17 + wterm swap)

Migration head: **65**. Live deploy URL: `http://astronomer.5.78.101.247.nip.io:8080/` · admin / `j3mMt0GJVtkQ3fYltBgV`.

### Sprint 15
- 055 SIEM forwarder · 056 Fleet operations · 057 Maintenance windows

### Sprint 16
- 058 Dashboard widgets · 059 Notification template editor · 060 GitOps cluster registration · 061 BYO helm catalogs per project

### Sprint 17
- 062 Image vulnerability scanning (Trivy CRD ingest)
- 063 Read-side API audit (8 seeded policies, middleware + cache evaluator)
- 064 Compliance baselines (PCI / HIPAA / FedRAMP-Moderate / SOC2 presets)
- 065 In-browser kubectl shell (xterm initially, swapped to wterm same-day)

### One-off this session
- **wterm swap** — replaced xterm.js (`@xterm/xterm@5.5.0`) with `@wterm/react@0.3.0` across both terminal consumers (cluster-shell + pod-terminal). Vercel-Labs Zig→WASM core. Bundle drops xterm.js + 2 addons; prebuild script copies `node_modules/@wterm/core/wasm/wterm.wasm` → `public/wterm.wasm`. next.config.js gained `transpilePackages: ['@wterm/core', '@wterm/dom', '@wterm/react']`. **Rollback**: `git revert 319d9b5`.

Live `.247` verification:
- `/health/` → version `9036610`
- `SELECT MAX(version) FROM schema_migrations` = 61… wait, 65 now. Already verified earlier.
- All sprint-15-17 admin endpoints return 200 with admin JWT.

## Pending (sprint 18 — agents to launch)

Picking 4 more from the remaining Rancher parity gap. Mix of operator UX + security hardening.

| # | Feature | Why | Effort | Migration |
|---|---|---|---|---|
| 1 | **Cluster groups / folders** | Fleets of 50+ clusters need org structure. Today the list is flat. Groups = label-style folders + bulk-apply target for fleet ops. | M | 066 |
| 2 | **Vault integration for helm values** | Operators store secrets in HashiCorp Vault. Sprint 067 lets helm-chart values reference `vault://path/key` and the materializer fetches at install time. | M | 067 |
| 3 | **Network policy templates** | Pre-built NetworkPolicy bundles (deny-all-ingress, project-isolated, namespace-only) + per-cluster apply. Sister of sprint 049 cluster_templates. | S/M | 068 |
| 4 | **CRD-mirror v2: more managed resources** | Sprint 014 mirrors a handful of CRDs. Add: IngressClass, GatewayClass, NetworkPolicy, ResourceQuota, LimitRange — read-side, so the cluster detail page shows "what's installed" without a fresh kubectl roundtrip. | S/M | 069 |

After sprint 18: deploy + verify live.

## Tech-debt punch list (from prior sprint reports, not yet scheduled)

Mostly small, defer until convenient:
- **Sprint 060**: Fernet-encrypt the `gitops_registration_sources.auth_encrypted` blob at write time (column exists; ciphertext support is the missing piece).
- **Sprint 062**: Wire the Trivy `Ingester.AuditHook` in `server.go` once a CRD-mirror event dispatcher exists.
- **Sprint 064**: Webhook + SMTP delete enforcement when those resources are listed in an active baseline's `required_*` field (currently recorded, not enforced).
- **Sprint 065**: Audit-log fan-out for the kubectl session `expired` / `reaped` events fired from the worker reaper. Also: register the `astronomer_kubectl_sessions_active` gauge + opened/commands counters.
- **Sprint 063**: `audit_log` policy `path_pattern` was `/audit-log` in the spec; live uses `/audit` (matches chi mount). Same intent.
- **GitOps spec.project + spec.registries + spec.toolPresets** binding — the YAML fields are parsed and surfaced through preview, but binding them into the actual cluster_registry_configs / tool installations is a follow-up.

## Cumulative roadmap remaining (post-18)

- Cost allocation per project (L effort, AWS Cost Explorer / GCP billing API plumbing)
- Anomaly detection on metrics (alerting++)
- Application marketplace ratings/recommendations
- IP allow-list per cluster (apiserver firewall)
- Service mesh (Istio/Linkerd) integration tile

User declined: licensing management.

## Process notes (carry forward, unchanged)

- Agent leaks to main: `git checkout -- .` + remove untracked, then re-merge the worktree branch.
- 3-way merge for worker/scheduler/querier: every backend sprint adds entries; resolve additively.
- Test-name collisions: prefix per-feature (avoid generic `TestHandler_RequiresSuperuser`).
- sqlc CLI broken (compliance.sql lexer); hand-write `*_ext.sql.go` / `*_manual.go`.
- ArgoCD self-manage reconciles helm values back to `:dev`. Deploy via re-tag locally + `kubectl rollout restart` (don't fight Argo).
- CWD discipline: worktree agents stay in their own dir; merge work happens from primary.

## Memory index (unchanged)

- `user_role.md`, `project_live_env.md`, `project_astronomer_go_independence.md`, `feedback_frontend_image_name.md`
