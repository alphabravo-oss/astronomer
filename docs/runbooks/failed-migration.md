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
   the suspect migration (T30 lint catches `ADD COLUMN ... NOT NULL`
   without DEFAULT but not all unsafe patterns).

## Recovery

### Resolve a dirty migration

```bash
# Connect to Postgres
psql "$DATABASE_URL"
# Identify the version + state
SELECT version, dirty FROM schema_migrations;
# Manually finish or undo the half-applied statements based on
# inspecting the migration .up.sql file
# Then clear the dirty flag
UPDATE schema_migrations SET dirty = false WHERE version = <N>;
# Restart the pods so migrate retries (or skips if you set version=N+1)
kubectl -n astronomer rollout restart deploy/astronomer-server
```

### Roll back the migration (when an .up.sql is wrong and you have time)

Use the matching `.down.sql`:
```bash
# Inside the migrate container:
migrate -database "$DATABASE_URL" -path /migrations down 1
```
Then redeploy the prior chart version while you fix `.up.sql`.

### Skip a known-broken migration (last resort)

`UPDATE schema_migrations SET version = <next> WHERE version = <current>;`
**Only** if you've manually run the equivalent SQL yourself. Log the
deviation in the incident notes and add a follow-up to fix the
migration.

## Verify

- All pods Ready
- `SELECT version, dirty FROM schema_migrations;` returns highest
  version with `dirty=false`
- A representative sqlc query against the schema works (e.g. `SELECT
  COUNT(*) FROM users;`)

## Prevention

- `make check-migrations` in CI (T30)
- Every `.up.sql` ships with a working `.down.sql`
- For destructive changes, the staging deploy goes first

## Related

- `internal/db/migrations/` — chronological migration files
- `scripts/check-migrations.sh` — T30 lint
- `deploy/chart/templates/server-deployment.yaml` — migrate init container
