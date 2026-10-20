# 0009 — goose migrations embedded in the server, applied at boot

- Status: accepted
- Date: 2026-10-20
- Guide: Technology Selection Guide ch.02 (goose, `embed.FS`, advisory lock, run before traffic)

## Context

Migrations were golang-migrate `NNN_<slug>.{up,down}.sql` pairs applied by a
separate `cmd/migrator` binary shipped as its own image and run as a Helm
hook Job plus an init container. The server only checked the applied
version at readiness. Image-tag skew between server and migrate images had
caused a production 503 (the "F1" guard existed to catch it).

## Decision

- Migrations are goose files `internal/db/migrations/NNNNN_<slug>.sql`
  (sequential, zero-padded; the numeric version equals the old migration
  number) with a single `-- +goose Up` block and no Down section
  (forward-only, per PLAN-UPDATE D-F). Each body is wrapped in
  `StatementBegin/End` so it executes as one multi-statement command, exactly
  as golang-migrate ran it; the 16 `CREATE INDEX CONCURRENTLY` migrations are
  `-- +goose NO TRANSACTION`.
- The set is embedded (`//go:embed *.sql`) and applied by `migrations.Up`
  through `goose.NewProvider` with a Postgres session locker on advisory lock
  `781930472366215`. `server.NewApp` runs it before anything else touches
  the schema; a failure is terminal. The worker waits for the schema instead.
- `db.ExpectedSchemaVersion` is derived from the embedded set at init.
- Deleted: `cmd/migrator`, `Dockerfile.migrate`, the migrate image, the Helm
  hook Job, the server init container, the F1 skew guard, `make migrate-down`.
  `server migrate` and `make migrate` remain for the dev loop.

## Consequences

- Equivalence was proven by applying both tool chains to empty Postgres 17.11
  databases and diffing `pg_dump --schema-only` (identical).
- Integration tests clone a template database migrated by the same `Up`
  (pgtestdb), so the production migrator is the one under test.
- The chart preflight reads `goose_db_version` instead of `schema_migrations`.
