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
| Queue has `available:true`, `materialized:false`, `consumer_ready:true` | Treat it as configured, served, and idle. The Redis key is created only after work is enqueued; do not report the queue as unavailable. |
| Queue has `consumer_ready:false` | Confirm the expected worker registration and queue weights; this is an unserved queue even if inspection itself is available. |
| Queue has `available:false` and `inspection_code:queue_inspection_unavailable` | Report an inspection failure and correlate with Redis and worker health; do not describe it as an empty queue. |
| Queue backlog or failed tasks | List the relevant state with `queue.tasks`, inspect one with `queue.task_get`, and use `failed_tasks` for the archived set; `queue.retry_task` is a **write** (approval/auto policy) |
| `catalog:sync` pending or failed | Correlate the task with `catalog.repositories`; it synchronizes Astronomer Helm catalog repositories and is separate from `charlie:trigger_dispatch` |
| Component unready with CrashLoop | Follow **crashloop-management-pod** playbook |
| DB not accepting connections | Report `database.health` evidence; operator fixes Postgres / DSN |

## Stop conditions

- Do not invent migration commands Charlie cannot run.
- Do not claim queue retry succeeded without a successful write tool result.
- Do not request or disclose raw queue payload values, raw failure strings,
  repository credentials, or secret-bearing repository URL components.
