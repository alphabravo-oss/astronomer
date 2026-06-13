# Runbook - Postgres runtime contention

**Alerts**: `AstronomerDBDeadlocks`, `AstronomerDBLongTransaction`
**Severity**: warning
**Component**: `internal/db`, PostgreSQL runtime statistics

## Symptoms

- `rate(astronomer_db_deadlocks_total[5m]) > 0`
- `astronomer_db_longest_transaction_seconds > 300` for 10 minutes
- Migrations, vacuum, or ordinary requests are slower than normal
- Application logs may show serialization, deadlock, timeout, or canceled-query errors

## Triage

1. Find old transactions:
   ```sql
   SELECT pid, usename, state, age(clock_timestamp(), xact_start) AS xact_age, wait_event_type, wait_event, query
   FROM pg_stat_activity
   WHERE datname = current_database()
     AND xact_start IS NOT NULL
   ORDER BY xact_start ASC
   LIMIT 20;
   ```

2. Find blockers and waiters:
   ```sql
   SELECT blocked.pid AS blocked_pid,
          blocked.query AS blocked_query,
          blocking.pid AS blocking_pid,
          blocking.query AS blocking_query
   FROM pg_catalog.pg_locks blocked_locks
   JOIN pg_catalog.pg_stat_activity blocked
     ON blocked.pid = blocked_locks.pid
   JOIN pg_catalog.pg_locks blocking_locks
     ON blocking_locks.locktype = blocked_locks.locktype
    AND blocking_locks.database IS NOT DISTINCT FROM blocked_locks.database
    AND blocking_locks.relation IS NOT DISTINCT FROM blocked_locks.relation
    AND blocking_locks.page IS NOT DISTINCT FROM blocked_locks.page
    AND blocking_locks.tuple IS NOT DISTINCT FROM blocked_locks.tuple
    AND blocking_locks.virtualxid IS NOT DISTINCT FROM blocked_locks.virtualxid
    AND blocking_locks.transactionid IS NOT DISTINCT FROM blocked_locks.transactionid
    AND blocking_locks.classid IS NOT DISTINCT FROM blocked_locks.classid
    AND blocking_locks.objid IS NOT DISTINCT FROM blocked_locks.objid
    AND blocking_locks.objsubid IS NOT DISTINCT FROM blocked_locks.objsubid
    AND blocking_locks.pid != blocked_locks.pid
   JOIN pg_catalog.pg_stat_activity blocking
     ON blocking.pid = blocking_locks.pid
   WHERE NOT blocked_locks.granted
     AND blocking_locks.granted;
   ```

3. Correlate with app metrics:
   ```promql
   histogram_quantile(0.99, sum(rate(astronomer_db_query_duration_seconds_bucket[5m])) by (le, operation))
   rate(astronomer_db_deadlocks_total[5m])
   astronomer_db_longest_transaction_seconds
   ```

## Recovery

- If one backend is clearly stuck and blocking the system, terminate only that backend:
  ```sql
  SELECT pg_terminate_backend(<blocking_pid>);
  ```
- If contention started after a deploy, roll back or disable the feature path that introduced the new transaction pattern.
- If migrations are waiting on an application transaction, pause the migration and drain server/worker pods before retrying.
- If deadlocks repeat, inspect code paths that update the same tables in different orders and make the order consistent.

## Verify

- `rate(astronomer_db_deadlocks_total[5m])` returns to 0
- `astronomer_db_longest_transaction_seconds` drops below 60 seconds
- p99 `astronomer_db_query_duration_seconds` returns to baseline
- No new database deadlock errors appear in server or worker logs

## Prevention

- Keep explicit transactions small and add `defer tx.Rollback(ctx)` immediately after `Begin`.
- Avoid network calls while a DB transaction is open.
- For multi-table updates, use one consistent table order across handlers and workers.
- For large backfills, use bounded batches and sleep between batches.
