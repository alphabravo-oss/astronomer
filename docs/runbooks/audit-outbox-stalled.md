# AstronomerAuditOutboxDeadRows / AstronomerAuditOutboxStalled

Transactional audit intents are not reaching the canonical partitioned
`audit_log` table within the expected delivery window. A domain mutation and
its `audit_outbox` intent commit together, so the intent is durable; this alert
means canonical audit evidence is delayed or has exhausted automated retries.

## Symptoms

- `AstronomerAuditOutboxStalled` fires when the oldest `pending`, `failed`, or
  `delivering` intent is older than five minutes.
- `AstronomerAuditOutboxDeadRows` fires when at least one intent exhausted its
  retry budget and entered `dead`.
- Worker logs contain `audit outbox row dispatch failed` or `mandatory audit
  intent exhausted delivery attempts`.
- Support bundles report `audit-pipeline-health.json` as degraded.

## Triage

1. Check management Postgres health, disk capacity, connection saturation,
   failed migrations, and the `astronomer-worker` replicas.
2. Inspect lifecycle metadata without selecting the bounded event detail:

   ```sql
   SELECT status, count(*), min(event_created_at), max(updated_at)
   FROM audit_outbox
   GROUP BY status
   ORDER BY status;

   SELECT id, action, resource_type, attempt_count, max_attempts,
          next_attempt_at, locked_until, left(last_error, 256) AS last_error
   FROM audit_outbox
   WHERE status IN ('failed', 'dead', 'delivering')
   ORDER BY updated_at DESC
   LIMIT 100;
   ```

3. Correlate failures with `audit:outbox_dispatch` worker logs. A `delivering`
   row with an expired lease is recovered automatically on the next dispatch.
4. Confirm migration 15 is applied and `audit_log` has a writable partition
   for `event_created_at` (the default partition is the safety net).

## Recovery

- Restore Postgres write availability or worker scheduling first. Pending and
  failed rows retry automatically with bounded backoff.
- For a dead row, preserve the row and incident evidence. After correcting the
  cause, re-arm it in a controlled transaction:

  ```sql
  UPDATE audit_outbox
  SET status = 'failed', attempt_count = 0, next_attempt_at = now(),
      locked_until = NULL, last_error = '', updated_at = now()
  WHERE id = '<reviewed-audit-outbox-uuid>' AND status = 'dead';
  ```

- Never delete pending, failed, delivering, or dead rows. The nightly retention
  task deletes only delivered receipts older than 30 days; canonical
  `audit_log` retention remains governed by its partition policy.

## Verify

- `astronomer_audit_outbox_rows{status=~"pending|failed|delivering|dead"}`
  returns zero after the backlog drains.
- `astronomer_audit_outbox_oldest_seconds` returns zero for active states.
- The reviewed event IDs exist in `audit_log` with matching `created_at`, and
  the corresponding outbox rows are `delivered`.
- Both alerts resolve and a fresh support bundle reports a healthy pipeline.
