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
| Logs | `astronomer.management.pod_logs` | Needs `pod` + `container` from pods first |
| Events | `astronomer.management.events` | `component`, `since`, `limit` |
| Nodes | `astronomer.management.nodes` | Management-plane nodes only |
| Storage / network | `astronomer.management.storage` / `.network` | Owned PVCs / Services / NP |
| DB / migrations | `astronomer.database.health` / `astronomer.migrations.status` | |
| Queue overview | `astronomer.queue.health` | Counts by queue and state |
| Queued work | `astronomer.queue.tasks` then `.task_get` | Inspect pending/active/scheduled/retry/archived work without payload values |
| Failed work | `astronomer.queue.failed_tasks` then `.task_get` | Includes purpose, timing, retry state, and a sanitized failure category |
| Catalog sync | `astronomer.catalog.repositories` | Repository host, enabled/auth/sync state, last attempt/success, and sanitized failure category |
| Argo self-mgmt | `astronomer.argocd.self_management_status` | |
| Fleet agents | `astronomer.agent_fleet.*` | Metadata only |
| Tunnel | `astronomer.tunnel.health` / `.recent_errors` / `.replica_distribution` | |
| Alerts / audit | `astronomer.alert.*` / `astronomer.audit.recent_changes` | |
| TLS / backups / obs | `astronomer.tls.status` / `.backups.status` / `.observability.health` | Obs uses fixed `query_template` enum |

---

## Writes (mode-gated)

| Tool | Auto-eligible? | Typical mode |
| --- | --- | --- |
| `astronomer.management.workload_restart` | No | approval |
| `astronomer.management.workload_rollout` | No | approval |
| `astronomer.management.workload_scale` | No | approval (replicas 2–20) |
| `astronomer.management.run_job` | No | approval (allowlisted jobs only) |
| `astronomer.tunnel.restart_component` | No | approval |
| `astronomer.argocd.self_management_sync` | **Yes** (if allowlisted) | auto or approval |
| `astronomer.queue.retry_task` | **Yes** (if allowlisted) | auto or approval |

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
