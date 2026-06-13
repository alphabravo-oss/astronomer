# Runbook — Postgres connection pool exhausted

**Alert**: `AstronomerDBPoolExhausted`
**Severity**: warning (any acquire-on-empty events over 5m)
**Component**: `internal/db`, pgxpool

## Symptoms

- PrometheusRule expr: `rate(astronomer_db_pool_empty_acquire_count_total[5m]) > 0` for 5m
- HTTP requests slow / time out at the chi handler boundary
- `astronomer_db_query_duration_seconds` p99 climbs disproportionately
  to query plan changes (the gap is wait-for-conn, not query time)
- `/readyz` returns 503 with
  `checks.database.error = "pgx pool exhausted: callers queueing for connections"`
  (T21 saturation gate)

## Triage

1. **How big is the gap?**
   ```promql
   astronomer_db_pool_total_connections - astronomer_db_pool_idle_connections
   rate(astronomer_db_pool_empty_acquire_count_total[5m])
   ```
   - All conns held with active queries → real load; bump `postgres.pool.maxConns` (chart) and roll.
   - Active conns << maxConns but acquires still blocking → leaked
     conns (rare; bug); jump to step 3.

2. **What's holding them?**
   ```sql
   -- inside Postgres
   SELECT pid, state, age(clock_timestamp(), query_start) AS age, query
   FROM pg_stat_activity
   WHERE state IN ('active', 'idle in transaction')
   ORDER BY age DESC
   LIMIT 20;
   ```
   - `idle in transaction` rows → a code path opened a tx and didn't commit/rollback. Note the `query` column and grep the repo.
   - Long `active` queries → consider whether they need indexes (rare in this codebase since sqlc-generated queries are simple).

3. **Leader-election advisory lock?** asynq leader election uses
   `pg_try_advisory_lock`. A pod that holds it but didn't release on
   crash will hold a conn for `idleTimeout` (default 5m).

## Recovery

### Bump pool size (chart)

```yaml
# values-production.yaml
postgres:
  pool:
    maxConns: 50          # default 25
    minConns: 10          # default 5
```
Then `helm upgrade` (or Argo sync). Watch the empty-acquire rate go
to zero before declaring done.

### Kill stuck Postgres connections (immediate)

```sql
-- inside Postgres, for a specific stuck pid from the triage query
SELECT pg_terminate_backend(<pid>);
```
Pgxpool will reconnect lazily; existing in-flight queries on that
backend will fail (the caller retries through asynq, or the user sees
a 5xx).

### Restart server pods (forces conn close)

```bash
kubectl -n astronomer rollout restart deploy/astronomer-server deploy/astronomer-worker
```
Use this only if (a) you can't identify a single stuck conn and
(b) you can afford the connection storm on the DB side.

## Verify

- `rate(astronomer_db_pool_empty_acquire_count_total[5m])` returns to 0
- `/readyz` clears the database-saturation 503
- `AstronomerDBPoolExhausted` alert clears

## Prevention

- Set per-handler timeouts (`chimiddleware.Timeout` is applied
  per-group in `internal/server/routes.go`) so a slow downstream
  doesn't leak a conn until the global server-write timeout.
- Always `defer rows.Close()` or `defer tx.Rollback()` after a sqlc-
  generated query that returns a Rows handle.
- Code review gate: anything opening an explicit transaction needs a
  matching `defer commit-or-rollback`.

## Related

- `internal/db/db.go` `ConnectWithConfig` + `PoolConfig`
- `internal/server/readiness.go` `dbPoolSaturationReporter` (T21)
- `internal/db/metrics.go` — pool-state metrics emission
