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
5. `astronomer.queue.health` and `astronomer.queue.failed_tasks`
6. `astronomer.management.pods` / `astronomer.management.events` if a component is not ready
7. `astronomer.backups.status` if restore/drill related

## Decision

| Observation | Next step |
| --- | --- |
| Schema **dirty** or version behind expected | Stop recommending app restarts; escalate to migration/operator runbooks |
| Queue backlog of failed tasks | List with `failed_tasks`; `queue.retry_task` is a **write** (approval/auto policy) |
| Component unready with CrashLoop | Follow **crashloop-management-pod** playbook |
| DB not accepting connections | Report `database.health` evidence; operator fixes Postgres / DSN |

## Stop conditions

- Do not invent migration commands Charlie cannot run.
- Do not claim queue retry succeeded without a successful write tool result.
