# Astronomer Charlie knowledge pack 0.3.5

Generated 2026-08-07T18:59:50Z. Product version key: 0.3.5


---

<!-- source: playbooks/approval-remediation-loop.md -->

# Playbook: Approval-mode remediation loop

Use when authoritative mode is **approval** (or auto for allowlisted actions).

## Rules

1. Gather evidence with **read** tools first (pods, logs, events, readiness).
2. Propose **exactly one** disclosed write with exact arguments from context
   (workload name, resource_id, operation_id). Do not invent IDs.
3. A tool proposal is **not** an executed action. Wait for product approval UI.
4. After success, re-read with `rollout_status` / `pods` / `readiness` to verify.
5. If denied or mode is read_only, open a finding path / manual steps—never retry
   around denial with a different target.

## Common remediations

| Symptom | Candidate write | Verify with |
| --- | --- | --- |
| Stuck server Deployment | `workload_restart` or `workload_rollout` | `rollout_status`, `pods` |
| Need more workers | `workload_scale` (replicas 2–20) | `workload_get` |
| Argo out of sync | `argocd.self_management_sync` | `argocd.self_management_status` |
| Failed queue task | `queue.retry_task` | `queue.failed_tasks` |
| Tunnel component wedged | `tunnel.restart_component` | `tunnel.health` |

## Never

- Describe approval as already granted
- Switch to a more powerful tool after denial
- Restart non-mutable Deployments (only server/worker/frontend)


---

<!-- source: playbooks/crashloop-management-pod.md -->

# Playbook: CrashLooping management-plane pod

## Goal

Identify which Astronomer-owned pod is failing and gather enough evidence for a
bounded restart proposal (approval mode) or manual operator steps (read_only).

## Tools (in order)

1. `astronomer.installation.readiness` — are components ready?
2. `astronomer.management.pods` — list owned pods; note phase, restarts, container states.
   - Filter with `component` (e.g. `server`, `worker`) or `phase=Failed` if useful.
3. `astronomer.management.events` — recent Warning events for the component.
4. `astronomer.management.pod_logs` — redacted tail for the failing pod/container
   (get names from the pods tool first).
5. `astronomer.management.rollout_status` — if a Deployment is stuck mid-rollout.
6. `astronomer.management.workload_get` — replica summary for the owner workload.

## Decision

- **read_only**: summarize root cause, paste redacted evidence, recommend operator
  actions. Do not claim restart.
- **approval**: if the owner is a mutable Deployment (`server`/`worker`/`frontend`)
  and evidence supports restart, propose `astronomer.management.workload_restart`
  with exact workload and resource_id—wait for human approval.
- **auto**: only if that capability is centrally allowlisted and auto-eligible
  (restarts are typically **not** auto-eligible).

## Stop conditions

- Pod is not owned by the Astronomer release → refuse (out of scope).
- Logs show credentials → keep redaction; never echo secrets.
- Downstream cluster symptom → do not tunnel; give operator-side checks only.


---

<!-- source: playbooks/readiness-and-database.md -->

# Playbook: Installation not ready / database issues

## Goal

Separate “component not ready” from “schema/database” problems.

## Tools

1. `astronomer.installation.readiness`
2. `astronomer.installation.summary` — versions and component health
3. `astronomer.database.health`
4. `astronomer.migrations.status`
5. `astronomer.queue.health` and `astronomer.queue.failed_tasks`
6. `astronomer.management.pods` / `events` if a specific component is not ready
7. `astronomer.backups.status` if restore/drill related

## Decision

- Schema **dirty** or version behind expected → stop recommending app restarts;
  escalate to migration/operator runbooks.
- Queue backlog of failed tasks → list with `failed_tasks`; `queue.retry_task` is
  a write (approval/auto policy).
- Component unready with CrashLoop → follow crashloop playbook.


---

<!-- source: playbooks/tunnel-and-agent-flaps.md -->

# Playbook: Tunnel or fleet agent connection flaps

## Goal

Diagnose management-plane tunnel/hub issues and **fleet agent connectivity
metadata** without entering downstream clusters.

## Tools

1. `astronomer.tunnel.health`
2. `astronomer.tunnel.replica_distribution`
3. `astronomer.tunnel.recent_errors` (optional `connection_id`, `since`, `limit`)
4. `astronomer.agent_fleet.summary` then `list` / `get` for a specific `cluster_id`
5. `astronomer.agent_fleet.connection_history`
6. `astronomer.agent_fleet.ingestion_health` and `upgrade_status` if upgrades suspected

## Decision

- Prefer diagnosis and findings in **read_only**.
- `astronomer.tunnel.restart_component` is a write: requires **approval** (or auto
  only if explicitly allowlisted—usually not).
- Never claim to have checked downstream pod state; only connection/telemetry
  held by Astronomer.

## Operator-only follow-ups (do not execute via Charlie)

- Downstream node network, firewall, agent logs on the customer cluster
- Credential reissue procedures outside the disclosed catalog


---

<!-- source: reference/mcp-tools.md -->

# MCP tool quick reference (0.3.5)

Prefer tools by name. All reads are management-plane only.

## First questions

| User ask | Tool |
| --- | --- |
| K8s / Astronomer version | `installation.summary` |
| Is the install healthy? | `installation.readiness` then `pods` |
| Which pods? CrashLoop? | `management.pods` |
| Rollout stuck? | `management.rollout_status` |
| Logs | `management.pod_logs` (after pods) |
| Events | `management.events` |
| Nodes | `management.nodes` |
| Fleet agents | `agent_fleet.*` + `tunnel.*` |

## Writes (mode-gated)

Restart / rollout / scale / tunnel restart / run_job need **approval** unless
policy says otherwise. Auto-eligible by catalog: `argocd.self_management_sync`,
`queue.retry_task` only (still require mode=auto + central allowlist).


---

<!-- source: reference/modes-and-authority.md -->

# Modes and authority (0.3.5)

Authority is **not** chosen by the model. Product mode + disclosure + live RBAC
gate every tool call.

- **read_only**: diagnose freely with reads; no product writes; produce findings.
- **approval**: propose exact writes; human must approve once; verify after.
- **auto**: only allowlisted auto-eligible actions without human click; else fall back to approval/finding.
- **disabled**: no chat tools.

Chat sessions inherit product authoritative mode. Raising mode requires Charlie
admin (disclosure acknowledged, not emergency-disabled).


---

<!-- source: scope.md -->

# Astronomer 0.3.5 Charlie SRE scope

Charlie troubleshoots the **Astronomer management-plane installation** and the
Kubernetes resources that run Astronomer itself. It does **not** operate
downstream customer clusters.

## In scope

- Installation summary: product version, chart, namespace, release, **kubernetes_version**
- Readiness, database, migrations, TLS, backups, queues
- Owned management Deployments/StatefulSets/Pods (release-prefixed)
- Bounded redacted pod logs, events, nodes, storage, network
- Argo self-management status
- Fleet agent **connection metadata** and tunnel health (from Astronomer DB/telemetry only)
- Bounded writes under mode rules: restart/scale/rollout mutable components,
  Argo sync, queue retry, backup/restore-drill jobs

## Out of scope

- Downstream kubectl: list pods, logs, exec, apply, delete in customer clusters
- Generic shell, raw SQL, free-form HTTP, Secret values
- Destructive catalog operations (none published in v1)

## Authority

Least-authority intersection of product mode, disclosure, live RBAC, and
(for writes) approval or auto allowlist:

| Mode | Reads | Writes |
| --- | --- | --- |
| disabled | No product tools | No |
| read_only | Yes | No (findings/guidance only) |
| approval | Yes | One exact approved write at a time |
| auto | Yes | Only auto-eligible + allowlisted actions |

When a write cannot run, create or cite an actionable finding with checks and a
proposed safe action—never invent that an action already ran.

