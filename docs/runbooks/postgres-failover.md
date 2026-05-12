# Runbook — Postgres failover (external DB)

**Severity**: page
**Component**: `internal/db` + external Postgres

## Symptoms

- `/readyz` reports `checks.database.error`
- Server / worker logs flooded with `dial tcp` or `password
  authentication failed for user` errors
- `AstronomerDBPoolExhausted` may fire as connections get held waiting
  for an unreachable backend

## Triage

1. **Primary or replica?**
   - Managed RDS / CloudSQL / Aurora: check the provider console for
     failover events
   - DIY HA (Patroni / repmgr / pg_auto_failover): check cluster status
2. **Read-only or fully down?**
   ```bash
   psql "$DATABASE_URL" -c "SELECT pg_is_in_recovery();"
   ```
   - `t` → connected to a replica; we need write access
   - `f` but reads failing → server-side issue (disk, OOM, swap)
3. **Connectivity vs auth?**
   - `pg_isready -h $DB_HOST` to check reachability
   - Re-validate the secret value matches the DB user's password

## Recovery

### Managed failover (cloud provider)

Wait for the provider's automated failover; the DSN points at the
endpoint (writer endpoint for Aurora; reader endpoint must NOT be in
the chart's `postgres.external.dsn`). Once failover completes, pgxpool
reconnects within `healthCheckPeriod` (default 30s).

### Manual failover (DIY HA)

Follow the cluster manager's promote procedure, then update
`postgres.external.dsn` in the chart values to point at the new
primary. `helm upgrade` (or Argo sync) re-rolls the pods so they pick
up the new DSN.

### Failover during a migration

If a migration was mid-flight (the migrate init container) when
failover happened, see [failed-migration.md](failed-migration.md) for
the dirty-state recovery procedure first, then bring up the app.

## Verify

- `/readyz` returns 200 with `checks.database.ok=true`
- A sample write succeeds (audit log row should appear from any UI
  action — see `audit_log` table)
- `astronomer_db_query_duration_seconds_count{operation="select"}` rate
  matches pre-incident baseline

## Prevention

- Use multi-AZ managed Postgres (RDS Multi-AZ / Aurora)
- Pgxpool's `HealthCheckPeriod` (default 30s, chart-tunable via T21)
  catches stale conns within ~30s after failover
- Keep DSN in a Secret keyed by an external param so failover doesn't
  require a chart re-publish

## Related

- [management-plane-dr-runbook.md](../management-plane-dr-runbook.md) —
  full DR including PITR + pg_dump restore
- `internal/db/db.go` `ConnectWithConfig` — pool config knobs (T21)
- [db-pool-exhausted.md](db-pool-exhausted.md) — secondary alert
