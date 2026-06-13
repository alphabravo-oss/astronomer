# Database Migration Safety Standard

Astronomer stores durable control-plane state in Postgres. Schema changes must
therefore be rollout-safe: old code, new code, workers, and background
reconcilers may overlap during deploys, restarts, and rollbacks.

## Required Pattern

Use expand/migrate/contract for every change that touches existing rows or a
runtime hot path.

1. Expand: add nullable columns, additive indexes, new tables, or new enum-like
   values without changing existing readers or writers.
2. Dual-read or dual-write: deploy code that can tolerate both the old and new
   shape. Prefer writing both locations before switching reads.
3. Backfill: run bounded batches outside request transactions. Each batch must
   be resumable and idempotent.
4. Contract: remove old columns, constraints, or code only after the previous
   release has fully rolled out and the backfill has converged.

## Review Checklist

- Migration works with the previous server and worker binaries still running.
- Migration works with the next server and worker binaries before every pod is
  updated.
- Large-table backfills are batched and restartable.
- `ALTER TABLE ... ADD COLUMN ... NOT NULL` includes a safe default or is split
  into nullable-add, backfill, validate, and set-not-null phases.
- New uniqueness or foreign-key constraints on existing data are introduced as
  not-valid/validated steps where Postgres supports that pattern.
- Indexes on large existing tables avoid long write-blocking locks. If
  `CREATE INDEX CONCURRENTLY` is required, keep it in a migration mode that does
  not wrap the statement in a transaction.
- Data rewrites for encrypted, JSONB, or large text columns run in online
  batches and record progress in the owning table or a durable operation row.
- Down migrations are either safe rollbacks or explicitly documented
  forward-fix stubs when destructive rollback would be riskier.
- The migration includes an operator note when it changes restore, backup,
  encryption-key, or CRD ownership semantics.

## Prohibited Patterns

- Blocking table rewrites on hot tables during normal deploy.
- Dropping a column in the same release that stops writing it.
- Reusing enum/status values with changed meaning.
- Depending on Redis queue state for migration completion.
- Hiding irreversible data loss inside a `.down.sql` migration.

## Validation

Run before opening a PR:

```bash
./scripts/check-migrations.sh
go test ./internal/db ./internal/server ./internal/worker/tasks
```

For high-risk migrations, also restore the latest production-like dump into a
clean database, run migrations forward, run `DB.SchemaHealth`, and execute the
management-plane restore drill.
