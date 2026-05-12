# Runbook — Backup restore drill failed / stale

**Alerts**: `AstronomerBackupRestoreDrillFailed`, `AstronomerBackupRestoreDrillStale`
**Severity**: critical (failed) / warning (stale)
**Component**: `deploy/chart/templates/management-plane-restore-drill-cronjob.yaml`

## Background

The weekly restore drill is the audit half of NIST CP-9 / ISO 27001
A.12.3.1: it proves the nightly `pg_dump` is actually restorable.

The CronJob (`astronomer-restore-drill`, default schedule `0 4 * * 1`)
runs a Job whose pod contains two containers:

- **Sidecar Postgres** (init container with `restartPolicy: Always`) —
  an ephemeral Postgres on `emptyDir`. Tears down with the pod.
- **Restore runner** — pulls the most recent backup from S3,
  `pg_restore`s it into the sidecar, runs a schema-sanity check
  (`schema_migrations.version >= expected`, `dirty=false`), and a
  row-count check on critical tables (`users`, `clusters`, `projects`,
  `audit_log`).

On every run — success **or** failure — the drill writes a row into the
production `backup_drill_results` table. That row, and only that row,
is the drill's contact with the production database. The admin endpoint
`GET /api/v1/admin/backup-drill/` reads it back.

## Symptoms

- `AstronomerBackupRestoreDrillFailed` fires (any Job failure in 24h)
- `AstronomerBackupRestoreDrillStale` fires (no success in 14d)
- Dashboard "Last successful restore drill" goes red or shows ">14d"

## Triage

### 1. Read the most recent drill row

```bash
TOKEN=...  # superuser JWT
curl -s -H "Authorization: Bearer $TOKEN" \
  "https://${HOST}/api/v1/admin/backup-drill/" | jq
```

The response includes `latest.status`, `latest.error_message`, and
`latest.backup_key`. The error message is the proximate cause.

### 2. Pull the failed pod's logs

```bash
kubectl -n astronomer get pods -l app.kubernetes.io/component=restore-drill \
  --sort-by=.metadata.creationTimestamp
# Grab the most recent failed pod
kubectl -n astronomer logs <pod>     # restore-runner container
kubectl -n astronomer logs <pod> -c postgres  # sidecar Postgres
```

The restore-runner logs each phase with a `DRILL FAILURE: <msg>` line on
exit. Common failure modes and their next-hop:

| Error from log                                           | Most likely cause                                    | Where to look next                                                              |
| -------------------------------------------------------- | ---------------------------------------------------- | ------------------------------------------------------------------------------- |
| `no backups found under s3://…/daily/`                   | Backup CronJob is not running, OR S3 prefix changed  | Check `astronomer-management-backup` CronJob logs                               |
| AWS error during `s3api list-objects-v2`                 | Credentials secret bad, or bucket ACL changed        | Verify `managementBackup.s3.credentialsSecretRef` secret + S3 bucket policy     |
| `pg_restore failed`                                      | Corrupted dump, or version mismatch (16 → older)     | Try `pg_restore --list` on the dump file locally; re-take a backup if corrupted |
| `schema_migrations is empty in restored DB`              | Backup taken BEFORE migrations ran (rare)            | Confirm against `schema_migrations` in production                               |
| `schema_migrations.dirty=true`                           | Production DB was mid-migration when backup taken    | Investigate why production was left dirty; complete or roll back the migration  |
| `schema_version N < expected min M`                      | Backup is stale (older than the rolling daily tier)  | Bump `managementBackup.retention.daily` or update `expectedMinSchemaVersion`    |
| `table X has zero rows in restored DB`                   | Critical table empty in production (possible!)       | Confirm against production; otherwise remove from `expectedTables`              |

### 3. Confirm S3 backup health

```bash
aws s3 ls s3://${BUCKET}/${PREFIX}/${RELEASE}/daily/ --recursive | sort | tail -5
```

The latest key should be ≤ 26h old. If the timestamps are stuck in the
past, the nightly `astronomer-management-backup` CronJob is broken —
escalate per `docs/management-plane-dr-runbook.md`.

## Recovery

### Re-run the drill manually

```bash
kubectl -n astronomer create job \
  --from=cronjob/astronomer-restore-drill \
  astronomer-restore-drill-manual-$(date +%s)
kubectl -n astronomer get jobs -l app.kubernetes.io/component=restore-drill \
  --sort-by=.metadata.creationTimestamp
```

If the manual rerun succeeds, the failure was transient (S3 rate limit,
pod scheduling glitch) and no further action is needed; the next
scheduled run will keep the cadence.

### Backup is genuinely unrestorable

If the rerun fails the same way, the backups themselves are not
restorable. This is the situation `docs/management-plane-dr-runbook.md`
exists for — escalate to the on-call DBA and treat as a P1 DR-readiness
incident.

Immediate compensating steps while the root cause is investigated:

1. Take a manual `pg_dump` of production and upload it OUT-OF-BAND
   (separate bucket, separate credentials) so you have at least one
   known-good artifact while triaging.
2. Tag the bad S3 objects so they're not GC'd by the retention prune.
3. Move the chart's `managementBackup.retention.daily` higher to keep
   more history available for the next drill attempt.

### Drill is failing because production schema is at version N but expectedMinSchemaVersion is N+1

`managementRestoreDrill.expectedMinSchemaVersion` was bumped in a chart
release but the migration that takes the DB to N+1 has not yet run.
Either:

- Run the migration job (`astronomer-migrate`) to advance the schema,
  then the next drill will pass.
- Roll back `expectedMinSchemaVersion` in your `values.yaml` override
  until you're ready to migrate.

## Verify

- `GET /api/v1/admin/backup-drill/` returns `latest.status == "success"`
  with a recent `started_at`.
- `kube_job_status_succeeded{job_name=~".*-restore-drill-.*"}` shows a
  recent successful Job.
- `AstronomerBackupRestoreDrillStale` clears within 1 hour.
- `AstronomerBackupRestoreDrillFailed` clears within the next 24h
  window once successful runs outweigh the failed one.

## Prevention

- Keep `managementRestoreDrill.expectedMinSchemaVersion` in step with
  the chart release notes. Don't let it lag behind production by more
  than one migration.
- Run the drill in staging on the same cadence — staging usually has a
  smaller backup, so failures surface faster than they do in prod.
- The drill respects `managementBackup.s3.credentialsSecretRef` — when
  rotating those credentials, rotate them in a way that overlaps so
  the drill doesn't hit a 24-hour blackout.

## Related

- [management-plane-dr-runbook.md](../management-plane-dr-runbook.md) — manual restore procedure
- `deploy/chart/templates/management-plane-restore-drill-cronjob.yaml`
- `internal/handler/admin_drill.go` — read-side endpoint
- `internal/db/migrations/041_backup_drill.up.sql` — status table schema
