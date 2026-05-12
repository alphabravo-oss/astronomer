# Runbook — Worker queue backlog

**Alert**: `AstronomerWorkerQueueBacklog`
**Severity**: warning (>1000 pending tasks for 5m)
**Component**: `internal/worker`

## Symptoms

- PrometheusRule expr: `max(astronomer_worker_queue_depth{state="pending"}) > 1000` for 5m
- UI actions complete but their downstream effects lag (e.g. helm
  install returns 202 but operation row stays `pending` for minutes)
- `astronomer_worker_jobs_total` rate flat while `queue_depth{state="pending"}` climbs

## Triage

1. **Which queue is backed up?** asynq has multiple queues (default,
   critical) and many task types. The backlog is per-queue:
   ```bash
   # Via support-bundle (admin)
   curl -s -H "Authorization: Bearer $TOKEN" \
     "https://${HOST}/api/v1/admin/queues/" | jq
   # Or the embedded admin endpoint
   curl -s -H "Authorization: Bearer $TOKEN" \
     "https://${HOST}/api/v1/admin/queues/default/dlq" | jq '.tasks | length'
   ```

2. **Workers running?** Confirm the worker Deployment has all replicas Ready:
   ```bash
   kubectl -n astronomer get deploy/astronomer-worker
   kubectl -n astronomer logs -l app.kubernetes.io/component=worker --tail=200 | grep -iE 'panic|error|dead'
   ```
   - Zero replicas Ready → cluster-level issue; scale check + readyz on workers.

3. **Long-running tasks hogging concurrency?** asynq concurrency is
   shared across task types. A single 10-min helm operation per worker
   pod ties up one slot. Check via:
   ```bash
   kubectl -n astronomer logs -l app.kubernetes.io/component=worker --since=15m \
     | grep '"event":"worker_job_started"' | awk -F'"job":"' '{print $2}' | cut -d'"' -f1 | sort | uniq -c
   ```
   If one task type dominates and matches the slow path, see
   `internal/handler/catalog.go` `processPendingOperations` parallelism
   (T17) — that protects the reconciler from same-handler stalls, but
   the asynq worker pool is separate.

## Recovery

### Scale workers horizontally

```bash
kubectl -n astronomer scale deployment/astronomer-worker --replicas=6
```
Each replica adds `asynq.Config.Concurrency` (default 10) handler
slots. Verify load redistributes via the `astronomer_worker_jobs_total`
rate.

### Drain blocked tasks

If a runaway task type is the cause, inspect + selectively kill via the
asynq CLI:
```bash
kubectl -n astronomer exec -it deploy/astronomer-worker -- \
  asynq stats --uri "$REDIS_URL"
kubectl -n astronomer exec -it deploy/astronomer-worker -- \
  asynq task ls --queue default --state active
```

## Verify

- `astronomer_worker_queue_depth{state="pending"}` returns to baseline (<100)
- `AstronomerWorkerQueueBacklog` clears
- `astronomer_worker_jobs_total` rate matches enqueue rate

## Prevention

- Scale workers proactively before known event-heavy windows
  (mass cluster onboard, large argocd app sync)
- Watch `astronomer_worker_job_duration_seconds` p95 per job — a step
  change is your earliest signal

## Related

- [worker-dlq-growing.md](worker-dlq-growing.md) — what happens when retries are exhausted
- `internal/worker/worker.go` — asynq mux + concurrency config
