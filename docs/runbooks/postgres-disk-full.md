# Runbook — Postgres PVC near full

**Severity**: warning (>80%) → page (>95%)
**Component**: bundled Postgres StatefulSet PVC, or external DB storage

## Symptoms

- `kubelet_volume_stats_used_bytes / kubelet_volume_stats_capacity_bytes`
  > 0.80 on the `astronomer-postgres-0` PVC
- Postgres writes start failing with `could not extend file`
- Application errors: `pq: out of disk space` or `disk full`
- Audit log inserts begin failing (the audit_log table is the largest
  growing table — see partition maintenance)

## Triage

1. **What's eating the space?**
   ```sql
   SELECT relname, pg_size_pretty(pg_total_relation_size(c.oid)) AS size
   FROM pg_class c
   JOIN pg_namespace n ON n.oid = c.relnamespace
   WHERE c.relkind = 'r' AND n.nspname = 'public'
   ORDER BY pg_total_relation_size(c.oid) DESC
   LIMIT 15;
   ```
   Typical offenders (in order of usual size):
   - `audit_log_YYYY_MM` partitions — see retention task below
   - `cluster_metrics_* ` (if observability data is persisted long-term)
   - `tool_operations` / `catalog_operations` / `monitoring_operations`
     event tables

2. **Bloat vs real growth?**
   ```sql
   SELECT relname, n_dead_tup, n_live_tup,
          ROUND(100.0 * n_dead_tup / NULLIF(n_dead_tup + n_live_tup, 0), 1) AS pct_dead
   FROM pg_stat_user_tables
   ORDER BY n_dead_tup DESC LIMIT 10;
   ```
   High dead-tuple % → autovacuum is behind.

3. **WAL accumulation?**
   ```bash
   du -sh /var/lib/postgresql/data/pg_wal/ 2>/dev/null
   ```
   If WAL > 5GB, replication may have stopped (subscriber down).

## Recovery

### Immediate: free space

```bash
# Drop the oldest audit_log partition (retention is enforced by
# T22-era audit_partition_maintenance task, but if it's behind:
kubectl -n astronomer exec deploy/astronomer-server -- \
  psql "$DATABASE_URL" -c "DROP TABLE audit_log_2025_01;"
```
(Adjust the partition name; partitions follow `audit_log_YYYY_MM`.)

### Permanent fix: grow the PVC

For the bundled StatefulSet:
```yaml
# values-production.yaml
postgres:
  bundled:
    enabled: true
    storage:
      size: 200Gi    # bumped from 100Gi
```
`helm upgrade`. Note: PVC resize requires the underlying StorageClass
to support online expansion (`allowVolumeExpansion: true`).

For external Postgres: contact the DB owner / cloud provider to grow
the disk.

### Vacuum bloat

```sql
VACUUM (FULL, VERBOSE) audit_log;
-- Or for specific partitions:
VACUUM (FULL, VERBOSE) audit_log_2026_05;
```
`VACUUM FULL` takes an exclusive lock — schedule for a maintenance
window if traffic is heavy.

## Verify

- `kubelet_volume_stats_available_bytes` on the PVC > 20% of capacity
- Postgres inserts succeed: a test audit log row appears from any UI
  action
- Vacuum stats show `last_vacuum` / `last_autovacuum` within the last hour

## Prevention

- audit_log retention task runs nightly (`EnforceAuditLogRetentionType`)
  — confirm `AuditLogRetentionMonths` is set sensibly (chart value)
- Alert on PVC capacity at 70% via the cluster's own monitoring (not
  the astronomer chart's rules)
- Watch `pg_stat_user_tables.n_dead_tup` for autovacuum lag

## Related

- `internal/worker/tasks/audit_partition_maintenance.go` — partition maintenance
- `internal/worker/tasks/audit_retention.go` — retention enforcement
- [postgres-failover.md](postgres-failover.md) — failover for managed DBs
