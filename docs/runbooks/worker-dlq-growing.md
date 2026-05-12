# Runbook — Worker DLQ growing

**Alert**: `AstronomerWorkerDLQGrowing`
**Severity**: warning (any growth in archived/DLQ over 10m)
**Component**: `internal/worker`

## Symptoms

- PrometheusRule expr:
  `sum(increase(astronomer_worker_queue_depth{state="archived"}[10m])) > 0`
  for 10m
- Tasks exhaust their retry budget and land in asynq's "archived" state
- Same task type appearing repeatedly in the DLQ → systemic bug, not blip

## Triage

1. **Inspect the DLQ contents** (T28 admin endpoint):
   ```bash
   curl -s -H "Authorization: Bearer $TOKEN" \
     "https://${HOST}/api/v1/admin/queues/default/dlq?limit=50" | jq '.tasks[] | {type, error, queue}'
   ```
   Or via the support bundle (`asynq-queues.json` section).

2. **Group by error message** — one common error usually points to the
   root cause:
   ```bash
   curl -s -H "Authorization: Bearer $TOKEN" \
     "https://${HOST}/api/v1/admin/queues/default/dlq?limit=200" \
     | jq -r '.tasks[].error' | sort | uniq -c | sort -rn | head -10
   ```

3. **Recent deploy?** A new task type + bug may be poisoning the queue.
   `git log` against `internal/worker/tasks/` for the suspect type.

## Recovery

### One-off failures: requeue selectively

```bash
# Move all DLQ tasks for type X back to pending
curl -X POST -H "Authorization: Bearer $TOKEN" \
  "https://${HOST}/api/v1/admin/queues/default/dlq/requeue?type=cluster:health_check"
```
(Endpoint shape may evolve; check `internal/handler/admin_queues.go` —
T28.)

### Systemic bug: pause + fix + replay

1. Pause workers for the affected task type — easiest is to scale to
   zero, fix the bug, redeploy, then replay:
   ```bash
   kubectl -n astronomer scale deployment/astronomer-worker --replicas=0
   ```
2. Roll the fix (`kubectl rollout` after image rebuild + push).
3. Scale workers back up, then requeue from DLQ.

### Permanent kills: drop unrecoverable tasks

If the payload is malformed or the targeted resource is permanently
gone (e.g. a project that was deleted), drop the DLQ entries:
```bash
curl -X DELETE -H "Authorization: Bearer $TOKEN" \
  "https://${HOST}/api/v1/admin/queues/default/dlq?type=project:reconcile"
```

## Verify

- `astronomer_worker_queue_depth{state="archived"}` flat or decreasing
- `astronomer_worker_jobs_total{status="error"}` rate stays low
- No new entries in DLQ over the next hour

## Prevention

- Bounded retries: asynq tasks set `MaxRetry` explicitly. Don't ship a
  new task type with the default — pick a number based on the
  idempotency of the side effect.
- Tasks should be idempotent so retries are safe.
- For tasks calling external APIs, wrap with timeouts shorter than the
  retry window so a hung call doesn't burn all attempts.

## Related

- [worker-queue-backlog.md](worker-queue-backlog.md) — upstream
- `internal/worker/job_metrics.go` `instrumentTask` — metrics + tracing wrap
- `internal/handler/admin_queues.go` — admin DLQ endpoints (T28)
