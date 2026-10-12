# W08 system rollout fixed SQL

Twenty-three unique statements now live in `internal/db/queries/delivery_system_rollout.sql`; canonical sqlc generation produced the matching methods and shared interface. Pool-bound methods own ordinary reads and sweep discovery. Every mutation, global advisory transaction lock, row lock, assignment change, event and mandatory audit intent remains bound to the original Serializable transaction. The global lock is transaction scoped, unlike builtin's earlier session lock. Commit/rollback ownership stays with the service; generated methods never open a different connection.

Before SQL movement, four actual DB parents exposed and then verified the separate queued-cohort budget correction (baseline `cc481a34`, correction `42a8f8f2`). A fifth parent verifies a zero-row release promotion: revoking the draft before promotion returns `ErrConflict` and rolls back prior release retirement, rollout counters, fencing and completion events. All five pass against the corrected pre-sqlc source in an isolated control worktree ([pre-movement log](systemrollout-pre-sqlc.log)).

After movement, the full package and complete race run pass with an actual owned PostgreSQL 16 database and zero skips ([package](systemrollout-after.log), [race](systemrollout-race.log)). The five DB parents are:

- `TestPostgresSystemRolloutStartIdempotencyAndEligibility`
- `TestPostgresSystemRolloutAuditFailureRollsBackStartAndAction`
- `TestPostgresSystemRolloutConcurrentFenceHasOneWinner`
- `TestPostgresSystemRolloutCohortPromotionAndRollback`
- `TestPostgresSystemRolloutPromotionConflictRollsBackRetirement`

They exercise eligible managed-cluster filtering, idempotent operation/event/audit identity, active-rollout conflict, missing row, initial nullable timestamps, exact cohort assignment defaults, late mandatory audit failure with no partial creation/action, concurrent CAS with one winner, concurrency budget, successful release promotion and partial rollback completion. Concurrent Serializable conflict SQLSTATE 40001 is an existing permitted losing outcome; it is not masked by a retry. Test schemas clone migrated domain columns/checks/indexes/identity and use minimal cluster/token projections; they do not claim all production FKs/triggers. Parent-owned selected PostgreSQL 17/18 and strict mandatory integration-harness receipts remain separate qualification.

The generated record adapter preserves the public `View` fields, nullable UUID/timestamps and the canonical persisted-strategy integrity reader. `SELECT/RETURNING *` reuse the existing generated domain record internally; no extra record fields are exposed in the public view. The JSON reader ownership metadata names the new adapter. Aggregate counts use explicit SQL integer casts, matching the prior int32 scanning domain without unchecked Go narrowing. Planned cluster/cohort/order counts are checked before int32 conversion. Released affected-row counts are checked against the positive requested limit before conversion. Promotion still requires exactly one affected row. Named action parameters preserve an absent completion timestamp through `sqlc.narg` and `pgtype.Timestamptz`, with no global type override.

`GOMAXPROCS=4 make sqlc-generate`, `make sqlc-check`, pinned golangci-lint 2.14.0 (zero issues), scoped `go vet`, dependency boundaries, complexity and `make data-governance-check` pass ([generation](systemrollout-sqlc-generate.log), [check](systemrollout-sqlc-check.log), [lint](systemrollout-lint.log)). The broader SQL/deletion governance gate remains a separate unfinished W08 slice. Canonical movement is complete for the four selected services; this does not complete W08's governance tasks.
