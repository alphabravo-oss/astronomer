# MCP tool quick reference (Astronomer 0.3.5)

All tools are **management-plane only**. Prefer exact names below.

Full catalog lives in product code (`internal/charlie/catalog.go`). This page is
the Charlie RAG card for the **0.3.5 test run**.

---

## First questions

| User ask | Tool | Notes |
| --- | --- | --- |
| K8s / Astronomer version | `astronomer.installation.summary` | Includes `kubernetes_version`, chart/app versions |
| Is the install healthy? | `astronomer.installation.readiness` then pods/workloads | Boolean readiness + counts |
| Which pods? CrashLoop? | `astronomer.management.pods` | Optional `component`, `phase` |
| Workload list | `astronomer.management.workloads` | Deployments/StatefulSets |
| One workload | `astronomer.management.workload_get` | `workload=deployment\|statefulset/<name>` |
| Rollout stuck? | `astronomer.management.rollout_status` | Same `workload` arg |
| Logs | `astronomer.management.pod_logs` | Needs `pod` + `container` from pods first; inspect `truncated` and requested/returned line counts |
| Events | `astronomer.management.events` | `component`, `since`, `limit` |
| Nodes | `astronomer.management.nodes` | Management-plane nodes only |
| Storage / network | `astronomer.management.storage` / `.network` | Owned PVCs / Services / NP |
| Runtime objects | `astronomer.management.jobs` / `.job_get` / `.daemonsets` / `.availability` / `.ingress` | Owned management objects only; ingress reports partial EndpointSlice visibility explicitly |
| Resource usage | `astronomer.management.resource_usage` | Requests/limits/restarts plus live metrics when available |
| DB / migrations | `astronomer.database.health` / `.performance` / `astronomer.migrations.status` | Fixed SQL projections only |
| Redis / server | `astronomer.redis.health` / `astronomer.runtime.http_health` / `.process_health` | No keys, paths, headers, or bodies |
| Key rotation | `astronomer.security.key_status` | Loaded-key counts and dev-sentinel names only; no key material |
| Queue overview | `astronomer.queue.health` | Counts by queue and state |
| Queued work | `astronomer.queue.tasks` then `.task_get` | Inspect pending/active/scheduled/retry/archived work without payload values |
| Failed work | `astronomer.queue.failed_tasks` then `.task_get` | Includes purpose, timing, retry state, and a sanitized failure category |
| Catalog sync | `astronomer.catalog.repositories` | Repository host, enabled/auth/sync state, last attempt/success, and sanitized failure category |
| Durable delivery | `astronomer.task_outbox.summary` then `.list` / `.get` | Correlates DB delivery rows to queue task IDs without payload values |
| Scheduler/controllers | `astronomer.scheduler.health` / `astronomer.controllers.summary` / `.alerts` | Finds stuck durable work and controller-wide patterns |
| Controller operations | `astronomer.catalog|argocd|tools|monitoring|logging|workloads.operations` then `.operation_get` | Sanitized lifecycle/event timelines |
| Argo self-mgmt | `astronomer.argocd.self_management_status` | |
| Fleet agents | `astronomer.agent_fleet.*` | Metadata only |
| Tunnel | `astronomer.tunnel.health` / `.recent_errors` / `.replica_distribution` | |
| Alerts / audit | `astronomer.alert.*` / `astronomer.audit.recent_changes` / `.search` | Search is exact-filtered and paginated |
| TLS / backups / obs | `astronomer.tls.status` / `.backups.status` / `.observability.health` | Obs uses fixed `query_template` enum |

## Administrative coverage

Start with `astronomer.platform.inventory` when the affected subsystem is not
known. It returns counts only; then use the domain-specific tool.

| Domain | Tool |
| --- | --- |
| Email, webhook, SIEM, notification delivery | `astronomer.delivery.summary` or `.email_health`, `.webhook_health`, `.siem_health` |
| Logging and monitoring configuration | `astronomer.logging.health`, `astronomer.monitoring.health` |
| Identity, Dex, SSO, SCIM | `astronomer.identity.health` |
| Authentication sessions, TOTP, recovery | `astronomer.authentication.health` |
| RBAC graph integrity | `astronomer.rbac.health` |
| Credential/TLS/account/compliance posture | `astronomer.security.posture` |
| Cloud credential materialization and Vault | `astronomer.external_integrations.health` |
| Maintenance, quotas, compliance, read audit | `astronomer.governance.health` |
| Controller threshold/silence policy | `astronomer.policy_engine.health` |
| Template/baseline reconciliation | `astronomer.templates.health` |
| Platform setting overrides | `astronomer.configuration.overview` |
| Users/projects/registered-cluster metadata/quotas | `astronomer.tenancy.summary` |
| Registration/decommission/agent lifecycle | `astronomer.registration.health` |
| Fleet-operation orchestration records | `astronomer.fleet_operations.health` |
| GitOps registration | `astronomer.gitops.health` |
| UI extensions | `astronomer.extensions.health` |
| Alert rules/channels/events | `astronomer.alerting.health` |
| Catalog ingestion/hydration/inventory | `astronomer.catalog.health` |
| Repair/idempotency bookkeeping | `astronomer.reconciliation.health` |
| Dashboard widgets/data sources | `astronomer.dashboard.health` |
| Product-local Charlie lifecycle | `astronomer.charlie.runtime_health` |

Administrative tools return aggregate state and safe enums. They deliberately
withhold identities, free-form configuration, policy documents, message
content, endpoint paths, credentials, and raw errors. Cross-domain tools require
wildcard read RBAC or superuser authority.

---

## Writes (mode-gated)

| Tool | Auto-eligible? | Typical mode |
| --- | --- | --- |
| `astronomer.management.workload_restart` | No | approval |
| `astronomer.management.workload_rollout` | No | approval |
| `astronomer.management.workload_scale` | No | approval (replicas 2–20) |
| `astronomer.management.run_job` | No | approval (allowlisted jobs only) |
| `astronomer.tunnel.restart_component` | No | approval |
| `astronomer.argocd.self_management_sync` | No | approval |
| `astronomer.queue.retry_task` | **Yes** (if allowlisted) | auto or approval |
| `astronomer.task_outbox.retry_delivery` | No | approval |

Write args:

| Field | How to fill |
| --- | --- |
| `resource_id` | Session-scoped id from product context `resource_ids`. Default install-wide chat scope is **`local`**. Never ask the user for this. |
| `workload` / `replicas` / `component` / … | From the user's natural language + prior read tools (e.g. `deployment/astronomer-worker`). |
| `operation_id` | **Generate** any fresh opaque correlator (UUID is fine). Product replaces it with the trusted action id. Never ask the user. |

Users never name tools. Match natural language to tool **descriptions** in the disclosed MCP catalog.

---

## Tool use rules

1. **Read first** — gather evidence before any write proposal.
2. **Owned only** — refuse resources not owned by the Astronomer release.
3. **Redaction** — never echo secrets that appear in logs/events.
4. **No downstream** — fleet tools are DB/telemetry only.
5. **Mode honesty** — if mode is `read_only`, stop at diagnosis + operator steps.
6. **Natural language** — pick tools from the disclosed catalog; do not require the operator to know tool names.
7. **Queue diagnosis** — use `queue.tasks` to identify the affected task, then
   `queue.task_get` for safe detail. For `catalog:sync`, correlate with
   `catalog.repositories`; do not infer that it is Charlie-specific.
8. **Durable delivery diagnosis** — use `task_outbox` before the queue when work
   may never have reached Redis. Retry delivery only for a proven `failed` or
   `dead` row and only after exact approval.
9. **Inventory then detail** — use `platform.inventory` only to choose a
   specific administrative diagnostic; do not treat a count as a diagnosis.
