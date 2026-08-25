# Runbook — Failed schema migration

**Severity**: page (install is broken)
**Component**: `internal/db/migrations` + migrate init container

## Symptoms

- Server / worker pods stuck in `Init:Error` or `CrashLoopBackOff`
  with `astronomer-migrate` container reporting a SQL error
- `kubectl logs <pod> -c migrate` shows the failing migration filename
  and the Postgres error
- New install: nothing comes up. Upgrade: existing pods may still serve
  traffic on the old image until the new ReplicaSet finishes
  rolling-out

## Triage

1. **Capture the error**:
   ```bash
   kubectl -n astronomer logs <server-pod> -c migrate | tail -100
   ```
2. **Which version did we get to?**
   ```sql
   SELECT * FROM schema_migrations ORDER BY version DESC LIMIT 3;
   ```
   - `dirty=true` → migrate exited mid-statement; manual cleanup needed
     (see Recovery → "Resolve a dirty migration").
   - `dirty=false` but pod failing → migration ran but a follow-up
     change (e.g. a CHECK constraint on existing data) failed.
3. **Was it caught by `make check-migrations`?** Run locally against
   the suspect migration. The gate rejects blocking `ADD COLUMN ... NOT NULL`
   and destructive contract DDL without compatibility-window approval.

## Recovery

### Resolve a dirty migration

```sql
SELECT version, dirty FROM schema_migrations;
```

Compare live objects and data with the exact `.up.sql` and `.down.sql` files.
Restore the verified pre-upgrade backup if the resulting state is uncertain.
If all statements rolled back, reset to the last known-good version with the
release migration binary; do not edit `schema_migrations` directly:

```bash
# Compare live objects and data with the exact .up.sql and .down.sql files.
migrate -database "$DATABASE_URL" -path /migrations force <last-good-version>
# Restart the pods so the serialized migration entrypoint retries.
kubectl -n astronomer rollout restart deploy/astronomer-server
```

For a failure in the first migration, golang-migrate's no-version sentinel is
`-1`, not `0`. Never force a version merely to make the pod Ready: first prove
the database objects exactly match that version and retain the SQL inspection
and approval in the incident record.

### Roll back the migration (when an .up.sql is wrong and you have time)

Use the matching `.down.sql`:
```bash
# Inside the migrate container:
migrate -database "$DATABASE_URL" -path /migrations down 1
```
Then redeploy the prior chart version while you fix `.up.sql`.

### Skip a known-broken migration (last resort)

Use `migrate ... force <version>` only if you've manually run and verified the
equivalent SQL. Direct edits to `schema_migrations` are unsupported. Log the
deviation, query results, approver, and backup identity in the incident record.

## Verify

- All pods Ready
- `SELECT version, dirty FROM schema_migrations;` returns highest
  version with `dirty=false`
- A representative sqlc query against the schema works (e.g. `SELECT
  COUNT(*) FROM users;`)

## Prevention

- `make check-migrations` in CI (T30)
- Every `.up.sql` ships with a working `.down.sql`
- Destructive SQL is a contract-phase release after its declared compatibility
  window, never a same-release cleanup
- PostgreSQL 16/17 release fixture, concurrency, and interruption matrix in CI

## Related

- `internal/db/migrations/` — chronological migration files
- `scripts/check-migrations.sh` — T30 lint
- `deploy/chart/templates/server-deployment.yaml` — migrate init container
