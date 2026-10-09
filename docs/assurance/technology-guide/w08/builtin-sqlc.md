# W08 builtin fixed SQL

Fifteen builtin statements now live in `internal/db/queries/delivery_builtin_assets.sql`; `make sqlc-generate` produced the corresponding methods and the shared query interface. `Provisioner` uses generated methods with the same pool, checked-out connection, or transaction as each original statement. The private source/component interfaces receive only the transaction-bound implementation.

The session advisory lock remains on the acquired physical connection **before** Serializable begins. Cleanup keeps the existing five-second independent context: successful unlock releases that connection; failed unlock hijacks and closes it. The migration does not move this lock into the transaction. Rollback, commit, source identity checks, immutable version checks, target generation increments, and preview/planner audit intent ownership remain in their existing order.

The registration query uses a lateral latest-retry lookup with the same timestamp/step ordering. Because the repository's generated timestamp mapping is a nonnullable `time.Time`, the SQL explicitly returns retry presence alongside an internal placeholder timestamp; the adapter returns `nil` when absent. This preserves missing-retry behavior without changing shared generated model types or reading a second snapshot. Catalog source parameters are named to preserve the original unusual positional order. A target generation must be positive before conversion to its unsigned domain type.

Before migration, `TestPostgresBuiltinRegistrationRetryPresenceAndOrdering` passed against the original production implementation in the retained control worktree. It verifies nullable install baseline, absent retry, cluster/step filtering, latest timestamp ordering, inventory readiness and decommissioned-cluster errors. The preceding cancellation characterization also passed before movement: cleanup is bounded and asynchronous socket closure is allowed to release the server lock within that budget. The initial instantaneous lock-probe failure is retained as a fixture expectation correction, not a persistent product defect claim.

After migration, the full builtin package and its race run passed with an actual owned local PostgreSQL 16 database, with zero skips. The four DB parents are `TestProvisionerPostgresMultiSourceAssets`, `TestAutomaticScanningPostgres`, `TestPostgresBuiltinCanceledTransactionReleasesSessionLock`, and `TestPostgresBuiltinRegistrationRetryPresenceAndOrdering`. Existing fixtures exercise idempotency, parallel reconciliations, immutable identity conflicts, rollback, observation/retry behavior, and registration settings. Their isolated domain DDL is not a claim of all production FK/trigger coverage. Selected PostgreSQL 17/18 qualification and strict integration-harness inclusion are separate parent-owned receipts.

Commands and results:

- `BUILTIN_PROVISIONER_TEST_DATABASE_URL=<protected owned DSN> GOMAXPROCS=4 go test -p 2 -count=1 -v ./internal/delivery/builtin`: PASS ([log](builtin-after.log)).
- Same protected environment, `GOMAXPROCS=2 go test -race -p 1 -count=1 -v ./internal/delivery/builtin`: PASS ([log](builtin-race.log)).
- `GOMAXPROCS=4 make sqlc-generate` and `make sqlc-check`: PASS ([generation](builtin-sqlc-generate.log), [check](builtin-sqlc-check.log)).
- Pinned golangci-lint 2.14.0 on `./internal/delivery/builtin/...`: PASS, zero issues ([log](builtin-lint.log)).
- `GOMAXPROCS=4 go vet -p 2 ./internal/delivery/builtin`, dependency boundaries, complexity budget and `make data-governance-check`: PASS. The latter remains its existing canonical destructive-SQL scope; W08's broader runtime SQL gate is still being implemented.

This slice completes builtin fixed-query movement and its before/after characterization. Systemrollout and the broader SQL/deletion governance tasks remain open.
