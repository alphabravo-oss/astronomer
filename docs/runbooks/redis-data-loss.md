# Runbook — Redis data loss

**Severity**: page
**Component**: Redis (asynq backend)

## Symptoms

- `/readyz` reports `checks.redis.error = "<connection-failed>"`
- Worker pods log `dial tcp: connection refused` for Redis
- Async tasks not processing — pending queue empty (lost) or
  unprocessable (e.g. ID references rows that no longer exist)
- `astronomer_worker_queue_depth` drops to zero unexpectedly

## Triage

1. **Redis up?**
   ```bash
   kubectl -n astronomer get pods -l app=redis
   kubectl -n astronomer exec -it astronomer-redis-0 -- redis-cli PING
   # External Redis:
   redis-cli -h $REDIS_HOST PING
   ```
2. **Was data lost?** Compare current `DBSIZE` against the last known
   queue depth from Prometheus:
   ```bash
   kubectl -n astronomer exec astronomer-redis-0 -- redis-cli DBSIZE
   ```
   Compare to the last `astronomer_worker_queue_depth` value before the
   incident.
3. **Persistence configured?**
   ```bash
   kubectl -n astronomer exec astronomer-redis-0 -- redis-cli CONFIG GET save
   kubectl -n astronomer exec astronomer-redis-0 -- redis-cli CONFIG GET appendonly
   ```
   - `save ""` and `appendonly no` → not persistent; restart = data loss
     by design. Note this in the incident.

## Recovery

### Restore from AOF / RDB

If persistence was enabled, the PVC retains the dump. Restart the
Redis pod and asynq picks up from there:
```bash
kubectl -n astronomer rollout restart statefulset/astronomer-redis
```

### Accept the loss — re-derive enqueued work

For the work that's now gone, look at the database for "what should
have been pending":
- `task_outbox WHERE status IN ('pending','failed','delivering')` —
  these are durable task intents and will be retried by
  `task_outbox:dispatch` after Redis returns. Rows in `dead` need
  operator review before requeueing.
- `tool_operations WHERE status='pending'` — these can be requeued
  by the reconciler's next tick (T17 parallel dispatch)
- `catalog_operations WHERE status='pending'` — same
- `monitoring_operations WHERE status='pending'` — same
- `notifications` / one-off tasks — these are gone; users will need
  to re-trigger

After Redis is back, trigger reconcilers explicitly:
```bash
# Hit the trigger endpoint (admin):
curl -X POST -H "Authorization: Bearer $TOKEN" \
  "https://${HOST}/api/v1/admin/reconcile/trigger"
```
(Endpoint shape may vary — check `internal/handler/`.)

### Snapshot recovery for external Redis

If using a managed Redis (ElastiCache / Cloud Memorystore), restore the
latest snapshot per the provider's procedure. Point the chart at the
restored endpoint via `redis.external.address`.

## Verify

- `/readyz` returns 200 with `checks.redis.ok=true`
- `astronomer_worker_jobs_total` rate climbs as queued work drains
- `task_outbox WHERE status IN ('pending','failed','delivering') AND
  next_attempt_at < NOW() - interval '5 minutes'` is empty, unless Redis
  is still down
- `task_outbox WHERE status='dead'` is empty or has an acknowledged
  incident note
- `tool_operations WHERE status='pending' AND created_at < NOW() -
  interval '1 hour'` is empty (no orphaned work)

## Prevention

- Enable AOF (`appendonly yes`, `appendfsync everysec`) for the
  bundled Redis if you can afford the disk
- Use a managed Redis with multi-AZ failover for production
- Periodic snapshot job; document RTO/RPO targets

## Related

- `internal/worker/worker.go` — asynq client wiring
- [management-plane-dr-runbook.md](../management-plane-dr-runbook.md) —
  full DR plan (Postgres + Redis as a pair)
