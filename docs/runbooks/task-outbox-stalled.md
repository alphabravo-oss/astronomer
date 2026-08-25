# Task Outbox Stalled

The `task_outbox` table stores durable task intents before they are delivered
to Redis/Asynq. A stalled or dead row means Postgres has recorded work that has
not reached the execution queue.

## Check Redis and Workers

```bash
kubectl -n astronomer get pods -l app.kubernetes.io/component=worker
kubectl -n astronomer logs deploy/astronomer-worker --tail=200 | grep task_outbox
kubectl -n astronomer exec statefulset/astronomer-redis -- redis-cli ping
```

## Inspect Rows

Use the admin API for normal operations:

```bash
curl -H "Authorization: Bearer $TOKEN" \
  "https://${HOST}/api/v1/admin/task-outbox/?status=dead&limit=50"
```

The API intentionally reports `payload_size` instead of raw task payload bytes.
If the row is safe to retry:

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  "https://${HOST}/api/v1/admin/task-outbox/${TASK_OUTBOX_ID}/retry/"
```

The retry API locks the row, refuses delivered work, resets the delivery-attempt
budget and stale lease/error state, and commits the change with its mandatory
audit intent. Use this endpoint instead of directly updating the table.

For database-level inspection:

```sql
SELECT id, dedupe_key, task_type, queue_name, status, attempt_count,
       max_delivery_attempts, next_attempt_at, locked_until, last_error
FROM task_outbox
WHERE status IN ('pending','failed','delivering','dead')
ORDER BY next_attempt_at ASC, created_at ASC
LIMIT 50;
```

Rows in `pending`, `failed`, or expired `delivering` should retry through
`task_outbox:dispatch` once Redis is healthy. Rows in `dead` require operator
review; confirm the task is still valid, then use the administrative retry API
so the retry and audit record cannot diverge.

## Rehearse Outage Recovery

Before a release, run the repository-owned disposable failure-injection drill:

```bash
make test-redis-outage-recovery
```

The drill uses isolated PostgreSQL 16 and Redis 7 containers. It stops Redis
before the production test-notification API transaction, proves the task and
audit intents committed without a false external-delivery claim, exercises a
failed production outbox dispatch, restarts Redis, and verifies the original
row reaches the real notification worker and receiver exactly once. The
containers and temporary migration binary are removed automatically. Set
`REDIS_OUTAGE_RECOVERY_RACE=1` to run the same drill under Go's race detector.

To qualify process replacement separately from Redis failure, run:

```bash
make test-process-restart-qualification
```

That drill gracefully replaces a server-embedded tunnel worker between two
durable CIS polling stages, then replaces a standalone worker after task and
audit intent commit but before its already-scheduled dispatcher wakeups. It
uses real process constructors and asserts that the replacement processes
complete the original rows exactly once. Set
`PROCESS_RESTART_QUALIFICATION_RACE=1` for race instrumentation.
