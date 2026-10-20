# Runbook — Failed schema migration

**Severity**: page (install or upgrade is broken)
**Component**: `internal/db/migrations` (goose, embedded in the server binary)

## How migrations run

The server applies every pending migration at boot, before it listens, under
the PostgreSQL session advisory lock `781930472366215`. Replicas starting at
once serialize on the lock; exactly one applies each migration. The worker
does not migrate — it waits (up to 10 minutes) for the schema to reach the
version its binary was built against, then starts. Migrations are
forward-only: there is no Down section and no roll-back command.

Bookkeeping lives in `public.goose_db_version` (`version_id`, `is_applied`,
`tstamp`). Transactional migrations roll back on failure; the 16 migrations
marked `-- +goose NO TRANSACTION` (`CREATE INDEX CONCURRENTLY`) do not.

## Symptoms

- Server pods in `CrashLoopBackOff`; `kubectl logs <server-pod>` ends with
  `database migration failed: migrations: apply: ... (type:sql,version:N)`.
- Worker pods logging `waiting for database schema` every 10 s and then
  exiting with `database schema not ready`.
- `/readyz` reporting `schema_version` with `applied schema version X <
  required Y` for longer than a migration takes (another replica holds the
  lock and is itself failing).

## Triage

1. Capture the failing version and the Postgres error from the server log.
2. Check the applied state:
   ```sql
   SELECT version_id, is_applied, tstamp FROM goose_db_version ORDER BY id DESC LIMIT 5;
   ```
3. Check for a stuck lock holder (a crashed replica's session normally
   releases it; a hung one does not):
   ```sql
   SELECT pid, state, query_start FROM pg_stat_activity
   WHERE pid IN (SELECT pid FROM pg_locks WHERE locktype = 'advisory' AND objid = 781930472366215);
   ```

## Recovery

- **Transactional migration failed** (the common case): the DDL rolled back.
  Fix the data condition it tripped on (a CHECK constraint, a NOT NULL
  backfill), or ship a corrected migration with a higher version, then
  restart the pod. Nothing to clean up.
- **`NO TRANSACTION` migration failed mid-way**: a `CREATE INDEX CONCURRENTLY`
  that fails leaves an `INVALID` index. Drop it
  (`DROP INDEX CONCURRENTLY <name>`) and restart the pod; the migration
  re-runs from the top.
- **Lock held by a hung session**: `SELECT pg_terminate_backend(<pid>)` and
  restart the server pod.
- **Apply ahead of a rollout** (optional): `server migrate -database "$DATABASE_URL"`
  from any machine that can reach the database, using the same image tag you
  are about to deploy.

## Prevention

- `make test-integration` applies the full embedded set to a fresh Postgres
  17 for every test package (pgtestdb template clone), so a migration that
  cannot apply never reaches a release.
