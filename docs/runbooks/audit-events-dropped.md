# AstronomerAuditEventsDropped / AstronomerAuditWriteFailures

One of the audit-log drop/failure counters is incrementing. Audit events are
either being **discarded before persistence** (`astronomer_audit_dropped_total`)
or **failing to write** to the database (`astronomer_audit_write_failures_total`).
On a platform that sells compliance, any sustained increase is a gap in the
audit trail and must be treated as an incident.

## Scope

- `AstronomerAuditEventsDropped` fires on `rate(astronomer_audit_dropped_total[10m]) > 0`.
  Events were dropped because the in-memory buffer was full (the sink could not
  keep up) and the enqueue was non-blocking by design.
- `AstronomerAuditWriteFailures` fires on `rate(astronomer_audit_write_failures_total[10m]) > 0`.
  The event reached the writer but the DB insert failed past its retry budget
  (connectivity, constraint violation, disk full, or a wedged transaction).

The two often fire together: writes failing → buffer backs up → drops begin.

## Diagnose

1. Correlate with database health. Audit writes share the management Postgres:

   ```
   # Are DB pool / deadlock / long-transaction alerts also firing?
   # AstronomerDBPoolExhausted, AstronomerDBDeadlocks, AstronomerDBLongTransaction
   kubectl -n <release-namespace> logs -l app.kubernetes.io/component=server \
     --tail=500 | grep -i 'audit'
   ```

2. Check Postgres disk and connectivity — a full data disk or an exhausted
   connection pool is the most common root cause:

   ```
   kubectl -n <release-namespace> exec <postgres-pod> -- \
     psql -U astronomer -d astronomer -c \
     "SELECT count(*) FROM audit_log;"
   ```

   A hang here means the DB, not the audit pipeline, is the problem — see
   `db-pool-exhausted.md` / `postgres-disk-full.md`.

3. Look for a sustained write-throughput spike (bulk RBAC change, mass
   reconcile, a runaway integration) that overwhelmed the buffer. The drop
   counter is labelled by component where available.

## Recover

- **DB-side cause** (pool exhausted, disk full, deadlock): fix the database
  problem first; the audit pipeline recovers on its own once writes succeed.
  Follow the matching DB runbook.
- **Sustained volume above capacity**: the buffer is intentionally bounded so a
  slow sink can't OOM the server. If legitimate volume has grown, scale the
  server (`server.replicaCount`) and/or bump the audit buffer size if the
  install exposes it; confirm the drop rate returns to zero.
- **Transient spike**: if the counters flatline again after the burst and the
  DB is healthy, no action beyond confirming the gap window is needed. Record
  the window (start/end of non-zero rate) for the compliance record.

## Assess the gap

Once recovered, quantify what was lost so it can be documented:

```
# Total dropped since process start (per component label):
# astronomer_audit_dropped_total
# Cross-check against expected event volume from monitoring / the SIEM sink.
```

If an external SIEM forwarder is configured, some of the "dropped" DB events may
still have been forwarded off-box (or vice-versa) — see `siem-events-dropped.md`.

## See also

- Metrics: `internal/audit/metrics.go`
  (`astronomer_audit_dropped_total`, `astronomer_audit_write_failures_total`)
- `db-pool-exhausted.md`, `postgres-disk-full.md`, `db-runtime-contention.md`
- `siem-events-dropped.md` (external forwarding loss)
