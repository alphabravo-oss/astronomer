# Astronomer Charlie knowledge pack 0.3.5

Generated 2026-08-11T15:58:37Z. Product version key: 0.3.5


---

<!-- source: playbooks/approval-remediation-loop.md -->

# Playbook: Approval-mode remediation loop

**Version:** 0.3.5 · **Maturity:** test-run

Use when authoritative mode is **approval** (or auto for allowlisted actions only).

## Rules

1. Gather evidence with **read** tools first (pods, logs, events, readiness).
2. Propose **exactly one** disclosed write with exact arguments from context
   (`workload`, `resource_id`, `operation_id`, `task_id`, …). Do not invent IDs.
3. A tool proposal is **not** an executed action. Wait for product approval UI.
4. After success, re-read with `rollout_status` / `pods` / `readiness` to verify.
5. If denied or mode is `read_only`, open a finding path / manual steps — never
   retry around denial with a different target.

## Common remediations

| Symptom | Candidate write | Verify with |
| --- | --- | --- |
| Stuck server Deployment | `workload_restart` or `workload_rollout` | `rollout_status`, `pods` |
| Need more workers | `workload_scale` (replicas 2–20) | `workload_get` |
| Argo out of sync | `argocd.self_management_sync` | `argocd.self_management_status` |
| Failed queue task | `queue.retry_task` | `queue.failed_tasks` |
| Tunnel component wedged | `tunnel.restart_component` | `tunnel.health` |

Prefix all tool names with `astronomer.` as in the catalog
(e.g. `astronomer.management.workload_restart`).

## Never

- Describe approval as already granted
- Switch to a more powerful tool after denial
- Restart non-mutable Deployments (only server/worker/frontend are mutable for restart/scale/rollout)
- Claim success without a successful tool result payload


---

<!-- source: playbooks/crashloop-management-pod.md -->

# Playbook: CrashLooping management-plane pod

**Version:** 0.3.5 · **Maturity:** test-run

## Goal

Identify which **Astronomer-owned** pod is failing and gather enough evidence for
a bounded restart proposal (`approval`) or manual operator steps (`read_only`).

## Tools (in order)

1. `astronomer.installation.readiness` — are components ready?
2. `astronomer.management.pods` — list owned pods; note phase, restarts, container states.
   - Optional filters: `component` (e.g. `server`, `worker`), `phase`
3. `astronomer.management.events` — recent Warning events for the component.
4. `astronomer.management.pod_logs` — redacted tail for the failing pod/container
   (get names from the pods tool first).
5. `astronomer.management.rollout_status` — if a Deployment is stuck mid-rollout
   (`workload=deployment/<name>`).
6. `astronomer.management.workload_get` — replica summary for the owner workload.

## Decision

| Mode | Action |
| --- | --- |
| `read_only` | Summarize root cause with redacted evidence; recommend operator actions. **Do not** claim restart. |
| `approval` | If owner is a **mutable** Deployment (`server` / `worker` / `frontend`) and evidence supports restart, propose `astronomer.management.workload_restart` with exact `workload`, `resource_id`, `operation_id` — wait for human approval. |
| `auto` | Only if that capability is centrally allowlisted and auto-eligible (restarts are typically **not**). |

## Stop conditions

- Pod is not owned by the Astronomer release → refuse (out of scope).
- Logs show credentials → keep redaction; never echo secrets.
- Downstream cluster symptom → do not tunnel into customer clusters; connection metadata tools only.

## Example operator prompts (test run)

- “Which pods are crashlooping?”
- “Server keeps restarting — gather logs and events.”
- “Is the server Deployment rollout stuck?”


---

<!-- source: playbooks/readiness-and-database.md -->

# Playbook: Installation not ready / database issues

**Version:** 0.3.5 · **Maturity:** test-run

## Goal

Separate “component not ready” from “schema/database” problems before proposing
restarts.

## Tools (in order)

1. `astronomer.installation.readiness`
2. `astronomer.installation.summary` — versions and component health
3. `astronomer.database.health`
4. `astronomer.migrations.status`
5. `astronomer.queue.health`, `astronomer.queue.tasks`, and
   `astronomer.queue.failed_tasks`
6. `astronomer.queue.task_get` for one task's safe retry/timing/failure detail
7. `astronomer.catalog.repositories` when the task type is `catalog:sync`
8. `astronomer.management.pods` / `astronomer.management.events` if a component is not ready
9. `astronomer.backups.status` if restore/drill related

## Decision

| Observation | Next step |
| --- | --- |
| Schema **dirty** or version behind expected | Stop recommending app restarts; escalate to migration/operator runbooks |
| Queue backlog or failed tasks | List the relevant state with `queue.tasks`, inspect one with `queue.task_get`, and use `failed_tasks` for the archived set; `queue.retry_task` is a **write** (approval/auto policy) |
| `catalog:sync` pending or failed | Correlate the task with `catalog.repositories`; it synchronizes Astronomer Helm catalog repositories and is separate from `charlie:trigger_dispatch` |
| Component unready with CrashLoop | Follow **crashloop-management-pod** playbook |
| DB not accepting connections | Report `database.health` evidence; operator fixes Postgres / DSN |

## Stop conditions

- Do not invent migration commands Charlie cannot run.
- Do not claim queue retry succeeded without a successful write tool result.
- Do not request or disclose raw queue payload values, raw failure strings,
  repository credentials, or secret-bearing repository URL components.


---

<!-- source: playbooks/tunnel-and-agent-flaps.md -->

# Playbook: Tunnel or fleet agent connection flaps

**Version:** 0.3.5 · **Maturity:** test-run

## Goal

Diagnose management-plane tunnel/hub issues and **fleet agent connectivity
metadata** without entering downstream clusters.

## Tools (in order)

1. `astronomer.tunnel.health`
2. `astronomer.tunnel.replica_distribution`
3. `astronomer.tunnel.recent_errors` (optional `connection_id`, `since`, `limit`)
4. `astronomer.agent_fleet.summary` then `list` / `get` for a specific `cluster_id`
5. `astronomer.agent_fleet.connection_history`
6. `astronomer.agent_fleet.ingestion_health` and `upgrade_status` if upgrades suspected

## Decision

| Mode | Action |
| --- | --- |
| `read_only` | Prefer diagnosis + findings only |
| `approval` | `astronomer.tunnel.restart_component` only with exact args after evidence |
| `auto` | Tunnel restart usually **not** allowlisted; fall back to approval/finding |

Never claim to have checked downstream pod state. Only connection/telemetry held
by Astronomer is in scope.

## Operator-only follow-ups (do not execute via Charlie)

- Downstream node network, firewall, agent logs on the customer cluster
- Credential reissue procedures outside the disclosed catalog


---

<!-- source: reference/mcp-tools.md -->

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


---

<!-- source: reference/modes-and-authority.md -->

# Modes and authority (Astronomer 0.3.5)

Authority is **not** chosen by the model. Product mode + disclosure + live RBAC
gate every tool call. Charlie chat sessions inherit the product’s authoritative
mode for that connection.

---

## Modes

| Mode | Product tools | Writes | Typical use in test run |
| --- | --- | --- | --- |
| `disabled` | Off | Off | Connection present but not usable for SRE chat |
| `read_only` | Reads yes | No | **Default for this test install** — diagnose freely |
| `approval` | Reads yes | One exact human-approved write | Remediations after disclosure |
| `auto` | Reads yes | Only auto-eligible + allowlisted | Narrow background remediations |

Raising mode requires Charlie admin configuration, disclosure acknowledged, and
not emergency-disabled. Astronomer UI may still show requested vs verified mode
drift — trust **verified** mode from status APIs.

---

## Disclosure

Operators must acknowledge the product safety disclosure digest before elevated
modes apply. Mismatch → tools stay restricted.

---

## Approval loop (when mode = approval)

1. Read tools gather evidence.
2. Model proposes **exactly one** write with exact arguments.
3. Human approves **once** in the product UI (proposal ≠ execution).
4. After success, re-read (`rollout_status` / `pods` / `readiness`) to verify.
5. Denial or `read_only` → finding / manual steps; never “shop” for a more powerful tool.

---

## Auto-eligible writes (catalog)

Only these writes are marked auto-eligible in product code; both still need
`mode=auto` **and** central allowlist:

- `astronomer.argocd.self_management_sync`
- `astronomer.queue.retry_task`

Restarts, scale, tunnel restart, and run_job are **not** auto-eligible.


---

<!-- source: scope.md -->

# Astronomer 0.3.5 Charlie SRE scope

**Product documentation version:** `0.3.5`
**Audience:** Charlie (RAG) and human operators on the management plane
**Maturity:** test-run

Charlie troubleshoots the **Astronomer management-plane installation** and the
Kubernetes resources that run Astronomer itself. It does **not** operate
downstream customer clusters.

Charlie (product agent) is the isolated bridge installed beside Astronomer.
**Astronomer cluster agents** are the existing agents installed in adopted
customer clusters. Charlie may inspect connection **metadata** about those
agents; it may not kubectl into those clusters.

---

## In scope

| Area | Examples |
| --- | --- |
| Install identity | Product/chart version, namespace, release, **kubernetes_version**, distribution |
| Readiness | Database, schema/migrations dirty flag, queues, component ready counts |
| Owned workloads | Deployments/StatefulSets/Pods with release ownership (install namespace) |
| Diagnostics | Bounded redacted pod logs, events, nodes, storage (PVC), network (Services/NP) |
| Data plane health | Postgres health, queue health/failed tasks, TLS status, backups summary |
| Self-management | Argo CD Application sync/health for the Astronomer install |
| Fleet (metadata only) | Agent list/summary, connection history, upgrade/ingestion status |
| Tunnel hub | Health, replica distribution, recent errors; restart only if mode allows |
| Bounded writes | Restart/scale/rollout mutable Deployments; Argo sync; queue retry; tunnel restart; allowlisted run jobs — all mode-gated |

## Out of scope

- Downstream kubectl: list/logs/exec/apply/delete in **customer** clusters
- Generic shell, raw SQL, free-form HTTP, Secret **values**
- Destructive catalog operations not published for this version
- Inventing tools, IDs, or “I already restarted it” without tool evidence

---

## Authority (intersection — all must pass)

1. **Product mode** on the Charlie connection (`disabled` | `read_only` | `approval` | `auto`)
2. **Disclosure** acknowledged (digest match)
3. **Live RBAC** of the human (or automation principal) in Astronomer
4. For writes: **approval** one-shot or **auto** allowlist (only auto-eligible capabilities)

| Mode | Reads | Writes |
| --- | --- | --- |
| `disabled` | No product tools | No |
| `read_only` | Yes | No — findings and guidance only |
| `approval` | Yes | One exact approved write at a time |
| `auto` | Yes | Only auto-eligible **and** centrally allowlisted actions |

When a write cannot run, create or cite an actionable finding with checks and a
proposed safe action — **never** invent that an action already ran.

---

## How product version is pinned

Astronomer asserts documentation version via `currentProductDocumentationVersion()`
(e.g. chart/app `0.3.5`). Git describe strings like `v0.3.5-23-g…` are stripped
to `0.3.5` before Charlie session retrieval. Knowledge releases must use the
same exact string as `product_version`.

