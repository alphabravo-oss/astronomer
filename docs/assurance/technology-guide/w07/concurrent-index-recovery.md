# Interrupted catalog chart index build

Migration073 has one `CREATE INDEX CONCURRENTLY` statement and deliberately omits `IF NOT EXISTS`. A duplicate name or interrupted invalid build is an error, not evidence that the required index is usable. Down removes only this additive index concurrently and does not roll back data or change the reversible schema floor66.

Before retrying an interrupted build, an operator must inspect the exact public index and its definition, `indisvalid`, `indisready`, migration version/dirty state and any active index-build session. The expected index belongs to `public.helm_charts` with `(name ASC, repository_id ASC, id ASC)` and no predicate. A same-name index with another definition, a currently active build, or an unexpected migration version requires investigation; do not drop or force it automatically.

```sql
SELECT n.nspname, c.relname, i.indisvalid, i.indisready,
       pg_get_indexdef(c.oid)
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
JOIN pg_catalog.pg_index i ON i.indexrelid = c.oid
WHERE n.nspname = 'public' AND c.relname = 'idx_helm_charts_keyset';
SELECT pid, phase, index_relid
FROM pg_catalog.pg_stat_progress_create_index
WHERE relid = 'public.helm_charts'::regclass;
```

Only after the inspected invalid object is confirmed to be this migration's interrupted build, under the operator's maintenance authorization, remove it with the exact `DROP INDEX CONCURRENTLY public.idx_helm_charts_keyset`. For a dirty073 state with no remaining073 object and verified complete schema72, the shipped migrator's explicit `force 72` command can reset the migration bookkeeping before `up` retries073. `force` does not execute rollback or repair SQL and must never be used to conceal unknown partial work. A valid exact index left by a completed build but dirty bookkeeping requires an explicit reviewed completion decision, not an automatic drop/rebuild or blind force. After retry/completion, require a clean73 version, the exact valid index definition and the actual indexed continuation plan before declaring recovery successful.

The owned fixture applied the real concurrent statement successfully. Interruption/recovery failure injection is still pending; this runbook is not a claimed recovery receipt.
