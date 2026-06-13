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
review; confirm the task payload is still valid before setting `status='pending'`
and `next_attempt_at=now()`.
