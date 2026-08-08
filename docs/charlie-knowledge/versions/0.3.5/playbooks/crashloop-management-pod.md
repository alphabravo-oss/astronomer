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
