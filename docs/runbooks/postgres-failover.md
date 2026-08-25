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

Before a release, rehearse the fail-closed mutation contract with the
repository-owned disposable PostgreSQL 16 drill:

```bash
make test-postgres-outage-qualification
```

The drill exercises every transaction interface wired through the production
server adapter, compliance-baseline apply/revert, delivery planning, rollout
control and approval, deployment control, system rollout, credential-returning
authentication, an SMTP configuration test, and the raw Kubernetes pre-effect
audit boundary. It stops and restores only its labelled disposable container,
then checks that no unaudited intent, remote effect, response credential, or
secret-bearing audit detail escaped.
Set `POSTGRES_OUTAGE_QUALIFICATION_RACE=1` for race instrumentation.

Certify the recovery objective separately with the disposable physical-
replication lane:

```bash
make test-postgres-failover-certification
```

This starts a real PostgreSQL 16 primary and physical streaming standby,
enables synchronous acknowledgement, commits Astronomer persistence canaries,
and keeps the production database pool alive behind a stable writer endpoint.
It kills the primary with `SIGKILL`, requires `/readyz` to return 503, promotes
the standby with `pg_ctl promote`, redirects the endpoint, and requires both
readiness and a durable write/read to recover. The default release thresholds
are zero acknowledged rows lost and 30 seconds from fault injection through
successful persistence recovery. Override them only for an explicitly approved
environment-specific objective:

```bash
POSTGRES_FAILOVER_RPO_MAX_ROWS=0 \
POSTGRES_FAILOVER_RTO_MAX_SECONDS=30 \
POSTGRES_FAILOVER_ARTIFACT_DIR=/secure/release-evidence/postgres-failover \
make test-postgres-failover-certification
```

The retained directory contains `evidence.json` using schema
`astronomer-postgres-failover-certification/v1`, primary/replica logs and
inspection metadata, base-backup and migration logs, and the test transcript.
The evidence records source/workflow/image provenance, UTC event timestamps,
the commit LSN, row RPO, RTO, promotion time, readiness failure-detection time,
thresholds, and residual scope. Missing or malformed evidence, a non-zero RPO,
an over-budget RTO, readiness that does not fail closed, or a failed recovered
write blocks the lane.

The deterministic TCP endpoint models the stable writer endpoint supplied by a
managed HA service. The recorded RTO intentionally excludes that provider's
control-plane/DNS detection time; production release evidence must add the
provider's measured component rather than presenting this local value as an
end-to-end managed-service RTO.

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
