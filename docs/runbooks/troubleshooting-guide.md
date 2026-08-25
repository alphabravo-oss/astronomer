# Troubleshooting guide

This is the entry point when the symptom does not yet map to a specific alert
runbook. It prioritizes read-only evidence and safe stabilization. Do not
restart every component, delete queues, force migrations, rotate keys, or
reinstall agents before identifying the failed dependency and preserving
evidence.

## First five minutes

1. Declare severity, incident commander, affected tenants/clusters, start time,
   last known good time, and recent changes.
2. Check the public liveness endpoint and dependency-aware readiness endpoint.
3. Inspect deployment availability, Pod restarts, Jobs, PDBs, node pressure,
   and recent events.
4. Correlate the same request ID across ingress, server logs, audit, worker
   operation, and task state. Keep credentials and payloads out of notes.
5. Classify the failure before acting: management API, PostgreSQL, Redis/worker,
   tunnel/agent, delivery/Flux, identity, monitoring/logging, or release/config.

```bash
curl --fail --show-error https://astronomer.example.com/health/
curl --fail --show-error https://astronomer.example.com/readyz
kubectl -n astronomer get deploy,pod,job,cronjob,pdb -o wide
kubectl -n astronomer get events --sort-by=.lastTimestamp | tail -100
helm -n astronomer history astronomer
```

## Symptom routing

| Symptom | First evidence | Runbook |
|---|---|---|
| API 5xx, latency, or unavailable readiness | request IDs, server restarts, DB pool, dependency readiness | [High HTTP error rate](high-http-error-rate.md), [DB pool exhausted](db-pool-exhausted.md) |
| Login, SSO, callback, or group sync failure | IdP status, callback request ID, connector revision, clock | [OIDC outage](oidc-outage.md) |
| Migration/preflight Job failed | exact release, schema state, Job logs, previous revision | [Failed migration](failed-migration.md), [upgrade](../upgrade-runbook.md) |
| Queue lag, repeated retries, or dead tasks | owner/queue metrics, operation/outbox status, worker readiness | [Worker backlog](worker-queue-backlog.md), [DLQ growing](worker-dlq-growing.md), [task outbox](task-outbox-stalled.md) |
| Missing audit/SIEM events | audit write/outbox metrics and durable rows | [Audit events dropped](audit-events-dropped.md), [audit outbox](audit-outbox-stalled.md), [SIEM events](siem-events-dropped.md) |
| One or many agents disconnected | tunnel owner, heartbeat age, DNS/TLS, agent conditions | [Agent disconnected](agent-disconnected.md), [mass disconnect](cluster-agent-mass-disconnect.md) |
| Flux rollout stalled or stale | source resolution, assignment ack, Flux conditions, ownership | [Delivery control plane](delivery-control-plane.md), [assignment lag](delivery-assignment-lag.md), [status stale](delivery-status-stale.md) |
| Backup or recovery proof missing | backup Job/object/key bundle/drill result | [Backup and restore](management-backup-and-restore.md), [drill failed](backup-restore-drill-failed.md) |
| Management logs, Loki, or Grafana unavailable | gateway health, ingest counters, storage/capacity | [Management logging](management-logging-down.md), [Loki](loki-gateway-down.md), [Grafana](grafana-down.md) |
| Disk, Postgres failover, or Redis loss | storage/replication metrics and provider events | [Postgres disk](postgres-disk-full.md), [Postgres failover](postgres-failover.md), [Redis loss](redis-data-loss.md) |
| Unknown/multisystem incident | bounded redacted artifact | [Support bundle](support-bundle.md) |

## Layered diagnosis

### Release and Kubernetes layer

Compare the running chart revision, image digests, compatibility manifest, and
rendered values with the approved release record. Look for Pending Pods,
unsatisfied affinity/topology, denied Pod Security, exhausted quota, image pull
failures, crash loops, failing probes, blocked PDBs, and a preflight/migration
Job that never completed. A mutable or unexpected image is a security incident.

### API and database layer

Use `/readyz`, route-class metrics, status code, operation ID, and request ID.
Check PostgreSQL reachability, TLS, pool saturation, long transactions,
deadlocks, replication lag, disk, schema version, and `dirty=false`. Do not run
unbounded SQL against an impaired primary. A high-risk mutation that fails
closed during a database outage is expected; do not bypass audit persistence.

### Queue and worker layer

Distinguish the standalone `default` owner from the server-owned `tunnel`
queue. Confirm the matching process advertises required capabilities, the task
outbox is dispatching, retries are bounded, and the durable operation row is
not terminal. Never move tunnel work to a DB-only worker or delete an outbox/DLQ
row to clear a graph. Retry through the typed operation endpoint after the
dependency is healthy.

### Agent and downstream layer

Determine whether one cluster, one failure domain, or the fleet is affected.
Check management DNS/TLS, outbound connectivity, registration/token expiry,
protocol and Kubernetes compatibility, tunnel ownership, heartbeat, and agent
conditions. For delivery, inspect the immutable source/version/target and the
local Flux source, Kustomization/HelmRelease conditions. Astronomer does not
provision or repair cluster infrastructure.

### Identity and policy layer

Separate authentication (who is the caller), token scope, platform RBAC,
project/cluster/namespace scope, and downstream Kubernetes authorization. A
clean 401/403 is evidence, not a reason to grant admin. Reproduce with a
least-privilege test identity and retain the request ID and evaluated scope;
never test by sharing a production user's token.

## Stabilize and recover

Choose the smallest reversible action: stop a rollout, pause a producer, scale
one known bottleneck within its certified profile, restore a failed dependency,
or retry one idempotent operation. Preserve logs and state before restarting a
crashing process. Use Helm rollback only with the database compatibility and
backup decision in the [upgrade runbook](../upgrade-runbook.md).

After recovery, verify readiness, error/latency/queue/audit metrics, the original
user workflow, one negative authorization case, one canary cluster/operation,
and the full observation window. Record root cause, blast radius, action and
rollback timestamps, evidence links, achieved SLO/RPO/RTO, and prevention work.
