package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ExpectedSchemaVersion is the highest migration number the binary was built
// against. /readyz gates the pod out of Service rotation until the applied
// schema reaches at least this version (C-03), which closes the upgrade-window
// gap where new code serves traffic against the old schema because migrations
// run post-upgrade.
//
// It MUST equal max(internal/db/migrations/*.up.sql). TestExpectedSchemaVersionMatchesMigrations
// (schema_version_test.go) fails the build when a new migration is added without
// bumping this constant, so it can't silently drift.
const ExpectedSchemaVersion int64 = 138

// SchemaVersion returns the currently-applied golang-migrate schema version
// (the highest row in schema_migrations) along with that row's dirty flag. It
// reports version=0, dirty=false — not an error — when the table does not exist
// yet or is empty (a fresh, pre-migration install), so the readiness probe
// treats "not migrated yet" as not-ready rather than errored.
//
// golang-migrate writes (version=N, dirty=true) BEFORE running migration N and
// flips dirty=false only on success, so dirty=true means the latest migration is
// in progress or crashed mid-DDL. The readiness probe must hold the pod out of
// Service rotation in that state (it would otherwise serve traffic against a
// half-applied schema).
func (d *DB) SchemaVersion(ctx context.Context) (int64, bool, error) {
	if d == nil || d.pool == nil {
		return 0, false, nil
	}
	var version int64
	var dirty bool
	if err := d.pool.QueryRow(ctx, `SELECT version, dirty FROM schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&version, &dirty); err != nil {
		if isUndefinedTable(err) || errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return version, dirty, nil
}
